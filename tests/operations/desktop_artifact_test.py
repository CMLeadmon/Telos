#!/usr/bin/env python3
"""scripts/verify-desktop-artifact.py against fixture AppImages (never executed)."""

import hashlib
import json
import os
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
VERIFIER = ROOT / "scripts" / "verify-desktop-artifact.py"
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import release_graph as rg  # noqa: E402

COMMIT = "a" * 40
VERSION = "0.1.0-beta.1"
HAVE_SQUASHFS = shutil.which("mksquashfs") is not None and shutil.which("unsquashfs") is not None


def elf_stub(section_count=4):
    """A minimal little-endian ELF64 header whose section table ends right after itself."""
    header = bytearray(64)
    header[0:4] = b"\x7fELF"
    header[4], header[5], header[6] = 2, 1, 1
    shoff = 64
    struct.pack_into("<Q", header, 0x28, shoff)
    struct.pack_into("<HH", header, 0x3A, 64, section_count)
    return bytes(header) + b"\x00" * (64 * section_count)


def build_appimage(directory, files, name=None, version=VERSION):
    """Writes an AppImage whose payload is a real SquashFS of ``files`` ({path: bytes})."""
    scratch = tempfile.mkdtemp(prefix="appimage-fixture-")  # outside the bundle directory
    tree = Path(tempfile.mkdtemp(dir=scratch, prefix="tree-"))
    for relative, data in files.items():
        target = tree / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    image = Path(scratch) / "payload.squashfs"
    subprocess.run(["mksquashfs", str(tree), str(image), "-quiet", "-no-progress", "-noappend"], check=True, capture_output=True)
    path = Path(directory) / (name or f"Telos_{version}_amd64.AppImage")
    path.write_bytes(elf_stub() + image.read_bytes())
    shutil.rmtree(scratch, ignore_errors=True)
    return path


def write_identity(path, appimage, **overrides):
    data = appimage.read_bytes()
    identity = {
        "schemaVersion": 1, "platform": "linux-x86_64", "sourceCommit": COMMIT, "version": VERSION,
        "size": len(data), "sha256": hashlib.sha256(data).hexdigest(), "workflowRunId": 4242, "workflowRunAttempt": 1,
    }
    identity.update(overrides)
    Path(path).write_bytes(rg.canonical_bytes(identity))
    return Path(path)


def run(*args):
    return subprocess.run([sys.executable, "-I", str(VERIFIER), "--platform", "linux", *args], capture_output=True, text=True, check=False)


GOOD_PAYLOAD = {"usr/bin/telos-shell": b"\x7fELF binary", "usr/share/applications/Telos.desktop": b"[Desktop Entry]\nName=Telos\n"}


