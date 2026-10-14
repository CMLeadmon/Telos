#!/usr/bin/env python3
"""Staging a draft prerelease and promoting it without rebuilding, against a fake GitHub CLI.

A real signed candidate-shaped set (signed tag, graph, archive, signed evidence bundle, signed certification, a
review by a *different* principal) is staged and promoted; then one dimension at a time is broken. The fake `gh`
keeps releases and asset bytes in a state directory and refuses what the real one would (clobber, duplicate tag)."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
sys.path.insert(0, str(HERE.parents[1] / "scripts" / "lib"))
import build_release as br  # noqa: E402
import evidence_bundle as eb  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402
import stage_release as sr  # noqa: E402
from evidence_bundle_test import BundleFixture, PRINCIPAL, keygen  # noqa: E402

TAG = "v0.1.0-beta.1"
REVIEWER = "reviewer@example.test"
FAKE_GH = textwrap.dedent('''\
    #!/usr/bin/env python3
    import json, os, shutil, sys
    state_dir = os.environ["FAKE_GH_STATE"]
    os.makedirs(state_dir, exist_ok=True)
    state_file = os.path.join(state_dir, "releases.json")
    state = json.load(open(state_file)) if os.path.exists(state_file) else {}
    log = open(os.path.join(state_dir, "calls.log"), "a")
    args = sys.argv[1:]
    log.write(" ".join(args) + "\\n"); log.close()
    def save():
        json.dump(state, open(state_file, "w"))
    def assets_dir(tag):
        path = os.path.join(state_dir, "assets", tag)
        os.makedirs(path, exist_ok=True)
        return path
    login = os.environ.get("FAKE_GH_LOGIN", "publisher")
    if args[:2] == ["api", "user"]:
        print(login)
    elif args[:2] == ["repo", "view"]:
        print(os.environ.get("FAKE_GH_REPO", "owner/telos"))
    elif args[:2] == ["release", "view"]:
        tag = args[2]
        if tag not in state:
            sys.exit("release not found")
        release = state[tag]
        listing = []
        for name in sorted(os.listdir(assets_dir(tag))):
            listing.append({"name": name, "size": os.path.getsize(os.path.join(assets_dir(tag), name))})
        print(json.dumps({"tagName": tag, "isDraft": release["draft"], "isPrerelease": release["prerelease"], "assets": listing}))
    elif args[:2] == ["release", "create"]:
        tag = args[2]
        for required in ("--verify-tag", "--draft", "--prerelease"):
            if required not in args:
                sys.exit("create must pass " + required)
        if tag in state:
            sys.exit("release already exists")
        state[tag] = {"draft": True, "prerelease": True}
        save()
    elif args[:2] == ["release", "upload"]:
        tag = args[2]
        if "--clobber" in args:
            sys.exit("clobber is not allowed")
        if tag not in state:
            sys.exit("no such release")
        for path in [a for a in args[3:] if not a.startswith("--") and os.path.exists(a)]:
            target = os.path.join(assets_dir(tag), os.path.basename(path))
            if os.path.exists(target):
                sys.exit("asset already exists: " + os.path.basename(path))
            shutil.copyfile(path, target)
    elif args[:2] == ["release", "download"]:
        tag = args[2]
        directory = args[args.index("--dir") + 1]
        for name in os.listdir(assets_dir(tag)):
            if name == os.environ.get("FAKE_GH_DROP"):
                continue
            shutil.copyfile(os.path.join(assets_dir(tag), name), os.path.join(directory, name))
            if name == os.environ.get("FAKE_GH_TAMPER"):
                with open(os.path.join(directory, name), "ab") as handle:
                    handle.write(b"tampered in transit")
    elif args[:2] == ["release", "edit"]:
        tag = args[2]
        if "--draft=false" in args:
            state[tag]["draft"] = False
        if "--prerelease=true" in args:
            state[tag]["prerelease"] = True
        save()
    else:
        sys.exit("fake gh: unsupported " + " ".join(args))
    ''')
NOTES = """\
# Telos 0.1.0-beta.1 (unsigned invite-only beta)

Reference server: Ubuntu 24.04 or newer, x86-64, rootful Podman 5. Browser clients: Chrome, Firefox, Edge, desktop Safari, iOS Safari,
and Android Chrome, plus the Linux desktop application. Only platform-trusted HTTPS is supported.

The Linux desktop packages are unsigned; verify the checksum (`sha256sum -c`) and the signature (`ssh-keygen -Y verify`) first.

