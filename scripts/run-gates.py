#!/usr/bin/env python3
"""Execute Telos gates from a fully validated argv-only catalog."""

import argparse
import datetime
import json
import os
import platform
import re
import signal
import stat
import subprocess
import sys


SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(SCRIPT_DIR)
sys.dont_write_bytecode = True
sys.path.insert(0, os.path.join(SCRIPT_DIR, "lib"))
import evidence
import process_supervisor


EXIT_FAILED = 1
EXIT_USAGE = 2
EXIT_NOT_RUN = 3
CATALOG_FIELDS = {"schemaVersion", "gates"}
REQUIRED_GATE_FIELDS = {
    "id", "phase", "scope", "required", "timeoutSeconds", "command",
    "prerequisites", "subjectKind",
}
OPTIONAL_GATE_FIELDS = {"terminationGraceSeconds"}
GATE_FIELDS = REQUIRED_GATE_FIELDS | OPTIONAL_GATE_FIELDS
# Catalog v2 adds three required, closed fields. v1 stays parseable for the
# Phase 1 fixtures but can never certify a release.
V2_GATE_FIELDS = {"subjectSelector", "maxAgeHours", "requiredPlatforms"}
CATALOG_VERSIONS = {1, 2}
RESERVED_AGGREGATE = "beta-certification"
AGGREGATE_GUIDANCE = (
    "beta-certification is the terminal aggregate and is never scheduled by the runner; import the "
    "evidence and run: scripts/certify-beta.sh --candidate PATH --evidence-bundle PATH "
    "--catalog ci/phase-gates.json --out-dir PATH"
)
GATE_ID_PATTERN = re.compile(r"[a-z0-9]+(?:-[a-z0-9]+)*\Z")
SCOPES = {"local", "external"}
SUBJECT_KINDS = set(evidence.SUBJECT_KINDS)
VERSIONED_TOOLS = {"bash", "python3", "node", "npm", "npx", "podman", "git"}
TOOL_VERSION_TIMEOUT_SECONDS = 5
SHELL_OPTIONS_WITH_ARGUMENT = {
    "-O", "+O", "-o", "+o", "--init-file", "--rcfile",
}


class GateError(Exception):
    """A catalog, invocation, or evidence-output error."""


def load_producers():
    """Import the v2 producer support only when a v2 catalog needs it."""
    import gate_producers
    return gate_producers


def is_integer(value):
    return isinstance(value, int) and not isinstance(value, bool)


def shell_uses_command_string(command: list[str]) -> bool:
    if os.path.basename(command[0]) not in {"bash", "sh"}:
        return False

    index = 1
    while index < len(command):
        argument = command[index]
        if argument == "--":
            return False
        if argument in SHELL_OPTIONS_WITH_ARGUMENT:
            index += 2
            continue
        if any(
            argument.startswith(option) and argument != option
            for option in ("-O", "+O", "-o", "+o")
        ):
            index += 1
            continue
        if argument.startswith("--"):
            index += 1
            continue
        if argument.startswith("-") and argument != "-":
            if "c" in argument[1:]:
                return True
            index += 1
            continue
        if argument.startswith("+") and argument != "+":
            index += 1
            continue
        return False
    return False


def timestamp():
    return (
        datetime.datetime.now(datetime.timezone.utc)
        .isoformat(timespec="seconds")
        .replace("+00:00", "Z")
    )


def load_catalog(path):
    try:
        input_stat = os.lstat(path)
        if not stat.S_ISREG(input_stat.st_mode):
            raise GateError("catalog must be a regular file")
        with open(path, encoding="utf-8") as input_file:
            catalog = json.load(input_file)
    except GateError:
        raise
    except (OSError, json.JSONDecodeError) as error:
        raise GateError(f"cannot load catalog: {error}") from error
    validate_catalog(catalog)
    return catalog


