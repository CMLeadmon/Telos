#!/usr/bin/env python3
"""Guided and automated smoke records: closed, candidate-bound, signed, and aggregated into one gate result.

A record answers every question of release/guided-assertions.json for one platform, in order, with pass, fail or
not_run. It names the exact candidate and the exact artifact under test, the tester's OS/app/device, a start and an end
time, hashed attachments and a tester principal. The recorder can never write an overall `passed`: this module derives
that, and only when every answer is `pass`. A signature in the `telos-guided-smoke-v1` namespace by an enrolled tester
binds the canonical record bytes. WebKit automation is its own platform; it never satisfies a Safari record.

    guided_smoke.py aggregate --matrix browser|desktop --records-dir DIR
                              (--rehearsal --source-commit SHA [--rehearsal-lock FILE] | --candidate-lock FILE) --result-out FILE
                              [--signers FILE] [--max-age-hours N]
"""

import argparse
import datetime
import json
import os
import re
import shutil
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
sys.dont_write_bytecode = True
sys.path.insert(0, str(HERE))
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

CATALOG_PATH = REPO / "release" / "guided-assertions.json"
NAMESPACE = signing.NAMESPACES["guided"]
MAX_ATTACHMENT_BYTES = 20 << 20
AUTOMATED_DEVICE = "automated"


class GuidedError(Exception):
    """A refusal; the message is safe to print."""


def fail(message):
    raise GuidedError(message)


def load_catalog(path=None):
    document = json.loads(Path(path or CATALOG_PATH).read_text(encoding="utf-8"))
    if document.get("schemaVersion") != 1 or set(document) - {"schemaVersion", "note", "matrices"}:
        fail("guided-assertions.json is not a schemaVersion 1 catalog")
    for name, matrix in document["matrices"].items():
        ids = [a["id"] for a in matrix["assertions"]]
        if len(ids) != len(set(ids)) or not ids:
            fail(f"the {name} matrix repeats or lacks assertions")
        if not matrix["platforms"]:
            fail(f"the {name} matrix has no platforms")
    return document


def stamp(moment):
    return moment.strftime("%Y-%m-%dT%H:%M:%SZ")


def now_utc():
    return datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)


def parse_stamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def assertions_for(catalog, matrix, platform):
    """The question list a platform answers: automated platforms have their own journeys."""
    spec = catalog["matrices"][matrix]
    if spec["platforms"][platform]["mode"] == "automated":
        return spec["automatedAssertions"]
    return spec["assertions"]


def derive_status(record):
    """passed only when every answer is pass; failed if any is fail; otherwise not_run."""
    answers = [a["answer"] for a in record["assertions"]]
    if "fail" in answers:
        return "failed"
    if "not_run" in answers:
        return "not_run"
    return "passed"


def matrix_artifact_digest(catalog, matrix, lock):
    """The digest of the artifact this matrix tests, taken from the candidate lock."""
    spec = catalog["matrices"][matrix]["artifact"]
    artifacts = lock["artifacts"]
    if spec.startswith("kind:"):
        found = [digest for path, digest in artifacts.items() if path.endswith(".AppImage")]
        if len(found) != 1:
            fail("the candidate must contain exactly one AppImage for the desktop matrix")
        return found[0]
    if spec not in artifacts:
        fail(f"{spec} is not in the candidate lock")
    return artifacts[spec]


def validate_record(record, catalog, lock, lock_digest, now=None, max_age_hours=None):
    """Schema plus every semantic rule. Raises GuidedError; returns the derived status."""
    try:
        rg.validate_record("guided-smoke", record)
    except rg.GraphError as error:
        fail(f"the record is invalid: {error}")
    matrix = catalog["matrices"].get(record["matrix"])
    if matrix is None:
        fail(f"unknown matrix {record['matrix']!r}")
    platform = matrix["platforms"].get(record["platform"])
    if platform is None:
        fail(f"{record['platform']!r} is not a platform of the {record['matrix']} matrix (aliases are not accepted)")
    expected = [a["id"] for a in assertions_for(catalog, record["matrix"], record["platform"])]
    actual = [a["id"] for a in record["assertions"]]
    if actual != expected:
        missing = [i for i in expected if i not in actual]
        extra = [i for i in actual if i not in expected]
        fail(f"{record['platform']}: assertions must be exactly {len(expected)} in catalog order "
             f"(missing {missing[:3]}, unexpected {extra[:3]}, or misordered)")
    if record["candidateLockDigest"] != lock_digest or record["sourceCommit"] != lock["sourceCommit"]:
        fail(f"{record['platform']}: the record is bound to another candidate")
    if record["artifactDigest"] != matrix_artifact_digest(catalog, record["matrix"], lock):
        fail(f"{record['platform']}: the record tested another artifact than the candidate's")
    automated = platform["mode"] == "automated"
    if automated != (record["deviceModel"] == AUTOMATED_DEVICE):
        fail(f"{record['platform']}: {'an automated platform needs an automated record' if automated else 'a guided platform needs a real device, not automation'}")
    for field in ("applicationVersion", "os", "osVersion", "deviceModel"):
        if record[field].strip().lower() in ("", "unknown", "n/a", "none", "tbd"):
            fail(f"{record['platform']}: {field} was not captured")
    started, finished = parse_stamp(record["startedAt"]), parse_stamp(record["finishedAt"])
    current = now or now_utc()
    if finished < started:
        fail(f"{record['platform']}: the record ends before it starts")
    if finished > current:
        fail(f"{record['platform']}: the record ends in the future")
    if max_age_hours is not None and (current - finished) > datetime.timedelta(hours=max_age_hours):
        fail(f"{record['platform']}: the record is older than {max_age_hours} hours")
    names = [a["path"] for a in record["attachments"]]
    if len(set(names)) != len(names):
        fail(f"{record['platform']}: an attachment is listed twice")
    for entry in record["assertions"]:
        rg.reject_secret_text(entry["notes"], f"{record['platform']} {entry['id']} notes")
    rg.reject_secret_text(json.dumps(record), f"{record['platform']} record")
    return derive_status(record)


