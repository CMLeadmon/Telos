#!/usr/bin/env python3
"""Mutation tests for the container-hardening verifier.

The baseline is the repository's own Compose file rendered by the hermetic double and
judged against config/runtime-hardening.json: it must be clean. Every control the
policy names is then removed or loosened on its own and the verifier must name that
control. Live inspection is exercised the same way against synthetic observations that
satisfy the policy, plus a projection test over a real `podman inspect` capture.
"""

import copy
import datetime
import json
import os
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.dont_write_bytecode = True
import runtime_hardening as rh  # noqa: E402
import yaml  # noqa: E402

POLICY = rh.load_policy(ROOT / "config" / "runtime-hardening.json")
LOCK = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
TODAY = datetime.date(2026, 10, 14)


def render():
    env = dict(os.environ, PATH=f"{ROOT / 'tests/helpers/compose_double'}:{os.environ['PATH']}")
    out = subprocess.run(["podman", "compose", "--env-file", "tests/fixtures/compose.env", "-f", "docker-compose.yml", "config"],
                         cwd=ROOT, env=env, capture_output=True, text=True, check=True).stdout
    return yaml.safe_load(out)


MODEL = render()


def findings(model=None, policy=None, lock=None, today=TODAY):
    return rh.check_static(policy or POLICY, model or MODEL, lock or LOCK, today)


def mutated(service, change):
    model = copy.deepcopy(MODEL)
    change(model["services"][service])
    return model


class StaticBaseline(unittest.TestCase):
    def test_the_repository_compose_file_satisfies_its_policy(self):
        self.assertEqual(findings(), [])

    def test_every_compose_service_is_named_by_the_policy_and_back(self):
        self.assertEqual(set(MODEL["services"]), set(POLICY["services"]))


