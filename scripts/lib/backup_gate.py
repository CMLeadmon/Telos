#!/usr/bin/env python3
"""The encrypted off-node backup gate: take a real backup, then judge it from the repository's side.

Run from a machine that is not the node:

    backup_gate.py --target-file FILE (--rehearsal --source-commit SHA [--rehearsal-lock FILE] | --candidate-lock FILE) --result-out FILE

The drill target is the protected descriptor used by the other drills; its credentials file additionally carries
{"backupRepository": "<restic repository>", "backupPasswordFile": "/abs/path"} (and optionally "backupCredentialsFile"):
the verifier reads the repository itself, with its own copy of the password, so the node cannot vouch for its own backup.

  node (fixed program, as root): checks /etc/telos/backup.env points at an OFF-NODE repository with root-only 0600
        secrets, runs the active release's scripts/backup.sh once, and reports the exit status, its result record,
        the timer state, leftover plaintext staging, the metrics file and whether every service is running again;
  verifier: finds that snapshot in the repository, proves the repository is encrypted (a wrong password is refused),
        runs `restic check --read-data-subset=5%`, dumps the manifest from the repository and validates it (schema,
        every required component, ordered times, consistency point inside the run), lists the snapshot and compares
        each component's size to the manifest, and confirms the retention policy (14 daily, 8 weekly).

Exit codes: 0 passed, 1 failed, 2 unsafe input, 3 not_run (no node, no repository access, no restic).
"""

import argparse
import datetime
import json
import os
import platform
import re
import secrets
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import backup_manifest as bm  # noqa: E402
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import remote_program as rp  # noqa: E402

GATE_ID = "encrypted-backup"
REMOTE_SCHEMES = ("sftp", "s3", "b2", "rest", "rclone", "azure", "gs", "swift")
RETENTION = (14, 8)


def check(identifier, description, ok, detail):
    return cid.check(identifier, description, bool(ok), detail, detail)


def not_run(identifier, detail):
    return cid.not_run(identifier, "a backup configuration exists on the node to be examined", detail)

NODE_PROGRAM = r'''
import json, os, re, stat, subprocess, sys, time
P = json.loads(%(paths)s)
def run(argv, timeout=60, env=None):
    try:
        done = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, env=env)
        return done.returncode, done.stdout
    except (OSError, subprocess.TimeoutExpired):
        return 127, ""
def mode(path):
    try:
        info = os.stat(path)
        return {"mode": "%%04o" %% stat.S_IMODE(info.st_mode), "rootOwned": info.st_uid == P.get("ownerUid", 0), "regular": stat.S_ISREG(info.st_mode)}
    except OSError:
        return None
def parse_env(path):
    values = {}
    try:
        for line in open(path, encoding="utf-8"):
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                key, _, value = line.partition("=")
                values[key.strip()] = value.strip().strip("'\"")
    except OSError:
        return None
    return values
out = {"schemaVersion": 1}
cfg = parse_env(P["backupEnv"])
config = {"present": cfg is not None}
if cfg is not None:
    repo = cfg.get("RESTIC_REPOSITORY", "")
    scheme = repo.split(":", 1)[0] if re.match(r"^[a-z0-9]+:", repo) and not repo.startswith("/") else "local"
    config.update(scheme=scheme, repositoryOnNode=repo.startswith("/") or repo.startswith("./") or repo.startswith("file:"),
                  passwordFile=mode(cfg.get("RESTIC_PASSWORD_FILE", "")),
                  credentialsFile=mode(cfg["RESTIC_CREDENTIALS_FILE"]) if cfg.get("RESTIC_CREDENTIALS_FILE") else "none")
out["config"] = config
code, text = run(["systemctl", "is-enabled", "telos-backup.timer"])
timer = {"enabled": code == 0 and text.strip() == "enabled"}
code, text = run(["systemctl", "is-active", "telos-backup.timer"])
timer["active"] = code == 0 and text.strip() == "active"
out["timer"] = timer
release = os.path.realpath(P["currentLink"]) if os.path.isdir(P["currentLink"]) else P["fallbackRelease"]
script = os.path.join(release, "scripts", "backup.sh")
out["release"] = {"root": release if release != P["fallbackRelease"] else "installed-tree", "scriptPresent": os.path.isfile(script)}
retention = {}
try:
    text = open(script, encoding="utf-8").read()
    for name in ("RETENTION_DAILY", "RETENTION_WEEKLY"):
        found = re.search(r"^%%s=(\d+)" %% name, text, re.M)
        retention[name] = int(found.group(1)) if found else None
except OSError:
    pass
out["retention"] = retention
started = time.time()
result_path = os.path.join(P["stateDir"], "backup-gate.result.json")
try:
    os.unlink(result_path)
except OSError:
    pass
if cfg is None or not os.path.isfile(script):
    out["run"] = {"attempted": False}
else:
    env = dict(os.environ)
    env.update(cfg)
    env.setdefault("TELOS_NODE_PATH", P["nodePath"])
    env["TELOS_RELEASE_ROOT"] = release
    argv = [script, "--result-out", result_path]
    lock = os.path.join(P["stateDir"], "current-candidate-lock.json")
    if os.path.isfile(lock):
        argv[1:1] = ["--candidate-lock", lock]
    code, _ = run(argv, timeout=P["backupTimeout"], env=env)
    result = None
    try:
        result = json.load(open(result_path, encoding="utf-8"))
    except (OSError, ValueError):
        pass
    out["run"] = {"attempted": True, "exit": code, "startedAt": started, "finishedAt": time.time(), "result": result,
                  "usedCandidateLock": os.path.isfile(lock)}
leftovers = sorted(name for name in os.listdir(P["tmpRoot"]) if name.startswith("telos-backup.")) if os.path.isdir(P["tmpRoot"]) else []
out["hygiene"] = {"stagingLeftovers": leftovers, "stageJournalPresent": os.path.exists(os.path.join(P["stateDir"], "backup-stage.journal"))}
prom = os.path.join(P["opsDir"], "last-backup.prom")
metrics = {"present": os.path.isfile(prom)}
if metrics["present"]:
    body = open(prom, encoding="utf-8").read()
    result_line = re.search(r"^telos_backup_last_result (\d+)$", body, re.M)
    stamp_line = re.search(r"^telos_backup_last_success_timestamp_seconds (\d+)$", body, re.M)
    metrics["success"] = bool(result_line and result_line.group(1) == "1")
    metrics["ageSeconds"] = int(time.time() - int(stamp_line.group(1))) if stamp_line else None
out["metrics"] = metrics
code, text = run(["podman", "ps", "--format", "{{.Names}}"])
running = sorted(line for line in text.split() if line.startswith("telos-")) if code == 0 else None
out["running"] = running
print(json.dumps(out))
'''


