"""Linux process supervision for bounded gate-runner commands."""

import ctypes
import errno
import os
import select
import selectors
import signal
import subprocess
import sys
import time
from dataclasses import dataclass


PROC_ROOT = "/proc"
PR_SET_CHILD_SUBREAPER = 36
PR_GET_CHILD_SUBREAPER = 37
POLL_INTERVAL_SECONDS = 0.02
IDENTITY_BIND_TIMEOUT_SECONDS = 1
TERM_WAIT_SECONDS = 1
KILL_WAIT_SECONDS = 2


class SupervisionError(RuntimeError):
    """The host cannot provide the required process-containment guarantee."""


class SignalState:
    """Record the first catchable signal without doing work in its handler."""

    def __init__(self) -> None:
        self._signum: int | None = None

    @property
    def signum(self) -> int | None:
        return self._signum

    def receive(self, signum: int, _frame: object) -> None:
        if self._signum is None:
            self._signum = signum


@dataclass(frozen=True)
class SupervisedResult:
    output: bytes
    return_code: int | None
    cause: str
    cleanup_errors: tuple[str, ...]
    interrupted_signal: int | None


@dataclass
class _ProcessIdentity:
    pid: int
    start_time: int
    pidfd: int | None

    def close(self):
        if self.pidfd is not None:
            os.close(self.pidfd)
            self.pidfd = None


def _capability_error(detail):
    return SupervisionError(
        "gate process supervision requires Linux with readable /proc, "
        "prctl child-subreaper support, os.pidfd_open, and "
        f"signal.pidfd_send_signal; {detail}"
    )


def _libc_prctl():
    try:
        libc = ctypes.CDLL(None, use_errno=True)
        prctl = libc.prctl
    except (AttributeError, OSError) as error:
        raise _capability_error(f"prctl is unavailable: {error}") from error
    prctl.restype = ctypes.c_int
    return prctl


def _get_child_subreaper(prctl):
    enabled = ctypes.c_int()
    if prctl(PR_GET_CHILD_SUBREAPER, ctypes.byref(enabled), 0, 0, 0) != 0:
        error_number = ctypes.get_errno()
        raise _capability_error(
            "PR_GET_CHILD_SUBREAPER failed: "
            f"{os.strerror(error_number)} (errno {error_number})"
        )
    return enabled.value


def require_support() -> None:
    """Fail closed unless every Linux ownership primitive is available."""

    if not sys.platform.startswith("linux"):
        raise _capability_error(f"current platform is {sys.platform!r}")
    if not os.path.isdir(PROC_ROOT) or not os.access(
        PROC_ROOT, os.R_OK | os.X_OK
    ):
        raise _capability_error("/proc is not readable")
    if not callable(getattr(os, "pidfd_open", None)):
        raise _capability_error("os.pidfd_open is unavailable")
    if not callable(getattr(signal, "pidfd_send_signal", None)):
        raise _capability_error("signal.pidfd_send_signal is unavailable")
    try:
        if _read_process_stat(os.getpid()) is None:
            raise _capability_error("the current process is absent from /proc")
        with os.scandir(PROC_ROOT) as entries:
            next(entries, None)
    except SupervisionError:
        raise
    except OSError as error:
        raise _capability_error(f"/proc inspection failed: {error}") from error
    _get_child_subreaper(_libc_prctl())


def enable_child_subreaper() -> None:
    """Enable and verify child-subreaper adoption for the current process."""

    require_support()
    prctl = _libc_prctl()
    if prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) != 0:
        error_number = ctypes.get_errno()
        raise _capability_error(
            "PR_SET_CHILD_SUBREAPER failed: "
            f"{os.strerror(error_number)} (errno {error_number})"
        )
    if _get_child_subreaper(prctl) != 1:
        raise _capability_error("PR_SET_CHILD_SUBREAPER did not remain enabled")