class StaticMutations(unittest.TestCase):
    def expect(self, service, change, fragment):
        result = findings(mutated(service, change))
        self.assertTrue(any(fragment in item and item.startswith(service) for item in result),
                        f"{service}: {fragment!r} not reported in {result}")

    def test_identity(self):
        self.expect("telos-core", lambda s: s.__setitem__("user", "0:0"), "runs as root")
        self.expect("telos-client", lambda s: s.__setitem__("user", "root"), "runs as root")
        self.expect("telos-core", lambda s: s.pop("user"), "does not state the user")
        self.expect("telos-core", lambda s: s.__setitem__("user", "10002:10002"), "differs from the policy identity")
        self.expect("jellyfin", lambda s: s.__setitem__("user", "0:0"), "runs as root")
        self.expect("grimmory", lambda s: s.__setitem__("user", "app:app"), "numeric non-root")

    def test_privilege_controls(self):
        self.expect("telos-core", lambda s: s.__setitem__("security_opt", []), "no-new-privileges")
        self.expect("redis", lambda s: s.pop("security_opt"), "no-new-privileges")
        self.expect("telos-core", lambda s: s.__setitem__("cap_drop", []), "does not drop all capabilities")
        self.expect("telos-core", lambda s: s.__setitem__("cap_add", ["NET_ADMIN"]), "adds capabilities")
        self.expect("telos-core", lambda s: s.__setitem__("read_only", False), "not read-only")
        self.expect("telos-core", lambda s: s.__setitem__("privileged", True), "privileged")
        for key in ("pid", "ipc", "network_mode"):
            self.expect("telos-core", lambda s, k=key: s.__setitem__(k, "host"), f"via {key}")
        self.expect("telos-core", lambda s: s.__setitem__("devices", ["/dev/kmsg"]), "device")
        self.expect("telos-core", lambda s: s.__setitem__("security_opt", ["no-new-privileges:true", "seccomp=unconfined"]), "disables confinement")
        self.expect("telos-core", lambda s: s.__setitem__("security_opt", ["no-new-privileges:true", "label=disable"]), "disables confinement")

    def test_writable_paths(self):
        self.expect("telos-core", lambda s: s["volumes"].append("/srv/extra:/extra"), "writable mount /extra")
        self.expect("telos-core", lambda s: s["volumes"].append("/run/podman/podman.sock:/run/podman.sock"), "container runtime")
        self.expect("telos-core", lambda s: s["volumes"].append("/:/host:ro"), "host root")
        self.expect("telos-core", lambda s: s["tmpfs"].append("/var/cache:size=1m"), "tmpfs /var/cache is not in the policy")
        self.expect("telos-core", lambda s: s.__setitem__("tmpfs", ["/tmp"]), "not bounded")
        self.expect("telos-core", lambda s: s.__setitem__("tmpfs", ["/tmp:size=4g,mode=1777"]), "not bounded")

    def test_resource_bounds(self):
        self.expect("telos-core", lambda s: s.pop("cpus"), "cpus")
        self.expect("telos-core", lambda s: s.__setitem__("cpus", 8), "cpus")
        self.expect("telos-core", lambda s: s.pop("mem_limit"), "memory")
        self.expect("telos-core", lambda s: s.__setitem__("mem_limit", "8g"), "memory")
        self.expect("telos-core", lambda s: s.pop("pids_limit"), "pids_limit")
        self.expect("telos-core", lambda s: s.__setitem__("pids_limit", -1), "pids_limit")
        self.expect("telos-core", lambda s: s.pop("ulimits"), "nofile")
        self.expect("telos-core", lambda s: s["ulimits"]["nofile"].__setitem__("hard", 1048576), "nofile")

    def test_logging_restart_stop_and_health(self):
        self.expect("telos-core", lambda s: s.pop("logging"), "logging")
        self.expect("telos-core", lambda s: s["logging"]["options"].pop("max-size"), "logging")
        self.expect("telos-core", lambda s: s["logging"]["options"].__setitem__("max-size", "1g"), "logging")
        self.expect("telos-core", lambda s: s["logging"].__setitem__("driver", "none"), "logging")
        self.expect("telos-core", lambda s: s.__setitem__("restart", "always"), "restart")
        self.expect("telos-migrate", lambda s: s.__setitem__("restart", "unless-stopped"), "restart")
        self.expect("telos-core", lambda s: s.__setitem__("stop_grace_period", "10s"), "stop_grace_period")
        self.expect("telos-core", lambda s: s.pop("stop_grace_period"), "stop_grace_period")
        self.expect("telos-core", lambda s: s.pop("healthcheck"), "health check")
        self.expect("telos-core", lambda s: s["healthcheck"].__setitem__("disable", True), "health check")

    def test_network_exposure_and_image_identity(self):
        self.expect("redis", lambda s: s.__setitem__("networks", ["telos-backend", "telos-ingress"]), "networks")
        self.expect("telos-core", lambda s: s.__setitem__("networks", ["telos-backend", "telos-db"]), "networks")
        self.expect("postgres", lambda s: s.__setitem__("ports", ["5432:5432"]), "published ports")
        self.expect("traefik", lambda s: s["ports"].append("8443:8443"), "published ports")
        self.expect("traefik", lambda s: s.__setitem__("ports", ["80:80", "443:443"]), "published ports")
        self.expect("postgres", lambda s: s.__setitem__("image", "docker.io/library/postgres:16.14-alpine"), "not the locked")
        self.expect("postgres", lambda s: s.__setitem__("image", "docker.io/library/postgres:latest"), "not the locked")
        self.expect("telos-core", lambda s: s.pop("build"), "build section")

    def test_service_set_must_match_in_both_directions(self):
        model = copy.deepcopy(MODEL)
        model["services"]["sidecar"] = copy.deepcopy(model["services"]["redis"])
        self.assertTrue(any(i.startswith("sidecar") and "not named by the hardening policy" in i for i in findings(model)))
        model = copy.deepcopy(MODEL)
        del model["services"]["clamav"]
        self.assertTrue(any(i.startswith("clamav") and "missing from Compose" in i for i in findings(model)))

    def test_the_postgres_and_mariadb_exceptions_are_the_only_privilege_exceptions(self):
        excepted = {name for name, rule in POLICY["services"].items() if rule["exceptions"]}
        self.assertEqual(excepted, {"postgres", "grimmory-db"})
        for name in excepted:
            self.assertEqual([e["control"] for e in POLICY["services"][name]["exceptions"]], ["noNewPrivileges"])


