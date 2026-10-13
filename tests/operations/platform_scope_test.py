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

    def assert_platform_gate_rejected(self, mutate, label):
        path = "ci/phase-gates.json"
        catalog = self.read(path)
        mutate(catalog)
        self.write(path, catalog)
        result = self.check()
        self.assertNotEqual(result.returncode, 0, f"{label} was accepted: {result.stdout}")
        self.assertIn("platform-scope", result.stdout + result.stderr, label)

    @staticmethod
    def gate(catalog, gate_id):
        return next(g for g in catalog["gates"] if g["id"] == gate_id)

    def test_catalog_must_carry_platform_scope_gate(self):
        self.assert_platform_gate_rejected(
            lambda catalog: catalog.update(gates=[
                g for g in catalog["gates"] if g["id"] != "platform-scope"]),
            "catalog without platform-scope")

    def test_platform_scope_gate_cannot_be_optional(self):
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(required=False),
            "optional platform-scope")

    def test_platform_scope_gate_must_stay_phase_one_local(self):
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(phase=2),
            "platform-scope moved out of phase 1")
        self.setUp()
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(scope="external"),
            "platform-scope moved out of local scope")

    def test_certification_must_depend_on_platform_scope(self):
        def drop(catalog):
            gate = self.gate(catalog, "beta-certification")
            gate["prerequisites"] = [p for p in gate["prerequisites"] if p != "platform-scope"]
        self.assert_platform_gate_rejected(drop, "certification without platform-scope")

    def test_platform_scope_gate_must_run_this_test(self):
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(
                command=["python3", "-c", "raise SystemExit(0)"]),
            "platform-scope with a hollow command")

    def test_platform_scope_gate_must_bind_source_without_prerequisites(self):
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(subjectKind="fixture"),
            "platform-scope bound to a fixture subject")
        self.setUp()
        self.assert_platform_gate_rejected(
            lambda catalog: self.gate(catalog, "platform-scope").update(
                prerequisites=["evidence-envelope"]),
            "platform-scope with a prerequisite")


if __name__ == "__main__":
    unittest.main()
