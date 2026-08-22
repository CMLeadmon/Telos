#!/usr/bin/env python3
"""Run Playwright against an owned, bounded frontend development server."""

import argparse
import os
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FRONTEND_ROOT = os.path.join(REPO_ROOT, "frontend")
READY_URL = "http://127.0.0.1:3000/"


def positive_seconds(value):
    try:
        seconds = int(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be a positive integer") from error
    if seconds <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return seconds


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--ready-timeout-seconds", type=positive_seconds, default=60
    )
    parser.add_argument(
        "--shutdown-timeout-seconds", type=positive_seconds, default=5
    )
    return parser


def replay_server_log(server_log):
    server_log.flush()
    server_log.seek(0)
    while True:
        chunk = server_log.read(1024 * 1024)
        if not chunk:
            break
        sys.stderr.buffer.write(chunk)
    sys.stderr.buffer.flush()


def wait_until_ready(server, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        return_code = server.poll()
        if return_code is not None:
            return False, f"frontend dev server exited {return_code} before readiness"
        try:
            with urllib.request.urlopen(READY_URL, timeout=1) as response:
                if response.status < 500 and server.poll() is None:
                    return True, ""
        except urllib.error.HTTPError as error:
            if error.code < 500 and server.poll() is None:
                return True, ""
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.25)
    return False, f"frontend dev server was not ready within {timeout_seconds} seconds"


def stop_owned_server(server, timeout_seconds):
    if server.poll() is not None:
        return
    try:
        os.killpg(server.pid, signal.SIGTERM)
        server.wait(timeout=timeout_seconds)
    except ProcessLookupError:
        return
    except subprocess.TimeoutExpired:
        try:
            os.killpg(server.pid, signal.SIGKILL)
        except ProcessLookupError:
            return
        server.wait()


def run_gate(args):
    with tempfile.TemporaryFile(mode="w+b") as server_log:
        try:
            server = subprocess.Popen(
                ["npm", "run", "dev"],
                cwd=FRONTEND_ROOT,
                shell=False,
                stdout=server_log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        except OSError as error:
            print(
                f"playwright-gate: cannot start frontend dev server: {error}",
                file=sys.stderr,
            )
            return 1
        try:
            ready, reason = wait_until_ready(server, args.ready_timeout_seconds)
            if not ready:
                print(f"playwright-gate: {reason}", file=sys.stderr)
                replay_server_log(server_log)
                return 1
            try:
                completed = subprocess.run(
                    ["npx", "playwright", "test"],
                    cwd=FRONTEND_ROOT,
                    shell=False,
                    check=False,
                )
            except OSError as error:
                print(f"playwright-gate: cannot run Playwright: {error}", file=sys.stderr)
                return 1
            return completed.returncode
        finally:
            stop_owned_server(server, args.shutdown_timeout_seconds)


def main(argv=None):
    args = build_parser().parse_args(argv)
    return run_gate(args)


if __name__ == "__main__":
    raise SystemExit(main())