class Exceptions(unittest.TestCase):
    def test_an_exception_excuses_only_its_own_control(self):
        # postgres may skip no-new-privileges, but still must drop every capability.
        model = mutated("postgres", lambda s: s.__setitem__("cap_drop", []))
        self.assertTrue(any("does not drop all capabilities" in i for i in findings(model)))
        model = mutated("postgres", lambda s: s.__setitem__("security_opt", ["no-new-privileges:true"]))
        self.assertEqual(findings(model), [])  # setting it is always allowed

    def test_an_expired_exception_is_a_finding_and_no_longer_excuses(self):
        late = datetime.date(2027, 5, 1)
        result = findings(today=late)
        self.assertTrue(any("exception for noNewPrivileges expired" in i for i in result), result)
        self.assertTrue(any(i.startswith("postgres") and "no-new-privileges" in i for i in result), result)

    def test_exception_requirements(self):
        policy = copy.deepcopy(POLICY)
        policy["services"]["postgres"]["exceptions"][0]["compensating"] = []
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)
        policy = copy.deepcopy(POLICY)
        policy["services"]["postgres"]["exceptions"][0]["reason"] = " "
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)
        policy = copy.deepcopy(POLICY)
        policy["services"]["postgres"]["exceptions"][0]["control"] = "everything"
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)
        policy = copy.deepcopy(POLICY)
        policy["services"]["postgres"]["exceptions"][0]["expires"] = "someday"
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)

    def test_policy_shape_is_closed(self):
        policy = copy.deepcopy(POLICY)
        policy["services"]["redis"]["surprise"] = True
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)
        policy = copy.deepcopy(POLICY)
        policy["logBounds"]["driver"] = "journald"
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)
        policy = copy.deepcopy(POLICY)
        policy["services"]["redis"]["uid"] = 0
        with self.assertRaises(rh.PolicyError):
            rh.validate_policy(policy)


def observation(name, rule):
    """A running container that satisfies the policy exactly."""
    uid = 1000 if rule["uid"] == "app" else rule["uid"]
    image = rule["image"]
    digest = LOCK["images"][image["key"]]["manifestDigest"] if image["kind"] == "locked" else ""
    one_shot = rule["oneShot"]
    nnp_excused = any(e["control"] == "noNewPrivileges" for e in rule["exceptions"])
    return {
        "state": "exited" if one_shot else "running",
        "exitCode": 0 if one_shot else None,
        "health": "healthy" if rule["health"] else None,
        "imageId": "a" * 64,
        "imageDigest": digest,
        "procStatus": None if one_shot else {"Uid": f"{uid} {uid} {uid} {uid}", "Gid": f"{uid} {uid} {uid} {uid}",
                                             "CapEff": "0000000000000000", "NoNewPrivs": "0" if nnp_excused else "1"},
        "listeners": None if one_shot else sorted(rule["listeners"]),
        "host": {"privileged": False, "pidMode": "private", "ipcMode": "private", "networkMode": "bridge", "devices": [],
                 "readonlyRootfs": True, "memoryBytes": rule["limits"]["memoryMiB"] * (1 << 20),
                 "nanoCpus": int(rule["limits"]["cpus"] * 1e9), "pidsLimit": rule["limits"]["pids"], "nofile": rule["limits"]["nofile"],
                 "logDriver": "k8s-file", "logMaxSizeBytes": 10 << 20, "restart": rule["restart"]},
        "mounts": [{"source": f"/var/lib/containers/{v}", "destination": v, "writable": True, "type": "volume"} for v in rule["writable"]["volumes"]]
                  + [{"source": "tmpfs", "destination": t, "writable": True, "type": "tmpfs"} for t in rule["writable"]["tmpfs"]],
        "networks": sorted(rule["networks"]),
        "publishedPorts": [{"hostPort": p["published"], "containerPort": p["target"]} for p in rule["publishedPorts"]],
        "pid": 1234,
    }


