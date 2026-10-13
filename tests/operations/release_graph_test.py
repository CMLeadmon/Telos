#!/usr/bin/env python3
"""Mutation tests for the strict, acyclic release graph (Phase 5 Task 1a).

Each test builds a tiny valid candidate graph, changes exactly one dimension and
requires a specific rejection. Nothing here needs a signing key, a network or a
container runtime.
"""
import copy
import hashlib
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/lib"))
import release_graph as rg  # noqa: E402
import schema_check  # noqa: E402

COMMIT = "a" * 40
LOCK = "sha256:" + "b" * 64
OTHER = "sha256:" + "c" * 64
D = "sha256:" + "d" * 64
VERSION = "0.1.0-beta.1"

PAYLOAD = {
    "images/telos-core.oci.tar": (b"core image bytes", "oci-image", "linux/amd64", D),
    "images/telos-client.oci.tar": (b"client image bytes", "oci-image", "linux/amd64", OTHER),
    "desktop/telos-0.1.0-beta.1-x86_64.AppImage": (b"appimage", "desktop", "linux-x86_64", None),
    "config/images.env": (b"TELOS_CORE_IMAGE=example\n", "config", "any", None),
}


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


def make_graph(base):
    """Build a fully valid candidate root and return it."""
    root = Path(base) / "candidate"
    descriptors = []
    for relative, (data, kind, platform, content) in PAYLOAD.items():
        write(root / relative, data)
        descriptors.append({"path": relative, "kind": kind, "platform": platform, "contentDigest": content})
    index = rg.build_index(root, {"artifacts": descriptors})
    rg.write_atomic_exclusive(root / "artifact-index.json", rg.canonical_bytes(index))
    metadata = {
        "version": VERSION, "sourceCommit": COMMIT, "sourceTag": "v" + VERSION,
        "sourceDateEpoch": 1787234400, "imagesLockDigest": D, "migrationManifestDigest": OTHER,
        "buildInputs": {"npmLock": D, "cargoLock": OTHER, "goSum": D},
    }
    manifest = rg.build_manifest(root, metadata)
    rg.write_atomic_exclusive(root / "release-manifest.json", rg.canonical_bytes(manifest))
    lock = rg.build_candidate_lock(root)
    rg.write_atomic_exclusive(root / "candidate-lock.json", rg.canonical_bytes(lock))
    rg.write_atomic_exclusive(root / "SHA256SUMS", rg.checksum_lines(root), 0o644)
    return root


def rewrite(root, name, mutate, canonical=True):
    path = root / name
    document = json.loads(path.read_text())
    mutate(document)
    path.write_bytes(rg.canonical_bytes(document) if canonical else json.dumps(document, indent=2).encode())
    refresh_checksums(root)


def refresh_checksums(root):
    path = root / "SHA256SUMS"
    if path.exists():
        path.write_bytes(rg.checksum_lines(root))


class GraphTestCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = make_graph(self.temp.name)

    def assertGraphRejects(self, fragment):
        with self.assertRaises(rg.GraphError) as caught:
            rg.verify_root(self.root)
        self.assertIn(fragment, str(caught.exception))


class CanonicalBytesTests(unittest.TestCase):
    def test_canonical_form_is_sorted_compact_utf8_with_one_lf(self):
        data = rg.canonical_bytes({"b": [1, {"y": "é", "x": None}], "a": True})
        self.assertEqual(data, '{"a":true,"b":[1,{"x":null,"y":"é"}]}\n'.encode("utf-8"))

    def test_rejects_non_canonical_and_ambiguous_inputs(self):
        cases = {
            "duplicate key": b'{"a":1,"a":2}\n',
            "nan": b'{"a":NaN}\n',
            "infinity": b'{"a":Infinity}\n',
            "float": b'{"a":1.5}\n',
            "bom": b'\xef\xbb\xbf{"a":1}\n',
            "invalid utf-8": b'{"a":"\xff"}\n',
        }
        for name, data in cases.items():
            with self.subTest(name), self.assertRaises(rg.GraphError):
                rg.parse_json(data)
        for name, data in {
            "pretty printed": b'{\n  "a": 1\n}\n',
            "missing trailing LF": b'{"a":1}',
            "two trailing LF": b'{"a":1}\n\n',
            "unsorted": b'{"b":1,"a":2}\n',
            "spaces": b'{"a": 1}\n',
        }.items():
            with self.subTest(name), self.assertRaises(rg.GraphError):
                rg.parse_canonical(data)
        with self.assertRaises(rg.GraphError):
            rg.canonical_bytes({"a": float("nan")})

    def test_digest_forms(self):
        self.assertEqual(rg.digest_bytes(b""), "sha256:" + hashlib.sha256(b"").hexdigest())
        self.assertEqual(rg.raw_sha256(b""), hashlib.sha256(b"").hexdigest())


