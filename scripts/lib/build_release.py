#!/usr/bin/env python3
"""Assemble one immutable standalone candidate from an exact source commit.

    build_release.py build --version X.Y.Z-beta.N --tag vX.Y.Z-beta.N --out-dir ABS
                           [--desktop-dir DIR] [--rehearsal --source-commit SHA]
                           [--verify-reproducible] [--tools-dir DIR]
    build_release.py compare --a CANDIDATE_DIR --b CANDIDATE_DIR

Order of work (every step fails closed; nothing is built from the working tree):

  1. resolve the source: a signed annotated tag on a clean checkout (candidate), or an explicit
     commit marked as a rehearsal -- a rehearsal can never become a candidate;
  2. export exactly that commit with `git archive`, verify the version declarations and image lock;
  3. build the images Telos owns (core, client, egress proxy) with locked bases and a fixed
     timestamp, archive them as OCI archives; archive each third-party platform image byte for byte
     with skopeo --preserve-digests so its digest is the upstream digest the lock records;
  4. verify and take over the native desktop artifact (never executed);
  5. assemble the generation: the operational tree, a derived build-free Compose model, images.env
     and the candidate-independent generation.json;
  6. index the content, write artifact-bound SBOMs, then artifact index -> release manifest ->
     candidate lock -> SHA256SUMS, and verify the whole graph;
  7. package a normalized tar.zst and its detached SHA-256; optionally build a second time in a
     different directory without cache and require identical graph documents.

The candidate lock digest does not exist until step 6; nothing built earlier mentions it.
"""

import argparse
import contextlib
import datetime
import hashlib
import io
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import release_compose as rc  # noqa: E402
import release_graph as rg  # noqa: E402

REPO = HERE.parents[1]
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+-beta\.[1-9][0-9]*\Z")
COMMIT = re.compile(r"[0-9a-f]{40}\Z")
EXIT_NOT_RUN = 3

# service -> (build context relative to the export, Dockerfile relative to the export, images.env variable)
BUILD_PLAN = {
    "telos-core": (".", "backend/Dockerfile", "TELOS_CORE_IMAGE"),
    "telos-client": ("frontend", "Dockerfile", "TELOS_CLIENT_IMAGE"),
    "telos-egress-proxy": (".", "deploy/egress-proxy/Dockerfile", "TELOS_EGRESS_PROXY_IMAGE"),
}
# What a generation carries: tracked operational files only.
GENERATION_TOP_FILES = ("telos", "install.sh", "uninstall.sh", "LICENSE", "NOTICE", "CREDITS.md", "README.md", ".env.example",
                        "docker-compose.private-ca.yml")
GENERATION_TREES = ("cli", "config", "deploy", "scripts", "release", "documentation", "backend/db/migrations")
GENERATION_SKIP_PARTS = {"__pycache__", "tests", "ci", "node_modules"}
GENERATION_SKIP_NAMES = {"Dockerfile"}
FORBIDDEN_NAME = re.compile(r"(^|/)(\.env(\..*)?|\.telos|\.git(ignore|attributes)?|id_(rsa|ed25519).*|.*\.(pem|key|p12|pfx))$", re.I)
GENERATED = ("docker-compose.yml", "docker-compose.setup.yml", "docker-compose.release.yml", "images.env", "generation.json")
SECRET_TEXT = re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}")


class BuildError(Exception):
    """A refusal; the message is safe to print."""


class Unavailable(BuildError):
    """An input the build needs does not exist (maps to not_run, exit 3)."""


def fail(message):
    raise BuildError(message)


# ------------------------------------------------------------------ process helpers

class Tools:
    """The external programs, replaceable in tests through TELOS_BUILD_<NAME> variables."""

    def __init__(self, env=None):
        env = os.environ if env is None else env
        self.git = env.get("TELOS_BUILD_GIT", "git")
        self.podman = env.get("TELOS_BUILD_PODMAN", "podman")
        self.skopeo = env.get("TELOS_BUILD_SKOPEO", "skopeo")
        self.zstd = env.get("TELOS_BUILD_ZSTD", "zstd")
        self.python = sys.executable


