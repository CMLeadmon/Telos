#!/usr/bin/env python3
"""Behavioral contract for the Linux gate-process supervisor."""

import signal
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
SUPERVISOR_LIB = REPO_ROOT / "scripts" / "lib"
FIXTURE = REPO_ROOT / "tests" / "fixtures" / "gates" / "supervised_process_tree.py"
sys.path.insert(0, str(SUPERVISOR_LIB))

import process_supervisor as supervisor  # noqa: E402


def process_is_dead(pid):
    try:
        stat_bytes = Path(f"/proc/{pid}/stat").read_bytes()
    except FileNotFoundError:
        return True
    command_end = stat_bytes.rfind(b")")
    if command_end < 0:
        return False
    fields = stat_bytes[command_end + 1 :].split()
    return bool(fields) and fields[0] in {b"X", b"Z"}


class ProcessSupervisorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        supervisor.require_support()
        supervisor.enable_child_subreaper()

    def run_fixture(self, mode, state_dir, *, timeout_seconds=1, signals=None):
        return supervisor.run(
            [sys.executable, str(FIXTURE), mode, str(state_dir)],
            cwd=REPO_ROOT,
            timeout_seconds=timeout_seconds,
            cooperative_grace_seconds=1,
            signals=signals or supervisor.SignalState(),
        )

    def assert_recorded_processes_dead(self, state_dir):
        pid_files = sorted(state_dir.glob("*.pid"))
        self.assertTrue(pid_files, "fixture did not record any process IDs")
        for pid_file in pid_files:
            pid = int(pid_file.read_text(encoding="ascii"))
            self.assertTrue(
                process_is_dead(pid),
                f"{pid_file.name} process {pid} survived supervisor cleanup",
            )

    def test_successful_process_returns_captured_output(self):
        # Mutation caught: returning before the leader exit/output pipe is observed.
        success = supervisor.run(
            [sys.executable, "-c", "print('supervised-ok')"],
            cwd=REPO_ROOT,
            timeout_seconds=5,
            cooperative_grace_seconds=1,
            signals=supervisor.SignalState(),
        )
        self.assertEqual(success.cause, "exited")
        self.assertEqual(success.return_code, 0)
        self.assertEqual(success.output, b"supervised-ok\n")
        self.assertEqual(success.cleanup_errors, ())

    def test_timeout_kills_leader_and_stubborn_child_within_bound(self):
        # Mutation caught: timing out only the leader and leaking its TERM-ignoring child.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("timeout", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_dead(state_dir)

    def test_zero_exit_with_live_descendant_is_a_containment_failure(self):
        # Mutation caught: accepting a zero-exit leader while its detached child survives.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture(
                "leader-exit", state_dir, timeout_seconds=5
            )
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "descendants")
            self.assertEqual(result.return_code, 0)
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_dead(state_dir)

    def test_setsid_child_cannot_escape_timeout_cleanup(self):
        # Mutation caught: limiting cleanup to the leader's original process group.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("setsid", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_dead(state_dir)

    def test_double_forked_grandchild_cannot_escape_timeout_cleanup(self):
        # Mutation caught: losing lineage when an intermediate child exits after a fork.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("double-fork", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_dead(state_dir)

    def test_interruption_waits_for_cooperative_cleanup(self):
        # Mutation caught: escalating before the leader forwards TERM and records cleanup.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            signals = supervisor.SignalState()
            signal_sent = threading.Event()

            def signal_when_ready():
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    if (state_dir / "ready").exists():
                        signals.receive(signal.SIGTERM, None)
                        signal_sent.set()
                        return
                    time.sleep(0.01)

            timer = threading.Timer(0.01, signal_when_ready)
            timer.start()
            try:
                result = self.run_fixture(
                    "cooperative", state_dir, timeout_seconds=5, signals=signals
                )
            finally:
                timer.join(timeout=6)

            self.assertTrue(signal_sent.is_set(), "fixture never became ready")
            self.assertEqual(result.cause, "interrupted")
            self.assertEqual(result.interrupted_signal, signal.SIGTERM)
            self.assertTrue((state_dir / "cleanup").exists())
            self.assert_recorded_processes_dead(state_dir)


if __name__ == "__main__":
    unittest.main()
