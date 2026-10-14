#!/usr/bin/env python3
"""The capacity drill: 100 disposable members, 25 simultaneous sessions, 30 minutes of real traffic.

Run from a LOAD HOST that is not the node:

    capacity_drill.py --target-file FILE (--rehearsal --source-commit SHA | --candidate-lock FILE)
                      --result-out FILE [--duration 30m]

The drill
  1. checks the node (identity, reference platform) and the operator-owned capacity target, which must
     declare the node disposable and match the candidate, the origin and the machine;
  2. creates 100 member accounts and a dedicated channel through the public API
     (scripts/prepare-capacity-fixture.py), leaving the credentials in a private temp directory;
  3. samples the node every 5 seconds from its cgroups, Redis, the database and the gateway's saturation
     gauges (scripts/collect-runtime-stats.py, over SSH) while the LOCKED k6 image runs tests/load/telos.js
     against the origin (25 sessions; Chat, Stream, Library, Files and commentary reads, 10 durable writes
     per second);
  4. judges the k6 summary and the samples:  request failures < 1 %, p95 < 750 ms, mutation p95 < 1000 ms,
     every semantic check; no restart, OOM or health loss; CPU below 80 % of each service's quota in every
     complete 60 s window; memory below 85 % of its limit at every sample; free storage at least
     max(10 GiB, 10 % of the filesystem); no 60 s stretch with the database pool, Redis or the stream
     limiter at its maximum; never more than 25 live member sessions;
  5. runs the member journey once more (the node must still work), and removes every fixture object.

A run shorter than 30 minutes cannot pass. A missing load host, engine or sample is ``not_run`` or
``failed``, never a pass. Exit codes: 0 passed, 1 failed, 2 unsafe input, 3 not_run.
"""

import argparse
import json
import os
import platform
import re
import secrets
import shutil
import ssl
import stat
import subprocess
import sys
import tempfile
import threading
import urllib.parse
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import remote_program as rp  # noqa: E402

GATE_ID = "capacity"
ROOT = Path(__file__).resolve().parent.parent.parent
REQUIRED_SECONDS = 1800
SESSIONS = 25
ACCOUNTS = 100
SAMPLE_INTERVAL = 5
WINDOW_SECONDS = 60
CPU_LIMIT_FRACTION = 0.80
MEMORY_LIMIT_FRACTION = 0.85
MIN_FREE_BYTES = 10 * 1024 ** 3
MIN_FREE_FRACTION = 0.10
P95_MS, MUTATION_P95_MS, FAILED_RATE, CHECK_RATE = 750, 1000, 0.01, 0.99
ONE_SHOT = {"telos-migrate", "telos-audiobook-migrate", "telos-sanitize-restore"}   # they exit by design
REQUIRED_THRESHOLDS = {
    "http_req_failed": {"rate<0.01"},
    "http_req_duration": {"p(95)<750"},
    "http_req_duration{kind:mutation}": {"p(95)<1000"},
    "checks": {"rate>0.99"},
    "semantic_failures": {"count==0"},
}
assertion, check, not_run = cid.assertion, cid.check, cid.not_run


def duration_seconds(text):
    match = re.fullmatch(r"(\d+)(s|m|h)", text or "")
    if not match:
        raise dc.DrillError("--duration looks like 30m, 90s or 1h")
    return int(match.group(1)) * {"s": 1, "m": 60, "h": 3600}[match.group(2)]


# ------------------------------------------------------------------ judging the k6 summary

def metric(summary, name):
    return ((summary or {}).get("metrics") or {}).get(name) or {}