def build_env(epoch, extra=None):
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": os.environ.get("HOME", "/tmp"), "LC_ALL": "C", "TZ": "UTC",
           "SOURCE_DATE_EPOCH": str(epoch)}
    for name, value in os.environ.items():
        # TELOS_BUILD_* are the test seams for the stand-in tools; the rest let podman and skopeo find their runtime and credentials.
        if name.startswith("TELOS_BUILD_") or name in ("XDG_RUNTIME_DIR", "REGISTRY_AUTH_FILE", "TMPDIR"):
            env[name] = value
    env.update(extra or {})
    return env


def run(argv, *, cwd=None, env=None, timeout=7200, what=None):
    try:
        completed = subprocess.run([str(a) for a in argv], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout, check=False)
    except FileNotFoundError as error:
        raise Unavailable(f"{what or argv[0]} is not installed: {error.filename}")
    except subprocess.TimeoutExpired:
        fail(f"{what or argv[0]} timed out after {timeout} s")
    if completed.returncode != 0:
        tail = (completed.stderr or completed.stdout or "").strip().splitlines()[-6:]
        fail(f"{what or argv[0]} failed ({completed.returncode}): " + " | ".join(tail)[:600])
    return completed


# ------------------------------------------------------------------ source

class Source:
    def __init__(self, commit, epoch, tag, version, rehearsal):
        self.commit, self.epoch, self.tag, self.version, self.rehearsal = commit, epoch, tag, version, rehearsal


def resolve_source(repo, tools, version, tag, rehearsal, source_commit):
    if not VERSION.match(version):
        fail(f"{version!r} is not a beta version (X.Y.Z-beta.N)")
    tag = tag or f"v{version}"
    if tag != f"v{version}":
        fail(f"tag {tag} does not match version {version}")
    if rehearsal:
        if not source_commit or not COMMIT.match(source_commit):
            fail("a rehearsal build needs --source-commit with the full 40-character commit")
        probe = subprocess.run([tools.git, "-C", str(repo), "cat-file", "-e", source_commit + "^{commit}"], capture_output=True, check=False)
        if probe.returncode != 0:
            fail(f"commit {source_commit[:12]} is not in this repository")
        commit = source_commit
    else:
        if source_commit:
            fail("--source-commit is only for a rehearsal; a candidate is built from its signed tag")
        kind = subprocess.run([tools.git, "-C", str(repo), "cat-file", "-t", f"refs/tags/{tag}"], capture_output=True, text=True, check=False)
        if kind.returncode != 0:
            fail(f"tag {tag} does not exist")
        if kind.stdout.strip() != "tag":
            fail(f"tag {tag} is lightweight; a candidate needs a signed annotated tag")
        signers = repo / "release" / "telos-tag-signers"
        entries = [line for line in signers.read_text(encoding="utf-8").splitlines() if line.strip() and not line.lstrip().startswith("#")] \
            if signers.is_file() else []
        if not entries:
            raise Unavailable("release/telos-tag-signers enrolls no signer, so no tag can be verified; a candidate cannot be built")
        verified = subprocess.run([tools.git, "-C", str(repo), "-c", "gpg.format=ssh", "-c", f"gpg.ssh.allowedSignersFile={signers}",
                                   "tag", "-v", tag], capture_output=True, text=True, check=False)
        if verified.returncode != 0:
            fail(f"tag {tag} is not signed by an enrolled signer: {verified.stderr.strip()[-200:]}")
        commit = run([tools.git, "-C", repo, "rev-parse", f"refs/tags/{tag}^{{commit}}"], what="git rev-parse").stdout.strip()
        head = run([tools.git, "-C", repo, "rev-parse", "HEAD"], what="git rev-parse").stdout.strip()
        if head != commit:
            fail(f"HEAD ({head[:12]}) is not the tagged commit ({commit[:12]}); check out the tag")
        dirty = run([tools.git, "-C", repo, "status", "--porcelain", "--untracked-files=normal"], what="git status").stdout.strip()
        if dirty:
            fail("the working tree is not clean; a candidate is built from a clean checkout of its tag")
    epoch = int(run([tools.git, "-C", repo, "log", "-1", "--format=%ct", commit], what="git log").stdout.strip())
    return Source(commit, epoch, tag, version, rehearsal)


