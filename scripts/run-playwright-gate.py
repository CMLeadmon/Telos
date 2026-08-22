#!/usr/bin/env python3
"""Run Playwright against an owned, bounded frontend development server."""

import argparse
import errno
import os
import secrets
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
PROC_ROOT = "/proc"
PROCESS_OWNER_ENV = "TELOS_PLAYWRIGHT_GATE_OWNER"
IDENTITY_BIND_TIMEOUT_SECONDS = 1


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


class OpenProcessIdentity:
    def __init__(self, pid, start_time, pidfd):
        self.pid = pid
        self.start_time = start_time
        self.pidfd = pidfd

    def close(self):
        if self.pidfd is not None:
            os.close(self.pidfd)
            self.pidfd = None


class OwnedProcess:
    def __init__(self, process, ownership_token, leader_identity):
        self.process = process
        self.ownership_token = ownership_token
        self.leader_start_time = (
            None if leader_identity is None else leader_identity.start_time
        )
        self.leader_pidfd = (
            None if leader_identity is None else leader_identity.pidfd
        )
        if leader_identity is not None:
            leader_identity.pidfd = None
        # Retained for non-destructive diagnostics only. Numeric PGIDs are
        # reusable and are never destructive-signal targets.
        self.pgid = process.pid

    def close(self):
        if self.leader_pidfd is not None:
            os.close(self.leader_pidfd)
            self.leader_pidfd = None


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


def require_process_identity_support():
    if not sys.platform.startswith("linux"):
        raise RuntimeError("process ownership cleanup requires Linux /proc")
    if not callable(getattr(os, "pidfd_open", None)):
        raise RuntimeError("process ownership cleanup requires os.pidfd_open")
    if not callable(getattr(signal, "pidfd_send_signal", None)):
        raise RuntimeError(
            "process ownership cleanup requires signal.pidfd_send_signal"
        )
    try:
        if read_process_start_time(os.getpid()) is None:
            raise RuntimeError("cannot inspect the wrapper process identity")
        with open(f"{PROC_ROOT}/self/environ", "rb") as environment_file:
            environment_file.read()
    except RuntimeError:
        raise
    except OSError as error:
        raise RuntimeError(
            f"process ownership inspection is unavailable: {error}"
        ) from error


def read_process_stat(pid):
    try:
        with open(f"{PROC_ROOT}/{pid}/stat", "rb") as stat_file:
            stat_bytes = stat_file.read()
    except (FileNotFoundError, ProcessLookupError):
        return None
    except OSError as error:
        raise RuntimeError(f"cannot inspect process {pid} identity: {error}") from error
    command_end = stat_bytes.rfind(b")")
    if command_end < 0:
        raise RuntimeError(f"cannot parse process {pid} identity")
    fields = stat_bytes[command_end + 1:].split()
    if len(fields) <= 19:
        raise RuntimeError(f"cannot parse process {pid} identity")
    try:
        return fields[0].decode("ascii"), int(fields[2]), int(fields[19])
    except (UnicodeDecodeError, ValueError) as error:
        raise RuntimeError(f"cannot parse process {pid} identity") from error


def read_process_start_time(pid):
    process_stat = read_process_stat(pid)
    if process_stat is None or process_stat[0] in {"X", "Z"}:
        return None
    return process_stat[2]


def process_has_ownership_token(
    pid, ownership_token, allow_unrelated_permission_denied=False
):
    expected = f"{PROCESS_OWNER_ENV}={ownership_token}".encode("ascii")
    try:
        with open(f"{PROC_ROOT}/{pid}/environ", "rb") as environment_file:
            environment = environment_file.read().split(b"\0")
    except (FileNotFoundError, ProcessLookupError):
        return False
    except PermissionError:
        if allow_unrelated_permission_denied:
            return False
        raise RuntimeError(f"cannot inspect process {pid} ownership token")
    except OSError as error:
        raise RuntimeError(
            f"cannot inspect process {pid} ownership token: {error}"
        ) from error
    return expected in environment


def open_owned_process(pid, ownership_token):
    start_time = read_process_start_time(pid)
    if start_time is None or not process_has_ownership_token(
        pid, ownership_token
    ):
        return None
    try:
        pidfd = os.pidfd_open(pid, 0)
    except ProcessLookupError:
        return None
    except OSError as error:
        raise RuntimeError(
            f"cannot open stable identity for process {pid}: {error}"
        ) from error
    identity = OpenProcessIdentity(pid, start_time, pidfd)
    try:
        confirmed_start_time = read_process_start_time(pid)
        if (
            confirmed_start_time != start_time
            or not process_has_ownership_token(pid, ownership_token)
        ):
            identity.close()
            return None
    except Exception:
        identity.close()
        raise
    return identity


