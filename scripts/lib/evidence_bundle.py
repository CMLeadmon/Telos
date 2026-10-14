#!/usr/bin/env python3
"""Import, sign-check and certify a complete set of candidate-bound gate evidence.

    evidence_bundle.py import  --candidate-lock P --catalog P --source-dir D [--source-dir D ...] --bundle-dir D
    evidence_bundle.py verify  --candidate-lock P --catalog P --bundle-dir D --signers FILE --principal NAME
    evidence_bundle.py certify --candidate DIR --catalog P --evidence-bundle D --signers FILE --out-dir ABS
                               [--repo DIR]            (key: TELOS_EVIDENCE_SIGNING_KEY / _PRINCIPAL in the environment)

The runner's output directories are the only input. Nothing here repairs, re-runs or reinterprets evidence:
a bundle is complete or it is refused. "Complete" means exactly the catalog's required gate set (terminal
aggregate excluded), every envelope valid, passed, bound to this candidate lock and source commit, fresh for
that gate, with its log and producer result intact, and every dependency finished before its dependent
started. Certification adds the checks that need the candidate itself: the frozen graph, every producer
subject re-verified against the frozen bytes, the signed tag, and an evidence signature by an enrolled
principal. A failure writes only a non-certifying diagnostic; a certification file is never half-written.
"""

import argparse
import datetime
import importlib.util
import json
import os
import re
import shutil
import stat
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.dont_write_bytecode = True
sys.path.insert(0, str(HERE))
import evidence  # noqa: E402
import gate_producers as producers  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

TERMINAL = "beta-certification"
INDEX_NAME = "evidence-index.json"
GATES_DIR = "gates"
SIGNATURE_NAME = INDEX_NAME + ".sig"
CERTIFICATION_NAME = "certification.json"
CERTIFICATION_SIGNATURE = CERTIFICATION_NAME + ".sig"
DIAGNOSTIC_NAME = "certification-diagnostic.json"
EXIT_NOT_RUN = 3

_spec = importlib.util.spec_from_file_location("telos_run_gates", REPO / "scripts" / "run-gates.py")
rungates = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(rungates)


class BundleError(Exception):
    """A refusal; the message is safe to print."""


class Unavailable(BundleError):
    """A required trust input does not exist yet (maps to not_run)."""


def fail(message):
    raise BundleError(message)


def now_utc():
    return datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)


def stamp(moment):
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def parse_stamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


# ------------------------------------------------------------------ the catalog and the candidate

class Context:
    def __init__(self, lock_path, catalog_path):
        try:
            self.catalog = rungates.load_catalog(catalog_path)
            self.source_commit, self.lock_digest = rungates.read_candidate_lock(lock_path)
        except rungates.GateError as error:
            fail(str(error))
        if self.catalog["schemaVersion"] != 2:
            fail("certification requires catalog schema v2")
        self.by_id = {gate["id"]: gate for gate in self.catalog["gates"]}
        self.required = sorted(g["id"] for g in self.catalog["gates"] if g["required"] and g["id"] != TERMINAL)
        if not self.required:
            fail("the catalog requires no gates")


def check_directory(directory):
    directory = Path(directory)
    if directory.is_symlink() or not directory.is_dir():
        fail(f"{directory} is not a real directory")
    return directory


def verify_evidence_directories(context, directories, now):
    """The exact required gate set, each passed and fresh, in dependency order. Returns {gate: envelope}."""
    try:
        usable, rejected = producers.load_prior_evidence(
            [str(check_directory(d)) for d in directories], context.by_id, context.source_commit, context.lock_digest, now)
    except producers.ProducerError as error:
        fail(str(error))
    present = set(usable) | set(rejected)
    missing = sorted(set(context.required) - present)
    if missing:
        fail(f"required evidence is missing for: {', '.join(missing[:6])}")
    extra = sorted(present - set(context.required))
    if extra:
        fail(f"evidence is present for gates outside the required set: {', '.join(extra[:6])}")
    if rejected:
        gate_id = sorted(rejected)[0]
        fail(f"{gate_id}: {rejected[gate_id]}")
    envelopes = {}
    for gate_id in context.required:
        envelopes[gate_id] = find_envelope(directories, gate_id)
    for gate_id, envelope in envelopes.items():
        for prerequisite in context.by_id[gate_id]["prerequisites"]:
            if prerequisite in envelopes and parse_stamp(envelopes[prerequisite]["finishedAt"]) > parse_stamp(envelope["startedAt"]):
                fail(f"{prerequisite} finished after its dependent {gate_id} started")
    return envelopes


