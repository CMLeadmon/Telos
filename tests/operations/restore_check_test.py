#!/usr/bin/env python3
"""Restore pre-flight refusals: manifest integrity, host separation, candidate binding,
schema compatibility, disk space, and hostile tar members. Each refusal is exercised on its
own against an otherwise valid backup, and the valid one must be accepted."""

import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.dont_write_bytecode = True
import backup_manifest as bm  # noqa: E402
import restore_check as rc  # noqa: E402

MIGRATIONS = ROOT / "backend" / "db" / "migrations"
SOURCE_HOST = "sha256:" + "a" * 64
TARGET_HOST = "sha256:" + "b" * 64
CANDIDATE = "sha256:" + "c" * 64


def real_migrations():
    out = []
    for path in sorted(MIGRATIONS.glob("*.sql")):
        out.append({"version": int(path.name[:4]), "name": path.stem[5:], "checksum": hashlib.sha256(path.read_bytes()).hexdigest()})
    return out


class Backup:
    def __init__(self, candidate=CANDIDATE, host=SOURCE_HOST, migrations=None):
        self.dir = Path(tempfile.mkdtemp())
        self.data = self.dir / "data"
        self.data.mkdir()
        for name, _ in bm.COMPONENTS:
            (self.data / name).write_bytes(f"component {name}\n".encode() * 10)
        migrations = migrations if migrations is not None else real_migrations()
        (self.data / "migrations.json").write_text(json.dumps(migrations))
        manifest = self.dir / "manifest.json"
        bm.main(["create", "--stage", str(self.data), "--out", str(manifest), "--started", "2026-10-14T00:00:00Z",
                 "--consistency-point", "2026-10-14T00:00:30Z", "--host-id-sha256", host, "--release", "beta.1",
                 "--candidate-lock-digest", candidate or "", "--tag", "scheduled", "--tag", "telos", "--migrations", str(self.data / "migrations.json")])
        shutil.move(str(manifest), str(self.data / "manifest.json"))

    def close(self):
        shutil.rmtree(self.dir, ignore_errors=True)


def run_plan(backup, **overrides):
    argv = ["plan", "--data", str(backup.data), "--target-machine-id-sha256", overrides.get("host", TARGET_HOST),
            "--migrations", str(overrides.get("migrations", MIGRATIONS)), "--free", f"TMP={overrides.get('free', 10 << 30)}"]
    if overrides.get("rehearsal"):
        argv.append("--rehearsal")
    elif overrides.get("candidate", CANDIDATE):
        argv += ["--candidate-lock-digest", overrides.get("candidate", CANDIDATE)]
    stderr = io.StringIO()
    old = sys.stderr
    sys.stderr = stderr
    try:
        code = rc.main(argv)
    finally:
        sys.stderr = old
    return code, stderr.getvalue()


class Plan(unittest.TestCase):
    def setUp(self):
        self.backup = Backup()
        self.addCleanup(self.backup.close)

    def refused(self, fragment, **overrides):
        code, message = run_plan(self.backup, **overrides)
        self.assertEqual(code, 1, f"expected a refusal: {message}")
        self.assertIn(fragment, message)

    def test_a_valid_backup_on_another_host_is_accepted(self):
        self.assertEqual(run_plan(self.backup), (0, ""))

    def test_a_rehearsal_is_accepted_and_says_it_is_not_evidence(self):
        code, message = run_plan(self.backup, rehearsal=True, candidate=None)
        self.assertEqual(code, 0)
        self.assertIn("not evidence", message)

    def test_the_same_machine_is_refused(self):
        self.refused("machine that made the backup", host=SOURCE_HOST)

    def test_candidate_binding(self):
        self.refused("different candidate", candidate="sha256:" + "d" * 64)
        self.refused("candidate lock is required", candidate="")

    def test_a_tampered_component_is_refused(self):
        (self.backup.data / "postgres.dump").write_bytes(b"tampered")
        self.refused("not trustworthy")

    def test_a_missing_component_and_a_tampered_manifest_are_refused(self):
        (self.backup.data / "grimmory.sql").unlink()
        self.refused("not trustworthy")
        manifest = self.backup.data / "manifest.json"
        document = json.loads(manifest.read_text())
        document["components"].pop()
        manifest.write_text(json.dumps(document))
        self.refused("not trustworthy")

    def test_a_newer_schema_is_refused(self):
        newer = Backup(migrations=real_migrations() + [{"version": len(real_migrations()) + 1, "name": "future", "checksum": "f" * 64}])
        self.addCleanup(newer.close)
        code, message = run_plan(newer)
        self.assertEqual(code, 1)
        self.assertIn("newer than this release", message)

    def test_migration_checksum_drift_is_refused(self):
        drifted = real_migrations()
        drifted[4]["checksum"] = "0" * 64
        backup = Backup(migrations=drifted)
        self.addCleanup(backup.close)
        code, message = run_plan(backup)
        self.assertEqual(code, 1)
        self.assertIn("history drift", message)
        self.assertIn("0005", message)

    def test_insufficient_disk_is_refused(self):
        self.refused("not enough free space for TMP", free=1 << 20)