Known limitations: no voice, no push notifications, no high availability. Backups: a recovery point objective of at most 24
hours and a recovery time objective of at most 4 hours. Upgrade: install the next candidate and run the upgrade command.
"""


class StageFixture(BundleFixture):
    def setUp(self):
        super().setUp()
        self.reviewer_key, self.reviewer_public = keygen(self.shared, f"reviewer-{self.id().split('.')[-1]}")
        self.signers_path = self.dir / "signers"
        namespaces = ",".join(signing.NAMESPACES[k] for k in ("evidence", "certification", "review", "guided"))
        self.signers_path.write_text(
            f'{PRINCIPAL} namespaces="{namespaces}" {self.public[0]} {self.public[1]}\n'
            f'{REVIEWER} namespaces="{namespaces}" {self.reviewer_public[0]} {self.reviewer_public[1]}\n')
        self.signers = self.signers_path
        self.bundle = self.signed()
        self.certification = self.dir / "certification"
        self.certify(out="certification")
        self.out = self.dir / "build-out"
        self.out.mkdir()
        archive = self.out / f"telos-0.1.0-beta.1-candidate.tar.zst"
        digest = br.package(br.Tools(), self.candidate, 1787234400, archive)
        record = {"schemaVersion": 1, "recordKind": "candidate", "version": "0.1.0-beta.1", "sourceCommit": self.commit, "sourceTag": TAG,
                  "candidateLockDigest": rg.digest_bytes(self.lock.read_bytes()), "archive": archive.name, "archiveSha256": digest,
                  "reproducibilityVerified": True}
        (self.out / "build-record.json").write_bytes(rg.canonical_bytes(record))
        self.notes = self.dir / "RELEASE-NOTES.md"
        self.notes.write_text(NOTES)
        self.review = self.dir / "release-review.json"
        self.write_review()
        self.gh = self.dir / "gh"
        self.gh.write_text(FAKE_GH)
        os.chmod(self.gh, 0o755)
        self.state = self.dir / "gh-state"
        self.policy = self.dir / "publisher-policy.json"
        self.policy.write_text(json.dumps({"schemaVersion": 1, "githubRepository": "owner/telos", "allowedLogins": ["publisher"]}))
        self.env = {"FAKE_GH_STATE": str(self.state)}
        self.old_env = {k: os.environ.get(k) for k in ("FAKE_GH_STATE", "FAKE_GH_LOGIN", "FAKE_GH_REPO", "FAKE_GH_TAMPER", "FAKE_GH_DROP")}
        os.environ["FAKE_GH_STATE"] = str(self.state)
        self.addCleanup(self.restore_env)

    def restore_env(self):
        for key, value in self.old_env.items():
            os.environ.pop(key, None) if value is None else os.environ.__setitem__(key, value)

    def write_review(self, reviewer_key=None, **changes):
        certification_bytes = (self.certification / "certification.json").read_bytes()
        certification = json.loads(certification_bytes)
        record = {"schemaVersion": 1, "candidateLockDigest": certification["candidateLockDigest"], "certificationDigest": rg.digest_bytes(certification_bytes),
                  "evidenceIndexDigest": certification["evidenceIndexDigest"], "decision": "approved", "reviewerPrincipal": REVIEWER,
                  "reviewedAt": eb.stamp(eb.now_utc())}
        record.update(changes)
        data = rg.canonical_bytes(record)
        self.review.write_bytes(data)
        Path(str(self.review) + ".sig").write_bytes(signing.sign_file(reviewer_key or self.reviewer_key, signing.NAMESPACES["review"], self.review))

    def arguments(self, command="stage", **overrides):
        values = {"tag": TAG, "catalog": str(self.catalog_path), "signers": str(self.signers_path), "repo": str(self.repo), "policy": str(self.policy),
                  "gh": str(self.gh), "candidate": str(self.out), "evidence_bundle": str(self.bundle), "certification_dir": str(self.certification),
                  "review": str(self.review), "notes": str(self.notes), "candidate_lock_digest": rg.digest_bytes(self.lock.read_bytes())}
        values.update(overrides)
        return type("Args", (), values)()

    def stage(self, **overrides):
        return sr.stage(self.arguments(**overrides))

    def promote(self, **overrides):
        return sr.promote(self.arguments(**overrides))

    def calls(self):
        path = self.state / "calls.log"
        return path.read_text().splitlines() if path.exists() else []

    def release_state(self):
        path = self.state / "releases.json"
        return json.loads(path.read_text()) if path.exists() else {}


class HappyPathTests(StageFixture):
    def test_stage_creates_a_draft_prerelease_and_uploads_without_overwrite(self):
        result = self.stage()
        self.assertEqual(result["state"], "draft-prerelease")
        self.assertEqual(self.release_state()[TAG], {"draft": True, "prerelease": True})
        calls = self.calls()
        self.assertTrue(any(c.startswith("release create") and "--verify-tag" in c and "--draft" in c and "--prerelease" in c for c in calls))
        uploads = [c for c in calls if c.startswith("release upload")]
        self.assertEqual(len(uploads), 9)
        self.assertFalse(any("--clobber" in c for c in calls))
        names = set(os.listdir(self.state / "assets" / TAG))
        self.assertEqual(names, set(sr.asset_names("0.1.0-beta.1").values()))

    def test_promotion_flips_only_the_draft_state(self):
        self.stage()
        before = {n: (self.state / "assets" / TAG / n).read_bytes() for n in os.listdir(self.state / "assets" / TAG)}
        result = self.promote()
        self.assertEqual(result["state"], "published-prerelease")
        self.assertEqual(self.release_state()[TAG], {"draft": False, "prerelease": True})
        after = {n: (self.state / "assets" / TAG / n).read_bytes() for n in os.listdir(self.state / "assets" / TAG)}
        self.assertEqual(before, after, "promotion must not change an asset byte")
        edits = [c for c in self.calls() if c.startswith("release edit")]
        self.assertEqual(len(edits), 1)
        self.assertIn("--draft=false", edits[0])
        self.assertIn("--prerelease=true", edits[0])
        later = [c for c in self.calls()[self.calls().index(edits[0]) + 1:] if c.startswith("release upload") or c.startswith("release create")]
        self.assertEqual(later, [])


class StageRefusalTests(StageFixture):
    def refuses(self, fragment, **overrides):
        with self.assertRaisesRegex((sr.StageError, eb.BundleError), fragment):
            self.stage(**overrides)
        self.assertNotIn(TAG, self.release_state(), "a refused staging must create no release")

    def test_an_unreviewed_policy_is_not_run(self):
        self.policy.write_text(json.dumps({"schemaVersion": 1, "githubRepository": None, "allowedLogins": []}))
        with self.assertRaises(sr.Unavailable):
            self.stage()
        self.assertEqual(self.calls(), [], "nothing is sent while the publisher policy is unreviewed")

    def test_a_login_that_is_not_on_the_allowlist(self):
        os.environ["FAKE_GH_LOGIN"] = "someone-else"
        self.refuses("not on the reviewed publisher allowlist")

    def test_another_repository_than_the_reviewed_one(self):
        os.environ["FAKE_GH_REPO"] = "attacker/telos"
        self.refuses("not the reviewed one")

    def test_a_nonexistent_or_unsigned_tag(self):
        git = lambda *a: subprocess.run(["git", "-C", str(self.repo), *a], check=True, capture_output=True, text=True)
        git("tag", "-d", TAG)
        try:
            self.refuses("tag")
        finally:
            git("-c", "gpg.format=ssh", "-c", f"user.signingkey={self.tag_key}", "tag", "-s", TAG, "-m", "release")

    def test_a_version_that_is_not_the_tags(self):
        self.refuses("candidate version is not the tag|built for another tag|missing", tag="v0.1.0-beta.2")

    def test_an_existing_release_is_never_reused(self):
        self.stage()
        with self.assertRaisesRegex(sr.StageError, "already exists"):
            self.stage()

    def test_a_missing_certification(self):
        (self.certification / "certification.json.sig").unlink()
        self.refuses("missing from the supplied inputs")

    def test_a_missing_review_signature(self):
        Path(str(self.review) + ".sig").unlink()
        self.refuses("missing from the supplied inputs")

    def test_a_checksum_that_does_not_match(self):
        archive = self.out / "telos-0.1.0-beta.1-candidate.tar.zst"
        archive.write_bytes(archive.read_bytes() + b"x")
        self.refuses("detached checksum")

    def test_a_private_key_in_an_input_directory(self):
        (self.certification / "id_ed25519").write_text("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n")
        self.refuses("private key material")

    def test_the_certifier_cannot_review_their_own_release(self):
        self.write_review(reviewer_key=self.key, reviewerPrincipal=PRINCIPAL)
        self.refuses("different principal")

    def test_a_review_for_another_certification(self):
        self.write_review(certificationDigest="sha256:" + "5" * 64)
        self.refuses("certificationDigest")

    def test_a_review_signed_by_an_unenrolled_key(self):
        other, _ = keygen(self.shared, f"stranger-{self.id().split('.')[-1]}")
        self.write_review(reviewer_key=other)
        self.refuses("enrolled reviewer")

    def test_a_review_made_before_the_certification(self):
        self.write_review(reviewedAt="2020-01-01T00:00:00Z")
        self.refuses("predates the certification")

    def test_release_notes_that_omit_required_statements(self):
        self.notes.write_text("# Telos\n" + "words " * 80)
        self.refuses("do not state")

    def test_a_rehearsal_build_record(self):
        record = json.loads((self.out / "build-record.json").read_text())
        record["recordKind"] = "rehearsal"
        (self.out / "build-record.json").write_bytes(rg.canonical_bytes(record))
        self.refuses("rehearsal")

    def test_an_evidence_bundle_with_an_altered_file(self):
        victim = self.bundle / eb.GATES_DIR / "fix-gate.log"
        os.chmod(victim, 0o644)
        victim.write_bytes(victim.read_bytes() + b"x")
        self.refuses("changed after it was indexed|does not verify")

    def test_a_download_that_differs_from_what_was_uploaded(self):
        os.environ["FAKE_GH_TAMPER"] = "certification.json"
        with self.assertRaises(sr.StageError):
            self.stage()
        self.assertEqual(self.release_state()[TAG]["draft"], True, "the draft is left for diagnosis, never published")


class PromoteRefusalTests(StageFixture):
    def setUp(self):
        super().setUp()
        self.stage()

    def refuses(self, fragment, **overrides):
        with self.assertRaisesRegex((sr.StageError, eb.BundleError), fragment):
            self.promote(**overrides)
        self.assertEqual(self.release_state()[TAG]["draft"], True, "a refused promotion leaves the draft a draft")

    def test_the_wrong_candidate_digest(self):
        self.refuses("exact digest", candidate_lock_digest="sha256:" + "0" * 64)

    def test_an_asset_tampered_with_in_the_draft(self):
        os.environ["FAKE_GH_TAMPER"] = "evidence-bundle.tar.zst"
        self.refuses("cannot be unpacked|does not verify")

    def test_an_asset_missing_from_the_draft(self):
        os.environ["FAKE_GH_DROP"] = "release-review.json.sig"
        self.refuses("release asset missing")

    def test_an_unexpected_extra_asset(self):
        extra = self.dir / "extra.bin"
        extra.write_text("x")
        subprocess.run([str(self.gh), "release", "upload", TAG, str(extra)], check=True, capture_output=True, env=dict(os.environ))
        self.refuses("unexpected release asset|asset list")

    def test_a_published_release_is_not_promoted_again(self):
        self.promote()
        with self.assertRaisesRegex(sr.StageError, "already published"):
            self.promote()

    def test_a_publisher_who_is_not_on_the_allowlist(self):
        os.environ["FAKE_GH_LOGIN"] = "someone-else"
        self.refuses("allowlist")

    def test_evidence_that_went_stale_before_promotion(self):
        with self.assertRaises((sr.StageError, eb.BundleError)):
            sr.verify_assets(self.download_all(), TAG, self.catalog_path, self.signers_path, self.repo, now=eb.now_utc() + __import__("datetime").timedelta(days=60))

    def download_all(self):
        target = self.dir / "dl"
        target.mkdir(exist_ok=True)
        subprocess.run([str(self.gh), "release", "download", TAG, "--dir", str(target), "--pattern", "*"], check=True, capture_output=True, env=dict(os.environ))
        return target

    def test_the_promotion_digest_must_be_well_formed(self):
        self.assertEqual(sr.main(["promote", "--tag", TAG, "--candidate-lock-digest", "abc", "--policy", str(self.policy), "--gh", str(self.gh)]), 1)


class WorkflowContractTests(unittest.TestCase):
    def test_the_release_workflow_only_verifies_and_promotes(self):
        import yaml
        root = HERE.parents[1]
        text = (root / ".github/workflows/release.yml").read_text()
        document = yaml.safe_load(text)
        for forbidden in ("npm ", "cargo ", "go build", "go test", "podman build", "podman save", "docker ", "apt-get", "pip ", "id-token", "secrets.",
                          "continue-on-error", "--clobber", "gh release upload", "gh release create", "build-release.sh", "sign-evidence"):
            self.assertNotIn(forbidden, text, forbidden)
        triggers = document.get("on", document.get(True))
        self.assertEqual(list(triggers), ["workflow_dispatch"])
        self.assertEqual(set(triggers["workflow_dispatch"]["inputs"]), {"tag", "candidate_lock_digest"})
        self.assertEqual(document["permissions"], {"contents": "read"})
        self.assertEqual(list(document["jobs"]), ["verify", "promote"])
        self.assertEqual(document["jobs"]["promote"]["permissions"], {"contents": "write"})
        self.assertEqual(document["jobs"]["promote"]["needs"], "verify")
        import re
        for reference in re.findall(r"^\s*-?\s*uses:\s*(\S+)", text, re.M):
            self.assertRegex(reference, r"^[\w.-]+/[\w.-]+@[0-9a-f]{40}$")
        script = "\n".join(str(s.get("run", "")) for j in document["jobs"].values() for s in j["steps"])
        self.assertIn("scripts/promote-release.sh", script)


if __name__ == "__main__":
    unittest.main()
