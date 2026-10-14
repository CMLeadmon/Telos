#!/usr/bin/env python3
"""Canonical bytes, closed release records and the acyclic candidate graph.

One serializer and one validator for every release-graph document, so the
builder, the gate runner, the importer and the certifier cannot disagree about
bytes. Directed graph (no node may name itself or a later node):

    payload files (+ artifact-bound SBOMs)
      -> artifact-index.json
      -> release-manifest.json
      -> candidate-lock.json
      -> gate results / envelopes
      -> evidence-index.json
      -> signatures / certification

Canonical JSON: UTF-8, recursively sorted keys, compact separators, integers
only (no floats, NaN or Infinity), exactly one trailing LF, duplicate keys
rejected on parse. Digests are ``sha256:`` plus 64 lowercase hex digits unless a
schema names a raw ``sha256`` field.
"""

import argparse
import hashlib
import json
import os
import re
import stat
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import schema_check  # noqa: E402

RELEASE_DIR = Path(__file__).resolve().parents[2] / "release"
SCHEMA_FILES = {
    "artifact-index": "artifact-index.schema.json",
    "release-manifest": "release-manifest.schema.json",
    "candidate-lock": "candidate-lock.schema.json",
    "generation-manifest": "generation-manifest.schema.json",
    "installed-generation": "installed-generation.schema.json",
    "operation-journal": "operation-journal.schema.json",
    "artifact-identity": "artifact-identity.schema.json",
    "gate-result": "gate-result.schema.json",
    "observed-subject": "observed-subject.schema.json",
    "guided-smoke": "guided-smoke.schema.json",
    "evidence-index": "evidence-index.schema.json",
    "certification": "certification.schema.json",
    "release-review": "release-review.schema.json",
    "drill-target": "drill-target.schema.json",
    "gate-inputs": "gate-inputs.schema.json",
}
GRAPH_DOCUMENTS = {
    "artifact-index": "artifact-index.json",
    "release-manifest": "release-manifest.json",
    "candidate-lock": "candidate-lock.json",
}
CHECKSUM_FILE = "SHA256SUMS"
# Files and prefixes that are downstream of the candidate lock (or of the
# evidence) and so may never appear as payload: listing one would create a cycle.
RESERVED_BASENAMES = {
    "artifact-index.json", "release-manifest.json", "candidate-lock.json",
    CHECKSUM_FILE, "evidence-index.json", "certification.json",
    "release-review.json", "installed-generation.json",
}
RESERVED_PREFIXES = ("evidence/", "signatures/", "certification/")
RESERVED_SUFFIXES = (".sig", ".sigstore.json", ".minisig")

SAFE_PATH = re.compile(
    r"(?!/)(?!.*(?:^|/)\.{1,2}(?:/|$))[A-Za-z0-9._@+=-]+(?:/[A-Za-z0-9._@+=-]+)*\Z"
)
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
SECRET_TEXT = (
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
    re.compile(r"(?i)\b(?:password|passwd|passphrase|secret|api[_-]?key|token|authorization)\b\s*[:=]\s*\S{6,}"),
    re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}"),
    re.compile(r"[A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:[^@\s]+@"),
)


class GraphError(Exception):
    """A release record or graph violates the contract."""


def fail(message):
    raise GraphError(message)


# --------------------------------------------------------------- canonical bytes

def _reject_floats(value, path="$"):
    if isinstance(value, float):
        fail(f"{path}: floating point numbers have no canonical form")
    if isinstance(value, dict):
        for key, child in value.items():
            _reject_floats(child, f"{path}.{key}")
    elif isinstance(value, list):
        for index, child in enumerate(value):
            _reject_floats(child, f"{path}[{index}]")


def canonical_bytes(value):
    _reject_floats(value)
    try:
        text = json.dumps(
            value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False
        )
    except (TypeError, ValueError) as error:
        fail(f"value has no canonical JSON form: {error}")
    return text.encode("utf-8") + b"\n"


def _no_duplicates(pairs):
    seen = {}
    for key, value in pairs:
        if key in seen:
            fail(f"duplicate object key {key!r}")
        seen[key] = value
    return seen


def _reject_constant(name):
    fail(f"non-finite number {name} is not allowed")


def _reject_float_literal(text):
    fail(f"floating point number {text} is not allowed")


