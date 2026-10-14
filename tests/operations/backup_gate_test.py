#!/usr/bin/env python3
"""The encrypted-backup gate: the node program against fake systemctl/podman/backup.sh, the repository checks
against a fake restic, and the judgement of both. Hermetic: it proves the gate's logic and result contract; it is
NOT evidence about any node or repository."""

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

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import backup_gate as gate  # noqa: E402
import backup_manifest as bm  # noqa: E402
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402

FAKE_SYSTEMCTL = '''#!/usr/bin/env python3
import json, os, sys
s = json.load(open(os.environ["FAKE_SCENARIO"]))
args = sys.argv[1:]
if args[:1] == ["is-enabled"]:
    print("enabled" if s.get("timerEnabled", True) else "disabled"); sys.exit(0 if s.get("timerEnabled", True) else 1)
if args[:1] == ["is-active"]:
    print("active" if s.get("timerActive", True) else "inactive"); sys.exit(0 if s.get("timerActive", True) else 3)
sys.exit(1)
'''
FAKE_PODMAN = '''#!/usr/bin/env python3
import json, os, sys
s = json.load(open(os.environ["FAKE_SCENARIO"]))
if sys.argv[1:2] == ["ps"]:
    print("\\n".join(s.get("running", ["telos-%d" % i for i in range(12)])))
'''
FAKE_BACKUP = '''#!/usr/bin/env python3
import json, os, sys, time
s = json.load(open(os.environ["FAKE_SCENARIO"]))
args = sys.argv[1:]
out = args[args.index("--result-out") + 1]
if s.get("backupFails"):
    sys.exit(1)
json.dump({"status": "passed", "snapshot": s["snapshot"], "manifest": "0" * 64, "at": "now"}, open(out, "w"))
open(os.path.join(os.environ["FAKE_OPS"], "last-backup.prom"), "w").write("telos_backup_last_success_timestamp_seconds %d\\ntelos_backup_last_result %d\\n" % (time.time(), 0 if s.get("metricsFail") else 1))
if s.get("leftover"):
    os.mkdir(os.path.join(os.environ["FAKE_TMP"], "telos-backup.leftover"))
if s.get("journal"):
    open(os.path.join(os.environ["FAKE_STATE"], "backup-stage.journal"), "w").write("x")
open(os.environ["FAKE_ARGV"], "w").write(json.dumps(args))
'''
FAKE_RESTIC = '''#!/usr/bin/env python3
import json, os, sys, time
s = json.load(open(os.environ["FAKE_SCENARIO"]))
args = sys.argv[1:]
password = open(os.environ["RESTIC_PASSWORD_FILE"]).read().strip()
right = password == "correct-horse"
if args[:1] == ["version"]:
    print("restic 0.18.1 compiled with go1.24 on linux/amd64"); sys.exit(0)
if not right:
    sys.stderr.write("wrong password or no key found\\n"); sys.exit(0 if s.get("acceptsAnyPassword") else 1)
if args[:1] == ["snapshots"]:
    if s.get("noSnapshot"):
        print("[]"); sys.exit(0)
    row = {"id": s["snapshot"], "short_id": s["snapshot"][:8], "time": s.get("snapshotTime") or time.strftime("%Y-%m-%dT%H:%M:%S.000000000Z", time.gmtime()),
           "hostname": s.get("hostname", "telos-node"), "tags": s.get("tags", ["scheduled", "telos", "release:0.1.0-beta.1", "candidate:none"]), "paths": ["/var/tmp/telos-backup.AbCdEf/data"]}
    print(json.dumps([row])); sys.exit(0)
if args[:1] == ["check"]:
    sys.exit(1 if s.get("checkFails") else 0)
if args[:1] == ["dump"]:
    sys.stdout.write(open(s["manifestFile"]).read()); sys.exit(0)
if args[:1] == ["ls"]:
    for name, size in json.load(open(s["listingFile"])).items():
        print(json.dumps({"type": "file", "path": "/var/tmp/telos-backup.AbCdEf/data/" + name, "size": size}))
    sys.exit(0)
sys.exit(2)
'''


