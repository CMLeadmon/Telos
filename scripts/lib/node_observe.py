#!/usr/bin/env python3
"""Read-only fact collector for a Telos node, run on the node by a drill.

This file is self-contained on purpose: a drill pipes it over SSH (``python3 -I -``)
so nothing has to be installed on the node first. It only *reads*: file metadata,
the Podman API through its CLI, listening sockets. It reports facts and makes no
judgement; the drill evaluates them elsewhere. It never prints a secret: node.env
is reduced to key names, a placeholder flag per key and the values of an explicit
non-secret allowlist.

Modes (argv[1]):
  identity                 machine/boot identity only
  clean                    what already exists on the host (for a clean-install check)
  install NODE_PATH        the full install picture described in the clean-install runbook
  watch-setup NODE_PATH SECONDS INTERVAL
                           poll for published setup ports and persist the result under
                           NODE_PATH/state/ (root-only), printing it as JSON
"""

import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import time

SCHEMA = 1
SETUP_WATCH_FILE = "drill-setup-watch.json"
NON_SECRET_VALUES = {
    "TELOS_ENV", "TELOS_DOMAIN", "TELOS_PUBLIC_ORIGIN", "TELOS_ALLOWED_ORIGINS",
    "JELLYFIN_LIBRARY_IDS", "GRIMMORY_LIBRARY_IDS", "TELOS_NODE_PATH", "TELOS_CONFIG_PATH",
    "STORAGE_PATH", "TRAEFIK_HTTP_PORT", "TRAEFIK_HTTPS_PORT", "APP_UID", "APP_GID",
}
PLACEHOLDER = re.compile(r"change-me|generate-me|^$", re.I)
CONTAINER_PREFIX = "telos-"  # every Compose service sets container_name: telos-<service>
COMMAND_TIMEOUT = 30