def export_source(repo, tools, source, work):
    """`git archive` the commit; return (tar path, extracted tree)."""
    tarball = work / f"telos-{source.version}-source.tar"
    with open(tarball, "wb") as handle:
        completed = subprocess.run([tools.git, "-C", str(repo), "archive", "--format=tar", f"--prefix=telos-{source.version}/", source.commit],
                                   stdout=handle, stderr=subprocess.PIPE, check=False)
    if completed.returncode != 0:
        fail(f"git archive failed: {completed.stderr.decode(errors='replace')[-200:]}")
    tree = work / "src"
    tree.mkdir()
    with tarfile.open(tarball) as archive:
        for member in archive.getmembers():
            if member.name == f"telos-{source.version}":
                continue
            if not member.name.startswith(f"telos-{source.version}/") or member.islnk():
                fail(f"unexpected path in the source export: {member.name}")
            member.name = member.name[len(f"telos-{source.version}/"):]
            if member.name.startswith("/") or ".." in Path(member.name).parts:
                fail(f"unsafe path in the source export: {member.name}")
            archive.extract(member, tree, filter="tar")
    return tarball, tree


def check_versions(tree, version):
    declared = {}
    declared["frontend/package.json"] = json.loads((tree / "frontend/package.json").read_text(encoding="utf-8")).get("version")
    declared["frontend/src-tauri/tauri.conf.json"] = json.loads((tree / "frontend/src-tauri/tauri.conf.json").read_text(encoding="utf-8")).get("version")
    match = re.search(r'^version\s*=\s*"([^"]+)"', (tree / "frontend/src-tauri/Cargo.toml").read_text(encoding="utf-8"), re.M)
    declared["frontend/src-tauri/Cargo.toml"] = match.group(1) if match else None
    for name, value in declared.items():
        if value != version:
            fail(f"{name} declares version {value!r}, not {version}")


# ------------------------------------------------------------------ OCI archives

def read_oci_archive(path):
    """Verify an OCI archive end to end; return {manifestDigest, config, labels, layers}."""
    with tarfile.open(path) as archive:
        members = {m.name: m for m in archive.getmembers() if m.isfile()}
        if "index.json" not in members or "oci-layout" not in members:
            fail(f"{path.name} is not an OCI archive")
        index = json.loads(archive.extractfile(members["index.json"]).read())
        manifests = index.get("manifests") or []
        if len(manifests) != 1:
            fail(f"{path.name} must hold exactly one image, found {len(manifests)}")
        descriptor = manifests[0]
        if descriptor.get("mediaType") not in ("application/vnd.oci.image.manifest.v1+json",):
            fail(f"{path.name} holds a {descriptor.get('mediaType')}, not a single OCI image manifest")

        def blob(digest):
            algorithm, _, hexdigest = digest.partition(":")
            if algorithm != "sha256" or not re.fullmatch(r"[0-9a-f]{64}", hexdigest):
                fail(f"{path.name}: unsupported digest {digest}")
            member = members.get(f"blobs/sha256/{hexdigest}")
            if member is None:
                fail(f"{path.name}: blob {hexdigest[:12]} is missing")
            sha = hashlib.sha256()
            handle = archive.extractfile(member)
            for chunk in iter(lambda: handle.read(1 << 20), b""):
                sha.update(chunk)
            if sha.hexdigest() != hexdigest:
                fail(f"{path.name}: blob {hexdigest[:12]} does not hash to its name")
            return member

        manifest_member = blob(descriptor["digest"])
        manifest = json.loads(archive.extractfile(manifest_member).read())
        config_member = blob(manifest["config"]["digest"])
        config = json.loads(archive.extractfile(config_member).read())
        for layer in manifest.get("layers") or []:
            blob(layer["digest"])
    return {"manifestDigest": descriptor["digest"], "labels": (config.get("config") or {}).get("Labels") or {},
            "architecture": config.get("architecture"), "os": config.get("os"), "user": (config.get("config") or {}).get("User"),
            "refName": (descriptor.get("annotations") or {}).get("org.opencontainers.image.ref.name")}


