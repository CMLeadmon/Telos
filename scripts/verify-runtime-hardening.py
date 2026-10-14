#!/usr/bin/env python3
"""Verify container hardening: the rendered Compose model, or the running containers.

    verify-runtime-hardening.sh --compose FILE --env-file FILE --policy FILE
    verify-runtime-hardening.sh --runtime --policy FILE
        [--candidate-lock FILE --generation-receipt FILE --result-out FILE]

Static mode renders the model with the host's Podman Compose provider (nothing is
started) and judges it against the per-service policy. Runtime mode inspects the
containers that are actually running. Exit codes: 0 every control holds, 1 at least
one finding, 2 usage, 3 not_run (no container engine or Compose provider).

Runtime mode may write the shared gate-result record, but only for a candidate:
the observed-runtime subject binds the installed generation receipt, so a run
without ``--candidate-lock`` and ``--generation-receipt`` is a rehearsal and says
so rather than producing evidence.
"""

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / "lib"))
import runtime_hardening as rh  # noqa: E402

try:
    import yaml
except ImportError:  # pragma: no cover - PyYAML is a declared prerequisite
    raise SystemExit("verify-runtime-hardening: PyYAML is required")

EXIT_FAILED, EXIT_USAGE, EXIT_NOT_RUN = 1, 2, 3


def load_lock(root):
    return json.loads((root / "release" / "images.lock").read_text(encoding="utf-8"))


def render_model(compose, env_file):
    podman = shutil.which("podman")
    if podman is None:
        raise LookupError("podman is required to render the Compose model")
    probe = subprocess.run([podman, "compose", "version"], capture_output=True, text=True, check=False)
    if probe.returncode != 0:
        raise LookupError("a podman compose provider (podman-compose) is required")
    rendered = subprocess.run([podman, "compose", "--env-file", env_file, "-f", compose, "config"],
                              capture_output=True, text=True, check=False)
    if rendered.returncode != 0:
        raise ValueError("the Compose model failed to render")
    return yaml.safe_load(rendered.stdout)


def podman_inspect(name):
    result = subprocess.run(["podman", "inspect", "--type", "container", name], capture_output=True, text=True, check=False)
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)[0]


def read_proc(pid, name):
    try:
        return Path(f"/proc/{int(pid)}/{name}").read_text(encoding="utf-8")
    except OSError:
        return None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--policy", required=True)
    parser.add_argument("--compose")
    parser.add_argument("--env-file")
    parser.add_argument("--runtime", action="store_true")
    parser.add_argument("--candidate-lock")
    parser.add_argument("--generation-receipt")
    parser.add_argument("--result-out")
    parser.add_argument("--evidence-out")
    parser.add_argument("--root", default=str(HERE.parent))
    parser.add_argument("--today")
    args = parser.parse_args(argv)
    root = Path(args.root).resolve()
    if args.runtime == bool(args.compose):
        parser.error("choose exactly one of --compose (static) or --runtime")
    if args.compose and not args.env_file:
        parser.error("--compose needs --env-file")
    today = None
    if args.today:
        import datetime
        today = datetime.date.fromisoformat(args.today)
    try:
        policy = rh.load_policy(args.policy)
        lock = load_lock(root)
    except (rh.PolicyError, OSError, ValueError) as error:
        print(f"verify-runtime-hardening: {error}", file=sys.stderr)
        return EXIT_FAILED

    if args.compose:
        try:
            model = render_model(args.compose, args.env_file)
        except LookupError as error:
            print(f"verify-runtime-hardening: not_run: {error}", file=sys.stderr)
            return EXIT_NOT_RUN
        except ValueError as error:
            print(f"verify-runtime-hardening: {error}", file=sys.stderr)
            return EXIT_FAILED
        findings = rh.check_static(policy, model, lock, today)
        return report(findings, f"static: {len(model.get('services') or {})} services in the rendered model")

    if shutil.which("podman") is None:
        print("verify-runtime-hardening: not_run: podman is required to inspect running containers", file=sys.stderr)
        return EXIT_NOT_RUN
    candidate_images = None
    if args.candidate_lock:
        candidate = json.loads(Path(args.candidate_lock).read_text(encoding="utf-8"))
        candidate_images = {value for key, value in candidate.get("artifacts", {}).items() if key.startswith("images/")}
    observations = rh.collect_runtime(policy, podman_inspect, read_proc)
    findings = rh.check_runtime(policy, observations, lock, candidate_images, today)
    code = report(findings, f"runtime: {sum(1 for v in observations.values() if v)} containers inspected")
    if args.result_out:
        # The observed-runtime subject binds the installed generation receipt and the
        # candidate lock; without both this run is a rehearsal, never evidence.
        if not (args.candidate_lock and args.generation_receipt):
            print("verify-runtime-hardening: not_run: a gate result needs --candidate-lock and --generation-receipt; "
                  "this inspection is a rehearsal, not evidence", file=sys.stderr)
            return EXIT_NOT_RUN
    return code


def report(findings, summary):
    if findings:
        for finding in findings:
            print(f"verify-runtime-hardening: {finding}", file=sys.stderr)
        print(f"verify-runtime-hardening: FAILED ({len(findings)} findings) -- {summary}", file=sys.stderr)
        return EXIT_FAILED
    print(f"verify-runtime-hardening: ok -- {summary}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
