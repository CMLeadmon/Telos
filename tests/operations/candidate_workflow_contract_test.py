#!/usr/bin/env python3
"""Contract for .github/workflows/candidate.yml: one immutable candidate, built once, from a signed tag, with no secrets."""
import re
import sys
import unittest
from pathlib import Path

import yaml

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/candidate.yml"


class CandidateWorkflowContract(unittest.TestCase):
    def setUp(self):
        self.text = WORKFLOW.read_text(encoding="utf-8")
        self.document = yaml.safe_load(self.text)
        # PyYAML reads the bare key `on` as the boolean True
        self.triggers = self.document.get("on", self.document.get(True))
        self.jobs = self.document["jobs"]

    def script(self, job):
        return "\n".join(str(step.get("run", "")) for step in self.jobs[job]["steps"])

    def test_dispatch_only_with_read_permission_and_no_secret(self):
        self.assertEqual(list(self.triggers), ["workflow_dispatch"])
        self.assertEqual(self.document["permissions"], {"contents": "read"})
        for forbidden in ("secrets.", "pull_request_target", "continue-on-error", "id-token", "contents: write"):
            self.assertNotIn(forbidden, self.text)
        self.assertEqual(set(self.triggers["workflow_dispatch"]["inputs"]), {"tag", "version"})

    def test_every_action_is_pinned_to_a_commit(self):
        uses = re.findall(r"^\s*-?\s*uses:\s*(\S+)", self.text, re.M)
        self.assertTrue(uses)
        for reference in uses:
            self.assertRegex(reference, r"^[\w.-]+/[\w.-]+@[0-9a-f]{40}$", reference)

    def test_inputs_never_reach_a_shell_by_interpolation(self):
        for name in self.jobs:
            for step in self.jobs[name]["steps"]:
                self.assertNotIn("${{", str(step.get("run", "")).replace("${{ needs", "").replace("${{ runner", ""), step)
                if "inputs." in str(step.get("run", "")):
                    self.fail(f"{name}: an input is interpolated into a script")

    def test_linux_only_and_three_jobs_in_order(self):
        self.assertEqual(list(self.jobs), ["validate", "desktop", "assemble"])
        for job in self.jobs.values():
            self.assertEqual(job["runs-on"], "ubuntu-24.04")
        self.assertEqual(self.jobs["desktop"]["needs"], "validate")
        self.assertEqual(set(self.jobs["assemble"]["needs"]), {"validate", "desktop"})
        lowered = self.text.lower()
        for deferred in ("windows", "macos", "notariz", "signtool", "codesign"):
            self.assertNotIn(deferred, lowered)

    def test_the_tag_must_be_signed_annotated_and_match_the_version(self):
        script = self.script("validate")
        self.assertIn('"$TAG" = "v$VERSION"', script)
        self.assertIn("git cat-file -t", script)
        self.assertIn("gpg.ssh.allowedSignersFile=release/telos-tag-signers", script)
        self.assertIn("tag -v", script)

    def test_the_candidate_is_built_once_reproducibly_from_the_tag(self):
        script = self.script("assemble")
        self.assertEqual(script.count("build-release.sh"), 1)
        self.assertIn("--verify-reproducible", script)
        self.assertIn('--tag "$TAG"', script)
        checkouts = [s for j in self.jobs.values() for s in j["steps"] if str(s.get("uses", "")).startswith("actions/checkout")]
        for step in checkouts:
            self.assertEqual(step["with"]["ref"], "${{ inputs.tag }}")
            self.assertIs(step["with"]["persist-credentials"], False)

    def test_the_desktop_artifact_carries_its_identity_from_the_same_run(self):
        script = self.script("desktop")
        for needed in ("write-artifact-identity.py", "--run-id \"$GITHUB_RUN_ID\"", "--run-attempt \"$GITHUB_RUN_ATTEMPT\"", "verify-desktop-artifact.py --platform linux"):
            self.assertIn(needed, script)
        self.assertLess(script.index("write-artifact-identity.py"), script.index("verify-desktop-artifact.py"))
        # Tauri leaves Telos.AppDir beside the AppImage: verify the staged upload set, never the bundle directory.
        self.assertIn("--artifact desktop-out", script)
        self.assertNotIn('--artifact "$bundle"', script)
        self.assertLess(script.index('cp "$image"'), script.index("verify-desktop-artifact.py"))

    def test_uploads_are_named_with_the_version_and_the_lock_and_kept_two_weeks(self):
        uploads = [s for j in self.jobs.values() for s in j["steps"] if str(s.get("uses", "")).startswith("actions/upload-artifact")]
        self.assertEqual(len(uploads), 2)
        for step in uploads:
            self.assertGreaterEqual(step["with"]["retention-days"], 14)
            self.assertEqual(step["with"]["if-no-files-found"], "error")
            self.assertIn("inputs.version", step["with"]["name"])
        self.assertIn("steps.lock.outputs.digest", uploads[1]["with"]["name"])


if __name__ == "__main__":
    unittest.main()
