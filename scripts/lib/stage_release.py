#!/usr/bin/env python3
"""Stage a certified candidate as a GitHub draft prerelease, and promote it without rebuilding.

    stage_release.py stage   --tag TAG --candidate OUT_DIR --evidence-bundle DIR --certification-dir DIR --review FILE --notes FILE
                             [--catalog ci/phase-gates.json] [--signers FILE] [--repo DIR] [--policy FILE]
    stage_release.py promote --tag TAG --candidate-lock-digest sha256:HEX
                             [--catalog ci/phase-gates.json] [--signers FILE] [--repo DIR] [--policy FILE]

OUT_DIR is the builder's output directory (the candidate archive, its detached checksum and build-record.json).
Every input is verified locally before anything is sent: the signed tag, the candidate graph and checksum, the
whole signed evidence bundle (exact gate set, fresh, ordered), the signed certification, and a signed release
review by a *different* enrolled principal than the certifier. The authenticated GitHub login must be on the
reviewed publisher allowlist and the target repository must be the allowlisted one. Staging creates a draft
prerelease and uploads each uniquely named asset without overwrite, then downloads them back and re-verifies
the bytes. Promotion downloads the draft's assets, runs the same verification against the exact candidate-lock
digest it was given, and only then flips draft to published (still a prerelease). Nothing in either path
builds, installs dependencies, signs, renames or uploads during promotion.
"""

import argparse
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.dont_write_bytecode = True
sys.path.insert(0, str(HERE))
import build_release as br  # noqa: E402
import evidence_bundle as eb  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

EXIT_NOT_RUN = 3
POLICY_PATH = REPO / "release" / "publisher-policy.json"
NOTES_SECTIONS = ("reference server", "browser", "desktop", "platform-trusted https", "unsigned", "checksum", "signature",
                  "known limitations", "recovery point", "recovery time", "upgrade")
PRIVATE_KEY = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")


class StageError(Exception):
    """A refusal; the message is safe to print."""


class Unavailable(StageError):
    """A reviewed trust input does not exist yet (maps to not_run)."""


def fail(message):
    raise StageError(message)


def asset_names(version):
    return {
        "archive": f"telos-{version}-candidate.tar.zst",
        "checksum": f"telos-{version}-candidate.tar.zst.sha256",
        "record": "build-record.json",
        "bundle": "evidence-bundle.tar.zst",
        "certification": "certification.json",
        "certification-signature": "certification.json.sig",
        "review": "release-review.json",
        "review-signature": "release-review.json.sig",
        "notes": "RELEASE-NOTES.md",
    }


# ------------------------------------------------------------------ policy and gh

def load_policy(path):
    path = Path(path)
    if not path.is_file():
        raise Unavailable("release/publisher-policy.json does not exist")
    document = json.loads(path.read_text(encoding="utf-8"))
    if set(document) - {"schemaVersion", "githubRepository", "allowedLogins", "note"} or document.get("schemaVersion") != 1:
        fail("release/publisher-policy.json has unexpected fields")
    repository, logins = document.get("githubRepository"), document.get("allowedLogins")
    if not repository or not logins:
        raise Unavailable("release/publisher-policy.json names no reviewed repository and publisher login")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        fail("the publisher policy repository is not owner/name")
    if not isinstance(logins, list) or len(set(logins)) != len(logins) or not all(isinstance(l, str) and l for l in logins):
        fail("the publisher policy logins must be a non-empty unique list")
    return repository, logins


class Gh:
    def __init__(self, command):
        self.command = command

    def run(self, *arguments, check=True):
        try:
            completed = subprocess.run([self.command, *arguments], capture_output=True, text=True, check=False, timeout=900)
        except FileNotFoundError:
            raise Unavailable("the GitHub CLI (gh) is not installed")
        if check and completed.returncode != 0:
            fail(f"gh {' '.join(arguments[:2])} failed: {(completed.stderr or completed.stdout).strip()[-300:]}")
        return completed


def check_publisher(gh, repository, logins):
    login = gh.run("api", "user", "--jq", ".login").stdout.strip()
    if login not in logins:
        fail(f"the authenticated GitHub user {login!r} is not on the reviewed publisher allowlist")
    actual = gh.run("repo", "view", repository, "--json", "nameWithOwner", "--jq", ".nameWithOwner").stdout.strip()
    if actual != repository:
        fail("the repository gh resolves is not the reviewed one")
    return login


# ------------------------------------------------------------------ verification of an asset directory

