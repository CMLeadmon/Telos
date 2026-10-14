#!/usr/bin/env python3
"""Guided and automated smoke records: the recorder, the signatures and the matrix aggregation.

Keys are ephemeral. Records are produced by the real recorder (scripted answers) and the real aggregator; each
adversarial case changes one dimension of an otherwise valid set and must be refused or demoted for its own reason."""

import contextlib
import datetime
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(ROOT / "scripts"))
import guided_smoke as gs  # noqa: E402
import release_graph as rg  # noqa: E402
import signing  # noqa: E402

RECORDER = ROOT / "scripts" / "record-guided-smoke.py"
CLIENT_BYTES = b"the hosted client image"
APPIMAGE_BYTES = b"\x7fELF appimage bytes"
COMMIT = "a" * 40
TESTER = "tester@example.test"
CATALOG = gs.load_catalog()


def keygen(directory, name):
    path = Path(directory) / name
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ephemeral", "-f", str(path)], check=True)
    return path, Path(str(path) + ".pub").read_text().split()


class Fixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.shared = Path(tempfile.mkdtemp(prefix="guided-shared-"))
        os.chmod(cls.shared, 0o700)
        cls.key, cls.public = keygen(cls.shared, "tester")
        cls.other_key, cls.other_public = keygen(cls.shared, "stranger")
        cls.signers = cls.shared / "signers"
        cls.signers.write_text(f'{TESTER} namespaces="{signing.NAMESPACES["guided"]}" {cls.public[0]} {cls.public[1]}\n')
        cls.client = cls.shared / "telos-client.oci.tar"
        cls.client.write_bytes(CLIENT_BYTES)
        cls.appimage = cls.shared / "Telos_0.1.0-beta.1_amd64.AppImage"
        cls.appimage.write_bytes(APPIMAGE_BYTES)
        lock = {"schemaVersion": 1, "version": "0.1.0-beta.1", "sourceCommit": COMMIT, "artifactIndexDigest": "sha256:" + "1" * 64,
                "releaseManifestDigest": "sha256:" + "2" * 64,
                "artifacts": {"images/telos-client.oci.tar": "sha256:" + hashlib.sha256(CLIENT_BYTES).hexdigest(),
                              "desktop/Telos_0.1.0-beta.1_amd64.AppImage": "sha256:" + hashlib.sha256(APPIMAGE_BYTES).hexdigest()}}
        cls.lock = cls.shared / "candidate-lock.json"
        cls.lock.write_bytes(rg.canonical_bytes(lock))
        cls.lock_digest = rg.digest_bytes(cls.lock.read_bytes())

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.shared, True)

    def setUp(self):
        self.dir = Path(tempfile.mkdtemp(prefix="guided-"))
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.records = self.dir / "records"
        self.records.mkdir()
        self.environment = {"TELOS_EVIDENCE_SIGNING_KEY": str(self.key), "TELOS_EVIDENCE_SIGNING_PRINCIPAL": TESTER}

    # ---- answers and the recorder
    def answers(self, matrix, platform, answer="pass", **changes):
        questions = gs.assertions_for(CATALOG, matrix, platform)
        automated = CATALOG["matrices"][matrix]["platforms"][platform]["mode"] == "automated"
        data = {"applicationVersion": "Chrome 141.0.7390.54" if automated else "Safari 18.1 (20619.2.8.11.10)",
                "os": "Ubuntu" if (automated or matrix == "desktop") else "iOS", "osVersion": "24.04" if (automated or matrix == "desktop") else "18.1",
                "deviceModel": gs.AUTOMATED_DEVICE if automated else ("Dell XPS 13" if matrix == "desktop" else "iPhone 15"),
                "hostId": ("sha256:" + "ab" * 32) if matrix == "desktop" else ("automation-host-01" if automated else "tester-device-01"),
                "assertions": [{"id": q["id"], "answer": answer, "notes": ""} for q in questions], "attachments": []}
        data.update(changes)
        return data

    def record(self, matrix, platform, answers=None, key=None, env=None, signers=None, out=None, lock=None, artifact=None, force_recorder=False):
        """Guided platforms go through the recorder; automated ones through the Playwright report tool, as in real use."""
        answers = answers or self.answers(matrix, platform)
        automated = CATALOG["matrices"][matrix]["platforms"].get(platform, {}).get("mode") == "automated"
        out = out or (self.records / f"{platform}.json")
        environment = dict(os.environ, **self.environment)
        environment.update(env or {})
        if key is not None:
            environment["TELOS_EVIDENCE_SIGNING_KEY"] = str(key)
        artifact = artifact or (self.appimage if matrix == "desktop" else self.client)
        if automated and not force_recorder:
            report = self.dir / f"playwright-{platform}.json"
            tests = []
            for entry in answers["assertions"]:
                if entry["answer"] == "not_run":
                    continue
                ok = entry["answer"] == "pass"
                tests.append({"title": f"journey @assertion:{entry['id']}", "tests": [{"projectName": platform, "status": "expected" if ok else "unexpected",
                                                                                      "results": [{"status": "passed" if ok else "failed"}]}]})
            report.write_text(json.dumps({"suites": [{"title": "hosted.spec.ts", "specs": tests}]}))
            now = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
            return subprocess.run([sys.executable, str(ROOT / "scripts" / "lib" / "browser_report.py"), "--platform", platform, "--playwright-json", str(report),
                                   "--candidate-lock", str(lock or self.lock), "--artifact", str(artifact), "--browser-version", answers["applicationVersion"],
                                   "--started", gs.stamp(now - datetime.timedelta(minutes=5)), "--finished", gs.stamp(now), "--out", str(out),
                                   "--signers", str(signers or self.signers), "--draft-dir", str(self.dir / "drafts"), "--os", answers["os"],
                                   "--os-version", answers["osVersion"], "--host-id", answers["hostId"]],
                                  capture_output=True, text=True, env=environment)
        answers_file = self.dir / f"answers-{matrix}-{platform}.json"
        answers_file.write_text(json.dumps(answers))
        return subprocess.run([sys.executable, str(RECORDER), "--matrix", matrix, "--platform", platform, "--candidate-lock", str(lock or self.lock),
                               "--artifact", str(artifact), "--out", str(out), "--answers-file", str(answers_file),
                               "--signers", str(signers or self.signers), "--draft-dir", str(self.dir / "drafts")],
                              capture_output=True, text=True, env=environment)

    def populate(self, matrix, answer="pass"):
        for platform, spec in CATALOG["matrices"][matrix]["platforms"].items():
            done = self.record(matrix, platform, self.answers(matrix, platform, answer))
            self.assertEqual(done.returncode, 0, done.stderr)

    def aggregate(self, matrix, **kwargs):
        out = self.dir / f"result-{matrix}"
        out.mkdir(exist_ok=True)
        arguments = ["aggregate", "--matrix", matrix, "--records-dir", str(self.records), "--candidate-lock", str(self.lock),
                     "--signers", str(self.signers), "--result-out", str(out / "result.json")]
        arguments += kwargs.get("extra", [])
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()) as err:
            code = gs.main(arguments)
        result = json.loads((out / "result.json").read_text()) if (out / "result.json").exists() else None
        return code, result, out, err.getvalue()


