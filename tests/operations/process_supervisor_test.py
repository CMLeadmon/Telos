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


def emergency_stop_recorded_processes(state_dir):
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

    def assert_recorded_processes_gone(self, state_dir):
        pid_files = sorted(state_dir.glob("*.pid"))
        self.assertTrue(pid_files, "fixture did not record any process IDs")
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            if all(
                not Path(f"/proc/{int(pid_file.read_text(encoding='ascii'))}").exists()
                for pid_file in pid_files
            ):
                break
            time.sleep(0.01)
        for pid_file in pid_files:
            pid = int(pid_file.read_text(encoding="ascii"))
            self.assertFalse(
                Path(f"/proc/{pid}").exists(),
                f"{pid_file.name} process {pid} was not reaped",
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

    def test_launcher_preserves_target_argv_and_inherited_environment(self):
        # Mutation caught: serializing or rewriting argv/environment in the launcher.
        with mock.patch.dict(
            supervisor.os.environ,
            {"TELOS_SUPERVISOR_HANDSHAKE_TEST": "inherited-value"},
        ):
            result = supervisor.run(
                [
                    sys.executable,
                    "-c",
                    (
                        "import os,sys; "
                        "print(repr(sys.argv[1:])); "
                        "print(os.environ['TELOS_SUPERVISOR_HANDSHAKE_TEST'])"
                    ),
                    "literal argument with spaces",
                    "$(not-executed)",
                ],
                cwd=REPO_ROOT,
                timeout_seconds=5,
                cooperative_grace_seconds=1,
                signals=supervisor.SignalState(),
            )

        self.assertEqual(result.cause, "exited")
        self.assertEqual(result.return_code, 0)
        self.assertEqual(
            result.output,
            b"['literal argument with spaces', '$(not-executed)']\n"
            b"inherited-value\n",
        )

    def test_launcher_preserves_executable_not_found_error(self):
        # Mutation caught: translating target exec failure into launcher exit 127.
        with self.assertRaises(FileNotFoundError):
            supervisor.run(
                ["/definitely/missing/telos-supervised-command"],
                cwd=REPO_ROOT,
                timeout_seconds=5,
                cooperative_grace_seconds=1,
                signals=supervisor.SignalState(),
            )

    def test_support_preflight_exercises_pidfd_signal_zero_and_closes_it(self):
        # Mutation caught: checking only that Python exposes the pidfd callables.
        opened_pidfds = []
        signal_calls = []
        real_pidfd_open = supervisor.os.pidfd_open
        real_pidfd_send_signal = supervisor.signal.pidfd_send_signal

        def tracking_pidfd_open(pid, flags):
            descriptor = real_pidfd_open(pid, flags)
            opened_pidfds.append(descriptor)
            return descriptor

        def tracking_pidfd_send_signal(pidfd, signum, siginfo, flags):
            signal_calls.append((pidfd, signum, siginfo, flags))
            return real_pidfd_send_signal(pidfd, signum, siginfo, flags)

        with mock.patch.object(
            supervisor.os, "pidfd_open", side_effect=tracking_pidfd_open
        ), mock.patch.object(
            supervisor.signal,
            "pidfd_send_signal",
            side_effect=tracking_pidfd_send_signal,
        ):
            supervisor.require_support()

        self.assertEqual(len(opened_pidfds), 1)
        self.assertEqual(signal_calls, [(opened_pidfds[0], 0, None, 0)])
        with self.assertRaises(OSError):
            supervisor.os.fstat(opened_pidfds[0])

    def test_support_preflight_fails_when_pidfd_signaling_is_not_operational(self):
        # Mutation caught: accepting a host where pidfd signaling is denied at runtime.
        opened_pidfds = []
        real_pidfd_open = supervisor.os.pidfd_open

        def tracking_pidfd_open(pid, flags):
            descriptor = real_pidfd_open(pid, flags)
            opened_pidfds.append(descriptor)
            return descriptor

        with mock.patch.object(
            supervisor.os, "pidfd_open", side_effect=tracking_pidfd_open
        ), mock.patch.object(
            supervisor.signal,
            "pidfd_send_signal",
            side_effect=PermissionError("forced pidfd signal denial"),
        ):
            with self.assertRaisesRegex(
                supervisor.SupervisionError, "pidfd signaling preflight failed"
            ):
                supervisor.require_support()

        self.assertEqual(len(opened_pidfds), 1)
        with self.assertRaises(OSError):
            supervisor.os.fstat(opened_pidfds[0])

    def test_live_leader_bind_failure_never_executes_the_target(self):
        # Mutation caught: allowing the target to execute before leader pidfd binding.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            side_effect = state_dir / "target-ran"
            target_pid = state_dir / "target.pid"
            launched = []
            real_popen = supervisor.subprocess.Popen
            real_pidfd_send_signal = supervisor.signal.pidfd_send_signal

            def tracking_popen(*args, **kwargs):
                process = real_popen(*args, **kwargs)
                launched.append(process)
                return process

            escaped_process = False
            try:
                with mock.patch.object(
                    supervisor.subprocess, "Popen", side_effect=tracking_popen
                ), mock.patch.object(
                    supervisor, "_wait_for_identity", return_value=None
                ), mock.patch.object(
                    supervisor, "_open_identity", return_value=None
                ):
                    result = supervisor.run(
                        [
                            sys.executable,
                            "-c",
                            (
                                "import os,pathlib,sys,time; "
                                "pathlib.Path(sys.argv[1]).write_text('ran\\n'); "
                                "pathlib.Path(sys.argv[2]).write_text(str(os.getpid())); "
                                "time.sleep(30)"
                            ),
                            str(side_effect),
                            str(target_pid),
                        ],
                        cwd=REPO_ROOT,
                        timeout_seconds=5,
                        cooperative_grace_seconds=0,
                        signals=supervisor.SignalState(),
                    )
                escaped_process = bool(launched and launched[0].poll() is None)
            finally:
                if launched and launched[0].poll() is None:
                    pidfd = supervisor.os.pidfd_open(launched[0].pid, 0)
                    try:
                        real_pidfd_send_signal(pidfd, signal.SIGKILL, None, 0)
                    finally:
                        supervisor.os.close(pidfd)
                    launched[0].wait(timeout=2)

            self.assertEqual(result.cause, "capture_error")
            self.assertFalse(escaped_process, "unbound launcher survived run()")
            self.assertFalse(side_effect.exists(), "target executed before pidfd bind")
            self.assertFalse(target_pid.exists(), "target process recorded a PID")

    def test_inert_launcher_ignores_python_startup_injection_before_binding(self):
        # Mutation caught: importing inherited PYTHONPATH code before pidfd binding.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            launcher_side_effect = state_dir / "launcher-startup-ran"
            (state_dir / "sitecustomize.py").write_text(
                "import pathlib\n"
                f"pathlib.Path({str(launcher_side_effect)!r}).write_text('ran\\n')\n",
                encoding="utf-8",
            )

            with mock.patch.dict(
                supervisor.os.environ, {"PYTHONPATH": str(state_dir)}
            ), mock.patch.object(
                supervisor, "_wait_for_identity", return_value=None
            ), mock.patch.object(
                supervisor, "_open_identity", return_value=None
            ):
                result = supervisor.run(
                    [sys.executable, "-c", "raise SystemExit(0)"],
                    cwd=REPO_ROOT,
                    timeout_seconds=5,
                    cooperative_grace_seconds=0,
                    signals=supervisor.SignalState(),
                )

            self.assertEqual(result.cause, "capture_error")
            self.assertFalse(
                launcher_side_effect.exists(),
                "launcher imported inherited startup code before pidfd bind",
            )

    def test_timeout_kills_leader_and_stubborn_child_within_bound(self):
        # Mutation caught: timing out only the leader and leaking its TERM-ignoring child.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("timeout", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_gone(state_dir)

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
            self.assert_recorded_processes_gone(state_dir)

    def test_setsid_child_cannot_escape_timeout_cleanup(self):
        # Mutation caught: limiting cleanup to the leader's original process group.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("setsid", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_gone(state_dir)

    def test_double_forked_grandchild_cannot_escape_timeout_cleanup(self):
        # Mutation caught: losing lineage when an intermediate child exits after a fork.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            started = time.monotonic()
            result = self.run_fixture("double-fork", state_dir)
            elapsed = time.monotonic() - started

            self.assertEqual(result.cause, "timeout")
            self.assertLess(elapsed, 8)
            self.assert_recorded_processes_gone(state_dir)

    def test_post_death_quiescence_catches_a_child_forked_after_the_last_scan(self):
        # Mutation caught: accepting one empty snapshot after the leader pidfd dies.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            release = state_dir / "release"
            real_scan_processes = supervisor._scan_processes
            released_after_snapshot = False

            def release_after_stale_snapshot():
                nonlocal released_after_snapshot
                process_stats = real_scan_processes()
                leader_path = state_dir / "leader.pid"
                if leader_path.exists() and not released_after_snapshot:
                    leader_pid = int(leader_path.read_text(encoding="ascii"))
                    if leader_pid in process_stats:
                        released_after_snapshot = True
                        release.write_text("fork now\n", encoding="ascii")
                        deadline = time.monotonic() + 2
                        while time.monotonic() < deadline:
                            stat = supervisor._read_process_stat(leader_pid)
                            if stat is None or stat[0] == "Z":
                                break
                            time.sleep(0.001)
                return process_stats

            escaped_process = False
            try:
                with mock.patch.object(
                    supervisor, "_scan_processes", side_effect=release_after_stale_snapshot
                ):
                    result = self.run_fixture(
                        "late-fork-at-exit", state_dir, timeout_seconds=5
                    )
                escaped_process = any(
                    not process_is_dead(int(pid_path.read_text(encoding="ascii")))
                    for pid_path in state_dir.glob("*.pid")
                )
            finally:
                emergency_stop_recorded_processes(state_dir)

            self.assertTrue(released_after_snapshot)
            self.assertEqual(result.cause, "descendants")
            self.assertEqual(result.return_code, 0)
            self.assertFalse(escaped_process, "late-forked child survived run()")
            self.assert_recorded_processes_gone(state_dir)

    def test_adopted_zombie_is_reaped_before_success_returns(self):
        # Mutation caught: reaping only children that previously acquired pidfds.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            unreaped_process = False
            try:
                result = self.run_fixture(
                    "zombie-at-exit", state_dir, timeout_seconds=5
                )
                unreaped_process = any(
                    Path(
                        f"/proc/{int(pid_path.read_text(encoding='ascii'))}"
                    ).exists()
                    for pid_path in state_dir.glob("*.pid")
                )
            finally:
                emergency_stop_recorded_processes(state_dir)

            self.assertEqual(result.cause, "exited")
            self.assertEqual(result.return_code, 0)
            self.assertFalse(unreaped_process, "adopted zombie survived run()")
            self.assert_recorded_processes_gone(state_dir)

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
            self.assert_recorded_processes_gone(state_dir)

    def test_capture_setup_failure_keeps_target_inert_and_closes_resources(self):
        # Mutation caught: releasing the target before capture setup is complete.
        class FailingSelector:
            def __init__(self, state_dir):
                self.state_dir = state_dir
                self.closed = False

            def register(self, _stream, _events):
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
            self.assertEqual(list(state_dir.glob("*.pid")), [])

    def test_target_starts_only_after_verified_launcher_binding(self):
        # Mutation caught: executing the target while its launcher is still unbound.
        with tempfile.TemporaryDirectory() as temporary_dir:
            state_dir = Path(temporary_dir)
            real_wait_for_identity = supervisor._wait_for_identity
            target_was_inert = False

            def bind_before_release(process):
                nonlocal target_was_inert
                target_was_inert = not (state_dir / "leader.pid").exists()
                return real_wait_for_identity(process)

            with mock.patch.object(
                supervisor, "_wait_for_identity", side_effect=bind_before_release
            ):
                result = self.run_fixture(
                    "leader-exit", state_dir, timeout_seconds=5
                )

            self.assertTrue(target_was_inert)
            self.assertEqual(result.cause, "descendants")
            self.assert_recorded_processes_gone(state_dir)

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