def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def tar_bundle(bundle, destination, epoch=0):
    """A deterministic tar.zst of an evidence bundle (sorted, root-owned, fixed times)."""
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.GNU_FORMAT) as archive:
        for path in sorted(Path(bundle).rglob("*")):
            info = tarfile.TarInfo(path.relative_to(bundle).as_posix() + ("/" if path.is_dir() else ""))
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = epoch
            if path.is_dir():
                info.type, info.mode = tarfile.DIRTYPE, 0o555
                archive.addfile(info)
            else:
                data = path.read_bytes()
                info.size, info.mode = len(data), 0o444
                archive.addfile(info, io.BytesIO(data))
    raw = Path(str(destination)[: -len(".zst")])
    raw.write_bytes(buffer.getvalue())
    subprocess.run(["zstd", "-q", "-19", "-T1", "--no-check", "-f", "-o", str(destination), str(raw)], check=True)
    raw.unlink()


def extract_tar_zst(archive, destination):
    raw = Path(destination).parent / (Path(archive).name + ".tar")
    try:
        subprocess.run(["zstd", "-q", "-d", "-f", "-o", str(raw), str(archive)], check=True, capture_output=True)
        with tarfile.open(raw) as tar:
            tar.extractall(destination, filter="data")
    except (subprocess.CalledProcessError, tarfile.TarError, OSError) as error:
        fail(f"{Path(archive).name} cannot be unpacked: {type(error).__name__}")
    finally:
        raw.unlink(missing_ok=True)


def check_notes(path):
    text = Path(path).read_text(encoding="utf-8").lower()
    if len(text.strip()) < 200:
        fail("the release notes are too short to carry the required statements")
    missing = [section for section in NOTES_SECTIONS if section not in text]
    if missing:
        fail(f"the release notes do not state: {', '.join(missing)}")
    if PRIVATE_KEY.search(text):
        fail("the release notes contain key material")


def verify_assets(directory, tag, catalog, signers, repo, expected_digest=None, now=None):
    """Verify a directory holding exactly the release assets. Returns {version, lockDigest, assets: {name: sha256}}."""
    directory = Path(directory)
    present = {p.name for p in directory.iterdir()}
    if any(p.is_symlink() or not p.is_file() for p in directory.iterdir()):
        fail("the asset directory holds something other than regular files")
    match = re.fullmatch(r"v([0-9]+\.[0-9]+\.[0-9]+-beta\.[1-9][0-9]*)", tag)
    if not match:
        fail("the tag is not a beta tag")
    version = match.group(1)
    names = asset_names(version)
    missing = sorted(set(names.values()) - present)
    if missing:
        fail(f"release asset missing: {missing[0]}")
    extra = sorted(present - set(names.values()))
    if extra:
        fail(f"unexpected release asset: {extra[0]}")
    for name in names.values():
        if PRIVATE_KEY.search((directory / name).read_bytes().decode("utf-8", errors="ignore")[:1 << 20]):
            fail(f"{name} contains key material")
    recorded = (directory / names["checksum"]).read_text(encoding="utf-8").split()
    if len(recorded) != 2 or recorded[1] != names["archive"] or recorded[0] != sha256_file(directory / names["archive"]):
        fail("the candidate archive does not match its detached checksum")
    with tempfile.TemporaryDirectory(prefix="telos-verify-") as scratch:
        scratch = Path(scratch)
        candidate = scratch / "candidate"
        candidate.mkdir()
        extract_tar_zst(directory / names["archive"], candidate)
        try:
            graph = rg.verify_root(candidate)
        except rg.GraphError as error:
            fail(f"the candidate graph does not verify: {error}")
        lock_digest = graph["candidateLockDigest"]
        if expected_digest and expected_digest != lock_digest:
            fail("the candidate is not the one the exact digest input names")
        if graph["version"] != version:
            fail("the candidate version is not the tag's version")
        manifest = rg.load_record("release-manifest", rg.read_regular(candidate, "release-manifest.json"))
        if manifest["sourceTag"] != tag:
            fail("the candidate was built for another tag")
        record = json.loads((directory / names["record"]).read_text(encoding="utf-8"))
        if record.get("recordKind") != "candidate" or record.get("candidateLockDigest") != lock_digest or record.get("archiveSha256") != recorded[0]:
            fail("the build record does not describe this candidate archive (or is a rehearsal)")
        try:
            eb.verify_candidate(candidate, eb.Context(candidate / "candidate-lock.json", catalog), repo)
        except eb.Unavailable as error:
            raise Unavailable(str(error))
        except eb.BundleError as error:
            fail(str(error))
        bundle = scratch / "bundle"
        bundle.mkdir()
        extract_tar_zst(directory / names["bundle"], bundle)
        certification_bytes = (directory / names["certification"]).read_bytes()
        try:
            certification = rg.load_record("certification", certification_bytes)
        except rg.GraphError as error:
            fail(f"the certification is invalid: {error}")
        if certification["candidateLockDigest"] != lock_digest:
            fail("the certification is for another candidate")
        try:
            verified = eb.verify_bundle(candidate / "candidate-lock.json", catalog, bundle, signers, certification["signerPrincipal"], now)
            fingerprint = signing.verify_signature(signers, certification["signerPrincipal"], signing.NAMESPACES["certification"],
                                                   certification_bytes, (directory / names["certification-signature"]).read_bytes())
            eb.verify_subjects(candidate, verified["context"], bundle)
        except (eb.BundleError, signing.SigningError) as error:
            fail(f"the evidence or certification does not verify: {error}")
        finally:
            eb.make_writable(bundle)
        if fingerprint != certification["signerFingerprint"] or verified["signerFingerprint"] != certification["signerFingerprint"]:
            fail("the certification and the evidence were not signed by the same key")
        if certification["evidenceIndexDigest"] != verified["evidenceIndexDigest"] or certification["requiredGates"] != verified["envelopes"]:
            fail("the certification does not describe this evidence bundle")
        review_bytes = (directory / names["review"]).read_bytes()
        try:
            review = rg.load_record("release-review", review_bytes)
        except rg.GraphError as error:
            fail(f"the release review is invalid: {error}")
        if review["reviewerPrincipal"] == certification["signerPrincipal"]:
            fail("the reviewer must be a different principal than the certifier")
        for key, expected in (("candidateLockDigest", lock_digest), ("certificationDigest", rg.digest_bytes(certification_bytes)),
                              ("evidenceIndexDigest", verified["evidenceIndexDigest"])):
            if review[key] != expected:
                fail(f"the release review's {key} is not this release's")
        if eb.parse_stamp(review["reviewedAt"]) < eb.parse_stamp(certification["certifiedAt"]):
            fail("the release review predates the certification")
        try:
            signing.verify_signature(signers, review["reviewerPrincipal"], signing.NAMESPACES["review"], review_bytes,
                                     (directory / names["review-signature"]).read_bytes())
        except signing.SigningError as error:
            fail(f"the release review is not signed by an enrolled reviewer: {error}")
    check_notes(directory / names["notes"])
    return {"version": version, "lockDigest": lock_digest, "assets": {name: sha256_file(directory / name) for name in sorted(names.values())}}


