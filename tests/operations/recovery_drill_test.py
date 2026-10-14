#!/usr/bin/env python3
"""Behavior of the separate-host recovery drill (the `separate-host-restore` gate).

Hermetic: the recovered host is simulated by canned collector output and the TLS, DNS,
credential and member-journey probes are injected. This proves the drill's judgement and
its result contract; it is NOT evidence about any real host. Real evidence comes only from
running scripts/drills/recovery.sh against a restored machine.
"""

import copy
import datetime
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import recovery_drill as drill  # noqa: E402
import release_graph as rg  # noqa: E402

COMMIT = base.COMMIT
SOURCE_HOST = "sha256:" + "5" * 64
RECOVERED_HOST = base.NODE_HOST
VERIFIER_HOST = base.VERIFIER_HOST
SNAPSHOT = "7881a2df"
OWNER = {"username": "rehearsal-owner", "password": "Owner-Password-Canary-1"}
MEMBER = {"username": "restore-member", "password": "Member-Password-Canary-2"}
STALE = {
    "capturedAt": "2026-10-14T04:30:00Z",
    "cookies": {"telos_session": "stale-cookie-canary-3"},
    "inviteToken": "stale-invite-canary-4",
    "deviceAccessToken": "stale-access-canary-5",
    "deviceRefreshToken": "stale-refresh-canary-6",
}


def iso(offset_seconds):
    start = datetime.datetime(2026, 10, 14, 4, 41, 0, tzinfo=datetime.timezone.utc)
    return (start + datetime.timedelta(seconds=offset_seconds)).strftime("%Y-%m-%dT%H:%M:%SZ")


def epoch(offset_seconds):
    return datetime.datetime.fromisoformat(iso(offset_seconds).replace("Z", "+00:00")).timestamp()


def journal():
    rows, tick = [], 0
    for step in drill.REQUIRED_STEPS:
        for state in ("begin", "done"):
            tick += 1
            rows.append({"at": iso(100 + tick), "step": step, "state": state})
    return rows


def good_restore_facts():
    facts = base.good_facts()
    facts["mode"] = "restore"
    facts["host"]["machineIdSha256"] = RECOVERED_HOST
    facts["restore"] = {
        "journal": journal(),
        "result": {"status": "restored", "mode": "separate-host", "recordKind": "rehearsal", "snapshot": SNAPSHOT,
                   "manifestSha256": "d" * 64, "sourceHostIdSha256": SOURCE_HOST, "targetHostIdSha256": RECOVERED_HOST,
                   "candidateLockDigest": None, "consistencyPointAt": iso(0), "snapshotFinishedAt": iso(20),
                   "incidentDeclaredAt": iso(60), "restoredAt": iso(200)},
        "sanitization": {"available": True, "liveSessions": 0, "liveDevices": 0, "liveRefreshTokens": 0, "usableInvites": 0,
                         "restoreEvents": 1, "latestSnapshot": SNAPSHOT, "latestRestoredAt": iso(150)},
        "addresses": ["192.0.2.10"],
        "staleCredentials": {},
    }
    for argument in drill.stale_hashes(STALE):
        facts["restore"]["staleCredentials"][argument] = {"known": True, "dead": True}
    return facts


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


