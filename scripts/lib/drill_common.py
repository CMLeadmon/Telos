#!/usr/bin/env python3
"""Shared plumbing for live drills: protected targets, fixed remote collectors, identity.

A drill acts on a node named by a *target descriptor* (release/drill-target.schema.json).
The descriptor is operational authorization, not evidence: it says where to act, never
what was observed. This module owns the parts every drill must do the same way:

* load the descriptor from a protected file (absolute, not a symlink, mode 0600, owned
  by the invoking user) and refuse embedded credentials;
* verify the node's machine identity *before* any action, so a descriptor that points at
  the wrong machine cannot cause an action there;
* run only FIXED collector programs on the node (``scripts/lib/node_observe.py`` piped on
  stdin), locally or over SSH with a protected config and strict host-key checking. A drill
  cannot be made to run an arbitrary command by editing the descriptor;
* exit codes shared with the gate runner: 0 passed, 1 failed, 2 usage, 3 not_run.
"""

import hashlib
import json
import os
import re
import stat
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import release_graph as rg  # noqa: E402

EXIT_PASSED = 0
EXIT_FAILED = 1
EXIT_USAGE = 2
EXIT_NOT_RUN = 3
COLLECTOR = Path(__file__).resolve().with_name("node_observe.py")
COLLECTOR_TIMEOUT = 120


class DrillError(Exception):
    """A usage, safety or environment problem; the message is safe to print."""


class NotRun(DrillError):
    """A required prerequisite is unavailable; the drill cannot run (exit 3)."""


def protected_file(path, what, mode=0o600):
    """Return the path after proving it is an owned, private, regular file."""
    if not isinstance(path, str) or not os.path.isabs(path):
        raise DrillError(f"{what} must be an absolute path")
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        raise NotRun(f"{what} does not exist: {path}")
    except OSError as error:
        raise DrillError(f"{what} is unreadable ({error.__class__.__name__})")
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise DrillError(f"{what} must be a regular file, not a symlink or special file")
    if info.st_uid not in (os.geteuid(), 0):
        raise DrillError(f"{what} must be owned by the invoking user or root")
    if stat.S_IMODE(info.st_mode) & ~mode:
        raise DrillError(f"{what} must not be readable or writable by group/other (mode {oct(mode)[2:]})")
    return path


def load_target(path):
    """Load and validate a drill target descriptor from a protected file."""
    protected_file(path, "the drill target file")
    document = rg.load_record("drill-target", Path(path).read_bytes(), canonical=False)
    if document["sshConfigFile"] is not None:
        protected_file(document["sshConfigFile"], "the SSH config file")
    protected_file(document["credentialsFile"], "the credentials file")
    return document