def now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def run(argv, timeout=COMMAND_TIMEOUT):
    """Run a fixed argv with a clean-ish environment; return (exit, stdout) or (None, '')."""
    try:
        completed = subprocess.run(
            argv, stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout, check=False,
            env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/bin:/usr/local/sbin", "LC_ALL": "C", "HOME": "/root"})
    except (OSError, subprocess.SubprocessError):
        return None, ""
    return completed.returncode, completed.stdout.decode("utf-8", "replace")


def read_text(path, limit=65536):
    try:
        with open(path, "rb") as handle:
            return handle.read(limit).decode("utf-8", "replace")
    except OSError:
        return None


def sha256_text(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def birth_time(path):
    """Birth time via statx-backed `stat -c %W`; None where the filesystem lacks it."""
    code, out = run(["stat", "-c", "%W", "--", path])
    if code != 0:
        return None
    try:
        value = int(out.strip())
    except ValueError:
        return None
    return value if value > 0 else None


def file_facts(path):
    """Metadata about one path without following a final symlink."""
    try:
        info = os.lstat(path)
    except FileNotFoundError:
        return {"path": path, "exists": False}
    except OSError as error:
        return {"path": path, "exists": False, "error": error.__class__.__name__}
    mode = info.st_mode
    kind = ("symlink" if stat.S_ISLNK(mode) else "dir" if stat.S_ISDIR(mode)
            else "file" if stat.S_ISREG(mode) else "other")
    return {
        "path": path, "exists": True, "type": kind, "uid": info.st_uid, "gid": info.st_gid,
        "mode": "%04o" % stat.S_IMODE(mode), "size": info.st_size, "mtime": int(info.st_mtime),
        "birth": birth_time(path),
    }


def ancestor_facts(path):
    """Facts for every ancestor of an absolute path (a hostile symlink or a writable
    directory above the node could redirect or replace it)."""
    facts, current = [], os.path.dirname(path)
    while current and current != "/":
        facts.append(file_facts(current))
        current = os.path.dirname(current)
    facts.append(file_facts("/"))
    return facts


def host_facts():
    machine = None
    raw = None
    try:
        with open("/etc/machine-id", "rb") as handle:
            raw = handle.read(4096)
    except OSError:
        pass
    if raw is not None:
        machine = sha256_text(raw)
    os_id = version = None
    for line in (read_text("/etc/os-release") or "").splitlines():
        key, _, value = line.partition("=")
        value = value.strip().strip('"').strip("'")
        if key == "ID":
            os_id = value
        elif key == "VERSION_ID":
            version = value
    code, arch = run(["uname", "-m"])
    code, kernel = run(["uname", "-r"])
    return {
        "machineIdSha256": machine, "bootId": (read_text("/proc/sys/kernel/random/boot_id") or "").strip() or None,
        "osId": os_id, "osVersion": version, "arch": arch.strip() or None, "kernel": kernel.strip() or None,
        "euid": os.geteuid(),
    }


def first_version(text):
    match = re.search(r"[0-9]+(?:\.[0-9]+)+", text or "")
    return match.group(0) if match else None


def tool_facts():
    podman = None
    code, out = run(["podman", "--version"])
    if code == 0:
        code_info, info = run(["podman", "info", "--format", "{{.Host.Security.Rootless}}"])
        podman = {"version": first_version(out), "rootless": (info.strip() == "true") if code_info == 0 else None}
    provider = None
    candidate = os.environ.get("PODMAN_COMPOSE_PROVIDER")
    if not candidate:
        for directory in os.environ.get("PATH", "").split(":"):
            path = os.path.join(directory, "podman-compose")
            if os.access(path, os.X_OK) and not os.path.isdir(path):
                candidate = path
                break
    if candidate:
        code, out = run([candidate, "--version"])
        provider = {"path": candidate, "version": first_version(out.splitlines()[0] if out else "") if code == 0 else None}
    return {"podman": podman, "provider": provider}


def parse_env_keys(text):
    """Key names, a placeholder flag per key, and non-secret values. Never any secret."""
    keys, placeholders, values = [], [], {}
    for raw in (text or "").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        key = key.strip()
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        keys.append(key)
        if PLACEHOLDER.search(value):
            placeholders.append(key)
        if key in NON_SECRET_VALUES:
            values[key] = value
    return {"keys": sorted(set(keys)), "placeholderKeys": sorted(set(placeholders)), "values": values}


def node_facts(node_path):
    facts = {"path": node_path, "dir": file_facts(node_path), "ancestors": ancestor_facts(node_path)}
    env_path = os.path.join(node_path, "node.env")
    env = file_facts(env_path)
    if env.get("exists") and env.get("type") == "file":
        env.update(parse_env_keys(read_text(env_path, 262144)))
    facts["nodeEnv"] = env
    config_dir = os.path.join(node_path, "config")
    config = file_facts(config_dir)
    config["files"] = []
    if config.get("exists") and config.get("type") == "dir":
        try:
            names = sorted(os.listdir(config_dir))
        except OSError:
            names = []
        for name in names:
            config["files"].append(file_facts(os.path.join(config_dir, name)))
    facts["config"] = config
    facts["state"] = file_facts(os.path.join(node_path, "state"))
    watch = None
    text = read_text(os.path.join(node_path, "state", SETUP_WATCH_FILE), 262144)
    if text:
        try:
            watch = json.loads(text)
        except ValueError:
            watch = {"invalid": True}
    facts["setupWatch"] = watch
    return facts


# ------------------------------------------------------------------------ Podman

def _port_rows(settings):
    rows = []
    for key, bindings in sorted((settings or {}).items()):
        port, _, protocol = key.partition("/")
        for binding in bindings or []:
            rows.append({
                "containerPort": int(port) if port.isdigit() else None, "protocol": protocol or "tcp",
                "hostIp": (binding or {}).get("HostIp", ""), "hostPort": int((binding or {}).get("HostPort") or 0),
            })
    return rows


def container_rows():
    code, out = run(["podman", "ps", "-a", "--format", "{{.ID}}"])
    if code != 0:
        return None
    ids = [line.strip() for line in out.splitlines() if line.strip()]
    if not ids:
        return []
    code, out = run(["podman", "inspect", "--type", "container"] + ids)
    if code != 0:
        return None
    try:
        documents = json.loads(out)
    except ValueError:
        return None
    rows = []
    for item in documents:
        state = item.get("State") or {}
        host_config = item.get("HostConfig") or {}
        config = item.get("Config") or {}
        networks = sorted(((item.get("NetworkSettings") or {}).get("Networks") or {}).keys())
        mounts = [{"destination": m.get("Destination"), "type": m.get("Type"), "readOnly": not bool(m.get("RW", True))}
                  for m in item.get("Mounts") or []]
        name = (item.get("Name") or "").lstrip("/")
        rows.append({
            "name": name, "id": (item.get("Id") or "")[:64], "image": item.get("ImageName") or config.get("Image"),
            "imageId": item.get("Image"), "state": state.get("Status"), "exitCode": state.get("ExitCode"),
            "created": item.get("Created"), "startedAt": state.get("StartedAt"), "user": config.get("User") or "",
            "networks": networks, "mounts": sorted(mounts, key=lambda m: str(m["destination"])),
            "ports": _port_rows(host_config.get("PortBindings")),
            "telos": name.startswith(CONTAINER_PREFIX),
        })
    return sorted(rows, key=lambda r: r["name"])


def volume_rows():
    code, out = run(["podman", "volume", "ls", "--format", "json"])
    if code != 0:
        return None
    try:
        documents = json.loads(out)
    except ValueError:
        return None
    return sorted(({"name": v.get("Name"), "created": v.get("CreatedAt")} for v in documents), key=lambda v: str(v["name"]))


def listener_rows():
    code, out = run(["ss", "-Hltn"])
    if code != 0:
        return None
    rows = []
    for line in out.splitlines():
        parts = line.split()
        if len(parts) >= 4:
            address, _, port = parts[3].rpartition(":")
            if port.isdigit():
                rows.append({"address": address.strip("[]"), "port": int(port)})
    return sorted(rows, key=lambda r: (r["port"], r["address"]))


# ------------------------------------------------------------------------- modes

def mode_identity():
    return {"schemaVersion": SCHEMA, "mode": "identity", "observedAt": now_iso(), "host": host_facts()}


def mode_clean():
    paths = ["/var/lib/telos", "/srv/telos", "/etc/telos"]
    return {
        "schemaVersion": SCHEMA, "mode": "clean", "observedAt": now_iso(), "host": host_facts(),
        "paths": [file_facts(p) for p in paths], "containers": container_rows(), "volumes": volume_rows(),
    }


def mode_install(node_path):
    return {
        "schemaVersion": SCHEMA, "mode": "install", "observedAt": now_iso(), "host": host_facts(),
        "tools": tool_facts(), "node": node_facts(node_path), "containers": container_rows(),
        "volumes": volume_rows(), "listeners": listener_rows(),
    }


def mode_watch_setup(node_path, seconds, interval):
    deadline = time.monotonic() + seconds
    samples, seen = [], set()
    while True:
        rows = container_rows() or []
        for row in rows:
            if not re.search(r"jellyfin|grimmory", row["name"]):
                continue
            for port in row["ports"]:
                key = (row["name"], port["containerPort"], port["hostIp"], port["hostPort"])
                if key not in seen:
                    seen.add(key)
                    samples.append({"container": row["name"], "observedAt": now_iso(), **port})
        if time.monotonic() >= deadline:
            break
        time.sleep(interval)
    document = {
        "schemaVersion": SCHEMA, "mode": "watch-setup", "observedAt": now_iso(), "seconds": seconds,
        "intervalSeconds": interval, "bindings": samples,
    }
    directory = os.path.join(node_path, "state")
    if os.path.isdir(directory):
        target = os.path.join(directory, SETUP_WATCH_FILE)
        temporary = target + ".tmp"
        descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(descriptor, "w") as handle:
            json.dump(document, handle, sort_keys=True)
        os.replace(temporary, target)
    return document


def main(argv):
    if len(argv) < 2:
        print("usage: node_observe.py identity|clean|install NODE|watch-setup NODE SECONDS INTERVAL", file=sys.stderr)
        return 2
    mode = argv[1]
    try:
        if mode == "identity" and len(argv) == 2:
            document = mode_identity()
        elif mode == "clean" and len(argv) == 2:
            document = mode_clean()
        elif mode == "install" and len(argv) == 3 and os.path.isabs(argv[2]):
            document = mode_install(os.path.normpath(argv[2]))
        elif mode == "watch-setup" and len(argv) == 5 and os.path.isabs(argv[2]):
            seconds, interval = int(argv[3]), float(argv[4])
            if not (1 <= seconds <= 3600 and 0.2 <= interval <= 60):
                raise ValueError("seconds 1..3600, interval 0.2..60")
            document = mode_watch_setup(os.path.normpath(argv[2]), seconds, interval)
        else:
            print("node_observe: invalid mode or arguments", file=sys.stderr)
            return 2
    except (ValueError, OSError) as error:
        print(f"node_observe: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return 1
    json.dump(document, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