def node_program(paths):
    """The fixed node-side program as the bytes the transport (``python3 -I -`` over SSH) takes on stdin."""
    return (NODE_PROGRAM % {"paths": repr(json.dumps(paths))}).encode("utf-8")


DEFAULT_PATHS = {"backupEnv": "/etc/telos/backup.env", "currentLink": "/srv/telos/current", "fallbackRelease": "/opt/telos",
                 "stateDir": "/var/lib/telos/state", "opsDir": "/var/lib/telos/ops", "nodePath": "/var/lib/telos", "tmpRoot": "/var/tmp",
                 "backupTimeout": 5400, "ownerUid": 0}


# ------------------------------------------------------------------ the verifier's side

class Restic:
    """The repository from the verifier's side; secrets stay in the environment, never in argv or output."""

    def __init__(self, repository, password_file, credentials_file=None, binary="restic", runner=subprocess.run):
        self.binary, self.runner = binary, runner
        self.env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": os.environ.get("HOME", "/tmp"), "RESTIC_REPOSITORY": repository,
                    "RESTIC_PASSWORD_FILE": password_file, "RESTIC_CACHE_DIR": os.path.join(tempfile.gettempdir(), "telos-gate-restic")}
        if credentials_file:
            for line in Path(credentials_file).read_text(encoding="utf-8").splitlines():
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    key, _, value = line.partition("=")
                    self.env[key.strip()] = value.strip().strip("'\"")

    def call(self, *args, env=None, timeout=1800):
        try:
            return self.runner([self.binary, *args], capture_output=True, text=True, env=env or self.env, timeout=timeout, check=False)
        except FileNotFoundError:
            raise dc.NotRun("restic is not installed on the verifying machine")
        except subprocess.TimeoutExpired:
            raise dc.DrillError(f"restic {args[0]} timed out")

    def json(self, *args):
        done = self.call(*args)
        if done.returncode != 0:
            raise dc.DrillError(f"restic {args[0]} failed: {(done.stderr or '').strip()[-160:]}")
        try:
            return json.loads(done.stdout)
        except ValueError:
            raise dc.DrillError(f"restic {args[0]} returned malformed output")

    def lines(self, *args):
        done = self.call(*args)
        if done.returncode != 0:
            raise dc.DrillError(f"restic {args[0]} failed: {(done.stderr or '').strip()[-160:]}")
        rows = []
        for line in done.stdout.splitlines():
            try:
                rows.append(json.loads(line))
            except ValueError:
                pass
        return rows