def parse_json(data):
    """Strictly parse JSON bytes: UTF-8 without BOM, no duplicates, no floats."""
    if not isinstance(data, (bytes, bytearray)):
        fail("JSON input must be bytes")
    if data.startswith(b"\xef\xbb\xbf"):
        fail("JSON input must not start with a byte order mark")
    try:
        text = bytes(data).decode("utf-8")
    except UnicodeDecodeError as error:
        fail(f"JSON input is not valid UTF-8: {error}")
    try:
        return json.loads(
            text,
            object_pairs_hook=_no_duplicates,
            parse_constant=_reject_constant,
            parse_float=_reject_float_literal,
        )
    except json.JSONDecodeError as error:
        fail(f"malformed JSON: {error.msg}")


def parse_canonical(data):
    value = parse_json(data)
    if canonical_bytes(value) != bytes(data):
        fail("document is valid JSON but not in canonical form")
    return value


def digest_bytes(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def raw_sha256(data):
    return hashlib.sha256(data).hexdigest()


# --------------------------------------------------------------- filesystem safety

def check_relative_path(path, field="path"):
    if not isinstance(path, str) or not SAFE_PATH.match(path) or len(path) > 512:
        fail(f"{field} {path!r} is not a safe relative path")
    return path


def is_reserved(path):
    base = path.rsplit("/", 1)[-1]
    return (
        base in RESERVED_BASENAMES
        or path.startswith(RESERVED_PREFIXES)
        or base.endswith(RESERVED_SUFFIXES)
    )


def resolve_regular(root, relative):
    """Return the real path of a regular file below ``root``; no symlink anywhere."""
    check_relative_path(relative)
    current = Path(root)
    for index, part in enumerate(relative.split("/")):
        current = current / part
        try:
            mode = os.lstat(current).st_mode
        except FileNotFoundError:
            fail(f"{relative} does not exist")
        if stat.S_ISLNK(mode):
            fail(f"{relative} traverses a symbolic link")
        last = index == len(relative.split("/")) - 1
        if last and not stat.S_ISREG(mode):
            fail(f"{relative} is not a regular file")
        if not last and not stat.S_ISDIR(mode):
            fail(f"{relative} has a non-directory parent")
    return current


def read_regular(root, relative):
    path = resolve_regular(root, relative)
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags)
    with os.fdopen(descriptor, "rb") as handle:
        return handle.read()


def file_identity(root, relative):
    data = read_regular(root, relative)
    return len(data), raw_sha256(data), data


def write_atomic_exclusive(path, data, mode=0o600):
    """Write ``data`` to a sibling temp file, fsync, and publish without overwrite."""
    path = Path(path)
    directory = path.parent
    if not directory.is_dir():
        fail(f"output directory {directory} does not exist")
    descriptor, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=directory)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temp_name, mode)
        try:
            os.link(temp_name, path)
        except FileExistsError:
            fail(f"refusing to overwrite existing {path}")
    finally:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
    directory_fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(directory_fd)
    finally:
        os.close(directory_fd)


# --------------------------------------------------------------- schemas

_SCHEMA_CACHE = {}


def load_schema(kind):
    if kind not in SCHEMA_FILES:
        fail(f"unknown record kind {kind!r}")
    if kind not in _SCHEMA_CACHE:
        schema = json.loads((RELEASE_DIR / SCHEMA_FILES[kind]).read_text(encoding="utf-8"))
        schema_check.check_schema(schema)
        _SCHEMA_CACHE[kind] = schema
    return _SCHEMA_CACHE[kind]


def reject_secret_text(text, where):
    for pattern in SECRET_TEXT:
        if pattern.search(text):
            fail(f"{where} contains secret-looking text")


def _sorted_unique(items, key, label):
    keys = [key(item) for item in items]
    if len(set(keys)) != len(keys):
        fail(f"{label} contains a duplicate")
    if keys != sorted(keys):
        fail(f"{label} must be sorted")


def _parse_time(value):
    import datetime
    return datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ")


def _semantic_artifact_index(doc):
    artifacts = doc["artifacts"]
    _sorted_unique(artifacts, lambda a: a["path"], "artifacts")
    for artifact in artifacts:
        path = artifact["path"]
        check_relative_path(path)
        if is_reserved(path):
            fail(f"{path} is downstream of the artifact index and cannot be payload")
        if artifact["kind"] == "oci-image":
            if artifact["platform"] != "linux/amd64":
                fail(f"{path}: OCI images must be linux/amd64")
            if artifact["contentDigest"] is None:
                fail(f"{path}: an OCI image requires its manifest contentDigest")
        elif artifact["contentDigest"] is not None:
            fail(f"{path}: only OCI images carry a contentDigest")
        if artifact["kind"] == "desktop" and artifact["platform"] != "linux-x86_64":
            fail(f"{path}: the initial desktop artifact is linux-x86_64 only")