def validate_catalog(catalog):
    if not isinstance(catalog, dict):
        raise GateError("catalog must be an object")
    unknown = set(catalog) - CATALOG_FIELDS
    if unknown:
        raise GateError(f"catalog has unknown field: {sorted(unknown)[0]}")
    missing = CATALOG_FIELDS - set(catalog)
    if missing:
        raise GateError(f"catalog is missing field: {sorted(missing)[0]}")
    version = catalog["schemaVersion"]
    if not is_integer(version) or version not in CATALOG_VERSIONS:
        raise GateError("catalog schemaVersion must equal 1 or 2")
    v2 = version == 2
    allowed_fields = GATE_FIELDS | (V2_GATE_FIELDS if v2 else set())
    required_fields = REQUIRED_GATE_FIELDS | (V2_GATE_FIELDS if v2 else set())
    producers = load_producers() if v2 else None
    gates = catalog["gates"]
    if not isinstance(gates, list) or not gates:
        raise GateError("catalog gates must be a non-empty array")

    gate_by_id = {}
    for index, gate in enumerate(gates):
        prefix = f"gate at index {index}"
        if not isinstance(gate, dict):
            raise GateError(f"{prefix} must be an object")
        unknown = set(gate) - allowed_fields
        if unknown:
            raise GateError(f"{prefix} has unknown field: {sorted(unknown)[0]}")
        missing = required_fields - set(gate)
        if missing:
            raise GateError(f"{prefix} is missing field: {sorted(missing)[0]}")

        gate_id = gate["id"]
        if not isinstance(gate_id, str) or not GATE_ID_PATTERN.fullmatch(gate_id):
            raise GateError(f"{prefix} id must be stable kebab-case")
        if gate_id in gate_by_id:
            raise GateError(f"duplicate gate id: {gate_id}")
        gate_by_id[gate_id] = gate

        if not is_integer(gate["phase"]) or gate["phase"] <= 0:
            raise GateError(f"gate {gate_id} phase must be a positive integer")
        if not isinstance(gate["scope"], str) or gate["scope"] not in SCOPES:
            raise GateError(f"gate {gate_id} scope must be local or external")
        if not isinstance(gate["required"], bool):
            raise GateError(f"gate {gate_id} required must be a boolean")
        timeout = gate["timeoutSeconds"]
        if not is_integer(timeout) or timeout <= 0:
            raise GateError(f"gate {gate_id} timeoutSeconds must be a positive integer")
        command = gate["command"]
        if not isinstance(command, list) or not command:
            raise GateError(f"gate {gate_id} command must be a non-empty argv array")
        if any(not isinstance(argument, str) for argument in command):
            raise GateError(f"gate {gate_id} command entries must be strings")
        if any("\0" in argument for argument in command):
            raise GateError(f"gate {gate_id} command entries must not contain NUL")
        if not command[0]:
            raise GateError(f"gate {gate_id} command executable must not be empty")
        if shell_uses_command_string(command):
            raise GateError(
                f"gate {gate_id} must not use shell command strings"
            )
        if "terminationGraceSeconds" in gate:
            grace = gate["terminationGraceSeconds"]
            if not is_integer(grace) or grace < 0:
                raise GateError(
                    f"gate {gate_id} terminationGraceSeconds must be "
                    "a non-negative integer"
                )
        prerequisites = gate["prerequisites"]
        if not isinstance(prerequisites, list):
            raise GateError(f"gate {gate_id} prerequisites must be an array")
        if any(
            not isinstance(prerequisite, str)
            or not GATE_ID_PATTERN.fullmatch(prerequisite)
            for prerequisite in prerequisites
        ):
            raise GateError(f"gate {gate_id} prerequisites must be kebab-case IDs")
        if len(prerequisites) != len(set(prerequisites)):
            raise GateError(f"gate {gate_id} has duplicate prerequisites")
        if (
            not isinstance(gate["subjectKind"], str)
            or gate["subjectKind"] not in SUBJECT_KINDS
        ):
            raise GateError(f"gate {gate_id} subjectKind is invalid")
        if v2:
            try:
                producers.validate_v2_gate(gate, f"gate {gate_id}")
            except producers.ProducerError as error:
                raise GateError(str(error)) from error

    if v2:
        for gate in gates:
            if RESERVED_AGGREGATE in gate["prerequisites"]:
                raise GateError(
                    f"gate {gate['id']} may not depend on the terminal aggregate {RESERVED_AGGREGATE}"
                )

    for gate in gates:
        for prerequisite in gate["prerequisites"]:
            if prerequisite not in gate_by_id:
                raise GateError(
                    f"gate {gate['id']} has unknown prerequisite: {prerequisite}"
                )

    visiting = set()
    visited = set()

    def visit(gate_id, path):
        if gate_id in visiting:
            cycle_start = path.index(gate_id)
            cycle = path[cycle_start:] + [gate_id]
            raise GateError(f"prerequisite cycle: {' -> '.join(cycle)}")
        if gate_id in visited:
            return
        visiting.add(gate_id)
        for prerequisite in gate_by_id[gate_id]["prerequisites"]:
            visit(prerequisite, path + [gate_id])
        visiting.remove(gate_id)
        visited.add(gate_id)

    for gate in gates:
        visit(gate["id"], [])


