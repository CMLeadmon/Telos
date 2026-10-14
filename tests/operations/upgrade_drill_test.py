#!/usr/bin/env python3
"""The upgrade/rollback drill: its judgement, its sequencing against a simulated node, its authorization and start-state
refusals, and the fixed node-side program. Hermetic -- the node is a small state machine, the member journey, TLS probes and
owner markers are injected. It proves logic and the result contract; it is NOT evidence about any node.
"""

import copy
import hashlib
import json
import os
import secrets
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import browser_node_smoke as smoke_client  # noqa: E402
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import upgrade_drill as drill  # noqa: E402

NODE_PROGRAM = ROOT / "scripts" / "lib" / "upgrade_node.py"
PRED = "1" * 64
LOCK_BYTES = json.dumps({"sourceCommit": base.COMMIT}).encode()
CAND = hashlib.sha256(LOCK_BYTES).hexdigest()
BASE_DIR = "/srv/telos"
NODE_DIR = "/var/lib/telos"
SNAPSHOT = "ab" * 32


def receipt(generation):
    return {"candidateLockDigest": "sha256:" + generation, "generationManifestDigest": "sha256:" + "7" * 64, "generationPath": f"{BASE_DIR}/generations/{generation}",
            "installedAt": "2026-10-14T10:00:00Z", "schemaVersion": 1}


class FakeNode:
    """The lifecycle as the real scripts leave it, with switchable faults."""

    def __init__(self, compatible=True, faults=()):
        self.compatible, self.faults = compatible, set(faults)
        self.current, self.schema = PRED, 24
        self.post_schema = 24 if compatible else 25
        self.cand_through = 24
        self.receipt, self.journal = receipt(PRED), None
        self.operations, self.rollback_dirs, self.markers, self.snapshot_markers = [], [], [], []
        self.installed = {PRED, CAND}
        self.calls = []
        self.gateway = "ok"

    def state(self):
        return {"schemaVersion": 1, "mode": "state", "observedAt": "2026-10-14T10:00:00Z", "current": self.current, "receipt": self.receipt,
                "journal": copy.deepcopy(self.journal), "operationsKept": [f"{op}.json" for op in self.operations], "schema": self.schema, "gateway": self.gateway,
                "composeProvider": {"set": "no-provider" not in self.faults, "usable": "no-provider" not in self.faults},
                "generations": {PRED: {"present": PRED in self.installed, "migrations": {"min": 1, "current": 24, "max": 24}, "rollbackCompatibleThrough": 24},
                                CAND: {"present": CAND in self.installed, "migrations": {"min": 10, "current": self.post_schema, "max": self.post_schema},
                                       "rollbackCompatibleThrough": self.cand_through}},
                "rollbackDirs": list(self.rollback_dirs)}

    @staticmethod
    def outcome(code, tail, result=None, seconds=100.0):
        return {"schemaVersion": 1, "mode": "run", "exitCode": code, "timedOut": False, "seconds": seconds, "outputTail": tail, "result": result}

    def upgrade(self, to, source):
        self.calls.append("upgrade")
        if "upgrade-fails" in self.faults:
            return self.outcome(1, ["upgrade: the backup failed"])
        operation = secrets.token_hex(16)
        pre = self.schema
        self.snapshot_markers = list(self.markers)
        self.schema, self.current, self.receipt = self.post_schema, to, receipt(to)
        self.journal = {"operationId": operation, "kind": "upgrade", "state": "complete", "fromGeneration": source, "toGeneration": to,
                        "snapshotId": None if "no-snapshot" in self.faults else SNAPSHOT, "preSchema": pre, "postSchema": self.schema, "hostIdSha256": base.NODE_HOST,
                        "lastError": None, "schemaVersion": 1, "startedAt": "2026-10-14T10:00:00Z", "updatedAt": "2026-10-14T10:02:00Z"}
        if "journal-wrong-target" in self.faults:
            self.journal["toGeneration"] = PRED
        self.operations.append(operation)
        if "upgrade-leaves-predecessor" in self.faults:
            self.current, self.receipt = source, receipt(source)
        return self.outcome(0, [f"upgrade: upgraded to {to[:12]} (schema {pre} -> {self.schema})"],
                            {"status": "complete", "operationId": operation, "snapshotId": SNAPSHOT, "preSchema": pre, "postSchema": self.schema})

    def rollback(self, to, operation):
        self.calls.append("rollback")
        if self.journal is None or self.journal["operationId"] != operation:
            return self.outcome(1, ["rollback: that is not the node's last operation"])
        if not self.compatible and "rollback-succeeds" not in self.faults:
            if "refusal-mutates" in self.faults:
                self.journal = dict(self.journal, state="restore-required")
            return self.outcome(1, [f"rollback: the previous generation's binaries cannot run on schema {self.schema}; nothing was changed. Restore the recorded snapshot instead:",
                                    f"rollback:   scripts/restore.sh --mode rollback --operation-id {operation} --confirm RESTORE-RECORDED-ROLLBACK"])
        previous = self.journal["fromGeneration"]
        if "rollback-loses-data" in self.faults:
            self.markers = self.markers[:-1]
        self.current, self.receipt = (to if "rollback-wrong-generation" in self.faults else previous), receipt(previous)
        self.journal = {**self.journal, "operationId": secrets.token_hex(16), "kind": "rollback", "state": "complete", "fromGeneration": to, "toGeneration": previous}
        return self.outcome(0, ["rollback: rolled back"], {"status": "complete", "operationId": self.journal["operationId"], "preSchema": self.schema, "postSchema": None})

    def restore(self, operation, flavour):
        self.calls.append(f"restore-{flavour}")
        if self.journal is None or self.journal["operationId"] != operation or self.compatible:
            return self.outcome(1, ["restore: refusing the rollback restore"])
        if "restore-fails" in self.faults:
            return self.outcome(1, ["restore: FAILED at step 'volume restore'"])
        if "restore-keeps-newer-data" not in self.faults:
            self.markers = list(self.snapshot_markers)
        self.schema = self.journal["preSchema"] if "restore-wrong-schema" not in self.faults else self.post_schema
        self.current, self.receipt = PRED, receipt(PRED)
        self.journal = {**self.journal, "state": "restored"}
        if "restore-keeps-nothing" not in self.faults:
            self.rollback_dirs.append("20261014T100000Z")
        return self.outcome(0, ["restore: restored"], {"status": "restored", "mode": "rollback", "operationId": operation})


