#!/usr/bin/env python3
"""Run one fixed, read-only Python program on a drill target and parse its JSON.

The target is a protected descriptor (release/drill-target.schema.json). The node's
machine identity is checked before anything runs, the program travels on stdin of
``python3 -I -`` (locally, or over SSH with strict host-key checking and a protected
config), and its arguments are restricted to a safe alphabet. A verifier builds its
program from code in this repository; nothing in the descriptor can change what runs.
"""

import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import drill_common as dc  # noqa: E402

NotRun = dc.NotRun
DrillError = dc.DrillError


def run_program(target_file, program, timeout=180):
    target = dc.load_target(target_file)
    remote = dc.Remote(target)
    dc.verify_node_identity(remote, target)  # never act on a machine the descriptor does not name
    try:
        completed = subprocess.run(remote.command([]), input=program, capture_output=True, timeout=timeout, check=False)
    except FileNotFoundError:
        raise NotRun("ssh is not installed on the machine running the check")
    except subprocess.TimeoutExpired:
        raise DrillError(f"the node program timed out after {timeout}s")
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", "replace").strip().splitlines()[-1:] or ["no diagnostic"]
        raise DrillError(f"the node program failed ({completed.returncode}): {detail[0][:200]}")
    try:
        return json.loads(completed.stdout.decode("utf-8"))
    except ValueError:
        raise DrillError("the node program returned malformed output")