def read_candidate_lock(path):
    try:
        input_stat = os.lstat(path)
        if not stat.S_ISREG(input_stat.st_mode):
            raise GateError("candidate lock must be a regular file")
        with open(path, "rb") as input_file:
            candidate_bytes = input_file.read()
        candidate = json.loads(candidate_bytes.decode("utf-8"))
    except GateError:
        raise
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise GateError(f"cannot load candidate lock: {error}") from error
    source_commit = candidate.get("sourceCommit") if isinstance(candidate, dict) else None
    if (
        not isinstance(source_commit, str)
        or not evidence.COMMIT_PATTERN.fullmatch(source_commit)
    ):
        raise GateError(
            "candidate lock must contain a lowercase hexadecimal sourceCommit"
        )
    return source_commit, evidence.digest_bytes(candidate_bytes)


def current_checkout_commit():
    try:
        completed = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=REPO_ROOT,
            shell=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
    except OSError as error:
        raise GateError(f"cannot resolve current checkout commit: {error}") from error
    commit = completed.stdout.strip()
    if completed.returncode != 0 or not evidence.COMMIT_PATTERN.fullmatch(commit):
        raise GateError("cannot resolve current checkout commit")
    return commit


def checkout_has_source_changes():
    try:
        completed = subprocess.run(
            [
                "git", "status", "--porcelain=v1", "-z",
                "--untracked-files=all", "--ignored=no",
            ],
            cwd=REPO_ROOT,
            shell=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except OSError as error:
        raise GateError(f"cannot inspect current checkout state: {error}") from error
    if completed.returncode != 0:
        raise GateError("cannot inspect current checkout state")
    return bool(completed.stdout)


def source_checkout_problem(source_commit):
    if current_checkout_commit() != source_commit:
        return "HEAD does not match candidate sourceCommit"
    if checkout_has_source_changes():
        return "tree is dirty relative to candidate sourceCommit"
    return None


def validate_subject_bindings(gates, source_commit):
    if any(gate["subjectKind"] == "source" for gate in gates):
        problem = source_checkout_problem(source_commit)
        if problem:
            raise GateError(f"source checkout {problem}")


def select_gates(catalog, scope, requested_ids, phase):
    gates = catalog["gates"]
    gate_by_id = {gate["id"]: gate for gate in gates}
    if len(requested_ids) != len(set(requested_ids)):
        raise GateError("a gate ID may be selected only once")
    if RESERVED_AGGREGATE in requested_ids:
        raise GateError(AGGREGATE_GUIDANCE)
    # A catalog v2 run never executes a prerequisite from another scope: an
    # external prerequisite of a local gate (or a local one of an external gate)
    # must arrive as verified prior evidence, never be run implicitly.
    same_scope_only = catalog.get("schemaVersion") == 2

    seeds = []
    if phase is not None:
        seeds.extend(
            gate["id"]
            for gate in gates
            if gate["scope"] == scope and gate["phase"] <= phase
            and gate["id"] != RESERVED_AGGREGATE
        )
    if requested_ids:
        for gate_id in requested_ids:
            gate = gate_by_id.get(gate_id)
            if gate is None:
                raise GateError(f"unknown selected gate: {gate_id}")
            if gate["scope"] != scope:
                raise GateError(
                    f"selected gate {gate_id} has scope {gate['scope']}, not {scope}"
                )
            if gate_id not in seeds:
                seeds.append(gate_id)
    elif phase is None:
        seeds.extend(
            gate["id"] for gate in gates
            if gate["scope"] == scope and gate["id"] != RESERVED_AGGREGATE
        )
    if not seeds:
        raise GateError("gate selection is empty")

    selected = set()

    def include(gate_id):
        if gate_id in selected:
            return
        selected.add(gate_id)
        for prerequisite in gate_by_id[gate_id]["prerequisites"]:
            if same_scope_only and gate_by_id[prerequisite]["scope"] != scope:
                continue
            include(prerequisite)

    for gate_id in seeds:
        include(gate_id)

    ordered = []
    emitted = set()

    def emit(gate_id):
        if gate_id in emitted:
            return
        for prerequisite in gate_by_id[gate_id]["prerequisites"]:
            if prerequisite in selected:
                emit(prerequisite)
        emitted.add(gate_id)
        ordered.append(gate_by_id[gate_id])

    for gate in gates:
        if gate["id"] in selected:
            emit(gate["id"])
    return ordered


def create_evidence_directory(path):
    path = os.path.abspath(path)
    parent = os.path.dirname(path)
    if not os.path.isdir(parent):
        raise GateError("evidence directory parent does not exist")
    try:
        os.mkdir(path, 0o700)
        os.chmod(path, 0o700)
    except FileExistsError as error:
        raise GateError("evidence directory must not already exist") from error
    except OSError as error:
        raise GateError(f"cannot create evidence directory: {error}") from error
    return os.path.realpath(path)


def output_path(evidence_dir, gate_id, suffix):
    path = os.path.realpath(os.path.join(evidence_dir, f"{gate_id}.{suffix}"))
    if os.path.commonpath((evidence_dir, path)) != evidence_dir:
        raise GateError(f"output path for gate {gate_id} escapes evidence directory")
    return path


def write_log(path, output):
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as output_file:
            output_file.write(output)
            output_file.flush()
            os.fsync(output_file.fileno())
        os.chmod(path, 0o600)
    except OSError as error:
        raise GateError(f"cannot write gate log: {error}") from error


def normalize_exit_code(return_code):
    if return_code < 0:
        return min(255, 128 + abs(return_code))
    return min(255, return_code)


def cleanup_diagnostics(cleanup_errors):
    if not cleanup_errors:
        return b""
    return (
        "process cleanup failed: " + "; ".join(cleanup_errors) + "\n"
    ).encode("utf-8", errors="replace")


def probe_tool_versions(gate, signals=None):
    versions = {"python": platform.python_version()}
    executable = gate["command"][0]
    tool = os.path.basename(executable)
    if tool not in VERSIONED_TOOLS:
        return (
            versions,
            f"tool version identity is unsupported for: {tool}",
            None,
            None,
        )
    if signals is None:
        signals = process_supervisor.SignalState()
    try:
        result = process_supervisor.run(
            [executable, "--version"],
            cwd=REPO_ROOT,
            timeout_seconds=TOOL_VERSION_TIMEOUT_SECONDS,
            cooperative_grace_seconds=1,
            signals=signals,
        )
    except FileNotFoundError:
        return (
            versions,
            f"tool version identity is unavailable for: {tool}",
            None,
            None,
        )
    except (OSError, process_supervisor.SupervisionError) as error:
        return (
            versions,
            f"tool version probe failed for {tool}: {error}",
            None,
            None,
        )

    probe_cleanup = cleanup_diagnostics(result.cleanup_errors)
    if result.cause == "timeout":
        return (
            versions,
            f"tool version probe timed out for: {tool}",
            None,
            probe_cleanup,
        )
    if result.cause == "descendants":
        return (
            versions,
            f"tool version probe left descendants running for: {tool}",
            None,
            probe_cleanup,
        )
    if result.cause == "capture_error":
        detail = "; ".join(result.cleanup_errors) or "output capture failed"
        return (
            versions,
            f"tool version probe failed for {tool}: {detail}",
            None,
            probe_cleanup,
        )
    if result.cause == "interrupted":
        return (
            versions,
            None,
            result.interrupted_signal,
            result.output + probe_cleanup,
        )
    if result.cleanup_errors and result.return_code == 0:
        detail = "; ".join(result.cleanup_errors)
        return (
            versions,
            f"tool version probe failed for {tool}: {detail}",
            None,
            probe_cleanup,
        )

    decoded_output = result.output.decode("utf-8", errors="replace")
    lines = [line.strip() for line in decoded_output.splitlines() if line.strip()]
    if result.return_code != 0 or not lines:
        return (
            versions,
            f"tool version identity could not be obtained for: {tool}",
            None,
            probe_cleanup,
        )
    versions[tool] = lines[0]
    return versions, None, None, None


def run_command(gate, signals=None):
    if signals is None:
        signals = process_supervisor.SignalState()
    try:
        result = process_supervisor.run(
            gate["command"],
            cwd=REPO_ROOT,
            timeout_seconds=gate["timeoutSeconds"],
            cooperative_grace_seconds=gate.get("terminationGraceSeconds", 1),
            signals=signals,
        )
    except FileNotFoundError:
        executable = gate["command"][0]
        output = f"not_run: executable is unavailable: {executable}\n".encode("utf-8")
        return (
            "not_run",
            f"executable is unavailable: {executable}",
            EXIT_NOT_RUN,
            output,
            None,
        )
    except OSError as error:
        output = f"gate execution failed: {error}\n".encode("utf-8", errors="replace")
        return "failed", "command could not be executed", 126, output, None
    except process_supervisor.SupervisionError as error:
        output = f"gate execution failed: {error}\n".encode("utf-8", errors="replace")
        return "failed", "command could not be captured", 126, output, None

    output = result.output
    if result.cause == "timeout":
        output += f"gate timed out after {gate['timeoutSeconds']} seconds\n".encode("utf-8")
        status = "failed"
        reason = "command timed out"
        exit_code = 124
    elif result.cause == "descendants":
        output += b"gate left an ordinary descendant running after command exit\n"
        status = "failed"
        reason = "command left descendants running"
        exit_code = 125
    elif result.cause == "capture_error":
        output += b"gate execution failed: command output capture failed\n"
        status = "failed"
        reason = "command could not be captured"
        exit_code = 126
    elif result.cause == "interrupted":
        interruption_signal = result.interrupted_signal
        exit_code = min(255, 128 + (interruption_signal or 0))
        status = "failed"
        reason = f"command interrupted by signal {interruption_signal}"
    elif result.cause != "exited" or result.return_code is None:
        output += f"gate execution failed: unknown supervisor result {result.cause}\n".encode(
            "utf-8", errors="replace"
        )
        status = "failed"
        reason = "command could not be captured"
        exit_code = 126
    else:
        exit_code = normalize_exit_code(result.return_code)
        if result.return_code == 0:
            status = "passed"
            reason = "command exited 0"
        elif result.return_code == EXIT_NOT_RUN:
            status = "not_run"
            reason = (
                "command reported not_run; inspect the gate log for corrective action"
            )
        else:
            status = "failed"
            reason = f"command exited {exit_code}"

    output += cleanup_diagnostics(result.cleanup_errors)
    return status, reason, exit_code, output, result.interrupted_signal


def subject_digest(gate, candidate_digest, source_commit, status):
    if gate["subjectKind"] == "source":
        return evidence.digest_bytes(source_commit.encode("ascii"))
    if gate["subjectKind"] == "artifact":
        if status == "passed":
            raise GateError("artifact gate cannot pass without a declared subject identity")
        # Schema version 1 has no artifact identity interface. This digest binds
        # an unavailable-subject declaration; it is not a digest of an artifact.
        unavailable_subject = json.dumps(
            {
                "availability": "unavailable",
                "candidateLockDigest": candidate_digest,
                "gateId": gate["id"],
                "kind": "artifact",
            },
            sort_keys=True,
            separators=(",", ":"),
        ).encode("utf-8")
        return evidence.digest_bytes(unavailable_subject)
    declared_subject = json.dumps(
        {
            "candidateLockDigest": candidate_digest,
            "kind": gate["subjectKind"],
            "command": gate["command"],
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    return evidence.digest_bytes(declared_subject)


def write_envelope(
    gate, envelope_path, status, reason, source_commit, candidate_digest,
    started_at, finished_at, exit_code, output, tool_versions, subject=None,
):
    # ``subject`` is a verified (kind, digest) observed by the runner; without it
    # the catalog v1 declaration-based digest is used.
    subject_kind, subject_value = subject or (
        gate["subjectKind"],
        subject_digest(gate, candidate_digest, source_commit, status),
    )
    envelope_args = argparse.Namespace(
        output=envelope_path,
        gate_id=gate["id"],
        status=status,
        reason=reason,
        source_commit=source_commit,
        candidate_lock_digest=candidate_digest,
        command_json=json.dumps(gate["command"], separators=(",", ":")),
        started_at=started_at,
        finished_at=finished_at,
        exit_code=exit_code,
        output_digest=evidence.digest_bytes(output),
        tool_versions_json=json.dumps(tool_versions, separators=(",", ":")),
        subject_json=json.dumps(
            {"kind": subject_kind, "digest": subject_value},
            separators=(",", ":"),
        ),
    )
    try:
        evidence.write_envelope(envelope_args)
    except evidence.EvidenceError as error:
        raise GateError(f"cannot write evidence for {gate['id']}: {error}") from error


class RunContext:
    """Catalog v2 execution state: the candidate, injected inputs, prior evidence."""

    def __init__(self, candidate, inputs, prior_usable, prior_rejected):
        self.candidate = candidate
        self.inputs = inputs
        self.prior_usable = prior_usable
        self.prior_rejected = prior_rejected


def run_producer_gate(gate, context, evidence_dir, signals):
    """Run a v2 producer and verify what it returned against the frozen candidate.

    Returns (status, reason, exit_code, output, interrupted_signal, subject, versions).
    Only the runner decides the final status; a producer's claim is evidence to
    check, not a verdict.
    """
    producers = load_producers()
    candidate = context.candidate
    if not candidate.full:
        reason = f"a verified frozen candidate is required: {candidate.problem}"
        return "not_run", reason, EXIT_NOT_RUN, f"not_run: {reason}\n".encode(), None, None, {}
    missing = producers.missing_inputs(gate["id"], context.inputs)
    if missing:
        reason = "missing required input: " + ", ".join(missing)
        return "not_run", reason, EXIT_NOT_RUN, f"not_run: {reason}\n".encode(), None, None, {}
    directory = producers.producer_directory(evidence_dir, gate["id"])
    try:
        os.mkdir(directory, 0o700)
    except OSError as error:
        raise GateError(f"cannot create producer output directory: {error}") from error
    result_path = os.path.join(directory, producers.RESULT_NAME)
    producer_gate = dict(gate)
    producer_gate["command"] = producers.producer_argv(gate, candidate, result_path, context.inputs)
    previous = os.environ.get("TELOS_GATE_ID")
    os.environ["TELOS_GATE_ID"] = gate["id"]
    try:
        status, reason, exit_code, output, interrupted = run_command(producer_gate, signals)
    finally:
        if previous is None:
            os.environ.pop("TELOS_GATE_ID", None)
        else:
            os.environ["TELOS_GATE_ID"] = previous
    if interrupted is not None or exit_code not in (0, EXIT_NOT_RUN):
        return status, reason, exit_code, output, interrupted, None, {}
    try:
        result_status, result_reason, subject, versions = producers.evaluate_result(
            gate, candidate, directory, exit_code
        )
    except producers.ProducerError as error:
        reason = f"producer contract violated: {error}"
        return "failed", reason, EXIT_FAILED, output + f"failed: {reason}\n".encode("utf-8"), None, None, {}
    if result_reason:
        reason = result_reason
    return result_status, reason, exit_code, output, None, subject, versions


def signal_name(signum):
    return signal.Signals(signum).name


def apply_interruption(output, signum, stage):
    reason = f"runner interrupted by {signal_name(signum)} during {stage}"
    return (
        "failed",
        reason,
        min(255, 128 + signum),
        output + f"{reason}\n".encode("utf-8"),
    )


def write_remaining_cancellation_evidence(
    gates, source_commit, candidate_digest, evidence_dir
):
    reason = "runner interrupted before execution"
    output = b"not_run: runner interrupted before execution\n"
    for gate in gates:
        started_at = timestamp()
        finished_at = timestamp()
        write_log(output_path(evidence_dir, gate["id"], "log"), output)
        write_envelope(
            gate,
            output_path(evidence_dir, gate["id"], "json"),
            "not_run",
            reason,
            source_commit,
            candidate_digest,
            started_at,
            finished_at,
            EXIT_NOT_RUN,
            output,
            {"python": platform.python_version()},
        )
        print(f"{gate['id']}: not_run")


def execute_gates(
    gates, source_commit, candidate_digest, evidence_dir, signals, context=None
):
    statuses = {}
    prior_reasons = {}
    stop_reason = None
    if context is not None:
        statuses.update({gate_id: "passed" for gate_id in context.prior_usable})
        statuses.update({gate_id: "unusable" for gate_id in context.prior_rejected})
        prior_reasons = dict(context.prior_rejected)
    for gate_index, gate in enumerate(gates):
        if signals.signum is not None:
            write_remaining_cancellation_evidence(
                gates[gate_index:], source_commit, candidate_digest, evidence_dir
            )
            return False, signals.signum

        if stop_reason is not None:
            started_at = timestamp()
            output = f"not_run: {stop_reason}\n".encode("utf-8")
            write_log(output_path(evidence_dir, gate["id"], "log"), output)
            write_envelope(
                gate, output_path(evidence_dir, gate["id"], "json"), "not_run", stop_reason,
                source_commit, candidate_digest, started_at, timestamp(), EXIT_NOT_RUN, output,
                {"python": platform.python_version()},
            )
            statuses[gate["id"]] = "not_run"
            print(f"{gate['id']}: not_run")
            continue

        started_at = timestamp()
        tool_versions = {"python": platform.python_version()}
        interrupted_signal = None
        interruption_stage = "gate execution"
        subject_override = None
        blocked = [
            prerequisite
            for prerequisite in gate["prerequisites"]
            if statuses.get(prerequisite) != "passed"
        ]
        if blocked:
            details = ", ".join(
                f"{prerequisite}={statuses.get(prerequisite, 'unavailable')}"
                + (f" ({prior_reasons[prerequisite]})" if prerequisite in prior_reasons else "")
                for prerequisite in blocked
            )
            status = "not_run"
            reason = f"prerequisite did not pass: {details}"
            exit_code = EXIT_NOT_RUN
            output = f"not_run: {reason}\n".encode("utf-8")
        else:
            source_problem = None
            if gate["subjectKind"] == "source":
                source_problem = source_checkout_problem(source_commit)
            if source_problem:
                status = "failed"
                if source_problem.startswith("tree is dirty"):
                    reason = "source checkout was dirty before gate execution"
                else:
                    reason = f"source checkout {source_problem} before gate execution"
                exit_code = EXIT_FAILED
                output = f"failed: {reason}\n".encode("utf-8")
            else:
                (
                    tool_versions,
                    tool_problem,
                    interrupted_signal,
                    probe_output,
                ) = probe_tool_versions(gate, signals)
                interruption_stage = "tool version probe"
                if interrupted_signal is None:
                    interrupted_signal = signals.signum
                if interrupted_signal is not None:
                    status, reason, exit_code, output = apply_interruption(
                        probe_output or b"",
                        interrupted_signal,
                        interruption_stage,
                    )
                elif tool_problem:
                    status = "not_run"
                    reason = tool_problem
                    exit_code = EXIT_NOT_RUN
                    output = (
                        f"not_run: {reason}\n".encode("utf-8")
                        + (probe_output or b"")
                    )
                elif signals.signum is not None:
                    interrupted_signal = signals.signum
                    status, reason, exit_code, output = apply_interruption(
                        b"", interrupted_signal, interruption_stage
                    )
                else:
                    interruption_stage = "gate execution"
                    if context is not None and gate["subjectKind"] in {
                        "artifact", "runtime", "external-host",
                    }:
                        (
                            status,
                            reason,
                            exit_code,
                            output,
                            interrupted_signal,
                            subject_override,
                            observed_versions,
                        ) = run_producer_gate(gate, context, evidence_dir, signals)
                        # The runner's own observation of a tool wins over a claim.
                        tool_versions = {**observed_versions, **tool_versions}
                    else:
                        (
                            status,
                            reason,
                            exit_code,
                            output,
                            interrupted_signal,
                        ) = run_command(gate, signals)
                    if interrupted_signal is None:
                        interrupted_signal = signals.signum
                    if interrupted_signal is not None:
                        status, reason, exit_code, output = apply_interruption(
                            output, interrupted_signal, interruption_stage
                        )
                    elif gate["subjectKind"] == "source":
                        source_problem = source_checkout_problem(source_commit)
                    if interrupted_signal is None and signals.signum is not None:
                        interrupted_signal = signals.signum
                        status, reason, exit_code, output = apply_interruption(
                            output, interrupted_signal, interruption_stage
                        )
                    elif source_problem:
                        status = "failed"
                        if source_problem.startswith("tree is dirty"):
                            reason = "source gate dirtied the checkout during execution"
                        else:
                            reason = (
                                "source gate violated candidate identity during execution: "
                                f"{source_problem}"
                            )
                        exit_code = EXIT_FAILED
                        output += f"failed: {reason}\n".encode("utf-8")
        if (
            interrupted_signal is None
            and context is None
            and gate["subjectKind"] == "artifact"
        ):
            if status == "passed":
                status = "failed"
                reason = "artifact subject is unavailable despite command exit 0"
                exit_code = EXIT_FAILED
                output += (
                    b"failed: artifact subject is unavailable; "
                    b"catalog schema version 1 declares no artifact identity\n"
                )
            else:
                reason = f"artifact subject is unavailable; {reason}"
        if interrupted_signal is None and signals.signum is not None:
            interrupted_signal = signals.signum
            status, reason, exit_code, output = apply_interruption(
                output, interrupted_signal, interruption_stage
            )
        finished_at = timestamp()
        log_path = output_path(evidence_dir, gate["id"], "log")
        envelope_path = output_path(evidence_dir, gate["id"], "json")
        write_log(log_path, output)
        subject = subject_override
        if context is not None and subject is None and gate["subjectKind"] == "fixture":
            # Under catalog v2 a fixture's identity is its completed sanitized output.
            subject = ("fixture", evidence.digest_bytes(output))
        write_envelope(
            gate, envelope_path, status, reason, source_commit, candidate_digest,
            started_at, finished_at, exit_code, output, tool_versions, subject,
        )
        statuses[gate["id"]] = status
        print(f"{gate['id']}: {status}")
        if (
            context is not None
            and status == "failed"
            and gate["required"]
            and stop_reason is None
        ):
            stop_reason = f"blocked by failed required gate {gate['id']}"
        if interrupted_signal is not None:
            write_remaining_cancellation_evidence(
                gates[gate_index + 1:],
                source_commit,
                candidate_digest,
                evidence_dir,
            )
            return False, interrupted_signal
    return (
        all(
            not gate["required"] or statuses[gate["id"]] == "passed"
            for gate in gates
        ),
        signals.signum,
    )


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", required=True)
    parser.add_argument("--scope", required=True, choices=sorted(SCOPES))
    parser.add_argument("--candidate-lock", required=True)
    parser.add_argument("--evidence-dir", required=True)
    parser.add_argument("--phase", type=int)
    parser.add_argument("--gate", action="append", default=[])
    parser.add_argument(
        "--inputs",
        help="absolute path to a closed JSON manifest of protected per-gate input paths (catalog v2)",
    )
    parser.add_argument(
        "--prior-evidence-dir", action="append", default=[],
        help="earlier runner output whose verified, fresh, passed gates satisfy prerequisites (catalog v2)",
    )
    return parser


def build_context(catalog, gates, args, source_commit, candidate_digest, candidate_lock):
    """Create the catalog v2 execution context, or None for a v1 catalog."""
    if catalog.get("schemaVersion") != 2:
        if args.inputs or args.prior_evidence_dir:
            raise GateError("--inputs and --prior-evidence-dir require a catalog schemaVersion 2")
        return None
    producers = load_producers()
    try:
        candidate = producers.Candidate(candidate_lock, source_commit, candidate_digest)
        selected = {gate["id"] for gate in gates}
        inputs = {}
        if args.inputs:
            inputs = producers.load_inputs(args.inputs, catalog["gates"])
            outside = sorted(set(inputs) - selected)
            if outside:
                raise GateError(f"inputs name a gate that is not selected: {outside[0]}")
        usable, rejected = {}, {}
        if args.prior_evidence_dir:
            now = datetime.datetime.now(datetime.timezone.utc)
            usable, rejected = producers.load_prior_evidence(
                args.prior_evidence_dir,
                {gate["id"]: gate for gate in catalog["gates"]},
                source_commit, candidate_digest, now,
            )
            clash = sorted(selected & (set(usable) | set(rejected)))
            if clash:
                raise GateError(f"gate {clash[0]} is both selected and present in prior evidence")
    except producers.ProducerError as error:
        raise GateError(str(error)) from error
    return RunContext(candidate, inputs, usable, rejected)


def main(argv=None):
    args = build_parser().parse_args(argv)
    if args.phase is not None and args.phase <= 0:
        print("run-gates: --phase must be a positive integer", file=sys.stderr)
        return EXIT_USAGE
    catalog_path = os.path.abspath(args.catalog)
    candidate_lock = os.path.abspath(args.candidate_lock)
    try:
        catalog = load_catalog(catalog_path)
        gates = select_gates(catalog, args.scope, args.gate, args.phase)
        source_commit, candidate_digest = read_candidate_lock(candidate_lock)
        validate_subject_bindings(gates, source_commit)
        context = build_context(
            catalog, gates, args, source_commit, candidate_digest, candidate_lock
        )
        try:
            process_supervisor.require_support()
            process_supervisor.enable_child_subreaper()
        except process_supervisor.SupervisionError as error:
            raise GateError(str(error)) from error
        evidence_dir = create_evidence_directory(args.evidence_dir)
        signals = process_supervisor.SignalState()
        previous_sigint = signal.getsignal(signal.SIGINT)
        previous_sigterm = signal.getsignal(signal.SIGTERM)
        signal.signal(signal.SIGINT, signals.receive)
        signal.signal(signal.SIGTERM, signals.receive)
        interrupted_signal = None
        try:
            context_arguments = {} if context is None else {"context": context}
            passed, interrupted_signal = execute_gates(
                gates, source_commit, candidate_digest, evidence_dir, signals,
                **context_arguments,
            )
            if interrupted_signal is None:
                interrupted_signal = signals.signum
        finally:
            signal.signal(signal.SIGINT, previous_sigint)
            signal.signal(signal.SIGTERM, previous_sigterm)
            if interrupted_signal is None:
                interrupted_signal = signals.signum
    except GateError as error:
        print(f"run-gates: {error}", file=sys.stderr)
        return EXIT_USAGE
    if interrupted_signal is not None:
        return min(255, 128 + interrupted_signal)
    return 0 if passed else EXIT_FAILED


if __name__ == "__main__":
    raise SystemExit(main())