def gather_repository_facts(restic, snapshot, started, finished):
    facts = {"snapshot": snapshot}
    snapshots = restic.json("snapshots", "--json", snapshot)
    facts["snapshotRows"] = snapshots
    wrong = dict(restic.env)
    scratch = Path(tempfile.mkdtemp())
    password = scratch / "wrong"
    password.write_text("not-the-password-" + secrets.token_hex(8), encoding="utf-8")
    wrong["RESTIC_PASSWORD_FILE"] = str(password)
    try:
        facts["wrongPasswordExit"] = restic.call("snapshots", "--json", env=wrong, timeout=120).returncode
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
    facts["checkExit"] = restic.call("check", "--read-data-subset=5%").returncode
    if snapshots:
        data_path = snapshots[0]["paths"][0]
        facts["dataPath"] = data_path
        done = restic.call("dump", snapshot, f"{data_path}/manifest.json")
        facts["manifestText"] = done.stdout if done.returncode == 0 else None
        listing = restic.lines("ls", "--json", snapshot, data_path)
        facts["listing"] = {row["path"].rsplit("/", 1)[-1]: row.get("size") for row in listing if row.get("type") == "file"}
    return facts


# ------------------------------------------------------------------ judgement

def parse_time(text):
    return datetime.datetime.fromisoformat(text.replace("Z", "+00:00"))


def evaluate(node, repo, started_wall, lock_digest=None):
    """Assertions from the node's report and the repository's facts."""
    results = []
    config = node.get("config", {})
    if not config.get("present"):
        results.append(not_run("backup.config", "the node has no /etc/telos/backup.env; no repository is configured"))
        return results

    def private(entry):
        return isinstance(entry, dict) and entry.get("mode") == "0600" and entry.get("rootOwned") and entry.get("regular")

    secrets_ok = private(config.get("passwordFile")) and (config.get("credentialsFile") == "none" or private(config.get("credentialsFile")))
    results.append(check("backup.config", "the repository is off-node and its secrets are root-only 0600 files",
                         config.get("scheme") in REMOTE_SCHEMES and not config.get("repositoryOnNode") and secrets_ok,
                         f"scheme {config.get('scheme')}; on node {config.get('repositoryOnNode')}; secrets ok {secrets_ok}"))
    timer = node.get("timer", {})
    results.append(check("backup.schedule", "the six-hourly backup timer is enabled and active", timer.get("enabled") and timer.get("active"),
                         f"enabled {timer.get('enabled')}, active {timer.get('active')}"))
    run = node.get("run", {})
    result = run.get("result") or {}
    ran = run.get("attempted") and run.get("exit") == 0 and result.get("status") == "passed" and result.get("snapshot")
    results.append(check("backup.run", "scripts/backup.sh completed and reported a verified snapshot", ran,
                         f"attempted {run.get('attempted')}, exit {run.get('exit')}, status {result.get('status')}"))
    if not ran:
        return results
    rows = repo.get("snapshotRows") or []
    snapshot = rows[0] if len(rows) == 1 else None
    tags = set((snapshot or {}).get("tags") or [])
    created_ok = False
    if snapshot:
        created = parse_time(snapshot["time"]).timestamp()
        created_ok = started_wall - 30 <= created <= run.get("finishedAt", 0) + 600
    tagged = {"scheduled", "telos"} <= tags and any(t.startswith("release:") for t in tags) and any(t.startswith("candidate:") for t in tags)
    results.append(check("backup.snapshot", "the snapshot exists in the repository, made by this run, tagged for release and candidate",
                         snapshot is not None and created_ok and tagged and snapshot.get("hostname") == "telos-node",
                         f"found {snapshot is not None}, made by this run {created_ok}, tags {sorted(tags)[:4]}"))
    results.append(check("backup.encrypted", "the repository refuses a wrong password", repo.get("wrongPasswordExit") not in (0, None),
                         f"restic with a wrong password exited {repo.get('wrongPasswordExit')}"))
    results.append(check("backup.integrity", "restic check with a 5% data read passes", repo.get("checkExit") == 0, f"exit {repo.get('checkExit')}"))
    manifest_ok, detail, document = False, "no manifest in the snapshot", None
    if repo.get("manifestText"):
        try:
            document = json.loads(repo["manifestText"])
            bm.validate(document)
            consistency = bm.parse_time(document["consistencyPointAt"]).timestamp()
            manifest_ok = started_wall - 30 <= consistency <= run.get("finishedAt", 0) + 5
            detail = "valid and inside this run" if manifest_ok else "the consistency point is outside this run"
            bound = (document.get("source") or {}).get("candidateLockDigest")
            if manifest_ok and lock_digest is not None and run.get("usedCandidateLock") and bound != lock_digest:
                manifest_ok, detail = False, "the manifest names another candidate than the one under certification"
        except bm.ManifestError as error:
            detail = str(error)[:160]
        except (ValueError, KeyError, TypeError) as error:
            detail = f"unreadable: {error.__class__.__name__}"
    results.append(check("backup.manifest", "the manifest dumped from the repository is valid and inside this run", manifest_ok, detail))
    listing = repo.get("listing") or {}
    mismatched = []
    if document:
        for component in document.get("components", []):
            if listing.get(component["name"]) != component["sizeBytes"]:
                mismatched.append(component["name"])
    results.append(check("backup.contents", "every manifest component is in the snapshot at its recorded size", document is not None and not mismatched,
                         "all components present" if document and not mismatched else f"mismatched: {mismatched[:4]}"))
    hygiene = node.get("hygiene", {})
    results.append(check("backup.hygiene", "no plaintext staging or journal is left on the node and every service runs again",
                         not hygiene.get("stagingLeftovers") and not hygiene.get("stageJournalPresent") and bool(node.get("running")) and len(node["running"]) >= 10,
                         f"leftovers {hygiene.get('stagingLeftovers')}, journal {hygiene.get('stageJournalPresent')}, running {len(node.get('running') or [])}"))
    metrics = node.get("metrics", {})
    results.append(check("backup.metrics", "the backup metrics file records this success",
                         metrics.get("present") and metrics.get("success") and metrics.get("ageSeconds") is not None and 0 <= metrics["ageSeconds"] <= 900,
                         f"present {metrics.get('present')}, success {metrics.get('success')}, age {metrics.get('ageSeconds')}s"))
    retention = node.get("retention", {})
    results.append(check("backup.retention", "retention is 14 daily and 8 weekly snapshots",
                         (retention.get("RETENTION_DAILY"), retention.get("RETENTION_WEEKLY")) == RETENTION, f"found {retention}"))
    return results