class EvaluationTests(unittest.TestCase):
    def target(self):
        return {"hostId": RECOVERED_HOST}

    def evaluate(self, facts, mode="rehearsal", lock=None):
        restore = facts["restore"]
        hashes = drill.stale_hashes(STALE)
        return (drill.evaluate_restore_record(restore, self.target(), mode, lock) + drill.evaluate_sanitization(restore)
                + drill.evaluate_stale_known(restore, STALE, hashes) + drill.evaluate_timeline(restore["result"], epoch(300)))

    def test_a_correct_recovery_passes_every_assertion(self):
        result = statuses(self.evaluate(good_restore_facts()))
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})
        self.assertEqual(set(result), {"restore.result", "restore.hosts", "restore.journal", "sanitize.state", "sanitize.audit",
                                       "stale.provenance", "recovery.objectives"})

    def test_each_mutation_fails_exactly_its_assertion(self):
        def drop_step(facts, step):
            facts["restore"]["journal"] = [r for r in facts["restore"]["journal"] if not (r["step"] == step and r["state"] == "done")]

        def reorder(facts):
            rows = facts["restore"]["journal"]
            sanitize = [r for r in rows if r["step"] == "sanitize" and r["state"] == "done"][0]
            rows.remove(sanitize)
            rows.append(sanitize)   # sanitize "finished" after the stack started

        mutations = {
            "restore.result": [lambda f: f["restore"]["result"].update(status="failed"), lambda f: f["restore"]["result"].update(mode="same-host"),
                               lambda f: f["restore"]["result"].update(recordKind="candidate")],
            "restore.hosts": [lambda f: f["restore"]["result"].update(sourceHostIdSha256=RECOVERED_HOST),
                              lambda f: f["restore"]["result"].update(targetHostIdSha256="sha256:" + "9" * 64),
                              lambda f: f["restore"]["result"].update(sourceHostIdSha256="not-a-digest")],
            "restore.journal": [lambda f: drop_step(f, "sanitize"), lambda f: drop_step(f, "result"), reorder,
                                lambda f: f["restore"]["journal"].append({"at": iso(250), "step": "start stack", "state": "failed"}),
                                lambda f: f["restore"]["journal"].append({"at": iso(250), "step": "failure-stack-down", "state": "done"})],
            "sanitize.state": [lambda f: f["restore"]["sanitization"].update(liveSessions=1), lambda f: f["restore"]["sanitization"].update(liveDevices=2),
                               lambda f: f["restore"]["sanitization"].update(liveRefreshTokens=1), lambda f: f["restore"]["sanitization"].update(usableInvites=1),
                               lambda f: f["restore"].update(sanitization={"available": False})],
            "sanitize.audit": [lambda f: f["restore"]["sanitization"].update(restoreEvents=0),
                               lambda f: f["restore"]["sanitization"].update(latestSnapshot="deadbeef")],
            "stale.provenance": [lambda f: f["restore"].update(staleCredentials={}),
                                 lambda f: next(iter(f["restore"]["staleCredentials"].values())).update(known=False),
                                 lambda f: next(iter(f["restore"]["staleCredentials"].values())).update(dead=False)],
            "recovery.objectives": [lambda f: f["restore"]["result"].update(snapshotFinishedAt=iso(-5)),      # finished before the consistency point
                                    lambda f: f["restore"]["result"].update(incidentDeclaredAt=iso(10)),      # declared before the snapshot finished
                                    lambda f: f["restore"]["result"].update(incidentDeclaredAt=iso(drill.RPO_MAX_SECONDS + 100)),
                                    lambda f: f["restore"]["result"].update(consistencyPointAt="garbage")],
        }
        for expected, changes in mutations.items():
            for index, change in enumerate(changes):
                facts = good_restore_facts()
                change(facts)
                incident = drill.parse_time(facts["restore"]["result"].get("incidentDeclaredAt") if facts["restore"]["result"] else None)
                journey = incident + 200 if incident else epoch(300)   # the journey passes 200 s after the incident is declared
                result = statuses(drill.evaluate_restore_record(facts["restore"], self.target(), "rehearsal", None)
                                  + drill.evaluate_sanitization(facts["restore"])
                                  + drill.evaluate_stale_known(facts["restore"], STALE, drill.stale_hashes(STALE))
                                  + drill.evaluate_timeline(facts["restore"]["result"], journey))
                failed = sorted(k for k, v in result.items() if v == "failed")
                self.assertEqual(failed, [expected], f"{expected} mutation {index} failed {failed}")

    def test_a_missing_result_fails_closed(self):
        facts = good_restore_facts()
        facts["restore"]["result"] = None
        result = statuses(drill.evaluate_restore_record(facts["restore"], self.target(), "rehearsal", None))
        self.assertEqual(result, {"restore.result": "failed"})

    def test_recovery_time_over_four_hours_fails_and_missing_incident_time_is_not_run(self):
        result = good_restore_facts()["restore"]["result"]
        self.assertEqual(statuses(drill.evaluate_timeline(result, epoch(60 + drill.RTO_MAX_SECONDS)))["recovery.objectives"], "passed")
        self.assertEqual(statuses(drill.evaluate_timeline(result, epoch(61 + drill.RTO_MAX_SECONDS)))["recovery.objectives"], "failed")
        self.assertEqual(statuses(drill.evaluate_timeline(result, None))["recovery.objectives"], "failed")
        result["incidentDeclaredAt"] = None
        self.assertEqual(statuses(drill.evaluate_timeline(result, epoch(300)))["recovery.objectives"], "not_run")

    def test_candidate_records_must_be_bound_to_the_candidate(self):
        facts = good_restore_facts()
        facts["restore"]["result"].update(recordKind="candidate", candidateLockDigest="sha256:" + "1" * 64)
        ok = statuses(drill.evaluate_restore_record(facts["restore"], self.target(), "candidate", "sha256:" + "1" * 64))
        self.assertEqual(ok["restore.candidate"], "passed")
        bad = statuses(drill.evaluate_restore_record(facts["restore"], self.target(), "candidate", "sha256:" + "2" * 64))
        self.assertEqual(bad["restore.candidate"], "failed")

    def test_only_the_latest_restore_attempt_is_judged(self):
        facts = good_restore_facts()
        earlier = [{"at": iso(-500), "step": "configuration", "state": "begin"}, {"at": iso(-490), "step": "postgres restore", "state": "failed"},
                   {"at": iso(-489), "step": "failure-stack-down", "state": "done"}]
        facts["restore"]["journal"] = earlier + facts["restore"]["journal"]
        self.assertEqual(statuses(drill.evaluate_restore_record(facts["restore"], self.target(), "rehearsal", None))["restore.journal"], "passed")

    def test_dns_cutover_requires_an_address_the_recovered_host_owns(self):
        resolver = lambda host, port, type=None: [(2, 1, 6, "", ("192.0.2.10", 443))]  # noqa: E731
        self.assertEqual(drill.evaluate_dns("https://node.example.net", ["192.0.2.10"], resolver)["status"], "passed")
        self.assertEqual(drill.evaluate_dns("https://node.example.net", ["192.0.2.99"], resolver)["status"], "failed")

        def broken(host, port, type=None):
            raise OSError("no route")
        self.assertEqual(drill.evaluate_dns("https://node.example.net", ["192.0.2.10"], broken)["status"], "failed")