def baseline_observations():
    return {name: observation(name, rule) for name, rule in POLICY["services"].items()}


def runtime_findings(observations, candidate_images=None, today=TODAY):
    return rh.check_runtime(POLICY, observations, LOCK, candidate_images, today)


class RuntimeInspection(unittest.TestCase):
    def expect(self, service, change, fragment, candidate_images=None):
        observations = baseline_observations()
        change(observations[service])
        result = runtime_findings(observations, candidate_images)
        self.assertTrue(any(fragment in item and item.startswith(service) for item in result),
                        f"{service}: {fragment!r} not reported in {result}")

    def test_baseline_is_clean(self):
        self.assertEqual(runtime_findings(baseline_observations()), [])

    def test_effective_identity_and_capabilities(self):
        self.expect("telos-core", lambda o: o["procStatus"].update(Uid="0 0 0 0", Gid="0 0 0 0"), "runs as uid 0")
        self.expect("telos-core", lambda o: o["procStatus"].update(Uid="1 1 1 1"), "the policy says 10001")
        self.expect("telos-core", lambda o: o["procStatus"].update(CapEff="0000000000001000"), "NET_ADMIN")
        self.expect("telos-core", lambda o: o["procStatus"].update(CapEff="00000000a80425fb"), "effective capabilities")
        self.expect("telos-core", lambda o: o["procStatus"].update(NoNewPrivs="0"), "NoNewPrivs")
        self.expect("telos-core", lambda o: o.update(procStatus=None), "could not be read")
        # An excused control may be absent; an unexcused one may not.
        observations = baseline_observations()
        self.assertEqual(observations["postgres"]["procStatus"]["NoNewPrivs"], "0")
        self.assertEqual(runtime_findings(observations), [])

    def test_filesystem_and_host_sharing(self):
        self.expect("telos-core", lambda o: o["host"].update(readonlyRootfs=False), "root filesystem is writable")
        self.expect("telos-core", lambda o: o["host"].update(privileged=True), "privileged")
        self.expect("telos-core", lambda o: o["host"].update(pidMode="host"), "host namespaces")
        self.expect("telos-core", lambda o: o["host"].update(networkMode="host"), "host namespaces")
        self.expect("telos-core", lambda o: o["host"].update(devices=[{"PathOnHost": "/dev/kmsg"}]), "device")
        self.expect("telos-core", lambda o: o["mounts"].append({"source": "/srv", "destination": "/extra", "writable": True, "type": "bind"}), "writable mount /extra")
        self.expect("telos-core", lambda o: o["mounts"].append({"source": "/run/podman/podman.sock", "destination": "/s", "writable": False, "type": "bind"}), "container runtime")

    def test_limits_logs_and_restart(self):
        self.expect("telos-core", lambda o: o["host"].update(memoryBytes=0), "memory limit")
        self.expect("telos-core", lambda o: o["host"].update(nanoCpus=0), "cpu limit")
        self.expect("telos-core", lambda o: o["host"].update(pidsLimit=0), "pids limit")
        self.expect("telos-core", lambda o: o["host"].update(nofile=1048576), "nofile")
        self.expect("telos-core", lambda o: o["host"].update(nofile=None), "nofile")
        self.expect("telos-core", lambda o: o["host"].update(logMaxSizeBytes=None), "log rotation")
        self.expect("telos-core", lambda o: o["host"].update(logMaxSizeBytes=1 << 30), "log rotation")
        self.expect("telos-core", lambda o: o["host"].update(logDriver="journald"), "log rotation")
        self.expect("telos-core", lambda o: o["host"].update(restart="always"), "restart policy")

    def test_exposure_listeners_and_networks(self):
        self.expect("telos-core", lambda o: o.update(listeners=[8080, 9999]), "unexpected ports [9999]")
        self.expect("telos-core", lambda o: o.update(listeners=[]), "does not listen on expected ports [8080]")
        self.expect("redis", lambda o: o.update(networks=["telos-backend", "telos-ingress"]), "attached to networks")
        self.expect("postgres", lambda o: o.update(publishedPorts=[{"hostPort": "5432", "containerPort": "5432"}]), "published ports")

    def test_state_and_health(self):
        self.expect("telos-core", lambda o: o.update(state="exited"), "expected running")
        self.expect("telos-core", lambda o: o.update(health="unhealthy"), "expected healthy")
        self.expect("telos-migrate", lambda o: o.update(exitCode=1), "one-shot")
        observations = baseline_observations()
        observations["clamav"] = None
        self.assertTrue(any(i.startswith("clamav") and "is not running" in i for i in runtime_findings(observations)))
        observations = baseline_observations()
        observations["telos-audiobook-migrate"] = None  # profile-gated: absence is fine
        self.assertEqual(runtime_findings(observations), [])
        observations = baseline_observations()
        observations["stowaway"] = observation("redis", POLICY["services"]["redis"])
        self.assertTrue(any(i.startswith("stowaway") for i in runtime_findings(observations)))

    def test_image_identity(self):
        self.expect("redis", lambda o: o.update(imageDigest="sha256:" + "b" * 64), "not the locked one")
        self.expect("redis", lambda o: o.update(imageDigest=""), "not the locked one")
        candidate = {"sha256:" + "a" * 64}
        observations = baseline_observations()
        self.assertEqual(runtime_findings(observations, candidate), [])
        self.assertTrue(any("not a candidate artifact" in i for i in runtime_findings(observations, {"sha256:" + "c" * 64})))

    def test_expired_exception_applies_at_runtime_too(self):
        late = datetime.date(2027, 5, 1)
        result = runtime_findings(baseline_observations(), today=late)
        self.assertTrue(any("expired" in i for i in result))
        self.assertTrue(any(i.startswith("postgres") and "NoNewPrivs" in i for i in result), result)


