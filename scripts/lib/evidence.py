#!/usr/bin/env python3
"""Write and fail-closed verify Telos release evidence envelopes."""

import argparse
import datetime
import hashlib
import json
import os
import re
import tempfile


class EvidenceError(Exception):
    """An invalid evidence contract or invocation."""


REQUIRED_FIELDS = (
    "schemaVersion", "gateId", "status", "reason", "sourceCommit",
    "candidateLockDigest", "command", "startedAt", "finishedAt", "exitCode",
    "outputDigest", "toolVersions", "subject",
)
STATUSES = ("passed", "failed", "not_run")
SUBJECT_KINDS = ("source", "artifact", "runtime", "external-host", "fixture")
TOOL_VERSION_KEYS = (
    "python", "bash", "sh", "python3", "node", "npm", "npx", "podman", "git",
)
SHA256_PATTERN = re.compile(r"sha256:[0-9a-f]{64}\Z")
GATE_ID_PATTERN = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*\Z")
COMMIT_PATTERN = re.compile(r"[0-9a-f]{40}\Z")
TIMESTAMP_PATTERN = re.compile(
    r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\Z"
)
FORBIDDEN_KEY_PATTERN = re.compile(
    r"token|password|secret|credential|authorization", re.IGNORECASE
)


def fail(message):
    raise EvidenceError(message)


def is_integer(value):
    return isinstance(value, int) and not isinstance(value, bool)


def require_string(value, field):
    if not isinstance(value, str):
        fail(f"{field} must be a string")
    return value


def require_pattern(value, field, pattern):
    require_string(value, field)
    if not pattern.fullmatch(value):
        fail(f"{field} has an invalid value")


def parse_timestamp(value, field):
    require_string(value, field)
    if not TIMESTAMP_PATTERN.fullmatch(value):
        fail(f"{field} must be an RFC 3339 date-time")
    try:
        return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        fail(f"{field} must be an RFC 3339 date-time: {error}")