# ------------------------------------------------------------------ staging

def assemble_assets(destination, tag, candidate_out, bundle, certification_dir, review, notes):
    version = tag[1:]
    names = asset_names(version)
    candidate_out, certification_dir = Path(candidate_out), Path(certification_dir)
    sources = {
        "archive": candidate_out / names["archive"], "checksum": candidate_out / names["checksum"], "record": candidate_out / names["record"],
        "certification": certification_dir / "certification.json", "certification-signature": certification_dir / "certification.json.sig",
        "review": Path(review), "review-signature": Path(str(review) + ".sig"), "notes": Path(notes),
    }
    for key, source in sources.items():
        if source.is_symlink() or not source.is_file():
            fail(f"{names[key]} is missing from the supplied inputs")
        shutil.copyfile(source, Path(destination) / names[key])
    tar_bundle(bundle, Path(destination) / names["bundle"])
    return names


def refuse_key_material(*directories):
    """No private key may sit in any directory a release is assembled from."""
    for directory in directories:
        for path in sorted(Path(directory).rglob("*")):
            if path.is_symlink():
                fail(f"{path.name} is a symbolic link")
            if path.is_file() and path.stat().st_size <= 1 << 20 and PRIVATE_KEY.search(path.read_bytes().decode("utf-8", errors="ignore")):
                fail(f"{path.name} contains private key material; remove it before staging")


def stage(args):
    refuse_key_material(args.candidate, args.evidence_bundle, args.certification_dir)
    repository, logins = load_policy(args.policy)
    gh = Gh(args.gh)
    check_publisher(gh, repository, logins)
    existing = gh.run("release", "view", args.tag, "--repo", repository, check=False)
    if existing.returncode == 0:
        fail(f"a release for {args.tag} already exists; staging never reuses or replaces one")
    with tempfile.TemporaryDirectory(prefix="telos-stage-") as scratch:
        stage_dir = Path(scratch) / "assets"
        stage_dir.mkdir()
        assemble_assets(stage_dir, args.tag, args.candidate, args.evidence_bundle, args.certification_dir, args.review, args.notes)
        verified = verify_assets(stage_dir, args.tag, args.catalog, args.signers, args.repo)
        gh.run("release", "create", args.tag, "--repo", repository, "--verify-tag", "--draft", "--prerelease",
               "--title", f"Telos {args.tag}", "--notes-file", str(stage_dir / asset_names(args.tag[1:])["notes"]))
        for name in sorted(verified["assets"]):
            gh.run("release", "upload", args.tag, "--repo", repository, str(stage_dir / name))
        download = Path(scratch) / "download"
        download.mkdir()
        gh.run("release", "download", args.tag, "--repo", repository, "--dir", str(download), "--pattern", "*")
        names = set(verified["assets"])
        returned = {p.name for p in download.iterdir()}
        if returned != names:
            fail(f"the draft's assets differ from what was uploaded (recoverable: inspect the draft {args.tag})")
        for name in sorted(returned):
            if sha256_file(download / name) != verified["assets"][name]:
                fail(f"{name} changed in transit (recoverable: inspect the draft {args.tag})")
    return {"tag": args.tag, "candidateLockDigest": verified["lockDigest"], "state": "draft-prerelease", "assets": len(verified["assets"])}


