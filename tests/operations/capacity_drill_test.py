#!/usr/bin/env python3
"""The capacity drill: the judgement of a k6 summary and of the node's resource samples, the
sampler's cgroup parsing, the fixture preparer's safety rules and flow, and the whole drill with an
injected load generator. Hermetic -- it proves logic and contract, it is NOT evidence about any node.
"""

import copy
import io
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
sys.path.insert(0, str(ROOT / "scripts"))
sys.path.insert(0, str(Path(__file__).resolve().parent))
import clean_install_drill_test as base  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import capacity_drill as drill  # noqa: E402

import importlib.util  # noqa: E402


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


collector = load_script("collect_runtime_stats", "collect-runtime-stats.py")
preparer = load_script("prepare_capacity_fixture", "prepare-capacity-fixture.py")
GB = 1024 ** 3
SECONDS = 1800


def good_summary():
    return {"metrics": {
        "http_req_failed": {"value": 0.002, "thresholds": {"rate<0.01": False}},
        "http_req_duration": {"p(95)": 210.0, "thresholds": {"p(95)<750": False}},
        "http_req_duration{kind:mutation}": {"p(95)": 300.0, "thresholds": {"p(95)<1000": False}},
        "checks": {"value": 0.9995, "thresholds": {"rate>0.99": False}},
        "semantic_failures": {"count": 0, "thresholds": {"count==0": False}},
        "iterations": {"count": 17000},
    }}


def sample(t, **changes):
    row = {"t": 1000.0 + t,
           "containers": {
               "telos-core": {"status": "running", "health": "healthy", "restarts": 0, "oomKilled": False, "cpuUsec": int(t * 0.5e6), "cpuQuotaCores": 2.0,
                              "memoryBytes": 300 * 1024 ** 2, "memoryLimitBytes": GB, "oomKills": 0},
               "telos-postgres": {"status": "running", "health": "healthy", "restarts": 0, "oomKilled": False, "cpuUsec": int(t * 0.3e6), "cpuQuotaCores": 2.0,
                                  "memoryBytes": 500 * 1024 ** 2, "memoryLimitBytes": 2 * GB, "oomKills": 0},
               "telos-redis": {"status": "running", "health": "healthy", "restarts": 0, "oomKilled": False, "cpuUsec": int(t * 0.05e6), "cpuQuotaCores": 0.5,
                               "memoryBytes": 30 * 1024 ** 2, "memoryLimitBytes": 512 * 1024 ** 2, "oomKills": 0},
               "telos-traefik": {"status": "running", "health": "healthy", "restarts": 0, "oomKilled": False, "cpuUsec": int(t * 0.1e6), "cpuQuotaCores": 1.0,
                                 "memoryBytes": 80 * 1024 ** 2, "memoryLimitBytes": 256 * 1024 ** 2, "oomKills": 0},
           },
           "storage": {"shared": {"freeBytes": 60 * GB, "totalBytes": 100 * GB}, "node": {"freeBytes": 60 * GB, "totalBytes": 100 * GB}},
           "redis": {"usedBytes": 20 * 1024 ** 2, "maxBytes": 384 * 1024 ** 2},
           "gateway": {"dbPoolAcquired": 6, "dbPoolMax": 20, "activeStreams": 2, "streamMax": 100},
           "memberSessions": 25}
    row.update(changes)
    return row


def good_samples(seconds=SECONDS, step=5):
    return [sample(t) for t in range(0, seconds + 1, step)]


def statuses(assertions):
    return {a["id"]: a["status"] for a in assertions}


def mutate(samples, change, start=None, end=None):
    out = copy.deepcopy(samples)
    for index, row in enumerate(out):
        if (start is None or row["t"] - 1000 >= start) and (end is None or row["t"] - 1000 <= end):
            change(row, index)
    return out


SERVICES = ["telos-core", "telos-postgres", "telos-redis", "telos-traefik"]