class RecorderTests(Fixture):
    def test_a_complete_signed_record_is_written(self):
        done = self.record("browser", "safari-ios")
        self.assertEqual(done.returncode, 0, done.stderr)
        data = (self.records / "safari-ios.json").read_bytes()
        record = rg.parse_canonical(data)
        self.assertEqual(record["candidateLockDigest"], self.lock_digest)
        self.assertEqual(record["artifactDigest"], "sha256:" + hashlib.sha256(CLIENT_BYTES).hexdigest())
        self.assertEqual([a["id"] for a in record["assertions"]], [q["id"] for q in CATALOG["matrices"]["browser"]["assertions"]])
        self.assertNotIn("status", record, "the recorder cannot write an overall verdict")
        signing.verify_signature(self.signers, TESTER, signing.NAMESPACES["guided"], data, (self.records / "safari-ios.json.sig").read_bytes())

    def test_the_questions_are_shown_one_at_a_time_in_order(self):
        sys.path.insert(0, str(ROOT / "scripts"))
        import importlib.util
        spec = importlib.util.spec_from_file_location("recorder", RECORDER)
        recorder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(recorder)
        replies = iter(["Safari 18", "iOS", "18.1", "iPhone 15", "device-1"] + ["pass", ""] * 15 + [""])
        shown = io.StringIO()
        answers = recorder.interactive_answers(CATALOG, "browser", "safari-ios", {"os": "", "osVersion": "", "deviceModel": "", "hostId": ""},
                                               lambda _prompt: next(replies), shown)
        text = shown.getvalue()
        positions = [text.index(f"[{n}/15]") for n in range(1, 16)]
        self.assertEqual(positions, sorted(positions))
        self.assertEqual(len(answers["assertions"]), 15)

    def test_an_invalid_answer_is_asked_again_never_defaulted(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("recorder", RECORDER)
        recorder = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(recorder)
        replies = iter(["Safari", "iOS", "18", "iPhone", "d1", "maybe", "yes", "pass", ""] + ["pass", ""] * 14 + [""])
        shown = io.StringIO()
        answers = recorder.interactive_answers(CATALOG, "browser", "safari-ios", {"os": "", "osVersion": "", "deviceModel": "", "hostId": ""},
                                               lambda _prompt: next(replies), shown)
        self.assertIn("exactly pass, fail or not_run", shown.getvalue())
        self.assertEqual(answers["assertions"][0]["answer"], "pass")

    def test_credential_like_notes_are_redacted_before_they_are_recorded(self):
        answers = self.answers("browser", "safari-ios")
        answers["assertions"][0]["notes"] = "worked; password: hunter2hunter2 and token=abcd1234efgh"
        self.assertEqual(self.record("browser", "safari-ios", answers).returncode, 0)
        notes = json.loads((self.records / "safari-ios.json").read_text())["assertions"][0]["notes"]
        self.assertNotIn("hunter2", notes)
        self.assertNotIn("abcd1234", notes)
        self.assertIn("[redacted]", notes)

    def test_attachments_are_hashed_and_copied_beside_the_record(self):
        shot = self.dir / "screenshot.png"
        shot.write_bytes(b"\x89PNG not a secret")
        answers = self.answers("browser", "safari-ios", attachments=[str(shot)])
        self.assertEqual(self.record("browser", "safari-ios", answers).returncode, 0)
        record = json.loads((self.records / "safari-ios.json").read_text())
        self.assertEqual(record["attachments"], [{"path": "safari-ios.attachments/screenshot.png", "sha256": hashlib.sha256(shot.read_bytes()).hexdigest()}])
        self.assertTrue((self.records / "safari-ios.attachments" / "screenshot.png").is_file())

    def test_a_secret_in_an_attachment_is_refused(self):
        leak = self.dir / "notes.txt"
        leak.write_text("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n")
        done = self.record("browser", "safari-ios", self.answers("browser", "safari-ios", attachments=[str(leak)]))
        self.assertEqual(done.returncode, 1)
        self.assertIn("secret-looking", done.stderr)
        self.assertFalse((self.records / "safari-ios.json").exists())

    def test_missing_extra_misordered_and_unknown_answers_are_refused(self):
        base = self.answers("browser", "safari-ios")
        cases = {"missing": dict(base, assertions=base["assertions"][:-1]),
                 "extra": dict(base, assertions=base["assertions"] + [{"id": "bonus", "answer": "pass", "notes": ""}]),
                 "misordered": dict(base, assertions=list(reversed(base["assertions"]))),
                 "unknown answer": dict(base, assertions=[dict(base["assertions"][0], answer="passed")] + base["assertions"][1:])}
        for name, answers in cases.items():
            with self.subTest(name):
                done = self.record("browser", "safari-ios", answers, out=self.records / f"{name.replace(' ', '-')}.json")
                self.assertEqual(done.returncode, 1, done.stderr)

    def test_a_platform_alias_is_refused(self):
        done = self.record("browser", "safari", self.answers("browser", "safari-ios"))
        self.assertEqual(done.returncode, 1)
        self.assertIn("is not a platform", done.stderr)

    def test_an_automated_platform_cannot_be_recorded_by_hand(self):
        done = self.record("browser", "chrome", self.answers("browser", "chrome"), force_recorder=True)
        self.assertEqual(done.returncode, 1)
        self.assertIn("automated", done.stderr)

    def test_the_artifact_must_be_the_candidates(self):
        other = self.dir / "other.oci.tar"
        other.write_bytes(b"another image")
        done = self.record("browser", "safari-ios", artifact=other)
        self.assertEqual(done.returncode, 1)
        self.assertIn("not the exact artifact", done.stderr)

    def test_a_desktop_record_is_bound_to_the_appimage(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        record = json.loads((self.records / "linux-appimage.json").read_text())
        self.assertEqual(record["artifactDigest"], "sha256:" + hashlib.sha256(APPIMAGE_BYTES).hexdigest())

    def test_without_an_enrolled_tester_only_an_unsigned_draft_exists(self):
        done = self.record("browser", "safari-ios", key=self.other_key)
        self.assertEqual(done.returncode, 3)
        self.assertFalse((self.records / "safari-ios.json").exists())
        drafts = list((self.dir / "drafts").glob("*.draft.json"))
        self.assertEqual(len(drafts), 1)
        self.assertIn("UNSIGNED draft", done.stderr)

    def test_without_a_key_only_an_unsigned_draft_exists(self):
        environment = {k: v for k, v in os.environ.items() if not k.startswith("TELOS_EVIDENCE")}
        answers_file = self.dir / "a.json"
        answers_file.write_text(json.dumps(self.answers("browser", "safari-ios")))
        done = subprocess.run([sys.executable, str(RECORDER), "--matrix", "browser", "--platform", "safari-ios", "--candidate-lock", str(self.lock),
                               "--artifact", str(self.client), "--out", str(self.records / "safari-ios.json"), "--answers-file", str(answers_file),
                               "--signers", str(self.signers), "--draft-dir", str(self.dir / "drafts")], capture_output=True, text=True, env=environment)
        self.assertEqual(done.returncode, 3)
        self.assertFalse((self.records / "safari-ios.json").exists())

    def test_a_record_is_never_overwritten(self):
        self.assertEqual(self.record("browser", "safari-ios").returncode, 0)
        done = self.record("browser", "safari-ios")
        self.assertEqual(done.returncode, 1)
        self.assertIn("never overwritten", done.stderr)


class AggregationTests(Fixture):
    def test_a_complete_browser_matrix_passes_and_binds_every_record(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            self.assertEqual(self.record("browser", platform).returncode, 0)
        code, result, out, _ = self.aggregate("browser")
        self.assertEqual(code, 0)
        self.assertEqual(result["status"], "passed")
        self.assertEqual(result["gateId"], "browser-matrix")
        self.assertEqual(result["recordKind"], "candidate")
        rg.verify_result_directory(out, "result.json")
        subject = rg.parse_canonical((out / "subject.json").read_bytes())
        self.assertEqual(subject["kind"], "external-host")
        self.assertEqual(subject["purpose"], "browser-matrix")
        self.assertEqual(len(subject["records"]), len(result["attachments"]) - 1)
        paths = {a["path"] for a in result["attachments"]}
        self.assertIn("browser/safari-ios.json", paths)
        self.assertIn("browser/safari-ios.json.sig", paths)

    def test_the_desktop_gate_needs_the_one_linux_record(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        code, result, out, _ = self.aggregate("desktop")
        self.assertEqual((code, result["status"], result["gateId"]), (0, "passed", "desktop-linux"))

    def test_a_missing_platform_makes_the_gate_not_run(self):
        for platform in list(CATALOG["matrices"]["browser"]["platforms"])[:-1]:
            self.assertEqual(self.record("browser", platform).returncode, 0)
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual(code, 3)
        self.assertEqual(result["status"], "not_run")
        self.assertIn("platform.chrome-android", result["reason"])

    def test_a_failed_answer_fails_the_gate(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            answers = self.answers("browser", platform)
            if platform == "safari-ios":
                answers["assertions"][4]["answer"] = "fail"
            self.assertEqual(self.record("browser", platform, answers).returncode, 0)
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual((code, result["status"]), (1, "failed"))
        self.assertIn("platform.safari-ios", result["reason"])

    def test_a_skipped_answer_is_not_a_pass(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            answers = self.answers("browser", platform)
            if platform == "chrome-android":
                answers["assertions"][2]["answer"] = "not_run"
            self.assertEqual(self.record("browser", platform, answers).returncode, 0)
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual((code, result["status"]), (3, "not_run"))

    def test_webkit_automation_cannot_stand_in_for_safari(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            if platform not in ("safari-desktop", "safari-ios"):
                self.assertEqual(self.record("browser", platform).returncode, 0)
        shutil.copy(self.records / "webkit.json", self.records / "safari-ios.json")
        shutil.copy(self.records / "webkit.json.sig", self.records / "safari-ios.json.sig")
        code, result, _, _ = self.aggregate("browser")
        self.assertNotEqual(code, 0)
        self.assertNotEqual(result["status"], "passed")

    def test_a_desktop_browser_cannot_stand_in_for_a_phone(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            self.assertEqual(self.record("browser", platform).returncode, 0)
        shutil.copy(self.records / "chrome.json", self.records / "chrome-android.json")
        shutil.copy(self.records / "chrome.json.sig", self.records / "chrome-android.json.sig")
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_emulation_cannot_stand_in_for_a_device(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            self.assertEqual(self.record("browser", platform).returncode, 0)
        record = json.loads((self.records / "safari-ios.json").read_text())
        record["deviceModel"] = gs.AUTOMATED_DEVICE
        data = rg.canonical_bytes(record)
        (self.records / "safari-ios.json").write_bytes(data)
        (self.records / "safari-ios.json.sig").write_bytes(gs.sign_record(data, self.key))
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_an_answer_edited_after_signing(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            self.assertEqual(self.record("browser", platform, self.answers("browser", platform, "fail")).returncode, 0)
        record = json.loads((self.records / "safari-ios.json").read_text())
        for entry in record["assertions"]:
            entry["answer"] = "pass"
        (self.records / "safari-ios.json").write_bytes(rg.canonical_bytes(record))
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual(code, 1)
        failing = [a for a in json.loads((self.dir / "result-browser" / "assertions.json").read_text())["assertions"] if a["id"] == "platform.safari-ios"]
        self.assertEqual(failing[0]["status"], "failed")
        self.assertIn("not signed by an enrolled tester", failing[0]["detail"])

    def test_a_record_signed_by_a_stranger(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        data = (self.records / "linux-appimage.json").read_bytes()
        (self.records / "linux-appimage.json.sig").write_bytes(gs.sign_record(data, self.other_key))
        code, result, _, _ = self.aggregate("desktop")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_a_record_for_another_candidate(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        record = json.loads((self.records / "linux-appimage.json").read_text())
        record["candidateLockDigest"] = "sha256:" + "9" * 64
        data = rg.canonical_bytes(record)
        (self.records / "linux-appimage.json").write_bytes(data)
        (self.records / "linux-appimage.json.sig").write_bytes(gs.sign_record(data, self.key))
        code, result, _, _ = self.aggregate("desktop")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_future_and_reversed_times(self):
        for field_swap in ("future", "reversed"):
            self.setUp()
            self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
            record = json.loads((self.records / "linux-appimage.json").read_text())
            if field_swap == "future":
                record["finishedAt"] = gs.stamp(gs.now_utc() + datetime.timedelta(days=2))
            else:
                record["startedAt"], record["finishedAt"] = record["finishedAt"], gs.stamp(gs.now_utc() - datetime.timedelta(hours=3))
            data = rg.canonical_bytes(record)
            (self.records / "linux-appimage.json").write_bytes(data)
            (self.records / "linux-appimage.json.sig").write_bytes(gs.sign_record(data, self.key))
            code, result, _, _ = self.aggregate("desktop")
            self.assertEqual((code, result["status"]), (1, "failed"), field_swap)

    def test_a_stale_record(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        record = json.loads((self.records / "linux-appimage.json").read_text())
        old = gs.now_utc() - datetime.timedelta(days=30)
        record["startedAt"], record["finishedAt"] = gs.stamp(old - datetime.timedelta(hours=1)), gs.stamp(old)
        data = rg.canonical_bytes(record)
        (self.records / "linux-appimage.json").write_bytes(data)
        (self.records / "linux-appimage.json.sig").write_bytes(gs.sign_record(data, self.key))
        code, result, _, _ = self.aggregate("desktop")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_a_changed_attachment(self):
        shot = self.dir / "shot.png"
        shot.write_bytes(b"png")
        self.assertEqual(self.record("desktop", "linux-appimage", self.answers("desktop", "linux-appimage", attachments=[str(shot)])).returncode, 0)
        (self.records / "linux-appimage.attachments" / "shot.png").write_bytes(b"swapped")
        code, result, _, _ = self.aggregate("desktop")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_the_same_record_supplied_for_two_platforms(self):
        for platform in CATALOG["matrices"]["browser"]["platforms"]:
            self.assertEqual(self.record("browser", platform).returncode, 0)
        # two guided platforms with byte-identical records are one record reused
        for suffix in ("", ".sig"):
            shutil.copy(self.records / f"safari-desktop.json{suffix}", self.records / f"safari-ios.json{suffix}")
        code, result, _, _ = self.aggregate("browser")
        self.assertEqual((code, result["status"]), (1, "failed"))

    def test_an_unknown_file_in_the_records_directory(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        (self.records / "linux-appimage-copy.json").write_text("{}")
        code, _, _, error = self.aggregate("desktop")
        self.assertEqual(code, 2)
        self.assertIn("unexpected entry", error)

    def test_a_rehearsal_aggregation_is_marked_as_a_rehearsal(self):
        self.assertEqual(self.record("desktop", "linux-appimage").returncode, 0)
        out = self.dir / "rehearsal"
        out.mkdir()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            code = gs.main(["aggregate", "--matrix", "desktop", "--records-dir", str(self.records), "--rehearsal", "--source-commit", COMMIT,
                            "--rehearsal-lock", str(self.lock), "--signers", str(self.signers), "--result-out", str(out / "result.json")])
        result = json.loads((out / "result.json").read_text())
        self.assertEqual((code, result["recordKind"]), (0, "rehearsal"))

    def test_without_a_lock_the_gate_is_not_run(self):
        out = self.dir / "nolock"
        out.mkdir()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            code = gs.main(["aggregate", "--matrix", "desktop", "--records-dir", str(self.records), "--rehearsal", "--source-commit", COMMIT,
                            "--result-out", str(out / "result.json")])
        self.assertEqual(code, 3)

    def test_automated_records_have_their_own_journey_list(self):
        self.assertEqual(self.record("browser", "chrome").returncode, 0)
        record = json.loads((self.records / "chrome.json").read_text())
        self.assertEqual([a["id"] for a in record["assertions"]], [q["id"] for q in CATALOG["matrices"]["browser"]["automatedAssertions"]])
        self.assertEqual(record["deviceModel"], gs.AUTOMATED_DEVICE)



class ReportTests(Fixture):
    @staticmethod
    def report(*tests):
        return {"suites": [{"title": "x.spec.ts", "specs": [{"title": title, "tests": [{"projectName": project, "status": status, "results": [{"status": r} for r in results]}]}
                                                              for title, project, status, results in tests]}]}

    def answer(self, report, platform="chrome"):
        import browser_report
        answers, summary = browser_report.answer_journeys(report, platform, gs.assertions_for(CATALOG, "browser", platform))
        return {a["id"]: a["answer"] for a in answers}, summary

    def test_every_status_maps_to_the_honest_answer(self):
        answers, _ = self.answer(self.report(
            ("a @assertion:chat", "chrome", "expected", ["passed"]),
            ("b @assertion:files", "chrome", "unexpected", ["failed"]),
            ("c @assertion:commentary", "chrome", "skipped", ["skipped"]),
            ("d @assertion:member-auth", "chrome", "flaky", ["failed", "passed"]),
            ("e @assertion:logout-login", "chrome", "unexpected", ["timedOut"]),
            ("f @assertion:settings-admin", "chrome", "unexpected", ["interrupted"])))
        self.assertEqual((answers["chat"], answers["files"], answers["commentary"], answers["member-auth"], answers["logout-login"], answers["settings-admin"]),
                         ("pass", "fail", "not_run", "fail", "fail", "fail"))
        self.assertEqual(answers["trusted-https"], "not_run", "a journey without a test is not_run, never a pass")

    def test_one_failing_test_among_passing_ones_fails_the_journey(self):
        answers, _ = self.answer(self.report(("a @assertion:chat", "chrome", "expected", ["passed"]), ("b @assertion:chat", "chrome", "unexpected", ["failed"])))
        self.assertEqual(answers["chat"], "fail")

    def test_another_browsers_tests_never_count(self):
        answers, _ = self.answer(self.report(("a @assertion:chat", "firefox", "expected", ["passed"])))
        self.assertEqual(answers["chat"], "not_run")

    def test_untagged_and_unknown_tags_are_ignored(self):
        answers, summary = self.answer(self.report(("plain test", "chrome", "expected", ["passed"]), ("x @assertion:bonus", "chrome", "expected", ["passed"])))
        self.assertTrue(all(v == "not_run" for v in answers.values()))
        self.assertEqual(summary, [])

    def test_the_raw_report_is_never_attached(self):
        self.assertEqual(self.record("browser", "chrome").returncode, 0)
        record = json.loads((self.records / "chrome.json").read_text())
        self.assertEqual([a["path"] for a in record["attachments"]], ["chrome.attachments/summary.json"])
        summary = json.loads((self.records / "chrome.attachments" / "summary.json").read_text())
        self.assertEqual(summary["platform"], "chrome")

    def test_a_report_for_a_guided_platform_is_refused(self):
        import browser_report
        code = browser_report.main(["--platform", "safari-ios", "--playwright-json", str(self.dir / "none.json"), "--candidate-lock", str(self.lock),
                                    "--artifact", str(self.client), "--browser-version", "x", "--started", "2026-10-14T10:00:00Z", "--finished", "2026-10-14T10:05:00Z",
                                    "--out", str(self.records / "safari-ios.json")])
        self.assertEqual(code, 1)

    def test_the_wrong_artifact_is_refused(self):
        other = self.dir / "other.tar"
        other.write_bytes(b"x")
        done = self.record("browser", "chrome", artifact=other)
        self.assertEqual(done.returncode, 1)
        self.assertIn("exact hosted-client artifact", done.stderr)

if __name__ == "__main__":
    unittest.main()