@unittest.skipUnless(HAVE_SQUASHFS, "squashfs-tools (mksquashfs, unsquashfs) are required to build and inspect fixture AppImages")
class VerifierTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)

    def good(self, **identity):
        bundle = self.dir / "bundle"
        bundle.mkdir()
        image = build_appimage(bundle, GOOD_PAYLOAD)
        sidecar = write_identity(self.dir / "identity.json", image, **identity)
        return bundle, image, sidecar

    def test_accepts_exactly_one_appimage_with_a_matching_sidecar(self):
        bundle, image, sidecar = self.good()
        for artifact in (image, bundle):
            result = run("--artifact", str(artifact), "--identity", str(sidecar))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("not executed", result.stdout)

    def test_never_executes_the_artifact(self):
        bundle = self.dir / "bundle"
        bundle.mkdir()
        marker = self.dir / "ran"
        # A payload that would create a marker if anything ran the file; the ELF stub is data, not code.
        image = build_appimage(bundle, {"usr/bin/x": f"#!/bin/sh\ntouch {marker}\n".encode()})
        os.chmod(image, 0o755)
        sidecar = write_identity(self.dir / "identity.json", image)
        self.assertEqual(run("--artifact", str(image), "--identity", str(sidecar)).returncode, 0)
        self.assertFalse(marker.exists())

    def test_rejects_the_wrong_extension_and_non_regular_files(self):
        bundle, image, sidecar = self.good()
        renamed = image.with_name("Telos_0.1.0-beta.1_amd64.deb")
        image.rename(renamed)
        result = run("--artifact", str(renamed), "--identity", str(sidecar))
        self.assertEqual(result.returncode, 1)
        self.assertIn("only accepted artifact is an .AppImage", result.stderr)
        link = self.dir / "link.AppImage"
        link.symlink_to(renamed)
        self.assertEqual(run("--artifact", str(link), "--identity", str(sidecar)).returncode, 1)

    def test_rejects_a_duplicate_platform_artifact(self):
        bundle, image, sidecar = self.good()
        build_appimage(bundle, GOOD_PAYLOAD, name="Telos_0.1.0-beta.1_amd64_copy.AppImage")
        result = run("--artifact", str(bundle), "--identity", str(sidecar))
        self.assertEqual(result.returncode, 1)
        self.assertIn("duplicate platform artifact", result.stderr)

    def test_rejects_other_platform_artifacts_and_updater_metadata_in_the_bundle(self):
        for extra in ("Telos_0.1.0-beta.1_x64-setup.exe", "Telos.dmg", "Telos.AppImage.sig", "latest.json", "Telos.AppImage.zsync"):
            bundle, image, sidecar = self.good()
            (bundle / extra).write_bytes(b"x")
            result = run("--artifact", str(bundle), "--identity", str(sidecar))
            self.assertEqual(result.returncode, 1, extra)
            self.assertRegex(result.stderr, "unsupported artifact|updater or signing", extra)
            shutil.rmtree(bundle)
            (self.dir / "identity.json").unlink()

    def test_rejects_a_zero_length_artifact(self):
        empty = self.dir / "Telos_0.1.0-beta.1_amd64.AppImage"
        empty.write_bytes(b"")
        sidecar = write_identity(self.dir / "identity.json", empty, sha256=hashlib.sha256(b"").hexdigest(), size=0)
        result = run("--artifact", str(empty), "--identity", str(sidecar))
        self.assertEqual(result.returncode, 1)
        self.assertIn("zero-length", result.stderr)

    def test_rejects_a_missing_identity_sidecar(self):
        bundle, image, _ = self.good()
        for args in ([], ["--identity", str(self.dir / "absent.json")]):
            result = run("--artifact", str(image), *args)
            self.assertEqual(result.returncode, 1)
            self.assertIn("identity sidecar is missing", result.stderr)

    def test_rejects_checksum_size_and_version_mismatches(self):
        bundle, image, sidecar = self.good(sha256="0" * 64)
        self.assertIn("checksum differs", run("--artifact", str(image), "--identity", str(sidecar)).stderr)
        write_identity(sidecar, image, size=1)
        self.assertIn("size differs", run("--artifact", str(image), "--identity", str(sidecar)).stderr)
        write_identity(sidecar, image, version="0.2.0-beta.1")
        result = run("--artifact", str(image), "--identity", str(sidecar))
        self.assertEqual(result.returncode, 1)
        self.assertIn("does not carry the identity version", result.stderr)
        # A tampered byte after the sidecar was written is a checksum mismatch.
        write_identity(sidecar, image)
        data = bytearray(image.read_bytes())
        data[-1] ^= 0xFF
        image.write_bytes(bytes(data))
        self.assertEqual(run("--artifact", str(image), "--identity", str(sidecar)).returncode, 1)

    def test_rejects_a_malformed_sidecar(self):
        bundle, image, sidecar = self.good()
        document = json.loads(sidecar.read_text())
        document["extra"] = 1
        sidecar.write_text(json.dumps(document))
        self.assertEqual(run("--artifact", str(image), "--identity", str(sidecar)).returncode, 1)

    def test_rejects_a_bundled_credential_file_but_not_a_binary_that_merely_names_it(self):
        for path in ("usr/lib/telos-secrets.json", "telos-secrets.json", "usr/share/telos/telos-secrets.json"):
            bundle = self.dir / "b"
            bundle.mkdir()
            image = build_appimage(bundle, {**GOOD_PAYLOAD, path: b"{}"})
            sidecar = write_identity(self.dir / "identity.json", image)
            result = run("--artifact", str(image), "--identity", str(sidecar))
            self.assertEqual(result.returncode, 1, path)
            self.assertIn("credential file", result.stderr)
            shutil.rmtree(bundle)
        # The retirement helper's string inside a binary is legitimate.
        bundle = self.dir / "c"
        bundle.mkdir()
        image = build_appimage(bundle, {**GOOD_PAYLOAD, "usr/bin/telos-shell": b"\x7fELF ... telos-secrets.json ..."})
        sidecar = write_identity(self.dir / "identity2.json", image)
        self.assertEqual(run("--artifact", str(image), "--identity", str(sidecar)).returncode, 0)

    def test_rejects_updater_configuration_in_the_payload(self):
        for path in ("usr/share/telos/latest.json", "usr/lib/updater.json", "usr/bin/Telos.sig"):
            bundle = self.dir / "u"
            bundle.mkdir()
            image = build_appimage(bundle, {**GOOD_PAYLOAD, path: b"{}"})
            sidecar = write_identity(self.dir / "identity.json", image)
            result = run("--artifact", str(image), "--identity", str(sidecar))
            self.assertEqual(result.returncode, 1, path)
            self.assertIn("updater or signing", result.stderr)
            shutil.rmtree(bundle)

    def test_rejects_a_file_that_is_not_an_appimage_payload(self):
        bundle = self.dir / "bundle"
        bundle.mkdir()
        image = bundle / "Telos_0.1.0-beta.1_amd64.AppImage"
        image.write_bytes(elf_stub() + b"this is not a squashfs")
        sidecar = write_identity(self.dir / "identity.json", image)
        result = run("--artifact", str(image), "--identity", str(sidecar))
        self.assertEqual(result.returncode, 1)
        self.assertIn("no SquashFS payload", result.stderr)
        image.write_bytes(b"#!/bin/sh\necho not an elf\n" * 10)
        write_identity(sidecar, image)
        self.assertIn("not an ELF", run("--artifact", str(image), "--identity", str(sidecar)).stderr)

    def test_candidate_lock_binds_digest_commit_and_version_without_rewriting(self):
        bundle, image, sidecar = self.good()
        digest = "sha256:" + hashlib.sha256(image.read_bytes()).hexdigest()
        lock = {"schemaVersion": 1, "sourceCommit": COMMIT, "version": VERSION, "releaseManifestDigest": "sha256:" + "1" * 64,
                "artifactIndexDigest": "sha256:" + "2" * 64, "artifacts": {f"desktop/{image.name}": digest}}
        lock_path = self.dir / "lock.json"
        lock_path.write_bytes(rg.canonical_bytes(lock))
        before = (image.read_bytes(), sidecar.read_bytes())
        self.assertEqual(run("--artifact", str(image), "--identity", str(sidecar), "--candidate-lock", str(lock_path)).returncode, 0)
        self.assertEqual(before, (image.read_bytes(), sidecar.read_bytes()), "the artifact and sidecar must not be rewritten")
        for mutate, text in (
            (lambda d: d.update(sourceCommit="b" * 40), "source commit"),
            (lambda d: d.update(version="0.1.0-beta.2"), "version"),
            (lambda d: d.update(artifacts={f"desktop/{image.name}": "sha256:" + "9" * 64}), "not in the candidate lock"),
        ):
            changed = dict(lock)
            mutate(changed)
            lock_path.write_bytes(rg.canonical_bytes(changed))
            result = run("--artifact", str(image), "--identity", str(sidecar), "--candidate-lock", str(lock_path))
            self.assertEqual(result.returncode, 1, text)
            self.assertIn(text, result.stderr)


class WithoutTheInspectionTool(unittest.TestCase):
    def test_a_missing_unsquashfs_is_not_run_never_a_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / f"Telos_{VERSION}_amd64.AppImage"
            image.write_bytes(elf_stub() + b"hsqs" + b"\x00" * 64)
            sidecar = write_identity(Path(directory) / "identity.json", image)
            empty_bin = Path(directory) / "bin"
            empty_bin.mkdir()  # a PATH with no unsquashfs on it
            env = {"PATH": str(empty_bin), "HOME": directory}
            result = subprocess.run([sys.executable, "-I", str(VERIFIER), "--platform", "linux", "--artifact", str(image), "--identity", str(sidecar)],
                                    capture_output=True, text=True, env=env, check=False)
            self.assertEqual(result.returncode, 3, result.stderr)
            self.assertIn("unsquashfs is not installed", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=1)