def evaluate_k6(summary, seconds):
    out = []
    if not isinstance(summary, dict) or "metrics" not in summary:
        return [assertion("capacity.k6", "the load generator produced a summary", "failed", "no usable k6 summary was produced")]
    declared = {name: set((metric(summary, name).get("thresholds") or {})) for name in REQUIRED_THRESHOLDS}
    wrong = sorted(name for name, expected in REQUIRED_THRESHOLDS.items() if declared[name] != expected)
    out.append(check("capacity.thresholds", "the load test enforced exactly the required thresholds", not wrong,
                     "failed-rate < 1 %, p95 < 750 ms, mutation p95 < 1000 ms, checks > 99 %, no semantic failure were enforced",
                     f"the thresholds in the summary differ from the required ones for: {wrong}"))
    failed_rate = metric(summary, "http_req_failed").get("value")
    p95 = metric(summary, "http_req_duration").get("p(95)")
    mutation_p95 = metric(summary, "http_req_duration{kind:mutation}").get("p(95)")
    checks_rate = metric(summary, "checks").get("value")
    semantic = metric(summary, "semantic_failures").get("count")
    iterations = metric(summary, "iterations").get("count")
    numbers = (failed_rate, p95, mutation_p95, checks_rate, semantic, iterations)
    if any(not isinstance(n, (int, float)) or isinstance(n, bool) for n in numbers):
        return out + [assertion("capacity.k6", "the load generator reported every measurement", "failed", "the k6 summary lacks a measurement the drill needs (malformed summary)")]
    out.append(check("capacity.latency", "requests are fast and rarely fail",
                     failed_rate < FAILED_RATE and p95 < P95_MS and mutation_p95 < MUTATION_P95_MS,
                     f"failed {failed_rate:.4f}, p95 {p95:.0f} ms, mutation p95 {mutation_p95:.0f} ms",
                     f"failed {failed_rate:.4f} (limit {FAILED_RATE}), p95 {p95:.0f} ms (limit {P95_MS}), mutation p95 {mutation_p95:.0f} ms (limit {MUTATION_P95_MS})"))
    out.append(check("capacity.semantics", "every response was the right shape, not just a 2xx", checks_rate > CHECK_RATE and semantic == 0,
                     f"{checks_rate:.4f} of checks passed; 0 semantic failures", f"checks {checks_rate:.4f} (need > {CHECK_RATE}); semantic failures {semantic}"))
    minimum = int(seconds / 2.5 * SESSIONS * 0.8)   # 25 sessions each iterating about every 2.5 s, with 20 % slack
    out.append(check("capacity.volume", "the load really ran", iterations >= minimum,
                     f"{int(iterations)} iterations", f"only {int(iterations)} iterations in {seconds}s; at least {minimum} expected"))
    out.append(check("capacity.duration", "the run lasted the required 30 minutes", seconds >= REQUIRED_SECONDS,
                     f"{seconds}s", f"{seconds}s is shorter than the required {REQUIRED_SECONDS}s") if seconds >= REQUIRED_SECONDS
               else not_run("capacity.duration", "the run lasted the required 30 minutes", f"{seconds}s is shorter than the required {REQUIRED_SECONDS}s, so this run cannot pass"))
    return out


# ------------------------------------------------------------------ judging the node's samples

def rolling_windows(samples, seconds=WINDOW_SECONDS):
    """Yield (first, last) sample pairs spanning at least ``seconds``, one per starting sample."""
    for index, first in enumerate(samples):
        for last in samples[index + 1:]:
            if last["t"] - first["t"] >= seconds:
                yield first, last
                break


def sustained(samples, predicate, seconds=WINDOW_SECONDS):
    """True when predicate holds at every sample across some stretch of at least ``seconds``."""
    start = None
    for sample in samples:
        if predicate(sample):
            start = sample["t"] if start is None else start
            if sample["t"] - start >= seconds:
                return True
        else:
            start = None
    return False