def _semantic_release_manifest(doc):
    if doc["sourceTag"] != "v" + doc["version"]:
        fail("sourceTag must equal 'v' plus version")


def _semantic_candidate_lock(doc):
    for path in doc["artifacts"]:
        check_relative_path(path)
        if is_reserved(path):
            fail(f"{path} cannot appear in the candidate lock")


def _semantic_generation_manifest(doc):
    subset = doc["artifactIndexSubset"]
    _sorted_unique(subset, lambda item: item["path"], "artifactIndexSubset")
    for item in subset:
        check_relative_path(item["path"])
        if is_reserved(item["path"]) or item["path"] == "generation.json":
            fail(f"{item['path']} cannot be listed by the generation manifest")
    migrations = doc["migrations"]
    if not migrations["min"] <= migrations["current"] <= migrations["max"]:
        fail("migrations must satisfy min <= current <= max")
    if doc["rollbackCompatibleThrough"] > migrations["current"]:
        fail("rollbackCompatibleThrough cannot exceed the current migration")


def _semantic_installed_generation(doc):
    path = doc["generationPath"]
    if "//" in path or "/./" in path or "/../" in path or path.endswith("/"):
        fail("generationPath must be absolute and normalized")


UPGRADE_STATES = ("started", "snapshot-verified", "maintenance", "migrating", "migrated", "activated", "serving", "complete",
                  "aborted", "rolled-back", "restore-required", "failed")
ROLLBACK_STATES = ("started", "maintenance", "activated", "serving", "complete", "restore-required", "failed")
SCHEMA_CHANGED_STATES = ("migrated", "activated", "serving", "complete", "rolled-back")


def _semantic_operation_journal(doc):
    states = UPGRADE_STATES if doc["kind"] == "upgrade" else ROLLBACK_STATES
    if doc["state"] not in states:
        fail(f"state {doc['state']!r} does not exist for a {doc['kind']}")
    if doc["updatedAt"] < doc["startedAt"]:
        fail("updatedAt precedes startedAt")
    if doc["kind"] == "upgrade" and doc["fromGeneration"] == doc["toGeneration"]:
        fail("an upgrade must change generation")
    if doc["kind"] == "rollback" and doc["fromGeneration"] is None:
        fail("a rollback needs the generation it leaves")
    if doc["state"] in SCHEMA_CHANGED_STATES and doc["kind"] == "upgrade" and doc["postSchema"] is None:
        fail(f"an upgrade in state {doc['state']} must record the schema it reached")
    if doc["kind"] == "upgrade" and doc["state"] not in ("started", "snapshot-verified", "aborted", "failed") and doc["snapshotId"] is None:
        fail("an upgrade past its snapshot must name the snapshot")


def _semantic_artifact_identity(doc):
    return None


def _semantic_gate_result(doc):
    reject_secret_text(doc["reason"], "reason")
    attachments = doc["attachments"]
    _sorted_unique(attachments, lambda a: a["path"], "attachments")
    for attachment in attachments:
        check_relative_path(attachment["path"])
    if doc["recordKind"] == "candidate" and doc["candidateLockDigest"] is None:
        fail("a candidate record requires candidateLockDigest")
    subject_file = doc["subjectFile"]
    if subject_file is None:
        if doc["status"] == "passed":
            fail("a passed result requires a subjectFile")
    else:
        check_relative_path(subject_file, "subjectFile")
        listed = [a for a in attachments if a["path"] == subject_file]
        if len(listed) != 1 or listed[0]["kind"] != "subject":
            fail("subjectFile must be listed exactly once as a 'subject' attachment")
    if sum(1 for a in attachments if a["kind"] == "subject") > 1:
        fail("at most one subject attachment is allowed")


def _semantic_observed_subject(doc):
    kind = doc["kind"]
    if kind == "runtime":
        _sorted_unique(doc["services"], lambda s: s["name"], "services")
    elif kind == "external-host":
        _sorted_unique(doc["hosts"], lambda h: h["hostId"], "hosts")
        _sorted_unique(doc["records"], lambda r: r["path"], "records")
        for record in doc["records"]:
            check_relative_path(record["path"])
        for host in doc["hosts"]:
            machine = DIGEST.match(host["hostId"]) is not None
            if (host["identityMethod"] == "machine-id-sha256") != machine:
                fail("hostId form does not match identityMethod")
    elif kind == "artifact":
        return None