class ProcParsing(unittest.TestCase):
    def test_capability_mask_decoding(self):
        self.assertEqual(rh.decode_capabilities("0000000000000000"), set())
        self.assertEqual(rh.decode_capabilities("00000000800405fb"),
                         {"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "SETGID", "SETUID", "SETPCAP", "NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP"})

    def test_listening_sockets(self):
        tcp = ("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
               "   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0\n"
               "   1: 0100007F:0CEA 0100007F:C1D2 01 00000000:00000000 00:00000000 00000000     0        0 2 1 0\n")
        self.assertEqual(rh.parse_listening_ports(tcp), {8080})

    def test_units(self):
        self.assertEqual(rh.parse_bytes("256m"), 256 << 20)
        self.assertEqual(rh.parse_bytes("10MB"), 10 << 20)
        self.assertEqual(rh.parse_bytes("1g"), 1 << 30)
        self.assertIsNone(rh.parse_bytes("lots"))
        self.assertEqual(rh.parse_duration_seconds("75s"), 75)
        self.assertEqual(rh.parse_duration_seconds("1m30s"), 90)


class RealInspectCapture(unittest.TestCase):
    """A real `podman inspect` record (trimmed to the fields the projection reads)."""

    FIXTURE = ROOT / "tests" / "fixtures" / "runtime-hardening" / "inspect-redis.json"

    @unittest.skipUnless(FIXTURE.exists(), "no captured inspect record yet")
    def test_projection_of_a_live_redis(self):
        capture = json.loads(self.FIXTURE.read_text(encoding="utf-8"))
        projected = rh.project_inspect(capture["inspect"], capture["procStatus"], capture["tcp"])
        self.assertEqual(projected["state"], "running")
        self.assertTrue(projected["host"]["readonlyRootfs"])
        self.assertEqual(projected["host"]["logDriver"], "k8s-file")
        self.assertEqual(projected["listeners"], [6379])
        self.assertEqual(projected["procStatus"]["CapEff"], "0000000000000000")
        self.assertEqual(projected["procStatus"]["NoNewPrivs"], "1")
        observations = baseline_observations()
        observations["redis"] = projected
        remaining = [i for i in runtime_findings(observations) if i.startswith("redis")]
        self.assertEqual(remaining, [], "a live, hardened redis must satisfy the policy")


if __name__ == "__main__":
    unittest.main(verbosity=2)
