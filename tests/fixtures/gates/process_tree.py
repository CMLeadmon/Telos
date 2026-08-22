#!/usr/bin/env python3
"""Harmless process-tree fixture for Playwright gate lifecycle tests."""

import http.server
import os
import pathlib
import signal
import subprocess
import sys
import time


STATE_DIR = pathlib.Path(os.environ["TELOS_GATE_FIXTURE_STATE"])
STATE_DIR.mkdir(parents=True, exist_ok=True)
ROLES = {"npm", "npx", "server-child", "stubborn-child", "port-holder"}


def record(name):
    (STATE_DIR / f"{name}.pid").write_text(str(os.getpid()), encoding="ascii")


def ignore_termination():
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)


def wait_for(path, timeout_seconds=5):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if path.exists():
            return True
        time.sleep(0.02)
    return False


def spawn(role):
    return subprocess.Popen(
        [sys.executable, os.path.abspath(__file__), role],
        env=os.environ.copy(),
        close_fds=True,
    )


class QuietHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ready")

    def log_message(self, _format, *_arguments):
        return


def serve(name, stubborn):
    if stubborn:
        ignore_termination()
    server = http.server.HTTPServer(("127.0.0.1", 3000), QuietHandler)
    record(name)
    server.serve_forever()


def run_npm():
    record("server-leader")
    child = spawn("server-child")
    if not wait_for(STATE_DIR / "server-child.pid"):
        child.kill()
        return 2
    if os.environ.get("TELOS_GATE_FIXTURE_NPM_MODE") == "leader-exit":
        return 9
    while True:
        time.sleep(1)


def run_npx():
    record("playwright-leader")
    child = spawn("stubborn-child")
    if not wait_for(STATE_DIR / "stubborn-child.pid"):
        child.kill()
        return 2
    if os.environ.get("TELOS_GATE_FIXTURE_NPX_MODE") == "normal":
        return 0
    while True:
        time.sleep(1)


def main():
    requested_role = sys.argv[1] if len(sys.argv) > 1 else ""
    role = requested_role if requested_role in ROLES else pathlib.Path(sys.argv[0]).name
    if role == "npm":
        return run_npm()
    if role == "npx":
        return run_npx()
    if role == "server-child":
        serve("server-child", stubborn=True)
    if role == "stubborn-child":
        record("stubborn-child")
        ignore_termination()
        while True:
            time.sleep(1)
    if role == "port-holder":
        serve("occupied", stubborn=False)
    raise SystemExit(f"unknown fixture role: {role}")


if __name__ == "__main__":
    raise SystemExit(main())