class K6Tests(unittest.TestCase):
    def test_a_good_run_passes(self):
        result = statuses(drill.evaluate_k6(good_summary(), SECONDS))
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})

    def test_each_breach_fails_its_assertion(self):
        def run(change, seconds=SECONDS):
            summary = good_summary()
            change(summary["metrics"])
            return statuses(drill.evaluate_k6(summary, seconds))
        self.assertEqual(run(lambda m: m["http_req_failed"].update(value=0.011))["capacity.latency"], "failed")
        self.assertEqual(run(lambda m: m["http_req_duration"].update({"p(95)": 751.0}))["capacity.latency"], "failed")
        self.assertEqual(run(lambda m: m["http_req_duration{kind:mutation}"].update({"p(95)": 1000.0}))["capacity.latency"], "failed")
        self.assertEqual(run(lambda m: m["checks"].update(value=0.98))["capacity.semantics"], "failed")
        self.assertEqual(run(lambda m: m["semantic_failures"].update(count=1))["capacity.semantics"], "failed")
        self.assertEqual(run(lambda m: m["iterations"].update(count=100))["capacity.volume"], "failed")

    def test_weakened_or_missing_thresholds_fail(self):
        def run(change):
            summary = good_summary()
            change(summary["metrics"])
            return statuses(drill.evaluate_k6(summary, SECONDS))["capacity.thresholds"]
        self.assertEqual(run(lambda m: m["http_req_duration"].update(thresholds={"p(95)<5000": False})), "failed")
        self.assertEqual(run(lambda m: m["http_req_failed"].update(thresholds={})), "failed")
        self.assertEqual(run(lambda m: m["http_req_failed"].update(thresholds={"rate<0.01": False, "rate<0.5": False})), "failed")
        self.assertEqual(run(lambda m: m.pop("semantic_failures")), "failed")

    def test_a_malformed_or_absent_summary_fails_and_a_short_run_cannot_pass(self):
        self.assertEqual(statuses(drill.evaluate_k6(None, SECONDS)), {"capacity.k6": "failed"})
        summary = good_summary()
        summary["metrics"]["http_req_duration"]["p(95)"] = "fast"
        self.assertEqual(statuses(drill.evaluate_k6(summary, SECONDS))["capacity.k6"], "failed")
        short = statuses(drill.evaluate_k6(good_summary(), 600))
        self.assertEqual(short["capacity.duration"], "not_run")

    def test_the_load_script_declares_the_required_shape(self):
        script = (ROOT / "tests" / "load" / "telos.js").read_text()
        for needle in ('"constant-vus"', "vus: SESSIONS", "const SESSIONS = 25", "WRITES_PER_SECOND = 10", 'http_req_failed: ["rate<0.01"]',
                       'http_req_duration: ["p(95)<750"]', '"http_req_duration{kind:mutation}": ["p(95)<1000"]', "semantic_failures", "PHASES = 4"):
            self.assertIn(needle, script)
        self.assertEqual({t for t in drill.REQUIRED_THRESHOLDS["http_req_duration"]}, {"p(95)<750"})

    def test_the_load_script_is_valid_javascript(self):
        node = subprocess.run(["bash", "-c", "command -v node"], capture_output=True, text=True).stdout.strip() or str(Path.home() / ".local/telos-toolchain/node/bin/node")
        if not os.path.exists(node):
            self.skipTest("node is not available")
        completed = subprocess.run([node, "--check", str(ROOT / "tests" / "load" / "telos.js")], capture_output=True, text=True)
        self.assertEqual(completed.returncode, 0, completed.stderr)