def _read_process_stat(pid):
    try:
        with open(f"{PROC_ROOT}/{pid}/stat", "rb") as stat_file:
            stat_bytes = stat_file.read()
    except (FileNotFoundError, ProcessLookupError):
        return None
    except OSError as error:
        raise SupervisionError(f"cannot inspect process {pid}: {error}") from error

    command_end = stat_bytes.rfind(b")")
    if command_end < 0:
        raise SupervisionError(f"cannot parse process {pid} identity")
    fields = stat_bytes[command_end + 1 :].split()
    if len(fields) <= 19:
        raise SupervisionError(f"cannot parse process {pid} identity")
    try:
        state = fields[0].decode("ascii")
        parent_pid = int(fields[1])
        start_time = int(fields[19])
    except (UnicodeDecodeError, ValueError) as error:
        raise SupervisionError(f"cannot parse process {pid} identity") from error
    return state, parent_pid, start_time


def _scan_processes():
    try:
        entries = list(os.scandir(PROC_ROOT))
    except OSError as error:
        raise SupervisionError(f"cannot enumerate {PROC_ROOT}: {error}") from error

    processes = {}
    for entry in entries:
        if not entry.name.isdigit():
            continue
        pid = int(entry.name)
        try:
            process_stat = _read_process_stat(pid)
        except SupervisionError:
            # Unrelated processes may be hidden by a restrictive procfs mount.
            continue
        if process_stat is not None:
            processes[pid] = process_stat
    return processes


def _open_identity(pid):
    first_stat = _read_process_stat(pid)
    if first_stat is None or first_stat[0] in {"X", "Z"}:
        return None
    try:
        pidfd = os.pidfd_open(pid, 0)
    except ProcessLookupError:
        return None
    except OSError as error:
        if error.errno == errno.ESRCH:
            return None
        raise SupervisionError(
            f"cannot open stable identity for process {pid}: {error}"
        ) from error

    identity = _ProcessIdentity(pid, first_stat[2], pidfd)
    try:
        second_stat = _read_process_stat(pid)
        if (
            second_stat is None
            or second_stat[0] in {"X", "Z"}
            or second_stat[2] != first_stat[2]
        ):
            identity.close()
            return None
    except Exception:
        identity.close()
        raise
    return identity


def _identity_alive(identity, process_stats=None):
    if identity.pidfd is None:
        return False
    poller = select.poll()
    poller.register(identity.pidfd, select.POLLIN)
    return not poller.poll(0)


def _wait_for_identity(process):
    deadline = time.monotonic() + IDENTITY_BIND_TIMEOUT_SECONDS
    while True:
        identity = _open_identity(process.pid)
        if identity is not None:
            return identity
        if process.poll() is not None or time.monotonic() >= deadline:
            return None
        time.sleep(POLL_INTERVAL_SECONDS)


