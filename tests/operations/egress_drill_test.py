#!/usr/bin/env python3
"""Controlled-egress drill: the judgement of probe results, and the node-side program itself
executed against a fake `podman` that behaves like a correctly (or incorrectly) isolated node.

Hermetic. It proves the drill's logic and result contract; it is NOT evidence about any node.
"""

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
sys.path.insert(0, str(Path(__file__).resolve().parent))
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import egress_drill as drill  # noqa: E402
import release_graph as rg  # noqa: E402

REFERENCE = drill.locked_reference()
FAKE_PODMAN = r'''#!/usr/bin/env python3
import json, os, sys
scenario = json.load(open(os.environ["FAKE_SCENARIO"]))
args = sys.argv[1:]
def out(text="", code=0):
    sys.stdout.write(text)
    raise SystemExit(code)
if args[:1] == ["version"]:
    out("5.7.0\n", 0 if scenario.get("engine", True) else 1)
if args[:1] == ["pull"]:
    out("", 1 if scenario.get("pullFails") else 0)
if args[:2] == ["image", "inspect"]:
    out("sha256:" + "a" * 64 + "\n")
if args[:3] == ["inspect", "--type", "container"]:
    name = args[3]
    if name not in scenario["running"]:
        out("[]", 1)
    out(json.dumps([{"Id": "c" * 64, "Image": "d" * 64, "State": {"Running": True},
                     "NetworkSettings": {"Networks": {"telos-backend": {}}}}]))
if args[:2] == ["network", "inspect"]:
    internal = scenario["internalNetworks"]
    name = args[2]
    out(json.dumps([{"id": "n" * 12 + name[-4:].replace("-", "x"), "internal": name in internal}]))
if args[:1] == ["exec"]:
    token = scenario["token"]
    line = "1760000000.000 10 10.89.1.5 TCP_MISS/301 400 GET http://openlibrary.org/" + ("?telos_probe=" + token if scenario.get("leak") else "") + " - HIER_DIRECT/1.2.3.4 -\n"
    out(line if scenario.get("logs", True) else "")
if args[:1] == ["run"]:
    namespace = next(a.split(":", 1)[1] for a in args if a.startswith("container:"))
    curl = args[args.index("curl") + 1:]
    url = curl[-1]
    proxied = "-x" in curl
    if not proxied and namespace == "telos-egress-proxy":
        out("000" if scenario.get("noInternet") else "200", 56 if scenario.get("noInternet") else 0)
    if not proxied:
        leaks = scenario.get("directLeaks", [])
        label = "direct-hostname" if "openlibrary.org" in url else "direct-ip"
        if f"{namespace}:{label}" in leaks:
            out("200", 0)
        out("000", 7)
    if "telos_probe" in url:
        out("301", 0)
    allowed = url == "https://openlibrary.org/"
    if allowed:
        out("000" if scenario.get("noInternet") else "200", 56 if scenario.get("noInternet") else 0)
    breaches = scenario.get("proxyAllows", [])
    if any(b in url for b in breaches):
        out("200", 0)
    out("403", 0)
out("", 0)
'''


class Node:
    """A temp directory with a fake podman and a scenario file."""

    def __init__(self, **scenario):
        self.dir = Path(tempfile.mkdtemp())
        self.scenario = {"running": list(drill.INTERNAL_ONLY) + [drill.PROXY], "internalNetworks": ["telos-backend", "telos-db", "telos-observability"], "token": "unset"}
        self.scenario.update(scenario)

    def run(self, token="tok-test"):
        self.scenario["token"] = token
        bin_dir = self.dir / "bin"
        bin_dir.mkdir(exist_ok=True)
        podman = bin_dir / "podman"
        podman.write_text(FAKE_PODMAN)
        podman.chmod(podman.stat().st_mode | stat.S_IEXEC)
        (self.dir / "scenario.json").write_text(json.dumps(self.scenario))
        program = drill.remote_program(REFERENCE, token)
        completed = subprocess.run([sys.executable, "-I", "-"], input=program, capture_output=True, check=True,
                                   env={"PATH": f"{bin_dir}:/usr/bin:/bin", "FAKE_SCENARIO": str(self.dir / "scenario.json")})
        return json.loads(completed.stdout)

    def close(self):
        import shutil
        shutil.rmtree(self.dir, ignore_errors=True)


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