def build_images(tools, source, tree, lock_dir, no_cache, existing=None):
    """podman build + save for the images Telos owns. Returns {service: archive info}."""
    results = {}
    env = build_env(source.epoch)
    for service, (context, dockerfile, variable) in BUILD_PLAN.items():
        tag = f"localhost/{service}:{source.version}"
        argv = [tools.podman, "build", "--timestamp", str(source.epoch), "--pull=missing", "--format", "oci", "-t", tag,
                "--label", f"org.opencontainers.image.version={source.version}",
                "--label", f"org.opencontainers.image.revision={source.commit}",
                "--label", f"org.opencontainers.image.source=telos-{source.version}-source.tar",
                "-f", str(tree / context / dockerfile)]
        if no_cache:
            argv.append("--no-cache")
        argv.append(str(tree / context))
        run(argv, cwd=tree, env=env, what=f"podman build {service}")
        archive = lock_dir / f"{service}.oci.tar"
        run([tools.podman, "save", "--format", "oci-archive", "-o", archive, tag], env=env, what=f"podman save {service}")
        info = read_oci_archive(archive)
        labels = info["labels"]
        if labels.get("org.opencontainers.image.version") != source.version or labels.get("org.opencontainers.image.revision") != source.commit:
            fail(f"{service}: the image does not carry this version and source commit as labels")
        if info["os"] != "linux" or info["architecture"] != "amd64":
            fail(f"{service}: built for {info['os']}/{info['architecture']}, not linux/amd64")
        info["path"], info["variable"], info["repository"] = archive, variable, f"localhost/{service}"
        results[service] = info
    return results


def third_party_images(tree, lock):
    """The locked platform images the derived Compose model runs, as {lock name: (repository, source, platform digest)}."""
    index = rc.lock_index(lock)
    wanted = {}
    for name in rc.BASE_FILES:
        for line in (tree / name).read_text(encoding="utf-8").splitlines():
            match = rc.IMAGE_LINE.match(line)
            if match:
                source = match.group(2).split("@", 1)[0]
                if source in index:
                    repository, platform, lock_name = index[source]
                    wanted[lock_name] = (repository, source, platform)
    return wanted


def archive_third_party(tools, source, wanted, lock_dir):
    results = {}
    env = build_env(source.epoch)
    for name, (repository, source_ref, platform) in sorted(wanted.items()):
        archive = lock_dir / f"{name}.oci.tar"
        reference = source_ref
        run([tools.skopeo, "copy", "--preserve-digests", "--override-os", "linux", "--override-arch", "amd64",
             f"docker://{repository}@{platform}", f"oci-archive:{archive}:{reference}"], env=env, what=f"skopeo copy {name}")
        info = read_oci_archive(archive)
        if info["manifestDigest"] != platform:
            fail(f"{name}: the archived manifest {info['manifestDigest'][:19]} is not the locked platform digest {platform[:19]}")
        info["path"], info["repository"] = archive, repository
        results[name] = info
    return results


# ------------------------------------------------------------------ desktop

def take_over_desktop(tools, source, desktop_dir, tree):
    """Verify the native build and return its (artifact, identity) paths."""
    desktop_dir = Path(desktop_dir)
    images = sorted(desktop_dir.glob("*.AppImage"))
    if len(images) != 1:
        fail(f"{desktop_dir} must contain exactly one .AppImage, found {len(images)}")
    image = images[0]
    identity = Path(str(image) + ".identity.json")
    if image.is_symlink() or not identity.is_file() or identity.is_symlink():
        fail("the AppImage needs its regular .identity.json sidecar beside it")
    record = rg.load_record("artifact-identity", identity.read_bytes(), canonical=False)
    if record["sourceCommit"] != source.commit:
        fail("the desktop artifact was built from a different commit")
    if record["version"] != source.version:
        fail("the desktop artifact was built for a different version")
    completed = subprocess.run([tools.python, str(tree / "scripts" / "verify-desktop-artifact.py"), "--platform", "linux",
                                "--artifact", str(image), "--identity", str(identity)], capture_output=True, text=True, check=False)
    if completed.returncode == EXIT_NOT_RUN:
        raise Unavailable(completed.stderr.strip()[-200:] or "the desktop artifact could not be inspected")
    if completed.returncode != 0:
        fail("the desktop artifact was rejected: " + completed.stderr.strip()[-200:])
    return image, identity


# ------------------------------------------------------------------ generation

def migrations_of(tree):
    directory = tree / "backend" / "db" / "migrations"
    files = sorted(p for p in directory.iterdir() if re.fullmatch(r"[0-9]{4}_.+\.sql", p.name))
    if not files:
        fail("the source has no migrations")
    numbers = [int(p.name[:4]) for p in files]
    if numbers != list(range(1, len(numbers) + 1)):
        fail("the migration sequence is not contiguous from 0001")
    entries = [{"name": p.name, "sha256": rg.raw_sha256(p.read_bytes())} for p in files]
    return numbers[-1], rg.digest_bytes(rg.canonical_bytes(entries)), entries


