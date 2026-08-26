#!/usr/bin/env python3
"""Emit candidate-bound not_run evidence for gates without an implementation."""

import argparse
import datetime
import json
import os
import platform
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "lib"))
import evidence


EXIT_NOT_RUN = 3


def read_candidate_snapshot(candidate_lock):
    try:
        return evidence.read_candidate_snapshot(candidate_lock)
    except evidence.EvidenceError as error:
        raise ValueError(str(error)) from error


def timestamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def write_evidence(args, output):
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
        output_digest=evidence.digest_bytes(output),
        tool_versions_json=json.dumps({"python": platform.python_version()}),
        subject_json=json.dumps(
            {
                "kind": "source",
                "digest": evidence.digest_bytes(source_commit.encode("ascii")),
            }
        ),
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
    output = f"not_run: {args.reason}\n".encode("utf-8")
    if args.evidence_out:
        try:
            write_evidence(args, output)
        except ValueError as error:
            print(f"not_run: cannot write evidence: {error}", file=sys.stderr)
            return 1
    sys.stderr.buffer.write(output)
    sys.stderr.buffer.flush()
    return EXIT_NOT_RUN


if __name__ == "__main__":
    raise SystemExit(main())
