#!/usr/bin/env python3
"""Contract for .github/workflows/desktop.yml: the Ubuntu AppImage lane."""
import sys
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/desktop.yml"


class DesktopWorkflowContract(unittest.TestCase):
    def setUp(self):
        self.document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
        self.job = self.document["jobs"]["linux-appimage"]
        self.script = "\n".join(str(step.get("run", "")) for step in self.job["steps"])

    def test_least_privilege_and_no_tolerated_failure(self):
        self.assertEqual(self.document["permissions"], {"contents": "read"})
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertNotIn("continue-on-error", text)
        self.assertNotIn("pull_request_target", text)
        self.assertEqual(list(self.document["jobs"]), ["linux-appimage"])

    def test_native_lane_is_ubuntu_2404_and_linux_only(self):
        self.assertEqual(self.job["runs-on"], "ubuntu-24.04")
        self.assertGreaterEqual(self.job["timeout-minutes"], 30)
        text = WORKFLOW.read_text(encoding="utf-8").lower()
        for deferred in ("windows", "macos", "macos-", "notarize", "signtool", "codesign"):
            self.assertNotIn(deferred, text, f"a deferred native platform leaked into the initial lane: {deferred}")

    def test_declared_prerequisites_and_pinned_toolchains(self):
        for package in ("libwebkit2gtk-4.1-dev", "libxdo-dev", "libssl-dev", "librsvg2-dev", "libayatana-appindicator3-dev", "squashfs-tools", "xdg-utils"):
            self.assertIn(package, self.script)
        node = [s for s in self.job["steps"] if s.get("uses", "").startswith("actions/setup-node")]
        self.assertEqual(node[0]["with"]["node-version"], "24.18.0")
        self.assertIn("rustfmt", self.script)
        self.assertIn("clippy", self.script)
        self.assertIn("npm --prefix frontend ci", self.script)

    def test_gates_build_verify_and_upload_in_order(self):
        order = [
            "desktop-gate.sh os-credential-adapter",
            "desktop-gate.sh platform-trusted-tls",
            "desktop-gate.sh native-auth-state",
            "tauri -- build --bundles appimage",
            "write-artifact-identity.py",
            "verify-desktop-artifact.py --platform linux",
        ]
        positions = [self.script.index(item) for item in order]
        self.assertEqual(positions, sorted(positions))
        upload = [s for s in self.job["steps"] if s.get("uses", "").startswith("actions/upload-artifact")]
        self.assertEqual(len(upload), 1)
        self.assertEqual(upload[0]["with"]["if-no-files-found"], "error")
        paths = upload[0]["with"]["path"]
        self.assertIn("*.AppImage", paths)
        self.assertIn("*.AppImage.identity.json", paths)
        self.assertNotIn("mv ", self.script)  # uploaded without renaming

    def test_the_verified_directory_is_the_uploaded_one(self):
        # Tauri leaves Telos.AppDir beside the AppImage; the verifier rejects any file that is not the image or its sidecar.
        self.assertIn("--artifact desktop-out", self.script)
        self.assertNotIn('--artifact "$bundle"', self.script)
        self.assertLess(self.script.index("cp \"$image\""), self.script.index("verify-desktop-artifact.py --platform linux"))
        upload = [s for s in self.job["steps"] if s.get("uses", "").startswith("actions/upload-artifact")][0]
        self.assertIn("desktop-out/*.AppImage", upload["with"]["path"])
        self.assertNotIn("src-tauri/target", upload["with"]["path"])

    def test_no_updater_or_signing_configuration(self):
        text = WORKFLOW.read_text(encoding="utf-8")
        for forbidden in ("TAURI_SIGNING_PRIVATE_KEY", "TAURI_PRIVATE_KEY", "updater", "secrets."):
            self.assertNotIn(forbidden, text)


if __name__ == "__main__":
    unittest.main(verbosity=1)