def generation_files(tree):
    """Relative paths of the tracked operational files a generation carries."""
    chosen = []
    for name in GENERATION_TOP_FILES:
        if (tree / name).is_file():
            chosen.append(name)
    for top in GENERATION_TREES:
        base = tree / top
        if not base.is_dir():
            continue
        for path in sorted(base.rglob("*")):
            relative = path.relative_to(tree).as_posix()
            parts = set(Path(relative).parts)
            if path.is_dir():
                continue
            if path.is_symlink():
                fail(f"{relative} is a symbolic link; a generation carries none")
            if parts & GENERATION_SKIP_PARTS or path.name in GENERATION_SKIP_NAMES or relative.endswith(".pyc"):
                continue
            if FORBIDDEN_NAME.search(relative):
                fail(f"{relative} must not be shipped in a generation")
            chosen.append(relative)
    return chosen


def read_compat(tree):
    path = tree / "release" / "generation-compat.json"
    if not path.is_file():
        fail("release/generation-compat.json is missing")
    document = json.loads(path.read_text(encoding="utf-8"))
    if set(document) - {"schemaVersion", "minSchema", "rollbackCompatibleThrough", "note"} or document.get("schemaVersion") != 1:
        fail("release/generation-compat.json has unexpected fields")
    for key in ("minSchema", "rollbackCompatibleThrough"):
        if not isinstance(document.get(key), int) or isinstance(document.get(key), bool) or document[key] < 0:
            fail(f"release/generation-compat.json: {key} must be a natural number")
    return document


def predecessor_ids(tree, override):
    if override is not None:
        return list(override)
    policy = json.loads((tree / "release" / "predecessor-policy.json").read_text(encoding="utf-8"))
    predecessor = policy.get("predecessor")
    return [predecessor["candidateLockDigest"]] if predecessor else []


def assemble_generation(tree, root, source, images, derived_dir, rollback_through, predecessors, compat):
    """Write <root>/generation/** and return the generation.json document."""
    generation = root / "generation"
    generation.mkdir(mode=0o755)
    secret_scan = []
    for relative in generation_files(tree):
        if relative in GENERATED:
            continue
        data = (tree / relative).read_bytes()
        if SECRET_TEXT.search(data.decode("utf-8", errors="ignore")):
            secret_scan.append(relative)
        target = generation / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        executable = (tree / relative).stat().st_mode & 0o111
        os.chmod(target, 0o755 if executable else 0o644)
    if secret_scan:
        fail("secret-looking material in " + ", ".join(secret_scan[:3]))
    for name in (*rc.BASE_FILES, rc.OVERLAY):
        shutil.copyfile(derived_dir / name, generation / name)
        os.chmod(generation / name, 0o644)
    variables = {}
    for service, info in images.items():
        if service in BUILD_PLAN:
            variables[BUILD_PLAN[service][2]] = f"{info['repository']}@{info['manifestDigest']}"
    if sorted(variables) != sorted(plan[2] for plan in BUILD_PLAN.values()):
        fail("an image variable is missing for a built service")
    images_env = "".join(f"{name}={variables[name]}\n" for name in sorted(variables))
    (generation / "images.env").write_text(images_env, encoding="utf-8")
    os.chmod(generation / "images.env", 0o600)
    current, checksum_digest, _ = migrations_of(tree)
    document = {
        "schemaVersion": 1, "version": source.version, "sourceCommit": source.commit,
        "images": {name: info["manifestDigest"] for name, info in sorted(images.items())},
        "migrations": {"min": min(compat["minSchema"], current), "current": current, "max": current, "checksumSetDigest": checksum_digest},
        "predecessorIds": sorted(set(predecessors)),
        "rollbackCompatibleThrough": rollback_through if rollback_through is not None else compat["rollbackCompatibleThrough"],
        "artifactIndexSubset": [],
    }
    for path in sorted(generation.rglob("*")):
        if path.is_file():
            document["artifactIndexSubset"].append({"path": path.relative_to(generation).as_posix(), "sha256": rg.raw_sha256(path.read_bytes())})
    rg.validate_record("generation-manifest", document)
    (generation / "generation.json").write_bytes(rg.canonical_bytes(document))
    os.chmod(generation / "generation.json", 0o644)
    for directory in [generation, *[p for p in generation.rglob("*") if p.is_dir()]]:
        os.chmod(directory, 0o755)
    return document