def evaluate_samples(samples, seconds, node_services):
    out = []
    expected = int(seconds / SAMPLE_INTERVAL * 0.9)
    complete = [s for s in samples if s.get("containers")]
    out.append(check("capacity.samples", "resource samples were taken throughout", len(complete) >= expected and len(complete) > 0,
                     f"{len(complete)} samples every {SAMPLE_INTERVAL}s", f"{len(complete)} samples; at least {expected} were required"))
    if len(complete) < 2:
        return out
    names = sorted(set().union(*[set(s["containers"]) for s in complete]))
    missing = [n for n in node_services if n not in names]
    restarts, unhealthy, killed = [], [], []
    for name in names:
        rows = [s["containers"][name] for s in complete if name in s["containers"]]
        counts = {r["restarts"] for r in rows if r.get("restarts") is not None}
        if len(counts) > 1:
            restarts.append(name)
        if any(r.get("oomKilled") for r in rows):
            killed.append(name)
        ooms = [r["oomKills"] for r in rows if r.get("oomKills") is not None]
        if ooms and max(ooms) > min(ooms):
            killed.append(name)
        if any(r.get("health") == "unhealthy" or (name not in ONE_SHOT and r.get("status") not in ("running", None)) for r in rows):
            unhealthy.append(name)
    out.append(check("capacity.stability", "no service restarted, was killed for memory or lost its health",
                     not (restarts or killed or unhealthy or missing),
                     f"{len(names)} containers: no restart, OOM kill or health loss", f"restarted {restarts}; OOM {sorted(set(killed))}; unhealthy {unhealthy}; absent {missing}"))
    cpu_problems = []
    for name in names:
        series = [{"t": s["t"], "usec": s["containers"][name].get("cpuUsec"), "quota": s["containers"][name].get("cpuQuotaCores")}
                  for s in complete if name in s["containers"]]
        series = [p for p in series if p["usec"] is not None]
        quota = next((p["quota"] for p in series if p["quota"]), None)
        if quota is None:
            continue
        for first, last in rolling_windows(series):
            cores = (last["usec"] - first["usec"]) / 1e6 / (last["t"] - first["t"])
            if cores >= CPU_LIMIT_FRACTION * quota:
                cpu_problems.append(f"{name} {cores / quota:.0%} of {quota:g} CPU")
                break
    out.append(check("capacity.cpu", "CPU stayed below 80 % of every service's quota in every 60 s window", not cpu_problems,
                     "no complete 60 s window reached 80 % of any quota", f"over 80 % in a 60 s window: {cpu_problems[:4]}"))
    mem_problems = sorted({f"{n} {r['memoryBytes'] / r['memoryLimitBytes']:.0%}" for s in complete for n, r in s["containers"].items()
                           if r.get("memoryBytes") is not None and r.get("memoryLimitBytes") and r["memoryBytes"] >= MEMORY_LIMIT_FRACTION * r["memoryLimitBytes"]})
    out.append(check("capacity.memory", "memory stayed below 85 % of every service's limit at every sample", not mem_problems,
                     "every sample was below 85 % of every limit", f"at or over 85 % of the limit: {mem_problems[:4]}"))
    storage_problems = []
    for label in ("shared", "node"):
        rows = [s["storage"][label] for s in complete if (s.get("storage") or {}).get(label, {}).get("freeBytes") is not None]
        if not rows:
            storage_problems.append(f"{label}: unreadable")
            continue
        worst = min(rows, key=lambda r: r["freeBytes"])
        need = max(MIN_FREE_BYTES, MIN_FREE_FRACTION * worst["totalBytes"])
        if worst["freeBytes"] < need:
            storage_problems.append(f"{label}: {worst['freeBytes'] // 1024 ** 3} GiB free, need {int(need) // 1024 ** 3}")
    out.append(check("capacity.storage", "free storage stayed at least max(10 GiB, 10 % of the filesystem)", not storage_problems,
                     "shared storage and the node path kept their free space", "; ".join(storage_problems)))
    saturation = []
    pool_known = any((s.get("gateway") or {}).get("dbPoolMax") for s in complete)
    stream_known = any((s.get("gateway") or {}).get("streamMax") for s in complete)
    redis_known = any((s.get("redis") or {}).get("maxBytes") for s in complete)
    if sustained(complete, lambda s: (s["gateway"].get("dbPoolMax") or 0) > 0 and (s["gateway"].get("dbPoolAcquired") or 0) >= s["gateway"]["dbPoolMax"]):
        saturation.append("database pool at its maximum")
    if sustained(complete, lambda s: (s["gateway"].get("streamMax") or 0) > 0 and (s["gateway"].get("activeStreams") or 0) >= s["gateway"]["streamMax"]):
        saturation.append("stream limiter at its maximum")
    if sustained(complete, lambda s: (s["redis"].get("maxBytes") or 0) > 0 and (s["redis"].get("usedBytes") or 0) >= s["redis"]["maxBytes"]):
        saturation.append("Redis at maxmemory")
    if not (pool_known and stream_known and redis_known):
        out.append(not_run("capacity.saturation", "no 60 s stretch with the pool, Redis or streams at their maximum",
                           "the node did not report the pool, stream or Redis limits (gateway metrics unreachable or Redis maxmemory unset)"))
    else:
        out.append(check("capacity.saturation", "no 60 s stretch with the database pool, Redis or the stream limiter at its maximum", not saturation,
                         "no sustained saturation", "; ".join(saturation)))
    live = [s["memberSessions"] for s in complete if s.get("memberSessions") is not None]
    if not live:
        out.append(not_run("capacity.sessions", "exactly 25 member sessions were live at once", "the live session count could not be read"))
    else:
        low = sorted(live)[max(0, int(len(live) * 0.05) - 1)]
        out.append(check("capacity.sessions", "exactly 25 member sessions were live at once, never more", max(live) == SESSIONS and low >= SESSIONS - 2,
                         f"live member sessions: max {max(live)}, 5th percentile {low}", f"live member sessions: max {max(live)} (must be exactly {SESSIONS}), 5th percentile {low}"))
    return out


