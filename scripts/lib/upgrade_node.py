#!/usr/bin/env python3
"""The fixed node-side program of the upgrade/rollback drill.

The drill (scripts/lib/upgrade_drill.py) sends this file on stdin to ``python3 -I -`` on the node (as root, over the
same strict SSH path as node_observe.py) and passes a few arguments from a safe alphabet. Nothing in a drill-target
descriptor can change what runs: the program either OBSERVES the lifecycle state or runs exactly ONE of the node's own
lifecycle scripts with fixed arguments and the fixed confirmation token of that script.

    upgrade_node.py state BASE NODE [GENERATION_ID...]
    upgrade_node.py upgrade  BASE NODE TO_ID FROM_ID TAG
    upgrade_node.py rollback BASE NODE TO_ID OPERATION_ID TAG
    upgrade_node.py restore  BASE NODE TO_ID FROM_ID OPERATION_ID TAG (rehearsal|candidate)

BASE is the release base (the directory holding ``generations/`` and ``current``), NODE the node path. Generation ids
are 64 hex characters; the candidate locks the scripts need are read from NODE/state/candidates/<id>/ and must hash to
their id. A script runs from the generation the lifecycle says owns that step (upgrade and restore from the TARGET, a
fast rollback from the generation being left), never from this program's own checkout. Output is JSON on stdout.
"""

import hashlib
import json
import os
import re
import subprocess
import sys
import time

SCHEMA = 1
GENERATION = re.compile(r"[0-9a-f]{64}\Z")
OPERATION = re.compile(r"[0-9a-f]{32}\Z")
TAG = re.compile(r"[a-z0-9][a-z0-9-]{0,39}\Z")
CONFIRM = {"upgrade": "UPGRADE-THIS-NODE", "rollback": "ROLLBACK-THIS-NODE", "restore": "RESTORE-RECORDED-ROLLBACK"}
PATH = "/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/bin:/usr/local/sbin"
SCRIPT_TIMEOUT = 1500
PROBE_TIMEOUT = 120
TAIL_LINES = 40
LINE_LIMIT = 240


class ProgramError(Exception):
    """A usage or safety refusal; the message is safe to print."""


def now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def absolute(path, what):
    if not os.path.isabs(path) or os.path.normpath(path) != path or path == "/":
        raise ProgramError(f"{what} must be an absolute normalized path")
    return path


def generation_id(value):
    if not GENERATION.fullmatch(value):
        raise ProgramError("a generation id is 64 lowercase hex characters")
    return value


def read_json(path):
    try:
        with open(path, "rb") as handle:
            document = json.loads(handle.read(1 << 20).decode("utf-8"))
    except (OSError, ValueError):
        return None
    return document if isinstance(document, dict) else None


# The only variable taken from this program's own environment: the operator-verified Compose provider, which a node's operator
# gets through /etc/environment and which the CLI refuses to guess. It is passed on, never read from the drill's descriptor.
PASSED_THROUGH = ("PODMAN_COMPOSE_PROVIDER",)


def child_env(base, node, generation=None):
    env = {"PATH": PATH, "LC_ALL": "C", "HOME": "/root", "TELOS_RELEASE_BASE": base, "TELOS_NODE_PATH": node}
    for name in PASSED_THROUGH:
        if os.environ.get(name):
            env[name] = os.environ[name]
    if generation:
        env["TELOS_RELEASE_ROOT"] = os.path.join(base, "generations", generation)
    return env


def run(argv, env, timeout):
    started = time.monotonic()
    try:
        completed = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=env, timeout=timeout, check=False)
    except subprocess.TimeoutExpired as error:
        return None, (error.stdout or b"").decode("utf-8", "replace"), time.monotonic() - started
    except OSError as error:
        return None, f"could not run {os.path.basename(argv[0])}: {error.__class__.__name__}", time.monotonic() - started
    return completed.returncode, completed.stdout.decode("utf-8", "replace"), time.monotonic() - started


def tail(text):
    lines = [line.rstrip() for line in text.splitlines() if line.strip()]
    return [line[:LINE_LIMIT] for line in lines[-TAIL_LINES:]]


def current_generation(base):
    try:
        target = os.readlink(os.path.join(base, "current"))
    except OSError:
        return None
    match = re.fullmatch(r"(?:.*/)?generations/([0-9a-f]{64})/?", target)
    return match.group(1) if match else None


def generation_facts(base, identifier):
    manifest = read_json(os.path.join(base, "generations", identifier, "generation.json"))
    if not manifest:
        return {"present": False}
    migrations = manifest.get("migrations") or {}
    return {"present": True, "migrations": {key: migrations.get(key) for key in ("min", "current", "max")},
            "rollbackCompatibleThrough": manifest.get("rollbackCompatibleThrough")}


def cli(base, node, generation, *arguments):
    return run([os.path.join(base, "generations", generation, "telos"), *arguments], child_env(base, node, generation), PROBE_TIMEOUT)


def provider_facts(base, node):
    """Can the lifecycle scripts find the Compose provider? Without it an upgrade fails after it has stopped the writers."""
    path = child_env(base, node).get("PODMAN_COMPOSE_PROVIDER", "")
    return {"set": bool(path), "usable": bool(path) and os.path.isabs(path) and os.path.isfile(path) and os.access(path, os.X_OK)}