class SampleTests(unittest.TestCase):
    def evaluate(self, samples, seconds=SECONDS):
        return statuses(drill.evaluate_samples(samples, seconds, SERVICES))

    def test_a_healthy_run_passes_every_assertion(self):
        result = self.evaluate(good_samples())
        self.assertEqual({k: v for k, v in result.items() if v != "passed"}, {})
        self.assertEqual(set(result), {"capacity.samples", "capacity.stability", "capacity.cpu", "capacity.memory", "capacity.storage", "capacity.saturation", "capacity.sessions"})

    def test_a_one_shot_that_exited_is_not_a_lost_service_but_a_stopped_gateway_is(self):
        samples = good_samples()
        done = lambda r, i: r["containers"].update({"telos-migrate": {"status": "exited", "health": None, "restarts": 0, "oomKilled": False, "cpuUsec": 0,  # noqa: E731
                                                                      "cpuQuotaCores": None, "memoryBytes": None, "memoryLimitBytes": None, "oomKills": None}})
        self.assertEqual(self.evaluate(mutate(samples, done))["capacity.stability"], "passed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-core"].update(status="exited"), start=900, end=905))["capacity.stability"], "failed")

    def test_missing_samples_fail(self):
        self.assertEqual(self.evaluate(good_samples()[::3])["capacity.samples"], "failed")
        self.assertEqual(self.evaluate([])["capacity.samples"], "failed")

    def test_restarts_oom_health_and_absence_fail_stability(self):
        samples = good_samples()
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-core"].update(restarts=1), start=900))["capacity.stability"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-core"].update(oomKilled=True), start=900, end=905))["capacity.stability"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-redis"].update(oomKills=1), start=900))["capacity.stability"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-postgres"].update(health="unhealthy"), start=500, end=505))["capacity.stability"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"].pop("telos-traefik")))["capacity.stability"], "failed")

    def test_cpu_over_eighty_percent_for_a_full_minute_fails_but_a_short_spike_does_not(self):
        def burn(rows, start, end, cores):
            def change(row, index):
                row["containers"]["telos-core"]["cpuUsec"] = int(1000000 * (start * 0.5 + (row["t"] - 1000 - start) * cores + 0))
            return change
        samples = good_samples()
        # 1.7 cores of a 2-core quota (85 %) from t=600 to t=700, then back to the usual 0.5 -- a 100 s window
        usage, hot = 0.0, []
        for row in samples:
            t = row["t"] - 1000
            rate = 1.7 if 600 <= t < 700 else 0.5
            usage += rate * 5
            hot.append(int(usage * 1e6))
        out = copy.deepcopy(samples)
        for row, value in zip(out, hot):
            row["containers"]["telos-core"]["cpuUsec"] = value
        self.assertEqual(self.evaluate(out)["capacity.cpu"], "failed")
        usage, brief = 0.0, []
        for row in samples:
            t = row["t"] - 1000
            rate = 1.9 if 600 <= t < 625 else 0.5   # 25 s spike
            usage += rate * 5
            brief.append(int(usage * 1e6))
        out = copy.deepcopy(samples)
        for row, value in zip(out, brief):
            row["containers"]["telos-core"]["cpuUsec"] = value
        self.assertEqual(self.evaluate(out)["capacity.cpu"], "passed")

    def test_memory_storage_each_fail_at_their_boundary(self):
        samples = good_samples()
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-redis"].update(memoryBytes=int(0.85 * 512 * 1024 ** 2) + 1), start=300, end=300))["capacity.memory"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["containers"]["telos-redis"].update(memoryBytes=int(0.84 * 512 * 1024 ** 2)), start=300, end=300))["capacity.memory"], "passed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["storage"]["shared"].update(freeBytes=9 * GB)))["capacity.storage"], "failed")
        small = mutate(samples, lambda r, i: r["storage"]["node"].update(freeBytes=int(0.099 * 500 * GB), totalBytes=500 * GB))
        self.assertEqual(self.evaluate(small)["capacity.storage"], "failed", "10 % of a large filesystem beats the 10 GiB floor")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["storage"]["node"].update(freeBytes=None)))["capacity.storage"], "failed")

    def test_saturation_must_be_sustained_for_a_minute_to_fail(self):
        samples = good_samples()
        pool = lambda r, i: r["gateway"].update(dbPoolAcquired=20)   # noqa: E731
        self.assertEqual(self.evaluate(mutate(samples, pool, start=400, end=455))["capacity.saturation"], "passed", "55 s at the maximum is not a minute")
        self.assertEqual(self.evaluate(mutate(samples, pool, start=400, end=460))["capacity.saturation"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["redis"].update(usedBytes=384 * 1024 ** 2), start=100, end=200))["capacity.saturation"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r["gateway"].update(activeStreams=100), start=100, end=200))["capacity.saturation"], "failed")
        # an unreported limit proves nothing: not_run, never passed
        blind = mutate(samples, lambda r, i: r["gateway"].update(dbPoolMax=None, dbPoolAcquired=None))
        self.assertEqual(self.evaluate(blind)["capacity.saturation"], "not_run")

    def test_exactly_twenty_five_sessions_never_more(self):
        samples = good_samples()
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r.update(memberSessions=26), start=300, end=300))["capacity.sessions"], "failed")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r.update(memberSessions=24)))["capacity.sessions"], "failed", "never reached 25")
        brief_dip = mutate(samples, lambda r, i: r.update(memberSessions=24), start=600, end=600)
        self.assertEqual(self.evaluate(brief_dip)["capacity.sessions"], "passed", "a rotation instant dips by one")
        self.assertEqual(self.evaluate(mutate(samples, lambda r, i: r.update(memberSessions=None)))["capacity.sessions"], "not_run")