# ------------------------------------------------------------------ plumbing

def stats_program(seconds, node_path):
    source = (ROOT / "scripts" / "collect-runtime-stats.py").read_text(encoding="utf-8")
    driver = (f"\nimport io, json as _json\n_buffer = io.StringIO()\nmain(['--seconds', '{int(seconds)}', '--interval', '{SAMPLE_INTERVAL}', '--node-path', {node_path!r}], out=_buffer)\n"
              "print(_json.dumps([_json.loads(line) for line in _buffer.getvalue().splitlines() if line]))\n")
    # drop the `if __name__ == "__main__"` guard: the driver calls main() itself
    return (source.split('\nif __name__ == "__main__":')[0] + driver).encode("utf-8")


def load_credentials(path):
    document = json.loads(Path(path).read_bytes())
    manifest = document.pop("fixtureManifestFile", None)
    if not isinstance(manifest, str) or not os.path.isabs(manifest) or not os.path.isfile(manifest):
        raise dc.DrillError("the credentials file must name an absolute fixtureManifestFile that exists")
    for role in ("owner", "member"):
        if not isinstance(document.get(role), dict) or set(document[role]) != {"username", "password"}:
            raise dc.DrillError(f"the credentials file must hold {role}: {{username, password}}")
    document.pop("stale", None)
    document.pop("bootstrapToken", None)
    return document, manifest


def check_capacity_target(target, lock_digest, capacity_file):
    if not capacity_file:
        raise dc.NotRun("the drill target names no capacityTargetFile; a capacity run needs the operator's declaration that this node is disposable")
    dc.protected_file(capacity_file, "the capacity target file")
    declared = json.loads(Path(capacity_file).read_text(encoding="utf-8"))
    problems = []
    if declared.get("purpose") != "capacity-test":
        problems.append("purpose is not capacity-test")
    if declared.get("origin") != target["origin"]:
        problems.append("the origin differs from the drill target's")
    if declared.get("hostId") != target["hostId"]:
        problems.append("the machine identity differs from the drill target's")
    if declared.get("candidateLockDigest") != lock_digest:
        problems.append("the candidate differs from the one under test")
    return problems


def run_k6(engine, image, data_path, origin, duration, out_dir, add_host, ca_bundle, runner=subprocess.run):
    summary_path = os.path.join(out_dir, "k6-summary.json")
    pulled = runner([*engine, "pull", "--quiet", image], capture_output=True, timeout=600, check=False)   # by digest: the locked image or nothing
    if pulled.returncode != 0:
        raise dc.NotRun("the locked k6 image could not be pulled on the load host")
    # The container runs as the invoking user so it can read the 0600 credentials and write the 0700 output
    # directory; it keeps no capabilities and no writable root.
    argv = [*engine, "run", "--rm", "--pull=never", "--user", f"{os.geteuid()}:{os.getegid()}", "--cap-drop=ALL", "--security-opt=no-new-privileges",
            "--read-only", "--tmpfs", "/tmp", "--memory=2g", "--add-host", add_host,
            "-v", f"{ROOT / 'tests' / 'load'}:/scripts:ro", "-v", f"{data_path}:/data/load-data.json:ro", "-v", f"{out_dir}:/out",
            "-v", f"{ca_bundle}:/etc/ssl/certs/ca-certificates.crt:ro",
            "-e", f"TELOS_ORIGIN={origin}", "-e", "TELOS_LOAD_DATA=/data/load-data.json", "-e", f"TELOS_LOAD_DURATION={duration}",
            image, "run", "--summary-export", "/out/k6-summary.json", "--quiet", "/scripts/telos.js"]
    completed = runner(argv, capture_output=True, timeout=duration_seconds(duration) + 600, check=False)
    tail = completed.stderr.decode("utf-8", "replace").strip().splitlines()[-2:] if completed.stderr else []
    return completed.returncode, summary_path, " | ".join(tail)[:240]