# ------------------------------------------------------------------ SBOM, index, graph

def canonicalize_sbom(path, epoch, artifact_sha):
    """Make a Syft SPDX document reproducible: fixed creation time and a namespace derived from the artifact."""
    document = json.loads(Path(path).read_bytes())
    created = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    document.setdefault("creationInfo", {})["created"] = created
    document["documentNamespace"] = f"https://telos.invalid/spdx/{artifact_sha}"
    Path(path).write_bytes(rg.canonical_bytes(document))


def descriptor_for(relative, images_by_path):
    if relative.startswith("source/"):
        return {"kind": "source", "platform": "any", "contentDigest": None}
    if relative.startswith("images/"):
        return {"kind": "oci-image", "platform": "linux/amd64", "contentDigest": images_by_path[relative]}
    if relative.startswith("desktop/"):
        return {"kind": "desktop", "platform": "linux-x86_64", "contentDigest": None}
    if relative.startswith("generation/"):
        return {"kind": "generation", "platform": "any", "contentDigest": None}
    if relative.startswith("sbom/"):
        return {"kind": "sbom", "platform": "any", "contentDigest": None}
    fail(f"{relative} has no artifact kind")


def payload_descriptors(root, images_by_path, include_sbom):
    descriptors = []
    for path in sorted(p for p in root.rglob("*") if p.is_file()):
        relative = path.relative_to(root).as_posix()
        if relative in rg.GRAPH_DOCUMENTS.values() or relative == rg.CHECKSUM_FILE:
            continue
        if relative.startswith("sbom/") and not include_sbom:
            continue
        descriptors.append({"path": relative, **descriptor_for(relative, images_by_path)})
    return {"artifacts": descriptors}


def default_sbom_generator(index_path, root, out_dir, tools_dir):
    import supply_chain as sc   # imported lazily: only the real builder needs the locked scanners
    return sc.generate_sbom(index_path, root, out_dir, tools_dir)


def write_graph(root, source, tree, images, sbom_generator, tools_dir, work):
    images_by_path = {f"images/{Path(i['path']).name}": i["manifestDigest"] for i in images.values()}
    # 1. content index, before any SBOM exists
    pre = rg.build_index(root, payload_descriptors(root, images_by_path, include_sbom=False))
    pre_path = work / "payload-index.json"
    pre_path.write_bytes(rg.canonical_bytes(pre))
    # 2. artifact-bound SBOMs
    sbom_dir = root / "sbom"
    sbom_generator(pre_path, root, sbom_dir, tools_dir)
    for spdx in sorted(sbom_dir.glob("*.spdx.json")):
        canonicalize_sbom(spdx, source.epoch, spdx.name.split(".")[0])
    # the SBOM index records the digests of the (now canonical) SBOM bytes
    index_file = sbom_dir / "sbom-index.json"
    if index_file.is_file():
        document = json.loads(index_file.read_bytes())
        for entry in document["entries"]:
            if entry.get("sbom"):
                entry["sbom"]["sha256"] = rg.raw_sha256((sbom_dir / entry["sbom"]["path"]).read_bytes())
        index_file.write_bytes(rg.canonical_bytes(document))
    # 3. the graph
    index = rg.build_index(root, payload_descriptors(root, images_by_path, include_sbom=True))
    rg.write_atomic_exclusive(root / rg.GRAPH_DOCUMENTS["artifact-index"], rg.canonical_bytes(index))
    _, migrations_digest, _ = migrations_of(tree)
    metadata = {
        "version": source.version, "sourceCommit": source.commit, "sourceTag": source.tag, "sourceDateEpoch": source.epoch,
        "imagesLockDigest": rg.digest_bytes((tree / "release" / "images.lock").read_bytes()),
        "migrationManifestDigest": migrations_digest,
        "buildInputs": {
            "goSum": rg.digest_bytes((tree / "backend" / "go.sum").read_bytes()),
            "npmLock": rg.digest_bytes((tree / "frontend" / "package-lock.json").read_bytes()),
            "cargoLock": rg.digest_bytes((tree / "frontend" / "src-tauri" / "Cargo.lock").read_bytes()),
        },
    }
    manifest = rg.build_manifest(root, metadata)
    rg.write_atomic_exclusive(root / rg.GRAPH_DOCUMENTS["release-manifest"], rg.canonical_bytes(manifest))
    lock = rg.build_candidate_lock(root)
    rg.write_atomic_exclusive(root / rg.GRAPH_DOCUMENTS["candidate-lock"], rg.canonical_bytes(lock))
    rg.write_atomic_exclusive(root / rg.CHECKSUM_FILE, rg.checksum_lines(root), 0o644)
    return rg.verify_root(root)


