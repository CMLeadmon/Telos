#!/usr/bin/env python3
"""Trust-anchor enrollment and namespace-separated signature verification.

All keys are ephemeral: generated under a mode-0700 temporary directory and
removed on exit. No test reads or writes a real release key.
"""
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/lib"))
import signing  # noqa: E402

ENROLL = str(ROOT / "scripts/enroll-release-signer.py")


def keygen(directory, name, key_type="ed25519", comment="ephemeral-test-key"):
    path = Path(directory) / name
    subprocess.run(["ssh-keygen", "-q", "-t", key_type, "-N", "", "-C", comment, "-f", str(path)], check=True)
    return path, Path(str(path) + ".pub")


def fingerprint(public_key):
    out = subprocess.run(["ssh-keygen", "-l", "-f", str(public_key)], capture_output=True, text=True, check=True).stdout
    return out.split()[1]


class SignerTrustTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.dir = Path(self.temp.name)
        os.chmod(self.dir, 0o700)
        self.release = self.dir / "release"
        self.release.mkdir()
        for name in ("telos-tag-signers", "evidence-signers"):
            (self.release / name).write_text("# empty\n")
        self.private, self.public = keygen(self.dir, "signer")

    def enroll(self, purpose="evidence", principal="release@example.test", public=None, confirm="auto", **extra):
        public = public or self.public
        arguments = [sys.executable, ENROLL, "--release-dir", str(self.release), "--purpose", purpose,
                     "--principal", principal, "--public-key-file", str(public)]
        if confirm == "auto":
            confirm = fingerprint(public) if Path(public).exists() else "x"
        if confirm is not None:
            arguments += ["--confirm-fingerprint", confirm]
        return subprocess.run(arguments, capture_output=True, text=True, stdin=subprocess.DEVNULL)

    def verify_files(self):
        return subprocess.run([sys.executable, ENROLL, "--release-dir", str(self.release), "--verify-files"],
                              capture_output=True, text=True)

    def test_enrollment_requires_the_matching_fingerprint(self):
        done = self.enroll(confirm=None)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("confirmation", done.stderr)
        done = self.enroll(confirm="SHA256:" + "A" * 43)
        self.assertNotEqual(done.returncode, 0)
        self.assertNotIn("release@example.test", (self.release / "evidence-signers").read_text())

    def test_successful_enrollment_binds_purpose_namespaces(self):
        self.assertEqual(self.enroll().returncode, 0)
        entry = (self.release / "evidence-signers").read_text()
        for namespace in signing.NAMESPACES.values():
            self.assertIn(namespace, entry)
        self.assertEqual(self.enroll(purpose="tag", principal="tagger@example.test", public=self.public).returncode, 0)
        self.assertIn('namespaces="git"', (self.release / "telos-tag-signers").read_text())
        verified = self.verify_files()
        self.assertEqual(verified.returncode, 0, verified.stderr)
        self.assertIn(fingerprint(self.public), verified.stdout)

    def test_verify_files_is_not_run_until_both_files_have_a_signer(self):
        self.assertEqual(self.verify_files().returncode, 3)
        self.enroll()
        self.assertEqual(self.verify_files().returncode, 3)

    def test_rejects_private_keys_wrong_types_and_malformed_input(self):
        self.assertNotEqual(self.enroll(public=self.private).returncode, 0)
        _, ecdsa = keygen(self.dir, "ecdsa", "ecdsa")
        self.assertIn("unsupported key type", self.enroll(public=ecdsa).stderr)
        text = self.public.read_text().split()
        cases = {
            "ellipsis": f"{text[0]} AAAAC3NzaC1lZDI1NTE5AAAAI...\n",
            "bad base64": f"{text[0]} not*base64!\n",
            "type mismatch": f"ssh-ed25519 {text[1][:-4]}AAAA\n",
            "two keys": self.public.read_text() * 2,
            "control char": f"{text[0]} {text[1]} bad\x07comment\n",
            "empty": "",
        }
        for name, content in cases.items():
            with self.subTest(name):
                path = self.dir / f"case-{name.replace(' ', '-')}.pub"
                path.write_text(content)
                done = self.enroll(public=path, confirm="SHA256:" + "A" * 43)
                self.assertNotEqual(done.returncode, 0)
        self.assertEqual((self.release / "evidence-signers").read_text(), "# empty\n")

    def test_rejects_duplicate_principal_and_duplicate_key(self):
        self.assertEqual(self.enroll().returncode, 0)
        _, other = keygen(self.dir, "other")
        self.assertIn("duplicate principal", self.enroll(public=other).stderr)
        self.assertIn("duplicate key", self.enroll(principal="second@example.test").stderr)

    def test_rejects_invalid_principal(self):
        self.assertNotEqual(self.enroll(principal="bad principal").returncode, 0)
        self.assertNotEqual(self.enroll(principal="a" * 129).returncode, 0)

    def test_verify_files_rejects_hand_edited_fabricated_entries(self):
        (self.release / "telos-tag-signers").write_text(
            'release@telos.local namespaces="git" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI...\n')
        done = self.verify_files()
        self.assertEqual(done.returncode, 1)
        (self.release / "telos-tag-signers").write_text("release@telos.local ssh-ed25519 AAAA\n")
        self.assertEqual(self.verify_files().returncode, 1)

    def test_legacy_release_key_file_is_rejected(self):
        (self.release / "telos-release.pub").write_text("stale\n")
        done = self.verify_files()
        self.assertEqual(done.returncode, 1)
        self.assertIn("telos-release.pub", done.stderr)

    def test_repository_trust_files_are_free_of_fabricated_keys(self):
        self.assertFalse((ROOT / "release/telos-release.pub").exists())
        for name in ("telos-tag-signers", "evidence-signers"):
            text = (ROOT / "release" / name).read_text()
            self.assertNotIn("...", text, name)
        done = subprocess.run([sys.executable, ENROLL, "--verify-files"], capture_output=True, text=True)
        self.assertIn(done.returncode, (0, 3), done.stderr)


class SignatureNamespaceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.dir = Path(self.temp.name)
        os.chmod(self.dir, 0o700)
        self.private, self.public = keygen(self.dir, "signer")
        self.allowed = self.dir / "allowed"
        namespaces = ",".join(signing.NAMESPACES.values())
        key = " ".join(self.public.read_text().split()[:2])
        self.allowed.write_text(f'release@example.test namespaces="{namespaces}" {key}\n')
        self.data = self.dir / "index.json"
        self.data.write_bytes(b'{"evidence":"index"}\n')
        self.signature = signing.sign_file(self.private, signing.NAMESPACES["evidence"], self.data)

    def verify(self, **overrides):
        arguments = dict(allowed_signers=self.allowed, principal="release@example.test",
                         namespace=signing.NAMESPACES["evidence"], data=self.data.read_bytes(),
                         signature=self.signature)
        arguments.update(overrides)
        return signing.verify_signature(**arguments)

    def test_good_signature_reports_the_signer_fingerprint(self):
        self.assertEqual(self.verify(), fingerprint(self.public))

    def test_wrong_namespace_is_rejected(self):
        for name in ("certification", "review", "guided"):
            with self.subTest(name), self.assertRaises(signing.SigningError):
                self.verify(namespace=signing.NAMESPACES[name])
        with self.assertRaises(signing.SigningError):
            self.verify(namespace="git")

    def test_changed_bytes_are_rejected(self):
        with self.assertRaises(signing.SigningError):
            self.verify(data=b'{"evidence":"tampered"}\n')

    def test_unauthorized_principal_is_rejected(self):
        with self.assertRaises(signing.SigningError):
            self.verify(principal="intruder@example.test")

    def test_unenrolled_key_is_rejected(self):
        other_private, _ = keygen(self.dir, "stranger")
        forged = signing.sign_file(other_private, signing.NAMESPACES["evidence"], self.data)
        with self.assertRaises(signing.SigningError):
            self.verify(signature=forged)

    def test_namespace_restricted_entry_rejects_other_purposes(self):
        key = " ".join(self.public.read_text().split()[:2])
        self.allowed.write_text(f'release@example.test namespaces="{signing.NAMESPACES["review"]}" {key}\n')
        with self.assertRaises(signing.SigningError):
            self.verify()

    def test_signing_refuses_a_group_readable_key(self):
        os.chmod(self.private, 0o640)
        with self.assertRaises(signing.SigningError):
            signing.sign_file(self.private, signing.NAMESPACES["evidence"], self.data)

    def test_signature_over_a_different_purpose_cannot_be_replayed(self):
        review = signing.sign_file(self.private, signing.NAMESPACES["review"], self.data)
        with self.assertRaises(signing.SigningError):
            self.verify(signature=review)


if __name__ == "__main__":
    unittest.main(argv=sys.argv)