class CollectorTests(unittest.TestCase):
    def test_cgroup_counters_are_parsed_and_unreadable_ones_are_null(self):
        files = {"/sys/fs/cgroup/machine.slice/x.scope/cpu.stat": "usage_usec 12345\nuser_usec 1\n",
                 "/sys/fs/cgroup/machine.slice/x.scope/cpu.max": "200000 100000\n",
                 "/sys/fs/cgroup/machine.slice/x.scope/memory.current": "1048576\n",
                 "/sys/fs/cgroup/machine.slice/x.scope/memory.max": "max\n",
                 "/sys/fs/cgroup/machine.slice/x.scope/memory.events": "low 0\noom_kill 2\n"}
        numbers = collector.cgroup_numbers("/machine.slice/x.scope", reader=files.get)
        self.assertEqual(numbers, {"cpuUsec": 12345, "cpuQuotaCores": 2.0, "memoryBytes": 1048576, "memoryLimitBytes": None, "oomKills": 2})
        self.assertEqual(collector.cgroup_numbers("/nowhere", reader=lambda p: None),
                         {"cpuUsec": None, "cpuQuotaCores": None, "memoryBytes": None, "memoryLimitBytes": None, "oomKills": None})

    def test_gauges_redis_storage_and_the_storage_path_are_parsed(self):
        gauges = collector.parse_gauges("# HELP x y\ntelos_db_pool_acquired_connections 7\ntelos_db_pool_max_connections 20\ntelos_active_streams 3\ntelos_stream_capacity_max 100\nother 1\n")
        self.assertEqual(gauges, {"dbPoolAcquired": 7.0, "dbPoolMax": 20.0, "activeStreams": 3.0, "streamMax": 100.0})
        self.assertEqual(collector.parse_redis("used_memory:1000\nmaxmemory:2000\n"), {"usedBytes": 1000, "maxBytes": 2000})
        self.assertEqual(collector.parse_redis("garbage"), {"usedBytes": None, "maxBytes": None})
        self.assertEqual(collector.shared_storage_path("/n", reader=lambda p: "A=1\nSTORAGE_PATH='/mnt/x'\n"), "/mnt/x")
        self.assertEqual(collector.shared_storage_path("/n", reader=lambda p: None), "/mnt/storage/shared")

        class Stat:
            f_bavail, f_blocks, f_frsize = 10, 100, 4096
        self.assertEqual(collector.storage_row("/x", statvfs=lambda p: Stat), {"freeBytes": 40960, "totalBytes": 409600})
        self.assertEqual(collector.storage_row("/x", statvfs=lambda p: (_ for _ in ()).throw(OSError())), {"freeBytes": None, "totalBytes": None})

    def test_the_loop_samples_on_schedule_and_prints_only_json_lines(self):
        times = iter([0.0, 0.0, 0.1, 5.0, 5.0, 5.1, 10.0, 10.0, 10.1, 15.0, 15.0])
        out = io.StringIO()
        calls = []
        code = collector.main(["--seconds", "12", "--interval", "5", "--node-path", "/n"], out=out, clock=lambda: next(times), sleeper=calls.append,
                              sampler=lambda node: {"t": len(calls), "node": node})
        self.assertEqual(code, 0)
        lines = out.getvalue().splitlines()
        self.assertTrue(lines and all(json.loads(l)["node"] == "/n" for l in lines))
        self.assertEqual(collector.main(["--seconds", "0", "--interval", "5"], out=io.StringIO()), 2)
        self.assertEqual(collector.main(["--seconds", "10", "--interval", "5", "--node-path", "relative"], out=io.StringIO()), 2)

    def test_the_stats_program_is_self_contained_and_runs(self):
        program = drill.stats_program(1, "/var/lib/telos").decode()
        self.assertNotIn('__name__ == "__main__"', program)
        namespace = {}
        compile(program, "stats-program", "exec")