class SchemaValidatorTests(unittest.TestCase):
    def test_unsupported_keywords_are_rejected_not_ignored(self):
        for keyword in ("format", "dependentRequired", "patternProperties", "if"):
            with self.subTest(keyword), self.assertRaises(schema_check.SchemaError):
                schema_check.check_schema({"type": "object", keyword: {}})

    def test_booleans_are_not_integers_and_types_are_exact(self):
        schema = {"type": "integer"}
        with self.assertRaises(schema_check.ValidationError):
            schema_check.validate(True, schema)
        with self.assertRaises(schema_check.ValidationError):
            schema_check.validate(1, {"const": True})

    def test_every_release_schema_is_loadable(self):
        for kind in rg.SCHEMA_FILES:
            with self.subTest(kind):
                rg.load_schema(kind)


class ValidGraphTests(GraphTestCase):
    def test_valid_graph_verifies_and_reports_lock_digest(self):
        result = rg.verify_root(self.root)
        self.assertEqual(result["candidateLockDigest"], rg.digest_bytes((self.root / "candidate-lock.json").read_bytes()))
        self.assertEqual(result["artifacts"], len(PAYLOAD))

    def test_command_line_verify_and_validate(self):
        script = str(ROOT / "scripts/lib/release_graph.py")
        verified = subprocess.run([sys.executable, script, "verify", "--root", str(self.root)],
                                  capture_output=True, text=True)
        self.assertEqual(verified.returncode, 0, verified.stderr)
        validated = subprocess.run([sys.executable, script, "validate", "--kind", "candidate-lock",
                                    str(self.root / "candidate-lock.json")], capture_output=True, text=True)
        self.assertEqual(validated.returncode, 0, validated.stderr)
        (self.root / "candidate-lock.json").write_bytes(b"{}\n")
        invalid = subprocess.run([sys.executable, script, "verify", "--root", str(self.root)],
                                 capture_output=True, text=True)
        self.assertEqual(invalid.returncode, 1)

    def test_writers_use_private_modes_and_refuse_overwrite(self):
        target = Path(self.temp.name) / "out.json"
        rg.write_atomic_exclusive(target, b"{}\n")
        self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o600)
        with self.assertRaises(rg.GraphError):
            rg.write_atomic_exclusive(target, b"{}\n")
        self.assertEqual([p.name for p in Path(self.temp.name).iterdir() if p.name.startswith(".out")], [])