class World:
    """A temp node + a repository the fake tools describe."""

    def __init__(self, **scenario):
        self.dir = Path(tempfile.mkdtemp(prefix="backup-gate-"))
        d = self.dir
        (d / "bin").mkdir()
        for name, text in (("systemctl", FAKE_SYSTEMCTL), ("podman", FAKE_PODMAN), ("restic", FAKE_RESTIC)):
            (d / "bin" / name).write_text(text)
            (d / "bin" / name).chmod(0o755)
        self.state = d / "state"
        self.ops = d / "ops"
        self.tmp = d / "tmp"
        self.release = d / "release"
        for p in (self.state, self.ops, self.tmp, self.release / "scripts"):
            p.mkdir(parents=True)
        (self.release / "scripts" / "backup.sh").write_text(FAKE_BACKUP)
        (self.release / "scripts" / "backup.sh").chmod(0o755)
        (self.release / "scripts" / "backup.sh.retention").write_text("")
        self.backup_env = d / "backup.env"
        self.password = d / "restic.pw"
        self.password.write_text("correct-horse\n")
        self.password.chmod(0o600)
        self.scenario = {"snapshot": "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"}
        self.scenario.update(scenario)
        self.stage = d / "stage"
        self.stage.mkdir()
        self.write_env()
        self.build_repository()
        self.write_backup_script()

    def write_env(self, repository="sftp:backup@vault.example.net:/srv/telos", password_mode=0o600):
        self.backup_env.write_text(f"RESTIC_REPOSITORY={repository}\nRESTIC_PASSWORD_FILE={self.password}\n")
        os.chmod(self.password, password_mode)

    def write_backup_script(self, daily=14, weekly=8):
        text = FAKE_BACKUP.replace("import json, os, sys, time", f"RETENTION_DAILY={daily}\n# fixture\nimport json, os, sys, time", 1)
        text = text.replace("#!/usr/bin/env python3\n", f"#!/usr/bin/env python3\n# RETENTION_DAILY={daily}\n# RETENTION_WEEKLY={weekly}\n", 1)
        script = self.release / "scripts" / "backup.sh"
        # the node program greps lines of the form NAME=value, so keep real assignments at the start of a line
        body = f"#!/usr/bin/env python3\nRETENTION_DAILY={daily}\nRETENTION_WEEKLY={weekly}\n" + FAKE_BACKUP.split("\n", 1)[1]
        script.write_text(body)
        script.chmod(0o755)

    def build_repository(self):
        for name, kind in bm.COMPONENTS:
            (self.stage / name).write_bytes(f"component {name}".encode())
        migrations = self.dir / "migrations.json"
        migrations.write_text(json.dumps([{"version": 1, "name": "0001_first", "checksum": "ab" * 32}]))
        for name in ("migrations.json",):
            (self.stage / name).write_bytes(migrations.read_bytes())
        started = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
        manifest = self.dir / "manifest.json"
        digest = self.scenario.get("manifestCandidate")
        args = type("A", (), dict(stage=str(self.stage), out=str(manifest), started=started, consistency_point=started, host_id_sha256="sha256:" + "ab" * 32,
                                  release="0.1.0-beta.1", source_commit="", candidate_lock_digest=digest or "", tag=["scheduled", "telos"], migrations=str(migrations)))
        bm.create(args)
        self.manifest = manifest
        listing = {name: (self.stage / name).stat().st_size for name, _ in bm.COMPONENTS}
        listing["manifest.json"] = manifest.stat().st_size
        if self.scenario.get("shortComponent"):
            listing["postgres.dump"] += 1
        (self.dir / "listing.json").write_text(json.dumps(listing))
        self.scenario["manifestFile"] = str(manifest)
        self.scenario["listingFile"] = str(self.dir / "listing.json")

    def environment(self):
        scenario = self.dir / "scenario.json"
        scenario.write_text(json.dumps(self.scenario))
        return {"PATH": f"{self.dir / 'bin'}:/usr/bin:/bin", "FAKE_SCENARIO": str(scenario), "FAKE_STATE": str(self.state), "FAKE_TMP": str(self.tmp), "FAKE_OPS": str(self.ops),
                "FAKE_ARGV": str(self.dir / "argv.json")}

    def paths(self):
        return {"backupEnv": str(self.backup_env), "currentLink": str(self.dir / "no-current"), "fallbackRelease": str(self.release), "stateDir": str(self.state), "opsDir": str(self.ops),
                "nodePath": str(self.dir), "tmpRoot": str(self.tmp), "backupTimeout": 60, "ownerUid": os.getuid()}

    def run_node(self):
        completed = subprocess.run([sys.executable, "-I", "-"], input=gate.node_program(self.paths()), capture_output=True, text=True, env=self.environment())
        assert completed.returncode == 0, completed.stderr
        return json.loads(completed.stdout)

    def restic(self, password=None):
        pw = password or self.password
        environment = self.environment()
        return gate.Restic("sftp:backup@vault.example.net:/srv/telos", str(pw), binary=str(self.dir / "bin" / "restic"),
                           runner=lambda argv, **kw: subprocess.run(argv, **{**kw, "env": {**kw["env"], **environment}}))

    def close(self):
        shutil.rmtree(self.dir, ignore_errors=True)


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