def sign_record(record_bytes, key):
    return signing.sign_file(key, NAMESPACE, _temporary(record_bytes))


def _temporary(data):
    import tempfile
    handle = tempfile.NamedTemporaryFile(delete=False)
    handle.write(data)
    handle.close()
    os.chmod(handle.name, 0o600)
    return handle.name


def verify_signature(signers, record, record_bytes, signature):
    """The fingerprint of the enrolled tester who signed these exact record bytes."""
    try:
        return signing.verify_signature(signers, record["testerPrincipal"], NAMESPACE, record_bytes, signature)
    except signing.SigningError as error:
        fail(f"{record['platform']}: the record is not signed by an enrolled tester: {error}")



# ------------------------------------------------------------------ writing a finished record

def file_sha256(path):
    import hashlib
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def safe_name(name, number=1):
    return re.sub(r"[^A-Za-z0-9._-]", "_", name) or f"file-{number}"


def attachment_refs(platform, sources):
    """Hash attachment files; return (refs sorted by path, {ref path: source}). Refuses links, large files and secrets."""
    refs, mapping = [], {}
    for number, source in enumerate(sources, 1):
        source = Path(source)
        if source.is_symlink() or not source.is_file() or source.stat().st_size > MAX_ATTACHMENT_BYTES:
            fail(f"attachment {source.name} is missing, a link or too large")
        data = source.read_bytes()
        try:
            rg.reject_secret_text(data.decode("utf-8"), source.name)
        except UnicodeDecodeError:
            pass
        except rg.GraphError as error:
            fail(str(error))
        path = f"{platform}.attachments/{safe_name(source.name, number)}"
        if path in mapping:
            fail(f"two attachments would both be named {path}")
        refs.append({"path": path, "sha256": rg.raw_sha256(data)})
        mapping[path] = source
    return sorted(refs, key=lambda ref: ref["path"]), mapping


def write_record(record, mapping, out, signers, draft_dir, key=None, principal=None, status=None):
    """Write a validated record beside its attachments and a detached signature; or, with no enrolled tester, only a draft.

    Returns (exit code, message): 0 written and signed, 3 saved as an unsigned draft outside the records directory.
    """
    out = Path(out)
    data = rg.canonical_bytes(record)
    signature, reason = None, None
    if not key or not principal:
        reason = "no signing key or principal in the environment"
    else:
        try:
            signature = sign_record(data, key)
            verify_signature(signers, record, data, signature)
        except (signing.SigningError, GuidedError) as error:
            signature, reason = None, str(error)[:200]
    if signature is None:
        draft = Path(draft_dir)
        draft.mkdir(parents=True, exist_ok=True, mode=0o700)
        target = draft / f"{record['matrix']}-{record['platform']}-{record['finishedAt'].replace(':', '')}.draft.json"
        rg.write_atomic_exclusive(str(target), data, 0o600)
        return 3, f"saved an UNSIGNED draft ({status}) at {target}: {reason}. It is not importable evidence."
    out.parent.mkdir(parents=True, exist_ok=True)
    for ref_path, source in mapping.items():
        target = out.parent / ref_path
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)
    rg.write_atomic_exclusive(str(Path(str(out) + ".sig")), signature, 0o644)
    rg.write_atomic_exclusive(str(out), data, 0o644)
    return 0, f"wrote {out} ({status}, signed by {principal})"

# ------------------------------------------------------------------ aggregation

def host_entry(record):
    host_id = record["hostId"]
    method = "machine-id-sha256" if host_id.startswith("sha256:") else "tester-assigned"
    return {"hostId": host_id, "identityMethod": method, "os": record["os"], "osVersion": record["osVersion"], "arch": "n/a"}


