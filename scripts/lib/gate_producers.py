#!/usr/bin/env python3
"""Catalog v2 support for the gate runner: producer inputs, results, subjects.

The runner is the only envelope writer. A producer (any gate whose subject is an
artifact, a runtime or an external host) receives the verified candidate lock and
a fresh result path from the runner and writes one closed ``gate-result`` record
plus its declared attachments into a runner-created output directory. This module
validates what a producer returned and derives the *observed* subject digest the
envelope binds; it never trusts a producer's claim about its own identity.
"""

import os
import re
import stat
from pathlib import Path

import evidence
import release_graph as rg

CATALOG_V2 = 2
RESERVED_AGGREGATE = "beta-certification"
PRODUCER_KINDS = {"artifact", "runtime", "external-host"}
SELECTOR_BY_KIND = {
    "source": re.compile(r"sourceCommit\Z"),
    "fixture": re.compile(r"completed-output\Z"),
    "artifact": re.compile(r"(?:artifact-index|artifact:[A-Za-z0-9._@+=/-]+)\Z"),
    "runtime": re.compile(r"observed-runtime\Z"),
    "external-host": re.compile(r"observed-host\Z"),
}
PLATFORMS = {
    "linux-x86_64", "chrome", "firefox", "edge", "webkit",
    "desktop-safari", "ios-safari", "android-chrome",
}
DEFERRED_GATE_PREFIXES = (
    "desktop-windows", "desktop-macos", "operator-cli-windows", "operator-cli-darwin",
)
RESERVED_OPTIONS = {"--candidate-lock", "--result-out", "--rehearsal", "--rehearsal-lock", "--source-commit"}

# gate id -> {input key -> fixed producer option}. Nothing else may be injected.
GATE_INPUT_OPTIONS = {
    "clean-install": {"targetFile": "--target-file", "fixtureManifest": "--fixture-manifest"},
    "browser-matrix": {"recordsDir": "--records-dir"},
    "desktop-linux": {"recordsDir": "--records-dir"},
    "runtime-hardening": {"targetFile": "--target-file"},
    "monitoring": {"targetFile": "--target-file"},
    "encrypted-backup": {"targetFile": "--target-file"},
    "separate-host-restore": {"targetFile": "--target-file"},
    "upgrade-rollback": {"targetFile": "--target-file"},
    "capacity": {"targetFile": "--target-file"},
    "controlled-egress": {"targetFile": "--target-file"},
    "host-exposure": {"targetFile": "--target-file", "policyFile": "--policy"},
    "candidate-supply-chain": {"artifactRoot": "--artifact-root", "sbomDir": "--sbom-dir"},
}
DIRECTORY_INPUTS = {"recordsDir", "artifactRoot", "sbomDir"}
RESULT_NAME = "result.json"


class ProducerError(Exception):
    """A producer result or its inputs violate the contract."""


def fail(message):
    raise ProducerError(message)


# --------------------------------------------------------------- catalog v2 fields

def validate_v2_gate(gate, prefix):
    """Check the three catalog-v2 fields of one gate; callers own the v1 fields."""
    selector = gate["subjectSelector"]
    kind = gate["subjectKind"]
    if not isinstance(selector, str) or not SELECTOR_BY_KIND[kind].match(selector):
        fail(f"{prefix} subjectSelector {selector!r} is not valid for {kind}")
    if selector.startswith("artifact:"):
        try:
            rg.check_relative_path(selector[len("artifact:"):], "subjectSelector")
        except rg.GraphError as error:
            fail(f"{prefix} {error}")
    age = gate["maxAgeHours"]
    if not isinstance(age, int) or isinstance(age, bool) or age <= 0:
        fail(f"{prefix} maxAgeHours must be a positive integer")
    platforms = gate["requiredPlatforms"]
    if not isinstance(platforms, list) or any(not isinstance(item, str) for item in platforms):
        fail(f"{prefix} requiredPlatforms must be an array of strings")
    if len(platforms) != len(set(platforms)):
        fail(f"{prefix} requiredPlatforms must be unique")
    unknown = sorted(set(platforms) - PLATFORMS)
    if unknown:
        fail(f"{prefix} requiredPlatforms has unsupported platform {unknown[0]!r}")
    gate_id = gate["id"]
    if gate_id.startswith(DEFERRED_GATE_PREFIXES):
        fail(f"{prefix} is a deferred native or non-Ubuntu gate and may not be in the catalog")
    command = gate["command"]
    reserved = sorted(RESERVED_OPTIONS.intersection(command))
    if reserved:
        fail(f"{prefix} command may not contain runner-owned option {reserved[0]}")