class FakeRemote:
    node = None
    facts = None

    def __init__(self, target):
        pass

    def collect(self, *arguments, **kw):
        if arguments[0] == "identity":
            return {"host": {"machineIdSha256": base.NODE_HOST}}
        return copy.deepcopy(self.facts)

    def run_program(self, script, arguments, timeout=0, what=""):
        assert script == NODE_PROGRAM.read_bytes(), "the drill must send the fixed node program, nothing else"
        arguments = list(arguments)
        mode, rest = arguments[0], arguments[1:]
        assert rest[0] == BASE_DIR and rest[1] == NODE_DIR
        if mode == "state":
            assert rest[2:] == [CAND, PRED]
            return self.node.state()
        if mode == "upgrade":
            return self.node.upgrade(rest[2], rest[3])
        if mode == "rollback":
            return self.node.rollback(rest[2], rest[3])
        if mode == "restore":
            return self.node.restore(rest[4], rest[6])
        raise AssertionError(arguments)


class FakeMarkers:
    def __init__(self, node, faults=()):
        self.node, self.faults = node, set(faults)

    def post(self, nonce):
        if "marker-post-fails" in self.faults:
            raise smoke_client.Assertion("POST failed")
        self.node.markers.append(nonce)

    def present(self, nonce):
        if "marker-unreadable" in self.faults:
            raise smoke_client.Assertion("GET failed")
        return nonce in self.node.markers


def journey_report(status="passed"):
    rows = [{"id": "chat.send", "description": "c", "status": status, "detail": "ok"}, {"id": "outage.jellyfin", "description": "o", "status": "not_run", "detail": "needs a controller"}]
    return {"schemaVersion": 1, "origin": "https://node.example.net", "assertions": rows}


class DrillCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        manifest = self.dir / "fixtures.json"
        manifest.write_text("{}")
        credentials = self.dir / "credentials.json"
        self.target_file = base.TargetTests().make(self.dir, credentialsFile=str(credentials), predecessorGeneration=f"{BASE_DIR}/generations/{PRED}", confirmation=drill.CONFIRMATION)
        credentials.write_text(json.dumps({"owner": {"username": "o", "password": "p"}, "member": {"username": "m", "password": "q"}, "fixtureManifestFile": str(manifest)}))
        os.chmod(credentials, 0o600)
        self.lock = self.dir / "candidate-lock.json"
        self.lock.write_bytes(LOCK_BYTES)
        self.out = self.dir / "out"
        self.out.mkdir()

    def update_target(self, **changes):
        path = Path(self.target_file)
        document = json.loads(path.read_text())
        document.update(changes)
        path.write_text(json.dumps(document))

    def facts(self):
        facts = base.good_facts()
        facts["node"]["receipt"] = {"present": True, "digest": "sha256:" + "9" * 64}
        facts["host"]["bootId"] = "11111111-2222-3333-4444-555555555555"
        for container in facts["containers"]:
            container["image"] = f"docker.io/example/{container['name']}@sha256:{'7' * 64}"
            container["imageId"] = "8" * 64
        return facts

    def invoke(self, compatible=True, faults=(), journey_fails=(), verifier=base.VERIFIER_HOST, setup=None, candidate=False):
        self.node = FakeNode(compatible, faults)
        if setup:
            setup(self.node)
        remote = type("Remote", (FakeRemote,), {"node": self.node, "facts": self.facts()})
        self.journeys = []

        def smoke(args, mode, commit, lock_digest, target, scratch, manifest, hosts):
            self.journeys.append(Path(scratch).name)
            return 0, journey_report("failed" if Path(scratch).name in journey_fails else "passed"), "s"

        mode = ["--candidate-lock", str(self.lock)] if candidate else ["--rehearsal", "--source-commit", base.COMMIT, "--rehearsal-lock", str(self.lock)]
        return drill.run(["--target-file", self.target_file, *mode, "--result-out", str(self.out / "result.json")], remote_factory=remote, verifier_identity=lambda: verifier,
                         tls_probe=lambda origin: dict(base.GOOD_TLS), redirect_probe=lambda origin: dict(base.GOOD_REDIRECT), smoke=smoke,
                         markers=FakeMarkers(self.node, faults), token=lambda: "tok")

    def result(self):
        return rg.verify_result_directory(self.out, "result.json")

    def assertions(self):
        return {a["id"]: a for a in json.loads((self.out / "assertions.json").read_text())["assertions"]}

    def failed(self):
        return sorted(i for i, a in self.assertions().items() if a["status"] == "failed")


