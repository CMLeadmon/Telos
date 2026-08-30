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
QUIESCENCE_EMPTY_SCANS = 3
QUIESCENCE_WAIT_SECONDS = 0.2
TERM_WAIT_SECONDS = 1
KILL_WAIT_SECONDS = 2
_LAUNCHER_CODE = r"""
import errno
import os
import sys

control_fd = int(sys.argv[1])
error_fd = int(sys.argv[2])
target_argv = sys.argv[3:]
try:
    release = os.read(control_fd, 1)
finally:
    os.close(control_fd)
if release != b"1":
    os.close(error_fd)
    raise SystemExit(126)
try:
    os.set_inheritable(error_fd, False)
    os.execvpe(target_argv[0], target_argv, os.environ)
except OSError as error:
    error_number = error.errno if error.errno is not None else errno.EIO
    try:
        os.write(error_fd, str(error_number).encode("ascii"))
    finally:
        os.close(error_fd)
    raise SystemExit(127)
"""


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
    _preflight_pidfd_signaling()


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


def _preflight_pidfd_signaling():
    runner_pid = os.getpid()
    first_stat = _read_process_stat(runner_pid)
    if first_stat is None or first_stat[0] in {"X", "Z"}:
        raise _capability_error("cannot inspect the runner identity")
    pidfd = None
    try:
        try:
            pidfd = os.pidfd_open(runner_pid, 0)
        except OSError as error:
            raise _capability_error(
                f"pidfd open preflight failed: {error}"
            ) from error
        second_stat = _read_process_stat(runner_pid)
        if second_stat is None or second_stat[2] != first_stat[2]:
            raise _capability_error("runner identity changed during pidfd preflight")
        try:
            signal.pidfd_send_signal(pidfd, 0, None, 0)
        except OSError as error:
            raise _capability_error(
                f"pidfd signaling preflight failed: {error}"
            ) from error
        final_stat = _read_process_stat(runner_pid)
        if final_stat is None or final_stat[2] != first_stat[2]:
            raise _capability_error("runner identity changed during pidfd preflight")
    finally:
        if pidfd is not None:
            os.close(pidfd)


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


def _launch_inert(argv, cwd):
    control_read, control_write = os.pipe()
    error_read, error_write = os.pipe()
    try:
        process = subprocess.Popen(
            [
                sys.executable,
                "-I",
                "-S",
                "-c",
                _LAUNCHER_CODE,
                str(control_read),
                str(error_write),
                *argv,
            ],
            cwd=cwd,
            shell=False,
            start_new_session=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            pass_fds=(control_read, error_write),
        )
    except BaseException:
        os.close(control_write)
        os.close(error_read)
        raise
    finally:
        os.close(control_read)
        os.close(error_write)
    return process, control_write, error_read


def _close_descriptor(descriptor):
    if descriptor is not None:
        os.close(descriptor)


def _release_launcher(control_write):
    try:
        os.write(control_write, b"1")
    finally:
        os.close(control_write)


def _raise_exec_error(error_read, executable):
    try:
        payload = os.read(error_read, 64)
    finally:
        os.close(error_read)
    if not payload:
        return
    try:
        error_number = int(payload.decode("ascii"))
    except (UnicodeDecodeError, ValueError) as error:
        raise OSError(errno.EIO, "invalid launcher execution error") from error
    error_type = FileNotFoundError if error_number == errno.ENOENT else OSError
    raise error_type(error_number, os.strerror(error_number), executable)


