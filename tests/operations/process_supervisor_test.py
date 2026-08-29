#!/usr/bin/env python3
"""Behavioral contract for the Linux gate-process supervisor."""

import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from collections import Counter
from pathlib import Path
from unittest import mock


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


def wait_for_files(paths, timeout_seconds=2):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if all(path.exists() for path in paths):
            return
        time.sleep(0.01)


def emergency_stop_recorded_processes(state_dir):
    wait_for_files([state_dir / "leader.pid", state_dir / "child.pid"])
    pids = []
    for pid_file in state_dir.glob("*.pid"):
        pid = int(pid_file.read_text(encoding="ascii"))
        pids.append(pid)
        try:
            pidfd = supervisor.os.pidfd_open(pid, 0)
        except ProcessLookupError:
            continue
        try:
            supervisor.signal.pidfd_send_signal(pidfd, signal.SIGKILL, None, 0)
        except ProcessLookupError:
            pass
        finally:
            supervisor.os.close(pidfd)

    deadline = time.monotonic() + 2
    while time.monotonic() < deadline and any(
        not process_is_dead(pid) for pid in pids
    ):
        time.sleep(0.01)
    for pid in pids:
        try:
            supervisor.os.waitpid(pid, supervisor.os.WNOHANG)
        except ChildProcessError:
            pass


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

    def test_capture_setup_failure_still_contains_tree_and_closes_resources(self):
        # Mutation caught: acquiring post-spawn resources before the cleanup boundary.
        class FailingSelector:
            def __init__(self, state_dir):
                self.state_dir = state_dir
                self.closed = False

            def register(self, _stream, _events):
                wait_for_files(
                    [self.state_dir / "leader.pid", self.state_dir / "child.pid"]
                )
                raise OSError("forced selector registration failure")

            def close(self):
                self.closed = True

        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            selector = FailingSelector(state_dir)
            opened_pidfds = []
            closed_descriptors = []
            real_pidfd_open = supervisor.os.pidfd_open
            real_close = supervisor.os.close

            def tracking_pidfd_open(pid, flags):
                descriptor = real_pidfd_open(pid, flags)
                opened_pidfds.append(descriptor)
                return descriptor

            def tracking_close(descriptor):
                closed_descriptors.append(descriptor)
                return real_close(descriptor)

            escaped_error = None
            result = None
            try:
                with mock.patch.object(
                    supervisor.selectors,
                    "DefaultSelector",
                    return_value=selector,
                ), mock.patch.object(
                    supervisor.os, "pidfd_open", side_effect=tracking_pidfd_open
                ), mock.patch.object(
                    supervisor.os, "close", side_effect=tracking_close
                ):
                    try:
                        result = self.run_fixture(
                            "timeout", state_dir, timeout_seconds=5
                        )
                    except OSError as error:
                        escaped_error = error
            finally:
                emergency_stop_recorded_processes(state_dir)
                opened_counts = Counter(opened_pidfds)
                closed_counts = Counter(closed_descriptors)
                for descriptor, count in opened_counts.items():
                    for _index in range(max(0, count - closed_counts[descriptor])):
                        try:
                            real_close(descriptor)
                        except OSError:
                            pass

            self.assertIsNone(
                escaped_error,
                f"post-spawn setup error escaped cleanup: {escaped_error}",
            )
            self.assertIsNotNone(result)
            self.assertEqual(result.cause, "capture_error")
            self.assertIn("forced selector registration failure", result.cleanup_errors)
            self.assertTrue(selector.closed)
            for descriptor, count in Counter(opened_pidfds).items():
                self.assertGreaterEqual(Counter(closed_descriptors)[descriptor], count)
            self.assert_recorded_processes_dead(state_dir)

    def test_leader_exit_before_binding_cleans_only_adopted_identity(self):
        # Mutation caught: requiring a leader pidfd before discovering adopted children.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            opened_pids = {}
            signaled_pids = []
            real_pidfd_open = supervisor.os.pidfd_open
            real_pidfd_send_signal = supervisor.signal.pidfd_send_signal

            def wait_until_leader_exits(process):
                deadline = time.monotonic() + 2
                while process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertIsNotNone(process.returncode)
                return None

            def tracking_pidfd_open(pid, flags):
                descriptor = real_pidfd_open(pid, flags)
                opened_pids[descriptor] = pid
                return descriptor

            def tracking_pidfd_send_signal(pidfd, signum, siginfo, flags):
                signaled_pids.append(opened_pids[pidfd])
                return real_pidfd_send_signal(pidfd, signum, siginfo, flags)

            with mock.patch.object(
                supervisor, "_wait_for_identity", side_effect=wait_until_leader_exits
            ), mock.patch.object(
                supervisor.os, "pidfd_open", side_effect=tracking_pidfd_open
            ), mock.patch.object(
                supervisor.signal,
                "pidfd_send_signal",
                side_effect=tracking_pidfd_send_signal,
            ):
                result = self.run_fixture(
                    "leader-exit", state_dir, timeout_seconds=5
                )

            leader_pid = int((state_dir / "leader.pid").read_text(encoding="ascii"))
            self.assertEqual(result.cause, "descendants")
            self.assertNotIn(leader_pid, opened_pids.values())
            self.assertNotIn(leader_pid, signaled_pids)
            self.assert_recorded_processes_dead(state_dir)

    def test_unbound_numeric_leader_is_not_promoted_or_signaled(self):
        # Mutation caught: seeding lineage from a bare leader PID after binding failed.
        unrelated = subprocess.Popen(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            cwd=REPO_ROOT,
            shell=False,
            start_new_session=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        owned = None
        real_pidfd_send_signal = supervisor.signal.pidfd_send_signal
        try:
            process_stat = supervisor._read_process_stat(unrelated.pid)
            self.assertIsNotNone(process_stat)
            baseline_children = {unrelated.pid: process_stat[2]}
            owned = supervisor._OwnedProcesses(
                unrelated.pid, None, baseline_children
            )
            sent_pidfds = []

            def record_signal(pidfd, _signum, _siginfo, _flags):
                sent_pidfds.append(pidfd)

            with mock.patch.object(
                supervisor.signal,
                "pidfd_send_signal",
                side_effect=record_signal,
            ):
                owned.discover()
                supervisor._signal_all(owned, signal.SIGTERM, [])

            self.assertNotIn(unrelated.pid, owned.identities)
            self.assertEqual(sent_pidfds, [])
        finally:
            if owned is not None:
                owned.close()
            if unrelated.poll() is None:
                pidfd = supervisor.os.pidfd_open(unrelated.pid, 0)
                try:
                    real_pidfd_send_signal(pidfd, signal.SIGKILL, None, 0)
                finally:
                    supervisor.os.close(pidfd)
            unrelated.wait(timeout=2)


if __name__ == "__main__":
    unittest.main()