class JudgementTests(unittest.TestCase):
    def judge(self, lock_digest=None, **scenario):
        world = World(**scenario)
        self.addCleanup(world.close)
        started = datetime.datetime.now(datetime.timezone.utc).timestamp() - 5
        node = world.run_node()
        result = (node["run"].get("result") or {})
        repo = gate.gather_repository_facts(world.restic(), result["snapshot"], started, started + 60) if result.get("snapshot") else {}
        return statuses(gate.evaluate(node, repo, started, lock_digest)), node, repo

    def test_a_correct_backup_passes_every_assertion(self):
        result, node, repo = self.judge()
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})
        self.assertEqual(set(result), {"backup.config", "backup.schedule", "backup.run", "backup.snapshot", "backup.encrypted", "backup.integrity",
                                       "backup.manifest", "backup.contents", "backup.hygiene", "backup.metrics", "backup.retention"})

    def test_the_node_program_returns_no_secret(self):
        _, node, _ = self.judge()
        text = json.dumps(node)
        self.assertNotIn("correct-horse", text)
        self.assertNotIn("backup@vault", text, "the repository address stays on the node; only its scheme travels")

    def test_a_local_or_missing_repository_is_not_off_node(self):
        for repository in ("/srv/backups/local", "./repo", "file:/srv/x"):
            world = World()
            self.addCleanup(world.close)
            world.write_env(repository)
            node = world.run_node()
            self.assertEqual(statuses(gate.evaluate(node, {}, 0))["backup.config"], "failed", repository)

    def test_a_group_readable_password_file_fails(self):
        world = World()
        self.addCleanup(world.close)
        world.write_env(password_mode=0o640)
        self.assertEqual(statuses(gate.evaluate(world.run_node(), {}, 0))["backup.config"], "failed")

    def test_no_configuration_is_not_run(self):
        world = World()
        self.addCleanup(world.close)
        world.backup_env.unlink()
        self.assertEqual(statuses(gate.evaluate(world.run_node(), {}, 0)), {"backup.config": "not_run"})

    def test_a_disabled_timer_fails_the_schedule(self):
        self.assertEqual(self.judge(timerEnabled=False)[0]["backup.schedule"], "failed")
        self.assertEqual(self.judge(timerActive=False)[0]["backup.schedule"], "failed")

    def test_a_backup_that_fails_stops_the_judgement(self):
        result, *_ = self.judge(backupFails=True)
        self.assertEqual(result["backup.run"], "failed")
        self.assertNotIn("backup.snapshot", result)

    def test_a_snapshot_missing_from_the_repository_fails(self):
        self.assertEqual(self.judge(noSnapshot=True)[0]["backup.snapshot"], "failed") if False else None
        world = World(noSnapshot=True)
        self.addCleanup(world.close)
        node = world.run_node()
        started = datetime.datetime.now(datetime.timezone.utc).timestamp() - 5
        repo = gate.gather_repository_facts(world.restic(), node["run"]["result"]["snapshot"], started, started + 60)
        self.assertEqual(statuses(gate.evaluate(node, repo, started))["backup.snapshot"], "failed")

    def test_an_old_snapshot_is_not_this_runs(self):
        old = (datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=2)).strftime("%Y-%m-%dT%H:%M:%S.000000000Z")
        self.assertEqual(self.judge(snapshotTime=old)[0]["backup.snapshot"], "failed")

    def test_missing_tags_or_a_foreign_host_fail_the_snapshot(self):
        self.assertEqual(self.judge(tags=["telos"])[0]["backup.snapshot"], "failed")
        self.assertEqual(self.judge(hostname="someone-else")[0]["backup.snapshot"], "failed")

    def test_a_repository_that_accepts_any_password_is_not_encrypted(self):
        self.assertEqual(self.judge(acceptsAnyPassword=True)[0]["backup.encrypted"], "failed")

    def test_a_failing_integrity_check(self):
        self.assertEqual(self.judge(checkFails=True)[0]["backup.integrity"], "failed")

    def test_a_component_whose_size_differs_from_the_manifest(self):
        self.assertEqual(self.judge(shortComponent=True)[0]["backup.contents"], "failed")

    def test_leftover_plaintext_staging_or_a_journal_fails_hygiene(self):
        self.assertEqual(self.judge(leftover=True)[0]["backup.hygiene"], "failed")
        self.assertEqual(self.judge(journal=True)[0]["backup.hygiene"], "failed")

    def test_services_that_did_not_come_back_fail_hygiene(self):
        self.assertEqual(self.judge(running=["telos-core"])[0]["backup.hygiene"], "failed")

    def test_a_metrics_file_that_says_failure(self):
        self.assertEqual(self.judge(metricsFail=True)[0]["backup.metrics"], "failed")

    def test_other_retention_than_14_and_8_fails(self):
        world = World()
        self.addCleanup(world.close)
        world.write_backup_script(daily=7, weekly=4)
        node = world.run_node()
        started = datetime.datetime.now(datetime.timezone.utc).timestamp() - 5
        repo = gate.gather_repository_facts(world.restic(), node["run"]["result"]["snapshot"], started, started + 60)
        self.assertEqual(statuses(gate.evaluate(node, repo, started))["backup.retention"], "failed")

    def test_a_manifest_naming_another_candidate_fails_under_a_candidate_lock(self):
        # the node used a candidate lock only when one is installed; here the manifest names a different one
        world = World(manifestCandidate="sha256:" + "5" * 64)
        self.addCleanup(world.close)
        (world.state / "current-candidate-lock.json").write_text("{}")
        node = world.run_node()
        started = datetime.datetime.now(datetime.timezone.utc).timestamp() - 5
        repo = gate.gather_repository_facts(world.restic(), node["run"]["result"]["snapshot"], started, started + 60)
        self.assertEqual(statuses(gate.evaluate(node, repo, started, "sha256:" + "6" * 64))["backup.manifest"], "failed")
        self.assertEqual(statuses(gate.evaluate(node, repo, started, "sha256:" + "5" * 64))["backup.manifest"], "passed")


class RunTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        self.target_file = base.TargetTests().make(self.dir)
        target = json.loads(Path(self.target_file).read_text())
        self.credentials = Path(target["credentialsFile"])
        self.out = self.dir / "out"
        self.out.mkdir()
        self.worlds = []

    def world(self, **scenario):
        world = World(**scenario)
        self.worlds.append(world)
        self.addCleanup(world.close)
        return world

    def configure(self, world, **extra):
        document = {"backupRepository": "sftp:backup@vault.example.net:/srv/telos", "backupPasswordFile": str(world.password)}
        document.update(extra)
        self.credentials.write_text(json.dumps(document))
        os.chmod(self.credentials, 0o600)

    def remote_factory(self):
        facts = base.good_facts()

        class Fake:
            def __init__(self, target):
                pass

            def collect(self, *arguments, **kw):
                return facts
        return Fake

    def invoke(self, world, verifier=base.VERIFIER_HOST):
        def runner(target_file, program, timeout=0):
            return world.run_node()

        def restic_factory(repository, password_file, credentials_file=None):
            return world.restic(Path(password_file))
        return gate.run(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--result-out", str(self.out / "result.json")],
                        remote_factory=self.remote_factory(), runner=runner, restic_factory=restic_factory, verifier_identity=lambda: verifier)

    def test_a_good_backup_without_a_generation_receipt_is_not_run_but_recorded(self):
        world = self.world()
        self.configure(world)
        self.assertEqual(self.invoke(world), 3)
        result = rg.verify_result_directory(self.out, "result.json")
        self.assertEqual((result["status"], result["gateId"], result["recordKind"]), ("not_run", "encrypted-backup", "rehearsal"))
        self.assertIn("subject.runtime", result["reason"])
        assertions = json.loads((self.out / "assertions.json").read_text())["assertions"]
        self.assertTrue(all(a["status"] == "passed" for a in assertions if a["id"].startswith("backup.")))
        observation = (self.out / "observation.json").read_text()
        self.assertNotIn("correct-horse", observation)
        self.assertNotIn("vault.example.net", observation)

    def test_a_bad_backup_fails_the_gate(self):
        world = self.world(checkFails=True)
        self.configure(world)
        self.assertEqual(self.invoke(world), 1)
        self.assertIn("backup.integrity", rg.verify_result_directory(self.out, "result.json")["reason"])

    def test_the_verifier_needs_its_own_repository_access(self):
        world = self.world()
        self.credentials.write_text("{}")
        os.chmod(self.credentials, 0o600)
        with self.assertRaisesRegex(dc.NotRun, "backupRepository"):
            self.invoke(world)

    def test_the_password_file_must_be_private(self):
        world = self.world()
        self.configure(world)
        os.chmod(world.password, 0o644)
        with self.assertRaises(dc.DrillError):
            self.invoke(world)

    def test_the_verifier_may_not_be_the_node(self):
        world = self.world()
        self.configure(world)
        with self.assertRaisesRegex(dc.NotRun, "node itself"):
            self.invoke(world, verifier=base.NODE_HOST)


if __name__ == "__main__":
    unittest.main()
