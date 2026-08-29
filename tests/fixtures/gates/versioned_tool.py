#!/usr/bin/env python3
"""Controlled primary-tool executable for runner version-evidence tests."""

import os
import pathlib
import signal
import sys
import time


VERSIONS = {
    "bash": "GNU bash, fixture version 5.2",
    "node": "v24.18.0-fixture",
    "npm": "11.6.2-fixture",
    "npx": "11.6.2-fixture",
    "podman": "podman version 5.0.0-fixture",
}


def spawn_stubborn_child(state_dir, redirect_streams):
    child_pid_path = state_dir / "probe-child.pid"
    child_pid = os.fork()
    if child_pid != 0:
        deadline = time.monotonic() + 2
        while not child_pid_path.exists():
            if time.monotonic() >= deadline:
                raise RuntimeError("version fixture child did not record its PID")
            time.sleep(0.01)
        return child_pid

    os.setsid()
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    if redirect_streams:
        devnull = os.open(os.devnull, os.O_RDWR)
        for descriptor in (
            sys.stdin.fileno(),
            sys.stdout.fileno(),
            sys.stderr.fileno(),
        ):
            os.dup2(devnull, descriptor)
        if devnull > sys.stderr.fileno():
            os.close(devnull)
    child_pid_path.write_text(str(os.getpid()), encoding="ascii")
    while True:
        time.sleep(60)


def main():
    tool = pathlib.Path(sys.argv[0]).name
    if tool not in VERSIONS:
        raise SystemExit(f"unsupported fixture tool: {tool}")
    if sys.argv[1:] == ["--version"]:
        if os.environ.get("TELOS_VERSION_FIXTURE_FAIL") == tool:
            print(f"{tool} fixture version unavailable", file=sys.stderr)
            return 9
        mode = os.environ.get("TELOS_VERSION_FIXTURE_MODE")
        if mode in {
            "hang-with-child",
            "signal-wait-with-child",
            "success-with-child",
        }:
            state_dir = pathlib.Path(os.environ["TELOS_VERSION_FIXTURE_STATE"])
            (state_dir / "probe-leader.pid").write_text(
                str(os.getpid()), encoding="ascii"
            )
            spawn_stubborn_child(
                state_dir, redirect_streams=mode == "success-with-child"
            )
            if mode == "signal-wait-with-child":
                (state_dir / "probe-ready").write_text(
                    "ready\n", encoding="utf-8"
                )
            if mode in {"hang-with-child", "signal-wait-with-child"}:
                while True:
                    time.sleep(60)
        print(VERSIONS[tool])
        return 0
    marker = os.environ.get("TELOS_VERSION_FIXTURE_MARKER")
    if marker:
        pathlib.Path(marker).write_text("gate ran\n", encoding="utf-8")
    print(f"{tool} fixture gate ran")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