class FakeSession:
    """Plays the gateway's public API for the fixture preparer."""
    channels = []
    users = {}
    invites = set()
    calls = []

    def __init__(self, label):
        self.label = label
        self.user = None

    def request(self, method, path, headers=None, body=None, json_body=None, timeout=0):
        FakeSession.calls.append((method, path))
        if (method, path) == ("POST", "/api/v1/auth/login"):
            if json_body["username"] == "owner" or json_body["username"] in FakeSession.users:
                self.user = json_body["username"]
                return 200, {}, b"{}"
            return 401, {}, b""
        if (method, path) == ("POST", "/api/v1/admin/channels"):
            FakeSession.channels.append({"id": f"chan-{len(FakeSession.channels)}", "name": json_body["name"]})
            return 201, {}, json.dumps({"id": FakeSession.channels[-1]["id"]}).encode()
        if (method, path) == ("POST", "/api/v1/auth/invites"):
            token = f"inv-{len(FakeSession.invites)}"
            FakeSession.invites.add(token)
            return 200, {}, json.dumps({"invite_token": token}).encode()
        if (method, path) == ("POST", "/api/v1/auth/invites/accept"):
            FakeSession.users[json_body["username"]] = f"id-{json_body['username']}"
            return 201, {}, b"{}"
        if (method, path) == ("GET", "/api/v1/auth/me"):
            return 200, {}, json.dumps({"ID": FakeSession.users[self.user]}).encode()
        if (method, path) == ("POST", "/api/v1/auth/logout"):
            return 200, {}, b""
        if (method, path) == ("GET", "/api/v1/channels"):
            return 200, {}, json.dumps(FakeSession.channels).encode()
        if method == "DELETE" and path.startswith("/api/v1/admin/users/"):
            FakeSession.users = {k: v for k, v in FakeSession.users.items() if v != path.rsplit("/", 1)[1]}
            return 200, {}, b"{}"
        if method == "DELETE" and path.startswith("/api/v1/admin/channels/"):
            FakeSession.channels = [c for c in FakeSession.channels if c["id"] != path.rsplit("/", 1)[1]]
            return 200, {}, b"{}"
        raise AssertionError(f"unexpected {method} {path}")


class FixtureTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        FakeSession.channels, FakeSession.users, FakeSession.invites, FakeSession.calls = [], {}, set(), []
        self.out = self.dir / "fixture"
        self.out.mkdir(mode=0o700)
        self.manifest = self.dir / "manifest.json"
        self.manifest.write_text(json.dumps({"schemaVersion": 1, "chatChannel": "general", "video": {"id": "v1"}, "audio": {"id": "a1"},
                                             "ebook": {"id": "e1", "format": "epub"}, "upload": {"filename": "f.txt", "text": "hello"}}))
        self.target = self.write_target()

    def write_target(self, **changes):
        document = {"schemaVersion": 1, "purpose": "capacity-test", "candidateLockDigest": None, "origin": "https://node.example.net", "hostId": "sha256:" + "a" * 64}
        document.update(changes)
        path = self.dir / "capacity-target.json"
        path.write_text(json.dumps(document))
        os.chmod(path, 0o600)
        return str(path)

    def main(self, command, *extra, fd_text=None, target=None, lock="-"):
        read_fd, write_fd = os.pipe()
        os.write(write_fd, (fd_text or json.dumps({"username": "owner", "password": "owner-pw"})).encode())
        os.close(write_fd)
        counter = []
        self.addCleanup(lambda: os.close(read_fd) if not counter else None)
        argv = [command, "--target-file", target or self.target, "--lock-digest", lock, "--owner-fd", str(read_fd), "--out-dir", str(self.out), *extra]
        if command == "prepare":
            argv += ["--fixture-manifest", str(self.manifest)]
        factory = lambda origin, directory, addresses=(): (lambda: FakeSession(origin))  # noqa: E731
        stderr = io.StringIO()
        old = sys.stderr
        sys.stderr = stderr
        try:
            code = preparer.main(argv, make_session_factory=factory)
        finally:
            sys.stderr = old
        return code, stderr.getvalue()

    def test_prepare_creates_a_hundred_accounts_and_a_channel_and_keeps_secrets_private(self):
        code, err = self.main("prepare")
        self.assertEqual((code, err), (0, ""))
        data = json.loads((self.out / "load-data.json").read_text())
        self.assertEqual(len(data["accounts"]), 100)
        self.assertEqual(len({a["username"] for a in data["accounts"]}), 100)
        self.assertEqual(data["fixture"]["video"], {"id": "v1"})
        self.assertTrue(data["fixture"]["chatChannel"].startswith("capacity-"))
        for name in ("load-data.json", "cleanup.json"):
            self.assertEqual(stat.S_IMODE(os.stat(self.out / name).st_mode), 0o600)
        cleanup = json.loads((self.out / "cleanup.json").read_text())
        self.assertEqual(len(cleanup["userIds"]), 100)
        self.assertTrue(cleanup["channelId"])

    def test_the_load_hosts_source_addresses_are_tolerated_but_other_keys_are_not(self):
        self.assertEqual(self.main("prepare", target=self.write_target(sourceAddresses=["192.0.2.10"]))[0], 0)

    def test_cleanup_removes_every_account_and_the_channel(self):
        self.main("prepare")
        self.assertEqual(len(FakeSession.users), 100)
        code, err = self.main("cleanup")
        self.assertEqual((code, err), (0, ""))
        self.assertEqual((FakeSession.users, FakeSession.channels), ({}, []))
        self.assertFalse((self.out / "cleanup.json").exists())

    def test_a_failure_part_way_removes_what_was_created(self):
        original = FakeSession.request
        accepts = []

        def flaky(session, method, path, **kwargs):
            if path == "/api/v1/auth/invites/accept":
                accepts.append(1)
                if len(accepts) == 40:
                    return 500, {}, b""
            return original(session, method, path, **kwargs)
        FakeSession.request = flaky
        self.addCleanup(lambda: setattr(FakeSession, "request", original))
        code, err = self.main("prepare")
        self.assertEqual(code, 2)
        self.assertEqual((FakeSession.users, FakeSession.channels), ({}, []), "39 accounts and the channel must not be left behind")

    def test_unsafe_requests_are_refused_before_anything_is_created(self):
        for label, kwargs in (
                ("purpose", dict(target=self.write_target(purpose="staging"))),
                ("candidate mismatch", dict(lock="sha256:" + "b" * 64)),
                ("http origin", dict(target=self.write_target(origin="http://node.example.net"))),
                ("ip literal", dict(target=self.write_target(origin="https://192.0.2.5"))),
                ("origin with path", dict(target=self.write_target(origin="https://node.example.net/app"))),
                ("bad host id", dict(target=self.write_target(hostId="machine-1"))),
                ("extra key", dict(target=self.write_target(extra=1)))):
            code, err = self.main("prepare", **kwargs)
            self.assertEqual(code, 2, label)
            self.assertEqual(FakeSession.calls, [], f"{label}: the API was touched")
        self.assertEqual(self.main("prepare", "--accounts", "99")[0], 2)
        self.assertEqual(self.main("prepare", fd_text="not json")[0], 2)
        loose = self.write_target()
        os.chmod(loose, 0o644)
        self.assertEqual(self.main("prepare", target=loose)[0], 2)

    def test_the_output_directory_must_be_private_and_outside_the_repository(self):
        os.chmod(self.out, 0o755)
        self.assertEqual(self.main("prepare")[0], 2)
        os.chmod(self.out, 0o700)
        inside = ROOT / "telos-review" / "capacity-fixture-test"
        inside.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.addCleanup(lambda: inside.rmdir())
        self.out = inside
        self.assertEqual(self.main("prepare")[0], 2)

    def test_no_password_or_invite_is_ever_printed(self):
        original_stdout = sys.stdout
        sys.stdout = captured = io.StringIO()
        try:
            self.main("prepare")
        finally:
            sys.stdout = original_stdout
        data = json.loads((self.out / "load-data.json").read_text())
        for account in data["accounts"][:5]:
            self.assertNotIn(account["password"], captured.getvalue())
        self.assertNotIn("inv-", captured.getvalue())


class RunTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        manifest = self.dir / "fixtures.json"
        manifest.write_text("{}")
        credentials = self.dir / "credentials.json"
        self.target_file = base.TargetTests().make(self.dir, hostId=base.NODE_HOST, credentialsFile=str(credentials))
        credentials.write_text(json.dumps({"owner": {"username": "o", "password": "p"}, "member": {"username": "m", "password": "q"}, "fixtureManifestFile": str(manifest)}))
        os.chmod(credentials, 0o600)
        self.capacity = self.dir / "capacity-target.json"
        self.write_capacity()
        self.update_target(capacityTargetFile=str(self.capacity))
        self.out = self.dir / "out"
        self.out.mkdir()
        self.steps = []

    def write_capacity(self, **changes):
        document = {"schemaVersion": 1, "purpose": "capacity-test", "candidateLockDigest": None, "origin": "https://node.example.net", "hostId": base.NODE_HOST}
        document.update(changes)
        self.capacity.write_text(json.dumps(document))
        os.chmod(self.capacity, 0o600)

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

    def remote_factory(self):
        facts = self.facts()

        class Fake:
            def __init__(self, target):
                pass

            def collect(self, *arguments, **kw):
                return {"host": {"machineIdSha256": facts["host"]["machineIdSha256"]}} if arguments[0] == "identity" else facts
        return Fake

    def invoke(self, summary=None, samples=None, duration="30m", smoke_status="passed", prepare_fails=False, cleanup_fails=False, verifier=base.VERIFIER_HOST):
        steps = self.steps

        def prepare(command, target, lock_digest, owner, manifest, out_dir, addresses):
            steps.append(command)
            if prepare_fails and command == "prepare":
                raise dc.DrillError("the capacity fixture step 'prepare' failed")
            if cleanup_fails and command == "cleanup":
                raise dc.DrillError("could not remove")

        def k6(engine, image, data_path, origin, duration_text, out_dir, add_host, ca, addresses):
            steps.append("k6")
            path = os.path.join(out_dir, "k6-summary.json")
            if summary is not None:
                Path(path).write_text(json.dumps(summary))
            return 0, path, ""

        def runner(target_file, program, timeout=0):
            steps.append("samples")
            return samples if samples is not None else good_samples(drill.duration_seconds(duration) + 30)

        def smoke(args, mode, commit, lock_digest, target, scratch, manifest, hosts):
            steps.append("smoke")
            return 0, {"schemaVersion": 1, "origin": target["origin"], "assertions": [{"id": "chat.send", "description": "c", "status": smoke_status, "detail": "ok"}]}, "s"

        return drill.run(["--target-file", self.target_file, "--rehearsal", "--source-commit", base.COMMIT, "--duration", duration,
                          "--result-out", str(self.out / "result.json")], remote_factory=self.remote_factory(), verifier_identity=lambda: verifier,
                         runner=runner, k6=k6, prepare=prepare, smoke=smoke, resolver=lambda host: "192.0.2.10",
                         journey_recorder=lambda origin, member, directory: steps.append("journey-member"),
                         addresses_for=lambda capacity_file: [f"192.0.2.{n}" for n in range(10, 35)])

    def result(self):
        return rg.verify_result_directory(self.out, "result.json")

    def test_a_healthy_run_passes_with_a_runtime_subject_and_cleans_up(self):
        self.assertEqual(self.invoke(summary=good_summary()), 0)
        result = self.result()
        self.assertEqual((result["status"], result["gateId"]), ("passed", "capacity"))
        subject = rg.load_record("observed-subject", (self.out / "subject.json").read_bytes())
        self.assertEqual(subject["kind"], "runtime")
        self.assertEqual(self.steps[0], "prepare")
        self.assertEqual(self.steps[-1], "cleanup")
        self.assertIn("k6", self.steps)
        self.assertLess(self.steps.index("journey-member"), self.steps.index("cleanup"), "the journey member is recorded before cleanup runs")

    def test_a_short_run_is_not_run_but_still_cleans_up(self):
        self.assertEqual(self.invoke(summary=good_summary(), duration="10m"), 3)
        self.assertEqual(self.result()["status"], "not_run")
        self.assertEqual(self.steps[-1], "cleanup")

    def test_a_breach_fails_the_gate_and_cleanup_still_runs(self):
        summary = good_summary()
        summary["metrics"]["http_req_duration"]["p(95)"] = 900.0
        self.assertEqual(self.invoke(summary=summary), 1)
        self.assertIn("capacity.latency", self.result()["reason"])
        self.assertEqual(self.steps[-1], "cleanup")

    def test_a_missing_summary_or_missing_samples_fail_closed(self):
        self.assertEqual(self.invoke(summary=None), 1)
        self.assertIn("capacity.k6", self.result()["reason"])
        for child in self.out.iterdir():
            child.unlink()
        self.assertEqual(self.invoke(summary=good_summary(), samples=[]), 1)
        self.assertIn("capacity.samples", self.result()["reason"])

    def test_a_failed_journey_after_the_load_fails_the_gate(self):
        self.assertEqual(self.invoke(summary=good_summary(), smoke_status="failed"), 1)
        self.assertIn("capacity.smoke", self.result()["reason"])

    def test_an_incomplete_cleanup_fails_the_gate(self):
        self.assertEqual(self.invoke(summary=good_summary(), cleanup_fails=True), 1)
        self.assertIn("capacity.cleanup", self.result()["reason"])

    def test_a_failed_fixture_runs_no_load_and_creates_nothing_to_clean(self):
        with self.assertRaisesRegex(dc.DrillError, "prepare"):
            self.invoke(summary=good_summary(), prepare_fails=True)
        self.assertEqual(self.steps, ["prepare"])

    def test_the_capacity_target_must_match_this_run(self):
        for change in ({"purpose": "production"}, {"origin": "https://other.example.net"}, {"hostId": "sha256:" + "c" * 64}, {"candidateLockDigest": "sha256:" + "d" * 64}):
            self.write_capacity(**change)
            with self.assertRaisesRegex(dc.DrillError, "does not match"):
                self.invoke(summary=good_summary())
        self.assertEqual(self.steps, [])
        self.update_target(capacityTargetFile=None)
        with self.assertRaisesRegex(dc.NotRun, "capacityTargetFile"):
            self.invoke(summary=good_summary())

    def test_the_load_host_may_not_be_the_node(self):
        with self.assertRaisesRegex(dc.NotRun, "node itself"):
            self.invoke(summary=good_summary(), verifier=base.NODE_HOST)
        self.assertEqual(self.steps, [])