# ------------------------------------------------------------------ packaging

def normalized_mode(path):
    return 0o755 if path.stat().st_mode & 0o111 else 0o644


def package(tools, root, epoch, out_file):
    """Deterministic tar (sorted, root-owned, fixed times) compressed with a single-threaded zstd."""
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.GNU_FORMAT) as archive:
        names = sorted(p for p in root.rglob("*"))
        for path in names:
            relative = path.relative_to(root).as_posix()
            info = tarfile.TarInfo(relative + ("/" if path.is_dir() else ""))
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = epoch
            if path.is_dir():
                info.type, info.mode = tarfile.DIRTYPE, 0o755
                archive.addfile(info)
            else:
                data = path.read_bytes()
                info.size = len(data)
                info.mode = 0o600 if relative == "generation/images.env" else normalized_mode(path)
                archive.addfile(info, io.BytesIO(data))
    raw = out_file.with_suffix("")   # *.tar
    raw.write_bytes(buffer.getvalue())
    run([tools.zstd, "-q", "-19", "-T1", "--no-check", "-f", "-o", out_file, raw], what="zstd")
    raw.unlink()
    digest = rg.raw_sha256(out_file.read_bytes())
    Path(str(out_file) + ".sha256").write_text(f"{digest}  {out_file.name}\n", encoding="utf-8")
    return digest


def extract_package(tools, archive, destination):
    raw = Path(destination).parent / "verify.tar"
    run([tools.zstd, "-q", "-d", "-f", "-o", raw, archive], what="zstd -d")
    with tarfile.open(raw) as tar:
        tar.extractall(destination, filter="data")
    raw.unlink()


# ------------------------------------------------------------------ the build

def build(args, tools=None, sbom_generator=None, repo=None):
    tools = tools or Tools()
    repo = Path(repo or REPO)
    out_dir = Path(args.out_dir)
    if not out_dir.is_absolute():
        fail("--out-dir must be an absolute path")
    with contextlib.suppress(ValueError):
        out_dir.resolve().relative_to(repo.resolve())
        fail("--out-dir must not be inside the repository")
    if out_dir.exists() and any(out_dir.iterdir()):
        fail("--out-dir must be empty or absent")
    source = resolve_source(repo, tools, args.version, args.tag, args.rehearsal, args.source_commit)
    out_dir.mkdir(parents=True, exist_ok=True, mode=0o755)
    os.umask(0o022)
    with tempfile.TemporaryDirectory(prefix="telos-build-", dir=str(out_dir.parent)) as scratch:
        work = Path(scratch)
        results = []
        passes = 2 if args.verify_reproducible else 1
        for number in range(passes):
            attempt = work / f"pass-{number}"
            attempt.mkdir()
            results.append(build_once(args, tools, sbom_generator, source, attempt, no_cache=bool(number)))
        if passes == 2:
            differences = compare_candidates(results[0]["root"], results[1]["root"])
            if differences:
                fail("the two builds differ: " + "; ".join(differences[:6]))
        chosen = results[0]
        final_root = out_dir / "candidate"
        shutil.move(str(chosen["root"]), str(final_root))
        archive = out_dir / f"telos-{source.version}-candidate.tar.zst"
        archive_digest = package(tools, final_root, source.epoch, archive)
        verify_dir = work / "verify" / "candidate"
        verify_dir.parent.mkdir()
        extract_package(tools, archive, verify_dir)
        verified = rg.verify_root(verify_dir)
        if verified["candidateLockDigest"] != chosen["graph"]["candidateLockDigest"]:
            fail("the packaged candidate does not verify to the lock that was built")
    record = {"schemaVersion": 1, "recordKind": "rehearsal" if source.rehearsal else "candidate", "version": source.version,
              "sourceCommit": source.commit, "sourceTag": source.tag, "candidateLockDigest": verified["candidateLockDigest"],
              "archive": archive.name, "archiveSha256": archive_digest, "reproducibilityVerified": passes == 2}
    rg.write_atomic_exclusive(out_dir / "build-record.json", rg.canonical_bytes(record), 0o644)
    return record