def _semantic_guided_smoke(doc):
    ids = [a["id"] for a in doc["assertions"]]
    if len(set(ids)) != len(ids):
        fail("assertions contains a duplicate id")
    for assertion in doc["assertions"]:
        reject_secret_text(assertion["notes"], f"notes for {assertion['id']}")
    if _parse_time(doc["finishedAt"]) < _parse_time(doc["startedAt"]):
        fail("finishedAt must not be earlier than startedAt")
    _sorted_unique(doc["attachments"], lambda a: a["path"], "attachments")
    for attachment in doc["attachments"]:
        check_relative_path(attachment["path"])
    machine = DIGEST.match(doc["hostId"]) is not None
    if doc["matrix"] == "desktop" and not machine:
        fail("a desktop record requires a machine-derived hostId")


def _semantic_evidence_index(doc):
    gates = doc["gates"]
    _sorted_unique(gates, lambda g: g["gateId"], "gates")
    owners = {}
    for gate in gates:
        references = [gate["envelope"]["path"], gate["log"]["path"]]
        if gate["result"] is not None:
            references.append(gate["result"]["path"])
        references += [item["path"] for item in gate["files"]]
        _sorted_unique(gate["files"], lambda item: item["path"], f"files of {gate['gateId']}")
        for path in references:
            check_relative_path(path)
            if path in owners:
                fail(f"{path} is claimed by both {owners[path]} and {gate['gateId']}")
            owners[path] = gate["gateId"]


def _semantic_certification(doc):
    if "beta-certification" in doc["requiredGates"]:
        fail("beta-certification cannot be one of its own required gates")
    for gate_id in doc["requiredGates"]:
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", gate_id):
            fail(f"invalid gate id {gate_id!r}")


def _semantic_release_review(doc):
    return None


def _semantic_drill_target(doc):
    origin = doc["origin"]
    if re.fullmatch(r"https://(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?::[0-9]+)?", origin):
        fail("origin must be a DNS name, not an IP literal")
    for key in ("nodePath", "sshConfigFile", "credentialsFile", "restoreTargetFile", "predecessorGeneration", "capacityTargetFile"):
        value = doc[key]
        if value is not None and (os.path.normpath(value) != value or value == "/"):
            fail(f"{key} must be an absolute normalized path")
    target = doc["sshTarget"]
    if target is not None and target.startswith("-"):
        fail("sshTarget may not start with a dash")
    if (target is None) != (doc["sshConfigFile"] is None):
        fail("sshTarget and sshConfigFile must be given together (a remote target needs a protected SSH config)")
    if "password" in json.dumps(doc).lower():
        fail("a drill target may not embed a password")


def _semantic_gate_inputs(doc):
    for gate_id, values in doc["gateInputs"].items():
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", gate_id):
            fail(f"invalid gate id {gate_id!r}")
        for key, value in values.items():
            if not re.fullmatch(r"[A-Za-z][A-Za-z0-9]*", key) or os.path.normpath(value) != value:
                fail(f"invalid input {gate_id}.{key}")


SEMANTICS = {
    "artifact-index": _semantic_artifact_index,
    "release-manifest": _semantic_release_manifest,
    "candidate-lock": _semantic_candidate_lock,
    "generation-manifest": _semantic_generation_manifest,
    "installed-generation": _semantic_installed_generation,
    "operation-journal": _semantic_operation_journal,
    "artifact-identity": _semantic_artifact_identity,
    "gate-result": _semantic_gate_result,
    "observed-subject": _semantic_observed_subject,
    "guided-smoke": _semantic_guided_smoke,
    "evidence-index": _semantic_evidence_index,
    "certification": _semantic_certification,
    "release-review": _semantic_release_review,
    "drill-target": _semantic_drill_target,
    "gate-inputs": _semantic_gate_inputs,
}


def validate_record(kind, document):
    """Schema validation plus the cross-field rules a schema cannot express."""
    try:
        schema_check.validate(document, load_schema(kind))
    except schema_check.ValidationError as error:
        fail(f"{kind}: {error}")
    SEMANTICS[kind](document)
    return document


def load_record(kind, data, canonical=True):
    document = parse_canonical(data) if canonical else parse_json(data)
    return validate_record(kind, document)


