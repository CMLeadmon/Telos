#!/usr/bin/env python3
"""Import, sign, verify and certify: a complete, signed, candidate-bound evidence set or nothing.

Evidence comes from the real runner against a real (miniature) candidate; keys are ephemeral;
the tag is a real SSH-signed tag in a throwaway repository. Every mutation changes one dimension
of a valid bundle and must be refused for its own reason."""

import copy
import datetime
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
import evidence  # noqa: E402
import evidence_bundle as eb  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

RUNNER = str(ROOT / "scripts" / "run-gates.py")
PRODUCER = str(ROOT / "tests" / "fixtures" / "gates" / "fixture_producer.py")
CORE = "sha256:" + "1" * 64
CLIENT = "sha256:" + "2" * 64
PRINCIPAL = "release@example.test"


def gate(gate_id, kind, selector, scope="external", prerequisites=(), command=None, max_age=168):
    return {"id": gate_id, "phase": 4, "scope": scope, "required": True, "timeoutSeconds": 60,
            "command": command or ["python3", PRODUCER], "prerequisites": list(prerequisites),
            "subjectKind": kind, "subjectSelector": selector, "maxAgeHours": max_age, "requiredPlatforms": []}


def keygen(directory, name):
    path = Path(directory) / name
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ephemeral", "-f", str(path)], check=True)
    return path, Path(str(path) + ".pub").read_text().split()


class BundleFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.shared = Path(tempfile.mkdtemp(prefix="evidence-bundle-shared-"))
        os.chmod(cls.shared, 0o700)
        cls.tag_key, cls.tag_public = keygen(cls.shared, "tag")
        cls.key, cls.public = keygen(cls.shared, "evidence")
        cls.other_key, cls.other_public = keygen(cls.shared, "other")
        cls.repo = cls.shared / "repo"
        cls.repo.mkdir()
        git = lambda *a: subprocess.run(["git", "-C", str(cls.repo), *a], check=True, capture_output=True, text=True)
        git("init", "-q", "-b", "main")
        git("config", "user.email", "t@example.invalid")
        git("config", "user.name", "T")
        (cls.repo / "release").mkdir()
        (cls.repo / "release" / "telos-tag-signers").write_text(f'{PRINCIPAL} namespaces="git" {cls.tag_public[0]} {cls.tag_public[1]}\n')
        (cls.repo / "README").write_text("x\n")
        git("add", "-A")
        git("commit", "-q", "-m", "fixture")
        cls.commit = git("rev-parse", "HEAD").stdout.strip()
        git("-c", "gpg.format=ssh", "-c", f"user.signingkey={cls.tag_key}", "tag", "-s", "v0.1.0-beta.1", "-m", "release")
        cls.signers = cls.shared / "evidence-signers"
        cls.signers.write_text(f'{PRINCIPAL} namespaces="{signing.NAMESPACES["evidence"]},{signing.NAMESPACES["certification"]}" {cls.public[0]} {cls.public[1]}\n')
        cls.catalog = {"schemaVersion": 2, "gates": [
            gate("fix-gate", "fixture", "completed-output", scope="local", command=["python3", "-c", "print('fixture verified')"]),
            gate("art-gate", "artifact", "artifact-index", prerequisites=["fix-gate"]),
            gate("run-gate", "runtime", "observed-runtime", prerequisites=["fix-gate"], max_age=72),
            gate("host-gate", "external-host", "observed-host", prerequisites=["fix-gate"]),
            gate("beta-certification", "artifact", "artifact-index", command=["bash", "scripts/certify-beta.sh"],
                 prerequisites=["fix-gate", "art-gate", "run-gate", "host-gate"]),
        ]}
        cls.catalog_path = cls.shared / "catalog.json"
        cls.catalog_path.write_text(json.dumps(cls.catalog))
        cls.candidate = cls.make_candidate(cls.shared / "candidate")
        cls.lock = cls.candidate / "candidate-lock.json"

    @classmethod
    def tearDownClass(cls):
        eb.make_writable(cls.shared)
        shutil.rmtree(cls.shared, True)

    @classmethod
    def make_candidate(cls, root):
        payload = {
            "config/images.env": (b"TELOS_CORE_IMAGE=example\n", "config", "any", None),
            "images/telos-core.oci.tar": (b"core", "oci-image", "linux/amd64", CORE),
            "images/telos-client.oci.tar": (b"client", "oci-image", "linux/amd64", CLIENT),
        }
        generation = {"schemaVersion": 1, "version": "0.1.0-beta.1", "sourceCommit": cls.commit,
                      "artifactIndexSubset": [{"path": "config/images.env", "sha256": rg.raw_sha256(payload["config/images.env"][0])}],
                      "images": {"telos-client": CLIENT, "telos-core": CORE},
                      "migrations": {"min": 1, "current": 23, "max": 23, "checksumSetDigest": "sha256:" + "3" * 64},
                      "predecessorIds": [], "rollbackCompatibleThrough": 20}
        payload["generation.json"] = (rg.canonical_bytes(generation), "generation", "any", None)
        descriptors = []
        for relative, (data, kind, plat, content) in payload.items():
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
            descriptors.append({"path": relative, "kind": kind, "platform": plat, "contentDigest": content})
        rg.write_atomic_exclusive(root / "artifact-index.json", rg.canonical_bytes(rg.build_index(root, {"artifacts": descriptors})))
        manifest = rg.build_manifest(root, {"version": "0.1.0-beta.1", "sourceCommit": cls.commit, "sourceTag": "v0.1.0-beta.1",
                                            "sourceDateEpoch": 1787234400, "imagesLockDigest": CORE, "migrationManifestDigest": CLIENT,
                                            "buildInputs": {"npmLock": CORE, "cargoLock": CLIENT, "goSum": CORE}})
        rg.write_atomic_exclusive(root / "release-manifest.json", rg.canonical_bytes(manifest))
        rg.write_atomic_exclusive(root / "candidate-lock.json", rg.canonical_bytes(rg.build_candidate_lock(root)))
        rg.write_atomic_exclusive(root / "SHA256SUMS", rg.checksum_lines(root), 0o644)
        return root

    # ---- per-test evidence
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="evidence-bundle-"))
        self.addCleanup(lambda: (eb.make_writable(self.dir), shutil.rmtree(self.dir, True)))
        self.counter = 0
        self.sources = self.produce()

    def run_runner(self, gates, env=None, scope="external", prior=(), expect=0):
        self.counter += 1
        catalog = self.dir / f"catalog-{self.counter}.json"
        catalog.write_text(json.dumps(self.catalog))
        out = self.dir / f"evidence-{self.counter}"
        arguments = [sys.executable, "-B", RUNNER, "--catalog", str(catalog), "--scope", scope, "--candidate-lock", str(self.lock), "--evidence-dir", str(out)]
        for g in gates:
            arguments += ["--gate", g]
        for p in prior:
            arguments += ["--prior-evidence-dir", str(p)]
        done = subprocess.run(arguments, capture_output=True, text=True, env=dict(os.environ, **(env or {})), cwd=ROOT)
        self.assertEqual(done.returncode, expect, done.stdout + done.stderr)
        return out

    def produce(self):
        local = self.run_runner(["fix-gate"], scope="local")
        services = json.dumps([{"name": "telos-client", "containerId": "ab" * 6, "imageId": "9" * 64, "manifestDigest": CLIENT},
                               {"name": "telos-core", "containerId": "cd" * 6, "imageId": "8" * 64, "manifestDigest": CORE}])
        artifact = self.run_runner(["art-gate"], env={"FIXTURE_MODE": "artifact-ok", "FIXTURE_KIND": "artifact", "FIXTURE_SELECTOR": "artifact-index"}, prior=[local])
        runtime = self.run_runner(["run-gate"], env={"FIXTURE_MODE": "artifact-ok", "FIXTURE_KIND": "runtime", "FIXTURE_SERVICES": services}, prior=[local])
        host = self.run_runner(["host-gate"], env={"FIXTURE_MODE": "artifact-ok", "FIXTURE_KIND": "external-host"}, prior=[local])
        return [local, artifact, runtime, host]

    def bundle_path(self, name="bundle"):
        return self.dir / name

    def import_bundle(self, sources=None, name="bundle", **kwargs):
        return eb.import_bundle(self.lock, self.catalog_path, sources or self.sources, self.bundle_path(name), **kwargs)

    def verify(self, name="bundle", signers=None, principal=PRINCIPAL, **kwargs):
        return eb.verify_bundle(self.lock, self.catalog_path, self.bundle_path(name), signers or self.signers, principal, **kwargs)

    def sign(self, name="bundle", key=None, principal=PRINCIPAL):
        return eb.sign_bundle(self.lock, self.catalog_path, self.bundle_path(name), self.signers, key or self.key, principal)

    def signed(self, name="bundle"):
        self.import_bundle(name=name)
        self.sign(name)
        return self.bundle_path(name)

    def certify(self, name="bundle", out="out", **kwargs):
        defaults = dict(candidate=self.candidate, catalog_path=self.catalog_path, bundle_dir=self.bundle_path(name), signers=self.signers,
                        out_dir=self.dir / out, repo=self.repo, key=self.key, principal=PRINCIPAL)
        defaults.update(kwargs)
        return eb.certify(**defaults)

    def edit_envelope(self, directory, gate_id, **changes):
        path = Path(directory) / f"{gate_id}.json"
        data = json.loads(path.read_text())
        data.update(changes)
        path.write_text(json.dumps(data))


