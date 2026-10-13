#!/usr/bin/env python3
"""Behavior of the clean-install drill, its collector and the shared drill plumbing.

Everything here is hermetic: the node is simulated by canned collector output, the TLS
and port-80 probes and the member-journey probe are injected. This proves the drill's
judgement, safety checks and result contract; it is NOT evidence about any real node.
"""

import copy
import datetime
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(ROOT / "cli" / "lib"))
import clean_install_drill as drill  # noqa: E402
import drill_common as dc  # noqa: E402
import node_config  # noqa: E402
import node_observe  # noqa: E402
import release_graph as rg  # noqa: E402

NODE_HOST = "sha256:" + "a" * 64
VERIFIER_HOST = "sha256:" + "b" * 64
COMMIT = "1" * 40
BIRTH = int(datetime.datetime(2026, 10, 13, 9, 59, 0, tzinfo=datetime.timezone.utc).timestamp())
CREATED = "2026-10-13T10:00:00.123456789Z"


def stat_row(path, kind, uid, gid, mode, size=100, birth=None):
    return {"path": path, "exists": True, "type": kind, "uid": uid, "gid": gid, "mode": mode,
            "size": size, "mtime": BIRTH, "birth": birth}


def container(name, state="running", ports=(), user="", networks=("telos_telos-backend",), mounts=(), exit_code=0):
    return {"name": name, "id": "c" * 64, "image": "example/" + name, "imageId": "d" * 64, "state": state,
            "exitCode": exit_code, "created": CREATED, "startedAt": CREATED, "user": user,
            "networks": list(networks), "mounts": list(mounts), "ports": list(ports), "telos": True}


def good_facts():
    traefik_ports = [{"containerPort": p, "protocol": "tcp", "hostIp": "", "hostPort": p} for p in (80, 443)]
    containers = [container(n) for n in drill.LONG_RUNNING]
    for row in containers:
        if row["name"] == "telos-traefik":
            row["ports"] = traefik_ports
        elif row["name"] == "telos-client":
            row.update(user="101", networks=["telos_telos-ingress"],
                       mounts=[{"destination": "/tmp", "type": "tmpfs", "readOnly": False}])
        elif row["name"] == "telos-core":
            row["user"] = "10001:10001"
    containers.append(container("telos-migrate", state="exited"))
    env_keys = sorted(set(drill.PRODUCTION_SECRETS) | {"TELOS_ENV", "JELLYFIN_LIBRARY_IDS", "GRIMMORY_LIBRARY_IDS", "TZ"})
    return {
        "schemaVersion": 1, "mode": "install", "observedAt": "2026-10-13T10:30:00Z",
        "host": {"machineIdSha256": NODE_HOST, "bootId": "0" * 8 + "-0000-0000-0000-" + "0" * 12, "osId": "ubuntu",
                 "osVersion": "24.04", "arch": "x86_64", "kernel": "6.8.0-31-generic", "euid": 0},
        "tools": {"podman": {"version": "5.0.3", "rootless": False}, "provider": {"path": "/opt/telos/bin/podman-compose", "version": "1.6.0"}},
        "node": {
            "path": "/var/lib/telos", "dir": stat_row("/var/lib/telos", "dir", 0, 0, "0755"),
            "ancestors": [stat_row("/var/lib", "dir", 0, 0, "0755"), stat_row("/var", "dir", 0, 0, "0755"), stat_row("/", "dir", 0, 0, "0755")],
            "nodeEnv": dict(stat_row("/var/lib/telos/node.env", "file", 0, 0, "0600", birth=BIRTH), keys=env_keys, placeholderKeys=[],
                            values={"TELOS_ENV": "production", "JELLYFIN_LIBRARY_IDS": "lib-a", "GRIMMORY_LIBRARY_IDS": "1,2"}),
            "config": dict(stat_row("/var/lib/telos/config", "dir", 10001, 10001, "0700"), files=[
                stat_row("/var/lib/telos/config/cursor-keys.json", "file", 10001, 10001, "0600", size=200),
                stat_row("/var/lib/telos/config/hls-key", "file", 10001, 10001, "0600", size=64)]),
            "state": stat_row("/var/lib/telos/state", "dir", 0, 0, "0700"),
            "setupWatch": {"schemaVersion": 1, "bindings": [
                {"container": "telos-jellyfin", "containerPort": 8096, "protocol": "tcp", "hostIp": "127.0.0.1", "hostPort": 8096},
                {"container": "telos-grimmory", "containerPort": 6060, "protocol": "tcp", "hostIp": "127.0.0.1", "hostPort": 6060}]},
        },
        "containers": containers,
        "volumes": [{"name": "telos_postgres_data", "created": CREATED}, {"name": "unrelated", "created": "2020-01-01T00:00:00Z"}],
        "listeners": [{"address": "0.0.0.0", "port": 22}, {"address": "::", "port": 80}, {"address": "::", "port": 443},
                      {"address": "127.0.0.1", "port": 5432}],
    }


