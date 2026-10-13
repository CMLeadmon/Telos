#!/usr/bin/env python3
"""Configurable stand-in for a catalog-v2 producer (test fixture only).

The runner supplies ``--candidate-lock`` and ``--result-out`` (plus fixed input
options); behavior is selected by FIXTURE_MODE so tests can exercise every way a
producer can honour or violate the result contract.
"""
import json
import os
import platform
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO / "scripts/lib"))
import release_graph as rg  # noqa: E402


def option(name):
    return sys.argv[sys.argv.index(name) + 1]


def main():
    mode = os.environ.get("FIXTURE_MODE", "artifact-ok")
    log = os.environ.get("FIXTURE_ARGV_LOG")
    if log:
        Path(log).write_text(json.dumps(sys.argv[1:]))
    if mode == "crash":
        print("producer crashed", file=sys.stderr)
        return 1
    lock_path = option("--candidate-lock")
    result_path = Path(option("--result-out"))
    out = result_path.parent
    lock_bytes = Path(lock_path).read_bytes()
    lock_digest = rg.digest_bytes(lock_bytes)
    lock = json.loads(lock_bytes)
    root = Path(lock_path).parent
    gate_id = os.environ.get("TELOS_GATE_ID", "unknown-gate")
    selector = os.environ.get("FIXTURE_SELECTOR", "artifact-index")
    kind = os.environ.get("FIXTURE_KIND", "artifact")

    if mode == "no-result-exit0":
        return 0
    if mode == "no-result-exit3":
        return 3

    record = {
        "schemaVersion": 1, "recordKind": "candidate", "gateId": gate_id,
        "sourceCommit": lock["sourceCommit"], "candidateLockDigest": lock_digest,
        "status": "passed", "reason": "fixture producer verified the subject",
        "subjectFile": "subject.json", "attachments": [],
        "toolVersions": {"python3": platform.python_version()},
    }
    if mode == "gate-id-mismatch":
        record["gateId"] = "some-other-gate"
    if mode == "rehearsal":
        record["recordKind"] = "rehearsal"
    if mode == "secret-reason":
        record["reason"] = "password: hunter2hunter2"
    if mode == "status-mismatch":
        record["status"] = "failed"
    if mode == "not-run":
        record.update(status="not_run", reason="fixture prerequisite unavailable", subjectFile=None)
    if mode == "wrong-lock":
        record["candidateLockDigest"] = "sha256:" + "9" * 64

    subject = None
    if record["subjectFile"]:
        if kind == "artifact":
            if selector == "artifact-index":
                digest = rg.digest_bytes((root / "artifact-index.json").read_bytes())
            else:
                digest = rg.digest_bytes((root / selector.split(":", 1)[1]).read_bytes())
            if mode == "artifact-wrong-digest":
                digest = "sha256:" + "0" * 64
            subject = {"schemaVersion": 1, "kind": "artifact", "sourceCommit": lock["sourceCommit"],
                       "candidateLockDigest": lock_digest, "selector": selector, "artifactDigest": digest}
        elif kind == "runtime":
            services = json.loads(os.environ["FIXTURE_SERVICES"])
            subject = {"schemaVersion": 1, "kind": "runtime", "sourceCommit": lock["sourceCommit"],
                       "candidateLockDigest": lock_digest, "observedAt": "2026-10-13T12:00:00Z",
                       "hostId": "sha256:" + "a" * 64, "bootId": "12345678-1234-1234-1234-123456789abc",
                       "runtime": {"name": "podman", "version": "5.7.0", "rootless": False},
                       "generationReceiptDigest": "sha256:" + "b" * 64, "services": services}
        elif kind == "external-host":
            records = []
            subject = {"schemaVersion": 1, "kind": "external-host", "sourceCommit": lock["sourceCommit"],
                       "candidateLockDigest": lock_digest, "observedAt": "2026-10-13T12:00:00Z",
                       "purpose": gate_id,
                       "hosts": [{"hostId": "sha256:" + "c" * 64, "identityMethod": "machine-id-sha256",
                                  "os": "Ubuntu", "osVersion": "24.04", "arch": "x86_64"}],
                       "records": records}
        data = rg.canonical_bytes(subject)
        (out / "subject.json").write_bytes(data)
        record["attachments"] = [{"path": "subject.json", "sha256": rg.raw_sha256(data), "kind": "subject"}]
    if mode == "unlisted-file":
        (out / "stray.txt").write_text("not declared")
    rg.write_atomic_exclusive(result_path, rg.canonical_bytes(record))
    return {"passed": 0, "not_run": 3}.get(record["status"], 1) if mode != "status-mismatch" else 0


if __name__ == "__main__":
    sys.exit(main())
