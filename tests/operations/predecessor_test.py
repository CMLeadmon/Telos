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


if __name__ == "__main__":
    unittest.main(verbosity=1)