# --------------------------------------------------------------- inputs manifest

def load_inputs(path, gates):
    """Parse the closed external inputs manifest into {gate id: {key: path}}."""
    manifest_path = Path(path)
    if not manifest_path.is_absolute():
        fail("--inputs must be an absolute path")
    try:
        mode = os.lstat(manifest_path).st_mode
    except OSError as error:
        fail(f"cannot read inputs manifest: {error}")
    if not stat.S_ISREG(mode):
        fail("inputs manifest must be a regular file")
    try:
        document = rg.parse_json(manifest_path.read_bytes())
    except (rg.GraphError, OSError) as error:
        fail(f"inputs manifest is invalid: {error}")
    if not isinstance(document, dict) or set(document) != {"gateInputs"}:
        fail("inputs manifest must be an object with exactly one key, gateInputs")
    try:
        rg.validate_record("gate-inputs", document)
    except rg.GraphError as error:
        fail(f"inputs manifest is invalid: {error}")
    gate_inputs = document["gateInputs"]
    if not isinstance(gate_inputs, dict):
        fail("gateInputs must be an object")
    known = {gate["id"]: gate for gate in gates}
    resolved = {}
    for gate_id, values in gate_inputs.items():
        if gate_id not in known:
            fail(f"inputs name an unknown gate: {gate_id}")
        allowed = GATE_INPUT_OPTIONS.get(gate_id)
        if allowed is None:
            fail(f"gate {gate_id} accepts no injected input")
        if not isinstance(values, dict):
            fail(f"inputs for {gate_id} must be an object")
        unknown_keys = sorted(set(values) - set(allowed))
        if unknown_keys:
            fail(f"inputs for {gate_id} have unknown key: {unknown_keys[0]}")
        resolved[gate_id] = {}
        for key, value in values.items():
            resolved[gate_id][key] = _checked_input_path(gate_id, key, value)
    return resolved


def _checked_input_path(gate_id, key, value):
    label = f"input {gate_id}.{key}"
    if not isinstance(value, str) or not value or "\0" in value:
        fail(f"{label} must be a nonempty path string")
    if not os.path.isabs(value) or os.path.normpath(value) != value:
        fail(f"{label} must be an absolute normalized path")
    current = Path("/")
    for part in Path(value).parts[1:]:
        current = current / part
        try:
            mode = os.lstat(current).st_mode
        except OSError as error:
            fail(f"{label} is not readable: {error}")
        if stat.S_ISLNK(mode):
            fail(f"{label} traverses a symbolic link")
    mode = os.lstat(value).st_mode
    wants_directory = key in DIRECTORY_INPUTS
    if wants_directory and not stat.S_ISDIR(mode):
        fail(f"{label} must be a directory")
    if not wants_directory and not stat.S_ISREG(mode):
        fail(f"{label} must be a regular file")
    return value


def missing_inputs(gate_id, supplied):
    required = GATE_INPUT_OPTIONS.get(gate_id, {})
    return sorted(set(required) - set(supplied.get(gate_id, {})))


# --------------------------------------------------------------- candidate context

class Candidate:
    """The frozen candidate a producer gate may examine.

    ``full`` is true only when the lock sits in a root whose complete graph
    verifies. A bare ``{schemaVersion, sourceCommit}`` identity file is enough
    for source/fixture gates and Phase 1 checkpoints, never for producers.
    """

    def __init__(self, lock_path, source_commit, lock_digest):
        self.lock_path = os.path.abspath(lock_path)
        self.root = os.path.dirname(self.lock_path)
        self.source_commit = source_commit
        self.lock_digest = lock_digest
        self.full = False
        self.problem = None
        self.index = None
        self.generation = None
        if os.path.basename(self.lock_path) != "candidate-lock.json":
            self.problem = "the lock is not named candidate-lock.json in a candidate root"
            return
        try:
            rg.verify_root(self.root)
            self.index = rg.load_record(
                "artifact-index", rg.read_regular(self.root, "artifact-index.json")
            )
        except (rg.GraphError, OSError) as error:
            self.problem = f"candidate graph does not verify: {error}"
            return
        self.full = True

    def artifact(self, path):
        for entry in self.index["artifacts"]:
            if entry["path"] == path:
                return entry
        fail(f"{path} is not in the frozen artifact index")

    def locked_images(self):
        """service -> manifest digest from the frozen generation descriptor."""
        if self.generation is None:
            entries = [a for a in self.index["artifacts"] if a["kind"] == "generation"]
            if len(entries) != 1:
                fail("the frozen candidate must contain exactly one generation descriptor")
            document = rg.load_record(
                "generation-manifest", rg.read_regular(self.root, entries[0]["path"]), canonical=False
            )
            self.generation = document["images"]
        return self.generation