def build_once(args, tools, sbom_generator, source, work, no_cache):
    lock_dir_root = work / "candidate"
    lock_dir_root.mkdir(mode=0o755)
    for sub in ("source", "images", "desktop"):
        (lock_dir_root / sub).mkdir(mode=0o755)
    tarball, tree = export_source(args_repo(args), tools, source, work)
    shutil.copyfile(tarball, lock_dir_root / "source" / tarball.name)
    check_versions(tree, source.version)
    run([tools.python, tree / "scripts" / "verify-image-lock.py", "--source"], cwd=tree, env=build_env(source.epoch), what="the image lock check")
    lock = rc.load_lock(tree / "release" / "images.lock")
    derived = work / "derived"
    rc.derive(tree, derived, lock)
    problems = rc.verify(derived, lock)
    if problems:
        fail("the derived Compose model is not a complete no-build model: " + problems[0])
    images = build_images(tools, source, tree, lock_dir_root / "images", no_cache)
    images.update(archive_third_party(tools, source, third_party_images(tree, lock), lock_dir_root / "images"))
    if args.desktop_dir:
        image, identity = take_over_desktop(tools, source, args.desktop_dir, tree)
        shutil.copyfile(image, lock_dir_root / "desktop" / image.name)
        shutil.copyfile(identity, lock_dir_root / "desktop" / identity.name)
    elif not args.rehearsal:
        fail("a candidate needs the native desktop artifact (--desktop-dir)")
    else:
        (lock_dir_root / "desktop").rmdir()
    compat = read_compat(tree)
    assemble_generation(tree, lock_dir_root, source, images, derived, args.rollback_compatible_through,
                        predecessor_ids(tree, args.predecessor_id), compat)
    generator = sbom_generator or default_sbom_generator
    tools_dir = args.tools_dir or os.environ.get("TELOS_CI_TOOLS_DIR") or str(Path.home() / ".cache" / "telos-ci-tools")
    graph = write_graph(lock_dir_root, source, tree, images, lambda p, r, o, t: generator(p, r, o, t), tools_dir, work)
    return {"root": lock_dir_root, "graph": graph}


def args_repo(args):
    return Path(getattr(args, "repo", None) or REPO)


# ------------------------------------------------------------------ reproducibility

def compare_candidates(a, b):
    """Differences between two candidate roots, by name; empty means byte-identical."""
    def listing(root):
        return {p.relative_to(root).as_posix(): rg.raw_sha256(p.read_bytes()) for p in sorted(Path(root).rglob("*")) if p.is_file()}
    left, right = listing(a), listing(b)
    problems = []
    for name in sorted(set(left) | set(right)):
        if name not in left:
            problems.append(f"{name} only in the second build")
        elif name not in right:
            problems.append(f"{name} only in the first build")
        elif left[name] != right[name]:
            problems.append(f"{name} differs")
    return problems


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    build_parser = commands.add_parser("build")
    build_parser.add_argument("--version", required=True)
    build_parser.add_argument("--tag")
    build_parser.add_argument("--out-dir", required=True)
    build_parser.add_argument("--desktop-dir")
    build_parser.add_argument("--rehearsal", action="store_true")
    build_parser.add_argument("--source-commit")
    build_parser.add_argument("--verify-reproducible", action="store_true")
    build_parser.add_argument("--tools-dir")
    build_parser.add_argument("--rollback-compatible-through", type=int)
    build_parser.add_argument("--predecessor-id", action="append")
    build_parser.add_argument("--repo")
    compare = commands.add_parser("compare")
    compare.add_argument("--a", required=True)
    compare.add_argument("--b", required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "build":
            record = build(args)
            print(json.dumps(record, sort_keys=True))
            return 0
        problems = compare_candidates(args.a, args.b)
        for problem in problems:
            print(f"build-release: {problem}", file=sys.stderr)
        return 1 if problems else 0
    except Unavailable as error:
        print(f"not_run: {error}", file=sys.stderr)
        return EXIT_NOT_RUN
    except (BuildError, rg.GraphError, rc.ComposeError, OSError, KeyError, ValueError) as error:
        print(f"build-release: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