def find_envelope(directories, gate_id):
    for directory in directories:
        path = Path(directory) / f"{gate_id}.json"
        if path.is_file():
            return evidence.load_envelope(str(path))
    fail(f"no envelope for {gate_id}")


# ------------------------------------------------------------------ import

def gate_files(directory, gate):
    """(envelope path, log path, result path | None, [(relative path, kind)]) of one gate's runner output."""
    directory = Path(directory)
    gate_id = gate["id"]
    files = []
    result_path = None
    if gate["subjectKind"] in producers.PRODUCER_KINDS:
        result_dir = Path(producers.producer_directory(str(directory), gate_id))
        result = rg.verify_result_directory(result_dir, producers.RESULT_NAME)
        result_path = f"{gate_id}.d/{producers.RESULT_NAME}"
        files = [(f"{gate_id}.d/{a['path']}", a["kind"]) for a in result["attachments"]]
    return f"{gate_id}.json", f"{gate_id}.log", result_path, files


def copy_readonly(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as handle:
        handle.write(Path(source).read_bytes())
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(destination, 0o444)


def scan_for_secrets(path):
    data = Path(path).read_bytes()
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError:
        return   # binary attachments are hashed, not read
    try:
        rg.reject_secret_text(text, str(path.name))
    except rg.GraphError as error:
        fail(str(error))


def import_bundle(lock_path, catalog_path, source_dirs, bundle_dir, now=None):
    context = Context(lock_path, catalog_path)
    now = now or now_utc()
    sources = [check_directory(d) for d in source_dirs]
    envelopes = verify_evidence_directories(context, sources, now)
    bundle = Path(bundle_dir)
    if bundle.exists() and (bundle.is_symlink() or not bundle.is_dir() or any(bundle.iterdir())):
        fail("the bundle directory must be empty or absent")
    gates = []
    plan = []
    for gate_id in context.required:
        gate = context.by_id[gate_id]
        origin = next(d for d in sources if (d / f"{gate_id}.json").is_file())
        envelope_name, log_name, result_name, attachments = gate_files(origin, gate)
        plan.append((origin, envelope_name, log_name, result_name, attachments, gate_id))
    for origin, envelope_name, log_name, result_name, attachments, gate_id in plan:
        for name in (envelope_name, log_name, *([result_name] if result_name else []), *[a for a, _ in attachments]):
            path = origin / name
            if path.is_symlink() or not path.is_file():
                fail(f"{name} is not a regular file")
            scan_for_secrets(path)
    bundle.mkdir(parents=True, exist_ok=True, mode=0o755)
    try:
        for origin, envelope_name, log_name, result_name, attachments, gate_id in plan:
            entry_files = []
            for name in (envelope_name, log_name, *([result_name] if result_name else []), *[a for a, _ in attachments]):
                copy_readonly(origin / name, bundle / GATES_DIR / name)
            ref = lambda name: {"path": f"{GATES_DIR}/{name}", "sha256": rg.raw_sha256((bundle / GATES_DIR / name).read_bytes())}
            for name, kind in attachments:
                entry_files.append({"kind": kind, "path": f"{GATES_DIR}/{name}", "sha256": rg.raw_sha256((bundle / GATES_DIR / name).read_bytes())})
            gates.append({"gateId": gate_id, "envelope": ref(envelope_name), "log": ref(log_name),
                          "result": ref(result_name) if result_name else None, "files": sorted(entry_files, key=lambda f: f["path"])})
        index = {"schemaVersion": 1, "candidateLockDigest": context.lock_digest, "sourceCommit": context.source_commit,
                 "createdAt": stamp(now), "gates": gates}
        rg.validate_record("evidence-index", index)
        index_path = bundle / INDEX_NAME
        rg.write_atomic_exclusive(index_path, rg.canonical_bytes(index), 0o444)
        for directory in sorted((p for p in bundle.rglob("*") if p.is_dir()), reverse=True):
            os.chmod(directory, 0o555)
        os.chmod(bundle, 0o555)
    except BaseException:
        make_writable(bundle)
        shutil.rmtree(bundle, ignore_errors=True)
        raise
    return {"gates": len(gates), "evidenceIndexDigest": rg.digest_bytes((bundle / INDEX_NAME).read_bytes())}


def make_writable(path):
    """Undo the read-only protection so a directory can be removed; a symbolic link is never followed."""
    for current, directories, files in os.walk(path):
        os.chmod(current, 0o755)
        for name in files:
            target = os.path.join(current, name)
            if not os.path.islink(target):
                os.chmod(target, 0o644)


# ------------------------------------------------------------------ verify

def verify_bundle(lock_path, catalog_path, bundle_dir, signers, principal, now=None):
    """Everything about a signed bundle that does not need the candidate's bytes. Returns a summary."""
    context = Context(lock_path, catalog_path)
    now = now or now_utc()
    bundle = check_directory(bundle_dir)
    index_bytes = rg.read_regular(bundle, INDEX_NAME)
    try:
        index = rg.load_record("evidence-index", index_bytes)
    except rg.GraphError as error:
        fail(f"the evidence index is invalid: {error}")
    if index["candidateLockDigest"] != context.lock_digest or index["sourceCommit"] != context.source_commit:
        fail("the evidence index is bound to another candidate")
    if parse_stamp(index["createdAt"]) > now:
        fail("the evidence index was created in the future")
    listed = {INDEX_NAME, SIGNATURE_NAME}
    for entry in index["gates"]:
        refs = [entry["envelope"], entry["log"], *([entry["result"]] if entry["result"] else []), *entry["files"]]
        for ref in refs:
            listed.add(ref["path"])
            size, digest, _ = rg.file_identity(bundle, ref["path"])
            if digest != ref["sha256"]:
                fail(f"{ref['path']} changed after it was indexed")
    present = set()
    for current, directories, files in os.walk(bundle):
        for name in list(directories) + files:
            full = os.path.join(current, name)
            if os.path.islink(full):
                fail(f"{os.path.relpath(full, bundle)} is a symbolic link")
            if os.path.isfile(full):
                present.add(os.path.relpath(full, bundle).replace(os.sep, "/"))
    unlisted = sorted(present - listed)
    if unlisted:
        fail(f"the bundle holds an unindexed file: {unlisted[0]}")
    gate_ids = [entry["gateId"] for entry in index["gates"]]
    if gate_ids != sorted(set(gate_ids)):
        fail("the evidence index repeats or misorders a gate")
    envelopes = verify_evidence_directories(context, [bundle / GATES_DIR], now)
    for entry in index["gates"]:
        if entry["envelope"]["path"] != f"{GATES_DIR}/{entry['gateId']}.json" or \
                entry["envelope"]["sha256"] != rg.raw_sha256(rg.read_regular(bundle, f"{GATES_DIR}/{entry['gateId']}.json")):
            fail(f"{entry['gateId']}: the indexed envelope is not the gate's envelope")
    if sorted(gate_ids) != context.required:
        fail("the evidence index does not list exactly the required gates")
    summary = {"evidenceIndexDigest": rg.digest_bytes(index_bytes),
               "envelopes": {gid: rg.digest_bytes(rg.read_regular(bundle, f"{GATES_DIR}/{gid}.json")) for gid in context.required},
               "lastFinishedAt": max(env["finishedAt"] for env in envelopes.values()), "context": context}
    if signers is None:
        return summary   # the structure alone, before anyone signs
    try:
        signature = rg.read_regular(bundle, SIGNATURE_NAME)
    except rg.GraphError:
        fail("the evidence index is not signed")
    try:
        fingerprint = signing.verify_signature(signers, principal, signing.NAMESPACES["evidence"], index_bytes, signature)
    except signing.SigningError as error:
        fail(f"the evidence signature is not from an enrolled principal: {error}")
    summary.update(signerPrincipal=principal, signerFingerprint=fingerprint)
    return summary


def sign_bundle(lock_path, catalog_path, bundle_dir, signers, key, principal, now=None):
    """Sign the verified, unsigned evidence index with an enrolled principal's key."""
    verify_bundle(lock_path, catalog_path, bundle_dir, None, None, now)
    bundle = Path(bundle_dir)
    if os.path.lexists(bundle / SIGNATURE_NAME):
        fail("the evidence index is already signed")
    index_bytes = rg.read_regular(bundle, INDEX_NAME)
    try:
        signature = signing.sign_file(key, signing.NAMESPACES["evidence"], bundle / INDEX_NAME)
        fingerprint = signing.verify_signature(signers, principal, signing.NAMESPACES["evidence"], index_bytes, signature)
    except signing.SigningError as error:
        fail(f"the evidence could not be signed by an enrolled principal: {error}")
    previous = stat.S_IMODE(os.stat(bundle).st_mode)
    os.chmod(bundle, 0o755)
    try:
        rg.write_atomic_exclusive(bundle / SIGNATURE_NAME, signature, 0o444)
    finally:
        os.chmod(bundle, previous)
    return {"signerPrincipal": principal, "signerFingerprint": fingerprint, "evidenceIndexDigest": rg.digest_bytes(index_bytes)}


# ------------------------------------------------------------------ certification

def verify_candidate(candidate, context, repo):
    """The frozen graph, the signed tag and every producer subject against the frozen bytes."""
    candidate = Path(candidate)
    try:
        graph = rg.verify_root(candidate)
    except rg.GraphError as error:
        fail(f"the candidate graph does not verify: {error}")
    lock_bytes = rg.read_regular(candidate, "candidate-lock.json")
    if rg.digest_bytes(lock_bytes) != context.lock_digest:
        fail("the candidate is not the one the evidence is bound to")
    manifest = rg.load_record("release-manifest", rg.read_regular(candidate, "release-manifest.json"))
    signers = Path(repo) / "release" / "telos-tag-signers"
    entries = [l for l in signers.read_text(encoding="utf-8").splitlines() if l.strip() and not l.lstrip().startswith("#")] if signers.is_file() else []
    if not entries:
        raise Unavailable("release/telos-tag-signers enrolls no signer, so the candidate's tag cannot be verified")
    tag = manifest["sourceTag"]
    verified = subprocess.run(["git", "-C", str(repo), "-c", "gpg.format=ssh", "-c", f"gpg.ssh.allowedSignersFile={signers}", "tag", "-v", tag],
                              capture_output=True, text=True, check=False)
    if verified.returncode != 0:
        fail(f"the candidate's tag {tag} is not signed by an enrolled signer")
    pointed = subprocess.run(["git", "-C", str(repo), "rev-parse", f"refs/tags/{tag}^{{commit}}"], capture_output=True, text=True, check=False)
    if pointed.returncode != 0 or pointed.stdout.strip() != manifest["sourceCommit"]:
        fail("the candidate's tag does not point at its source commit")
    return graph


def verify_subjects(candidate, context, bundle):
    """Re-derive every producer gate's observed subject from the frozen candidate and compare to the envelope."""
    lock_path = str(Path(candidate) / "candidate-lock.json")
    frozen = producers.Candidate(lock_path, context.source_commit, context.lock_digest)
    if not frozen.full:
        fail(f"the candidate cannot be examined: {frozen.problem}")
    for gate_id in context.required:
        gate = context.by_id[gate_id]
        if gate["subjectKind"] not in producers.PRODUCER_KINDS:
            continue
        envelope = evidence.load_envelope(str(Path(bundle) / GATES_DIR / f"{gate_id}.json"))
        directory = producers.producer_directory(str(Path(bundle) / GATES_DIR), gate_id)
        try:
            status, _, subject, _ = producers.evaluate_result(gate, frozen, directory, envelope["exitCode"])
        except producers.ProducerError as error:
            fail(f"{gate_id}: {error}")
        if status != "passed" or subject is None:
            fail(f"{gate_id}: its producer result is not a passed candidate record")
        if envelope["subject"] != {"kind": subject[0], "digest": subject[1]}:
            fail(f"{gate_id}: the envelope's subject is not what the frozen candidate yields")


def write_diagnostic(out_dir, reasons):
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    data = rg.canonical_bytes({"schemaVersion": 1, "status": "not_certified", "reasons": reasons, "at": stamp(now_utc())})
    target = out / DIAGNOSTIC_NAME
    if target.exists():
        target.unlink()
    rg.write_atomic_exclusive(target, data, 0o644)


def certify(candidate, catalog_path, bundle_dir, signers, out_dir, repo, key, principal, now=None):
    out = Path(out_dir)
    if not out.is_absolute():
        fail("--out-dir must be absolute")
    if out.exists() and any(p.name in (CERTIFICATION_NAME, CERTIFICATION_SIGNATURE) for p in out.iterdir()):
        fail("the output directory already holds a certification")
    lock_path = str(Path(candidate) / "candidate-lock.json")
    now = now or now_utc()
    verified = verify_bundle(lock_path, catalog_path, bundle_dir, signers, principal, now)
    context = verified.pop("context")
    verify_candidate(candidate, context, repo)
    verify_subjects(candidate, context, bundle_dir)
    if now < parse_stamp(verified["lastFinishedAt"]):
        fail("a certification cannot be created before the last evidence finished")
    document = {"schemaVersion": 1, "candidateLockDigest": context.lock_digest, "sourceCommit": context.source_commit,
                "evidenceIndexDigest": verified["evidenceIndexDigest"], "requiredGates": verified["envelopes"],
                "signerPrincipal": principal, "signerFingerprint": verified["signerFingerprint"],
                "certifiedAt": stamp(now), "status": "passed"}
    rg.validate_record("certification", document)
    data = rg.canonical_bytes(document)
    import tempfile
    with tempfile.TemporaryDirectory() as scratch:
        payload = Path(scratch) / CERTIFICATION_NAME
        payload.write_bytes(data)
        try:
            signature = signing.sign_file(key, signing.NAMESPACES["certification"], payload)
            signer = signing.verify_signature(signers, principal, signing.NAMESPACES["certification"], data, signature)
        except signing.SigningError as error:
            fail(f"the certification could not be signed by the enrolled principal: {error}")
    if signer != verified["signerFingerprint"]:
        fail("the certification key is not the key that signed the evidence")
    out.mkdir(parents=True, exist_ok=True)
    rg.write_atomic_exclusive(out / CERTIFICATION_SIGNATURE, signature, 0o644)
    rg.write_atomic_exclusive(out / CERTIFICATION_NAME, data, 0o644)
    return document


# ------------------------------------------------------------------ command line

def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    imp = commands.add_parser("import")
    imp.add_argument("--candidate-lock", required=True)
    imp.add_argument("--catalog", required=True)
    imp.add_argument("--source-dir", action="append", required=True)
    imp.add_argument("--bundle-dir", required=True)
    ver = commands.add_parser("verify")
    ver.add_argument("--candidate-lock", required=True)
    ver.add_argument("--catalog", required=True)
    ver.add_argument("--bundle-dir", required=True)
    ver.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    ver.add_argument("--principal", required=True)
    sig = commands.add_parser("sign")
    sig.add_argument("--candidate-lock", required=True)
    sig.add_argument("--catalog", required=True)
    sig.add_argument("--bundle-dir", required=True)
    sig.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    cert = commands.add_parser("certify")
    cert.add_argument("--candidate", required=True)
    cert.add_argument("--catalog", required=True)
    cert.add_argument("--evidence-bundle", required=True)
    cert.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    cert.add_argument("--out-dir", required=True)
    cert.add_argument("--repo", default=str(REPO))
    args = parser.parse_args(argv)
    try:
        if args.command == "import":
            print(json.dumps(import_bundle(args.candidate_lock, args.catalog, args.source_dir, args.bundle_dir), sort_keys=True))
        elif args.command == "verify":
            summary = verify_bundle(args.candidate_lock, args.catalog, args.bundle_dir, args.signers, args.principal)
            summary.pop("context")
            summary.pop("envelopes")
            print(json.dumps(summary, sort_keys=True))
        elif args.command == "sign":
            key = os.environ.get("TELOS_EVIDENCE_SIGNING_KEY")
            principal = os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL")
            if not key or not principal:
                raise Unavailable("TELOS_EVIDENCE_SIGNING_KEY and TELOS_EVIDENCE_SIGNING_PRINCIPAL are required to sign")
            print(json.dumps(sign_bundle(args.candidate_lock, args.catalog, args.bundle_dir, args.signers, key, principal), sort_keys=True))
        else:
            key = os.environ.get("TELOS_EVIDENCE_SIGNING_KEY")
            principal = os.environ.get("TELOS_EVIDENCE_SIGNING_PRINCIPAL")
            if not key or not principal:
                raise Unavailable("TELOS_EVIDENCE_SIGNING_KEY and TELOS_EVIDENCE_SIGNING_PRINCIPAL are required to certify")
            document = certify(args.candidate, args.catalog, args.evidence_bundle, args.signers, args.out_dir, args.repo, key, principal)
            print(json.dumps({"status": document["status"], "candidateLockDigest": document["candidateLockDigest"]}, sort_keys=True))
        return 0
    except Unavailable as error:
        print(f"not_run: {error}", file=sys.stderr)
        return EXIT_NOT_RUN
    except (BundleError, rg.GraphError, evidence.EvidenceError, signing.SigningError, OSError, KeyError, ValueError) as error:
        print(f"evidence: {error}", file=sys.stderr)
        if args.command == "certify":
            try:
                write_diagnostic(args.out_dir, [str(error)[:400]])
            except (OSError, rg.GraphError):
                pass
        return 1


if __name__ == "__main__":
    sys.exit(main())
