#!/usr/bin/env python3
"""Drive an interactive command through a real pseudo-terminal (test helper).

Usage: pty_drive.py TRANSCRIPT [--timeout N] -- COMMAND [ARG...]
The step script is a JSON array on stdin; each step is one of
  {"expect": REGEX}                      wait for output matching REGEX
  {"send": TEXT}                         type TEXT (use \\n for Enter)
  {"signal": "INT"|"TERM"|"HUP"|"KILL"}  signal the child
Everything the terminal displayed is appended to TRANSCRIPT, so a secret that was
echoed would appear there. Exits with the child's exit status (128+N if signalled).
"""
import json
import os
import pty
import re
import select
import signal
import sys
import time


def main():
    separator = sys.argv.index("--")
    transcript_path = sys.argv[1]
    timeout = 20.0
    if "--timeout" in sys.argv[2:separator]:
        timeout = float(sys.argv[sys.argv.index("--timeout") + 1])
    command = sys.argv[separator + 1:]
    steps = json.load(sys.stdin)
    pid, master = pty.fork()
    if pid == 0:
        os.execvp(command[0], command)
    buffer = ""
    log = open(transcript_path, "ab")

    def pump(wait):
        nonlocal buffer
        ready, _, _ = select.select([master], [], [], wait)
        if not ready:
            return False
        try:
            data = os.read(master, 4096)
        except OSError:
            return False
        if not data:
            return False
        log.write(data)
        log.flush()
        buffer += data.decode("utf-8", "replace")
        return True

    for step in steps:
        deadline = time.time() + timeout
        if "expect" in step:
            pattern = re.compile(step["expect"])
            while not pattern.search(buffer):
                if time.time() > deadline:
                    log.write(f"\n[pty_drive] timed out waiting for {step['expect']!r}\n".encode())
                    os.kill(pid, signal.SIGKILL)
                    os.waitpid(pid, 0)
                    return 97
                pump(0.1)
            buffer = buffer[pattern.search(buffer).end():]
        elif "send" in step:
            os.write(master, step["send"].encode())
        elif "signal" in step:
            os.kill(pid, getattr(signal, "SIG" + step["signal"]))
    end = time.time() + timeout
    while time.time() < end:
        pump(0.1)
        done, status = os.waitpid(pid, os.WNOHANG)
        if done:
            while pump(0.05):
                pass
            return os.waitstatus_to_exitcode(status) if os.waitstatus_to_exitcode(status) >= 0 else 128 - os.waitstatus_to_exitcode(status)
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    return 98


if __name__ == "__main__":
    sys.exit(main())
