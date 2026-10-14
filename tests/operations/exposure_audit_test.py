#!/usr/bin/env python3
"""Host exposure audit: policy validation, the static Compose check, the node's own report, the
external scan, and the whole live drill with injected collector and scanner.

Hermetic. It proves the judgement and the result contract; it is NOT evidence about any host.
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
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import exposure_audit as audit  # noqa: E402
import release_graph as rg  # noqa: E402

POLICY_PATH = ROOT / "config" / "exposure-policy.json"
NODE_HOST = base.NODE_HOST
VERIFIER = base.VERIFIER_HOST
COMMIT = base.COMMIT


def policy():
    return json.loads(POLICY_PATH.read_text())


def good_facts():
    facts = base.good_facts()
    facts["mode"] = "exposure"
    facts["host"]["machineIdSha256"] = NODE_HOST
    facts["listeners"] = [{"address": "0.0.0.0", "port": 80, "process": "conmon"}, {"address": "0.0.0.0", "port": 443, "process": "conmon"},
                          {"address": "0.0.0.0", "port": 22, "process": "sshd"}, {"address": "127.0.0.1", "port": 5432, "process": "postgres"}]
    facts["udpListeners"] = [{"address": "0.0.0.0", "port": 68, "process": "systemd-network"}, {"address": "127.0.0.53", "port": 53, "process": "systemd-resolve"}]
    facts["networks"] = [{"name": "telos-ingress", "internal": False, "driver": "bridge", "subnets": ["10.201.0.0/24"]},
                         {"name": "telos-backend", "internal": True, "driver": "bridge", "subnets": []},
                         {"name": "telos-db", "internal": True, "driver": "bridge", "subnets": []},
                         {"name": "telos-observability", "internal": True, "driver": "bridge", "subnets": ["10.202.0.0/24"]},
                         {"name": "telos-egress", "internal": False, "driver": "bridge", "subnets": []}]
    facts["addresses"] = ["192.0.2.10"]
    for container in facts["containers"]:
        container["networks"] = {"telos-traefik": ["telos-ingress", "telos-backend"], "telos-client": ["telos-ingress"],
                                 "telos-core": ["telos-ingress", "telos-backend", "telos-db", "telos-observability"],
                                 "telos-egress-proxy": ["telos-egress", "telos-backend"]}.get(container["name"], ["telos-backend"])
    return facts


def declared(policy_document, tcp=(22,), udp=(68,)):
    policy_document["operatorDeclared"] = {"tcp": list(tcp), "udp": list(udp)}
    return policy_document


GOOD_SCAN = {"scanner": "nmap", "scannerVersion": "7.98", "target": "192.0.2.10", "tcp": [22, 80, 443], "tcpPortsScanned": 65535, "udp": [68], "udpPortsScanned": 19}


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


class PolicyTests(unittest.TestCase):
    def write(self, document):
        handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        self.addCleanup(os.unlink, handle.name)
        json.dump(document, handle)
        handle.close()
        return handle.name

    def test_the_shipped_policy_loads(self):
        document = audit.load_policy(str(POLICY_PATH))
        self.assertEqual(document["telos"]["publicTcp"], [80, 443])
        self.assertEqual(document["telos"]["publicUdp"], [])

    def test_each_malformation_is_refused(self):
        def mutate(change):
            document = policy()
            change(document)
            with self.assertRaises(audit.PolicyError):
                audit.load_policy(self.write(document))
        mutate(lambda d: d["telos"].update(publicTcp=[80, 443, 8080]))
        mutate(lambda d: d["telos"].update(publicUdp=[443]))
        mutate(lambda d: d["telos"].update(publicTcp=[80]))
        mutate(lambda d: d["operatorDeclared"].update(tcp=[5432]))          # a Telos-internal port as "host management"
        mutate(lambda d: d["operatorDeclared"].update(tcp=[443]))
        mutate(lambda d: d["operatorDeclared"].update(udp=[9090]))
        mutate(lambda d: d["operatorDeclared"].update(tcp=["22"]))
        mutate(lambda d: d["scan"].update(tcp="1-1024"))
        mutate(lambda d: d["scan"].update(udp="all"))
        mutate(lambda d: d.update(extra=1))
        mutate(lambda d: d["telos"]["neverReachable"].append({"port": 443, "what": "x"}))
        with self.assertRaises(audit.PolicyError):
            audit.load_policy("/nonexistent/policy.json")


class SourceTests(unittest.TestCase):
    def compose(self, text, name="docker-compose.yml"):
        directory = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(directory, ignore_errors=True))
        path = Path(directory) / name
        path.write_text(text)
        return str(path)

    def problems(self, text, name="docker-compose.yml"):
        return audit.check_source(policy(), [self.compose(text, name)])

    def test_the_real_compose_files_pass(self):
        files = [str(ROOT / n) for n in ("docker-compose.yml", "docker-compose.setup.yml", "docker-compose.dev.yml", "docker-compose.test.yml")]
        self.assertEqual(audit.check_source(policy(), files), [])

    def test_only_traefik_may_publish_and_only_tcp_80_443(self):
        ok = 'services:\n  traefik:\n    container_name: telos-traefik\n    ports:\n      - "${TRAEFIK_HTTP_PORT:-80}:8080"\n      - "${TRAEFIK_HTTPS_PORT:-443}:8443"\n'
        self.assertEqual(self.problems(ok), [])
        self.assertTrue(self.problems(ok + '  jellyfin:\n    container_name: telos-jellyfin\n    ports:\n      - "8096:8096"\n'))
        self.assertTrue(self.problems(ok.replace("-443}", "-8443}")))
        self.assertTrue(self.problems(ok + '      - "443:8443/udp"\n'))
        self.assertTrue(self.problems(ok.replace('"${TRAEFIK_HTTP_PORT:-80}:8080"', '"10.0.0.5:80:8080"')))

    def test_overlays_may_publish_on_loopback_only(self):
        self.assertEqual(self.problems('services:\n  x:\n    container_name: telos-x\n    ports:\n      - "127.0.0.1:8096:8096"\n', "docker-compose.setup.yml"), [])
        self.assertTrue(self.problems('services:\n  x:\n    container_name: telos-x\n    ports:\n      - "8096:8096"\n', "docker-compose.setup.yml"))
        self.assertTrue(self.problems('services:\n  x:\n    container_name: telos-x\n    ports:\n      - "0.0.0.0:8096:8096"\n', "docker-compose.dev.yml"))


class LocalFactsTests(unittest.TestCase):
    def evaluate(self, facts, policy_document=None):
        return statuses(audit.evaluate_local(policy_document or declared(policy()), facts))

    def test_a_correct_node_passes(self):
        self.assertEqual({k: v for k, v in self.evaluate(good_facts()).items() if v != "passed"}, {})

    def test_each_defect_fails_its_own_assertion(self):
        def container(facts, name):
            return next(c for c in facts["containers"] if c["name"] == name)
        cases = {
            "exposure.publishers": [lambda f: container(f, "telos-jellyfin").update(ports=[{"containerPort": 8096, "protocol": "tcp", "hostIp": "", "hostPort": 8096}]),
                                    lambda f: container(f, "telos-traefik")["ports"].append({"containerPort": 443, "protocol": "udp", "hostIp": "", "hostPort": 443}),
                                    lambda f: container(f, "telos-traefik")["ports"].pop(),
                                    lambda f: container(f, "telos-traefik")["ports"][0].update(hostIp="192.0.2.1"), lambda f: f.update(containers=None)],
            "exposure.setup": [lambda f: f["containers"].append(dict(base.container("telos-setup-proxy"), telos=True)),
                               lambda f: f["listeners"].append({"address": "0.0.0.0", "port": 8096, "process": "conmon"})],
            "exposure.networks": [lambda f: f["networks"][1].update(internal=False), lambda f: f["networks"][2].update(internal=False),
                                  lambda f: container(f, "telos-core")["networks"].append("telos-egress"),
                                  lambda f: container(f, "telos-clamav").update(networks=["telos-ingress"])],
            "exposure.listeners": [lambda f: f["listeners"].append({"address": "0.0.0.0", "port": 9999, "process": "x"}),
                                   lambda f: f["listeners"].append({"address": "::", "port": 8080, "process": "x"}),
                                   lambda f: f["udpListeners"].append({"address": "0.0.0.0", "port": 5353, "process": "avahi-daemon"}),
                                   lambda f: f.update(listeners=None)],
        }
        for expected, changes in cases.items():
            for index, change in enumerate(changes):
                facts = good_facts()
                change(facts)
                failed = sorted(k for k, v in self.evaluate(facts).items() if v == "failed")
                self.assertIn(expected, failed, f"{expected} mutation {index} failed {failed}")

    def test_an_undeclared_management_port_is_a_failure_and_a_declared_one_is_not(self):
        facts = good_facts()
        self.assertEqual(self.evaluate(facts, policy())["exposure.listeners"], "failed", "SSH and DHCP undeclared")
        self.assertEqual(self.evaluate(facts, declared(policy()))["exposure.listeners"], "passed")


class ScanTests(unittest.TestCase):
    def evaluate(self, scan, facts=None, declared_tcp=(22,), declared_udp=(68,)):
        return statuses(audit.evaluate_scan(declared(policy(), declared_tcp, declared_udp), scan, facts or good_facts()))

    def test_a_clean_scan_passes(self):
        self.assertEqual({k: v for k, v in self.evaluate(GOOD_SCAN).items() if v != "passed"}, {})

    def test_each_scan_defect_fails(self):
        def with_scan(**changes):
            return dict(GOOD_SCAN, **changes)
        self.assertEqual(self.evaluate(with_scan(tcp=[22, 80, 443, 5432]))["scan.tcp"], "failed")
        self.assertEqual(self.evaluate(with_scan(tcp=[22, 80, 443, 8080]))["scan.tcp"], "failed")
        self.assertEqual(self.evaluate(with_scan(tcp=[22, 80]))["scan.tcp"], "failed", "443 must answer")
        self.assertEqual(self.evaluate(with_scan(tcp=[80, 443, 2222]))["scan.tcp"], "failed")
        self.assertEqual(self.evaluate(with_scan(tcpPortsScanned=1000))["scan.tcp"], "failed", "an incomplete scan proves nothing")
        self.assertEqual(self.evaluate(with_scan(udp=[68, 1900]))["scan.udp"], "failed")
        self.assertEqual(self.evaluate(with_scan(udpPortsScanned=0))["scan.udp"], "failed")
        facts = good_facts()
        facts["listeners"] = [l for l in facts["listeners"] if l["port"] != 22]
        self.assertEqual(self.evaluate(GOOD_SCAN, facts)["scan.agreement"], "failed", "open from outside but not listening on the node")

    def test_a_missing_scanner_is_not_run_never_passed(self):
        result = self.evaluate({"tcp": None, "tcpSkipped": "no nmap", "udp": None, "udpSkipped": "no nmap"})
        self.assertEqual((result["scan.tcp"], result["scan.udp"]), ("not_run", "not_run"))

    def test_nmap_xml_is_parsed_into_open_ports(self):
        xml = ('<?xml version="1.0"?><nmaprun version="7.98"><scaninfo type="connect" protocol="tcp" numservices="65535" services="1-65535"/>'
               '<host><ports><port protocol="tcp" portid="80"><state state="open"/></port><port protocol="tcp" portid="22"><state state="closed"/></port>'
               '<port protocol="udp" portid="68"><state state="open|filtered"/></port><port protocol="udp" portid="123"><state state="open"/></port></ports></host></nmaprun>')
        opened, scanned, version = audit.parse_nmap_xml(xml)
        self.assertEqual((opened["tcp"], opened["udp"], scanned["tcp"], version), ({80}, {123}, 65535, "7.98"))


class LiveTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        self.target_file = base.TargetTests().make(self.dir)
        self.policy_file = self.dir / "policy.json"
        self.policy_file.write_text(json.dumps(declared(policy())))
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
                return {"host": {"machineIdSha256": facts["host"]["machineIdSha256"]}} if arguments[0] == "identity" else facts
        return Fake

    def invoke(self, facts=None, scan=GOOD_SCAN, verifier=VERIFIER, resolver=lambda host: "192.0.2.10", extra=()):
        facts = facts or good_facts()
        scanner = lambda address, policy_document, timeout: dict(scan)  # noqa: E731
        return audit.command_live(["--target-file", self.target_file, "--policy", str(self.policy_file), "--rehearsal", "--source-commit", COMMIT,
                                   "--result-out", str(self.out / "result.json"), *extra], remote_factory=self.remote_factory(facts), scanner=scanner,
                                  verifier_identity=lambda: verifier, resolver=resolver)

    def result(self):
        return rg.verify_result_directory(self.out, "result.json")

    def test_a_correct_node_passes_with_a_verifiable_result_and_subject(self):
        self.assertEqual(self.invoke(), 0)
        result = self.result()
        self.assertEqual((result["status"], result["gateId"], result["recordKind"]), ("passed", "host-exposure", "rehearsal"))
        subject = rg.load_record("observed-subject", (self.out / "subject.json").read_bytes())
        rg.validate_subject_for_result(result, subject)
        self.assertEqual(subject["hosts"][0]["hostId"], NODE_HOST)
        self.assertEqual(self.calls[0], ("identity",))
        self.assertEqual(self.calls[1][:2], ("exposure", "/var/lib/telos"))

    def test_an_exposed_port_fails_and_blocks_the_subject(self):
        self.assertEqual(self.invoke(scan=dict(GOOD_SCAN, tcp=[22, 80, 443, 5432])), 1)
        result = self.result()
        self.assertEqual((result["status"], result["subjectFile"]), ("failed", None))
        self.assertIn("scan.tcp", result["reason"])

    def test_the_scanner_may_not_be_the_node_and_the_name_must_resolve_to_it(self):
        with self.assertRaisesRegex(dc.NotRun, "node itself"):
            self.invoke(verifier=NODE_HOST)
        self.assertEqual(self.invoke(resolver=lambda host: "198.51.100.7"), 1)
        self.assertIn("exposure.identity", self.result()["reason"])

    def test_no_scanner_is_not_run(self):
        self.assertEqual(self.invoke(scan={"tcp": None, "tcpSkipped": "no nmap", "udp": None, "udpSkipped": "no nmap"}), 3)
        self.assertEqual(self.result()["status"], "not_run")


class ShellTests(unittest.TestCase):
    def run_script(self, *args):
        return subprocess.run(["bash", str(ROOT / "scripts/audit-host-exposure.sh"), *args], capture_output=True, text=True, check=False)

    def test_source_mode_passes_on_the_repository(self):
        self.assertEqual(self.run_script("--source", "--policy", str(POLICY_PATH)).returncode, 0)

    def test_source_mode_fails_on_a_bad_policy_and_a_bad_compose(self):
        with tempfile.TemporaryDirectory() as directory:
            bad = Path(directory) / "policy.json"
            document = policy()
            document["telos"]["publicTcp"] = [80, 443, 22]
            bad.write_text(json.dumps(document))
            self.assertEqual(self.run_script("--source", "--policy", str(bad)).returncode, 2)
            compose = Path(directory) / "docker-compose.yml"
            compose.write_text('services:\n  db:\n    container_name: telos-postgres\n    ports:\n      - "5432:5432"\n')
            self.assertEqual(self.run_script("--source", "--policy", str(POLICY_PATH), "--compose", str(compose)).returncode, 1)

    def test_without_a_request_the_gate_is_not_run(self):
        completed = self.run_script("--rehearsal", "--source-commit", COMMIT, "--result-out", "/nonexistent/r.json")
        self.assertEqual(completed.returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=1)
