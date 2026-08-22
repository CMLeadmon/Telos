#!/usr/bin/env python3
"""Emit candidate-bound not_run evidence for gates without an implementation."""

import argparse
import datetime
import json
import os
import platform
import subprocess
import sys


EXIT_NOT_RUN = 3
EMPTY_OUTPUT_DIGEST = "sha256:" + ("e3b0c44298fc1c149afbf4c8996fb924" "27ae41e4649b934ca495991b7852b855")


def parse_candidate_source_commit(candidate_lock):
    try:
        with open(candidate_lock, encoding="utf-8") as candidate_file:
            candidate = json.load(candidate_file)
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read candidate lock: {error}") from error
    source_commit = candidate.get("sourceCommit") if isinstance(candidate, dict) else None
    if not isinstance(source_commit, str) or len(source_commit) != 40:
        raise ValueError("candidate lock must contain a 40-character sourceCommit")
    if any(character not in "0123456789abcdef" for character in source_commit):
        raise ValueError("candidate lock must contain a lowercase hexadecimal sourceCommit")
    return source_commit


def timestamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def write_evidence(args):
    source_commit = parse_candidate_source_commit(args.candidate_lock)
    evidence_path = os.path.join(os.path.dirname(__file__), "lib", "evidence.py")
    digest_result = subprocess.run(
        [sys.executable, evidence_path, "digest", args.candidate_lock],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if digest_result.returncode != 0:
        raise ValueError(digest_result.stderr.strip() or "cannot digest candidate lock")
    candidate_digest = digest_result.stdout.strip()
    started_at = timestamp()
    finished_at = timestamp()
    write_result = subprocess.run(
        [
            sys.executable,
            evidence_path,
            "write",
            "--output", args.evidence_out,
            "--gate-id", args.gate_id,
            "--status", "not_run",
            "--reason", args.reason,
            "--source-commit", source_commit,
            "--candidate-lock-digest", candidate_digest,
            "--command-json", json.dumps(args.original_argv, separators=(",", ":")),
            "--started-at", started_at,
            "--finished-at", finished_at,
            "--exit-code", str(EXIT_NOT_RUN),
            "--output-digest", EMPTY_OUTPUT_DIGEST,
            "--tool-versions-json", json.dumps({"python": platform.python_version()}),
            "--subject-json", json.dumps({"kind": "source", "digest": candidate_digest}),
        ],
        check=False,
    )
    if write_result.returncode != 0:
        raise ValueError("evidence writer rejected the not_run envelope")


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gate-id", required=True)
    parser.add_argument("--reason", required=True)
    parser.add_argument("--candidate-lock")
    parser.add_argument("--evidence-out")
    parser.add_argument("original_argv", nargs=argparse.REMAINDER)
    return parser


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    parser = build_parser()
    if "--" not in argv:
        parser.error("an original argv array must follow --")
    args = parser.parse_args(argv)
    if args.original_argv[:1] == ["--"]:
        args.original_argv = args.original_argv[1:]
    if not args.original_argv:
        parser.error("an original argv array must follow --")
    if args.evidence_out and not args.candidate_lock:
        parser.error("--candidate-lock is required with --evidence-out")
    if args.evidence_out:
        try:
            write_evidence(args)
        except ValueError as error:
            print(f"not_run: cannot write evidence: {error}", file=sys.stderr)
            return 1
    print(f"not_run: {args.reason}", file=sys.stderr)
    return EXIT_NOT_RUN


if __name__ == "__main__":
    raise SystemExit(main())