class IndexMutationTests(GraphTestCase):
    def test_unknown_fields_in_every_graph_document(self):
        for name in ("artifact-index.json", "release-manifest.json", "candidate-lock.json"):
            with self.subTest(name):
                self.setUp()
                rewrite(self.root, name, lambda d: d.update(extra=True))
                self.assertGraphRejects("unknown property")

    def test_unknown_field_inside_an_artifact(self):
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"][0].update(signature="x"))
        self.assertGraphRejects("unknown property")

    def test_missing_artifact_file(self):
        (self.root / "config/images.env").unlink()
        self.assertGraphRejects("does not exist")

    def test_changed_payload_bytes_after_indexing(self):
        (self.root / "config/images.env").write_bytes(b"TELOS_CORE_IMAGE=other\n")
        self.assertGraphRejects("differ")

    def test_wrong_size_digest_and_platform(self):
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"][0].update(size=1))
        self.assertGraphRejects("size differs")
        self.setUp()
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"][0].update(sha256="0" * 64))
        self.assertGraphRejects("bytes differ")
        self.setUp()
        rewrite(self.root, "artifact-index.json",
                lambda d: next(a for a in d["artifacts"] if a["kind"] == "desktop").update(platform="any"))
        self.assertGraphRejects("linux-x86_64")
        self.setUp()
        rewrite(self.root, "artifact-index.json",
                lambda d: next(a for a in d["artifacts"] if a["kind"] == "oci-image").update(platform="linux-x86_64"))
        self.assertGraphRejects("linux/amd64")

    def test_wrong_kind_and_content_digest_rules(self):
        rewrite(self.root, "artifact-index.json", lambda d: next(
            a for a in d["artifacts"] if a["kind"] == "oci-image").update(contentDigest=None))
        self.assertGraphRejects("requires its manifest contentDigest")
        self.setUp()
        rewrite(self.root, "artifact-index.json", lambda d: next(
            a for a in d["artifacts"] if a["kind"] == "config").update(contentDigest=D))
        self.assertGraphRejects("only OCI images")
        self.setUp()
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"][0].update(kind="container"))
        self.assertGraphRejects("allowed set")

    def test_duplicate_unsorted_and_unsafe_paths(self):
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"].append(copy.deepcopy(d["artifacts"][0])))
        self.assertGraphRejects("duplicate")
        self.setUp()
        rewrite(self.root, "artifact-index.json", lambda d: d["artifacts"].reverse())
        self.assertGraphRejects("sorted")
        for bad in ("../escape", "/etc/passwd", "a//b", "a/./b", "with space", "back\\slash", "trailing/", ""):
            with self.subTest(bad):
                self.setUp()
                rewrite(self.root, "artifact-index.json", lambda d, bad=bad: d["artifacts"][0].update(path=bad))
                with self.assertRaises(rg.GraphError):
                    rg.verify_root(self.root)

    def test_symlinked_artifact_and_symlinked_parent(self):
        target = self.root / "config/images.env"
        target.unlink()
        os.symlink("/etc/hostname", target)
        self.assertGraphRejects("symbolic link")
        self.setUp()
        real = self.root / "images"
        moved = self.root / "images-real"
        real.rename(moved)
        os.symlink(moved, real)
        self.assertGraphRejects("symbolic link")

    def test_unindexed_file_and_special_file(self):
        write(self.root / "extra.txt", b"smuggled")
        self.assertGraphRejects("not named by the artifact index")
        self.setUp()
        os.mkfifo(self.root / "pipe")
        self.assertGraphRejects("special file")

    def test_downstream_nodes_cannot_be_payload(self):
        for reserved in ("candidate-lock.json", "evidence/envelope.json", "certification.json",
                         "images/core.sig", "SHA256SUMS", "signatures/x"):
            with self.subTest(reserved):
                self.setUp()
                rewrite(self.root, "artifact-index.json", lambda d, reserved=reserved: d["artifacts"][0].update(path=reserved))
                with self.assertRaises(rg.GraphError):
                    rg.verify_root(self.root)

    def test_non_canonical_graph_documents_are_rejected(self):
        rewrite(self.root, "artifact-index.json", lambda d: None, canonical=False)
        self.assertGraphRejects("canonical")


class CrossDocumentTests(GraphTestCase):
    def test_manifest_must_name_the_exact_index_bytes(self):
        rewrite(self.root, "release-manifest.json", lambda d: d.update(artifactIndexDigest=OTHER))
        self.assertGraphRejects("does not name the artifact index")

    def test_lock_must_name_index_and_manifest_bytes(self):
        rewrite(self.root, "candidate-lock.json", lambda d: d.update(artifactIndexDigest=OTHER))
        self.assertGraphRejects("candidate lock does not name the artifact index")
        self.setUp()
        rewrite(self.root, "candidate-lock.json", lambda d: d.update(releaseManifestDigest=OTHER))
        self.assertGraphRejects("does not name the release manifest")

    def test_source_and_version_must_agree_across_documents(self):
        rewrite(self.root, "candidate-lock.json", lambda d: d.update(sourceCommit="e" * 40))
        self.assertGraphRejects("source differs")
        self.setUp()
        rewrite(self.root, "candidate-lock.json", lambda d: d.update(version="0.1.0-beta.2"))
        self.assertGraphRejects("version differs")
        self.setUp()
        rewrite(self.root, "release-manifest.json", lambda d: d.update(sourceTag="v0.1.0-beta.9"))
        self.assertGraphRejects("sourceTag")

    def test_lock_artifact_map_must_equal_the_index(self):
        rewrite(self.root, "candidate-lock.json", lambda d: d["artifacts"].pop("config/images.env"))
        self.assertGraphRejects("artifact map differs")
        self.setUp()
        rewrite(self.root, "candidate-lock.json", lambda d: d["artifacts"].update({"candidate-lock.json": D}))
        self.assertGraphRejects("cannot appear in the candidate lock")

    def test_checksum_file_must_match(self):
        (self.root / "SHA256SUMS").write_bytes(b"0" * 64 + b"  config/images.env\n")
        self.assertGraphRejects("SHA256SUMS")

    def test_version_format_is_enforced(self):
        for bad in ("1.0.0", "0.1.0-beta.0", "0.1.0-beta", "v0.1.0-beta.1", "0.1.0-rc.1"):
            with self.subTest(bad):
                self.setUp()
                rewrite(self.root, "release-manifest.json", lambda d, bad=bad: d.update(version=bad))
                with self.assertRaises(rg.GraphError):
                    rg.verify_root(self.root)