def machine_id_hash(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def local_machine_id():
    try:
        return machine_id_hash(Path("/etc/machine-id").read_bytes())
    except OSError:
        raise DrillError("this machine has no readable /etc/machine-id")


class Remote:
    """Executes only the fixed collector, on this machine or over SSH."""

    def __init__(self, target):
        self.ssh_target = target["sshTarget"]
        self.ssh_config = target["sshConfigFile"]

    @property
    def is_local(self):
        return self.ssh_target is None

    def command(self, arguments):
        """The argv that runs the collector with ``arguments``; the script goes on stdin."""
        for argument in arguments:
            if not re.fullmatch(r"[A-Za-z0-9_./=-]+", argument):
                raise DrillError("collector arguments are restricted to a safe alphabet")
        if self.is_local:
            return [sys.executable, "-I", "-", *arguments]
        return [
            "ssh", "-F", self.ssh_config, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
            "-o", "ClearAllForwardings=yes", "-o", "RequestTTY=no", self.ssh_target, "--",
            "sudo", "-n", "python3", "-I", "-", *arguments,
        ]

    def collect(self, *arguments, timeout=COLLECTOR_TIMEOUT):
        script = COLLECTOR.read_bytes()
        try:
            completed = subprocess.run(
                self.command(list(arguments)), input=script, capture_output=True, timeout=timeout, check=False)
        except FileNotFoundError:
            raise NotRun("ssh is not installed on the machine running the drill")
        except subprocess.TimeoutExpired:
            raise DrillError(f"the node collector timed out after {timeout}s")
        if completed.returncode != 0:
            detail = completed.stderr.decode("utf-8", "replace").strip().splitlines()[-1:] or ["no diagnostic"]
            raise DrillError(f"the node collector failed ({completed.returncode}): {detail[0][:200]}")
        try:
            return json.loads(completed.stdout.decode("utf-8"))
        except ValueError:
            raise DrillError("the node collector returned malformed output")


def verify_node_identity(remote, target):
    """Refuse to continue unless the machine answering is the machine the descriptor names."""
    observed = remote.collect("identity")["host"]["machineIdSha256"]
    if observed != target["hostId"]:
        raise DrillError("the target answered with a different machine identity than the descriptor declares; no action was taken")
    return observed


def parse_mode_arguments(parser):
    """Add the shared producer mode flags and return a function validating them."""
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--rehearsal", action="store_true")
    group.add_argument("--candidate-lock")
    parser.add_argument("--source-commit")
    parser.add_argument("--rehearsal-lock")
    parser.add_argument("--result-out", required=True)

    def resolve(args):
        if args.rehearsal:
            if not args.source_commit or not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
                raise DrillError("--rehearsal requires --source-commit with a 40-hex commit")
            commit, digest = args.source_commit, None
            if args.rehearsal_lock:
                data = Path(args.rehearsal_lock).read_bytes()
                commit, digest = json.loads(data)["sourceCommit"], rg.digest_bytes(data)
            return "rehearsal", commit, digest
        if args.source_commit or args.rehearsal_lock:
            raise DrillError("--source-commit and --rehearsal-lock belong to --rehearsal only")
        data = Path(args.candidate_lock).read_bytes()
        return "candidate", json.loads(data)["sourceCommit"], rg.digest_bytes(data)

    return resolve


def write_json(directory, name, document):
    data = rg.canonical_bytes(document)
    path = Path(directory) / name
    path.parent.mkdir(parents=True, exist_ok=True)
    rg.write_atomic_exclusive(str(path), data)
    return {"path": name, "sha256": rg.raw_sha256(data)}


MANIFEST_IN_REFERENCE = re.compile(r"@(sha256:[0-9a-f]{64})\Z")


def build_runtime_subject(facts, commit, lock_digest, containers=None):
    """The observed-runtime descriptor, or (None, why not).

    A runtime observation is only evidence about a *generation*: it binds the installed receipt and, for
    every running Telos service, the container, image and registry manifest digest. A node without an
    installed-generation receipt, or with a service whose image has no manifest digest (a locally built
    image), cannot give that, so a rehearsal there records its observations but no subject.
    """
    receipt = ((facts.get("node") or {}).get("receipt") or {})
    if not receipt.get("present"):
        return None, "the node has no installed-generation receipt, so the observation cannot be bound to a generation"
    rows = [c for c in (containers if containers is not None else facts.get("containers") or []) if c.get("telos") and c.get("state") == "running"]
    if not rows:
        return None, "no running Telos service was observed"
    services, unpinned = [], []
    for row in rows:
        match = MANIFEST_IN_REFERENCE.search(str(row.get("image") or ""))
        image_id = str(row.get("imageId") or "").removeprefix("sha256:")
        if not match or not re.fullmatch(r"[0-9a-f]{64}", image_id):
            unpinned.append(row["name"])
            continue
        services.append({"name": row["name"], "containerId": str(row["id"])[:64], "imageId": image_id, "manifestDigest": match.group(1)})
    if unpinned:
        return None, f"these services run images without a registry manifest digest: {', '.join(sorted(unpinned)[:6])}"
    host = facts["host"]
    return {"schemaVersion": 1, "kind": "runtime", "sourceCommit": commit, "candidateLockDigest": lock_digest, "observedAt": facts["observedAt"],
            "hostId": host["machineIdSha256"], "bootId": host["bootId"], "generationReceiptDigest": receipt["digest"],
            "runtime": {"name": "podman", "rootless": False, "version": ((facts.get("tools") or {}).get("podman") or {}).get("version") or "unknown"},
            "services": sorted(services, key=lambda s: s["name"])}, None


def finalize(args, mode, commit, lock_digest, gate_id, assertions, observation, hosts, tool_versions, observed_at, attachments=(), printer=print, label="drill",
             runtime_facts=None):
    """Write one gate-result record for an observing drill; return the shared exit code.

    ``assertions`` is a list of {id, description, status, detail}; the verdict is failed if any failed,
    not_run if any did not run, else passed. A passed drill also writes the observed-subject descriptor
    naming the hosts it observed and every attachment. The runner, not this function, writes the
    evidence envelope. ``observation`` must already be free of secrets (it is checked again here).
    """
    out_dir = Path(args.result_out).resolve().parent
    runtime_subject = None
    if runtime_facts is not None:
        runtime_subject, unavailable = build_runtime_subject(runtime_facts, commit, lock_digest)
        if runtime_subject is None:
            assertions = list(assertions) + [{"id": "subject.runtime", "description": "the observation can be bound to an installed generation",
                                              "status": "not_run", "detail": unavailable[:300]}]
    statuses = {a["status"] for a in assertions}
    status = "failed" if "failed" in statuses else "not_run" if "not_run" in statuses else "passed"
    reason = {
        "passed": f"{len(assertions)} assertions passed",
        "failed": "failed: " + ", ".join(a["id"] for a in assertions if a["status"] == "failed")[:240],
        "not_run": "not run: " + ", ".join(a["id"] for a in assertions if a["status"] == "not_run")[:240],
    }[status]
    files = [write_json(out_dir, "observation.json", observation) | {"kind": "report"},
             write_json(out_dir, "assertions.json", {"schemaVersion": 1, "assertions": assertions}) | {"kind": "report"}]
    for name, document in attachments:
        files.append(write_json(out_dir, name, document) | {"kind": "report"})
    record = {
        "schemaVersion": 1, "recordKind": mode, "gateId": os.environ.get("TELOS_GATE_ID", gate_id), "sourceCommit": commit,
        "candidateLockDigest": lock_digest, "status": status, "reason": reason, "subjectFile": None,
        "attachments": files, "toolVersions": tool_versions,
    }
    if status == "passed":
        subject = runtime_subject if runtime_subject is not None else {
            "schemaVersion": 1, "kind": "external-host", "sourceCommit": commit, "candidateLockDigest": lock_digest,
            "observedAt": observed_at, "purpose": record["gateId"], "hosts": sorted(hosts, key=lambda h: h["hostId"]),
            "records": sorted(({"path": a["path"], "sha256": a["sha256"]} for a in files), key=lambda r: r["path"]),
        }
        files.append(write_json(out_dir, "subject.json", subject) | {"kind": "subject"})
        record["subjectFile"] = "subject.json"
    files.sort(key=lambda item: item["path"])
    rg.reject_secret_text(json.dumps(record) + json.dumps(observation) + json.dumps(assertions), "drill output")
    rg.validate_record("gate-result", record)
    rg.write_atomic_exclusive(args.result_out, rg.canonical_bytes(record))
    printer(f"{label}: {status}: {reason}")
    return {"passed": EXIT_PASSED, "failed": EXIT_FAILED, "not_run": EXIT_NOT_RUN}[status]