GOOD_TLS = {"verified": True, "hostname": "node.example.net", "issuer": "Let's Encrypt", "selfSigned": False, "daysRemaining": 60}
GOOD_REDIRECT = {"ok": True, "status": 308, "location": "https://node.example.net/"}


def evaluate_all(facts, target=None, tls=None, redirect=None):
    target = target or {"nodePath": "/var/lib/telos"}
    return (drill.evaluate_platform(facts) + drill.evaluate_node(facts, target) + drill.evaluate_services(facts)
            + drill.evaluate_provenance(facts) + drill.evaluate_setup_watch(facts)
            + drill.evaluate_public(tls or GOOD_TLS, redirect or GOOD_REDIRECT, "https://node.example.net"))


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


class EvaluationTests(unittest.TestCase):
    def test_a_correct_install_passes_every_assertion(self):
        result = statuses(evaluate_all(good_facts()))
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})
        self.assertGreaterEqual(len(result), 17)

    def test_production_secret_list_matches_the_node_config_contract(self):
        self.assertEqual(set(drill.PRODUCTION_SECRETS), set(node_config.PRODUCTION_SECRETS))
        self.assertEqual((drill.RUNTIME_UID, drill.RUNTIME_GID), (node_config.RUNTIME_UID, node_config.RUNTIME_GID))
        self.assertEqual({"cursor-keys.json", "hls-key"}, {node_config.CURSOR_CONTAINER_PATH.rsplit("/", 1)[1], node_config.HLS_CONTAINER_PATH.rsplit("/", 1)[1]})

    def test_each_mutation_fails_exactly_its_assertion(self):
        def container_of(facts, name):
            return next(c for c in facts["containers"] if c["name"] == name)

        mutations = {
            "host.platform": [lambda f: f["host"].update(osVersion="22.04"), lambda f: f["host"].update(osId="debian"),
                              lambda f: f["host"].update(arch="aarch64")],
            "runtime.podman": [lambda f: f["tools"]["podman"].update(version="4.9.3"), lambda f: f["tools"]["podman"].update(rootless=True),
                               lambda f: f["tools"].update(podman=None)],
            "runtime.provider": [lambda f: f["tools"]["provider"].update(version="1.0.6"), lambda f: f["tools"].update(provider=None),
                                 lambda f: f["tools"]["provider"].update(path="podman-compose")],
            "node.path": [lambda f: f["node"]["dir"].update(uid=1000), lambda f: f["node"]["ancestors"][0].update(type="symlink"),
                          lambda f: f["node"]["ancestors"][1].update(mode="0777"), lambda f: f["node"]["ancestors"][0].update(uid=1000),
                          lambda f: f["node"].update(path="/home/x/.telos")],
            "node.env": [lambda f: f["node"]["nodeEnv"].update(mode="0644"), lambda f: f["node"]["nodeEnv"].update(uid=1000),
                         lambda f: f["node"]["nodeEnv"].update(exists=False, type=None)],
            "node.secrets": [lambda f: f["node"]["nodeEnv"].update(placeholderKeys=["REDIS_PASSWORD"]),
                             lambda f: f["node"]["nodeEnv"].update(keys=[k for k in f["node"]["nodeEnv"]["keys"] if k != "JELLYFIN_ADMIN_TOKEN"])],
            "node.libraries": [lambda f: f["node"]["nodeEnv"]["values"].update(JELLYFIN_LIBRARY_IDS=""),
                               lambda f: f["node"]["nodeEnv"]["values"].update(GRIMMORY_LIBRARY_IDS=" , "),
                               lambda f: f["node"]["nodeEnv"]["values"].update(TELOS_ENV="development")],
            "node.signing": [lambda f: f["node"]["config"].update(mode="0755"), lambda f: f["node"]["config"].update(uid=0),
                             lambda f: f["node"]["config"]["files"][1].update(mode="0644"), lambda f: f["node"]["config"]["files"][1].update(size=8),
                             lambda f: f["node"]["config"]["files"].append(stat_row("/var/lib/telos/config/extra", "file", 10001, 10001, "0600")),
                             lambda f: f["node"]["config"]["files"].pop()],
            "node.state": [lambda f: f["node"]["state"].update(mode="0755"), lambda f: f["node"]["state"].update(uid=10001)],
            "services.running": [lambda f: container_of(f, "telos-core").update(state="exited"),
                                 lambda f: f["containers"].remove(container_of(f, "telos-jellyfin")),
                                 lambda f: container_of(f, "telos-migrate").update(exitCode=1),
                                 lambda f: f["containers"].append(container("telos-audiobook-migrate"))],
            "ports.published": [lambda f: container_of(f, "telos-core").update(ports=[{"containerPort": 8080, "protocol": "tcp", "hostIp": "127.0.0.1", "hostPort": 8080}]),
                                lambda f: container_of(f, "telos-traefik").update(ports=container_of(f, "telos-traefik")["ports"][:1]),
                                lambda f: container_of(f, "telos-traefik")["ports"].append({"containerPort": 8081, "protocol": "tcp", "hostIp": "", "hostPort": 8081}),
                                lambda f: container_of(f, "telos-traefik")["ports"][0].update(hostIp="192.0.2.7")],
            "ports.listeners": [lambda f: f["listeners"].append({"address": "0.0.0.0", "port": 8081}),
                                lambda f: f["listeners"].append({"address": "::", "port": 5432}), lambda f: f.update(listeners=None)],
            "client.isolation": [lambda f: container_of(f, "telos-client").update(user="0"), lambda f: container_of(f, "telos-client").update(user=""),
                                 lambda f: container_of(f, "telos-client").update(networks=["telos_telos-ingress", "telos_telos-backend"]),
                                 lambda f: container_of(f, "telos-client")["mounts"].append({"destination": "/data", "type": "bind", "readOnly": True}),
                                 lambda f: container_of(f, "telos-client").update(ports=[{"containerPort": 8080, "protocol": "tcp", "hostIp": "", "hostPort": 8080}])],
            "core.identity": [lambda f: container_of(f, "telos-core").update(user="0:0"), lambda f: container_of(f, "telos-core").update(user="")],
            "clean.provenance": [lambda f: f["volumes"].append({"name": "telos_old", "created": "2025-01-01T00:00:00Z"}),
                                 lambda f: f["volumes"].append({"name": "telos_undated", "created": ""}),
                                 lambda f: container_of(f, "telos-redis").update(created="2025-01-01T00:00:00Z"), lambda f: f.update(volumes=None)],
            "setup.loopback": [lambda f: f["node"]["setupWatch"]["bindings"][0].update(hostIp=""),
                               lambda f: f["node"]["setupWatch"]["bindings"][1].update(hostIp="0.0.0.0"),
                               lambda f: f["node"].update(setupWatch={"invalid": True})],
        }
        for assertion_id, changes in mutations.items():
            for index, change in enumerate(changes):
                facts = copy.deepcopy(good_facts())
                change(facts)
                result = statuses(evaluate_all(facts))
                self.assertEqual(result[assertion_id], "failed", f"{assertion_id} mutation {index} was not detected")
                others = {k: v for k, v in result.items() if v == "failed" and k != assertion_id}
                # a missing/odd service legitimately also disturbs the port inventory and vice versa
                allowed = {"services.running": {"ports.published"}, "ports.published": {"services.running"}, "node.env": {"node.secrets", "node.libraries"}, "client.isolation": {"ports.published"}}
                self.assertLessEqual(set(others), allowed.get(assertion_id, set()), f"{assertion_id} mutation {index} also failed {others}")

    def test_unobservable_facts_are_not_run_never_passed(self):
        facts = good_facts()
        facts["node"]["nodeEnv"]["birth"] = None
        facts["node"]["setupWatch"] = None
        result = statuses(evaluate_all(facts))
        self.assertEqual(result["clean.provenance"], "not_run")
        self.assertEqual(result["setup.loopback"], "not_run")
        facts["node"]["setupWatch"] = {"bindings": [{"container": "telos-jellyfin", "containerPort": 8096, "protocol": "tcp", "hostIp": "127.0.0.1", "hostPort": 8096}]}
        self.assertEqual(statuses(evaluate_all(facts))["setup.loopback"], "not_run")
        self.assertEqual(drill.worst(evaluate_all(facts)), "not_run")

    def test_public_probes(self):
        bad_tls = dict(GOOD_TLS, verified=False, error="self-signed certificate")
        self.assertEqual(statuses(drill.evaluate_public(bad_tls, GOOD_REDIRECT, "https://node.example.net"))["tls.chain"], "failed")
        self.assertEqual(statuses(drill.evaluate_public(dict(GOOD_TLS, daysRemaining=3), GOOD_REDIRECT, "https://node.example.net"))["tls.chain"], "failed")
        self.assertEqual(statuses(drill.evaluate_public(dict(GOOD_TLS, selfSigned=True), GOOD_REDIRECT, "https://node.example.net"))["tls.chain"], "failed")
        self.assertEqual(statuses(drill.evaluate_public(GOOD_TLS, {"ok": False, "status": 200}, "https://node.example.net"))["public.http-redirect"], "failed")
        self.assertEqual(statuses(drill.evaluate_public(GOOD_TLS, {"skipped": "custom port"}, "https://node.example.net:8443"))["public.http-redirect"], "not_run")

    def test_parse_time_accepts_podman_forms(self):
        expected = datetime.datetime(2026, 10, 13, 10, 0, 0, tzinfo=datetime.timezone.utc).timestamp()
        for text in ("2026-10-13T10:00:00Z", "2026-10-13T10:00:00.123456789Z", "2026-10-13T12:00:00.5+02:00", "2026-10-13 10:00:00 +0000 UTC",
                     "2026-10-13T12:00:00+0200"):
            self.assertAlmostEqual(drill.parse_time(text), expected, delta=1, msg=text)
        for text in ("", None, "0001-01-01T00:00:00Z", "yesterday"):
            self.assertIsNone(drill.parse_time(text))


