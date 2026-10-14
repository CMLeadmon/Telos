#!/usr/bin/env python3
"""Sample a running Telos node's resource use for the capacity drill. Runs ON the node, as root.

    collect-runtime-stats.py --seconds N [--interval 5] [--node-path /var/lib/telos]

Prints one JSON object per line on stdout, one per interval, and nothing else:

  {"t": <epoch seconds>, "containers": {name: {"status", "health", "restarts", "oomKilled", "cpuUsec",
    "cpuQuotaCores", "memoryBytes", "memoryLimitBytes", "oomKills"}}, "storage": {"shared": {"freeBytes",
    "totalBytes"}, "node": {...}}, "redis": {"usedBytes", "maxBytes"}, "gateway": {"dbPoolAcquired",
    "dbPoolMax", "activeStreams", "streamMax"}, "memberSessions": <live sessions of capacity-* accounts>}

Counters come from each container's cgroup (the scope that holds the limits), identities from
``podman inspect``, Redis from ``redis-cli info memory``, the gateway's saturation gauges from its internal
metrics listener on the observability network, and storage from statvfs. A value that cannot be read is
``null``, never a guess. Nothing sampled is secret: no environment, no command line, no log text.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

CGROUP_ROOT = "/sys/fs/cgroup"
GAUGES = {
    "telos_db_pool_acquired_connections": "dbPoolAcquired",
    "telos_db_pool_max_connections": "dbPoolMax",
    "telos_active_streams": "activeStreams",
    "telos_stream_capacity_max": "streamMax",
}
SESSIONS_SQL = ("SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id "
                "WHERE u.username LIKE 'capacity-%' AND s.revoked_at IS NULL AND s.expires_at > NOW()")


def run(argv, timeout=20):
    try:
        completed = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, check=False,
                                   env={"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL": "C"})
    except (OSError, subprocess.SubprocessError):
        return None
    return completed.stdout if completed.returncode == 0 else None


def read(path):
    try:
        with open(path, encoding="utf-8") as handle:
            return handle.read()
    except OSError:
        return None


def cgroup_numbers(cgroup_path, root=CGROUP_ROOT, reader=read):
    """cpu/memory counters from a container's scope directory; every field None when unreadable."""
    base = os.path.join(root, cgroup_path.lstrip("/"))
    out = {"cpuUsec": None, "cpuQuotaCores": None, "memoryBytes": None, "memoryLimitBytes": None, "oomKills": None}
    stat = reader(os.path.join(base, "cpu.stat"))
    match = re.search(r"^usage_usec (\d+)$", stat or "", re.M)
    if match:
        out["cpuUsec"] = int(match.group(1))
    cpu_max = (reader(os.path.join(base, "cpu.max")) or "").split()
    if len(cpu_max) == 2 and cpu_max[0] != "max" and cpu_max[1].isdigit() and int(cpu_max[1]) > 0:
        out["cpuQuotaCores"] = int(cpu_max[0]) / int(cpu_max[1])
    current = (reader(os.path.join(base, "memory.current")) or "").strip()
    out["memoryBytes"] = int(current) if current.isdigit() else None
    limit = (reader(os.path.join(base, "memory.max")) or "").strip()
    out["memoryLimitBytes"] = int(limit) if limit.isdigit() else None
    events = reader(os.path.join(base, "memory.events"))
    match = re.search(r"^oom_kill (\d+)$", events or "", re.M)
    out["oomKills"] = int(match.group(1)) if match else None
    return out


def container_row(item, root=CGROUP_ROOT, reader=read):
    state = item.get("State") or {}
    health = (state.get("Health") or {}).get("Status")
    row = {"status": state.get("Status"), "health": health, "restarts": item.get("RestartCount"), "oomKilled": bool(state.get("OOMKilled"))}
    row.update(cgroup_numbers(state.get("CgroupPath") or "", root, reader) if state.get("CgroupPath") else
               {"cpuUsec": None, "cpuQuotaCores": None, "memoryBytes": None, "memoryLimitBytes": None, "oomKills": None})
    return row


