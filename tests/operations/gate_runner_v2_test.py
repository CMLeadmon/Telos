#!/usr/bin/env python3
"""Catalog v2 / producer-result contracts for the one gate runner (Phase 5 Task 4a).

Every test drives the real ``scripts/run-gates.py`` against a verified fixture
candidate and a configurable fixture producer, then inspects the runner-owned
envelopes. No signing key, network or container runtime is involved.
"""
import copy
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/lib"))
import evidence  # noqa: E402
import release_graph as rg  # noqa: E402

RUNNER = str(ROOT / "scripts/run-gates.py")
PRODUCER = str(ROOT / "tests/fixtures/gates/fixture_producer.py")
COMMIT = "a" * 40
CORE = "sha256:" + "1" * 64
CLIENT = "sha256:" + "2" * 64
ARTIFACT = "config/images.env"


def build_candidate(base):
    root = Path(base) / "candidate"
    payload = {
        "config/images.env": (b"TELOS_CORE_IMAGE=example\n", "config", "any", None),
        "images/telos-core.oci.tar": (b"core", "oci-image", "linux/amd64", CORE),
        "images/telos-client.oci.tar": (b"client", "oci-image", "linux/amd64", CLIENT),
    }
    generation = {
        "schemaVersion": 1, "version": "0.1.0-beta.1", "sourceCommit": COMMIT,
        "artifactIndexSubset": [{"path": "config/images.env", "sha256": rg.raw_sha256(payload["config/images.env"][0])}],
        "images": {"telos-client": CLIENT, "telos-core": CORE},
        "migrations": {"min": 1, "current": 23, "max": 23, "checksumSetDigest": "sha256:" + "3" * 64},
        "predecessorIds": [], "rollbackCompatibleThrough": 20,
    }
    payload["generation.json"] = (rg.canonical_bytes(generation), "generation", "any", None)
    descriptors = []
    for relative, (data, kind, platform_name, content) in payload.items():
        path = root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        descriptors.append({"path": relative, "kind": kind, "platform": platform_name, "contentDigest": content})
    index = rg.build_index(root, {"artifacts": descriptors})
    rg.write_atomic_exclusive(root / "artifact-index.json", rg.canonical_bytes(index))
    manifest = rg.build_manifest(root, {
        "version": "0.1.0-beta.1", "sourceCommit": COMMIT, "sourceTag": "v0.1.0-beta.1",
        "sourceDateEpoch": 1787234400, "imagesLockDigest": CORE, "migrationManifestDigest": CLIENT,
        "buildInputs": {"npmLock": CORE, "cargoLock": CLIENT, "goSum": CORE}})
    rg.write_atomic_exclusive(root / "release-manifest.json", rg.canonical_bytes(manifest))
    lock = rg.build_candidate_lock(root)
    rg.write_atomic_exclusive(root / "candidate-lock.json", rg.canonical_bytes(lock))
    return root


def gate(gate_id, kind="artifact", selector="artifact-index", scope="external", prerequisites=(), **extra):
    value = {
        "id": gate_id, "phase": 4, "scope": scope, "required": True, "timeoutSeconds": 30,
        "command": ["python3", PRODUCER], "prerequisites": list(prerequisites),
        "subjectKind": kind, "subjectSelector": selector, "maxAgeHours": 168, "requiredPlatforms": [],
    }
    value.update(extra)
    return value


def catalog(*gates, version=2):
    return {"schemaVersion": version, "gates": list(gates)}


class RunnerCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = build_candidate(self.base)
        self.lock = self.root / "candidate-lock.json"
        self.counter = 0

    def run_runner(self, cat, *extra, scope="external", mode="artifact-ok", env=None, lock=None):
        self.counter += 1
        catalog_path = self.base / f"catalog-{self.counter}.json"
        catalog_path.write_text(json.dumps(cat))
        evidence_dir = self.base / f"evidence-{self.counter}"
        environment = dict(os.environ, FIXTURE_MODE=mode, **(env or {}))
        done = subprocess.run(
            [sys.executable, "-B", RUNNER, "--catalog", str(catalog_path), "--scope", scope,
             "--candidate-lock", str(lock or self.lock), "--evidence-dir", str(evidence_dir), *extra],
            capture_output=True, text=True, env=environment, cwd=ROOT)
        self.evidence = evidence_dir
        return done

    def envelope(self, gate_id, directory=None):
        return json.loads(((directory or self.evidence) / f"{gate_id}.json").read_text())

    def assertGate(self, gate_id, status, fragment=None, directory=None):
        envelope = self.envelope(gate_id, directory)
        self.assertEqual(envelope["status"], status, envelope)
        if fragment:
            self.assertIn(fragment, envelope["reason"])
        return envelope