class SequenceTests(DrillCase):
    def test_the_compatible_branch_upgrades_rolls_back_and_upgrades_again(self):
        self.assertEqual(self.invoke(compatible=True), 0)
        self.assertEqual(self.node.calls, ["upgrade", "rollback", "upgrade"])
        result = self.result()
        self.assertEqual((result["status"], result["gateId"]), ("passed", "upgrade-rollback"))
        subject = rg.load_record("observed-subject", (self.out / "subject.json").read_bytes())
        self.assertEqual(subject["kind"], "runtime")
        observation = json.loads((self.out / "observation.json").read_text())
        self.assertEqual((observation["branch"], observation["candidate"], observation["predecessor"]), ("rollback", CAND, PRED))
        self.assertEqual([step["step"] for step in observation["trace"]], ["upgrade", "rollback", "re-upgrade"])
        self.assertEqual(self.journeys, ["upgrade", "rollback", "reupgrade"])
        ids = self.assertions()
        for needed in ("start.predecessor", "upgrade.exit", "upgrade.snapshot", "branch.selected", "rollback.journal", "rollback.after", "reupgrade.serving", "final.serving"):
            self.assertEqual(ids[needed]["status"], "passed", needed)
        self.assertNotIn("restore.exit", ids)
        self.assertEqual(self.node.current, CAND)

    def test_the_incompatible_branch_is_refused_then_restored_then_upgraded_again(self):
        self.assertEqual(self.invoke(compatible=False), 0)
        self.assertEqual(self.node.calls, ["upgrade", "rollback", "restore-rehearsal", "upgrade"])
        ids = self.assertions()
        for needed in ("refusal.exit", "refusal.unchanged", "restore.exit", "restore.journal", "restore.schema", "restore.kept", "restore.before", "restore.after", "reupgrade.exit"):
            self.assertEqual(ids[needed]["status"], "passed", needed)
        self.assertNotIn("rollback.exit", ids)
        self.assertEqual(self.journeys, ["upgrade", "restore", "reupgrade"])
        self.assertEqual(json.loads((self.out / "observation.json").read_text())["branch"], "restore")
        self.assertEqual(len(self.node.markers), 1, "the marker left after the snapshot is gone and the one before survives")
        self.assertTrue(self.node.markers[0].startswith("before-"))

    def test_a_candidate_run_restores_in_candidate_mode_and_records_a_candidate(self):
        self.assertEqual(self.invoke(compatible=False, candidate=True), 0)
        self.assertEqual(self.node.calls, ["upgrade", "rollback", "restore-candidate", "upgrade"])
        result = self.result()
        self.assertEqual((result["recordKind"], result["candidateLockDigest"]), ("candidate", "sha256:" + CAND))

    def test_each_fault_fails_exactly_what_catches_it_and_stops_the_sequence(self):
        cases = [
            ({"compatible": True, "faults": {"upgrade-fails"}}, ["upgrade.exit", "upgrade.journal", "upgrade.schema", "upgrade.serving", "upgrade.snapshot"], ["upgrade"]),
            ({"compatible": True, "faults": {"no-snapshot"}}, ["upgrade.snapshot"], ["upgrade"]),
            ({"compatible": True, "faults": {"journal-wrong-target"}}, ["upgrade.journal"], None),
            ({"compatible": True, "faults": {"upgrade-leaves-predecessor"}}, ["upgrade.serving"], None),
            ({"compatible": True, "faults": {"rollback-loses-data"}}, ["rollback.after"], ["upgrade", "rollback"]),
            ({"compatible": True, "faults": {"rollback-wrong-generation"}}, ["rollback.serving"], ["upgrade", "rollback"]),
            ({"compatible": False, "faults": {"rollback-succeeds"}}, ["refusal.exit", "refusal.unchanged"], ["upgrade", "rollback"]),
            ({"compatible": False, "faults": {"refusal-mutates"}}, ["refusal.unchanged"], ["upgrade", "rollback"]),
            ({"compatible": False, "faults": {"restore-keeps-newer-data"}}, ["restore.after"], ["upgrade", "rollback", "restore-rehearsal"]),
            ({"compatible": False, "faults": {"restore-wrong-schema"}}, ["restore.schema"], ["upgrade", "rollback", "restore-rehearsal"]),
            ({"compatible": False, "faults": {"restore-keeps-nothing"}}, ["restore.kept"], ["upgrade", "rollback", "restore-rehearsal"]),
            ({"compatible": False, "faults": {"restore-fails"}}, ["restore.exit", "restore.journal", "restore.serving", "restore.schema", "restore.kept"], ["upgrade", "rollback", "restore-rehearsal"]),
        ]
        for kwargs, expected, calls in cases:
            with self.subTest(kwargs=kwargs):
                self.out = self.dir / ("out-" + secrets.token_hex(3))
                self.out.mkdir()
                self.assertEqual(self.invoke(**kwargs), 1)
                failed = self.failed()
                for identifier in expected:
                    self.assertIn(identifier, failed)
                if calls is not None:
                    self.assertEqual(self.node.calls, calls, "a failed step stops every later step")
                self.assertEqual(self.result()["status"], "failed")
                self.assertFalse((self.out / "subject.json").exists(), "a failed drill writes no subject")
                if calls is not None and "sequence.complete" in self.assertions():
                    self.assertEqual(self.assertions()["sequence.complete"]["status"], "not_run")

    def test_a_failing_journey_stops_the_sequence_before_any_undo(self):
        self.assertEqual(self.invoke(compatible=True, journey_fails={"upgrade"}), 1)
        self.assertEqual(self.node.calls, ["upgrade"])
        self.assertIn("upgrade.journey", self.failed())

    def test_a_marker_that_cannot_be_read_back_fails_instead_of_passing(self):
        self.assertEqual(self.invoke(compatible=True, faults={"marker-unreadable"}), 1)
        self.assertEqual(self.node.calls, ["upgrade"])
        self.assertIn("upgrade.before", self.failed())

    def test_a_marker_that_cannot_be_left_stops_before_any_change(self):
        self.assertEqual(self.invoke(compatible=True, faults={"marker-post-fails"}), 1)
        self.assertEqual(self.node.calls, [])
        self.assertIn("start.marker", self.failed())

    def test_the_final_state_must_be_the_candidate(self):
        self.assertEqual(self.invoke(compatible=True, faults={"upgrade-fails"}), 1)
        self.assertEqual(self.assertions()["final.serving"]["status"], "failed")
        self.assertEqual(self.node.current, PRED)


