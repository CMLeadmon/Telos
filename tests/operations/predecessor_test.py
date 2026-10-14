#!/usr/bin/env python3
"""The predecessor policy is an exact, obtainable release -- never a version string alone."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import predecessor  # noqa: E402

COMMIT = subprocess.run(["git", "-C", str(ROOT), "rev-parse", "HEAD"], capture_output=True, text=True, check=True).stdout.strip()


def policy(**predecessor_fields):
    body = {"version": "0.1.0-beta.1", "sourceTag": "v0.1.0-beta.1", "sourceCommit": COMMIT,
            "candidateLockDigest": "sha256:" + "a" * 64, "generationManifestDigest": "sha256:" + "b" * 64}
    body.update(predecessor_fields)
    return {"schemaVersion": 1, "predecessor": body, "maxSchemaMigration": 24, "allowInPlaceRollback": True}


class PolicyTests(unittest.TestCase):
    def verdict(self, document):
        return predecessor.verdict(document, str(ROOT))[0]

    def test_the_shipped_policy_names_no_predecessor_and_is_not_run(self):
        document = json.loads((ROOT / "release" / "predecessor-policy.json").read_text())
        self.assertEqual(self.verdict(document), "not_run")

    def test_a_complete_obtainable_predecessor_is_ready(self):
        self.assertEqual(self.verdict(policy()), "ready")

    def test_an_unobtainable_commit_is_not_run(self):
        self.assertEqual(self.verdict(policy(sourceCommit="1" * 40)), "not_run")

    def test_a_version_only_assertion_or_any_malformed_field_is_invalid(self):
        self.assertEqual(self.verdict({"schemaVersion": 1, "predecessor": {"version": "0.1.0-beta.1"}, "maxSchemaMigration": 24, "allowInPlaceRollback": True}), "invalid")
        for change in ({"version": "0.1.0"}, {"sourceTag": "0.1.0-beta.1"}, {"sourceCommit": "abc"}, {"candidateLockDigest": "sha256:xyz"},
                       {"generationManifestDigest": "b" * 64}):
            with self.subTest(change):
                self.assertEqual(self.verdict(policy(**change)), "invalid")
        for broken in ({}, {"schemaVersion": 2}, {"schemaVersion": 1, "predecessor": None}, {"schemaVersion": 1, "predecessor": None, "maxSchemaMigration": -1, "allowInPlaceRollback": True}):
            with self.subTest(broken):
                self.assertEqual(self.verdict(broken), "invalid")

    def test_command_line_exit_codes(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "policy.json"
            path.write_text(json.dumps(policy()))
            run = lambda *a: subprocess.run([sys.executable, str(ROOT / "scripts/lib/predecessor.py"), "check", *a], capture_output=True, text=True)  # noqa: E731
            self.assertEqual(run("--policy", str(path), "--repo", str(ROOT)).returncode, 0)
            path.write_text(json.dumps(policy(sourceCommit="2" * 40)))
            self.assertEqual(run("--policy", str(path), "--repo", str(ROOT)).returncode, 3)
            path.write_text("{not json")
            self.assertEqual(run("--policy", str(path)).returncode, 2)



# ---------------------------------------------------------------------------------------------------------------
# Rebuilding a predecessor with its own builder (stand-in podman/skopeo and SBOM generator, a real signed tag).
sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_release as br  # noqa: E402
import release_graph as rg  # noqa: E402
from build_release_test import Fixture  # noqa: E402

FAKE_BUILDER = '''
import json, os, sys
sys.path.insert(0, %(lib)r)
sys.path.insert(0, %(tests)r)
import build_release as br
import release_graph as rg
def sbom(index_path, root, out_dir, tools_dir):
    os.makedirs(out_dir, mode=0o750, exist_ok=True)
    index = json.loads(open(index_path, "rb").read())
    entries = []
    for artifact in index["artifacts"]:
        entry = {"artifactPath": artifact["path"], "artifactSha256": artifact["sha256"], "kind": artifact["kind"], "sbom": None, "exempt": "n/a", "packages": 0}
        if artifact["kind"] in ("oci-image", "desktop"):
            data = rg.canonical_bytes({"spdxVersion": "SPDX-2.3", "creationInfo": {"created": "x"}, "documentNamespace": "x", "name": "x", "packages": [{"name": "p"}]})
            open(os.path.join(out_dir, artifact["sha256"] + ".spdx.json"), "wb").write(data)
            entry.update(sbom={"path": artifact["sha256"] + ".spdx.json", "sha256": rg.raw_sha256(data)}, exempt=None, packages=1)
        entries.append(entry)
    open(os.path.join(out_dir, "sbom-index.json"), "wb").write(rg.canonical_bytes({"schemaVersion": 1, "tool": {"name": "syft"}, "entries": entries}))
br.default_sbom_generator = sbom
sys.exit(br.main())
'''


class RebuildTests(Fixture):
    def setUp(self):
        super().setUp()
        key = self.dir / "tagkey"
        subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], check=True)
        public = key.with_suffix(".pub").read_text().split()
        (self.repo / "release" / "telos-tag-signers").write_text(f'r@example.invalid namespaces="git" {public[0]} {public[1]}\n')
        git = lambda *a: subprocess.run(["git", "-C", str(self.repo), *a], check=True, capture_output=True, text=True)  # noqa: E731
        git("add", "-A")
        git("commit", "-q", "-m", "enroll")
        self.commit = git("rev-parse", "HEAD").stdout.strip()
        git("-c", "gpg.format=ssh", "-c", f"user.signingkey={key}", "tag", "-s", f"v{self.version}", "-m", "release")
        self.builder = self.dir / "fake_builder.py"
        self.builder.write_text(FAKE_BUILDER % {"lib": str(ROOT / "scripts" / "lib"), "tests": str(Path(__file__).resolve().parent)})
        self.desktop_dir = str(self.desktop())
        # build once to learn the digests the policy will name
        first = self.dir / "first"
        done = subprocess.run([sys.executable, str(self.builder), "build", "--repo", str(self.repo), "--tag", f"v{self.version}", "--version", self.version,
                               "--out-dir", str(first), "--desktop-dir", self.desktop_dir], capture_output=True, text=True, env=os.environ)
        assert done.returncode == 0, done.stderr
        self.record = json.loads((first / "build-record.json").read_text())
        self.generation = "sha256:" + __import__("hashlib").sha256((first / "candidate" / "generation" / "generation.json").read_bytes()).hexdigest()

    def policy(self, **changes):
        body = {"version": self.version, "sourceTag": f"v{self.version}", "sourceCommit": self.commit,
                "candidateLockDigest": self.record["candidateLockDigest"], "generationManifestDigest": self.generation}
        body.update(changes)
        return {"schemaVersion": 1, "predecessor": body, "maxSchemaMigration": 2, "allowInPlaceRollback": True}

    def rebuild(self, policy, name="second", builder=True):
        return predecessor.build(policy, str(self.repo), str(self.dir / name), self.desktop_dir, builder=str(self.builder) if builder else None)

    def test_the_predecessor_rebuilds_to_exactly_the_recorded_digests(self):
        state, reason, identity = self.rebuild(self.policy())
        self.assertEqual((state, identity["candidateLockDigest"], identity["generationManifestDigest"]),
                         ("ready", self.record["candidateLockDigest"], self.generation), reason)
        self.assertEqual(identity["generationId"], self.record["candidateLockDigest"].split(":")[1])
        rg.verify_root(identity["path"])

    def test_a_policy_naming_other_bytes_is_refused(self):
        state, reason, identity = self.rebuild(self.policy(candidateLockDigest="sha256:" + "5" * 64))
        self.assertEqual(state, "invalid")
        self.assertIn("candidate lock", reason)
        state, reason, _ = self.rebuild(self.policy(generationManifestDigest="sha256:" + "6" * 64), name="third")
        self.assertEqual(state, "invalid")
        self.assertIn("generation manifest", reason)

    def test_a_commit_without_a_release_builder_cannot_be_rebuilt(self):
        state, reason, _ = self.rebuild(self.policy(), builder=False)
        self.assertEqual(state, "not_run")
        self.assertIn("no release builder", reason)

    def test_no_enrolled_signer_is_not_run(self):
        (self.repo / "release" / "telos-tag-signers").write_text("# none\n")
        # the tag still verifies against the committed file at that commit, so move to a new unsigned tag state
        subprocess.run(["git", "-C", str(self.repo), "tag", "-d", f"v{self.version}"], check=True, capture_output=True)
        subprocess.run(["git", "-C", str(self.repo), "tag", "-a", f"v{self.version}", "-m", "unsigned"], check=True, capture_output=True)
        state, reason, _ = self.rebuild(self.policy())
        self.assertIn(state, ("not_run", "invalid"))

    def test_the_command_line_requires_an_absolute_out_dir_and_reports_not_run_without_a_predecessor(self):
        run = lambda *a: subprocess.run([sys.executable, str(ROOT / "scripts/lib/predecessor.py"), *a], capture_output=True, text=True)  # noqa: E731
        shipped = run("build", "--out-dir", "relative")
        self.assertEqual(shipped.returncode, 2)
        absolute = run("build", "--out-dir", str(self.dir / "nowhere"))
        self.assertEqual(absolute.returncode, 3)
        self.assertEqual(json.loads(absolute.stdout)["verdict"], "not_run")


if __name__ == "__main__":
    unittest.main(verbosity=1)