def gate_result(**overrides):
    result = {
        "schemaVersion": 1, "recordKind": "candidate", "gateId": "clean-install", "sourceCommit": COMMIT,
        "candidateLockDigest": LOCK, "status": "passed", "reason": "all journey assertions passed",
        "subjectFile": "subject.json",
        "attachments": [{"path": "report.txt", "sha256": "1" * 64, "kind": "report"},
                        {"path": "subject.json", "sha256": "2" * 64, "kind": "subject"}],
        "toolVersions": {"python3": "3.14.4", "restic": "0.18.0"},
    }
    result.update(overrides)
    return result


class RecordTests(unittest.TestCase):
    def assertInvalid(self, kind, document, fragment=None):
        with self.assertRaises(rg.GraphError) as caught:
            rg.validate_record(kind, document)
        if fragment:
            self.assertIn(fragment, str(caught.exception))

    def test_valid_gate_result(self):
        rg.validate_record("gate-result", gate_result())

    def test_gate_result_rejections(self):
        self.assertInvalid("gate-result", gate_result(status="success"))
        self.assertInvalid("gate-result", gate_result(extra=1), "unknown property")
        self.assertInvalid("gate-result", gate_result(recordKind="final"))
        self.assertInvalid("gate-result", gate_result(candidateLockDigest=None), "requires candidateLockDigest")
        self.assertInvalid("gate-result", gate_result(reason=""))
        self.assertInvalid("gate-result", gate_result(reason="token=abcdefghijk"), "secret-looking")
        self.assertInvalid("gate-result", gate_result(toolVersions={"unknown-tool": "1"}), "unknown property")
        self.assertInvalid("gate-result", gate_result(toolVersions={}))
        self.assertInvalid("gate-result", gate_result(subjectFile=None), "requires a subjectFile")
        self.assertInvalid("gate-result", gate_result(subjectFile="missing.json"), "listed exactly once")
        self.assertInvalid("gate-result", gate_result(attachments=[
            {"path": "a.txt", "sha256": "1" * 64, "kind": "report"},
            {"path": "a.txt", "sha256": "1" * 64, "kind": "report"},
            {"path": "subject.json", "sha256": "2" * 64, "kind": "subject"}]), "duplicate")
        self.assertInvalid("gate-result", gate_result(attachments=[
            {"path": "../a", "sha256": "1" * 64, "kind": "report"},
            {"path": "subject.json", "sha256": "2" * 64, "kind": "subject"}]))
        self.assertInvalid("gate-result", gate_result(attachments=[
            {"path": "a.txt", "sha256": "1" * 64, "kind": "log"},
            {"path": "subject.json", "sha256": "2" * 64, "kind": "subject"}]))

    def test_failed_and_not_run_results_may_omit_a_subject(self):
        for status in ("failed", "not_run"):
            rg.validate_record("gate-result", gate_result(status=status, subjectFile=None, attachments=[]))

    def test_rehearsal_result_may_have_no_lock(self):
        rg.validate_record("gate-result", gate_result(recordKind="rehearsal", candidateLockDigest=None))

    def test_subject_identity_must_match_result(self):
        subject = {"schemaVersion": 1, "kind": "artifact", "sourceCommit": COMMIT, "candidateLockDigest": LOCK,
                   "selector": "artifact-index", "artifactDigest": D}
        rg.validate_record("observed-subject", subject)
        rg.validate_subject_for_result(gate_result(), subject)
        for field, value in (("sourceCommit", "f" * 40), ("candidateLockDigest", OTHER)):
            with self.subTest(field), self.assertRaises(rg.GraphError):
                rg.validate_subject_for_result(gate_result(), dict(subject, **{field: value}))

    def test_runtime_descriptor(self):
        runtime = {
            "schemaVersion": 1, "kind": "runtime", "sourceCommit": COMMIT, "candidateLockDigest": LOCK,
            "observedAt": "2026-10-13T12:00:00Z", "hostId": D,
            "bootId": "12345678-1234-1234-1234-123456789abc",
            "runtime": {"name": "podman", "version": "5.7.0", "rootless": False},
            "generationReceiptDigest": OTHER,
            "services": [{"name": "telos-client", "containerId": "ab" * 6, "imageId": "1" * 64, "manifestDigest": D},
                         {"name": "telos-core", "containerId": "cd" * 6, "imageId": "2" * 64, "manifestDigest": OTHER}],
        }
        rg.validate_record("observed-subject", runtime)
        self.assertInvalid("observed-subject", dict(runtime, runtime={"name": "podman", "version": "5", "rootless": True}))
        self.assertInvalid("observed-subject", dict(runtime, services=list(reversed(runtime["services"]))), "sorted")
        self.assertInvalid("observed-subject", dict(runtime, extra=1))

    def test_external_host_descriptor(self):
        host = {
            "schemaVersion": 1, "kind": "external-host", "sourceCommit": COMMIT, "candidateLockDigest": None,
            "observedAt": "2026-10-13T12:00:00Z", "purpose": "separate-host-restore",
            "hosts": [{"hostId": D, "identityMethod": "machine-id-sha256", "os": "Ubuntu", "osVersion": "24.04", "arch": "x86_64"},
                      {"hostId": "tester-ios-1", "identityMethod": "tester-assigned", "os": "iOS", "osVersion": "18", "arch": "arm64"}],
            "records": [{"path": "records/a.json", "sha256": "1" * 64}],
        }
        rg.validate_record("observed-subject", host)
        self.assertInvalid("observed-subject", dict(host, hosts=list(reversed(host["hosts"]))), "sorted")
        bad = copy.deepcopy(host)
        bad["hosts"][0]["identityMethod"] = "tester-assigned"
        self.assertInvalid("observed-subject", bad, "identityMethod")

    def test_evidence_index_ownership_and_ordering(self):
        def gate(gate_id, prefix):
            return {"gateId": gate_id, "envelope": {"path": f"{prefix}/envelope.json", "sha256": "1" * 64},
                    "log": {"path": f"{prefix}/log.txt", "sha256": "2" * 64}, "result": None, "files": []}
        index = {"schemaVersion": 1, "sourceCommit": COMMIT, "candidateLockDigest": LOCK,
                 "createdAt": "2026-10-13T12:00:00Z", "gates": [gate("a-gate", "a"), gate("b-gate", "b")]}
        rg.validate_record("evidence-index", index)
        self.assertInvalid("evidence-index", dict(index, gates=list(reversed(index["gates"]))), "sorted")
        clash = copy.deepcopy(index)
        clash["gates"][1]["log"]["path"] = "a/log.txt"
        self.assertInvalid("evidence-index", clash, "claimed by both")
        self.assertInvalid("evidence-index", dict(index, gates=[index["gates"][0], index["gates"][0]]), "duplicate")
        self.assertInvalid("evidence-index", dict(index, extra=1))

    def test_certification_excludes_itself(self):
        certification = {"schemaVersion": 1, "sourceCommit": COMMIT, "candidateLockDigest": LOCK,
                         "evidenceIndexDigest": D, "certifiedAt": "2026-10-13T12:00:00Z", "status": "passed",
                         "signerPrincipal": "release@example.test",
                         "signerFingerprint": "SHA256:" + "A" * 43, "requiredGates": {"clean-install": OTHER}}
        rg.validate_record("certification", certification)
        self.assertInvalid("certification", dict(certification, status="failed"))
        self.assertInvalid("certification", dict(certification, requiredGates={"beta-certification": D}), "its own")
        self.assertInvalid("certification", dict(certification, requiredGates={}))

    def test_guided_record(self):
        record = {"schemaVersion": 1, "sourceCommit": COMMIT, "candidateLockDigest": LOCK, "matrix": "desktop",
                  "platform": "linux-x86_64", "artifactDigest": D, "hostId": OTHER, "os": "Ubuntu",
                  "osVersion": "24.04", "applicationVersion": VERSION, "deviceModel": "reference-desktop",
                  "testerPrincipal": "tester@example.test", "startedAt": "2026-10-13T12:00:00Z",
                  "finishedAt": "2026-10-13T12:30:00Z",
                  "assertions": [{"id": "install", "answer": "pass", "notes": "installed"},
                                 {"id": "restart", "answer": "not_run", "notes": ""}],
                  "attachments": []}
        rg.validate_record("guided-smoke", record)
        self.assertInvalid("guided-smoke", dict(record, finishedAt="2026-10-13T11:00:00Z"), "earlier")
        self.assertInvalid("guided-smoke", dict(record, overall="passed"), "unknown property")
        self.assertInvalid("guided-smoke", dict(record, assertions=[record["assertions"][0]] * 2), "duplicate")
        self.assertInvalid("guided-smoke", dict(record, assertions=[{"id": "x", "answer": "pass", "notes": "password: hunter2hunter2"}]), "secret-looking")
        self.assertInvalid("guided-smoke", dict(record, assertions=[{"id": "x", "answer": "yes", "notes": ""}]))
        self.assertInvalid("guided-smoke", dict(record, assertions=[{"id": "x", "answer": "pass", "notes": "n" * 1001}]))
        self.assertInvalid("guided-smoke", dict(record, hostId="reference-1"), "machine-derived")

    def test_generation_manifest_has_no_downstream_identity(self):
        generation = {"schemaVersion": 1, "version": VERSION, "sourceCommit": COMMIT,
                      "artifactIndexSubset": [{"path": "config/images.env", "sha256": "1" * 64}],
                      "images": {"telos-core": D}, "migrations": {"min": 1, "current": 23, "max": 23, "checksumSetDigest": OTHER},
                      "predecessorIds": [], "rollbackCompatibleThrough": 20}
        rg.validate_record("generation-manifest", generation)
        self.assertInvalid("generation-manifest", dict(generation, candidateLockDigest=LOCK), "unknown property")
        self.assertInvalid("generation-manifest", dict(generation, migrations=dict(generation["migrations"], current=30)), "min <= current")
        self.assertInvalid("generation-manifest", dict(generation, rollbackCompatibleThrough=24), "cannot exceed")
        self.assertInvalid("generation-manifest", dict(generation, artifactIndexSubset=[{"path": "generation.json", "sha256": "1" * 64}]))

    def test_installed_receipt_is_separate_from_the_payload(self):
        receipt = {"schemaVersion": 1, "candidateLockDigest": LOCK, "generationManifestDigest": D,
                   "generationPath": "/srv/telos/generations/" + "b" * 64, "installedAt": "2026-10-13T12:00:00Z"}
        rg.validate_record("installed-generation", receipt)
        self.assertInvalid("installed-generation", dict(receipt, generationPath="relative/path"))
        self.assertInvalid("installed-generation", dict(receipt, generationPath="/srv//telos"), "normalized")

    def test_artifact_identity_and_review(self):
        identity = {"schemaVersion": 1, "sourceCommit": COMMIT, "version": VERSION, "platform": "linux-x86_64",
                    "size": 10, "sha256": "1" * 64, "workflowRunId": 1, "workflowRunAttempt": 1}
        rg.validate_record("artifact-identity", identity)
        self.assertInvalid("artifact-identity", dict(identity, platform="windows-x86_64"))
        self.assertInvalid("artifact-identity", dict(identity, workflowRunAttempt=0))
        review = {"schemaVersion": 1, "candidateLockDigest": LOCK, "evidenceIndexDigest": D,
                  "certificationDigest": OTHER, "reviewerPrincipal": "reviewer@example.test",
                  "reviewedAt": "2026-10-13T12:00:00Z", "decision": "approved"}
        rg.validate_record("release-review", review)
        self.assertInvalid("release-review", dict(review, decision="rejected"))