class _OwnedProcesses:
    def __init__(self, leader_pid, leader_identity, baseline_children):
        self.leader_pid = leader_pid
        self.baseline_children = baseline_children
        self.identities = {}
        self.lineage = {}
        if leader_identity is not None:
            self.identities[leader_pid] = leader_identity
            self.lineage[leader_pid] = leader_identity.start_time

    @property
    def leader_identity(self):
        return self.identities.get(self.leader_pid)

    def discover(self):
        process_stats = _scan_processes()
        runner_pid = os.getpid()
        candidate_pids = {
            pid
            for pid, start_time in self.lineage.items()
            if pid in process_stats and process_stats[pid][2] == start_time
        }
        if self.leader_pid not in self.lineage:
            candidate_pids.add(self.leader_pid)
        candidate_pids.update(
            pid
            for pid, process_stat in process_stats.items()
            if process_stat[1] == runner_pid
            and self.baseline_children.get(pid) != process_stat[2]
        )

        changed = True
        while changed:
            changed = False
            for pid, process_stat in process_stats.items():
                if pid not in candidate_pids and process_stat[1] in candidate_pids:
                    candidate_pids.add(pid)
                    changed = True

        for pid in candidate_pids:
            process_stat = process_stats.get(pid)
            if process_stat is not None:
                self.lineage[pid] = process_stat[2]
            if pid in self.identities:
                continue
            if process_stat is None or process_stat[0] in {"X", "Z"}:
                continue
            identity = _open_identity(pid)
            if identity is not None and identity.start_time == process_stat[2]:
                self.identities[pid] = identity
            elif identity is not None:
                identity.close()

        return process_stats

    def live(self, process_stats=None):
        if process_stats is None:
            process_stats = self.discover()
        return {
            pid: identity
            for pid, identity in self.identities.items()
            if _identity_alive(identity, process_stats)
        }

    def reap_adopted(self, process):
        process.poll()
        for pid in list(self.identities):
            if pid == self.leader_pid:
                continue
            identity = self.identities[pid]
            if _identity_alive(identity):
                continue
            try:
                waited_pid, _status = os.waitpid(pid, os.WNOHANG)
            except ChildProcessError:
                waited_pid = 0
            except OSError as error:
                if error.errno in {errno.ECHILD, errno.ESRCH}:
                    waited_pid = 0
                else:
                    raise SupervisionError(
                        f"cannot reap owned process {pid}: {error}"
                    ) from error
            if waited_pid == pid or _read_process_stat(pid) is None:
                identity.close()
                del self.identities[pid]

    def close(self):
        for identity in self.identities.values():
            identity.close()
        self.identities.clear()


class _Capture:
    def __init__(self, stream):
        self.stream = stream
        self.output = bytearray()
        self.eof = False
        self.selector = selectors.DefaultSelector()
        os.set_blocking(stream.fileno(), False)
        self.selector.register(stream, selectors.EVENT_READ)

    def drain(self):
        if self.eof:
            return
        while self.selector.select(0):
            try:
                chunk = os.read(self.stream.fileno(), 1024 * 1024)
            except BlockingIOError:
                return
            except OSError as error:
                raise SupervisionError(f"cannot capture supervised output: {error}") from error
            if not chunk:
                self.eof = True
                try:
                    self.selector.unregister(self.stream)
                except KeyError:
                    pass
                return
            self.output.extend(chunk)

    def close(self):
        self.selector.close()
        self.stream.close()


def _baseline_direct_children():
    runner_pid = os.getpid()
    return {
        pid: process_stat[2]
        for pid, process_stat in _scan_processes().items()
        if process_stat[1] == runner_pid
    }


def _send_signal(identity, signum):
    if identity is None or identity.pidfd is None or not _identity_alive(identity):
        return
    try:
        signal.pidfd_send_signal(identity.pidfd, signum, None, 0)
    except ProcessLookupError:
        return
    except OSError as error:
        if error.errno != errno.ESRCH:
            raise SupervisionError(
                f"cannot signal owned process {identity.pid}: {error}"
            ) from error


def _record_error(cleanup_errors, error):
    message = str(error)
    if message not in cleanup_errors:
        cleanup_errors.append(message)


def _observe(process, owned, capture, cleanup_errors):
    try:
        process_stats = owned.discover()
        capture.drain()
        owned.reap_adopted(process)
        return owned.live(process_stats)
    except (OSError, SupervisionError) as error:
        _record_error(cleanup_errors, error)
        return owned.live({})


def _wait_for_exit(process, owned, capture, seconds, cleanup_errors):
    deadline = time.monotonic() + max(0, seconds)
    while True:
        live = _observe(process, owned, capture, cleanup_errors)
        if not live and process.poll() is not None:
            try:
                capture.drain()
            except SupervisionError as error:
                _record_error(cleanup_errors, error)
            return True
        if time.monotonic() >= deadline:
            return not live and process.poll() is not None
        time.sleep(POLL_INTERVAL_SECONDS)