class HappyPathTests(BundleFixture):
    def test_the_complete_set_imports_signs_verifies_and_certifies(self):
        summary = self.import_bundle()
        self.assertEqual(summary["gates"], 4)
        self.sign()
        verified = self.verify()
        self.assertEqual(verified["signerPrincipal"], PRINCIPAL)
        self.assertRegex(verified["signerFingerprint"], r"^SHA256:")
        document = self.certify()
        self.assertEqual(document["status"], "passed")
        self.assertEqual(sorted(document["requiredGates"]), ["art-gate", "fix-gate", "host-gate", "run-gate"])
        out = self.dir / "out"
        data = (out / eb.CERTIFICATION_NAME).read_bytes()
        rg.load_record("certification", data)
        signing.verify_signature(self.signers, PRINCIPAL, signing.NAMESPACES["certification"], data, (out / eb.CERTIFICATION_SIGNATURE).read_bytes())
        self.assertFalse((out / eb.DIAGNOSTIC_NAME).exists())

    def test_the_bundle_is_read_only_and_indexed_canonically(self):
        self.import_bundle()
        bundle = self.bundle_path()
        for path in bundle.rglob("*"):
            if path.is_file():
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o444, path)
        index = rg.load_record("evidence-index", (bundle / eb.INDEX_NAME).read_bytes())
        self.assertEqual([g["gateId"] for g in index["gates"]], ["art-gate", "fix-gate", "host-gate", "run-gate"])
        self.assertIsNone(next(g for g in index["gates"] if g["gateId"] == "fix-gate")["result"])
        self.assertIsNotNone(next(g for g in index["gates"] if g["gateId"] == "art-gate")["result"])

    def test_the_terminal_aggregate_is_never_part_of_the_evidence(self):
        self.import_bundle()
        self.assertFalse((self.bundle_path() / eb.GATES_DIR / "beta-certification.json").exists())

    def test_importing_the_same_evidence_twice_gives_the_same_set(self):
        a = self.import_bundle(name="a")
        b = self.import_bundle(name="b")
        self.assertEqual(a["gates"], b["gates"])