def reject_forbidden_keys(value, path="$"):
    if isinstance(value, dict):
        for key, child in value.items():
            if FORBIDDEN_KEY_PATTERN.search(key):
                fail(f"forbidden secret-looking key at {path}.{key}")
            reject_forbidden_keys(child, f"{path}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            reject_forbidden_keys(child, f"{path}[{index}]")


def validate_envelope(envelope, candidate_digest=None, candidate_source_commit=None):
    if not isinstance(envelope, dict):
        fail("envelope must be an object")
    reject_forbidden_keys(envelope)

    missing = [field for field in REQUIRED_FIELDS if field not in envelope]
    if missing:
        fail(f"envelope missing required field: {missing[0]}")
    unknown = set(envelope) - set(REQUIRED_FIELDS)
    if unknown:
        fail(f"envelope has unknown field: {sorted(unknown)[0]}")

    if envelope["schemaVersion"] != 2 or isinstance(envelope["schemaVersion"], bool):
        fail("schemaVersion must equal 2")
    require_pattern(envelope["gateId"], "gateId", GATE_ID_PATTERN)
    if envelope["status"] not in STATUSES:
        fail("status must be passed, failed, or not_run")
    if not isinstance(envelope["reason"], str) or not envelope["reason"]:
        fail("reason must be a non-empty string")
    require_pattern(envelope["sourceCommit"], "sourceCommit", COMMIT_PATTERN)
    require_pattern(envelope["candidateLockDigest"], "candidateLockDigest", SHA256_PATTERN)

    command = envelope["command"]
    if not isinstance(command, list) or not command:
        fail("command must be a non-empty array")
    if any(not isinstance(part, str) for part in command):
        fail("command entries must be strings")

    started_at = parse_timestamp(envelope["startedAt"], "startedAt")
    finished_at = parse_timestamp(envelope["finishedAt"], "finishedAt")
    if finished_at < started_at:
        fail("finishedAt must not be earlier than startedAt")

    exit_code = envelope["exitCode"]
    if not is_integer(exit_code) or not 0 <= exit_code <= 255:
        fail("exitCode must be an integer from 0 through 255")
    if envelope["status"] == "passed" and exit_code != 0:
        fail("passed evidence must have exitCode 0")
    if envelope["status"] == "not_run" and exit_code != 3:
        fail("not_run evidence must have exitCode 3")
    if envelope["status"] == "failed" and (exit_code == 0 or exit_code == 3):
        fail("failed evidence must have a nonzero exitCode other than 3")

    require_pattern(envelope["outputDigest"], "outputDigest", SHA256_PATTERN)
    tool_versions = envelope["toolVersions"]
    if not isinstance(tool_versions, dict) or not tool_versions:
        fail("toolVersions must be a non-empty object")
    unknown_tools = set(tool_versions) - set(TOOL_VERSION_KEYS)
    if unknown_tools:
        fail(f"toolVersions has unknown tool: {sorted(unknown_tools)[0]}")
    if any(not isinstance(value, str) or not value for value in tool_versions.values()):
        fail("toolVersions values must be non-empty strings")

    subject = envelope["subject"]
    if not isinstance(subject, dict):
        fail("subject must be an object")
    subject_missing = {"kind", "digest"} - set(subject)
    if subject_missing:
        fail(f"subject missing required field: {sorted(subject_missing)[0]}")
    subject_unknown = set(subject) - {"kind", "digest"}
    if subject_unknown:
        fail(f"subject has unknown field: {sorted(subject_unknown)[0]}")
    if subject["kind"] not in SUBJECT_KINDS:
        fail("subject.kind is invalid")
    require_pattern(subject["digest"], "subject.digest", SHA256_PATTERN)
    if subject["kind"] == "source":
        expected_source_digest = digest_bytes(envelope["sourceCommit"].encode("ascii"))
        if subject["digest"] != expected_source_digest:
            fail("source subject digest does not match sourceCommit")

    if candidate_digest is not None and envelope["candidateLockDigest"] != candidate_digest:
        fail("candidateLockDigest does not match candidate lock")
    if (
        candidate_source_commit is not None
        and envelope["sourceCommit"] != candidate_source_commit
    ):
        fail("sourceCommit does not match candidate lock")


def load_schema(path):
    try:
        with open(path, encoding="utf-8") as schema_file:
            schema = json.load(schema_file)
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot load schema: {error}")
    if not isinstance(schema, dict):
        fail("schema must be an object")
    if schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
        fail("schema must declare draft 2020-12")
    if schema.get("type") != "object" or schema.get("additionalProperties") is not False:
        fail("schema must close the envelope object")
    if schema.get("required") != list(REQUIRED_FIELDS):
        fail("schema required fields do not match the evidence contract")
    properties = schema.get("properties")
    if not isinstance(properties, dict) or set(properties) != set(REQUIRED_FIELDS):
        fail("schema properties do not match the evidence contract")
    expected_properties = {
        "schemaVersion": {"const": 2},
        "gateId": {"type": "string", "pattern": "^[a-z0-9]+(?:-[a-z0-9]+)*$"},
        "status": {"enum": list(STATUSES)},
        "reason": {"type": "string", "minLength": 1},
        "sourceCommit": {"type": "string", "pattern": "^[0-9a-f]{40}$"},
        "candidateLockDigest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
        "command": {"type": "array", "minItems": 1, "items": {"type": "string"}},
        "startedAt": {"type": "string", "format": "date-time"},
        "finishedAt": {"type": "string", "format": "date-time"},
        "exitCode": {"type": "integer", "minimum": 0, "maximum": 255},
        "outputDigest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
        "toolVersions": {
            "type": "object",
            "minProperties": 1,
            "additionalProperties": False,
            "properties": {
                key: {"type": "string", "minLength": 1}
                for key in TOOL_VERSION_KEYS
            },
        },
        "subject": {
            "type": "object",
            "additionalProperties": False,
            "required": ["kind", "digest"],
            "properties": {
                "kind": {"enum": list(SUBJECT_KINDS)},
                "digest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
            },
        },
    }
    for field, expected in expected_properties.items():
        if properties[field] != expected:
            fail(f"schema contract is invalid for {field}")
    return schema


def digest_bytes(payload):
    digest = hashlib.sha256()
    digest.update(payload)
    return f"sha256:{digest.hexdigest()}"


def digest_path(path):
    digest = hashlib.sha256()
    try:
        with open(path, "rb") as input_file:
            for chunk in iter(lambda: input_file.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        fail(f"cannot read {path}: {error}")
    return f"sha256:{digest.hexdigest()}"


def read_candidate_snapshot(path):
    try:
        with open(path, "rb") as candidate_file:
            candidate_bytes = candidate_file.read()
        candidate = json.loads(candidate_bytes.decode("utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        fail(f"cannot read candidate lock: {error}")
    source_commit = candidate.get("sourceCommit") if isinstance(candidate, dict) else None
    if not isinstance(source_commit, str) or not COMMIT_PATTERN.fullmatch(source_commit):
        fail("candidate lock must contain a lowercase hexadecimal sourceCommit")
    return source_commit, digest_bytes(candidate_bytes)


def load_envelope(path):
    try:
        with open(path, encoding="utf-8") as envelope_file:
            return json.load(envelope_file)
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot load envelope {path}: {error}")


def json_option(value, option):
    try:
        return json.loads(value)
    except json.JSONDecodeError as error:
        fail(f"{option} must contain JSON: {error}")


def write_envelope(args):
    envelope = {
        "schemaVersion": 2,
        "gateId": args.gate_id,
        "status": args.status,
        "reason": args.reason,
        "sourceCommit": args.source_commit,
        "candidateLockDigest": args.candidate_lock_digest,
        "command": json_option(args.command_json, "--command-json"),
        "startedAt": args.started_at,
        "finishedAt": args.finished_at,
        "exitCode": args.exit_code,
        "outputDigest": args.output_digest,
        "toolVersions": json_option(args.tool_versions_json, "--tool-versions-json"),
        "subject": json_option(args.subject_json, "--subject-json"),
    }
    validate_envelope(envelope)
    output_dir = os.path.dirname(os.path.abspath(args.output))
    output_path = os.path.abspath(args.output)
    directory_descriptor = None
    descriptor = None
    temporary_path = None
    try:
        directory_descriptor = os.open(
            output_dir, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0)
        )
        descriptor, temporary_path = tempfile.mkstemp(
            dir=output_dir, prefix=".evidence-", suffix=".tmp"
        )
        try:
            payload = json.dumps(envelope, sort_keys=True, separators=(",", ":")).encode("utf-8")
            os.fchmod(descriptor, 0o600)
            temporary_file = os.fdopen(descriptor, "wb")
            descriptor = None
            with temporary_file:
                temporary_file.write(payload)
                temporary_file.flush()
                os.fsync(temporary_file.fileno())
            os.link(temporary_path, output_path, follow_symlinks=False)
        finally:
            try:
                if descriptor is not None:
                    os.close(descriptor)
            finally:
                try:
                    try:
                        os.unlink(temporary_path)
                    except FileNotFoundError:
                        pass
                finally:
                    os.fsync(directory_descriptor)
    except FileExistsError as error:
        fail(f"cannot write envelope: output already exists: {error.filename}")
    except OSError as error:
        fail(f"cannot write envelope: {error}")
    finally:
        if directory_descriptor is not None:
            os.close(directory_descriptor)


def verify_envelopes(args):
    load_schema(args.schema)
    source_commit, candidate_digest = read_candidate_snapshot(args.candidate_lock)
    for envelope_path in args.envelopes:
        validate_envelope(
            load_envelope(envelope_path), candidate_digest, source_commit
        )


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="subcommand", required=True)
    digest_parser = subparsers.add_parser("digest")
    digest_parser.add_argument("path")
    write_parser = subparsers.add_parser("write")
    write_parser.add_argument("--output", required=True)
    write_parser.add_argument("--gate-id", required=True)
    write_parser.add_argument("--status", required=True, choices=STATUSES)
    write_parser.add_argument("--reason", required=True)
    write_parser.add_argument("--source-commit", required=True)
    write_parser.add_argument("--candidate-lock-digest", required=True)
    write_parser.add_argument("--command-json", required=True)
    write_parser.add_argument("--started-at", required=True)
    write_parser.add_argument("--finished-at", required=True)
    write_parser.add_argument("--exit-code", required=True, type=int)
    write_parser.add_argument("--output-digest", required=True)
    write_parser.add_argument("--tool-versions-json", required=True)
    write_parser.add_argument("--subject-json", required=True)
    verify_parser = subparsers.add_parser("verify")
    verify_parser.add_argument("--schema", required=True)
    verify_parser.add_argument("--candidate-lock", required=True)
    verify_parser.add_argument("envelopes", nargs="+")
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        if args.subcommand == "digest":
            print(digest_path(args.path))
        elif args.subcommand == "write":
            write_envelope(args)
        else:
            verify_envelopes(args)
    except EvidenceError as error:
        print(f"evidence: {error}", file=os.sys.stderr)
        return 1
    except OSError as error:
        print(f"evidence: {error}", file=os.sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