def wait_for_owned_process(process, ownership_token):
    deadline = time.monotonic() + IDENTITY_BIND_TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        identity = open_owned_process(process.pid, ownership_token)
        if identity is not None:
            return identity
        if process.poll() is not None:
            return None
        time.sleep(0.01)
    return open_owned_process(process.pid, ownership_token)


def open_owned_processes(owned):
    try:
        process_entries = list(os.scandir(PROC_ROOT))
    except OSError as error:
        raise RuntimeError(f"cannot inspect owned process set: {error}") from error
    identities = []
    try:
        for process_entry in process_entries:
            if not process_entry.name.isdigit():
                continue
            try:
                process_uid = process_entry.stat(follow_symlinks=False).st_uid
            except FileNotFoundError:
                continue
            except OSError as error:
                raise RuntimeError(
                    f"cannot inspect process {process_entry.name} owner: {error}"
                ) from error
            if process_uid != os.geteuid():
                continue
            process_stat = read_process_stat(int(process_entry.name))
            if process_stat is None:
                continue
            process_state, process_pgid, _process_start_time = process_stat
            if process_state in {"X", "Z"}:
                continue
            allow_unrelated_permission_denied = process_pgid != owned.pgid
            if not process_has_ownership_token(
                int(process_entry.name),
                owned.ownership_token,
                allow_unrelated_permission_denied=allow_unrelated_permission_denied,
            ):
                continue
            identity = open_owned_process(
                int(process_entry.name), owned.ownership_token
            )
            if identity is not None:
                identities.append(identity)
    except Exception:
        for identity in identities:
            identity.close()
        raise
    return identities


def process_matches_owned_leader(owned):
    start_time = read_process_start_time(owned.process.pid)
    return (
        start_time == owned.leader_start_time
        and process_has_ownership_token(
            owned.process.pid, owned.ownership_token
        )
    )


def owned_process_alive(owned):
    if owned.process.poll() is not None:
        return False
    try:
        return process_matches_owned_leader(owned)
    except RuntimeError:
        return False


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
                owner_is_server = process_has_ownership_token(
                    pid, server.ownership_token
                )
            except RuntimeError:
                return False, f"cannot confirm the owner of port {READY_PORT} listener"
            if not owner_is_server:
                return (
                    False,
                    f"port {READY_PORT} listener is not owned by the frontend dev server",
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
        if owned_processes_gone(owned):
            return True
        time.sleep(0.05)
    return owned_processes_gone(owned)


def close_process_identities(identities):
    for identity in identities:
        identity.close()


def owned_processes_gone(owned):
    identities = open_owned_processes(owned)
    try:
        return owned.process.poll() is not None and not identities
    finally:
        close_process_identities(identities)


def send_pidfd_signal(pidfd, signum):
    try:
        signal.pidfd_send_signal(pidfd, signum, None, 0)
    except ProcessLookupError:
        return


def signal_owned_processes(owned, signum):
    # The persistent leader pidfd was bound at launch and cannot be redirected
    # by PID/PGID reuse. Signal it first so it cannot keep creating children.
    if owned.leader_pidfd is not None:
        send_pidfd_signal(owned.leader_pidfd, signum)

    identities = open_owned_processes(owned)
    try:
        for identity in identities:
            if identity.pid != owned.process.pid:
                send_pidfd_signal(identity.pidfd, signum)
    finally:
        close_process_identities(identities)


def stop_owned_group(owned, timeout_seconds):
    try:
        stopped = owned_processes_gone(owned)
        if not stopped:
            signal_owned_processes(owned, signal.SIGTERM)
            stopped = wait_for_owned_exit(owned, timeout_seconds)
        if not stopped:
            signal_owned_processes(owned, signal.SIGKILL)
            stopped = wait_for_owned_exit(owned, timeout_seconds)
        return stopped
    finally:
        owned.close()


def start_owned(argv, cwd, **kwargs):
    require_process_identity_support()
    ownership_token = secrets.token_hex(32)
    environment = dict(kwargs.pop("env", os.environ))
    environment[PROCESS_OWNER_ENV] = ownership_token
    process = subprocess.Popen(
        argv,
        cwd=cwd,
        shell=False,
        start_new_session=True,
        env=environment,
        **kwargs,
    )
    leader_identity = wait_for_owned_process(process, ownership_token)
    return OwnedProcess(process, ownership_token, leader_identity)


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
            for label, owned in (
                ("Playwright", playwright),
                ("frontend dev server", server),
            ):
                if owned is None:
                    continue
                try:
                    stopped = stop_owned_group(
                        owned, args.shutdown_timeout_seconds
                    )
                except Exception as error:
                    print(
                        f"playwright-gate: {label} cleanup failed: {error}",
                        file=sys.stderr,
                    )
                    stopped = False
                cleanup_succeeded = stopped and cleanup_succeeded
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