class FakeSession:
    """Scripted stand-in for the smoke client's Session: answers by (method, path)."""
    answers = {}
    seen = []

    def __init__(self, origin, context, jar_path):
        self.cookies = {}

    def request(self, method, path, headers=None, body=None, json_body=None, timeout=0):
        FakeSession.seen.append((method, path, dict(self.cookies), dict(headers or {}), json_body))
        key = (method, path)
        if key == ("GET", "/api/v1/auth/me"):
            if self.cookies == STALE["cookies"]:
                return FakeSession.answers.get("stale-cookie", 401), {}, b""
            if (headers or {}).get("Authorization") == f"Bearer {STALE['deviceAccessToken']}":
                return FakeSession.answers.get("stale-access", 401), {}, b""
            return 200, {}, json.dumps({"Username": OWNER["username"]}).encode()
        if key == ("POST", "/api/v1/auth/login"):
            return FakeSession.answers.get("login", 200), {}, b"{}"
        if key == ("POST", "/api/v1/auth/devices/refresh"):
            return FakeSession.answers.get("stale-refresh", 401), {}, b""
        if key == ("POST", "/api/v1/auth/invites/accept"):
            return FakeSession.answers.get("stale-invite", 400), {}, b""
        raise AssertionError(f"unexpected request {key}")


class CredentialProbeTests(unittest.TestCase):
    def setUp(self):
        self.original = drill.smoke_client.Session
        drill.smoke_client.Session = FakeSession
        FakeSession.answers, FakeSession.seen = {}, []
        self.addCleanup(lambda: setattr(drill.smoke_client, "Session", self.original))

    def probe(self, stale=STALE):
        return statuses(drill.probe_credentials("https://node.example.net", OWNER, stale, None))

    def test_a_clean_recovery_passes_every_probe(self):
        self.assertEqual(self.probe(), {"auth.password": "passed", "stale.cookie": "passed", "stale.device": "passed", "stale.invite": "passed"})

    def test_each_stale_credential_that_still_works_fails_its_own_assertion(self):
        for key, expected in (("login", "auth.password"), ("stale-cookie", "stale.cookie"), ("stale-access", "stale.device"),
                              ("stale-refresh", "stale.device"), ("stale-invite", "stale.invite")):
            FakeSession.answers = {key: 401 if key == "login" else 200}
            result = self.probe()
            self.assertEqual([k for k, v in result.items() if v == "failed"], [expected], key)

    def test_missing_stale_credentials_are_not_run_never_passed(self):
        result = self.probe({"cookies": {}})
        self.assertEqual((result["stale.cookie"], result["stale.device"], result["stale.invite"]), ("not_run",) * 3)


class RunTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        self.manifest = self.dir / "fixtures.json"
        self.manifest.write_text("{}")
        self.credentials = self.dir / "credentials.json"
        self.out = self.dir / "out"
        self.out.mkdir()
        self.calls = []
        helper = base.TargetTests()
        self.target_file = helper.make(self.dir, credentialsFile=str(self.credentials), hostId=RECOVERED_HOST)
        self.write_credentials()   # after make(): it writes a placeholder credentials file of its own

    def write_credentials(self, **changes):
        document = {"owner": OWNER, "member": MEMBER, "fixtureManifestFile": str(self.manifest), "stale": copy.deepcopy(STALE)}
        document.update(changes)
        self.credentials.write_text(json.dumps(document))
        os.chmod(self.credentials, 0o600)

    def remote_factory(self, facts):
        calls = self.calls

        class Fake:
            def __init__(self, target):
                pass

            def collect(self, *arguments, **kw):
                calls.append(arguments)
                if arguments[0] == "identity":
                    return {"host": {"machineIdSha256": facts["host"]["machineIdSha256"]}}
                return facts
        return Fake

    def smoke(self, status="passed"):
        def run(args, mode, commit, lock_digest, target, scratch, manifest, hosts):
            self.calls.append(("smoke", mode, hosts[0]["hostId"], manifest))
            journey_credentials = json.loads(Path(target["credentialsFile"]).read_text())
            self.calls.append(("smoke-credential-keys", tuple(sorted(journey_credentials))))
            rows = [{"id": "chat.send", "description": "chat", "status": status, "detail": "ok"}]
            return (0 if status == "passed" else 1), {"schemaVersion": 1, "origin": target["origin"], "assertions": rows}, "summary"
        return run

    def invoke(self, facts=None, verifier=VERIFIER_HOST, smoke=None, probes=None, dns=None, clock=None, extra=()):
        facts = facts or good_restore_facts()
        argv = ["--target-file", self.target_file, "--rehearsal", "--source-commit", COMMIT,
                "--result-out", str(self.out / "result.json"), *extra]
        passing_dns = dns or (lambda origin, addresses: {"id": "dns.cutover", "description": "dns", "status": "passed", "detail": "ok"})
        passing_probes = probes or (lambda origin, owner, stale, context: [
            {"id": i, "description": i, "status": "passed", "detail": "ok"} for i in ("auth.password", "stale.cookie", "stale.device", "stale.invite")])
        return drill.run(argv, remote_factory=self.remote_factory(facts), tls_probe=lambda o: base.GOOD_TLS,
                         redirect_probe=lambda o: base.GOOD_REDIRECT, verifier_identity=lambda: verifier, smoke=smoke or self.smoke(),
                         probes=passing_probes, dns=passing_dns, clock=clock or (lambda: epoch(400)))

    def result(self):
        return rg.verify_result_directory(self.out, "result.json")

    def test_a_correct_recovery_passes_with_a_verifiable_result_and_subject(self):
        self.assertEqual(self.invoke(), 0)
        result = self.result()
        self.assertEqual((result["status"], result["recordKind"], result["gateId"], result["candidateLockDigest"]),
                         ("passed", "rehearsal", "separate-host-restore", None))
        subject = rg.load_record("observed-subject", (self.out / "subject.json").read_bytes())
        rg.validate_subject_for_result(result, subject)
        self.assertEqual([h["hostId"] for h in subject["hosts"]], [RECOVERED_HOST])
        observation = json.loads((self.out / "observation.json").read_text())
        self.assertEqual(observation["restore"]["result"]["sourceHostIdSha256"], SOURCE_HOST)
        assertions = json.loads((self.out / "assertions.json").read_text())["assertions"]
        self.assertEqual({a["status"] for a in assertions}, {"passed"}, [a["id"] for a in assertions if a["status"] != "passed"])
        self.assertIn("recovery.objectives", {a["id"] for a in assertions})
        self.assertEqual(self.calls[0], ("identity",))
        self.assertEqual(self.calls[1][:2], ("restore", "/var/lib/telos"))
        self.assertEqual(len([a for a in self.calls[1] if "=" in a]), 4, "one hash per stale credential kind is sent, never a secret")

    def test_the_journey_gets_only_its_own_credentials_and_the_fixture_manifest(self):
        self.invoke()
        self.assertIn(("smoke-credential-keys", ("member", "owner")), self.calls)
        self.assertIn(("smoke", "rehearsal", RECOVERED_HOST, str(self.manifest)), self.calls)

    def test_no_secret_or_stale_credential_reaches_the_output_or_the_collector(self):
        self.invoke()
        text = "".join(p.read_text() for p in self.out.iterdir())
        sent = " ".join(" ".join(c) for c in self.calls if c and isinstance(c[0], str) and c[0] == "restore")
        for canary in ("Owner-Password-Canary-1", "Member-Password-Canary-2", "stale-cookie-canary-3", "stale-invite-canary-4",
                       "stale-access-canary-5", "stale-refresh-canary-6"):
            self.assertNotIn(canary, text)
            self.assertNotIn(canary, sent)

    def test_the_verifier_may_not_be_the_recovered_host_or_the_source(self):
        with self.assertRaisesRegex(dc.NotRun, "recovered host itself"):
            self.invoke(verifier=RECOVERED_HOST)
        with self.assertRaisesRegex(dc.NotRun, "made the backup"):
            self.invoke(verifier=SOURCE_HOST)
        self.assertFalse((self.out / "result.json").exists())

    def test_a_rehearsal_may_use_the_source_as_observer_only_when_it_says_so(self):
        self.assertEqual(self.invoke(verifier=SOURCE_HOST, extra=("--rehearsal-source-verifier",)), 0)
        self.assertIn("observer was the machine that made the backup", self.result()["reason"])
        self.assertTrue(json.loads((self.out / "observation.json").read_text())["verifierIsSource"])
        for child in self.out.iterdir():
            child.unlink()
        self.assertEqual(self.invoke(), 0)
        self.assertFalse(json.loads((self.out / "observation.json").read_text())["verifierIsSource"])
        lock = self.dir / "lock.json"
        lock.write_bytes(rg.canonical_bytes({"schemaVersion": 1, "sourceCommit": COMMIT}))
        with self.assertRaisesRegex(dc.DrillError, "third machine"):
            drill.run(["--target-file", self.target_file, "--candidate-lock", str(lock), "--rehearsal-source-verifier",
                       "--result-out", str(self.out / "other.json")], remote_factory=self.remote_factory(good_restore_facts()),
                      verifier_identity=lambda: SOURCE_HOST)
        with self.assertRaisesRegex(dc.DrillError, "recovered host itself|third machine"):
            self.invoke(verifier=RECOVERED_HOST, extra=("--rehearsal-source-verifier",))

    def test_wrong_machine_identity_causes_no_collection_or_smoke(self):
        facts = good_restore_facts()
        facts["host"]["machineIdSha256"] = "sha256:" + "c" * 64
        with self.assertRaisesRegex(dc.DrillError, "different machine identity"):
            self.invoke(facts=facts)
        self.assertEqual([c[0] for c in self.calls], ["identity"])

    def test_a_missing_stale_credential_set_is_not_run_and_a_bootstrap_token_is_refused(self):
        self.write_credentials(stale=None)
        with self.assertRaisesRegex(dc.NotRun, "stale credentials"):
            self.invoke()
        self.write_credentials(bootstrapToken="x" * 40)
        with self.assertRaisesRegex(dc.DrillError, "already bootstrapped"):
            self.invoke()

    def test_failures_propagate_and_block_the_subject(self):
        facts = good_restore_facts()
        facts["restore"]["sanitization"]["liveSessions"] = 3
        self.assertEqual(self.invoke(facts=facts), 1)
        result = self.result()
        self.assertEqual((result["status"], result["subjectFile"]), ("failed", None))
        self.assertIn("sanitize.state", result["reason"])
        self.assertFalse((self.out / "subject.json").exists())

    def test_a_failed_journey_and_a_late_recovery_each_fail_the_gate(self):
        self.assertEqual(self.invoke(smoke=self.smoke(status="failed")), 1)
        self.assertIn("journey.smoke", self.result()["reason"])
        for child in self.out.iterdir():
            child.unlink()
        self.assertEqual(self.invoke(clock=lambda: epoch(60 + drill.RTO_MAX_SECONDS + 1)), 1)
        self.assertIn("recovery.objectives", self.result()["reason"])

    def test_dependency_outage_stages_are_out_of_scope_but_other_unrun_rows_are_not(self):
        rows = [{"id": "chat.send", "description": "chat", "status": "passed", "detail": "ok"},
                {"id": "outage.jellyfin", "description": "outage", "status": "not_run", "detail": "no controller"}]

        def with_rows(extra):
            def run(args, mode, commit, lock_digest, target, scratch, manifest, hosts):
                return 3, {"schemaVersion": 1, "origin": target["origin"], "assertions": rows + extra}, "summary"
            return run
        self.assertEqual(self.invoke(smoke=with_rows([])), 0)
        detail = [a for a in json.loads((self.out / "assertions.json").read_text())["assertions"] if a["id"] == "journey.smoke"][0]["detail"]
        self.assertIn("outage stages out of scope", detail)
        for child in self.out.iterdir():
            child.unlink()
        self.assertEqual(self.invoke(smoke=with_rows([{"id": "media.range", "description": "range", "status": "not_run", "detail": "x"}])), 3)
        for child in self.out.iterdir():
            child.unlink()
        self.assertEqual(self.invoke(smoke=with_rows([{"id": "outage.grimmory", "description": "o", "status": "failed", "detail": "x"}])), 1)

    def test_a_failed_journey_never_yields_a_recovery_time(self):
        # No journey pass, so there is no honest RTO: the objective is judged failed, not skipped.
        self.invoke(smoke=self.smoke(status="failed"))
        rows = {a["id"]: a["status"] for a in json.loads((self.out / "assertions.json").read_text())["assertions"]}
        self.assertEqual(rows["recovery.objectives"], "failed")

    def test_a_missing_incident_time_is_not_run(self):
        facts = good_restore_facts()
        facts["restore"]["result"]["incidentDeclaredAt"] = None
        self.assertEqual(self.invoke(facts=facts), 3)
        self.assertEqual(self.result()["status"], "not_run")

    def test_main_maps_errors_to_the_shared_exit_codes(self):
        base_args = ["--rehearsal", "--source-commit", COMMIT, "--result-out", str(self.out / "result.json")]
        self.assertEqual(drill.main(["--target-file", str(self.dir / "absent.json"), *base_args]), 3)
        os.chmod(self.target_file, 0o644)
        self.assertEqual(drill.main(["--target-file", self.target_file, *base_args]), 2)