# ------------------------------------------------------------------ the drill

def load_backup_credentials(target):
    path = dc.protected_file(target["credentialsFile"], "the credentials file")
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    for key in ("backupRepository", "backupPasswordFile"):
        if not data.get(key):
            raise dc.NotRun(f"the credentials file has no {key}; the verifier needs its own access to the repository")
    dc.protected_file(data["backupPasswordFile"], "backupPasswordFile")
    if data.get("backupCredentialsFile"):
        dc.protected_file(data["backupCredentialsFile"], "backupCredentialsFile")
    return data


def restic_version(restic):
    try:
        done = restic.call("version", timeout=30)
        found = re.search(r"restic\s+([0-9][0-9.]*)", done.stdout)
        return found.group(1) if found else "unknown"
    except (dc.DrillError, dc.NotRun):
        return "unknown"


def run(argv, remote_factory=dc.Remote, runner=rp.run_program, restic_factory=Restic, verifier_identity=dc.local_machine_id, clock=datetime.datetime.now):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--target-file", required=True)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    if not Path(args.result_out).resolve().parent.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the verifier is the node itself; run the gate from another machine over SSH")
    credentials = load_backup_credentials(target)
    restic = restic_factory(credentials["backupRepository"], credentials["backupPasswordFile"], credentials.get("backupCredentialsFile"))
    paths = dict(DEFAULT_PATHS, nodePath=target["nodePath"], stateDir=os.path.join(target["nodePath"], "state"), opsDir=os.path.join(target["nodePath"], "ops"))
    started_wall = clock(datetime.timezone.utc).timestamp()
    node = runner(args.target_file, node_program(paths), timeout=paths["backupTimeout"] + 600)
    remote = remote_factory(target)
    facts = remote.collect("install", target["nodePath"])
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the gate")
    result = (node.get("run") or {}).get("result") or {}
    repo = {}
    if result.get("snapshot"):
        repo = gather_repository_facts(restic, result["snapshot"], started_wall, clock(datetime.timezone.utc).timestamp())
    assertions = evaluate(node, repo, started_wall, lock_digest)
    observation = {"schemaVersion": 1, "origin": target["origin"], "node": {k: v for k, v in node.items() if k != "run"},
                   "run": {k: v for k, v in (node.get("run") or {}).items() if k != "result"},
                   "snapshot": {"id": result.get("snapshot"), "manifestSha256": result.get("manifest")},
                   "repository": {"scheme": (node.get("config") or {}).get("scheme"), "snapshotCount": len(repo.get("snapshotRows") or [])}}
    return dc.finalize(args, mode, commit, lock_digest, GATE_ID, assertions, observation, cid.host_descriptor_for(facts),
                       {"python3": platform.python_version(), "podman": cid.podman_version(facts), "restic": restic_version(restic)}, facts["observedAt"],
                       label="backup-gate", runtime_facts=facts)


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"backup-gate: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"backup-gate: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"backup-gate: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
