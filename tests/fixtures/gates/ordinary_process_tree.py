#!/usr/bin/env python3
"""Harmless stubborn descendant for ordinary gate process-group tests."""

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


def run_child(state_dir):
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    (state_dir / "child.pid").write_text(str(os.getpid()), encoding="ascii")
    while True:
        time.sleep(1)


def run_leader(mode, state_dir):
    state_dir.mkdir(parents=True, exist_ok=True)
    child = subprocess.Popen(
        [sys.executable, os.path.abspath(__file__), "child", str(state_dir)],
        close_fds=True,
    )
    if not wait_for(state_dir / "child.pid"):
        child.kill()
        return 2
    if mode == "pipe-hang":
        return 0
    if mode == "timeout":
        while True:
            time.sleep(1)
    raise SystemExit(f"unknown leader mode: {mode}")


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: ordinary_process_tree.py MODE STATE_DIR")
    mode, state_path = sys.argv[1:]
    state_dir = pathlib.Path(state_path)
    if mode == "child":
        return run_child(state_dir)
    return run_leader(mode, state_dir)


if __name__ == "__main__":
    raise SystemExit(main())