def inspect_all(runner=run):
    names = (runner(["podman", "ps", "-a", "--filter", "name=^telos-", "--format", "{{.Names}}"]) or "").split()
    if not names:
        return {}, {}
    text = runner(["podman", "inspect", "--type", "container", *names])
    try:
        items = json.loads(text) if text else []
    except ValueError:
        items = []
    return {(i.get("Name") or "").lstrip("/"): i for i in items}, {}


def parse_gauges(text):
    out = {value: None for value in GAUGES.values()}
    for line in (text or "").splitlines():
        if line.startswith("#"):
            continue
        name, _, value = line.partition(" ")
        if name in GAUGES:
            try:
                out[GAUGES[name]] = float(value.strip())
            except ValueError:
                pass
    return out


def parse_redis(text):
    used = re.search(r"^used_memory:(\d+)", text or "", re.M)
    maximum = re.search(r"^maxmemory:(\d+)", text or "", re.M)
    return {"usedBytes": int(used.group(1)) if used else None, "maxBytes": int(maximum.group(1)) if maximum else None}


def storage_row(path, statvfs=os.statvfs):
    try:
        info = statvfs(path)
    except OSError:
        return {"freeBytes": None, "totalBytes": None}
    return {"freeBytes": info.f_bavail * info.f_frsize, "totalBytes": info.f_blocks * info.f_frsize}


def shared_storage_path(node_path, reader=read):
    match = re.search(r"^STORAGE_PATH=['\"]?([^'\"\n]+)", reader(os.path.join(node_path, "node.env")) or "", re.M)
    return match.group(1) if match else "/mnt/storage/shared"


def gateway_gauges(items, fetch=None, timeout=5):
    address = (((items.get("telos-core") or {}).get("NetworkSettings") or {}).get("Networks") or {}).get("telos-observability", {}).get("IPAddress")
    if not address:
        return {value: None for value in GAUGES.values()}
    try:
        with (fetch or urllib.request.urlopen)(f"http://{address}:9090/metrics", timeout=timeout) as response:
            return parse_gauges(response.read().decode("utf-8", "replace"))
    except (OSError, ValueError):
        return {value: None for value in GAUGES.values()}


def sample(node_path, now=time.time, runner=run, root=CGROUP_ROOT, reader=read, statvfs=os.statvfs, fetch=None):
    items, _ = inspect_all(runner)
    redis = parse_redis(runner(["podman", "exec", "telos-redis", "redis-cli", "info", "memory"]))
    sessions = (runner(["podman", "exec", "telos-postgres", "sh", "-c",
                        'exec psql --no-psqlrc -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "$0"', SESSIONS_SQL]) or "").strip()
    return {
        "t": now(),
        "containers": {name: container_row(item, root, reader) for name, item in sorted(items.items())},
        "storage": {"shared": storage_row(shared_storage_path(node_path, reader), statvfs), "node": storage_row(node_path, statvfs)},
        "redis": redis,
        "gateway": gateway_gauges(items, fetch),
        "memberSessions": int(sessions) if sessions.isdigit() else None,
    }


def main(argv=None, out=sys.stdout, clock=time.monotonic, sleeper=time.sleep, sampler=sample):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--seconds", type=float, required=True)
    parser.add_argument("--interval", type=float, default=5.0)
    parser.add_argument("--node-path", default="/var/lib/telos")
    args = parser.parse_args(argv)
    if not (1 <= args.interval <= 60 and 1 <= args.seconds <= 4 * 3600) or not os.path.isabs(args.node_path):
        print("collect-runtime-stats: seconds 1..14400, interval 1..60, absolute node path", file=sys.stderr)
        return 2
    deadline = clock() + args.seconds
    count = 0
    while True:
        started = clock()
        out.write(json.dumps(sampler(args.node_path), sort_keys=True) + "\n")
        out.flush()
        count += 1
        if clock() + args.interval > deadline:
            break
        sleeper(max(0.0, args.interval - (clock() - started)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
