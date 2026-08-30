#!/usr/bin/env python3
"""Focused fault-injection contracts for gate-runner evidence boundaries."""

import hashlib
import importlib.util
import json
import os
import platform
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock


REPO_ROOT = Path(__file__).resolve().parents[2]
RUNNER_PATH = REPO_ROOT / "scripts" / "run-gates.py"
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("run_gates", RUNNER_PATH)
run_gates = importlib.util.module_from_spec(spec)
spec.loader.exec_module(run_gates)

SOURCE_COMMIT = "0" * 40
CANDIDATE_DIGEST = "sha256:" + "1" * 64


def fixture_gate(gate_id="active-probe"):
    return {
        "id": gate_id,
        "phase": 1,
        "scope": "local",
        "required": True,
        "timeoutSeconds": 5,
        "command": ["python3", "-c", "raise SystemExit(0)"],
        "prerequisites": [],
        "subjectKind": "fixture",
    }


class RunGatesTest(unittest.TestCase):
    def wait_for_path(self, path, process, timeout_seconds=5):
        deadline = time.monotonic() + timeout_seconds
        while time.monotonic() < deadline:
            if path.exists():
                return
            if process.poll() is not None:
                break
            time.sleep(0.005)
        stdout, _stderr = process.communicate(timeout=1)
        self.fail(f"runner exited before {path.name}: {stdout.decode(errors='replace')}")

    def stop_process(self, process):
        if process.poll() is None:
            os.kill(process.pid, signal.SIGKILL)
            process.wait(timeout=2)

    def execute_with_probe_result(self, result):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        evidence_dir = Path(temporary.name)
        signals = run_gates.process_supervisor.SignalState()
        with mock.patch.object(
            run_gates.process_supervisor, "run", return_value=result
        ):
            passed, interrupted = run_gates.execute_gates(
                [fixture_gate()],
                SOURCE_COMMIT,
                CANDIDATE_DIGEST,
                str(evidence_dir),
                signals,
            )
        envelope = json.loads(
            (evidence_dir / "active-probe.json").read_text(encoding="utf-8")
        )
        output = (evidence_dir / "active-probe.log").read_bytes()
        return passed, interrupted, envelope, output

    def assert_digest_matches(self, envelope, output):
        expected = "sha256:" + hashlib.sha256(output).hexdigest()
        self.assertEqual(envelope["outputDigest"], expected)

    def test_timeout_probe_cleanup_errors_are_durable(self):
        # Mutation caught: dropping cleanup diagnostics from timed-out probes.
        result = run_gates.process_supervisor.SupervisedResult(
            output=b"probe timeout bytes\n",
            return_code=None,
            cause="timeout",
            cleanup_errors=("forced timeout cleanup diagnostic",),
            interrupted_signal=None,
        )
        passed, interrupted, envelope, output = self.execute_with_probe_result(result)

        self.assertFalse(passed)
        self.assertIsNone(interrupted)
        self.assertEqual(envelope["status"], "not_run")
        self.assertEqual(envelope["exitCode"], 3)
        self.assertEqual(
            envelope["reason"], "tool version probe timed out for: python3"
        )
        self.assertEqual(
            output,
            b"not_run: tool version probe timed out for: python3\n"
            b"process cleanup failed: forced timeout cleanup diagnostic\n",
        )
        self.assert_digest_matches(envelope, output)

    def test_descendant_probe_cleanup_errors_are_durable(self):
        # Mutation caught: dropping cleanup diagnostics from descendant probe failures.
        result = run_gates.process_supervisor.SupervisedResult(
            output=b"probe descendant bytes\n",
            return_code=0,
            cause="descendants",
            cleanup_errors=("forced descendant cleanup diagnostic",),
            interrupted_signal=None,
        )
        passed, interrupted, envelope, output = self.execute_with_probe_result(result)

        self.assertFalse(passed)
        self.assertIsNone(interrupted)
        self.assertEqual(envelope["status"], "not_run")
        self.assertEqual(envelope["exitCode"], 3)
        self.assertEqual(
            envelope["reason"],
            "tool version probe left descendants running for: python3",
        )
        self.assertEqual(
            output,
            b"not_run: tool version probe left descendants running for: python3\n"
            b"process cleanup failed: forced descendant cleanup diagnostic\n",
        )
        self.assert_digest_matches(envelope, output)

    def test_interrupted_probe_preserves_bytes_cleanup_and_one_reason_line(self):
        # Mutation caught: retaining probe bytes but dropping interrupted cleanup errors.
        result = run_gates.process_supervisor.SupervisedResult(
            output=b"probe partial bytes\n",
            return_code=143,
            cause="interrupted",
            cleanup_errors=("forced interruption cleanup diagnostic",),
            interrupted_signal=signal.SIGTERM,
        )
        passed, interrupted, envelope, output = self.execute_with_probe_result(result)

        reason = "runner interrupted by SIGTERM during tool version probe"
        reason_line = (reason + "\n").encode("utf-8")
        self.assertFalse(passed)
        self.assertEqual(interrupted, signal.SIGTERM)
        self.assertEqual(envelope["status"], "failed")
        self.assertEqual(envelope["exitCode"], 143)
        self.assertEqual(envelope["reason"], reason)
        self.assertEqual(
            output,
            b"probe partial bytes\n"
            b"process cleanup failed: forced interruption cleanup diagnostic\n"
            + reason_line,
        )
        self.assertEqual(output.count(reason_line), 1)
        self.assert_digest_matches(envelope, output)

    def test_nominal_probe_result_cannot_drop_cleanup_errors(self):
        # Mutation caught: discarding cleanup errors when the probe leader exited zero.
        result = run_gates.process_supervisor.SupervisedResult(
            output=b"Python fixture\n",
            return_code=0,
            cause="exited",
            cleanup_errors=("forced nominal cleanup diagnostic",),
            interrupted_signal=None,
        )
        passed, interrupted, envelope, output = self.execute_with_probe_result(result)

        self.assertFalse(passed)
        self.assertIsNone(interrupted)
        self.assertEqual(envelope["status"], "not_run")
        self.assertEqual(envelope["exitCode"], 3)
        self.assertEqual(
            envelope["reason"],
            "tool version probe failed for python3: "
            "forced nominal cleanup diagnostic",
        )
        self.assertEqual(
            output,
            b"not_run: tool version probe failed for python3: "
            b"forced nominal cleanup diagnostic\n"
            b"process cleanup failed: forced nominal cleanup diagnostic\n",
        )
        self.assert_digest_matches(envelope, output)

    def test_signal_observed_after_probe_return_keeps_probe_stage_reason(self):
        # Mutation caught: classifying a post-probe signal as gate execution.
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        signals = run_gates.process_supervisor.SignalState()

        def completed_probe(_gate, shared_signals):
            shared_signals.receive(signal.SIGINT, None)
            return {"python": platform.python_version()}, None, None, None

        with mock.patch.object(
            run_gates, "probe_tool_versions", side_effect=completed_probe
        ):
            passed, interrupted = run_gates.execute_gates(
                [fixture_gate()],
                SOURCE_COMMIT,
                CANDIDATE_DIGEST,
                temporary.name,
                signals,
            )

        envelope = json.loads(
            (Path(temporary.name) / "active-probe.json").read_text(encoding="utf-8")
        )
        self.assertFalse(passed)
        self.assertEqual(interrupted, signal.SIGINT)
        self.assertEqual(
            envelope["reason"],
            "runner interrupted by SIGINT during tool version probe",
        )
        self.assertEqual(envelope["exitCode"], 130)

    def test_signal_observed_after_command_return_fails_active_gate(self):
        # Mutation caught: trusting a completed command result over shared signal state.
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        signals = run_gates.process_supervisor.SignalState()

        def completed_command(_gate, shared_signals):
            shared_signals.receive(signal.SIGTERM, None)
            return "passed", "command exited 0", 0, b"command bytes\n", None

        with mock.patch.object(
            run_gates,
            "probe_tool_versions",
            return_value=(
                {"python": platform.python_version(), "python3": "fixture"},
                None,
                None,
                None,
            ),
        ), mock.patch.object(
            run_gates, "run_command", side_effect=completed_command
        ):
            passed, interrupted = run_gates.execute_gates(
                [fixture_gate("active-command")],
                SOURCE_COMMIT,
                CANDIDATE_DIGEST,
                temporary.name,
                signals,
            )

        evidence_dir = Path(temporary.name)
        envelope = json.loads(
            (evidence_dir / "active-command.json").read_text(encoding="utf-8")
        )
        output = (evidence_dir / "active-command.log").read_bytes()
        reason = "runner interrupted by SIGTERM during gate execution"
        self.assertFalse(passed)
        self.assertEqual(interrupted, signal.SIGTERM)
        self.assertEqual(envelope["status"], "failed")
        self.assertEqual(envelope["reason"], reason)
        self.assertEqual(envelope["exitCode"], 143)
        self.assertEqual(output, b"command bytes\n" + (reason + "\n").encode())
        self.assert_digest_matches(envelope, output)

    def test_main_returns_final_gate_signal_observed_after_execute(self):
        # Mutation caught: returning success after execute_gates saw no supervisor signal.
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        evidence_path = str(Path(temporary.name) / "evidence")

        def publish_then_signal(
            _gates, _source_commit, _candidate_digest, _evidence_dir, signals
        ):
            signals.receive(signal.SIGTERM, None)
            return True, None

        with mock.patch.object(
            run_gates, "load_catalog", return_value={"gates": [fixture_gate()]}
        ), mock.patch.object(
            run_gates, "select_gates", return_value=[fixture_gate()]
        ), mock.patch.object(
            run_gates,
            "read_candidate_lock",
            return_value=(SOURCE_COMMIT, CANDIDATE_DIGEST),
        ), mock.patch.object(
            run_gates, "validate_subject_bindings", return_value=None
        ), mock.patch.object(
            run_gates.process_supervisor, "require_support", return_value=None
        ), mock.patch.object(
            run_gates.process_supervisor, "enable_child_subreaper", return_value=None
        ), mock.patch.object(
            run_gates, "execute_gates", side_effect=publish_then_signal
        ):
            exit_code = run_gates.main(
                [
                    "--catalog",
                    str(Path(temporary.name) / "catalog.json"),
                    "--scope",
                    "local",
                    "--candidate-lock",
                    str(Path(temporary.name) / "candidate.json"),
                    "--evidence-dir",
                    evidence_path,
                ]
            )

        self.assertEqual(exit_code, 143)

    def test_operational_preflight_failure_creates_no_evidence_directory(self):
        # Mutation caught: creating evidence before operational pidfd preflight succeeds.
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        evidence_path = Path(temporary.name) / "evidence"
        error = run_gates.process_supervisor.SupervisionError(
            "pidfd signaling preflight failed: forced denial"
        )

        with mock.patch.object(
            run_gates, "load_catalog", return_value={"gates": [fixture_gate()]}
        ), mock.patch.object(
            run_gates, "select_gates", return_value=[fixture_gate()]
        ), mock.patch.object(
            run_gates,
            "read_candidate_lock",
            return_value=(SOURCE_COMMIT, CANDIDATE_DIGEST),
        ), mock.patch.object(
            run_gates, "validate_subject_bindings", return_value=None
        ), mock.patch.object(
            run_gates.process_supervisor, "require_support", side_effect=error
        ):
            exit_code = run_gates.main(
                [
                    "--catalog",
                    str(Path(temporary.name) / "catalog.json"),
                    "--scope",
                    "local",
                    "--candidate-lock",
                    str(Path(temporary.name) / "candidate.json"),
                    "--evidence-dir",
                    str(evidence_path),
                ]
            )

        self.assertEqual(exit_code, 2)
        self.assertFalse(evidence_path.exists())

    def test_real_runner_observes_signal_during_post_command_validation(self):
        # Mutation caught: ignoring a real signal after run_command has returned.
        with tempfile.TemporaryDirectory() as temporary_dir:
            root = Path(temporary_dir)
            fixture_repo = root / "repo"
            scripts_dir = fixture_repo / "scripts"
            library_dir = scripts_dir / "lib"
            library_dir.mkdir(parents=True)
            shutil.copy2(RUNNER_PATH, scripts_dir / "run-gates.py")
            shutil.copy2(REPO_ROOT / "scripts/lib/evidence.py", library_dir)
            shutil.copy2(REPO_ROOT / "scripts/lib/process_supervisor.py", library_dir)
            subprocess.run(["git", "init", "-q", fixture_repo], check=True)
            subprocess.run(
                ["git", "-C", fixture_repo, "config", "user.name", "Gate Test"],
                check=True,
            )
            subprocess.run(
                [
                    "git",
                    "-C",
                    fixture_repo,
                    "config",
                    "user.email",
                    "gate-test@example.invalid",
                ],
                check=True,
            )
            subprocess.run(["git", "-C", fixture_repo, "add", "."], check=True)
            subprocess.run(
                ["git", "-C", fixture_repo, "commit", "-qm", "fixture"],
                check=True,
            )
            source_commit = subprocess.run(
                ["git", "-C", fixture_repo, "rev-parse", "HEAD"],
                check=True,
                stdout=subprocess.PIPE,
                text=True,
            ).stdout.strip()

            state_dir = root / "state"
            state_dir.mkdir()
            command_done = state_dir / "command-done"
            validation_ready = state_dir / "validation-ready"
            validation_release = state_dir / "validation-release"
            fake_bin = root / "bin"
            fake_bin.mkdir()
            real_git = shutil.which("git")
            self.assertIsNotNone(real_git)
            fake_git = fake_bin / "git"
            fake_git.write_text(
                "#!" + sys.executable + "\n"
                "import os,pathlib,sys,time\n"
                f"marker=pathlib.Path({str(command_done)!r})\n"
                f"ready=pathlib.Path({str(validation_ready)!r})\n"
                f"release=pathlib.Path({str(validation_release)!r})\n"
                "if marker.exists() and sys.argv[1:3] == ['rev-parse', 'HEAD']:\n"
                "    ready.write_text('ready\\n', encoding='ascii')\n"
                "    while not release.exists(): time.sleep(0.001)\n"
                f"os.execv({real_git!r}, [{real_git!r}, *sys.argv[1:]])\n",
                encoding="utf-8",
            )
            fake_git.chmod(0o700)

            catalog = root / "catalog.json"
            catalog.write_text(
                json.dumps(
                    {
                        "schemaVersion": 1,
                        "gates": [
                            {
                                **fixture_gate("late-command-signal"),
                                "subjectKind": "source",
                                "command": [
                                    "python3",
                                    "-c",
                                    (
                                        "import pathlib,sys; "
                                        "pathlib.Path(sys.argv[1]).write_text('done\\n')"
                                    ),
                                    str(command_done),
                                ],
                            }
                        ],
                    },
                    sort_keys=True,
                ),
                encoding="utf-8",
            )
            candidate = root / "candidate.json"
            candidate.write_text(
                json.dumps({"schemaVersion": 1, "sourceCommit": source_commit}),
                encoding="utf-8",
            )
            evidence_dir = root / "evidence"
            environment = os.environ.copy()
            environment["PATH"] = str(fake_bin) + os.pathsep + environment["PATH"]
            process = subprocess.Popen(
                [
                    sys.executable,
                    str(scripts_dir / "run-gates.py"),
                    "--catalog",
                    str(catalog),
                    "--scope",
                    "local",
                    "--candidate-lock",
                    str(candidate),
                    "--evidence-dir",
                    str(evidence_dir),
                ],
                cwd=fixture_repo,
                env=environment,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
            try:
                self.wait_for_path(validation_ready, process)
                self.assertTrue(command_done.exists())
                os.kill(process.pid, signal.SIGTERM)
                validation_release.write_text("release\n", encoding="ascii")
                stdout, _stderr = process.communicate(timeout=8)
            finally:
                self.stop_process(process)

            self.assertEqual(process.returncode, 143, stdout.decode(errors="replace"))
            envelope = json.loads(
                (evidence_dir / "late-command-signal.json").read_text(
                    encoding="utf-8"
                )
            )
            output = (evidence_dir / "late-command-signal.log").read_bytes()
            reason = "runner interrupted by SIGTERM during gate execution"
            self.assertEqual(envelope["status"], "failed")
            self.assertEqual(envelope["exitCode"], 143)
            self.assertEqual(envelope["reason"], reason)
            self.assertEqual(output, (reason + "\n").encode("utf-8"))
            self.assert_digest_matches(envelope, output)

    def test_real_runner_final_gate_signal_after_log_publication_is_authoritative(self):
        # Mutation caught: converting a final-gate late signal to runner exit zero.
        with tempfile.TemporaryDirectory() as temporary_dir:
            root = Path(temporary_dir)
            state_dir = root / "state"
            state_dir.mkdir()
            ready = state_dir / "ready"
            release = state_dir / "release"
            catalog = root / "catalog.json"
            catalog.write_text(
                json.dumps(
                    {
                        "schemaVersion": 1,
                        "gates": [
                            {
                                **fixture_gate("final-late-signal"),
                                "timeoutSeconds": 20,
                                "command": [
                                    "python3",
                                    "-c",
                                    (
                                        "import pathlib,sys,time; "
                                        "ready=pathlib.Path(sys.argv[1]); "
                                        "release=pathlib.Path(sys.argv[2]); "
                                        "ready.write_text('ready\\n')\n"
                                        "while not release.exists():\n"
                                        " time.sleep(0.001)\n"
                                        "sys.stdout.write('x' * (4 * 1024 * 1024))"
                                    ),
                                    str(ready),
                                    str(release),
                                ],
                            }
                        ],
                    },
                    sort_keys=True,
                ),
                encoding="utf-8",
            )
            candidate = root / "candidate.json"
            candidate.write_text(
                json.dumps({"schemaVersion": 1, "sourceCommit": SOURCE_COMMIT}),
                encoding="utf-8",
            )
            evidence_dir = root / "evidence"
            process = subprocess.Popen(
                [
                    sys.executable,
                    str(RUNNER_PATH),
                    "--catalog",
                    str(catalog),
                    "--scope",
                    "local",
                    "--candidate-lock",
                    str(candidate),
                    "--evidence-dir",
                    str(evidence_dir),
                ],
                cwd=REPO_ROOT,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
            inotify_fd = None
            try:
                self.wait_for_path(ready, process)
                libc = __import__("ctypes").CDLL(None, use_errno=True)
                inotify_fd = libc.inotify_init1(os.O_CLOEXEC)
                self.assertGreaterEqual(inotify_fd, 0)
                watch = libc.inotify_add_watch(
                    inotify_fd, os.fsencode(evidence_dir), 0x00000100
                )
                self.assertGreaterEqual(watch, 0)
                release.write_text("release\n", encoding="ascii")

                deadline = time.monotonic() + 8
                saw_log = False
                while time.monotonic() < deadline and not saw_log:
                    readable, _writable, _exceptional = select.select(
                        [inotify_fd], [], [], 0.5
                    )
                    if not readable:
                        continue
                    events = os.read(inotify_fd, 4096)
                    offset = 0
                    while offset + 16 <= len(events):
                        _wd, _mask, _cookie, name_length = struct.unpack_from(
                            "iIII", events, offset
                        )
                        offset += 16
                        name = events[offset : offset + name_length].split(b"\0", 1)[0]
                        offset += name_length
                        if name == b"final-late-signal.log":
                            saw_log = True
                            break
                self.assertTrue(saw_log, "final gate log was not published")
                os.kill(process.pid, signal.SIGSTOP)
                os.kill(process.pid, signal.SIGTERM)
                os.kill(process.pid, signal.SIGCONT)
                stdout, _stderr = process.communicate(timeout=8)
            finally:
                if inotify_fd is not None:
                    os.close(inotify_fd)
                self.stop_process(process)

            self.assertEqual(process.returncode, 143, stdout.decode(errors="replace"))
            envelope = json.loads(
                (evidence_dir / "final-late-signal.json").read_text(encoding="utf-8")
            )
            output = (evidence_dir / "final-late-signal.log").read_bytes()
            self.assertEqual(envelope["status"], "passed", (envelope, output[-256:]))
            self.assertEqual(envelope["exitCode"], 0)
            self.assertEqual(envelope["reason"], "command exited 0")
            self.assert_digest_matches(envelope, output)


if __name__ == "__main__":
    unittest.main()
