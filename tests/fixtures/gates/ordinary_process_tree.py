#!/usr/bin/env python3
"""Harmless descendants for ordinary gate process-lifecycle tests."""

import os
import pathlib
import signal
import subprocess
import sys
import time


def wait_for(path, timeout_seconds=5):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if path.exists():
            return True
        time.sleep(0.02)
    return False


def record_pid(state_dir, name):
    (state_dir / f"{name}.pid").write_text(str(os.getpid()), encoding="ascii")


def close_capture_streams():
    for descriptor in (sys.stdout.fileno(), sys.stderr.fileno()):
        try:
            os.close(descriptor)
        except OSError:
            pass


def remain_live(state_dir, name, *, separate_session=False, close_streams=False,
                term_marker=None):
    if separate_session:
        os.setsid()
    if term_marker is None:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
    else:
        def record_term(_signum, _frame):
            record_pid(state_dir, term_marker)

        signal.signal(signal.SIGTERM, record_term)
    record_pid(state_dir, name)
    if close_streams:
        close_capture_streams()
    while True:
        time.sleep(1)


def run_double_fork(state_dir):
    first_child = os.fork()
    if first_child > 0:
        os._exit(0)
    os.setsid()
    grandchild = os.fork()
    if grandchild > 0:
        os._exit(0)
    remain_live(state_dir, "grandchild", close_streams=True)


def run_signal_child(state_dir):
    def exit_on_signal(_signum, _frame):
        raise SystemExit(0)

    signal.signal(signal.SIGINT, exit_on_signal)
    signal.signal(signal.SIGTERM, exit_on_signal)
    record_pid(state_dir, "command-child")
    while True:
        time.sleep(1)


def run_cooperative_signal_gate(state_dir):
    state_dir.mkdir(parents=True, exist_ok=True)
    child = subprocess.Popen(
        [
            sys.executable,
            os.path.abspath(__file__),
            "signal-child",
            str(state_dir),
        ],
        close_fds=True,
    )
    if not wait_for(state_dir / "command-child.pid"):
        child.kill()
        return 2

    def cleanup_after_signal(signum, _frame):
        record_pid(state_dir, "command-signal-received")
        time.sleep(0.3)
        child.send_signal(signum)
        child.wait(timeout=2)
        record_pid(state_dir, "command-cleanup-complete")
        raise SystemExit(0)

    signal.signal(signal.SIGINT, cleanup_after_signal)
    signal.signal(signal.SIGTERM, cleanup_after_signal)
    record_pid(state_dir, "command-leader")
    (state_dir / "command-ready").write_text("ready\n", encoding="utf-8")
    print("cooperative gate ready", flush=True)
    while True:
        time.sleep(1)


def run_leader(mode, state_dir):
    state_dir.mkdir(parents=True, exist_ok=True)
    if mode == "clean-zero":
        print("clean gate exited zero", flush=True)
        return 0

    child_mode = "child"
    pid_marker = state_dir / "child.pid"
    if mode in {"setsid-timeout", "leader-exit-leak"}:
        child_mode = "setsid-child"
        pid_marker = state_dir / "detached-child.pid"
    elif mode in {"double-fork-timeout", "double-fork-leak"}:
        child_mode = "double-fork-child"
        pid_marker = state_dir / "grandchild.pid"
    child = subprocess.Popen(
        [sys.executable, os.path.abspath(__file__), child_mode, str(state_dir)],
        close_fds=True,
    )
    if not wait_for(pid_marker):
        child.kill()
        return 2
    if mode in {"pipe-hang", "leader-exit-leak", "double-fork-leak"}:
        return 0
    if mode in {"timeout", "setsid-timeout", "double-fork-timeout"}:
        while True:
            time.sleep(1)
    raise SystemExit(f"unknown leader mode: {mode}")


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: ordinary_process_tree.py MODE STATE_DIR")
    mode, state_path = sys.argv[1:]
    state_dir = pathlib.Path(state_path)
    if mode == "child":
        return remain_live(state_dir, "child")
    if mode == "setsid-child":
        return remain_live(
            state_dir,
            "detached-child",
            separate_session=True,
            close_streams=True,
            term_marker="detached-child-term",
        )
    if mode == "double-fork-child":
        return run_double_fork(state_dir)
    if mode == "signal-child":
        return run_signal_child(state_dir)
    if mode == "cooperative-signal":
        return run_cooperative_signal_gate(state_dir)
    return run_leader(mode, state_dir)


if __name__ == "__main__":
    raise SystemExit(main())