class StartStateTests(DrillCase):
    def refused_without_changes(self, setup, failed_id):
        self.assertEqual(self.invoke(setup=setup), 1)
        self.assertEqual(self.node.calls, [], "nothing may change on a node that does not start where the drill expects")
        self.assertIn(failed_id, self.failed())

    def test_a_blocking_earlier_operation(self):
        self.refused_without_changes(lambda n: setattr(n, "journal", {"operationId": "e" * 32, "kind": "upgrade", "state": "restore-required"}), "start.settled")

    def test_the_candidate_must_be_installed(self):
        self.refused_without_changes(lambda n: n.installed.discard(CAND), "start.candidate")

    def test_the_node_must_start_on_the_predecessor(self):
        self.refused_without_changes(lambda n: (setattr(n, "current", CAND), setattr(n, "receipt", receipt(CAND))), "start.predecessor")

    def test_a_node_whose_lifecycle_scripts_cannot_find_the_compose_provider_is_not_touched(self):
        self.refused_without_changes(lambda n: n.faults.add("no-provider"), "start.provider")

    def test_an_unhealthy_node_is_not_upgraded(self):
        self.refused_without_changes(lambda n: setattr(n, "gateway", None), "start.healthy")


class AuthorizationTests(DrillCase):
    def test_no_confirmation_is_not_run_and_a_wrong_one_is_refused(self):
        self.update_target(confirmation=None)
        with self.assertRaises(dc.NotRun):
            self.invoke()
        self.update_target(confirmation="UPGRADE-THIS-NODE")
        with self.assertRaisesRegex(dc.DrillError, "confirmation"):
            self.invoke()

    def test_a_predecessor_is_required_and_must_look_like_a_generation(self):
        self.update_target(predecessorGeneration=None)
        with self.assertRaises(dc.NotRun):
            self.invoke()
        for bad in ("/srv/telos/generations/short", "/srv/telos/current", f"/srv/telos/generation/{PRED}"):
            self.update_target(predecessorGeneration=bad)
            with self.assertRaisesRegex(dc.DrillError, "predecessorGeneration"):
                self.invoke()

    def test_the_candidate_cannot_be_its_own_predecessor(self):
        self.update_target(predecessorGeneration=f"{BASE_DIR}/generations/{CAND}")
        with self.assertRaisesRegex(dc.DrillError, "same generation"):
            self.invoke()

    def test_the_verifier_cannot_be_the_node(self):
        with self.assertRaises(dc.NotRun):
            self.invoke(verifier=base.NODE_HOST)

    def test_the_candidate_lock_is_required(self):
        with self.assertRaisesRegex(dc.DrillError, "lock of the generation"):
            drill.run(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--result-out", str(self.out / "result.json")],
                      remote_factory=FakeRemote)

    def test_a_node_that_answers_with_another_identity_is_never_touched(self):
        class Wrong(FakeRemote):
            def collect(self, *arguments, **kw):
                return {"host": {"machineIdSha256": base.VERIFIER_HOST}}
        Wrong.node = FakeNode()
        with self.assertRaisesRegex(dc.DrillError, "different machine identity"):
            drill.run(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--rehearsal-lock", str(self.lock), "--result-out", str(self.out / "result.json")],
                      remote_factory=Wrong, verifier_identity=lambda: base.VERIFIER_HOST)
        self.assertEqual(Wrong.node.calls, [])

    def test_main_maps_outcomes_to_the_shared_exit_codes(self):
        self.update_target(confirmation=None)
        self.assertEqual(drill.main(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--rehearsal-lock", str(self.lock),
                                     "--result-out", str(self.out / "result.json")]), dc.EXIT_NOT_RUN)
        self.update_target(confirmation="nope")
        self.assertEqual(drill.main(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--rehearsal-lock", str(self.lock),
                                     "--result-out", str(self.out / "result.json")]), dc.EXIT_USAGE)