# --------------------------------------------------------------- producer command

def producer_argv(gate, candidate, result_path, supplied):
    argv = list(gate["command"]) + [
        "--candidate-lock", candidate.lock_path, "--result-out", result_path,
    ]
    options = GATE_INPUT_OPTIONS.get(gate["id"], {})
    for key in sorted(supplied.get(gate["id"], {})):
        argv += [options[key], supplied[gate["id"]][key]]
    return argv


def producer_directory(evidence_dir, gate_id):
    return os.path.join(evidence_dir, f"{gate_id}.d")


# --------------------------------------------------------------- result evaluation

def evaluate_result(gate, candidate, directory, exit_code):
    """Validate a producer's output and return (status, reason, subject, versions).

    ``subject`` is ``(kind, digest)`` for a passed result and None otherwise.
    Raises ProducerError for anything that must become a runner-owned failure.
    """
    result_path = os.path.join(directory, RESULT_NAME)
    if not os.path.lexists(result_path):
        if exit_code == evidence_not_run():
            return "not_run", None, None, {}
        fail("producer exited without writing its result record")
    try:
        result = rg.verify_result_directory(directory, RESULT_NAME)
    except (rg.GraphError, OSError) as error:
        fail(f"producer result is invalid: {error}")
    if result["gateId"] != gate["id"]:
        fail(f"producer result is for gate {result['gateId']!r}, not {gate['id']!r}")
    if result["sourceCommit"] != candidate.source_commit:
        fail("producer result sourceCommit differs from the candidate lock")
    if result["recordKind"] != "candidate":
        fail("a rehearsal result can never enter candidate evidence")
    if result["candidateLockDigest"] != candidate.lock_digest:
        fail("producer result candidateLockDigest differs from the candidate lock")
    expected = {0: "passed", evidence_not_run(): "not_run"}.get(exit_code, "failed")
    if result["status"] != expected:
        fail(f"producer result status {result['status']!r} contradicts exit code {exit_code}")
    versions = dict(result["toolVersions"])
    if result["status"] != "passed":
        return result["status"], result["reason"], None, versions
    subject = observed_subject(gate, candidate, directory, result)
    return "passed", result["reason"], subject, versions


def evidence_not_run():
    return 3


def observed_subject(gate, candidate, directory, result):
    """Verify the producer's descriptor against the frozen candidate; return (kind, digest)."""
    if result["subjectFile"] is None:
        fail("a passed producer result must name a subject descriptor")
    raw = rg.read_regular(directory, result["subjectFile"])
    try:
        descriptor = rg.parse_canonical(raw)
        rg.validate_record("observed-subject", descriptor)
        rg.validate_subject_for_result(result, descriptor)
    except rg.GraphError as error:
        fail(f"subject descriptor is invalid: {error}")
    kind = gate["subjectKind"]
    if descriptor["kind"] != kind:
        fail(f"subject descriptor kind {descriptor['kind']!r} is not {kind!r}")
    selector = gate["subjectSelector"]
    if kind == "artifact":
        return _artifact_subject(candidate, descriptor, selector)
    if kind == "runtime":
        _check_runtime(candidate, descriptor)
    elif kind == "external-host":
        _check_host(gate, descriptor, result)
    return kind, rg.digest_bytes(raw)