class ShellWrapperTests(unittest.TestCase):
    def run_script(self, *args):
        return subprocess.run(["bash", str(ROOT / "scripts/drills/recovery.sh"), *args], capture_output=True, text=True, check=False)

    def test_without_a_target_the_gate_is_not_run_and_never_passes(self):
        completed = self.run_script("--rehearsal", "--source-commit", COMMIT, "--result-out", "/nonexistent/result.json")
        self.assertEqual(completed.returncode, 3, completed.stderr)
        self.assertIn("target", completed.stderr.lower())

    def test_with_a_candidate_lock_and_no_target_a_not_run_result_is_written(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / "lock.json"
            lock.write_bytes(rg.canonical_bytes({"schemaVersion": 1, "sourceCommit": COMMIT}))
            out = Path(directory) / "out"
            out.mkdir()
            completed = self.run_script("--candidate-lock", str(lock), "--result-out", str(out / "result.json"))
            self.assertEqual(completed.returncode, 3, completed.stderr)
            self.assertEqual(rg.verify_result_directory(out, "result.json")["status"], "not_run")

    def test_a_fixture_drill_checks_timestamp_policy_but_is_never_gate_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            record = Path(directory) / "drill.json"
            completed = subprocess.run(["bash", str(ROOT / "scripts/restore-drill.sh"), "--fixture", "--backup-epoch", "1000",
                                        "--restore-start", "2000", "--restore-end", "3000", "--out", str(record)],
                                       capture_output=True, text=True, check=False)
            self.assertEqual(completed.returncode, 0, completed.stderr)
            document = json.loads(record.read_text())
            self.assertEqual(document["drill"], "fixture")
            self.assertNotIn("gateId", document)
            late = subprocess.run(["bash", str(ROOT / "scripts/restore-drill.sh"), "--fixture", "--backup-epoch", "1000",
                                   "--restore-start", "2000", "--restore-end", str(2000 + drill.RTO_MAX_SECONDS + 1), "--out", str(record)],
                                  capture_output=True, text=True, check=False)
            self.assertEqual(late.returncode, 1)


if __name__ == "__main__":
    unittest.main(verbosity=1)