class PureJudgementTests(unittest.TestCase):
    def test_the_branch_comes_from_the_declared_window(self):
        pred = {"migrations": {"min": 1}}
        for post, through, expected in ((24, 24, "rollback"), (25, 24, "restore"), (0, 24, "restore"), (23, 24, "rollback"), (24, 25, "rollback")):
            self.assertEqual(drill.expected_branch(pred, {"rollbackCompatibleThrough": through}, post), expected, (post, through))
        self.assertIsNone(drill.expected_branch(pred, {"rollbackCompatibleThrough": None}, 24))
        self.assertIsNone(drill.expected_branch({}, {"rollbackCompatibleThrough": 24}, 24))

    def test_markers_that_cannot_be_read_are_failed_not_absent(self):
        out = drill.evaluate_markers("p", {"before": ("a", True), "after": ("b", False)}, {"a": None, "b": False})
        self.assertEqual([x["status"] for x in out], ["failed", "passed"])

    def test_a_journey_with_only_outage_rows_not_run_passes_but_other_gaps_do_not(self):
        self.assertEqual(drill.judge_journey("j", "d", 0, journey_report(), "")["status"], "passed")
        report = journey_report()
        report["assertions"].append({"id": "chat.edit", "description": "c", "status": "not_run", "detail": "skipped"})
        self.assertEqual(drill.judge_journey("j", "d", 0, report, "")["status"], "not_run")
        self.assertEqual(drill.judge_journey("j", "d", 1, None, "boom")["status"], "failed")
        self.assertEqual(drill.judge_journey("j", "d", 0, {"assertions": []}, "")["status"], "failed")