def locked_k6():
    entry = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))["toolImages"]["k6"]
    return f"{entry['source']}@{entry['manifestDigest']}"


def run(argv, remote_factory=dc.Remote, verifier_identity=dc.local_machine_id, runner=rp.run_program, k6=run_k6,
        prepare=None, smoke=cid.run_smoke, resolver=None, journey_recorder=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--target-file", required=True)
    parser.add_argument("--duration", default="30m")
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    seconds = duration_seconds(args.duration)
    if not Path(args.result_out).resolve().parent.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    credentials, manifest = load_credentials(target["credentialsFile"])
    problems = check_capacity_target(target, lock_digest, target["capacityTargetFile"])
    if problems:
        raise dc.DrillError("the capacity target does not match this run: " + "; ".join(problems))
    remote = remote_factory(target)
    dc.verify_node_identity(remote, target)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the load host is the node itself, so load from outside cannot be shown; run the drill from another machine")
    facts = remote.collect("install", target["nodePath"])
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the drill")
    assertions = cid.evaluate_platform(facts)
    host = urllib.parse.urlsplit(target["origin"]).hostname
    address = (resolver or __import__("socket").gethostbyname)(host)
    scratch = Path(tempfile.mkdtemp(prefix="telos-capacity."))
    os.chmod(scratch, 0o700)
    fixture_dir = scratch / "fixture"
    fixture_dir.mkdir(mode=0o700)
    prepared = False
    samples, stats_error = [], []
    try:
        prepare = prepare or prepare_fixture
        prepare("prepare", target, lock_digest, credentials["owner"], manifest, fixture_dir)
        prepared = True
        assertions.append(assertion("capacity.fixture", "100 disposable members and a dedicated channel were created", "passed", f"{ACCOUNTS} accounts"))

        def sample_node():
            try:
                samples.extend(runner(args.target_file, stats_program(seconds + 30, target["nodePath"]), timeout=seconds + 900))
            except (dc.DrillError, OSError, ValueError) as error:
                stats_error.append(str(error))
        sampler = threading.Thread(target=sample_node, daemon=True)
        sampler.start()
        engine = [] if os.geteuid() == 0 or shutil.which("podman") is None else (["sudo", "-n", "podman"] if os.environ.get("TELOS_LOAD_SUDO", "1") == "1" else ["podman"])
        engine = engine or ["podman"]
        code, summary_path, k6_tail = k6(engine, locked_k6(), str(fixture_dir / "load-data.json"), target["origin"], args.duration, str(scratch),
                                f"{host}:{address}", os.environ.get("SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt"))
        sampler.join(timeout=seconds + 900)
        summary = None
        try:
            summary = json.loads(Path(summary_path).read_text(encoding="utf-8"))
        except (OSError, ValueError):
            pass
        if summary is None:
            assertions.append(assertion("capacity.k6", "the load generator produced a summary", "failed", f"k6 exited {code} without a readable summary ({k6_tail or 'no output'})"))
        else:
            assertions += evaluate_k6(summary, seconds)
        if stats_error:
            assertions.append(assertion("capacity.samples", "resource samples were taken throughout", "failed", f"sampling failed: {stats_error[0][:200]}"))
        else:
            assertions += evaluate_samples(samples, seconds, ["telos-core", "telos-postgres", "telos-redis", "telos-traefik"])
        # the node must still serve a real member after the load
        journey_scratch = scratch / "smoke"
        journey_scratch.mkdir(mode=0o700)
        smoke_target = dict(target)
        # A fresh name every run: the journey member cannot be deleted by itself, and an earlier run's would collide.
        journey = dict(credentials, member=dict(credentials["member"], username=f"journey-{secrets.token_hex(4)}"))
        creds_path = journey_scratch / "credentials.json"
        descriptor = os.open(creds_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as handle:
            json.dump(journey, handle)
        smoke_target["credentialsFile"] = str(creds_path)
        args.fixture_manifest = None
        smoke_code, report, summary_text = smoke(args, mode, commit, lock_digest, smoke_target, journey_scratch, manifest, cid.host_descriptor_for(facts))
        rows = [r for r in (report or {}).get("assertions", []) if not (r["id"].startswith("outage.") and r["status"] == "not_run")]
        bad = [r["id"] for r in rows if r["status"] != "passed"]
        assertions.append(check("capacity.smoke", "the member journey still passes after the load", report is not None and rows and not bad,
                                f"{len(rows)} journey assertions passed", f"journey after load: {bad[:5] or summary_text}"))
        (journey_recorder or record_journey_member)(target["origin"], journey["member"], fixture_dir)
    finally:
        cleaned = True
        if prepared:
            try:
                prepare("cleanup", target, lock_digest, credentials["owner"], manifest, fixture_dir)
            except dc.DrillError:
                cleaned = False
        assertions.append(check("capacity.cleanup", "every disposable account and the channel were removed", prepared and cleaned,
                                "the fixture was removed", "the fixture could not be fully removed; cleanup.json was kept for a retry") if prepared
                           else not_run("capacity.cleanup", "every disposable account and the channel were removed", "nothing was created"))
        shutil.rmtree(scratch, ignore_errors=True)
    observation = {"schemaVersion": 1, "origin": target["origin"], "durationSeconds": seconds, "k6Image": locked_k6(),
                   "k6Summary": summary, "sampleCount": len(samples), "samples": samples[:: max(1, len(samples) // 400)] if samples else []}
    return dc.finalize(args, mode, commit, lock_digest, GATE_ID, assertions, observation, cid.host_descriptor_for(facts),
                       {"python3": platform.python_version(), "podman": cid.podman_version(facts)}, facts["observedAt"],
                       label="capacity-drill", runtime_facts=facts)


def record_journey_member(origin, member, fixture_dir, make_session=None):
    """Add the journey's member to the cleanup manifest so the fixture cleanup removes it too."""
    import browser_node_smoke as smoke_client
    try:
        with tempfile.TemporaryDirectory(prefix="telos-journey-id.") as directory:
            session = (make_session or (lambda: smoke_client.Session(origin, ssl.create_default_context(), os.path.join(directory, "jar.json"))))()
            status = session.request("POST", "/api/v1/auth/login", json_body=member)[0]
            if status != 200:
                return False
            me = json.loads(session.request("GET", "/api/v1/auth/me")[2] or b"{}")
            session.request("POST", "/api/v1/auth/logout")
            user_id = me.get("ID") or me.get("id")
            path = Path(fixture_dir) / "cleanup.json"
            created = json.loads(path.read_text(encoding="utf-8"))
            if user_id and user_id not in created["userIds"]:
                created["userIds"].append(user_id)
                path.write_text(json.dumps(created), encoding="utf-8")
            return bool(user_id)
    except (OSError, ValueError, smoke_client.Assertion):
        return False


def prepare_fixture(command, target, lock_digest, owner, manifest, out_dir):
    """Run scripts/prepare-capacity-fixture.py with the owner's credentials on a private pipe."""
    read_fd, write_fd = os.pipe()
    os.write(write_fd, json.dumps(owner).encode("utf-8"))
    os.close(write_fd)
    capacity_file = target["capacityTargetFile"]
    argv = [sys.executable, "-I", str(ROOT / "scripts" / "prepare-capacity-fixture.py"), command, "--target-file", capacity_file,
            "--lock-digest", lock_digest or "-", "--owner-fd", str(read_fd), "--out-dir", str(out_dir)]
    if command == "prepare":
        argv += ["--fixture-manifest", manifest]
    try:
        completed = subprocess.run(argv, pass_fds=(read_fd,), capture_output=True, text=True, timeout=1800, check=False)
    finally:
        os.close(read_fd)
    if completed.returncode != 0:
        raise dc.DrillError(f"the capacity fixture step '{command}' failed: {completed.stderr.strip().splitlines()[-1:] or ['no diagnostic']}"[:300])


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"capacity-drill: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"capacity-drill: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"capacity-drill: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