def aggregate(args):
    catalog = load_catalog()
    mode, commit, lock_digest = args.resolve(args)
    lock_path = Path(args.candidate_lock or args.rehearsal_lock or "")
    if not lock_path.is_file():
        raise dc.NotRun("a candidate (or rehearsal) lock is required to know which artifact was tested")
    lock = rg.load_record("candidate-lock", lock_path.read_bytes(), canonical=False)
    matrix = args.matrix
    spec = catalog["matrices"][matrix]
    records_dir = Path(args.records_dir)
    if records_dir.is_symlink() or not records_dir.is_dir():
        raise dc.NotRun("the records directory does not exist")
    gate_id = os.environ.get("TELOS_GATE_ID", {"browser": "browser-matrix", "desktop": "desktop-linux"}[matrix])
    allowed = {f"{p}.json" for p in spec["platforms"]} | {f"{p}.json.sig" for p in spec["platforms"]}
    entries = sorted(p.name for p in records_dir.iterdir())
    attachments_dirs = {f"{p}.attachments" for p in spec["platforms"]}
    unknown = [n for n in entries if n not in allowed and n not in attachments_dirs]
    if unknown:
        raise dc.DrillError(f"unexpected entry in the records directory: {unknown[0]}")
    assertions, hosts, extra, seen_digests, observation = [], [], [], {}, {"matrix": matrix, "platforms": {}}
    versions = {"python3": sys.version.split()[0]}
    for platform_id in spec["platforms"]:
        record_path, signature_path = records_dir / f"{platform_id}.json", records_dir / f"{platform_id}.json.sig"
        item = {"id": f"platform.{platform_id}", "description": spec["platforms"][platform_id]["name"]}
        if not record_path.is_file() or not signature_path.is_file():
            assertions.append({**item, "status": "not_run", "detail": "no signed record for this platform"})
            continue
        data, signature = record_path.read_bytes(), signature_path.read_bytes()
        try:
            record = json.loads(data)
            rg.parse_canonical(data)
            if record.get("platform") != platform_id or record.get("matrix") != matrix:
                fail(f"{platform_id}: the file holds a record for another platform")
            status = validate_record(record, catalog, lock, lock_digest, now=args.now, max_age_hours=args.max_age_hours)
            fingerprint = verify_signature(args.signers, record, data, signature)
            digest = rg.raw_sha256(data)
            if digest in seen_digests:
                fail(f"{platform_id}: the same record was supplied for {seen_digests[digest]}")
            seen_digests[digest] = platform_id
            attachment_files = []
            for ref in record["attachments"]:
                path = records_dir / ref["path"]
                if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_ATTACHMENT_BYTES:
                    fail(f"{platform_id}: attachment {ref['path']} is missing, linked or too large")
                blob = path.read_bytes()
                if rg.raw_sha256(blob) != ref["sha256"]:
                    fail(f"{platform_id}: attachment {ref['path']} changed after it was recorded")
                attachment_files.append((f"{matrix}/{ref['path']}", blob, "report"))
        except (GuidedError, rg.GraphError, ValueError, KeyError) as error:
            assertions.append({**item, "status": "failed", "detail": str(error)[:240]})
            continue
        assertions.append({**item, "status": {"passed": "passed", "failed": "failed", "not_run": "not_run"}[status],
                           "detail": f"{sum(a['answer'] == 'pass' for a in record['assertions'])}/{len(record['assertions'])} answered pass; signed by {record['testerPrincipal']} ({fingerprint})"})
        hosts.append(host_entry(record))
        extra += [(f"{matrix}/{platform_id}.json", data, "guided-record"), (f"{matrix}/{platform_id}.json.sig", signature, "signature"), *attachment_files]
        observation["platforms"][platform_id] = {"status": status, "app": record["applicationVersion"], "os": f"{record['os']} {record['osVersion']}",
                                                 "device": record["deviceModel"], "recordSha256": rg.raw_sha256(data)}
    unique_hosts = {h["hostId"]: h for h in hosts}
    observed_at = stamp(args.now or now_utc())
    return dc.finalize(args, mode, commit, lock_digest, gate_id, assertions, observation, list(unique_hosts.values()), versions, observed_at,
                       label=gate_id, extra_files=extra)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    agg = commands.add_parser("aggregate")
    agg.add_argument("--matrix", required=True, choices=["browser", "desktop"])
    agg.add_argument("--records-dir", required=True)
    agg.add_argument("--signers", default=str(REPO / "release" / "evidence-signers"))
    agg.add_argument("--max-age-hours", type=int, default=168)
    resolve = dc.parse_mode_arguments(agg)
    args = parser.parse_args(argv)
    args.resolve, args.now = resolve, None
    try:
        return aggregate(args)
    except dc.NotRun as error:
        print(f"{args.matrix}-matrix: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, GuidedError, rg.GraphError) as error:
        print(f"{args.matrix}-matrix: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError) as error:
        print(f"{args.matrix}-matrix: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
