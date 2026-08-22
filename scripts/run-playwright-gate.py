#!/usr/bin/env python3
"""Run Playwright against an owned, bounded frontend development server."""

import argparse
import errno
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FRONTEND_ROOT = os.path.join(REPO_ROOT, "frontend")
READY_HOST = "127.0.0.1"
READY_PORT = 3000
READY_URL = f"http://{READY_HOST}:{READY_PORT}/"
EXIT_FAILED = 1
EXIT_TIMEOUT = 124


class GateInterrupted(Exception):
    """The wrapper received a catchable external termination signal."""

    def __init__(self, signum):
        super().__init__(signum)
        self.signum = signum


class SignalState:
    def __init__(self):
        self.signum = None

    def receive(self, signum, _frame):
        if self.signum is None:
            self.signum = signum

    def raise_if_received(self):
        if self.signum is not None:
            raise GateInterrupted(self.signum)


class OwnedProcess:
    def __init__(self, process):
        self.process = process
        # start_new_session=True makes the child PID its stable process-group ID.
        self.pgid = process.pid


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
        "--playwright-timeout-seconds", type=positive_seconds, default=1650
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


def require_free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind(("0.0.0.0", READY_PORT))
        except OSError as error:
            if error.errno == errno.EADDRINUSE:
                raise RuntimeError(f"port {READY_PORT} is already occupied") from error
            raise RuntimeError(
                f"cannot verify port {READY_PORT} availability: {error}"
            ) from error


def process_group_alive(pgid):
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def owned_process_alive(owned):
    return owned.process.poll() is None and process_group_alive(owned.pgid)


def wait_until_ready(server, signals, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        signals.raise_if_received()
        if not owned_process_alive(server):
            return False, "frontend dev server leader/group exited before readiness"
        try:
            with urllib.request.urlopen(READY_URL, timeout=1) as response:
                if response.status < 500 and owned_process_alive(server):
                    signals.raise_if_received()
                    return True, ""
        except urllib.error.HTTPError as error:
            if error.code < 500 and owned_process_alive(server):
                signals.raise_if_received()
                return True, ""
        except (urllib.error.URLError, TimeoutError):
            pass
        time.sleep(0.1)
    return False, f"frontend dev server was not ready within {timeout_seconds} seconds"


def normalized_return_code(return_code):
    if return_code < 0:
        return min(255, 128 + abs(return_code))
    return min(255, return_code)


def wait_for_playwright(playwright, server, signals, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        signals.raise_if_received()
        return_code = playwright.process.poll()
        if return_code is not None:
            return normalized_return_code(return_code)
        if not owned_process_alive(server):
            print(
                "playwright-gate: frontend dev server leader/group exited "
                "during Playwright",
                file=sys.stderr,
            )
            return EXIT_FAILED
        time.sleep(0.1)
    print(
        f"playwright-gate: Playwright timed out after {timeout_seconds} seconds",
        file=sys.stderr,
    )
    return EXIT_TIMEOUT


def wait_for_group_exit(pgid, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        if not process_group_alive(pgid):
            return True
        time.sleep(0.05)
    return not process_group_alive(pgid)


def stop_owned_group(owned, timeout_seconds):
    if process_group_alive(owned.pgid):
        try:
            os.killpg(owned.pgid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    if not wait_for_group_exit(owned.pgid, timeout_seconds):
        try:
            os.killpg(owned.pgid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        wait_for_group_exit(owned.pgid, timeout_seconds)
    try:
        owned.process.wait(timeout=0)
    except subprocess.TimeoutExpired:
        pass


def start_owned(argv, cwd, **kwargs):
    process = subprocess.Popen(
        argv,
        cwd=cwd,
        shell=False,
        start_new_session=True,
        **kwargs,
    )
    return OwnedProcess(process)


def run_gate(args, signals):
    try:
        require_free_port()
    except RuntimeError as error:
        print(f"playwright-gate: {error}", file=sys.stderr)
        return EXIT_FAILED

    server = None
    playwright = None
    with tempfile.TemporaryFile(mode="w+b") as server_log:
        try:
            try:
                server = start_owned(
                    ["npm", "run", "dev"],
                    FRONTEND_ROOT,
                    stdout=server_log,
                    stderr=subprocess.STDOUT,
                )
            except OSError as error:
                print(
                    f"playwright-gate: cannot start frontend dev server: {error}",
                    file=sys.stderr,
                )
                return EXIT_FAILED

            ready, reason = wait_until_ready(
                server, signals, args.ready_timeout_seconds
            )
            if not ready:
                print(f"playwright-gate: {reason}", file=sys.stderr)
                replay_server_log(server_log)
                return EXIT_FAILED
            try:
                playwright = start_owned(
                    ["npx", "playwright", "test"], FRONTEND_ROOT
                )
            except OSError as error:
                print(f"playwright-gate: cannot run Playwright: {error}", file=sys.stderr)
                return EXIT_FAILED
            return wait_for_playwright(
                playwright, server, signals, args.playwright_timeout_seconds
            )
        finally:
            if playwright is not None:
                stop_owned_group(playwright, args.shutdown_timeout_seconds)
            if server is not None:
                stop_owned_group(server, args.shutdown_timeout_seconds)


def main(argv=None):
    args = build_parser().parse_args(argv)
    signals = SignalState()
    previous_handlers = {}
    for signum in (signal.SIGTERM, signal.SIGINT):
        previous_handlers[signum] = signal.signal(signum, signals.receive)
    try:
        return run_gate(args, signals)
    except GateInterrupted as interruption:
        return 128 + interruption.signum
    finally:
        for signum, handler in previous_handlers.items():
            signal.signal(signum, handler)


if __name__ == "__main__":
    raise SystemExit(main())
