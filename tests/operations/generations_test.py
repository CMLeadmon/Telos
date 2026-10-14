#!/usr/bin/env python3
"""Generations, the activation pointer, receipts, the operation journal and recovery planning.

Hermetic: generations are small directory trees built in a temporary release root. Ownership is
not asserted here (tests do not run as root); the production CLI always checks it.
"""

import contextlib
import io
import json
import os
import stat
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import generations as gen  # noqa: E402
import release_graph as rg  # noqa: E402

HOST = "sha256:" + "a" * 64
OLD = "1" * 64
NEW = "2" * 64
OTHER = "3" * 64


def manifest(version="0.1.0-beta.1", lo=1, current=24, hi=24, compatible=24, predecessors=(), files=()):
    return {"schemaVersion": 1, "sourceCommit": "4" * 40, "version": version, "images": {"core": "sha256:" + "5" * 64},
            "migrations": {"min": lo, "current": current, "max": hi, "checksumSetDigest": "sha256:" + "6" * 64},
            "predecessorIds": sorted(predecessors), "rollbackCompatibleThrough": compatible,
            "artifactIndexSubset": sorted(({"path": p, "sha256": rg.raw_sha256(c)} for p, c in files), key=lambda r: r["path"])}


class Fixture(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = os.path.realpath(self.tmp.name) + "/srv"
        self.node = os.path.realpath(self.tmp.name) + "/node"
        os.makedirs(self.root + "/generations")
        os.makedirs(self.node + "/state", mode=0o700)

    def make(self, gid, **kwargs):
        files = kwargs.pop("files", [("images.env", b"CORE=sha256:" + b"5" * 64 + b"\n")])
        path = f"{self.root}/generations/{gid}"
        os.makedirs(path, mode=0o755, exist_ok=True)
        for name, content in files:
            Path(path, name).write_bytes(content)
            os.chmod(Path(path, name), 0o644)
        Path(path, "generation.json").write_bytes(rg.canonical_bytes(manifest(files=files, **kwargs)))
        os.chmod(Path(path, "generation.json"), 0o644)
        os.chmod(path, 0o755)
        return path

    def verify(self, gid):
        return gen.verify_generation(self.root, gid, require_root_owner=False)


class GenerationTests(Fixture):
    def test_a_correct_generation_verifies(self):
        self.make(OLD)
        parsed, digest = self.verify(OLD)
        self.assertEqual(parsed["version"], "0.1.0-beta.1")
        self.assertTrue(digest.startswith("sha256:"))

    def test_each_corruption_is_refused(self):
        def tamper(change):
            self.make(OLD)
            change(f"{self.root}/generations/{OLD}")
            with self.assertRaises(gen.GenerationError):
                self.verify(OLD)
            import shutil
            shutil.rmtree(f"{self.root}/generations/{OLD}")

        tamper(lambda p: Path(p, "images.env").write_bytes(b"CORE=other\n"))
        tamper(lambda p: os.unlink(Path(p, "images.env")))
        tamper(lambda p: os.chmod(p, 0o775))
        tamper(lambda p: os.chmod(Path(p, "images.env"), 0o666))
        tamper(lambda p: os.symlink("/etc/passwd", Path(p, "link")))
        tamper(lambda p: Path(p, "generation.json").write_bytes(b"{}"))
        tamper(lambda p: os.unlink(Path(p, "generation.json")))

    def test_ids_must_be_candidate_digest_hex_and_stay_beneath_the_root(self):
        for bad in ("../x", "abc", "G" * 64, "1" * 63, ""):
            with self.assertRaises(gen.GenerationError):
                gen.generation_path(self.root, bad)
        with self.assertRaises(gen.GenerationError):
            gen.generations_dir("relative/root")
        outside = os.path.realpath(self.tmp.name) + "/elsewhere"
        os.makedirs(outside, mode=0o755)
        Path(outside, "generation.json").write_bytes(rg.canonical_bytes(manifest()))
        os.symlink(outside, f"{self.root}/generations/{OLD}")
        with self.assertRaisesRegex(gen.GenerationError, "real directory"):
            self.verify(OLD)

    def test_a_generations_directory_that_is_a_symlink_is_refused(self):
        os.rmdir(self.root + "/generations")
        os.symlink(self.tmp.name, self.root + "/generations")
        with self.assertRaises(gen.GenerationError):
            gen.generations_dir(self.root)

    def test_activation_swaps_atomically_and_refuses_a_missing_target(self):
        self.make(OLD)
        self.make(NEW)
        self.assertIsNone(gen.current_generation(self.root))
        gen.activate(self.root, OLD)
        self.assertEqual(gen.current_generation(self.root), OLD)
        gen.activate(self.root, NEW)
        self.assertEqual(os.readlink(self.root + "/current"), f"generations/{NEW}")
        with self.assertRaises(gen.GenerationError):
            gen.activate(self.root, OTHER)
        self.assertEqual(gen.current_generation(self.root), NEW, "a refused activation leaves the pointer alone")
        self.assertEqual([n for n in os.listdir(self.root) if n.startswith(".current")], [], "no temporary link is left behind")

    def test_a_current_pointer_that_is_not_a_generation_is_refused(self):
        os.symlink("/etc", self.root + "/current")
        with self.assertRaisesRegex(gen.GenerationError, "not a generation"):
            gen.current_generation(self.root)
        os.unlink(self.root + "/current")
        os.symlink(f"generations/{OLD}", self.root + "/current")
        with self.assertRaisesRegex(gen.GenerationError, "does not exist"):
            gen.current_generation(self.root)


class ReceiptTests(Fixture):
    def test_a_receipt_binds_candidate_manifest_and_path(self):
        self.make(OLD)
        receipt = gen.write_receipt(self.root, self.node, OLD, require_root_owner=False)
        self.assertEqual(receipt["candidateLockDigest"], f"sha256:{OLD}")
        self.assertEqual(stat.S_IMODE(os.stat(self.node + "/state/installed-generation.json").st_mode), 0o600)
        gen.check_receipt(self.root, self.node, OLD, require_root_owner=False)

    def test_a_receipt_for_another_generation_or_a_changed_manifest_is_refused(self):
        self.make(OLD)
        self.make(NEW, version="0.1.0-beta.2")
        gen.write_receipt(self.root, self.node, OLD, require_root_owner=False)
        with self.assertRaisesRegex(gen.GenerationError, "different candidate"):
            gen.check_receipt(self.root, self.node, NEW, require_root_owner=False)
        Path(f"{self.root}/generations/{OLD}/generation.json").write_bytes(rg.canonical_bytes(manifest(version="0.1.0-beta.9")))
        with self.assertRaisesRegex(gen.GenerationError, "manifest digest"):
            gen.check_receipt(self.root, self.node, OLD, require_root_owner=False)

    def test_a_missing_receipt_is_refused(self):
        self.make(OLD)
        with self.assertRaisesRegex(gen.GenerationError, "missing or invalid"):
            gen.check_receipt(self.root, self.node, OLD, require_root_owner=False)


class CompatibilityTests(unittest.TestCase):
    def test_upgrade_needs_a_listed_predecessor_and_a_schema_inside_the_window(self):
        target = manifest(lo=20, current=24, hi=24, predecessors=[f"sha256:{OLD}"])
        gen.check_upgrade(None, OLD, target, 22)
        with self.assertRaisesRegex(gen.GenerationError, "supported predecessor"):
            gen.check_upgrade(None, OTHER, target, 22)
        for schema in (19, 25):
            with self.assertRaisesRegex(gen.GenerationError, "outside the target's supported window"):
                gen.check_upgrade(None, OLD, target, schema)

    def test_fast_rollback_needs_the_schema_inside_the_previous_binaries_tolerance(self):
        previous = manifest(lo=10, current=22, hi=22)
        following = manifest(lo=10, current=24, hi=24, compatible=23)
        self.assertTrue(gen.fast_rollback_ok(following, previous, 23))
        self.assertTrue(gen.fast_rollback_ok(following, previous, 22))
        self.assertFalse(gen.fast_rollback_ok(following, previous, 24), "a migration beyond what the old binaries tolerate")
        self.assertFalse(gen.fast_rollback_ok(following, previous, 9), "older than the previous binaries can start on")


class JournalTests(Fixture):
    def begin(self, kind="upgrade", **kwargs):
        return gen.begin(self.node, kind, kwargs.get("from_id", OLD), kwargs.get("to_id", NEW), 22, HOST, kwargs.get("supersede"))

    def test_an_upgrade_walks_its_states_and_is_durable(self):
        record = self.begin()
        self.assertEqual((record["state"], record["snapshotId"], record["postSchema"]), ("started", None, None))
        self.assertEqual(stat.S_IMODE(os.stat(self.node + "/state/operation.json").st_mode), 0o600)
        gen.advance(self.node, "snapshot-verified", snapshot="abcdef12")
        gen.advance(self.node, "maintenance")
        gen.advance(self.node, "migrating", post_schema=24)
        gen.advance(self.node, "migrated")
        gen.advance(self.node, "activated")
        gen.advance(self.node, "serving")
        done = gen.advance(self.node, "complete")
        self.assertEqual((done["state"], done["snapshotId"], done["postSchema"]), ("complete", "abcdef12", 24))
        self.assertEqual(gen.read_journal(self.node), done)
        self.assertEqual([n for n in os.listdir(self.node + "/state") if n.startswith(".operation")], [])

    def test_states_cannot_be_skipped_or_revisited_and_terminals_are_final(self):
        self.begin()
        for illegal in ("maintenance", "migrated", "complete", "serving", "started"):
            with self.assertRaisesRegex(gen.GenerationError, "cannot move"):
                gen.advance(self.node, illegal)
        gen.advance(self.node, "aborted")
        with self.assertRaisesRegex(gen.GenerationError, "already ended"):
            gen.advance(self.node, "snapshot-verified")

    def test_migrating_is_not_abortable_after_the_schema_moved_but_may_demand_a_restore(self):
        self.begin()
        gen.advance(self.node, "snapshot-verified", snapshot="abcdef12")
        gen.advance(self.node, "maintenance")
        gen.advance(self.node, "migrating", post_schema=24)
        self.assertEqual(gen.advance(self.node, "restore-required", error="partial migration")["state"], "restore-required")

    def test_a_new_operation_waits_for_the_last_to_finish_and_blocking_ends_need_acknowledgement(self):
        first = self.begin()
        with self.assertRaisesRegex(gen.GenerationError, "unfinished"):
            self.begin()
        gen.advance(self.node, "failed", error="stack would not start")
        with self.assertRaisesRegex(gen.GenerationError, "acknowledge"):
            self.begin()
        with self.assertRaisesRegex(gen.GenerationError, "acknowledge"):
            self.begin(supersede="f" * 32)
        second = self.begin(supersede=first["operationId"])
        self.assertNotEqual(second["operationId"], first["operationId"])
        archived = json.loads(Path(self.node, "state", "operations", first["operationId"] + ".json").read_text())
        self.assertEqual((archived["state"], archived["lastError"]), ("failed", "stack would not start"))

    def test_a_completed_operation_is_archived_without_acknowledgement(self):
        self.begin(kind="rollback")
        for state in ("maintenance", "activated", "serving", "complete"):
            gen.advance(self.node, state)
        self.begin()
        self.assertEqual(len(os.listdir(self.node + "/state/operations")), 1)

    def test_a_corrupt_or_unknown_journal_stops_everything(self):
        self.begin()
        Path(self.node, "state", "operation.json").write_text("{not json")
        for call in (lambda: gen.read_journal(self.node), lambda: self.begin(), lambda: gen.advance(self.node, "snapshot-verified")):
            with self.assertRaisesRegex(gen.GenerationError, "investigate|invalid"):
                call()
        record = json.loads(rg.canonical_bytes({}).decode() or "{}")
        self.assertEqual(record, {})

    def test_the_journal_rejects_impossible_records(self):
        record = self.begin()
        for change in ({"state": "bogus"}, {"fromGeneration": NEW}, {"kind": "rollback", "fromGeneration": None},
                       {"state": "migrated", "postSchema": None}, {"state": "maintenance", "snapshotId": None}, {"extra": 1},
                       {"updatedAt": "2000-01-01T00:00:00Z"}, {"state": "restore-required", "kind": "rollback", "fromGeneration": None}):
            with self.subTest(change), self.assertRaises(rg.GraphError):
                rg.validate_record("operation-journal", dict(record, **change))

    def test_a_restore_resolves_only_a_blocked_operation_of_the_last_id(self):
        first = self.begin()
        with self.assertRaisesRegex(gen.GenerationError, "can be resolved"):
            gen.resolve(self.node, first["operationId"])
        with self.assertRaises(rg.GraphError):   # nothing to restore before a snapshot exists
            gen.advance(self.node, "restore-required")
        gen.advance(self.node, "snapshot-verified", snapshot="abcdef12")
        gen.advance(self.node, "restore-required", error="old binaries cannot run")
        with self.assertRaisesRegex(gen.GenerationError, "last operation"):
            gen.resolve(self.node, "f" * 32)
        done = gen.resolve(self.node, first["operationId"])
        self.assertEqual(done["state"], "restored")
        with self.assertRaisesRegex(gen.GenerationError, "already ended"):
            gen.advance(self.node, "failed")
        self.begin()   # a restored operation no longer blocks the next one

    def test_a_crash_between_temp_file_and_rename_leaves_the_last_journal_intact(self):
        self.begin()
        Path(self.node, "state", ".operation.json.123.deadbeef").write_text("{half")
        self.assertEqual(gen.read_journal(self.node)["state"], "started")


class RecoveryPlanTests(unittest.TestCase):
    def journal(self, state, kind="upgrade", pre=22, post=24, snapshot="abcdef12"):
        stamp = "2026-10-14T05:00:00Z"
        return {"schemaVersion": 1, "operationId": "e" * 32, "kind": kind, "hostIdSha256": HOST, "fromGeneration": OLD, "toGeneration": NEW,
                "snapshotId": snapshot, "preSchema": pre, "postSchema": post, "state": state, "startedAt": stamp, "updatedAt": stamp, "lastError": None}

    def plan(self, state, schema, ready=None, compatible=True, **kwargs):
        previous = manifest(lo=10, current=22, hi=22)
        following = manifest(lo=10, current=24, hi=24, compatible=24 if compatible else 22)
        return gen.plan_recovery(self.journal(state, **kwargs), schema, ready, {"previous": previous, "next": following})["action"]

    def test_nothing_to_do_without_or_after_an_operation(self):
        self.assertEqual(gen.plan_recovery(None, 22)["action"], "none")
        for state in gen.TERMINAL:
            self.assertEqual(self.plan(state, 22), "none")

    def test_before_any_migration_the_answer_is_always_abort(self):
        for state in ("started", "snapshot-verified", "maintenance"):
            for schema in (22, 23, 24):
                self.assertEqual(self.plan(state, schema), "abort", (state, schema))

    def test_interrupted_migration_is_decided_by_the_observed_schema_and_never_guessed(self):
        self.assertEqual(self.plan("migrating", 22), "abort")
        self.assertEqual(self.plan("migrating", 24), "continue")
        self.assertEqual(self.plan("migrating", 23), "restore")
        self.assertEqual(self.plan("migrating", 30), "restore")
        self.assertEqual(self.plan("migrating", 24, post=None), "restore")

    def test_migrated_continues_only_when_the_schema_is_the_recorded_one(self):
        self.assertEqual(self.plan("migrated", 24), "continue")
        self.assertEqual(self.plan("migrated", 22), "restore")

    def test_an_active_target_is_judged_by_readiness_then_by_compatibility(self):
        for state in ("activated", "serving"):
            self.assertEqual(self.plan(state, 24), "verify")
            self.assertEqual(self.plan(state, 24, ready=True), "none")
            self.assertEqual(self.plan(state, 24, ready=False, compatible=True), "rollback")
            self.assertEqual(self.plan(state, 24, ready=False, compatible=False), "restore")

    def test_without_manifests_an_unready_target_can_only_be_restored(self):
        self.assertEqual(gen.plan_recovery(self.journal("serving"), 24, False, None)["action"], "restore")

    def test_a_rollback_never_continues_past_an_unexplained_state(self):
        self.assertEqual(self.plan("started", 24, kind="rollback"), "abort")
        self.assertEqual(self.plan("maintenance", 24, kind="rollback"), "abort")
        self.assertEqual(self.plan("activated", 24, kind="rollback"), "verify")
        self.assertEqual(self.plan("activated", 24, kind="rollback", ready=True), "none")
        self.assertEqual(self.plan("activated", 24, kind="rollback", ready=False), "restore")

    def test_every_upgrade_state_has_a_decision_for_every_schema_in_range(self):
        for state in rg.UPGRADE_STATES:
            for schema in range(20, 27):
                for ready in (None, True, False):
                    action = self.plan(state, schema, ready)
                    self.assertIn(action, ("none", "abort", "continue", "verify", "rollback", "restore"))


class CommandLineTests(Fixture):
    def run_cli(self, *argv):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = gen.main(list(argv))
        return code, (json.loads(out.getvalue()) if out.getvalue().strip() else None), err.getvalue()

    def test_verify_activate_and_current(self):
        self.make(OLD)
        code, doc, _ = self.run_cli("verify", "--root", self.root, "--id", OLD, "--no-owner-check")
        self.assertEqual((code, doc["id"]), (0, OLD))
        self.assertEqual(self.run_cli("current", "--root", self.root)[1], {"current": None})
        self.assertEqual(self.run_cli("activate", "--root", self.root, "--id", OLD)[1], {"current": OLD})
        code, _, err = self.run_cli("verify", "--root", self.root, "--id", NEW, "--no-owner-check")
        self.assertEqual(code, 1)
        self.assertIn("not installed", err)

    def test_the_root_ownership_requirement_is_on_by_default(self):
        self.make(OLD)
        if os.geteuid() == 0:
            self.skipTest("running as root")
        code, _, err = self.run_cli("verify", "--root", self.root, "--id", OLD)
        self.assertEqual(code, 1)
        self.assertIn("not owned by root", err)

    def test_upgrade_check_and_journal_round_trip(self):
        self.make(OLD)
        self.make(NEW, predecessors=[f"sha256:{OLD}"], version="0.1.0-beta.2")
        self.assertEqual(self.run_cli("check-upgrade", "--root", self.root, "--from", OLD, "--to", NEW, "--schema", "22", "--no-owner-check")[0], 0)
        self.assertEqual(self.run_cli("check-upgrade", "--root", self.root, "--from", NEW, "--to", OLD, "--schema", "22", "--no-owner-check")[0], 1)
        code, record, _ = self.run_cli("journal", "begin", "--node", self.node, "--kind", "upgrade", "--from", OLD, "--to", NEW,
                                       "--schema", "22", "--host", HOST)
        self.assertEqual((code, record["state"]), (0, "started"))
        self.assertEqual(self.run_cli("journal", "advance", "--node", self.node, "--state", "snapshot-verified", "--snapshot", "abcdef12")[0], 0)
        self.assertEqual(self.run_cli("journal", "show", "--node", self.node)[1]["snapshotId"], "abcdef12")
        code, plan, _ = self.run_cli("plan-recovery", "--root", self.root, "--node", self.node, "--schema", "22", "--no-owner-check")
        self.assertEqual((code, plan["action"]), (0, "abort"))
        self.assertEqual(self.run_cli("journal", "advance", "--node", self.node, "--state", "complete")[0], 1)


if __name__ == "__main__":
    unittest.main(verbosity=1)