class DrillRecordTests(unittest.TestCase):
    def target(self, **overrides):
        value = {"schemaVersion": 1, "origin": "https://node.example.net", "nodePath": "/var/lib/telos",
                 "hostId": D, "sshTarget": "root@node.example.net", "sshConfigFile": "/home/op/.ssh/telos-config",
                 "credentialsFile": "/home/op/telos.creds", "restoreTargetFile": None,
                 "predecessorGeneration": None, "capacityTargetFile": None, "confirmation": None}
        value.update(overrides)
        return value

    def assertInvalid(self, kind, document, fragment=None):
        with self.assertRaises(rg.GraphError) as caught:
            rg.validate_record(kind, document)
        if fragment:
            self.assertIn(fragment, str(caught.exception))

    def test_valid_drill_target_and_local_target(self):
        rg.validate_record("drill-target", self.target())
        rg.validate_record("drill-target", self.target(sshTarget=None, sshConfigFile=None))

    def test_drill_target_rejections(self):
        self.assertInvalid("drill-target", self.target(origin="http://node.example.net"))
        self.assertInvalid("drill-target", self.target(origin="https://10.1.2.3"), "IP literal")
        self.assertInvalid("drill-target", self.target(origin="https://user:pw@node.example.net"))
        self.assertInvalid("drill-target", self.target(sshTarget="-oProxyCommand=evil"))
        self.assertInvalid("drill-target", self.target(sshTarget="a b@host"))
        self.assertInvalid("drill-target", self.target(sshConfigFile=None), "together")
        self.assertInvalid("drill-target", self.target(sshTarget=None), "together")
        self.assertInvalid("drill-target", self.target(nodePath="/var/../etc"), "normalized")
        self.assertInvalid("drill-target", self.target(nodePath="relative/path"))
        self.assertInvalid("drill-target", self.target(hostId="raw-machine-id"))
        self.assertInvalid("drill-target", self.target(extra="x"), "unknown property")
        self.assertInvalid("drill-target", {k: v for k, v in self.target().items() if k != "credentialsFile"})

    def test_gate_inputs(self):
        rg.validate_record("gate-inputs", {"gateInputs": {"host-exposure": {"targetFile": "/secure/t.json"}}})
        self.assertInvalid("gate-inputs", {"gateInputs": {"host-exposure": {"targetFile": "relative"}}})
        self.assertInvalid("gate-inputs", {"gateInputs": {"Bad_Id": {"targetFile": "/x"}}})
        self.assertInvalid("gate-inputs", {"gateInputs": {"g": {"bad key": "/x"}}})
        self.assertInvalid("gate-inputs", {"gateInputs": {}, "extra": 1})
        self.assertInvalid("gate-inputs", {"gateInputs": {"g": {"targetFile": "/a/../b"}}})


class ResultDirectoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.dir = Path(self.temp.name)
        write(self.dir / "report.txt", b"report")
        write(self.dir / "subject.json", b"{}\n")
        result = gate_result(attachments=[
            {"path": "report.txt", "sha256": rg.raw_sha256(b"report"), "kind": "report"},
            {"path": "subject.json", "sha256": rg.raw_sha256(b"{}\n"), "kind": "subject"}])
        (self.dir / "result.json").write_bytes(rg.canonical_bytes(result))

    def test_valid_directory(self):
        rg.verify_result_directory(self.dir, "result.json")

    def test_changed_unlisted_and_symlinked_files(self):
        (self.dir / "report.txt").write_bytes(b"tampered")
        with self.assertRaisesRegex(rg.GraphError, "changed"):
            rg.verify_result_directory(self.dir, "result.json")
        (self.dir / "report.txt").write_bytes(b"report")
        write(self.dir / "stray.txt", b"x")
        with self.assertRaisesRegex(rg.GraphError, "unlisted"):
            rg.verify_result_directory(self.dir, "result.json")
        (self.dir / "stray.txt").unlink()
        os.symlink("report.txt", self.dir / "link.txt")
        with self.assertRaisesRegex(rg.GraphError, "symbolic link"):
            rg.verify_result_directory(self.dir, "result.json")


if __name__ == "__main__":
    unittest.main(argv=sys.argv)