def validate_subject_for_result(result, subject):
    """A subject descriptor must carry the identity of the result that names it."""
    if subject["sourceCommit"] != result["sourceCommit"]:
        fail("subject sourceCommit differs from the result")
    if subject["candidateLockDigest"] != result["candidateLockDigest"]:
        fail("subject candidateLockDigest differs from the result")
    if result["recordKind"] == "candidate" and subject["candidateLockDigest"] is None:
        fail("a candidate subject requires a candidateLockDigest")


def verify_result_directory(directory, result_name):
    """Check a producer output directory against its closed result record."""
    directory = Path(directory)
    result = load_record("gate-result", read_regular(directory, result_name))
    declared = {}
    for attachment in result["attachments"]:
        size, digest, _ = file_identity(directory, attachment["path"])
        if digest != attachment["sha256"]:
            fail(f"attachment {attachment['path']} changed after it was recorded")
        declared[attachment["path"]] = attachment
    if result_name in declared:
        fail("the result record cannot list itself as an attachment")
    actual = set()
    for current, directories, files in os.walk(directory):
        for name in list(directories) + files:
            full = os.path.join(current, name)
            relative = os.path.relpath(full, directory).replace(os.sep, "/")
            if os.path.islink(full):
                fail(f"{relative} is a symbolic link")
            if os.path.isfile(full) and relative != result_name:
                actual.add(relative)
    unlisted = sorted(actual - set(declared))
    if unlisted:
        fail(f"unlisted file in producer output: {unlisted[0]}")
    return result


# --------------------------------------------------------------- graph builders

def _read_json_file(path):
    return parse_json(Path(path).read_bytes())


def build_index(root, descriptors):
    """Hash each listed payload file and return the artifact-index document."""
    artifacts = []
    for descriptor in descriptors["artifacts"]:
        unknown = set(descriptor) - {"path", "kind", "platform", "contentDigest"}
        if unknown:
            fail(f"artifact descriptor has unknown field {sorted(unknown)[0]!r}")
        size, digest, _ = file_identity(root, check_relative_path(descriptor["path"]))
        artifacts.append({
            "path": descriptor["path"], "kind": descriptor["kind"],
            "platform": descriptor["platform"], "size": size, "sha256": digest,
            "contentDigest": descriptor.get("contentDigest"),
        })
    artifacts.sort(key=lambda item: item["path"])
    document = {"schemaVersion": 1, "artifacts": artifacts}
    validate_record("artifact-index", document)
    return document


def build_manifest(root, metadata):
    index_bytes = read_regular(root, GRAPH_DOCUMENTS["artifact-index"])
    load_record("artifact-index", index_bytes)
    document = dict(metadata)
    document["schemaVersion"] = 1
    document["artifactIndexDigest"] = digest_bytes(index_bytes)
    validate_record("release-manifest", document)
    return document


def build_candidate_lock(root):
    index_bytes = read_regular(root, GRAPH_DOCUMENTS["artifact-index"])
    manifest_bytes = read_regular(root, GRAPH_DOCUMENTS["release-manifest"])
    index = load_record("artifact-index", index_bytes)
    manifest = load_record("release-manifest", manifest_bytes)
    if manifest["artifactIndexDigest"] != digest_bytes(index_bytes):
        fail("release manifest does not name the artifact index bytes")
    document = {
        "schemaVersion": 1,
        "version": manifest["version"],
        "sourceCommit": manifest["sourceCommit"],
        "artifactIndexDigest": digest_bytes(index_bytes),
        "releaseManifestDigest": digest_bytes(manifest_bytes),
        "artifacts": {a["path"]: "sha256:" + a["sha256"] for a in index["artifacts"]},
    }
    validate_record("candidate-lock", document)
    return document


def checksum_lines(root):
    lines = []
    for relative in _walk_files(root):
        if relative == CHECKSUM_FILE:
            continue
        lines.append(f"{raw_sha256(read_regular(root, relative))}  {relative}\n")
    return "".join(lines).encode("utf-8")


def _walk_files(root):
    root = Path(root)
    found = []
    for current, directories, files in os.walk(root):
        directories.sort()
        for name in sorted(directories + files):
            full = Path(current) / name
            relative = full.relative_to(root).as_posix()
            mode = os.lstat(full).st_mode
            if stat.S_ISLNK(mode):
                fail(f"{relative} is a symbolic link")
            if stat.S_ISREG(mode):
                found.append(relative)
            elif not stat.S_ISDIR(mode):
                fail(f"{relative} is a special file")
    return sorted(found)