def tar_with(entries):
    path = Path(tempfile.mkstemp(suffix=".tar")[1])
    with tarfile.open(path, "w") as archive:
        for name, kind, extra in entries:
            info = tarfile.TarInfo(name)
            if kind == "file":
                data = b"content"
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
            elif kind == "dir":
                info.type = tarfile.DIRTYPE
                archive.addfile(info)
            elif kind == "symlink":
                info.type = tarfile.SYMTYPE
                info.linkname = extra
                archive.addfile(info)
            elif kind == "hardlink":
                info.type = tarfile.LNKTYPE
                info.linkname = extra
                archive.addfile(info)
            elif kind == "chardev":
                info.type = tarfile.CHRTYPE
                archive.addfile(info)
            elif kind == "fifo":
                info.type = tarfile.FIFOTYPE
                archive.addfile(info)
    return path


class TarSafety(unittest.TestCase):
    def verdict(self, entries):
        path = tar_with(entries)
        self.addCleanup(path.unlink)
        return rc.check_tar(str(path))

    def test_ordinary_trees_and_inside_links_are_accepted(self):
        self.assertEqual(self.verdict([("a/", "dir", None), ("a/b.txt", "file", None), ("a/link", "symlink", "b.txt"),
                                       ("a/up", "symlink", "../a/b.txt"), ("a/hard", "hardlink", "a/b.txt")]), [])

    def test_hostile_members_are_refused(self):
        cases = {
            "absolute name": [("/etc/passwd", "file", None)],
            "parent traversal": [("../outside", "file", None)],
            "nested traversal": [("a/../../outside", "file", None)],
            "symlink out": [("l", "symlink", "../../etc")],
            "absolute symlink": [("l", "symlink", "/etc/shadow")],
            "deep symlink out": [("a/b/l", "symlink", "../../../x")],
            "hard link out": [("h", "hardlink", "../../etc/passwd")],
            "absolute hard link": [("h", "hardlink", "/etc/passwd")],
            "device file": [("dev", "chardev", None)],
            "fifo": [("pipe", "fifo", None)],
            "overlong name": [("a" * (rc.MAX_NAME + 1), "file", None)],
        }
        for label, entries in cases.items():
            self.assertTrue(self.verdict(entries), f"{label} was accepted")

    def test_an_unreadable_archive_is_refused(self):
        path = Path(tempfile.mkstemp()[1])
        path.write_bytes(b"not a tar")
        self.addCleanup(path.unlink)
        self.assertTrue(rc.check_tar(str(path)))

    def test_cli_exit_codes(self):
        good = tar_with([("a.txt", "file", None)])
        bad = tar_with([("../x", "file", None)])
        self.addCleanup(good.unlink)
        self.addCleanup(bad.unlink)
        script = str(ROOT / "scripts" / "lib" / "restore_check.py")
        self.assertEqual(subprocess.run([sys.executable, "-B", script, "tar", str(good)], capture_output=True).returncode, 0)
        refused = subprocess.run([sys.executable, "-B", script, "tar", str(good), str(bad)], capture_output=True, text=True)
        self.assertEqual(refused.returncode, 1)
        self.assertIn("escapes the extraction root", refused.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
