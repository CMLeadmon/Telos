#!/usr/bin/env python3
"""Protect the owner's Ubuntu-server / browser-and-Linux-first boundary."""
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class PlatformScopeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "repo"
        shutil.copytree(ROOT, self.root, ignore=shutil.ignore_patterns(
            ".git", "node_modules", "target", "__pycache__", "out"))

    def read(self, path):
        return json.loads((self.root / path).read_text())

    def write(self, path, value):
        (self.root / path).write_text(json.dumps(value))

    def check(self):
        return subprocess.run(
            ["bash", str(ROOT / "scripts/check-product-truth.sh"), "--root", str(self.root)],
            text=True, capture_output=True, check=False)

    def test_approved_initial_scope(self):
        contract = self.read("documentation/product/beta-contract.json")
        self.assertEqual(contract["server"]["os"], "ubuntu-24.04")
        self.assertEqual(contract["server"].get("versionPolicy"), "24.04-or-newer")
        self.assertEqual(contract["desktop"], ["linux-x86_64"])
        self.assertEqual(contract.get("plannedDesktop"), ["windows-x86_64", "macos-universal"])
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_rejects_non_ubuntu_server(self):
        path = "documentation/product/beta-contract.json"
        contract = self.read(path)
        contract["server"]["os"] = "windows"
        self.write(path, contract)
        self.assertNotEqual(self.check().returncode, 0)

    def test_rejects_windows_server_gate(self):
        path = "ci/phase-gates.json"
        catalog = self.read(path)
        gate = dict(next(g for g in catalog["gates"] if g["id"] == "operator-cli-linux-amd64"))
        gate["id"] = "operator-cli-windows-extra"
        gate["command"] = [x.replace("GOOS=linux", "GOOS=windows") for x in gate["command"]]
        catalog["gates"].append(gate)
        self.write(path, catalog)
        result = self.check()
        self.assertNotEqual(result.returncode, 0, result.stdout)

    def test_rejects_deferred_client_as_required(self):
        path = "ci/phase-gates.json"
        catalog = self.read(path)
        catalog["gates"] = [g for g in catalog["gates"] if g["id"] != "desktop-macos"]
        gate = dict(next(g for g in catalog["gates"] if g["id"] == "desktop-linux"))
        gate["id"] = "desktop-macos"
        catalog["gates"].append(gate)
        self.write(path, catalog)
        self.assertNotEqual(self.check().returncode, 0)

    def test_rejects_deferred_client_as_indirect_prerequisite(self):
        path = "ci/phase-gates.json"
        catalog = self.read(path)
        catalog["gates"] = [g for g in catalog["gates"] if g["id"] != "desktop-windows"]
        gate = dict(next(g for g in catalog["gates"] if g["id"] == "desktop-linux"))
        gate.update(id="desktop-windows", required=False)
        catalog["gates"].append(gate)
        next(g for g in catalog["gates"] if g["id"] == "desktop-builds")["prerequisites"].append("desktop-windows")
        self.write(path, catalog)
        self.assertNotEqual(self.check().returncode, 0)

    def test_browser_gate_cannot_be_optional(self):
        path = "ci/phase-gates.json"
        catalog = self.read(path)
        next(g for g in catalog["gates"] if g["id"] == "browser-matrix")["required"] = False
        self.write(path, catalog)
        self.assertNotEqual(self.check().returncode, 0)


if __name__ == "__main__":
    unittest.main()