class _OwnedProcesses:
    def __init__(self, leader_pid, leader_identity, baseline_children):
        self.leader_pid = leader_pid
        self.baseline_children = baseline_children
        self.identities = {}
        self.lineage = {}
        self.direct_children = {}
        if leader_identity is not None:
            self.identities[leader_pid] = leader_identity
            self.lineage[leader_pid] = leader_identity.start_time
            self.direct_children[leader_pid] = leader_identity.start_time

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
        candidate_pids.update(
            pid
            for pid, process_stat in process_stats.items()
            if process_stat[1] == runner_pid
            and pid != self.leader_pid
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
                if process_stat[1] == runner_pid and (
                    pid == self.leader_pid
                    or self.baseline_children.get(pid) != process_stat[2]
                ):
                    self.direct_children[pid] = process_stat[2]
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
        for pid, start_time in list(self.direct_children.items()):
            if pid == self.leader_pid:
                continue
            process_stat = _read_process_stat(pid)
            if process_stat is None or process_stat[2] != start_time:
                identity = self.identities.pop(pid, None)
                if identity is not None:
                    identity.close()
                self.direct_children.pop(pid, None)
                self.lineage.pop(pid, None)
                continue
            identity = self.identities.get(pid)
            if process_stat[0] not in {"X", "Z"} and (
                identity is None or _identity_alive(identity)
            ):
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
                identity = self.identities.pop(pid, None)
                if identity is not None:
                    identity.close()
                self.direct_children.pop(pid, None)
                self.lineage.pop(pid, None)

    def has_unreaped_adopted_children(self):
        for pid, start_time in list(self.direct_children.items()):
            if pid == self.leader_pid:
                continue
            process_stat = _read_process_stat(pid)
            if process_stat is not None and process_stat[2] == start_time:
                return True
        return False

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
        try:
            os.set_blocking(stream.fileno(), False)
            self.selector.register(stream, selectors.EVENT_READ)
        except BaseException:
            self.selector.close()
            raise

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


class _UnavailableCapture:
    """Own the output stream when nonblocking capture setup has failed."""

    def __init__(self, stream):
        self.stream = stream
        self.output = bytearray()
        self.eof = False

    def drain(self):
        return

    def close(self):
        if self.stream is not None:
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
    empty_scans = 0
    while True:
        live = _observe(process, owned, capture, cleanup_errors)
        if (
            not live
            and process.poll() is not None
            and not owned.has_unreaped_adopted_children()
        ):
            empty_scans += 1
        else:
            empty_scans = 0
        if empty_scans >= QUIESCENCE_EMPTY_SCANS:
            try:
                capture.drain()
            except SupervisionError as error:
                _record_error(cleanup_errors, error)
            return True
        if time.monotonic() >= deadline:
            return False
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
    leader_identity = None
    owned = None
    capture = None
    control_write = None
    error_read = None
    cleanup_errors = []
    cause = "capture_error"
    interrupted_signal = None
    process, control_write, error_read = _launch_inert(argv, cwd)

    try:
        setup_failed = False
        try:
            leader_identity = _wait_for_identity(process)
        except (OSError, SupervisionError) as error:
            _record_error(cleanup_errors, error)
            setup_failed = True

        if leader_identity is None and process.poll() is None:
            try:
                # Popen still owns an unreaped direct child here, so retrying
                # stable binding cannot be redirected through numeric PID reuse.
                leader_identity = _open_identity(process.pid)
            except (OSError, SupervisionError) as error:
                _record_error(cleanup_errors, error)
                setup_failed = True
        if leader_identity is None and process.poll() is None:
            _record_error(
                cleanup_errors,
                f"cannot bind stable identity for process {process.pid}",
            )
            setup_failed = True

        owned = _OwnedProcesses(
            process.pid, leader_identity, baseline_children
        )
        if process.stdout is None:
            _record_error(
                cleanup_errors, "supervised process output pipe was not created"
            )
            capture = _UnavailableCapture(None)
            setup_failed = True
        else:
            try:
                capture = _Capture(process.stdout)
            except (OSError, SupervisionError) as error:
                _record_error(cleanup_errors, error)
                capture = _UnavailableCapture(process.stdout)
                setup_failed = True

        if setup_failed:
            _close_descriptor(control_write)
            control_write = None
            _close_descriptor(error_read)
            error_read = None
            process.wait()
            cause = "capture_error"
        else:
            _release_launcher(control_write)
            control_write = None
            exec_error_read = error_read
            error_read = None
            _raise_exec_error(exec_error_read, argv[0])
            deadline = time.monotonic() + timeout_seconds
            exit_empty_scans = 0
            quiescence_deadline = None
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
                if (
                    return_code is not None
                    and not live
                    and capture.eof
                    and not owned.has_unreaped_adopted_children()
                ):
                    if quiescence_deadline is None:
                        quiescence_deadline = (
                            time.monotonic() + QUIESCENCE_WAIT_SECONDS
                        )
                    exit_empty_scans += 1
                    if exit_empty_scans >= QUIESCENCE_EMPTY_SCANS:
                        cause = "exited"
                        break
                else:
                    exit_empty_scans = 0
                now = time.monotonic()
                if (
                    quiescence_deadline is not None
                    and now >= quiescence_deadline
                ):
                    _record_error(
                        cleanup_errors,
                        "owned process set did not become quiescent after leader exit",
                    )
                    cause = "capture_error"
                    break
                if quiescence_deadline is None and now >= deadline:
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
    except BaseException:
        if control_write is not None:
            _close_descriptor(control_write)
            control_write = None
            process.wait()
        if error_read is not None:
            _close_descriptor(error_read)
            error_read = None
        if owned is None:
            owned = _OwnedProcesses(
                process.pid, leader_identity, baseline_children
            )
        if capture is None:
            capture = _UnavailableCapture(process.stdout)
        _forced_cleanup(process, owned, capture, cleanup_errors)
        try:
            owned.reap_adopted(process)
        except SupervisionError:
            pass
        raise
    finally:
        if control_write is not None:
            _close_descriptor(control_write)
        if error_read is not None:
            _close_descriptor(error_read)
        if capture is not None:
            capture.close()
        elif process.stdout is not None:
            process.stdout.close()
        if owned is not None:
            owned.close()
        elif leader_identity is not None:
            leader_identity.close()