class ProgramAndJudgementTests(unittest.TestCase):
    def evaluate(self, **scenario):
        node = Node(**scenario)
        self.addCleanup(node.close)
        return statuses(drill.evaluate(node.run(), REFERENCE))

    def test_a_correctly_isolated_node_passes_every_assertion(self):
        result = self.evaluate()
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})
        self.assertEqual(set(result), {"egress.probe-image", "egress.identity", "egress.direct", "egress.allowed", "egress.denied", "egress.redaction"})

    def test_a_direct_path_to_the_internet_from_any_internal_service_fails(self):
        for service in ("telos-jellyfin", "telos-postgres", "telos-grimmory-db"):
            for label in ("direct-hostname", "direct-ip"):
                result = self.evaluate(directLeaks=[f"{service}:{label}"])
                self.assertEqual(result["egress.direct"], "failed", (service, label))

    def test_a_proxy_that_allows_something_it_should_refuse_fails(self):
        for breach in ("example.com", "93.184.216.34", "10.0.0.1", "169.254.169.254", ":8443"):
            self.assertEqual(self.evaluate(proxyAllows=[breach])["egress.denied"], "failed", breach)

    def test_a_query_token_in_the_access_log_fails_and_an_empty_log_is_not_a_pass(self):
        self.assertEqual(self.evaluate(leak=True)["egress.redaction"], "failed")
        self.assertEqual(self.evaluate(logs=False)["egress.redaction"], "failed", "the request must appear in the log")

    def test_a_node_without_internet_cannot_show_the_allowed_path(self):
        result = self.evaluate(noInternet=True)
        self.assertEqual(result["egress.allowed"], "not_run")
        self.assertEqual(result["egress.denied"], "passed")

    def test_a_missing_service_or_wrongly_routable_network_fails_identity(self):
        self.assertEqual(self.evaluate(running=[n for n in drill.INTERNAL_ONLY if n != "telos-clamav"] + [drill.PROXY])["egress.identity"], "failed")
        self.assertEqual(self.evaluate(internalNetworks=["telos-db", "telos-observability"])["egress.identity"], "failed")
        self.assertEqual(self.evaluate(running=list(drill.INTERNAL_ONLY))["egress.identity"], "failed", "no proxy is running")

    def test_no_engine_or_no_probe_image_is_not_run(self):
        self.assertEqual(self.evaluate(engine=False), {"egress.engine": "not_run"})
        self.assertEqual(self.evaluate(pullFails=True), {"egress.probe-image": "not_run"})

    def test_the_probe_must_be_the_locked_image(self):
        node = Node()
        self.addCleanup(node.close)
        result = node.run()
        result["probeImage"]["reference"] = "docker.io/curlimages/curl:latest"
        self.assertEqual(statuses(drill.evaluate(result, REFERENCE))["egress.probe-image"], "failed")

    def test_the_node_program_returns_no_log_text_and_no_secret(self):
        node = Node(leak=True)
        self.addCleanup(node.close)
        text = json.dumps(node.run(token="tok-secret-1234"))
        self.assertNotIn("tok-secret-1234", text)
        self.assertNotIn("HIER_DIRECT", text)

    def test_the_locked_probe_image_is_pinned_by_digest(self):
        self.assertRegex(REFERENCE, r"^docker\.io/curlimages/curl:[0-9.]+@sha256:[0-9a-f]{64}$")


class RunTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        self.target_file = base.TargetTests().make(self.dir)
        self.out = self.dir / "out"
        self.out.mkdir()
        self.nodes = []

    def runner_for(self, **scenario):
        node = Node(**scenario)
        self.nodes.append(node)
        self.addCleanup(node.close)
        calls = []

        def run(target_file, program, timeout=0):
            import ast
            calls.append(target_file)
            config = json.loads(ast.literal_eval(program.decode().split("\n", 1)[0].split("CFG_JSON = ", 1)[1]))
            return node.run(config["token"])
        return run

    def remote_factory(self):
        facts = base.good_facts()

        class Fake:
            def __init__(self, target):
                pass

            def collect(self, *arguments, **kw):
                return facts
        return Fake

    def invoke(self, runner, verifier=base.VERIFIER_HOST):
        return drill.run(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--result-out", str(self.out / "result.json")],
                         remote_factory=self.remote_factory(), runner=runner, verifier_identity=lambda: verifier)

    def test_a_node_without_a_generation_receipt_records_its_observations_but_is_not_run(self):
        self.assertEqual(self.invoke(self.runner_for()), 3)
        result = rg.verify_result_directory(self.out, "result.json")
        self.assertEqual((result["status"], result["subjectFile"], result["gateId"]), ("not_run", None, "controlled-egress"))
        self.assertIn("subject.runtime", result["reason"])
        assertions = json.loads((self.out / "assertions.json").read_text())["assertions"]
        probes = [a for a in assertions if a["id"].startswith("egress.")]
        self.assertTrue(probes and all(a["status"] == "passed" for a in probes), "the probes themselves passed")

    def test_a_breach_fails_the_gate(self):
        self.assertEqual(self.invoke(self.runner_for(directLeaks=["telos-jellyfin:direct-ip"])), 1)
        self.assertIn("egress.direct", rg.verify_result_directory(self.out, "result.json")["reason"])

    def test_the_verifier_may_not_be_the_node(self):
        with self.assertRaisesRegex(dc.NotRun, "node itself"):
            self.invoke(self.runner_for(), verifier=base.NODE_HOST)

    def test_a_receipt_and_pinned_images_yield_a_runtime_subject(self):
        facts = base.good_facts()
        facts["node"]["receipt"] = {"present": True, "digest": "sha256:" + "9" * 64}
        facts["host"]["bootId"] = "11111111-2222-3333-4444-555555555555"
        for container in facts["containers"]:
            container["image"] = f"docker.io/example/{container['name']}@sha256:{'7' * 64}"
            container["imageId"] = "8" * 64
        subject, why = dc.build_runtime_subject(facts, base.COMMIT, None)
        self.assertIsNone(why)
        rg.validate_record("observed-subject", subject)
        self.assertEqual(subject["generationReceiptDigest"], "sha256:" + "9" * 64)
        facts["containers"][0]["image"] = "localhost/telos_telos-core:latest"
        unpinned, why = dc.build_runtime_subject(facts, base.COMMIT, None)
        self.assertIsNone(unpinned)
        self.assertIn("without a registry manifest digest", why)


class ShellTests(unittest.TestCase):
    def test_without_a_target_the_gate_is_not_run(self):
        completed = subprocess.run(["bash", str(ROOT / "scripts/drills/egress.sh"), "--rehearsal", "--source-commit", base.COMMIT, "--result-out", "/nonexistent/r.json"],
                                   capture_output=True, text=True, check=False)
        self.assertEqual(completed.returncode, 3, completed.stderr)

    def test_unknown_arguments_are_refused(self):
        self.assertEqual(subprocess.run(["bash", str(ROOT / "scripts/drills/egress.sh"), "--bogus"], capture_output=True, check=False).returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=1)