def _artifact_subject(candidate, descriptor, selector):
    if descriptor["selector"] != selector:
        fail("subject descriptor selector differs from the catalog selector")
    if selector == "artifact-index":
        actual = rg.digest_bytes(rg.read_regular(candidate.root, "artifact-index.json"))
    else:
        path = selector[len("artifact:"):]
        entry = candidate.artifact(path)
        actual = "sha256:" + rg.file_identity(candidate.root, path)[1]
        if actual != "sha256:" + entry["sha256"]:
            fail(f"{path} differs from the frozen artifact index")
    if descriptor["artifactDigest"] != actual:
        fail("subject descriptor does not match the exact frozen artifact bytes")
    return "artifact", actual


def _check_runtime(candidate, descriptor):
    locked = candidate.locked_images()
    seen = {}
    for service in descriptor["services"]:
        name = service["name"]
        if name not in locked:
            fail(f"service {name} is running but not in the candidate image lock")
        # The manifest digest is the OCI identity locked in the candidate; the
        # image/config ID is recorded separately and legitimately differs from it.
        if service["manifestDigest"] != locked[name]:
            fail(f"service {name} runs a manifest that differs from the candidate image lock")
        seen[name] = True
    missing = sorted(set(locked) - set(seen))
    if missing:
        fail(f"locked service {missing[0]} was not observed running")


def _check_host(gate, descriptor, result):
    if descriptor["purpose"] != gate["id"]:
        fail("host descriptor purpose differs from the gate")
    declared = {a["path"]: a["sha256"] for a in result["attachments"]}
    for record in descriptor["records"]:
        if declared.get(record["path"]) != record["sha256"]:
            fail(f"host record {record['path']} is not a matching declared attachment")


# --------------------------------------------------------------- prior evidence

def load_prior_evidence(directories, gates_by_id, source_commit, lock_digest, now):
    """Verify prior runner output and return ``(usable, rejected)``.

    ``usable`` maps gate id -> finishedAt for every fresh, passed, candidate-bound
    record. ``rejected`` maps gate id -> reason for records that are valid but
    cannot satisfy a prerequisite (failed, not_run, stale). A record that is
    malformed, tampered with, bound to another candidate or duplicated is a hard
    ProducerError: reuse must never silently skip over damaged evidence.
    """
    import datetime

    usable, rejected, seen = {}, {}, set()
    for directory in directories:
        directory = os.path.abspath(directory)
        if not os.path.isdir(directory) or os.path.islink(directory):
            fail(f"prior evidence directory is not a real directory: {directory}")
        for name in sorted(os.listdir(directory)):
            if not name.endswith(".json"):
                continue
            gate_id = name[: -len(".json")]
            gate = gates_by_id.get(gate_id)
            if gate is None:
                fail(f"prior evidence names an unknown gate: {gate_id}")
            if gate_id in seen:
                fail(f"duplicate prior evidence for gate {gate_id}")
            seen.add(gate_id)
            try:
                envelope = evidence.load_envelope(os.path.join(directory, name))
                evidence.validate_envelope(envelope, lock_digest, source_commit)
            except evidence.EvidenceError as error:
                fail(f"prior evidence {gate_id} is invalid: {error}")
            if envelope["gateId"] != gate_id:
                fail(f"prior evidence file {name} is for gate {envelope['gateId']}")
            try:
                log_digest = evidence.digest_path(os.path.join(directory, f"{gate_id}.log"))
            except evidence.EvidenceError as error:
                fail(f"prior evidence {gate_id}: {error}")
            if log_digest != envelope["outputDigest"]:
                fail(f"prior evidence {gate_id} log differs from its recorded digest")
            if envelope["status"] != "passed":
                rejected[gate_id] = f"prior evidence status is {envelope['status']}"
                continue
            if gate["subjectKind"] in PRODUCER_KINDS:
                try:
                    result = rg.verify_result_directory(producer_directory(directory, gate_id), RESULT_NAME)
                except (rg.GraphError, OSError) as error:
                    fail(f"prior evidence {gate_id} result is invalid: {error}")
                if result["recordKind"] != "candidate" or result["status"] != "passed":
                    fail(f"prior evidence {gate_id} result is not a passed candidate record")
            finished = datetime.datetime.fromisoformat(envelope["finishedAt"].replace("Z", "+00:00"))
            age = (now - finished).total_seconds()
            if age < 0:
                fail(f"prior evidence {gate_id} finished in the future")
            if age > gate["maxAgeHours"] * 3600:
                rejected[gate_id] = f"prior evidence is older than {gate['maxAgeHours']} hours"
                continue
            usable[gate_id] = finished
    return usable, rejected