class CollectorTests(unittest.TestCase):
    def test_node_env_is_reduced_to_names_flags_and_allowlisted_values(self):
        secret = "s3cr3t-canary-value-9f2c"
        facts = node_observe.parse_env_keys(
            f"POSTGRES_PASSWORD={secret}\nREDIS_PASSWORD=change-me\nTELOS_ENV=production\n"
            f"JELLYFIN_LIBRARY_IDS=abc,def\nJELLYFIN_ADMIN_TOKEN='{secret}'\n# comment\nEMPTY=\n")
        self.assertEqual(facts["values"], {"TELOS_ENV": "production", "JELLYFIN_LIBRARY_IDS": "abc,def"})
        self.assertEqual(facts["placeholderKeys"], ["EMPTY", "REDIS_PASSWORD"])
        self.assertNotIn(secret, json.dumps(facts))

    def test_file_facts_report_real_metadata_without_following_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / "file"
            target.write_text("x" * 40)
            os.chmod(target, 0o640)
            link = Path(directory) / "link"
            link.symlink_to(target)
            facts = node_observe.file_facts(str(target))
            self.assertEqual((facts["type"], facts["mode"], facts["size"], facts["uid"]), ("file", "0640", 40, os.getuid()))
            self.assertEqual(node_observe.file_facts(str(link))["type"], "symlink")
            self.assertFalse(node_observe.file_facts(str(Path(directory) / "missing"))["exists"])
            ancestors = node_observe.ancestor_facts(str(target))
            self.assertEqual(ancestors[-1]["path"], "/")

    def test_collector_runs_through_the_local_remote_and_never_prints_a_secret(self):
        secret = "canary-secret-7731"
        with tempfile.TemporaryDirectory() as directory:
            node = Path(directory) / "node"
            (node / "config").mkdir(parents=True)
            (node / "state").mkdir()
            (node / "node.env").write_text(f"POSTGRES_PASSWORD={secret}\nTELOS_ENV=production\n")
            (node / "config" / "hls-key").write_text("k" * 40)
            remote = dc.Remote({"sshTarget": None, "sshConfigFile": None})
            facts = remote.collect("install", str(node))
            identity = remote.collect("identity")
            self.assertEqual(facts["schemaVersion"], 1)
            self.assertEqual(facts["node"]["nodeEnv"]["values"], {"TELOS_ENV": "production"})
            self.assertEqual([f["path"].rsplit("/", 1)[1] for f in facts["node"]["config"]["files"]], ["hls-key"])
            self.assertNotIn(secret, json.dumps(facts))
            self.assertEqual(identity["host"]["machineIdSha256"], dc.local_machine_id())

    def test_setup_watch_is_persisted_only_under_state(self):
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / "state").mkdir()
            document = node_observe.mode_watch_setup(directory, 1, 0.2)
            self.assertEqual(document["bindings"], [])
            saved = Path(directory, "state", node_observe.SETUP_WATCH_FILE)
            self.assertEqual(stat.S_IMODE(saved.stat().st_mode), 0o600)
            self.assertEqual(json.loads(saved.read_text())["mode"], "watch-setup")
        self.assertEqual(node_observe.main(["-", "watch-setup", "relative", "5", "1"]), 2)
        self.assertEqual(node_observe.main(["-", "install"]), 2)
        self.assertEqual(node_observe.main(["-", "rm", "-rf"]), 2)