def listing(path):
    try:
        return sorted(name for name in os.listdir(path) if not name.startswith("."))
    except OSError:
        return []


def mode_state(base, node, identifiers):
    current = current_generation(base)
    schema = gateway = None
    if current:
        _, output, _ = cli(base, node, current, "migrate", "--status")
        lines = [line.strip() for line in output.splitlines() if line.strip()]
        if lines and lines[-1].isdigit():
            schema = int(lines[-1])
        _, output, _ = cli(base, node, current, "status", "--json")
        try:
            gateway = ((json.loads(output).get("gateway") or {}).get("status"))
        except (ValueError, AttributeError):
            gateway = None
    return {
        "schemaVersion": SCHEMA, "mode": "state", "observedAt": now_iso(), "current": current,
        "receipt": read_json(os.path.join(node, "state", "installed-generation.json")),
        "journal": read_json(os.path.join(node, "state", "operation.json")),
        "operationsKept": listing(os.path.join(node, "state", "operations")),
        "schema": schema, "gateway": gateway, "composeProvider": provider_facts(base, node),
        "generations": {identifier: generation_facts(base, identifier) for identifier in identifiers},
        "rollbackDirs": listing(os.path.join(os.path.dirname(node), "telos-rollback")),
    }


def lock_path(node, identifier):
    """The installed candidate lock of a generation, proved to be that generation's by its digest."""
    path = os.path.join(node, "state", "candidates", identifier, "candidate-lock.json")
    try:
        with open(path, "rb") as handle:
            data = handle.read(1 << 22)
    except OSError:
        raise ProgramError(f"the candidate lock of {identifier[:12]} is not installed on the node")
    if hashlib.sha256(data).hexdigest() != identifier:
        raise ProgramError(f"the installed lock of {identifier[:12]} does not hash to its generation id")
    return path


def mode_run(action, base, node, arguments):
    if action == "upgrade":
        to_id, from_id, tag = arguments
        generation, argv_tail = to_id, ["--generation", os.path.join(base, "generations", to_id), "--candidate-lock", lock_path(node, to_id),
                                        "--current-candidate-lock", lock_path(node, from_id)]
        script = "upgrade.sh"
    elif action == "rollback":
        to_id, operation, tag = arguments
        # Undoing an upgrade runs from the generation the upgrade made active.
        generation, argv_tail, script = to_id, ["--operation-id", operation], "rollback.sh"
    else:
        to_id, from_id, operation, tag, flavour = arguments
        argv_tail = ["--mode", "rollback", "--operation-id", operation]
        if flavour == "rehearsal":
            argv_tail.append("--rehearsal")
        elif flavour == "candidate":
            argv_tail += ["--candidate-lock", lock_path(node, from_id)]
        else:
            raise ProgramError("the restore flavour is rehearsal or candidate")
        generation, script = to_id, "restore.sh"
    if not TAG.fullmatch(tag):
        raise ProgramError("the tag is 1-40 characters of a-z, 0-9 and dash")
    if action != "upgrade" and not OPERATION.fullmatch(operation):
        raise ProgramError("an operation id is 32 lowercase hex characters")
    path = os.path.join(base, "generations", generation, "scripts", script)
    if not os.path.isfile(path) or os.path.islink(path):
        raise ProgramError(f"{script} is not installed in generation {generation[:12]}")
    results = os.path.join(node, "state", "drill-results")
    os.makedirs(results, mode=0o700, exist_ok=True)
    result_path = os.path.join(results, f"{action}-{tag}.json")
    if os.path.lexists(result_path):
        raise ProgramError("that tag was already used; the lifecycle scripts never overwrite a result")
    code, output, seconds = run(["bash", path, *argv_tail, "--result-out", result_path, "--confirm", CONFIRM[action]],
                                child_env(base, node, generation), SCRIPT_TIMEOUT)
    return {"schemaVersion": SCHEMA, "mode": "run", "action": action, "observedAt": now_iso(), "exitCode": code,
            "timedOut": code is None, "seconds": round(seconds, 1), "outputTail": tail(output), "result": read_json(result_path)}


def main(argv):
    try:
        if len(argv) < 4:
            raise ProgramError("usage: upgrade_node.py state|upgrade|rollback|restore BASE NODE ...")
        mode, base, node = argv[1], absolute(argv[2], "BASE"), absolute(argv[3], "NODE")
        rest = argv[4:]
        if mode == "state":
            document = mode_state(base, node, [generation_id(item) for item in rest])
        elif mode == "upgrade" and len(rest) == 3:
            document = mode_run("upgrade", base, node, [generation_id(rest[0]), generation_id(rest[1]), rest[2]])
        elif mode == "rollback" and len(rest) == 3:
            document = mode_run("rollback", base, node, [generation_id(rest[0]), rest[1], rest[2]])
        elif mode == "restore" and len(rest) == 5:
            document = mode_run("restore", base, node, [generation_id(rest[0]), generation_id(rest[1]), rest[2], rest[3], rest[4]])
        else:
            raise ProgramError("invalid mode or arguments")
    except ProgramError as error:
        print(f"upgrade_node: {error}", file=sys.stderr)
        return 2
    except OSError as error:
        print(f"upgrade_node: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return 1
    json.dump(document, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