class ImportRefusalTests(BundleFixture):
    def refuses(self, fragment, **kwargs):
        with self.assertRaisesRegex(eb.BundleError, fragment):
            self.import_bundle(**kwargs)
        self.assertFalse(self.bundle_path().exists() and any(self.bundle_path().iterdir()), "a refused import must leave nothing behind")

    def test_a_missing_gate(self):
        self.refuses("missing for: host-gate", sources=self.sources[:3])

    def test_an_extra_gate(self):
        extra = self.dir / "extra"
        extra.mkdir()
        shutil.copy(self.sources[0] / "fix-gate.json", extra / "fix-gate.json")
        shutil.copy(self.sources[0] / "fix-gate.log", extra / "fix-gate.log")
        catalog = json.loads(self.catalog_path.read_text())
        # an envelope for a gate that exists in the catalog but is not required
        catalog["gates"].append(dict(catalog["gates"][0], id="optional-gate", required=False))
        path = self.dir / "catalog-optional.json"
        path.write_text(json.dumps(catalog))
        envelope = json.loads((extra / "fix-gate.json").read_text())
        envelope["gateId"] = "optional-gate"
        (extra / "optional-gate.json").write_text(json.dumps(envelope))
        shutil.copy(extra / "fix-gate.log", extra / "optional-gate.log")
        (extra / "fix-gate.json").unlink()
        (extra / "fix-gate.log").unlink()
        with self.assertRaisesRegex(eb.BundleError, "outside the required set"):
            eb.import_bundle(self.lock, path, [*self.sources, extra], self.bundle_path())

    def test_a_duplicate_gate_across_directories(self):
        with self.assertRaisesRegex(eb.BundleError, "duplicate"):
            self.import_bundle(sources=[*self.sources, self.sources[0]])

    def test_a_failed_gate(self):
        self.edit_envelope(self.sources[1], "art-gate", status="failed", exitCode=1)
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_a_not_run_gate(self):
        failed = self.run_runner(["art-gate"], env={"FIXTURE_MODE": "not-run", "FIXTURE_KIND": "artifact"}, prior=[self.sources[0]], expect=1)
        with self.assertRaisesRegex(eb.BundleError, "status is not_run"):
            self.import_bundle(sources=[self.sources[0], failed, self.sources[2], self.sources[3]])

    def test_evidence_for_another_candidate(self):
        other = self.shared / "other-candidate"
        if not other.exists():
            self.__class__.make_candidate_other = None
        wrong = json.loads((self.sources[0] / "fix-gate.json").read_text())
        wrong["candidateLockDigest"] = "sha256:" + "9" * 64
        (self.sources[0] / "fix-gate.json").write_text(json.dumps(wrong))
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_an_altered_log(self):
        with open(self.sources[0] / "fix-gate.log", "ab") as handle:
            handle.write(b"extra line\n")
        with self.assertRaisesRegex(eb.BundleError, "log differs"):
            self.import_bundle()

    def test_an_altered_result_attachment(self):
        subject = self.sources[1] / "art-gate.d" / "subject.json"
        subject.write_bytes(subject.read_bytes() + b" ")
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_an_unlisted_file_in_a_producer_output(self):
        (self.sources[1] / "art-gate.d" / "stray.txt").write_text("not declared")
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_expired_evidence(self):
        with self.assertRaisesRegex(eb.BundleError, "older than 72 hours"):
            self.import_bundle(now=eb.now_utc() + datetime.timedelta(hours=100))

    def test_evidence_finished_in_the_future(self):
        with self.assertRaisesRegex(eb.BundleError, "future"):
            self.import_bundle(now=eb.now_utc() - datetime.timedelta(days=2))

    def test_a_dependency_that_finished_after_its_dependent_started(self):
        envelope = json.loads((self.sources[0] / "fix-gate.json").read_text())
        late = (eb.parse_stamp(envelope["finishedAt"]) + datetime.timedelta(minutes=30)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.edit_envelope(self.sources[0], "fix-gate", finishedAt=late)
        with self.assertRaisesRegex(eb.BundleError, "finished after its dependent"):
            self.import_bundle(now=eb.now_utc() + datetime.timedelta(hours=1))

    def test_a_rehearsal_result_is_not_candidate_evidence(self):
        rehearsal = self.run_runner(["art-gate"], env={"FIXTURE_MODE": "rehearsal", "FIXTURE_KIND": "artifact"}, prior=[self.sources[0]]) \
            if False else None
        result = self.sources[1] / "art-gate.d" / "result.json"
        document = json.loads(result.read_text())
        document["recordKind"] = "rehearsal"
        result.write_bytes(rg.canonical_bytes(document))
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_a_secret_in_an_attachment(self):
        # the producer's own output is rewritten with a key block and re-hashed, as a careless producer would
        directory = self.sources[1] / "art-gate.d"
        result = json.loads((directory / "result.json").read_text())
        attachment = directory / "notes.txt"
        attachment.write_text("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n")
        result["attachments"].append({"path": "notes.txt", "sha256": rg.raw_sha256(attachment.read_bytes()), "kind": "report"})
        result["attachments"].sort(key=lambda a: a["path"])
        (directory / "result.json").write_bytes(rg.canonical_bytes(result))
        with self.assertRaisesRegex(eb.BundleError, "secret-looking"):
            self.import_bundle()

    def test_a_symlink_in_the_source(self):
        os.chmod(self.sources[1] / "art-gate.d", 0o755)
        os.symlink("/etc/hostname", self.sources[1] / "art-gate.d" / "link")
        with self.assertRaises(eb.BundleError):
            self.import_bundle()

    def test_a_non_empty_bundle_directory(self):
        bundle = self.bundle_path()
        bundle.mkdir()
        (bundle / "x").write_text("x")
        with self.assertRaisesRegex(eb.BundleError, "empty or absent"):
            self.import_bundle()

    def test_a_v1_catalog_cannot_certify(self):
        path = self.dir / "v1.json"
        path.write_text(json.dumps({"schemaVersion": 1, "gates": []}))
        with self.assertRaises(eb.BundleError):
            eb.import_bundle(self.lock, path, self.sources, self.bundle_path())


class VerifyRefusalTests(BundleFixture):
    def test_unsigned(self):
        self.import_bundle()
        with self.assertRaisesRegex(eb.BundleError, "not signed"):
            self.verify()

    def test_signed_by_a_key_that_is_not_enrolled(self):
        self.import_bundle()
        with self.assertRaisesRegex(eb.BundleError, "enrolled principal"):
            self.sign(key=self.other_key)

    def test_an_unauthorized_principal(self):
        self.signed()
        with self.assertRaisesRegex(eb.BundleError, "enrolled principal"):
            self.verify(principal="intruder@example.test")

    def test_a_signature_made_for_another_purpose_is_refused(self):
        self.import_bundle()
        bundle = self.bundle_path()
        wrong = signing.sign_file(self.key, signing.NAMESPACES["certification"], bundle / eb.INDEX_NAME)
        os.chmod(bundle, 0o755)
        (bundle / eb.SIGNATURE_NAME).write_bytes(wrong)
        os.chmod(bundle, 0o555)
        with self.assertRaisesRegex(eb.BundleError, "enrolled principal"):
            self.verify()

    def test_an_index_changed_after_signing(self):
        bundle = self.signed()
        os.chmod(bundle, 0o755)
        os.chmod(bundle / eb.INDEX_NAME, 0o644)
        index = json.loads((bundle / eb.INDEX_NAME).read_text())
        index["createdAt"] = "2026-01-01T00:00:00Z"
        (bundle / eb.INDEX_NAME).write_bytes(rg.canonical_bytes(index))
        with self.assertRaises(eb.BundleError):
            self.verify()

    def test_a_file_changed_after_indexing(self):
        bundle = self.signed()
        victim = bundle / eb.GATES_DIR / "fix-gate.log"
        os.chmod(victim, 0o644)
        victim.write_bytes(victim.read_bytes() + b"x")
        with self.assertRaisesRegex(eb.BundleError, "changed after it was indexed"):
            self.verify()

    def test_an_unindexed_file_in_the_bundle(self):
        bundle = self.signed()
        os.chmod(bundle, 0o755)
        (bundle / "stray").write_text("x")
        with self.assertRaisesRegex(eb.BundleError, "unindexed file"):
            self.verify()

    def test_a_second_signature_is_refused(self):
        self.signed()
        with self.assertRaisesRegex(eb.BundleError, "already signed"):
            self.sign()

    def test_a_group_readable_key_is_refused(self):
        self.import_bundle()
        loose = self.dir / "loose"
        shutil.copy(self.key, loose)
        os.chmod(loose, 0o644)
        with self.assertRaisesRegex(eb.BundleError, "enrolled principal|group or others"):
            self.sign(key=loose)

    def test_evidence_that_went_stale_since_signing(self):
        self.signed()
        with self.assertRaisesRegex(eb.BundleError, "older than"):
            self.verify(now=eb.now_utc() + datetime.timedelta(days=30))


class CertifyRefusalTests(BundleFixture):
    def refuses(self, fragment, **kwargs):
        with self.assertRaisesRegex((eb.BundleError, eb.Unavailable), fragment):
            self.certify(**kwargs)
        self.assertFalse((self.dir / "out" / eb.CERTIFICATION_NAME).exists())
        self.assertFalse((self.dir / "out" / eb.CERTIFICATION_SIGNATURE).exists())

    def test_a_certification_is_not_created_before_the_last_evidence_finished(self):
        self.signed()
        with self.assertRaisesRegex(eb.BundleError, "future"):
            self.certify(now=eb.now_utc() - datetime.timedelta(minutes=10))
        self.assertFalse((self.dir / "out" / eb.CERTIFICATION_NAME).exists())

    def test_a_dirty_candidate_graph(self):
        self.signed()
        copy_root = self.dir / "dirty-candidate"
        shutil.copytree(self.candidate, copy_root)
        (copy_root / "config" / "images.env").write_bytes(b"TELOS_CORE_IMAGE=other\n")
        self.refuses("candidate graph does not verify", candidate=copy_root)

    def test_a_candidate_other_than_the_one_the_evidence_names(self):
        self.signed()
        other = self.dir / "other-candidate"
        shutil.copytree(self.candidate, other)
        manifest = json.loads((other / "release-manifest.json").read_text())
        manifest["sourceDateEpoch"] += 1
        (other / "release-manifest.json").write_bytes(rg.canonical_bytes(manifest))
        lock = rg.build_candidate_lock(other)
        (other / "candidate-lock.json").write_bytes(rg.canonical_bytes(lock))
        (other / "SHA256SUMS").write_bytes(rg.checksum_lines(other))
        self.refuses("bound to another candidate|not the one the evidence", candidate=other)

    def test_an_unsigned_or_unknown_tag(self):
        self.signed()
        git = lambda *a: subprocess.run(["git", "-C", str(self.repo), *a], check=True, capture_output=True, text=True)
        git("tag", "-d", "v0.1.0-beta.1")
        try:
            git("tag", "v0.1.0-beta.1")   # lightweight, unsigned
            self.refuses("not signed by an enrolled signer")
        finally:
            git("tag", "-d", "v0.1.0-beta.1")
            git("-c", "gpg.format=ssh", "-c", f"user.signingkey={self.tag_key}", "tag", "-s", "v0.1.0-beta.1", "-m", "release")

    def test_no_enrolled_tag_signer_is_not_run(self):
        self.signed()
        bare = self.dir / "bare-repo"
        shutil.copytree(self.repo, bare)
        (bare / "release" / "telos-tag-signers").write_text("# none\n")
        self.refuses("enrolls no signer", repo=bare)

    def test_an_envelope_subject_that_does_not_match_the_frozen_bytes(self):
        self.import_bundle()
        bundle = self.bundle_path()
        os.chmod(bundle, 0o755)
        gates = bundle / eb.GATES_DIR
        os.chmod(gates, 0o755)
        envelope_path = gates / "art-gate.json"
        os.chmod(envelope_path, 0o644)
        data = json.loads(envelope_path.read_text())
        data["subject"]["digest"] = "sha256:" + "0" * 64
        envelope_path.write_text(json.dumps(data))
        # re-index and re-sign so only the subject is wrong
        index = json.loads((bundle / eb.INDEX_NAME).read_text())
        for entry in index["gates"]:
            if entry["gateId"] == "art-gate":
                entry["envelope"]["sha256"] = rg.raw_sha256(envelope_path.read_bytes())
        os.chmod(bundle / eb.INDEX_NAME, 0o644)
        (bundle / eb.INDEX_NAME).write_bytes(rg.canonical_bytes(index))
        self.sign()
        self.refuses("subject")

    def test_certifying_with_a_key_that_did_not_sign_the_evidence(self):
        self.signed()
        # an enrolled second key for the certification namespace only
        second, public = keygen(self.shared, f"second-{self.id().split('.')[-1]}")
        self.signers.write_text(self.signers.read_text() + f'second@example.test namespaces="{signing.NAMESPACES["certification"]}" {public[0]} {public[1]}\n')
        self.addCleanup(lambda: self.signers.write_text(f'{PRINCIPAL} namespaces="{signing.NAMESPACES["evidence"]},{signing.NAMESPACES["certification"]}" {self.public[0]} {self.public[1]}\n'))
        self.refuses("enrolled principal|did not sign", key=second)

    def test_a_second_certification_into_the_same_directory_is_refused(self):
        self.signed()
        self.certify()
        with self.assertRaisesRegex(eb.BundleError, "already holds a certification"):
            self.certify()

    def test_a_failed_certification_leaves_only_a_diagnostic(self):
        bundle = self.signed()
        out = self.dir / "diag"
        code = eb.main(["certify", "--candidate", str(self.candidate), "--catalog", str(self.catalog_path), "--evidence-bundle", str(bundle),
                        "--signers", str(self.signers), "--out-dir", str(out), "--repo", str(self.repo)])
        # no key in the environment: not_run, and nothing is written as a certification
        self.assertEqual(code, eb.EXIT_NOT_RUN)
        self.assertFalse((out / eb.CERTIFICATION_NAME).exists())
        os.environ["TELOS_EVIDENCE_SIGNING_KEY"] = str(self.key)
        os.environ["TELOS_EVIDENCE_SIGNING_PRINCIPAL"] = "intruder@example.test"
        self.addCleanup(lambda: [os.environ.pop(k, None) for k in ("TELOS_EVIDENCE_SIGNING_KEY", "TELOS_EVIDENCE_SIGNING_PRINCIPAL")])
        code = eb.main(["certify", "--candidate", str(self.candidate), "--catalog", str(self.catalog_path), "--evidence-bundle", str(bundle),
                        "--signers", str(self.signers), "--out-dir", str(out), "--repo", str(self.repo)])
        self.assertEqual(code, 1)
        self.assertFalse((out / eb.CERTIFICATION_NAME).exists())
        self.assertFalse((out / eb.CERTIFICATION_SIGNATURE).exists())
        diagnostic = json.loads((out / eb.DIAGNOSTIC_NAME).read_text())
        self.assertEqual(diagnostic["status"], "not_certified")



class CommandLineTests(BundleFixture):
    """The shell and Python entry points, exactly as an operator would run them."""

    def run_script(self, script, *arguments, env=None):
        environment = dict(os.environ, **(env or {}))
        for name in ("TELOS_EVIDENCE_SIGNING_KEY", "TELOS_EVIDENCE_SIGNING_PRINCIPAL"):
            environment.pop(name, None) if env is None or name not in env else None
        return subprocess.run([str(ROOT / "scripts" / script), *arguments], capture_output=True, text=True, env=environment, cwd=ROOT)

    def test_import_sign_verify_certify_end_to_end(self):
        sources = [arg for source in self.sources for arg in ("--source-dir", str(source))]
        done = self.run_script("import-evidence.py", "--candidate-lock", str(self.lock), "--catalog", str(self.catalog_path), *sources, "--bundle-dir", str(self.bundle_path()))
        self.assertEqual(done.returncode, 0, done.stderr)
        base = ("--candidate-lock", str(self.lock), "--catalog", str(self.catalog_path), "--bundle-dir", str(self.bundle_path()), "--signers", str(self.signers))
        unsigned = self.run_script("sign-evidence.sh", *base)
        self.assertEqual(unsigned.returncode, 3, "no signing key in the environment is not_run")
        env = {"TELOS_EVIDENCE_SIGNING_KEY": str(self.key), "TELOS_EVIDENCE_SIGNING_PRINCIPAL": PRINCIPAL}
        signed = self.run_script("sign-evidence.sh", *base, env=env)
        self.assertEqual(signed.returncode, 0, signed.stderr)
        verified = self.run_script("verify-evidence.sh", *base, "--principal", PRINCIPAL)
        self.assertEqual(verified.returncode, 0, verified.stderr)
        self.assertEqual(json.loads(verified.stdout)["signerPrincipal"], PRINCIPAL)
        out = self.dir / "cli-out"
        certified = self.run_script("certify-beta.sh", "--candidate", str(self.candidate), "--evidence-bundle", str(self.bundle_path()), "--catalog", str(self.catalog_path),
                                    "--signers", str(self.signers), "--out-dir", str(out), "--repo", str(self.repo), env=env)
        self.assertEqual(certified.returncode, 0, certified.stderr)
        self.assertEqual(json.loads(certified.stdout)["status"], "passed")
        self.assertTrue((out / eb.CERTIFICATION_NAME).is_file())

    def test_the_placeholder_shapes_still_report_not_run(self):
        for script in ("sign-evidence.sh", "certify-beta.sh"):
            done = self.run_script(script)
            self.assertEqual(done.returncode, 3, (script, done.stderr))

    def test_certify_without_a_key_is_not_run_and_writes_nothing(self):
        bundle = self.signed()
        out = self.dir / "no-key"
        done = self.run_script("certify-beta.sh", "--candidate", str(self.candidate), "--evidence-bundle", str(bundle), "--catalog", str(self.catalog_path),
                               "--signers", str(self.signers), "--out-dir", str(out), "--repo", str(self.repo))
        self.assertEqual(done.returncode, 3, done.stderr)
        self.assertFalse((out / eb.CERTIFICATION_NAME).exists())

if __name__ == "__main__":
    unittest.main()