class TargetTests(unittest.TestCase):
    def make(self, directory, **overrides):
        directory = Path(directory)
        credentials = directory / "credentials.json"
        credentials.write_text("{}")
        os.chmod(credentials, 0o600)
        ssh_config = directory / "ssh_config"
        ssh_config.write_text("Host *\n")
        os.chmod(ssh_config, 0o600)
        document = {"schemaVersion": 1, "origin": "https://node.example.net", "nodePath": "/var/lib/telos", "hostId": NODE_HOST,
                    "sshTarget": "ops@node.example.net", "sshConfigFile": str(ssh_config), "credentialsFile": str(credentials),
                    "restoreTargetFile": None, "predecessorGeneration": None, "capacityTargetFile": None, "confirmation": None}
        document.update(overrides)
        path = directory / "target.json"
        path.write_text(json.dumps(document))
        os.chmod(path, 0o600)
        return str(path)

    def test_a_protected_target_loads_and_unsafe_ones_are_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            self.assertEqual(dc.load_target(self.make(directory))["sshTarget"], "ops@node.example.net")
            path = self.make(directory)
            os.chmod(path, 0o644)
            with self.assertRaisesRegex(dc.DrillError, "group/other"):
                dc.load_target(path)
            os.chmod(path, 0o600)
            link = Path(directory) / "link.json"
            link.symlink_to(path)
            with self.assertRaisesRegex(dc.DrillError, "symlink"):
                dc.load_target(str(link))
            with self.assertRaises(dc.NotRun):
                dc.load_target(str(Path(directory) / "missing.json"))
            with self.assertRaisesRegex(dc.DrillError, "absolute"):
                dc.load_target("relative.json")
            for overrides, message in (
                ({"origin": "http://node.example.net"}, "origin"),
                ({"origin": "https://user:pw@node.example.net"}, "origin"),
                ({"origin": "https://192.0.2.7"}, "IP literal"),
                ({"sshTarget": "-oProxyCommand=evil"}, "sshTarget"),
                ({"sshTarget": None}, "together"),
                ({"extra": 1}, "extra"),
                ({"hostId": "machine-1"}, "hostId"),
                ({"confirmation": "my password is hunter2"}, "password"),
            ):
                with self.assertRaises(rg.GraphError, msg=str(overrides)):
                    dc.load_target(self.make(directory, **overrides))
            path = self.make(directory)
            credentials = json.loads(Path(path).read_text())["credentialsFile"]
            os.chmod(credentials, 0o644)
            with self.assertRaisesRegex(dc.DrillError, "group/other"):
                dc.load_target(path)

    def test_remote_commands_are_fixed_and_strict(self):
        with tempfile.TemporaryDirectory() as directory:
            target = dc.load_target(self.make(directory))
            argv = dc.Remote(target).command(["install", "/var/lib/telos"])
            self.assertEqual(argv[0], "ssh")
            for needed in ("BatchMode=yes", "StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "RequestTTY=no"):
                self.assertIn(needed, argv)
            self.assertEqual(argv[argv.index("-F") + 1], target["sshConfigFile"])
            self.assertEqual(argv[argv.index("--") + 1:], ["sudo", "-n", "python3", "-I", "-", "install", "/var/lib/telos"])
            for hostile in ("; rm -rf /", "$(id)", "a b", "`x`", "--help\n"):
                with self.assertRaisesRegex(dc.DrillError, "safe alphabet"):
                    dc.Remote(target).command([hostile])
            local = dc.Remote({"sshTarget": None, "sshConfigFile": None}).command(["identity"])
            self.assertEqual(local[1:], ["-I", "-", "identity"])

    def test_ssh_transport_feeds_the_fixed_collector_on_stdin(self):
        with tempfile.TemporaryDirectory() as directory:
            target = dc.load_target(self.make(directory))
            shim_dir = Path(directory) / "bin"
            shim_dir.mkdir()
            shim = shim_dir / "ssh"
            log = Path(directory) / "ssh.argv"
            shim.write_text(
                "#!/usr/bin/env bash\n"
                f"printf '%s\\n' \"$@\" >{log}\n"
                'while [ "$1" != -- ]; do shift; done; shift\n'
                '[ "$1 $2" = "sudo -n" ] || exit 97\n'
                '[ "$3" = python3 ] || exit 98\n'
                "shift 3\n"
                f'exec {sys.executable} "$@"\n')
            shim.chmod(0o755)
            old_path = os.environ["PATH"]
            os.environ["PATH"] = f"{shim_dir}:{old_path}"
            try:
                observed = dc.Remote(target).collect("identity")
            finally:
                os.environ["PATH"] = old_path
            self.assertEqual(observed["host"]["machineIdSha256"], dc.local_machine_id())
            recorded = log.read_text().split()
            self.assertIn("StrictHostKeyChecking=yes", recorded)
            self.assertIn("ops@node.example.net", recorded)

    def test_identity_mismatch_stops_before_any_action(self):
        class Wrong:
            def collect(self, *arguments, **kw):
                return {"host": {"machineIdSha256": VERIFIER_HOST}}
        with self.assertRaisesRegex(dc.DrillError, "different machine identity"):
            dc.verify_node_identity(Wrong(), {"hostId": NODE_HOST})


class RunTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        helper = TargetTests()
        self.target_file = helper.make(self.dir)
        self.manifest = self.dir / "manifest.json"
        self.manifest.write_text("{}")
        self.out = self.dir / "out"
        self.out.mkdir()
        self.calls = []

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

    def smoke(self, status="passed", report=None):
        def run(args, mode, commit, lock_digest, target, scratch, manifest, hosts):
            self.calls.append(("smoke", mode, hosts[0]["hostId"]))
            rows = report if report is not None else [{"id": "chat.send", "description": "chat", "status": status, "detail": "ok"}]
            return (0 if status == "passed" else 1), {"schemaVersion": 1, "origin": target["origin"], "assertions": rows}, "summary"
        return run

    def invoke(self, facts=None, verifier=VERIFIER_HOST, tls=GOOD_TLS, redirect=GOOD_REDIRECT, smoke=None, extra=()):
        facts = facts or good_facts()
        argv = ["--target-file", self.target_file, "--fixture-manifest", str(self.manifest), "--rehearsal", "--source-commit", COMMIT,
                "--result-out", str(self.out / "result.json"), *extra]
        return drill.run(argv, remote_factory=self.remote_factory(facts), tls_probe=lambda origin: tls,
                         redirect_probe=lambda origin: redirect, verifier_identity=lambda: verifier, smoke=smoke or self.smoke())

    def result(self):
        return rg.verify_result_directory(self.out, "result.json")

    def test_a_correct_install_passes_with_a_verifiable_result_and_subject(self):
        code = self.invoke()
        self.assertEqual(code, 0)
        result = self.result()
        self.assertEqual((result["status"], result["recordKind"], result["gateId"], result["candidateLockDigest"]), ("passed", "rehearsal", "clean-install", None))
        self.assertEqual(result["toolVersions"], {"python3": result["toolVersions"]["python3"], "podman": "5.0.3"})
        subject = rg.load_record("observed-subject", (self.out / "subject.json").read_bytes())
        rg.validate_subject_for_result(result, subject)
        self.assertEqual(subject["hosts"][0]["hostId"], NODE_HOST)
        self.assertEqual({r["path"] for r in subject["records"]}, {"observation.json", "assertions.json", "journey-report.json"})
        self.assertEqual(self.calls[0], ("identity",))
        self.assertIn(("smoke", "rehearsal", NODE_HOST), self.calls)

    def test_nothing_secret_reaches_the_output(self):
        self.invoke()
        text = "".join(p.read_text() for p in self.out.iterdir())
        for needle in ("PASSWORD=", "BEGIN PRIVATE", "Bearer "):
            self.assertNotIn(needle, text)

    def test_verifier_equal_to_node_is_not_run(self):
        with self.assertRaisesRegex(dc.NotRun, "verifier is the node"):
            self.invoke(verifier=NODE_HOST)
        self.assertFalse((self.out / "result.json").exists())
        self.assertFalse(any(call[0] == "install" for call in self.calls))

    def test_wrong_machine_identity_causes_no_collection_or_smoke(self):
        facts = good_facts()
        facts["host"]["machineIdSha256"] = "sha256:" + "c" * 64
        with self.assertRaisesRegex(dc.DrillError, "different machine identity"):
            self.invoke(facts=facts)
        self.assertEqual([c[0] for c in self.calls], ["identity"])
        self.assertEqual(list(self.out.iterdir()), [])

    def test_failed_and_not_run_statuses_propagate_and_block_the_subject(self):
        facts = good_facts()
        facts["node"]["nodeEnv"]["mode"] = "0644"
        self.assertEqual(self.invoke(facts=facts), 1)
        result = self.result()
        self.assertEqual((result["status"], result["subjectFile"]), ("failed", None))
        self.assertIn("node.env", result["reason"])
        self.assertFalse((self.out / "subject.json").exists())
        for child in self.out.iterdir():
            child.unlink()
        facts = good_facts()
        facts["node"]["setupWatch"] = None
        self.assertEqual(self.invoke(facts=facts), 3)
        self.assertEqual(self.result()["status"], "not_run")

    def test_a_failed_journey_fails_the_gate_even_when_the_install_is_perfect(self):
        self.assertEqual(self.invoke(smoke=self.smoke(status="failed")), 1)
        self.assertIn("journey.smoke", self.result()["reason"])

    def test_a_missing_journey_report_fails_closed(self):
        def broken(*args):
            return 2, None, "usage"
        self.assertEqual(self.invoke(smoke=broken), 1)
        self.assertEqual(self.result()["status"], "failed")

    def test_candidate_mode_requires_a_lock_and_rejects_rehearsal_flags(self):
        lock = self.dir / "lock.json"
        lock.write_bytes(rg.canonical_bytes({"schemaVersion": 1, "sourceCommit": COMMIT}))
        argv = ["--target-file", self.target_file, "--fixture-manifest", str(self.manifest), "--candidate-lock", str(lock),
                "--result-out", str(self.out / "result.json")]
        code = drill.run(argv, remote_factory=self.remote_factory(good_facts()), tls_probe=lambda o: GOOD_TLS,
                         redirect_probe=lambda o: GOOD_REDIRECT, verifier_identity=lambda: VERIFIER_HOST, smoke=self.smoke())
        self.assertEqual(code, 0)
        self.assertEqual(self.result()["recordKind"], "candidate")
        self.assertTrue(self.result()["candidateLockDigest"].startswith("sha256:"))
        with self.assertRaises(dc.DrillError):
            drill.run(argv + ["--source-commit", COMMIT], remote_factory=self.remote_factory(good_facts()), verifier_identity=lambda: VERIFIER_HOST)

    def test_main_maps_errors_to_the_shared_exit_codes(self):
        base = ["--fixture-manifest", str(self.manifest), "--rehearsal", "--source-commit", COMMIT, "--result-out", str(self.out / "result.json")]
        self.assertEqual(drill.main(["--target-file", str(self.dir / "absent.json"), *base]), 3)
        os.chmod(self.target_file, 0o644)
        self.assertEqual(drill.main(["--target-file", self.target_file, *base]), 2)


class ShellWrapperTests(unittest.TestCase):
    def test_the_wrapper_reports_not_run_without_a_target(self):
        completed = subprocess.run(["bash", str(ROOT / "scripts/drills/clean-node-install.sh"), "--rehearsal", "--source-commit", COMMIT,
                                    "--result-out", "/nonexistent/result.json"], capture_output=True, text=True, check=False)
        self.assertEqual(completed.returncode, 3, completed.stderr)
        self.assertIn("target", completed.stderr.lower())

    def test_with_a_candidate_lock_and_no_target_a_not_run_result_is_written(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / "lock.json"
            lock.write_bytes(rg.canonical_bytes({"schemaVersion": 1, "sourceCommit": COMMIT}))
            out = Path(directory) / "out"
            out.mkdir()
            completed = subprocess.run(["bash", str(ROOT / "scripts/drills/clean-node-install.sh"), "--candidate-lock", str(lock),
                                        "--result-out", str(out / "result.json")], capture_output=True, text=True, check=False)
            self.assertEqual(completed.returncode, 3, completed.stderr)
            self.assertEqual(rg.verify_result_directory(out, "result.json")["status"], "not_run")


if __name__ == "__main__":
    unittest.main(verbosity=1)