def verify_root(root):
    """Verify schemas and every cross-document relationship of a candidate root."""
    root = Path(root)
    documents = {}
    raw = {}
    for kind, name in GRAPH_DOCUMENTS.items():
        raw[kind] = read_regular(root, name)
        documents[kind] = load_record(kind, raw[kind])
    index, manifest, lock = (documents[k] for k in ("artifact-index", "release-manifest", "candidate-lock"))

    for artifact in index["artifacts"]:
        size, digest, _ = file_identity(root, artifact["path"])
        if size != artifact["size"]:
            fail(f"{artifact['path']}: size differs from the artifact index")
        if digest != artifact["sha256"]:
            fail(f"{artifact['path']}: bytes differ from the artifact index")

    if manifest["artifactIndexDigest"] != digest_bytes(raw["artifact-index"]):
        fail("release manifest does not name the artifact index bytes")
    if lock["artifactIndexDigest"] != digest_bytes(raw["artifact-index"]):
        fail("candidate lock does not name the artifact index bytes")
    if lock["releaseManifestDigest"] != digest_bytes(raw["release-manifest"]):
        fail("candidate lock does not name the release manifest bytes")
    if lock["version"] != manifest["version"]:
        fail("candidate lock version differs from the release manifest")
    if lock["sourceCommit"] != manifest["sourceCommit"]:
        fail("candidate lock source differs from the release manifest")
    expected = {a["path"]: "sha256:" + a["sha256"] for a in index["artifacts"]}
    if lock["artifacts"] != expected:
        fail("candidate lock artifact map differs from the artifact index")

    allowed = {a["path"] for a in index["artifacts"]} | set(GRAPH_DOCUMENTS.values()) | {CHECKSUM_FILE}
    files = _walk_files(root)
    for relative in files:
        if relative not in allowed:
            fail(f"{relative} is not named by the artifact index")

    if CHECKSUM_FILE in files:
        expected_sums = checksum_lines(root)
        if read_regular(root, CHECKSUM_FILE) != expected_sums:
            fail("SHA256SUMS does not match the candidate files")
    return {
        "candidateLockDigest": digest_bytes(raw["candidate-lock"]),
        "version": manifest["version"],
        "sourceCommit": manifest["sourceCommit"],
        "artifacts": len(index["artifacts"]),
    }


# --------------------------------------------------------------- command line

def main(argv=None):
    parser = argparse.ArgumentParser(description="Telos release graph tools")
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("build-index", "build-manifest"):
        sub = commands.add_parser(name)
        sub.add_argument("--root", required=True)
        sub.add_argument("--out", required=True)
        sub.add_argument("--manifest-list" if name == "build-index" else "--metadata", required=True,
                         dest="descriptors")
    lock = commands.add_parser("build-candidate-lock")
    lock.add_argument("--root", required=True)
    lock.add_argument("--out", required=True)
    verify = commands.add_parser("verify")
    verify.add_argument("--root", required=True)
    validate = commands.add_parser("validate")
    validate.add_argument("--kind", required=True, choices=sorted(SCHEMA_FILES))
    validate.add_argument("file")
    digest = commands.add_parser("digest")
    digest.add_argument("file")
    sums = commands.add_parser("write-checksums")
    sums.add_argument("--root", required=True)
    arguments = parser.parse_args(argv)
    try:
        if arguments.command == "build-index":
            document = build_index(arguments.root, _read_json_file(arguments.descriptors))
            write_atomic_exclusive(arguments.out, canonical_bytes(document))
        elif arguments.command == "build-manifest":
            document = build_manifest(arguments.root, _read_json_file(arguments.descriptors))
            write_atomic_exclusive(arguments.out, canonical_bytes(document))
        elif arguments.command == "build-candidate-lock":
            write_atomic_exclusive(arguments.out, canonical_bytes(build_candidate_lock(arguments.root)))
        elif arguments.command == "verify":
            print(json.dumps(verify_root(arguments.root), sort_keys=True))
        elif arguments.command == "validate":
            load_record(arguments.kind, Path(arguments.file).read_bytes())
            print(f"{arguments.kind}: valid")
        elif arguments.command == "digest":
            print(digest_bytes(Path(arguments.file).read_bytes()))
        elif arguments.command == "write-checksums":
            write_atomic_exclusive(Path(arguments.root) / CHECKSUM_FILE, checksum_lines(arguments.root), 0o644)
    except (GraphError, OSError) as error:
        print(f"release-graph: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
