#!/usr/bin/env python3
"""Verify the node's monitoring: the configuration in the repository, or a live Prometheus.

    verify-monitoring.sh --source
    verify-monitoring.sh --runtime [--target-file FILE]
        [--candidate-lock FILE --generation-receipt FILE --result-out FILE]

``--source`` checks alert rules (severity, runbook anchors, only exported series, no
impossible comparisons), Compose routing (the metrics network is internal and joined only
by the gateway and Prometheus, no metrics or Prometheus port is published or routed,
retention is bounded) and the metric label vocabulary, then asks ``promtool`` to validate
the Prometheus configuration and the rules. ``promtool`` is the locked Prometheus image
when Podman is present; ``PROMTOOL`` may name a binary instead. Without either, the other
checks still run but the result is ``not_run`` (3), never a pass.

``--runtime`` asks the running Prometheus (through ``podman exec`` on this node, or on a
target node over SSH) whether its target is up and freshly scraped, its rules healthy, no
critical alert firing and every required series present.

Exit codes: 0 verified, 1 a finding, 2 usage, 3 not_run.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "lib"))
import monitoring_check as mc  # noqa: E402
import remote_program as rp  # noqa: E402

EXIT_FAILED, EXIT_USAGE, EXIT_NOT_RUN = 1, 2, 3
PROM_CONTAINER = "telos-prometheus"

REMOTE_PROGRAM = '''
import json, subprocess, time, urllib.parse

def fetch(path):
    result = subprocess.run(["podman", "exec", "telos-prometheus", "wget", "-q", "-O", "-", "http://localhost:9090" + path],
                            capture_output=True, text=True, check=False)
    if result.returncode != 0:
        return None
    return json.loads(result.stdout).get("data")

QUERIES = %(queries)s
out = {"now": time.time(), "targets": fetch("/api/v1/targets"), "rules": fetch("/api/v1/rules"), "alerts": fetch("/api/v1/alerts"), "series": {}}
for query in QUERIES:
    data = fetch("/api/v1/query?query=" + urllib.parse.quote(query))
    out["series"][query] = (data or {}).get("result", [])
print(json.dumps(out))
'''


def load_lock(root):
    return json.loads((root / "release" / "images.lock").read_text(encoding="utf-8"))


def promtool_command(root, lock):
    """argv prefix that runs promtool with config/prometheus visible at /etc/prometheus."""
    binary = os.environ.get("PROMTOOL")
    if binary and Path(binary).is_file():
        return [binary], str(root / "config" / "prometheus"), "local promtool " + _version([binary, "--version"])
    podman = shutil.which("podman")
    entry = lock.get("images", {}).get("prometheus")
    if podman and entry:
        reference = f"{entry['source']}@{entry['manifestDigest']}"
        return ([podman, "run", "--rm", "--network", "none", "-v", f"{root / 'config' / 'prometheus'}:/etc/prometheus:ro,z",
                 "--entrypoint", "promtool", reference], "/etc/prometheus", "locked image " + reference.split("@")[0])
    return None, None, None


def _version(argv):
    try:
        out = subprocess.run(argv, capture_output=True, text=True, timeout=20, check=False).stdout.splitlines()
        return out[0] if out else "unknown"
    except (OSError, subprocess.SubprocessError):
        return "unknown"


def run_promtool(root, lock):
    prefix, directory, label = promtool_command(root, lock)
    if prefix is None:
        return None, []
    findings = []
    for args in (["check", "config", f"{directory}/prometheus.yml"], ["check", "rules", f"{directory}/alerts.yml"]):
        completed = subprocess.run([*prefix, *args], capture_output=True, text=True, timeout=300, check=False)
        if completed.returncode != 0:
            findings.append(f"promtool {' '.join(args[:2])} failed: {(completed.stdout + completed.stderr).strip().splitlines()[-1][:200]}")
    return label, findings


def local_runtime():
    program = REMOTE_PROGRAM % {"queries": json.dumps(list(mc.REQUIRED_SERIES))}
    completed = subprocess.run([sys.executable, "-I", "-"], input=program.encode(), capture_output=True, timeout=180, check=False)
    if completed.returncode != 0:
        raise rp.DrillError("could not query Prometheus: " + completed.stderr.decode()[-200:])
    return json.loads(completed.stdout.decode())


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--source", action="store_true")
    mode.add_argument("--runtime", action="store_true")
    parser.add_argument("--root", default=str(HERE.parent))
    parser.add_argument("--target-file")
    parser.add_argument("--candidate-lock")
    parser.add_argument("--generation-receipt")
    parser.add_argument("--result-out")
    parser.add_argument("--evidence-out")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    lock = load_lock(root)

    if args.source:
        findings = mc.check_source(root, lock)
        label, tool_findings = run_promtool(root, lock)
        findings += tool_findings
        for finding in findings:
            print(f"verify-monitoring: {finding}", file=sys.stderr)
        if findings:
            print(f"verify-monitoring: FAILED ({len(findings)} findings)", file=sys.stderr)
            return EXIT_FAILED
        if label is None:
            print("verify-monitoring: not_run: the repository checks passed, but promtool is unavailable "
                  "(no Podman and no PROMTOOL binary), so the Prometheus configuration was not validated", file=sys.stderr)
            return EXIT_NOT_RUN
        print(f"verify-monitoring: source ok -- alerts, routing, privacy and Prometheus configuration ({label})")
        return 0

    try:
        if args.target_file:
            program = (REMOTE_PROGRAM % {"queries": json.dumps(list(mc.REQUIRED_SERIES))}).encode()
            observed = rp.run_program(args.target_file, program)
        else:
            if shutil.which("podman") is None:
                print("verify-monitoring: not_run: podman is required to query the node's Prometheus", file=sys.stderr)
                return EXIT_NOT_RUN
            observed = local_runtime()
    except rp.NotRun as error:
        print(f"verify-monitoring: not_run: {error}", file=sys.stderr)
        return EXIT_NOT_RUN
    except rp.DrillError as error:
        print(f"verify-monitoring: {error}", file=sys.stderr)
        return EXIT_FAILED
    if observed.get("targets") is None:
        print("verify-monitoring: Prometheus did not answer (is telos-prometheus running?)", file=sys.stderr)
        return EXIT_FAILED
    findings = mc.evaluate_runtime(observed["targets"], observed["rules"], observed["alerts"], observed["series"], observed.get("now", time.time()))
    for finding in findings:
        print(f"verify-monitoring: {finding}", file=sys.stderr)
    code = EXIT_FAILED if findings else 0
    if not findings:
        print("verify-monitoring: runtime ok -- target up and fresh, rules healthy, no critical alert, required series present")
    if args.result_out and not (args.candidate_lock and args.generation_receipt):
        print("verify-monitoring: not_run: a gate result needs --candidate-lock and --generation-receipt; "
              "this inspection is a rehearsal, not evidence", file=sys.stderr)
        return EXIT_NOT_RUN
    return code


if __name__ == "__main__":
    sys.exit(main())
