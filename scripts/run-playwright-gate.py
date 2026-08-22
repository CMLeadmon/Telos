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
TCP_LISTEN_STATE = "0A"
PROC_TCP_TABLES = ("/proc/net/tcp", "/proc/net/tcp6")


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


def listening_socket_inodes(port):
    if not sys.platform.startswith("linux"):
        raise RuntimeError("listener ownership inspection requires Linux /proc")
    inodes = set()
    tables_read = 0
    for table_path in PROC_TCP_TABLES:
        try:
            with open(table_path, encoding="ascii") as table:
                lines = table.readlines()
        except FileNotFoundError:
            continue
        except OSError as error:
            raise RuntimeError(
                f"cannot inspect listener ownership via {table_path}: {error}"
            ) from error
        tables_read += 1
        for line in lines[1:]:
            fields = line.split()
            if len(fields) < 10:
                raise RuntimeError(f"cannot parse listener ownership table {table_path}")
            try:
                local_port = int(fields[1].rsplit(":", 1)[1], 16)
            except (IndexError, ValueError) as error:
                raise RuntimeError(
                    f"cannot parse listener ownership table {table_path}"
                ) from error
            if fields[3] == TCP_LISTEN_STATE and local_port == port:
                inodes.add(fields[9])
    if tables_read == 0:
        raise RuntimeError("listener ownership inspection is unavailable")
    return inodes


def socket_owner_pids(inodes):
    owners = {inode: set() for inode in inodes}
    try:
        processes = list(os.scandir("/proc"))
    except OSError as error:
        raise RuntimeError(f"cannot inspect listener process ownership: {error}") from error
    for process_entry in processes:
        if not process_entry.name.isdigit():
            continue
        try:
            descriptors = list(os.scandir(f"/proc/{process_entry.name}/fd"))
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
        except OSError:
            continue
        for descriptor in descriptors:
            try:
                target = os.readlink(descriptor.path)
            except (FileNotFoundError, PermissionError, ProcessLookupError, OSError):
                continue
            if target.startswith("socket:[") and target.endswith("]"):
                inode = target[8:-1]
                if inode in owners:
                    owners[inode].add(int(process_entry.name))
    return owners


def owned_listener_alive(server):
    if not owned_process_alive(server):
        return False, "frontend dev server leader/group is not live"
    try:
        inodes = listening_socket_inodes(READY_PORT)
        if not inodes:
            return False, f"port {READY_PORT} has no listening socket"
        owners = socket_owner_pids(inodes)
    except RuntimeError as error:
        return False, str(error)
    for inode in sorted(inodes):
        owner_pids = owners[inode]
        if not owner_pids:
            return False, f"cannot identify the owner of port {READY_PORT} listener"
        for pid in owner_pids:
            try:
                owner_pgid = os.getpgid(pid)
            except (ProcessLookupError, PermissionError):
                return False, f"cannot confirm the owner of port {READY_PORT} listener"
            if owner_pgid != server.pgid:
                return (
                    False,
                    f"port {READY_PORT} listener is not owned by the frontend dev server group",
                )
    return True, ""


def wait_until_ready(server, signals, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        signals.raise_if_received()
        if not owned_process_alive(server):
            return False, "frontend dev server leader/group exited before readiness"
        try:
            with urllib.request.urlopen(READY_URL, timeout=1) as response:
                if response.status < 500 and owned_process_alive(server):
                    listener_ready, reason = owned_listener_alive(server)
                    if not listener_ready:
                        return False, reason
                    signals.raise_if_received()
                    return True, ""
        except urllib.error.HTTPError as error:
            if error.code < 500 and owned_process_alive(server):
                listener_ready, reason = owned_listener_alive(server)
                if not listener_ready:
                    return False, reason
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
            result = normalized_return_code(return_code)
            if result == 0:
                listener_ready, reason = owned_listener_alive(server)
                if not listener_ready:
                    print(
                        "playwright-gate: frontend dev server was not live at "
                        f"Playwright completion: {reason}",
                        file=sys.stderr,
                    )
                    return EXIT_FAILED
                signals.raise_if_received()
            return result
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


def wait_for_owned_exit(owned, timeout_seconds):
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        leader_exited = owned.process.poll() is not None
        if leader_exited and not process_group_alive(owned.pgid):
            return True
        time.sleep(0.05)
    leader_exited = owned.process.poll() is not None
    return leader_exited and not process_group_alive(owned.pgid)


def stop_owned_group(owned, timeout_seconds):
    if process_group_alive(owned.pgid):
        try:
            os.killpg(owned.pgid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    stopped = wait_for_owned_exit(owned, timeout_seconds)
    if not stopped:
        try:
            os.killpg(owned.pgid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        stopped = wait_for_owned_exit(owned, timeout_seconds)
    return stopped


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
    result = EXIT_FAILED
    cleanup_succeeded = True
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
            else:
                ready, reason = wait_until_ready(
                    server, signals, args.ready_timeout_seconds
                )
                if not ready:
                    print(f"playwright-gate: {reason}", file=sys.stderr)
                    replay_server_log(server_log)
                else:
                    try:
                        playwright = start_owned(
                            ["npx", "playwright", "test"], FRONTEND_ROOT
                        )
                    except OSError as error:
                        print(
                            f"playwright-gate: cannot run Playwright: {error}",
                            file=sys.stderr,
                        )
                    else:
                        result = wait_for_playwright(
                            playwright, server, signals,
                            args.playwright_timeout_seconds,
                        )
        finally:
            if playwright is not None:
                cleanup_succeeded = (
                    stop_owned_group(playwright, args.shutdown_timeout_seconds)
                    and cleanup_succeeded
                )
            if server is not None:
                cleanup_succeeded = (
                    stop_owned_group(server, args.shutdown_timeout_seconds)
                    and cleanup_succeeded
                )
    if not cleanup_succeeded:
        print(
            "playwright-gate: owned process cleanup could not be confirmed",
            file=sys.stderr,
        )
        if result == 0:
            result = EXIT_FAILED
    return result


def main(argv=None):
    args = build_parser().parse_args(argv)
    signals = SignalState()
    previous_handlers = {}
    for signum in (signal.SIGTERM, signal.SIGINT):
        previous_handlers[signum] = signal.signal(signum, signals.receive)
    try:
        result = run_gate(args, signals)
        signals.raise_if_received()
        return result
    except GateInterrupted as interruption:
        return 128 + interruption.signum
    finally:
        for signum, handler in previous_handlers.items():
            signal.signal(signum, handler)


if __name__ == "__main__":
    raise SystemExit(main())