class SourceAddressTests(unittest.TestCase):
    def write(self, **changes):
        handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        self.addCleanup(os.unlink, handle.name)
        json.dump(dict({"purpose": "capacity-test"}, **changes), handle)
        handle.close()
        return handle.name

    ADDRESSES = [f"192.0.2.{n}" for n in range(10, 36)]

    def test_enough_local_addresses_are_accepted(self):
        got = drill.source_addresses(self.write(sourceAddresses=self.ADDRESSES), local=lambda: set(self.ADDRESSES) | {"127.0.0.1"})
        self.assertEqual(got, sorted(self.ADDRESSES))

    def test_too_few_missing_or_foreign_addresses_are_not_run(self):
        local = lambda: set(self.ADDRESSES)  # noqa: E731
        for label, document in (("none", {}), ("few", {"sourceAddresses": self.ADDRESSES[:24]}), ("duplicates", {"sourceAddresses": ["192.0.2.10"] * 30}),
                                ("malformed", {"sourceAddresses": ["host.example"] * 30}), ("not a list", {"sourceAddresses": "192.0.2.10"})):
            with self.assertRaises(dc.NotRun, msg=label):
                drill.source_addresses(self.write(**document), local=local)
        with self.assertRaisesRegex(dc.NotRun, "not assigned to this machine"):
            drill.source_addresses(self.write(sourceAddresses=self.ADDRESSES), local=lambda: set(self.ADDRESSES[:20]))

    def test_k6_gets_the_addresses_and_host_networking(self):
        seen = []

        class Result:
            returncode, stderr = 0, b""

        def runner(argv, **kwargs):
            seen.append(argv)
            return Result()
        drill.run_k6(["podman"], "img@sha256:" + "a" * 64, "/d/data.json", "https://x.example", "30m", "/tmp/out", "x.example:192.0.2.1", "/ca.crt",
                     self.ADDRESSES, runner=runner)
        argv = seen[1]
        self.assertIn("--local-ips", argv)
        self.assertEqual(argv[argv.index("--local-ips") + 1], ",".join(self.ADDRESSES))
        self.assertEqual(argv[argv.index("--network") + 1], "host")
        for flag in ("--cap-drop=ALL", "--read-only"):
            self.assertIn(flag, argv)


class ShellTests(unittest.TestCase):
    def test_without_a_target_the_gate_is_not_run_and_unknown_arguments_are_refused(self):
        script = str(ROOT / "scripts/run-capacity-drill.sh")
        self.assertEqual(subprocess.run(["bash", script], capture_output=True).returncode, 3)
        self.assertEqual(subprocess.run(["bash", script, "--bogus"], capture_output=True).returncode, 2)


if __name__ == "__main__":
    unittest.main(verbosity=1)