def _signal_all(owned, signum, cleanup_errors):
    try:
        process_stats = owned.discover()
        live = owned.live(process_stats)
    except SupervisionError as error:
        _record_error(cleanup_errors, error)
        live = owned.live({})
    for identity in live.values():
        try:
            _send_signal(identity, signum)
        except SupervisionError as error:
            _record_error(cleanup_errors, error)


def _forced_cleanup(process, owned, capture, cleanup_errors):
    _signal_all(owned, signal.SIGTERM, cleanup_errors)
    if _wait_for_exit(
        process, owned, capture, TERM_WAIT_SECONDS, cleanup_errors
    ):
        return
    _signal_all(owned, signal.SIGKILL, cleanup_errors)
    if _wait_for_exit(
        process, owned, capture, KILL_WAIT_SECONDS, cleanup_errors
    ):
        return
    try:
        survivors = owned.live()
    except SupervisionError as error:
        _record_error(cleanup_errors, error)
        survivors = owned.live({})
    for pid in sorted(survivors):
        _record_error(cleanup_errors, f"owned process {pid} survived SIGKILL")


def run(
    argv: list[str],
    *,
    cwd,
    timeout_seconds,
    cooperative_grace_seconds,
    signals: SignalState,
) -> SupervisedResult:
    """Run one argv-only command and contain its complete Linux process tree."""

    require_support()
    baseline_children = _baseline_direct_children()
    process = subprocess.Popen(
        argv,
        cwd=cwd,
        shell=False,
        start_new_session=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if process.stdout is None:
        raise SupervisionError("supervised process output pipe was not created")

    leader_identity = _wait_for_identity(process)
    owned = _OwnedProcesses(process.pid, leader_identity, baseline_children)
    capture = _Capture(process.stdout)
    cleanup_errors = []
    cause = "capture_error"
    interrupted_signal = None
    deadline = time.monotonic() + timeout_seconds

    try:
        if leader_identity is None and process.poll() is None:
            _record_error(
                cleanup_errors,
                f"cannot bind stable identity for process {process.pid}",
            )
        else:
            while True:
                try:
                    process_stats = owned.discover()
                    capture.drain()
                    owned.reap_adopted(process)
                    live = owned.live(process_stats)
                except (OSError, SupervisionError) as error:
                    _record_error(cleanup_errors, error)
                    cause = "capture_error"
                    break

                if signals.signum is not None:
                    interrupted_signal = signals.signum
                    cause = "interrupted"
                    break

                return_code = process.poll()
                live_descendants = {
                    pid: identity
                    for pid, identity in live.items()
                    if pid != process.pid
                }
                if return_code is not None and live_descendants:
                    cause = "descendants"
                    break
                if return_code is not None and not live and capture.eof:
                    cause = "exited"
                    break
                if time.monotonic() >= deadline:
                    cause = "timeout"
                    break
                time.sleep(POLL_INTERVAL_SECONDS)

        if cause in {"timeout", "interrupted"}:
            first_signal = (
                interrupted_signal
                if interrupted_signal is not None
                else signal.SIGTERM
            )
            try:
                _send_signal(owned.leader_identity, first_signal)
            except SupervisionError as error:
                _record_error(cleanup_errors, error)
            cooperative_exit = _wait_for_exit(
                process,
                owned,
                capture,
                cooperative_grace_seconds,
                cleanup_errors,
            )
            if not cooperative_exit:
                _forced_cleanup(process, owned, capture, cleanup_errors)
        elif cause in {"descendants", "capture_error"}:
            _forced_cleanup(process, owned, capture, cleanup_errors)

        try:
            capture.drain()
        except SupervisionError as error:
            _record_error(cleanup_errors, error)
            if cause == "exited":
                cause = "capture_error"
        owned.reap_adopted(process)
        return SupervisedResult(
            output=bytes(capture.output),
            return_code=process.poll(),
            cause=cause,
            cleanup_errors=tuple(cleanup_errors),
            interrupted_signal=interrupted_signal,
        )
    finally:
        capture.close()
        owned.close()