# ------------------------------------------------------------------ promotion

def promote(args):
    repository, logins = load_policy(args.policy)
    gh = Gh(args.gh)
    check_publisher(gh, repository, logins)
    view = json.loads(gh.run("release", "view", args.tag, "--repo", repository, "--json", "isDraft,isPrerelease,assets,tagName").stdout)
    if view.get("tagName") != args.tag:
        fail("gh returned another release than the requested tag")
    if not view["isDraft"]:
        fail("the release is already published; promotion only changes a draft")
    if not view["isPrerelease"]:
        fail("the draft is not marked as a prerelease")
    with tempfile.TemporaryDirectory(prefix="telos-promote-") as scratch:
        download = Path(scratch) / "assets"
        download.mkdir()
        gh.run("release", "download", args.tag, "--repo", repository, "--dir", str(download), "--pattern", "*")
        verified = verify_assets(download, args.tag, args.catalog, args.signers, args.repo, expected_digest=args.candidate_lock_digest)
        listed = {a["name"] for a in view["assets"]}
        if listed != set(verified["assets"]):
            fail("the draft's asset list is not the verified inventory")
        if getattr(args, "verify_only", False):
            return {"tag": args.tag, "candidateLockDigest": verified["lockDigest"], "state": "verified-draft"}
        gh.run("release", "edit", args.tag, "--repo", repository, "--draft=false", "--prerelease=true")
        after = json.loads(gh.run("release", "view", args.tag, "--repo", repository, "--json", "isDraft,isPrerelease,assets").stdout)
        if after["isDraft"] or not after["isPrerelease"]:
            fail("the release did not end up published as a prerelease")
        sizes = {name: (download / name).stat().st_size for name in verified["assets"]}
        for asset in after["assets"]:
            if asset["name"] not in sizes or asset.get("size", sizes[asset["name"]]) != sizes[asset["name"]]:
                fail(f"published asset {asset['name']} differs from the verified draft")
            digest = asset.get("digest")
            if digest and digest != "sha256:" + verified["assets"][asset["name"]]:
                fail(f"published asset {asset['name']} has another digest than the verified draft")
        if {a["name"] for a in after["assets"]} != set(verified["assets"]):
            fail("the published asset list differs from the verified draft")
    return {"tag": args.tag, "candidateLockDigest": verified["lockDigest"], "state": "published-prerelease"}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("stage", "promote"):
        sub = commands.add_parser(name)
        sub.add_argument("--tag", required=True)
        sub.add_argument("--catalog", default=str(REPO / "ci" / "phase-gates.json"))
        sub.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
        sub.add_argument("--repo", default=str(REPO))
        sub.add_argument("--policy", default=str(POLICY_PATH))
        sub.add_argument("--gh", default=os.environ.get("TELOS_GH", "gh"))
        if name == "stage":
            sub.add_argument("--candidate", required=True)
            sub.add_argument("--evidence-bundle", required=True)
            sub.add_argument("--certification-dir", required=True)
            sub.add_argument("--review", required=True)
            sub.add_argument("--notes", required=True)
        else:
            sub.add_argument("--candidate-lock-digest", required=True)
            sub.add_argument("--verify-only", action="store_true", help="verify the draft and stop before publishing")
    args = parser.parse_args(argv)
    try:
        if args.command == "promote" and not re.fullmatch(r"sha256:[0-9a-f]{64}", args.candidate_lock_digest):
            fail("--candidate-lock-digest must be sha256: followed by 64 hex digits")
        result = stage(args) if args.command == "stage" else promote(args)
        print(json.dumps(result, sort_keys=True))
        return 0
    except (Unavailable, eb.Unavailable) as error:
        print(f"not_run: {error}", file=sys.stderr)
        return EXIT_NOT_RUN
    except (StageError, eb.BundleError, br.BuildError, rg.GraphError, signing.SigningError, OSError, KeyError, ValueError,
            subprocess.CalledProcessError, json.JSONDecodeError) as error:
        print(f"stage-release: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
