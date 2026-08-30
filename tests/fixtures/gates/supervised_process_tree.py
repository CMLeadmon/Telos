#!/usr/bin/env python3
"""Create harmless process trees for the Linux supervisor contract."""

import argparse
import os
import signal
import time
from pathlib import Path


def write_file(state_dir, name, value=b""):
    path = state_dir / name
    temporary = state_dir / f".{name}.{os.getpid()}"
    temporary.write_bytes(value)
    os.replace(temporary, path)


def record_pid(state_dir, role):
    write_file(state_dir, f"{role}.pid", f"{os.getpid()}\n".encode("ascii"))


def wait_forever():
    while True:
        signal.pause()


def detach_standard_streams():
    descriptor = os.open(os.devnull, os.O_RDWR)
    try:
        for target in (0, 1, 2):
            os.dup2(descriptor, target)
    finally:
        if descriptor > 2:
            os.close(descriptor)


def fork_stubborn_child(state_dir, *, start_session=False, detached=False):
    child_pid = os.fork()
    if child_pid != 0:
        return child_pid

    if start_session:
        os.setsid()
    if detached:
        detach_standard_streams()
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    record_pid(state_dir, "child")
    wait_forever()
    raise AssertionError("unreachable")


def run_timeout(state_dir):
    record_pid(state_dir, "leader")
    fork_stubborn_child(state_dir)
    wait_forever()


def run_leader_exit(state_dir):
    record_pid(state_dir, "leader")
    fork_stubborn_child(state_dir, detached=True)
    while not (state_dir / "child.pid").exists():
        time.sleep(0.01)


def run_setsid(state_dir):
    record_pid(state_dir, "leader")
    fork_stubborn_child(state_dir, start_session=True, detached=True)
    wait_forever()


def run_double_fork(state_dir):
    record_pid(state_dir, "leader")
    child_pid = os.fork()
    if child_pid == 0:
        record_pid(state_dir, "child")
        grandchild_pid = os.fork()
        if grandchild_pid != 0:
            os._exit(0)

        os.setsid()
        detach_standard_streams()
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        record_pid(state_dir, "grandchild")
        wait_forever()
        raise AssertionError("unreachable")

    while not (state_dir / "grandchild.pid").exists():
        time.sleep(0.01)
    wait_forever()


def run_late_fork_at_exit(state_dir):
    record_pid(state_dir, "leader")
    write_file(state_dir, "ready")
    while not (state_dir / "release").exists():
        time.sleep(0.001)

    child_pid = os.fork()
    if child_pid == 0:
        os.setsid()
        detach_standard_streams()
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        record_pid(state_dir, "late-child")
        wait_forever()
        raise AssertionError("unreachable")

    while not (state_dir / "late-child.pid").exists():
        time.sleep(0.001)


def run_zombie_at_exit(state_dir):
    record_pid(state_dir, "leader")
    child_pid = os.fork()
    if child_pid == 0:
        record_pid(state_dir, "zombie-child")
        os._exit(0)
    while not (state_dir / "zombie-child.pid").exists():
        time.sleep(0.001)
    while Path(f"/proc/{child_pid}/stat").exists():
        stat_bytes = Path(f"/proc/{child_pid}/stat").read_bytes()
        command_end = stat_bytes.rfind(b")")
        fields = stat_bytes[command_end + 1 :].split()
        if fields and fields[0] == b"Z":
            break
        time.sleep(0.001)


def run_cooperative(state_dir):
    record_pid(state_dir, "leader")
    child_pid = os.fork()
    if child_pid == 0:
        record_pid(state_dir, "child")
        wait_forever()
        raise AssertionError("unreachable")

    received_term = False

    def receive_term(_signum, _frame):
        nonlocal received_term
        received_term = True

    signal.signal(signal.SIGTERM, receive_term)
    while not (state_dir / "child.pid").exists():
        time.sleep(0.01)
    write_file(state_dir, "ready")

    while not received_term:
        signal.pause()
    os.kill(child_pid, signal.SIGTERM)
    os.waitpid(child_pid, 0)
    write_file(state_dir, "cleanup")
    raise SystemExit(143)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "mode",
        choices=(
            "timeout",
            "leader-exit",
            "setsid",
            "double-fork",
            "late-fork-at-exit",
            "zombie-at-exit",
            "cooperative",
        ),
    )
    parser.add_argument("state_dir", type=Path)
    args = parser.parse_args()
    args.state_dir.mkdir(parents=True, exist_ok=True)

    runners = {
        "timeout": run_timeout,
        "leader-exit": run_leader_exit,
        "setsid": run_setsid,
        "double-fork": run_double_fork,
        "late-fork-at-exit": run_late_fork_at_exit,
        "zombie-at-exit": run_zombie_at_exit,
        "cooperative": run_cooperative,
    }
    runners[args.mode](args.state_dir)


if __name__ == "__main__":
    main()