class CatalogV2Tests(RunnerCase):
    def test_catalog_v2_accepts_closed_fields(self):
        done = self.run_runner(catalog(gate("artifact-probe")))
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertGate("artifact-probe", "passed")

    def test_rejects_unknown_missing_and_invalid_v2_fields(self):
        base = gate("artifact-probe")
        cases = {
            "unknown field": dict(base, extra=True),
            "missing selector": {k: v for k, v in base.items() if k != "subjectSelector"},
            "missing max age": {k: v for k, v in base.items() if k != "maxAgeHours"},
            "missing platforms": {k: v for k, v in base.items() if k != "requiredPlatforms"},
            "zero max age": dict(base, maxAgeHours=0),
            "float max age": dict(base, maxAgeHours=1.5),
            "bool max age": dict(base, maxAgeHours=True),
            "selector for another kind": dict(base, subjectSelector="sourceCommit"),
            "unsafe artifact selector": dict(base, subjectSelector="artifact:../escape"),
            "unknown platform": dict(base, requiredPlatforms=["windows-x86_64"]),
            "macos platform": dict(base, requiredPlatforms=["macos-universal"]),
            "duplicate platform": dict(base, requiredPlatforms=["chrome", "chrome"]),
            "deferred gate id": dict(base, id="desktop-windows"),
            "runner-owned option": dict(base, command=["python3", PRODUCER, "--candidate-lock", "x"]),
            "result-out option": dict(base, command=["python3", PRODUCER, "--result-out", "x"]),
        }
        for name, bad in cases.items():
            with self.subTest(name):
                done = self.run_runner(catalog(bad))
                self.assertEqual(done.returncode, 2, (name, done.stdout, done.stderr))
                self.assertFalse(self.evidence.exists(), "an invalid catalog must create no evidence")

    def test_v1_fields_are_rejected_in_v1_catalogs_and_v1_stays_parseable(self):
        legacy = {k: v for k, v in gate("legacy", kind="fixture", selector="completed-output", scope="local").items()
                  if k not in ("subjectSelector", "maxAgeHours", "requiredPlatforms")}
        legacy["command"] = ["python3", "-c", "raise SystemExit(0)"]
        done = self.run_runner(catalog(legacy, version=1), scope="local", lock=self.lock)
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        done = self.run_runner(catalog(gate("v2-fields-in-v1"), version=1))
        self.assertEqual(done.returncode, 2)

    def test_catalog_version_three_is_rejected(self):
        self.assertEqual(self.run_runner(catalog(gate("artifact-probe"), version=3)).returncode, 2)

    def test_selector_kind_combinations(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("run_gates_under_test", RUNNER)
        runner = importlib.util.module_from_spec(spec)
        sys.path.insert(0, str(ROOT / "scripts/lib"))
        spec.loader.exec_module(runner)
        valid = [
            ("source", "sourceCommit"), ("fixture", "completed-output"), ("artifact", "artifact-index"),
            ("artifact", "artifact:images/telos-client.oci.tar"), ("runtime", "observed-runtime"),
            ("external-host", "observed-host"),
        ]
        for kind, selector in valid:
            with self.subTest((kind, selector)):
                runner.validate_catalog(catalog(gate("combo-probe", kind=kind, selector=selector)))
        for kind, selector in [("source", "observed-host"), ("fixture", "sourceCommit"), ("runtime", "artifact-index"),
                               ("external-host", "observed-runtime"), ("artifact", "completed-output"),
                               ("artifact", "artifact:"), ("artifact", "artifact:/abs")]:
            with self.subTest(("bad", kind, selector)), self.assertRaises(runner.GateError):
                runner.validate_catalog(catalog(gate("combo-probe", kind=kind, selector=selector)))


class ProducerResultTests(RunnerCase):
    def test_artifact_gate_verifies_indexed_bytes(self):
        done = self.run_runner(catalog(gate("frozen-config", selector=f"artifact:{ARTIFACT}")),
                               env={"FIXTURE_SELECTOR": f"artifact:{ARTIFACT}"})
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        envelope = self.assertGate("frozen-config", "passed")
        expected = "sha256:" + rg.file_identity(self.root, ARTIFACT)[1]
        self.assertEqual(envelope["subject"], {"kind": "artifact", "digest": expected})
        evidence.validate_envelope(envelope)
        # Evidence layout: runner envelope + log; the producer output stays in its own directory.
        self.assertTrue((self.evidence / "frozen-config.d" / "result.json").is_file())

    def test_one_byte_mutation_of_a_frozen_artifact_fails(self):
        (self.root / ARTIFACT).write_bytes(b"TELOS_CORE_IMAGE=examplf\n")
        done = self.run_runner(catalog(gate("frozen-config", selector=f"artifact:{ARTIFACT}")),
                               env={"FIXTURE_SELECTOR": f"artifact:{ARTIFACT}"})
        self.assertEqual(done.returncode, 1)
        self.assertGate("frozen-config", "not_run", "frozen candidate")

    def test_wrong_artifact_digest_in_descriptor_fails(self):
        self.run_runner(catalog(gate("frozen-config", selector=f"artifact:{ARTIFACT}")),
                        mode="artifact-wrong-digest", env={"FIXTURE_SELECTOR": f"artifact:{ARTIFACT}"})
        self.assertGate("frozen-config", "failed", "exact frozen artifact bytes")

    def test_selector_must_match_the_catalog(self):
        self.run_runner(catalog(gate("frozen-config", selector=f"artifact:{ARTIFACT}")),
                        env={"FIXTURE_SELECTOR": "artifact-index"})
        self.assertGate("frozen-config", "failed", "selector")

    def test_artifact_index_selector_binds_canonical_index_bytes(self):
        self.run_runner(catalog(gate("index-gate")))
        envelope = self.assertGate("index-gate", "passed")
        self.assertEqual(envelope["subject"]["digest"], rg.digest_bytes((self.root / "artifact-index.json").read_bytes()))

    def test_artifact_not_in_the_frozen_index_fails(self):
        self.run_runner(catalog(gate("ghost", selector="artifact:images/ghost.oci.tar")),
                        env={"FIXTURE_SELECTOR": "artifact:images/ghost.oci.tar"})
        with self.assertRaises(AssertionError):
            self.assertGate("ghost", "passed")

    def test_result_gate_id_mismatch_fails(self):
        done = self.run_runner(catalog(gate("artifact-probe")), mode="gate-id-mismatch")
        self.assertEqual(done.returncode, 1)
        self.assertGate("artifact-probe", "failed", "some-other-gate")

    def test_missing_result_is_not_success(self):
        done = self.run_runner(catalog(gate("artifact-probe")), mode="no-result-exit0")
        self.assertEqual(done.returncode, 1)
        self.assertGate("artifact-probe", "failed", "without writing its result")

    def test_exit_three_without_result_is_not_run(self):
        done = self.run_runner(catalog(gate("artifact-probe")), mode="no-result-exit3")
        self.assertEqual(done.returncode, 1)
        self.assertGate("artifact-probe", "not_run")

    def test_producer_crash_is_a_runner_owned_failure(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="crash")
        envelope = self.assertGate("artifact-probe", "failed", "command exited 1")
        self.assertEqual(envelope["exitCode"], 1)

    def test_status_must_agree_with_the_real_exit_code(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="status-mismatch")
        self.assertGate("artifact-probe", "failed", "contradicts exit code")

    def test_rehearsal_results_never_enter_candidate_evidence(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="rehearsal")
        self.assertGate("artifact-probe", "failed", "rehearsal")

    def test_result_bound_to_another_lock_fails(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="wrong-lock")
        self.assertGate("artifact-probe", "failed", "candidateLockDigest")

    def test_secret_looking_reason_is_rejected_before_it_reaches_evidence(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="secret-reason")
        envelope = self.assertGate("artifact-probe", "failed", "secret-looking")
        self.assertNotIn("hunter2", json.dumps(envelope))

    def test_unlisted_producer_files_fail(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="unlisted-file")
        self.assertGate("artifact-probe", "failed", "unlisted file")

    def test_not_run_result_is_recorded_as_not_run(self):
        self.run_runner(catalog(gate("artifact-probe")), mode="not-run")
        self.assertGate("artifact-probe", "not_run", "fixture prerequisite unavailable")

    def test_producer_receives_the_runner_gate_id(self):
        self.run_runner(catalog(gate("named-by-runner")))
        result = json.loads((self.evidence / "named-by-runner.d" / "result.json").read_text())
        self.assertEqual(result["gateId"], "named-by-runner")

    def test_source_only_lock_cannot_satisfy_a_producer(self):
        bare = self.base / "bare-lock.json"
        bare.write_text(json.dumps({"schemaVersion": 1, "sourceCommit": COMMIT}) + "\n")
        done = self.run_runner(catalog(gate("artifact-probe")), lock=bare)
        self.assertEqual(done.returncode, 1)
        self.assertGate("artifact-probe", "not_run", "verified frozen candidate")

    def test_tampered_candidate_graph_cannot_satisfy_a_producer(self):
        (self.root / "extra.txt").write_text("unindexed")
        self.run_runner(catalog(gate("artifact-probe")))
        self.assertGate("artifact-probe", "not_run", "verified frozen candidate")


SERVICES_OK = json.dumps([
    {"name": "telos-client", "containerId": "ab" * 6, "imageId": "9" * 64, "manifestDigest": CLIENT},
    {"name": "telos-core", "containerId": "cd" * 6, "imageId": "8" * 64, "manifestDigest": CORE},
])


class RuntimeSubjectTests(RunnerCase):
    def run_runtime(self, services):
        return self.run_runner(
            catalog(gate("runtime-probe", kind="runtime", selector="observed-runtime")),
            env={"FIXTURE_KIND": "runtime", "FIXTURE_SERVICES": services})

    def test_runtime_gate_accepts_distinct_config_id_and_manifest_digest(self):
        self.run_runtime(SERVICES_OK)
        envelope = self.assertGate("runtime-probe", "passed")
        self.assertEqual(envelope["subject"]["kind"], "runtime")
        evidence.validate_envelope(envelope)

    def test_runtime_gate_rejects_wrong_config_digest(self):
        services = json.loads(SERVICES_OK)
        services[1]["manifestDigest"] = "sha256:" + "7" * 64
        self.run_runtime(json.dumps(services))
        self.assertGate("runtime-probe", "failed", "differs from the candidate image lock")

    def test_runtime_gate_rejects_an_unlocked_service(self):
        services = json.loads(SERVICES_OK) + [
            {"name": "unexpected", "containerId": "ef" * 6, "imageId": "7" * 64, "manifestDigest": CORE}]
        self.run_runtime(json.dumps(services))
        self.assertGate("runtime-probe", "failed", "not in the candidate image lock")

    def test_runtime_gate_rejects_a_missing_locked_service(self):
        self.run_runtime(json.dumps(json.loads(SERVICES_OK)[:1]))
        self.assertGate("runtime-probe", "failed", "was not observed running")

    def test_runtime_subject_digest_is_the_canonical_descriptor(self):
        self.run_runtime(SERVICES_OK)
        envelope = self.envelope("runtime-probe")
        descriptor = (self.evidence / "runtime-probe.d" / "subject.json").read_bytes()
        self.assertEqual(envelope["subject"]["digest"], rg.digest_bytes(descriptor))


class HostSubjectTests(RunnerCase):
    def test_external_host_descriptor_purpose_must_name_the_gate(self):
        self.run_runner(catalog(gate("host-probe", kind="external-host", selector="observed-host")),
                        env={"FIXTURE_KIND": "external-host"})
        self.assertGate("host-probe", "passed")

    def test_descriptor_kind_must_match_the_gate(self):
        self.run_runner(catalog(gate("host-probe", kind="external-host", selector="observed-host")),
                        env={"FIXTURE_KIND": "artifact"})
        self.assertGate("host-probe", "failed", "kind")


class InputsAndSelectionTests(RunnerCase):
    def write_inputs(self, mapping):
        path = self.base / "inputs.json"
        path.write_text(json.dumps({"gateInputs": mapping}))
        return path

    def host_gate(self, gate_id="host-exposure"):
        return gate(gate_id, kind="external-host", selector="observed-host")

    def test_reserved_aggregate_cannot_be_scheduled(self):
        aggregate = gate("beta-certification")
        for arguments in (("--gate", "beta-certification"),):
            done = self.run_runner(catalog(gate("artifact-probe"), aggregate), *arguments)
            self.assertEqual(done.returncode, 2)
            self.assertIn("certify-beta.sh", done.stderr)
        done = self.run_runner(catalog(gate("artifact-probe"), aggregate))
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertFalse((self.evidence / "beta-certification.json").exists())
        self.assertTrue((self.evidence / "artifact-probe.json").exists())
        done = self.run_runner(catalog(gate("artifact-probe"), aggregate), "--phase", "9")
        self.assertFalse((self.evidence / "beta-certification.json").exists())

    def test_no_gate_may_depend_on_the_aggregate(self):
        aggregate = gate("beta-certification")
        done = self.run_runner(catalog(aggregate, gate("artifact-probe", prerequisites=["beta-certification"])))
        self.assertEqual(done.returncode, 2)

    def test_inputs_map_only_to_fixed_options(self):
        target = self.base / "target.json"
        target.write_text("{}")
        policy = self.base / "policy.json"
        policy.write_text("{}")
        log = self.base / "argv.json"
        inputs = self.write_inputs({"host-exposure": {"targetFile": str(target), "policyFile": str(policy)}})
        done = self.run_runner(catalog(self.host_gate()), "--inputs", str(inputs),
                               env={"FIXTURE_KIND": "external-host", "FIXTURE_ARGV_LOG": str(log)})
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        argv = json.loads(log.read_text())
        self.assertEqual(argv[argv.index("--target-file") + 1], str(target))
        self.assertEqual(argv[argv.index("--policy") + 1], str(policy))
        self.assertIn("--candidate-lock", argv)
        self.assertIn("--result-out", argv)

    def test_missing_required_input_is_not_run(self):
        inputs = self.write_inputs({"host-exposure": {"targetFile": str(self.base / "x")}})
        (self.base / "x").write_text("{}")
        done = self.run_runner(catalog(self.host_gate()), "--inputs", str(inputs), env={"FIXTURE_KIND": "external-host"})
        self.assertEqual(done.returncode, 1)
        self.assertGate("host-exposure", "not_run", "policyFile")
        done = self.run_runner(catalog(self.host_gate()), env={"FIXTURE_KIND": "external-host"})
        self.assertGate("host-exposure", "not_run", "missing required input")

    def test_unsafe_or_unknown_inputs_are_usage_errors_that_create_no_evidence(self):
        target = self.base / "target.json"
        target.write_text("{}")
        link = self.base / "link.json"
        os.symlink(target, link)
        (self.base / "dir").mkdir()
        cases = {
            "unknown gate": {"nonexistent": {"targetFile": str(target)}},
            "unknown key": {"host-exposure": {"targetFile": str(target), "command": "x"}},
            "relative path": {"host-exposure": {"targetFile": "target.json", "policyFile": str(target)}},
            "symlink": {"host-exposure": {"targetFile": str(link), "policyFile": str(target)}},
            "directory for a file input": {"host-exposure": {"targetFile": str(self.base / "dir"), "policyFile": str(target)}},
            "missing path": {"host-exposure": {"targetFile": str(self.base / "absent"), "policyFile": str(target)}},
            "non-normalized": {"host-exposure": {"targetFile": f"{self.base}/dir/../target.json", "policyFile": str(target)}},
            "gate without an input table": {"artifact-probe": {"targetFile": str(target)}},
            "non-object entry": {"host-exposure": "oops"},
        }
        for name, mapping in cases.items():
            with self.subTest(name):
                done = self.run_runner(catalog(self.host_gate(), gate("artifact-probe")),
                                       "--inputs", str(self.write_inputs(mapping)))
                self.assertEqual(done.returncode, 2, (name, done.stdout, done.stderr))
                self.assertFalse(self.evidence.exists())

    def test_inputs_manifest_must_be_closed_and_absolute(self):
        bad = self.base / "bad.json"
        for name, content in {"extra key": {"gateInputs": {}, "argv": ["rm"]}, "no wrapper": {"host-exposure": {}}}.items():
            with self.subTest(name):
                bad.write_text(json.dumps(content))
                self.assertEqual(self.run_runner(catalog(self.host_gate()), "--inputs", str(bad)).returncode, 2)
        done = self.run_runner(catalog(self.host_gate()), "--inputs", "relative.json")
        self.assertEqual(done.returncode, 2)

    def test_inputs_for_an_unselected_gate_are_refused(self):
        target = self.base / "target.json"
        target.write_text("{}")
        done = self.run_runner(catalog(self.host_gate(), gate("artifact-probe")), "--gate", "artifact-probe",
                               "--inputs", str(self.write_inputs({"host-exposure": {"targetFile": str(target), "policyFile": str(target)}})))
        self.assertEqual(done.returncode, 2)

    def test_inputs_require_a_v2_catalog(self):
        legacy = {k: v for k, v in gate("legacy", kind="fixture", selector="completed-output", scope="local").items()
                  if k not in ("subjectSelector", "maxAgeHours", "requiredPlatforms")}
        legacy["command"] = ["python3", "-c", "raise SystemExit(0)"]
        inputs = self.write_inputs({})
        self.assertEqual(self.run_runner(catalog(legacy, version=1), "--inputs", str(inputs), scope="local").returncode, 2)


class OrderingAndReuseTests(RunnerCase):
    def test_first_required_failure_blocks_every_remaining_gate(self):
        cat = catalog(gate("first-gate"), gate("second-gate"), gate("third-gate"))
        done = self.run_runner(cat, mode="crash")
        self.assertEqual(done.returncode, 1)
        self.assertGate("first-gate", "failed")
        self.assertGate("second-gate", "not_run", "blocked by failed required gate first-gate")
        self.assertGate("third-gate", "not_run", "blocked by failed required gate first-gate")

    def test_optional_failure_does_not_block_later_gates(self):
        cat = catalog(gate("optional-gate", required=False, command=["python3", "-c", "raise SystemExit(1)"],
                           subjectKind="fixture", subjectSelector="completed-output"), gate("second-gate"))
        self.run_runner(cat)
        self.assertGate("optional-gate", "failed")
        self.assertGate("second-gate", "passed")

    def test_fixture_subject_is_the_completed_output_digest(self):
        cat = catalog(gate("fixture-gate", kind="fixture", selector="completed-output",
                           command=["python3", "-c", "print('hello')"]))
        self.run_runner(cat)
        envelope = self.assertGate("fixture-gate", "passed")
        log = (self.evidence / "fixture-gate.log").read_bytes()
        self.assertEqual(envelope["subject"], {"kind": "fixture", "digest": rg.digest_bytes(log)})
        self.assertEqual(envelope["outputDigest"], rg.digest_bytes(log))

    def test_cross_scope_prerequisite_is_reused_once(self):
        local = gate("local-gate", scope="local")
        external = gate("external-gate", prerequisites=["local-gate"])
        cat = catalog(local, external)
        self.run_runner(cat, scope="local")
        local_dir = self.evidence
        self.assertGate("local-gate", "passed", directory=local_dir)
        marker = self.base / "ran-count"
        counting = gate("local-gate", scope="local", command=["python3", "-c",
                        f"import pathlib; p=pathlib.Path({str(marker)!r}); p.write_text(p.read_text()+'x' if p.exists() else 'x'); raise SystemExit(1)"])
        counting_catalog = catalog(counting, external)
        done = self.run_runner(counting_catalog, "--prior-evidence-dir", str(local_dir))
        self.assertEqual(done.returncode, 0, done.stdout + done.stderr)
        self.assertGate("external-gate", "passed")
        self.assertFalse((self.evidence / "local-gate.json").exists(), "a reused prerequisite is not rewritten")
        self.assertFalse(marker.exists(), "an external-scope run must not execute a local prerequisite")

    def test_missing_cross_scope_prerequisite_is_not_run_never_executed(self):
        cat = catalog(gate("local-gate", scope="local"), gate("external-gate", prerequisites=["local-gate"]))
        done = self.run_runner(cat)
        self.assertEqual(done.returncode, 1)
        self.assertFalse((self.evidence / "local-gate.json").exists())
        self.assertGate("external-gate", "not_run", "local-gate=unavailable")

    def test_failed_prior_prerequisite_is_not_reused(self):
        local = gate("local-gate", scope="local")
        external = gate("external-gate", prerequisites=["local-gate"])
        self.run_runner(catalog(local, external), scope="local", mode="crash")
        prior = self.evidence
        self.assertGate("local-gate", "failed", directory=prior)
        done = self.run_runner(catalog(local, external), "--prior-evidence-dir", str(prior))
        self.assertEqual(done.returncode, 1)
        self.assertGate("external-gate", "not_run", "prior evidence status is failed")

    def test_stale_prior_evidence_is_rejected_by_the_gate_age_limit(self):
        local = gate("local-gate", scope="local", maxAgeHours=1)
        external = gate("external-gate", prerequisites=["local-gate"])
        self.run_runner(catalog(local, external), scope="local")
        prior = self.evidence
        envelope_path = prior / "local-gate.json"
        envelope = json.loads(envelope_path.read_text())
        envelope["startedAt"] = "2020-01-01T00:00:00Z"
        envelope["finishedAt"] = "2020-01-01T00:00:01Z"
        envelope_path.write_text(json.dumps(envelope, sort_keys=True, separators=(",", ":")))
        done = self.run_runner(catalog(local, external), "--prior-evidence-dir", str(prior))
        self.assertEqual(done.returncode, 1)
        self.assertGate("external-gate", "not_run", "older than 1 hours")

    def test_tampered_prior_evidence_is_a_hard_error(self):
        local = gate("local-gate", scope="local")
        external = gate("external-gate", prerequisites=["local-gate"])
        self.run_runner(catalog(local, external), scope="local")
        prior = self.evidence
        (prior / "local-gate.log").write_bytes(b"tampered log")
        done = self.run_runner(catalog(local, external), "--prior-evidence-dir", str(prior))
        self.assertEqual(done.returncode, 2)
        self.assertIn("log differs", done.stderr)

    def test_prior_evidence_for_another_candidate_is_a_hard_error(self):
        local = gate("local-gate", scope="local")
        external = gate("external-gate", prerequisites=["local-gate"])
        self.run_runner(catalog(local, external), scope="local")
        prior = self.evidence
        other_lock = self.base / "other-lock.json"
        other_lock.write_text(json.dumps({"schemaVersion": 1, "sourceCommit": COMMIT}) + "\n")
        done = self.run_runner(catalog(local, external), "--prior-evidence-dir", str(prior), lock=other_lock)
        self.assertEqual(done.returncode, 2)
        self.assertIn("prior evidence", done.stderr)

    def test_duplicate_ids_across_current_and_prior_sets_are_refused(self):
        local = gate("local-gate", scope="local")
        external = gate("external-gate", prerequisites=["local-gate"])
        self.run_runner(catalog(local, external), scope="local")
        prior = self.evidence
        done = self.run_runner(catalog(local, external), "--scope", "local", "--prior-evidence-dir", str(prior), scope="local")
        self.assertNotEqual(done.returncode, 0)
        self.run_runner(catalog(local, external), scope="local")
        second_prior = self.evidence
        done = self.run_runner(catalog(local, external), "--prior-evidence-dir", str(prior),
                               "--prior-evidence-dir", str(second_prior))
        self.assertEqual(done.returncode, 2)
        self.assertIn("duplicate prior evidence", done.stderr)

    def test_prior_evidence_dir_requires_a_v2_catalog(self):
        legacy = {k: v for k, v in gate("legacy", kind="fixture", selector="completed-output", scope="local").items()
                  if k not in ("subjectSelector", "maxAgeHours", "requiredPlatforms")}
        legacy["command"] = ["python3", "-c", "raise SystemExit(0)"]
        (self.base / "prior").mkdir()
        done = self.run_runner(catalog(legacy, version=1), "--prior-evidence-dir", str(self.base / "prior"), scope="local")
        self.assertEqual(done.returncode, 2)


class ToolVersionPolicyTests(unittest.TestCase):
    def test_policy_adds_exactly_the_agreed_tools(self):
        added = {"restic", "k6", "syft", "trivy", "gitleaks", "skopeo", "promtool", "ssh-keygen",
                 "cargo", "rustc", "go", "buildah", "gh"}
        self.assertEqual(set(evidence.TOOL_VERSION_KEYS) - {"python", "bash", "sh", "python3", "node", "npm", "npx", "podman", "git"}, added)
        schema = json.loads((ROOT / "release/evidence-envelope.schema.json").read_text())
        self.assertEqual(set(schema["properties"]["toolVersions"]["properties"]), set(evidence.TOOL_VERSION_KEYS))
        evidence.load_schema(str(ROOT / "release/evidence-envelope.schema.json"))

    def test_arbitrary_tool_keys_are_still_rejected(self):
        envelope = {
            "schemaVersion": 2, "gateId": "probe", "status": "passed", "reason": "ok", "sourceCommit": COMMIT,
            "candidateLockDigest": CORE, "command": ["x"], "startedAt": "2026-10-13T00:00:00Z",
            "finishedAt": "2026-10-13T00:00:01Z", "exitCode": 0, "outputDigest": CORE,
            "toolVersions": {"made-up-tool": "1"}, "subject": {"kind": "fixture", "digest": CORE}}
        with self.assertRaises(evidence.EvidenceError):
            evidence.validate_envelope(envelope)
        envelope["toolVersions"] = {"restic": "0.18.0", "ssh-keygen": "OpenSSH_10"}
        evidence.validate_envelope(envelope)


class RepositoryCatalogTests(unittest.TestCase):
    def test_repository_catalog_is_v2_with_the_agreed_freshness_table(self):
        document = json.loads((ROOT / "ci/phase-gates.json").read_text())
        self.assertEqual(document["schemaVersion"], 2)
        by_id = {g["id"]: g for g in document["gates"]}
        for gate_id in ("runtime-hardening", "host-exposure", "separate-host-restore", "upgrade-rollback"):
            self.assertEqual(by_id[gate_id]["maxAgeHours"], 72, gate_id)
        self.assertEqual(by_id["candidate-supply-chain"]["maxAgeHours"], 24)
        for gate_id, entry in by_id.items():
            if gate_id not in ("runtime-hardening", "host-exposure", "separate-host-restore",
                               "upgrade-rollback", "candidate-supply-chain"):
                self.assertEqual(entry["maxAgeHours"], 168, gate_id)
        self.assertEqual(by_id["browser-matrix"]["requiredPlatforms"], [
            "chrome", "firefox", "edge", "webkit", "desktop-safari", "ios-safari", "android-chrome"])
        self.assertEqual(by_id["desktop-linux"]["requiredPlatforms"], ["linux-x86_64"])
        self.assertEqual(by_id["static-client-image"]["subjectSelector"], "artifact:images/telos-client.oci.tar")
        self.assertEqual(by_id["desktop-builds"]["subjectSelector"], "artifact-index")
        for entry in document["gates"]:
            self.assertFalse(set(entry["command"]) & {"--candidate-lock", "--result-out"}, entry["id"])

    def test_catalog_selectors_agree_with_kinds(self):
        sys.path.insert(0, str(ROOT / "scripts/lib"))
        import gate_producers
        for entry in json.loads((ROOT / "ci/phase-gates.json").read_text())["gates"]:
            self.assertTrue(gate_producers.SELECTOR_BY_KIND[entry["subjectKind"]].match(entry["subjectSelector"]), entry["id"])


if __name__ == "__main__":
    unittest.main(argv=sys.argv)
