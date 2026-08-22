#!/usr/bin/env python3
"""Emit candidate-bound not_run evidence for gates without an implementation."""

import argparse
import datetime
import json
import os
import platform
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "lib"))
import evidence


EXIT_NOT_RUN = 3
EMPTY_OUTPUT_DIGEST = "sha256:" + ("e3b0c44298fc1c149afbf4c8996fb924" "27ae41e4649b934ca495991b7852b855")


def read_candidate_snapshot(candidate_lock):
    try:
        with open(candidate_lock, "rb") as candidate_file:
            candidate_bytes = candidate_file.read()
        candidate = json.loads(candidate_bytes.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read candidate lock: {error}") from error
    source_commit = candidate.get("sourceCommit") if isinstance(candidate, dict) else None
    if not isinstance(source_commit, str) or len(source_commit) != 40:
        raise ValueError("candidate lock must contain a 40-character sourceCommit")
    if any(character not in "0123456789abcdef" for character in source_commit):
        raise ValueError("candidate lock must contain a lowercase hexadecimal sourceCommit")
    return source_commit, evidence.digest_bytes(candidate_bytes)


def timestamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def write_evidence(args):
    source_commit, candidate_digest = read_candidate_snapshot(args.candidate_lock)
    started_at = timestamp()
    finished_at = timestamp()
    envelope_args = argparse.Namespace(
        output=args.evidence_out,
        gate_id=args.gate_id,
        status="not_run",
        reason=args.reason,
        source_commit=source_commit,
        candidate_lock_digest=candidate_digest,
        command_json=json.dumps(args.original_argv, separators=(",", ":")),
        started_at=started_at,
        finished_at=finished_at,
        exit_code=EXIT_NOT_RUN,
        output_digest=EMPTY_OUTPUT_DIGEST,
        tool_versions_json=json.dumps({"python": platform.python_version()}),
        subject_json=json.dumps({"kind": "source", "digest": candidate_digest}),
    )
    try:
        evidence.write_envelope(envelope_args)
    except evidence.EvidenceError as error:
        raise ValueError(f"evidence writer rejected the not_run envelope: {error}") from error


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