class NodeProgramTests(unittest.TestCase):
    """The fixed node-side program, against a stub release tree."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(os.path.realpath(self.tmp.name))
        self.base, self.node = root / "srv", root / "node"
        self.cand_lock = json.dumps({"lock": "candidate"}).encode()
        self.pred_lock = json.dumps({"lock": "predecessor"}).encode()
        self.cand, self.pred = hashlib.sha256(self.cand_lock).hexdigest(), hashlib.sha256(self.pred_lock).hexdigest()
        self.log = root / "calls.log"
        for identifier, lock in ((self.cand, self.cand_lock), (self.pred, self.pred_lock)):
            generation = self.base / "generations" / identifier
            (generation / "scripts").mkdir(parents=True)
            (generation / "telos").write_text('#!/usr/bin/env bash\ncase "$1 $2" in\n  "migrate --status") echo 25 ;;\n'
                                              '  "status --json") echo \'{"gateway":{"status":"ok"}}\' ;;\nesac\n')
            (generation / "telos").chmod(0o755)
            (generation / "generation.json").write_text(json.dumps({"migrations": {"min": 1, "current": 25, "max": 25}, "rollbackCompatibleThrough": 24}))
            for script in ("upgrade.sh", "rollback.sh", "restore.sh"):
                (generation / "scripts" / script).write_text(f'#!/usr/bin/env bash\necho "$0 $*" >>"{self.log}"\nout=""\n'
                                                              'while [ $# -gt 0 ]; do [ "$1" = --result-out ] && out="$2"; shift; done\n'
                                                              'echo "ran in $TELOS_RELEASE_ROOT base=$TELOS_RELEASE_BASE node=$TELOS_NODE_PATH"\n'
                                                              'printf \'{"status":"complete"}\\n\' >"$out"\n')
            (self.node / "state" / "candidates" / identifier).mkdir(parents=True)
            (self.node / "state" / "candidates" / identifier / "candidate-lock.json").write_bytes(lock)
        (self.base / "current").symlink_to(f"generations/{self.cand}")
        (self.node / "state" / "operation.json").write_text(json.dumps({"operationId": "a" * 32, "state": "complete"}))
        (self.node / "state" / "operations").mkdir()
        (self.node / "state" / "operations" / ("a" * 32 + ".json")).write_text("{}")
        self.env = dict(os.environ)

    def call(self, *arguments):
        completed = subprocess.run([sys.executable, "-I", str(NODE_PROGRAM), *arguments], capture_output=True, env=self.env, timeout=60, check=False)
        return completed.returncode, completed.stdout.decode(), completed.stderr.decode()

    def test_state_reports_what_the_lifecycle_needs(self):
        code, out, _ = self.call("state", str(self.base), str(self.node), self.cand, self.pred, "f" * 64)
        self.assertEqual(code, 0)
        state = json.loads(out)
        self.assertEqual((state["current"], state["schema"], state["gateway"]), (self.cand, 25, "ok"))
        self.assertEqual(state["journal"]["state"], "complete")
        self.assertEqual(state["operationsKept"], ["a" * 32 + ".json"])
        self.assertTrue(state["generations"][self.cand]["present"])
        self.assertEqual(state["generations"][self.cand]["rollbackCompatibleThrough"], 24)
        self.assertFalse(state["generations"]["f" * 64]["present"])

    def test_each_action_runs_the_right_script_with_its_own_confirmation_token(self):
        self.assertEqual(self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "t1")[0], 0)
        self.assertEqual(self.call("rollback", str(self.base), str(self.node), self.cand, "b" * 32, "t1")[0], 0)
        code, out, _ = self.call("restore", str(self.base), str(self.node), self.cand, self.pred, "b" * 32, "t1", "candidate")
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(out)["result"], {"status": "complete"})
        lines = self.log.read_text().splitlines()
        self.assertEqual(len(lines), 3)
        self.assertIn(f"{self.base}/generations/{self.cand}/scripts/upgrade.sh --generation {self.base}/generations/{self.cand} --candidate-lock", lines[0])
        self.assertIn("--current-candidate-lock", lines[0])
        self.assertIn("--confirm UPGRADE-THIS-NODE", lines[0])
        self.assertIn("--operation-id " + "b" * 32 + " --result-out", lines[1])
        self.assertIn("--confirm ROLLBACK-THIS-NODE", lines[1])
        self.assertIn("--mode rollback --operation-id " + "b" * 32 + " --candidate-lock", lines[2])
        self.assertIn(f"/state/candidates/{self.pred}/candidate-lock.json", lines[2], "the restore is bound to the lock of the generation restored to")
        self.assertIn("--confirm RESTORE-RECORDED-ROLLBACK", lines[2])
        code, out, _ = self.call("restore", str(self.base), str(self.node), self.cand, self.pred, "b" * 32, "t2", "rehearsal")
        self.assertIn("--rehearsal", self.log.read_text().splitlines()[-1])
        self.assertNotIn("--candidate-lock", self.log.read_text().splitlines()[-1])

    def test_only_the_compose_provider_is_passed_to_the_scripts(self):
        env_probe = self.base / "generations" / self.cand / "scripts" / "upgrade.sh"
        env_probe.write_text(f'#!/usr/bin/env bash\necho "provider=${{PODMAN_COMPOSE_PROVIDER:-unset}} secret=${{SECRET_TOKEN:-unset}} release_base=$TELOS_RELEASE_BASE" >>"{self.log}"\n'
                             'out=""; while [ $# -gt 0 ]; do [ "$1" = --result-out ] && out="$2"; shift; done; echo \'{"status":"complete"}\' >"$out"\n')
        self.env = dict(os.environ, PODMAN_COMPOSE_PROVIDER="/opt/provider/podman-compose", SECRET_TOKEN="hunter2")
        self.assertEqual(self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "env1")[0], 0)
        self.assertEqual(self.log.read_text().strip(), f"provider=/opt/provider/podman-compose secret=unset release_base={self.base}")
        del self.env["PODMAN_COMPOSE_PROVIDER"]
        self.assertEqual(self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "env2")[0], 0)
        self.assertIn("provider=unset", self.log.read_text().splitlines()[-1])

    def test_state_says_whether_the_provider_is_usable(self):
        provider = self.base / "podman-compose"
        provider.write_text("#!/bin/sh\n")
        self.env = dict(os.environ, PODMAN_COMPOSE_PROVIDER=str(provider))
        self.assertEqual(json.loads(self.call("state", str(self.base), str(self.node))[1])["composeProvider"], {"set": True, "usable": False})
        provider.chmod(0o755)
        self.assertEqual(json.loads(self.call("state", str(self.base), str(self.node))[1])["composeProvider"], {"set": True, "usable": True})
        self.env = {k: v for k, v in os.environ.items() if k != "PODMAN_COMPOSE_PROVIDER"}
        self.assertEqual(json.loads(self.call("state", str(self.base), str(self.node))[1])["composeProvider"], {"set": False, "usable": False})

    def test_unsafe_arguments_are_refused_before_anything_runs(self):
        cases = [
            ("upgrade", "relative", str(self.node), self.cand, self.pred, "t"),
            ("upgrade", str(self.base), str(self.node), "short", self.pred, "t"),
            ("upgrade", str(self.base), str(self.node), self.cand, self.pred, "Bad Tag"),
            ("upgrade", str(self.base), str(self.node), self.cand, self.pred, "-x"),
            ("rollback", str(self.base), str(self.node), self.cand, "not-an-operation", "t"),
            ("restore", str(self.base), str(self.node), self.cand, self.pred, "b" * 32, "t", "everything"),
            ("restore", str(self.base), str(self.node), self.cand, self.pred, "b" * 32, "t"),
            ("format-disk", str(self.base), str(self.node)),
            ("state", str(self.base), str(self.node) + "/../x"),
        ]
        for arguments in cases:
            with self.subTest(arguments=arguments):
                code, _, error = self.call(*arguments)
                self.assertEqual(code, 2, error)
        self.assertFalse(self.log.exists(), "no lifecycle script ran")

    def test_a_tag_is_used_once_and_a_lock_must_hash_to_its_generation(self):
        self.assertEqual(self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "once")[0], 0)
        code, _, error = self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "once")
        self.assertEqual(code, 2)
        self.assertIn("already used", error)
        (self.node / "state" / "candidates" / self.pred / "candidate-lock.json").write_text("tampered")
        code, _, error = self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "other")
        self.assertEqual(code, 2)
        self.assertIn("does not hash", error)
        self.assertEqual(len(self.log.read_text().splitlines()), 1)

    def test_a_script_that_is_a_symlink_or_missing_is_not_run(self):
        script = self.base / "generations" / self.cand / "scripts" / "rollback.sh"
        script.unlink()
        script.symlink_to("/bin/true")
        code, _, error = self.call("rollback", str(self.base), str(self.node), self.cand, "b" * 32, "t")
        self.assertEqual(code, 2)
        self.assertIn("not installed", error)
        script.unlink()
        self.assertEqual(self.call("rollback", str(self.base), str(self.node), self.cand, "b" * 32, "t")[0], 2)

    def test_a_failing_script_is_reported_not_hidden(self):
        script = self.base / "generations" / self.cand / "scripts" / "upgrade.sh"
        script.write_text('#!/usr/bin/env bash\necho "upgrade: the backup failed"\nexit 7\n')
        code, out, _ = self.call("upgrade", str(self.base), str(self.node), self.cand, self.pred, "bad")
        self.assertEqual(code, 0)
        outcome = json.loads(out)
        self.assertEqual((outcome["exitCode"], outcome["result"]), (7, None))
        self.assertIn("the backup failed", outcome["outputTail"][-1])


if __name__ == "__main__":
    unittest.main()
