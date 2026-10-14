#!/usr/bin/env python3
"""Effective container hardening: static policy checks and live inspection.

The policy (``config/runtime-hardening.json``) names every service the node runs and
the exact controls each one must have. Two modes judge it:

* ``check_static`` compares the *rendered* Compose model (what the provider will
  actually run, after interpolation and merging) with the policy.
* ``check_runtime`` compares observations of the *running* containers -- the
  effective UID and capabilities from ``/proc/1/status``, ``podman inspect`` host
  configuration, mounts, networks, published ports and listening sockets -- with the
  same policy. A Compose file that says the right thing proves nothing about a
  container that was started from an older one.

Both return a list of findings; nothing here prints or exits. A service the policy
does not name, or a policy service the model lacks, is a finding in its own right.
Documented exceptions need a reason, compensating controls and an unexpired date,
so an exception cannot quietly become permanent.
"""

import datetime
import json
import re

POLICY_SCHEMA_VERSION = 1

KNOWN_CONTROLS = {
    "noNewPrivileges", "readOnlyRoot", "capDrop", "nonRoot", "health", "limits",
}

CAPABILITY_NAMES = [
    "CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER", "FSETID", "KILL", "SETGID", "SETUID",
    "SETPCAP", "LINUX_IMMUTABLE", "NET_BIND_SERVICE", "NET_BROADCAST", "NET_ADMIN", "NET_RAW",
    "IPC_LOCK", "IPC_OWNER", "SYS_MODULE", "SYS_RAWIO", "SYS_CHROOT", "SYS_PTRACE", "SYS_PACCT",
    "SYS_ADMIN", "SYS_BOOT", "SYS_NICE", "SYS_RESOURCE", "SYS_TIME", "SYS_TTY_CONFIG", "MKNOD",
    "LEASE", "AUDIT_WRITE", "AUDIT_CONTROL", "SETFCAP", "MAC_OVERRIDE", "MAC_ADMIN", "SYSLOG",
    "WAKE_ALARM", "BLOCK_SUSPEND", "AUDIT_READ", "PERFMON", "BPF", "CHECKPOINT_RESTORE",
]

SOCKET_SOURCES = re.compile(r"(^|/)(docker|podman)\.sock$|^/run/(podman|docker)(/|$)|^/var/run/(podman|docker)(/|$)")
SIZE_UNITS = {"": 1, "b": 1, "k": 1 << 10, "kb": 1 << 10, "m": 1 << 20, "mb": 1 << 20, "g": 1 << 30, "gb": 1 << 30}


class PolicyError(Exception):
    """The policy file itself is malformed."""


# ------------------------------------------------------------------ parsing helpers

def parse_bytes(value):
    if isinstance(value, bool) or value is None:
        return None
    if isinstance(value, (int, float)):
        return int(value)
    match = re.fullmatch(r"\s*([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z]*)\s*", str(value))
    if not match or match.group(2).lower() not in SIZE_UNITS:
        return None
    return int(float(match.group(1)) * SIZE_UNITS[match.group(2).lower()])


def parse_duration_seconds(value):
    if value is None:
        return None
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value) / 1e9 if value > 1e6 else float(value)
    text = str(value).strip()
    if re.fullmatch(r"[0-9]+(\.[0-9]+)?", text):
        return float(text)
    total, matched = 0.0, False
    for number, unit in re.findall(r"([0-9]+(?:\.[0-9]+)?)(ms|us|ns|h|m|s)", text):
        matched = True
        total += float(number) * {"h": 3600, "m": 60, "s": 1, "ms": 1e-3, "us": 1e-6, "ns": 1e-9}[unit]
    return total if matched else None


def decode_capabilities(hex_mask):
    value = int(hex_mask, 16)
    return {CAPABILITY_NAMES[i] for i in range(len(CAPABILITY_NAMES)) if value & (1 << i)}


# ------------------------------------------------------------------ policy

def load_policy(path):
    try:
        with open(path, encoding="utf-8") as handle:
            policy = json.load(handle)
    except (OSError, ValueError) as error:
        raise PolicyError(f"cannot read the hardening policy: {error}") from error
    validate_policy(policy)
    return policy


def validate_policy(policy):
    if not isinstance(policy, dict) or policy.get("schemaVersion") != POLICY_SCHEMA_VERSION:
        raise PolicyError(f"policy must be an object with schemaVersion {POLICY_SCHEMA_VERSION}")
    if set(policy) != {"schemaVersion", "logBounds", "services"}:
        raise PolicyError("policy keys must be exactly schemaVersion, logBounds, services")
    bounds = policy["logBounds"]
    if not isinstance(bounds, dict) or set(bounds) != {"maxSizeMiB", "maxFiles"} or not all(isinstance(v, int) and v > 0 for v in bounds.values()):
        raise PolicyError("logBounds must be {maxSizeMiB, maxFiles} positive integers")
    services = policy["services"]
    if not isinstance(services, dict) or not services:
        raise PolicyError("policy must name at least one service")
    required = {"container", "oneShot", "image", "uid", "networks", "publishedPorts", "listeners", "writable",
                "limits", "restart", "stopGraceSeconds", "health", "capAdd", "exceptions"}
    for name, service in services.items():
        if not isinstance(service, dict):
            raise PolicyError(f"{name}: must be an object")
        optional = {"user", "profile"}
        if not required <= set(service) or set(service) - required - optional:
            raise PolicyError(f"{name}: keys must be {sorted(required)} plus optional {sorted(optional)}")
        image = service["image"]
        if not isinstance(image, dict) or image.get("kind") not in ("locked", "built") or \
                (image["kind"] == "locked" and not image.get("key")) or (image["kind"] == "built" and not image.get("artifact")):
            raise PolicyError(f"{name}: image must be {{kind: locked, key}} or {{kind: built, artifact}}")
        limits = service["limits"]
        if not isinstance(limits, dict) or set(limits) != {"cpus", "memoryMiB", "pids", "nofile"} or \
                not all(isinstance(v, (int, float)) and v > 0 for v in limits.values()):
            raise PolicyError(f"{name}: limits must be {{cpus, memoryMiB, pids, nofile}} positive numbers")
        writable = service["writable"]
        if not isinstance(writable, dict) or set(writable) != {"volumes", "tmpfs"} or \
                not isinstance(writable["tmpfs"], dict) or not isinstance(writable["volumes"], list):
            raise PolicyError(f"{name}: writable must be {{volumes: [target], tmpfs: {{target: maxMiB}}}}")
        if not all(isinstance(p, int) for p in service["listeners"]):
            raise PolicyError(f"{name}: listeners must be port numbers")
        for exception in service["exceptions"]:
            if not isinstance(exception, dict) or set(exception) != {"control", "reason", "compensating", "expires"}:
                raise PolicyError(f"{name}: an exception needs control, reason, compensating and expires")
            if exception["control"] not in KNOWN_CONTROLS:
                raise PolicyError(f"{name}: unknown exception control {exception['control']!r}")
            if not exception["reason"].strip() or not exception["compensating"]:
                raise PolicyError(f"{name}: an exception needs a reason and at least one compensating control")
            try:
                datetime.date.fromisoformat(exception["expires"])
            except ValueError as error:
                raise PolicyError(f"{name}: exception expiry must be an ISO date") from error


def active_exceptions(name, service, today, findings):
    """The controls this service may skip right now; expired exceptions are findings."""
    active = set()
    for exception in service["exceptions"]:
        if datetime.date.fromisoformat(exception["expires"]) < today:
            findings.append(f"{name}: the exception for {exception['control']} expired on {exception['expires']}")
        else:
            active.add(exception["control"])
    return active


# ------------------------------------------------------------------ static mode

def _listify(value):
    return [] if value is None else value if isinstance(value, list) else [value]


def _mount_target(volume):
    if isinstance(volume, dict):
        return volume.get("target"), volume.get("source"), bool(volume.get("read_only"))
    parts = str(volume).split(":")
    if len(parts) == 1:
        return parts[0], None, False
    options = parts[2].split(",") if len(parts) > 2 else []
    return parts[1], parts[0], "ro" in options


def _ulimit_nofile(service):
    nofile = (service.get("ulimits") or {}).get("nofile")
    if isinstance(nofile, dict):
        return nofile.get("soft"), nofile.get("hard")
    return (nofile, nofile) if nofile is not None else (None, None)


def _port_pairs(ports):
    pairs = set()
    for port in _listify(ports):
        if isinstance(port, dict):
            pairs.add((str(port.get("published")), str(port.get("target"))))
        else:
            text = str(port).split("/", 1)[0]
            bits = text.split(":")
            pairs.add((bits[-2] if len(bits) > 1 else "", bits[-1]))
    return pairs


def _tmpfs_entries(service):
    entries = {}
    for item in _listify(service.get("tmpfs")):
        if isinstance(item, dict):
            entries[item.get("target")] = item.get("tmpfs", {}).get("size")
            continue
        target, _, options = str(item).partition(":")
        size = None
        for option in options.split(","):
            if option.startswith("size="):
                size = option[5:]
        entries[target] = size
    return entries


def check_static(policy, model, lock, today=None):
    today = today or datetime.date.today()
    findings = []
    compose_services = (model or {}).get("services") or {}
    policy_services = policy["services"]
    for name in sorted(set(compose_services) - set(policy_services)):
        findings.append(f"{name}: runs in Compose but is not named by the hardening policy")
    for name in sorted(set(policy_services) - set(compose_services)):
        findings.append(f"{name}: named by the hardening policy but missing from Compose")
    bounds = policy["logBounds"]

    for name in sorted(set(compose_services) & set(policy_services)):
        service, rule = compose_services[name], policy_services[name]
        skip = active_exceptions(name, rule, today, findings)

        def bad(message):
            findings.append(f"{name}: {message}")

        # image
        image = service.get("image")
        if rule["image"]["kind"] == "locked":
            entry = (lock or {}).get("images", {}).get(rule["image"]["key"])
            expected = f"{entry['source']}@{entry['manifestDigest']}" if entry else None
            if expected is None:
                bad(f"image lock has no entry {rule['image']['key']}")
            elif image != expected:
                bad(f"image {image!r} is not the locked {expected}")
        elif "build" not in service:
            bad("a Telos-built image must have a build section")

        # identity
        user = service.get("user")
        if user is not None and str(user).split(":")[0] in ("0", "root"):
            bad("runs as root")
        if "user" in rule and str(user) != str(rule["user"]):
            bad(f"user {user!r} differs from the policy identity {rule['user']!r}")
        if "nonRoot" not in skip and user is None and rule["uid"] == 0:
            bad("policy allows root without a documented exception")

        # isolation
        if service.get("privileged"):
            bad("is privileged")
        for key in ("pid", "ipc", "network_mode", "userns_mode", "cgroup"):
            if str(service.get(key, "")).lower() == "host":
                bad(f"shares the host via {key}")
        if _listify(service.get("devices")):
            bad("mounts a device")
        extra = sorted(set(map(str.upper, _listify(service.get("cap_add")))) - set(rule["capAdd"]))
        if extra:
            bad(f"adds capabilities outside the policy: {', '.join(extra)}")
        if "capDrop" not in skip and "ALL" not in map(str.upper, _listify(service.get("cap_drop"))):
            bad("does not drop all capabilities")
        security = [str(item) for item in _listify(service.get("security_opt"))]
        if "noNewPrivileges" not in skip and not any(item.startswith("no-new-privileges") and not item.endswith("false") for item in security):
            bad("does not set no-new-privileges")
        for item in security:
            if "unconfined" in item or item in ("label=disable", "label:disable"):
                bad(f"disables confinement ({item})")
        if "readOnlyRoot" not in skip and service.get("read_only") is not True:
            bad("root filesystem is not read-only")

        # mounts
        allowed_volumes = set(rule["writable"]["volumes"])
        for volume in _listify(service.get("volumes")):
            target, source, read_only = _mount_target(volume)
            if source and (SOCKET_SOURCES.search(str(source)) or str(source) in ("/", "/var/run", "/run")):
                bad(f"mounts the container runtime or host root ({source})")
            if target not in allowed_volumes and not read_only:
                bad(f"writable mount {target} is not in the policy")
        for target, size in _tmpfs_entries(service).items():
            limit = rule["writable"]["tmpfs"].get(target)
            if limit is None:
                bad(f"tmpfs {target} is not in the policy")
                continue
            size_bytes = parse_bytes(size)
            if size_bytes is None or size_bytes > limit * (1 << 20):
                bad(f"tmpfs {target} is not bounded to {limit} MiB (size={size})")

        # resources
        if "limits" not in skip:
            limits = rule["limits"]
            cpus = service.get("cpus") if service.get("cpus") is not None else (((service.get("deploy") or {}).get("resources") or {}).get("limits") or {}).get("cpus")
            memory = service.get("mem_limit") if service.get("mem_limit") is not None else (((service.get("deploy") or {}).get("resources") or {}).get("limits") or {}).get("memory")
            pids = service.get("pids_limit") if service.get("pids_limit") is not None else (((service.get("deploy") or {}).get("resources") or {}).get("limits") or {}).get("pids")
            try:
                cpus_value = float(cpus) if cpus is not None else None
            except (TypeError, ValueError):
                cpus_value = None
            if cpus_value is None or cpus_value > limits["cpus"]:
                bad(f"cpus {cpus} is missing or above {limits['cpus']}")
            memory_bytes = parse_bytes(memory)
            if memory_bytes is None or memory_bytes > limits["memoryMiB"] * (1 << 20):
                bad(f"memory {memory} is missing or above {limits['memoryMiB']} MiB")
            try:
                pids_value = int(pids) if pids is not None else None
            except (TypeError, ValueError):
                pids_value = None
            if pids_value is None or pids_value <= 0 or pids_value > limits["pids"]:
                bad(f"pids_limit {pids} is missing or above {limits['pids']}")
            soft, hard = _ulimit_nofile(service)
            if soft is None or hard is None or int(soft) > limits["nofile"] or int(hard) > limits["nofile"]:
                bad(f"nofile is missing or above {limits['nofile']}")

        # logging
        logging = service.get("logging") or {}
        options = logging.get("options") or {}
        size = parse_bytes(options.get("max-size"))
        try:
            files = int(options.get("max-file"))
        except (TypeError, ValueError):
            files = None
        if size is None or size > bounds["maxSizeMiB"] * (1 << 20) or files is None or files > bounds["maxFiles"]:
            bad(f"logging is not rotated within {bounds['maxSizeMiB']} MiB x {bounds['maxFiles']} files")

        # lifecycle
        restart = str(service.get("restart", ""))
        if restart != rule["restart"]:
            bad(f"restart {restart!r} differs from {rule['restart']!r}")
        grace = parse_duration_seconds(service.get("stop_grace_period"))
        if rule["oneShot"] is False and (grace is None or grace < rule["stopGraceSeconds"]):
            bad(f"stop_grace_period {service.get('stop_grace_period')} is below {rule['stopGraceSeconds']}s")
        health = service.get("healthcheck") or {}
        if rule["health"] and "health" not in skip and (not health or health.get("disable") or health.get("test") in (None, ["NONE"])):
            bad("has no health check")

        # network exposure
        networks = service.get("networks")
        names = sorted(networks if isinstance(networks, list) else (networks or {}).keys())
        if names != sorted(rule["networks"]):
            bad(f"networks {names} differ from the policy {sorted(rule['networks'])}")
        published = _port_pairs(service.get("ports"))
        expected_ports = {(str(p["published"]), str(p["target"])) for p in rule["publishedPorts"]}
        if published != expected_ports:
            bad(f"published ports {sorted(published)} differ from the policy {sorted(expected_ports)}")
        for key in ("expose",):
            if _listify(service.get(key)) and not rule["listeners"]:
                bad("declares exposed ports but the policy expects no listener")
    return findings


# ------------------------------------------------------------------ runtime mode

def parse_proc_status(text):
    fields = {}
    for line in text.splitlines():
        if ":" in line:
            key, _, value = line.partition(":")
            fields[key.strip()] = value.strip()
    return fields


def parse_listening_ports(tcp_text):
    ports = set()
    for line in tcp_text.splitlines()[1:]:
        columns = line.split()
        if len(columns) > 3 and columns[3] == "0A":
            ports.add(int(columns[1].rsplit(":", 1)[1], 16))
    return ports


def check_runtime(policy, observations, lock, candidate_images=None, today=None):
    """observations: {service: sanitized projection of the running container}."""
    today = today or datetime.date.today()
    findings = []
    bounds = policy["logBounds"]
    policy_services = policy["services"]
    for name in sorted(set(observations) - set(policy_services)):
        findings.append(f"{name}: a container runs that the hardening policy does not name")
    for name, rule in sorted(policy_services.items()):
        observed = observations.get(name)
        skip = active_exceptions(name, rule, today, findings)

        def bad(message, _name=name):
            findings.append(f"{_name}: {message}")

        if observed is None:
            if rule.get("profile"):
                continue
            bad("is not running")
            continue
        if rule["oneShot"]:
            if observed["state"] != "exited" or observed["exitCode"] != 0:
                bad(f"one-shot service is {observed['state']} (exit {observed['exitCode']}), expected a completed run")
        elif observed["state"] != "running":
            bad(f"is {observed['state']}, expected running")
        elif rule["health"] and "health" not in skip and observed["health"] != "healthy":
            bad(f"health is {observed['health']!r}, expected healthy")

        # image identity
        if rule["image"]["kind"] == "locked":
            entry = (lock or {}).get("images", {}).get(rule["image"]["key"])
            digests = {entry["manifestDigest"], entry["platforms"]["linux/amd64"]} if entry else set()
            if observed["imageDigest"] not in digests:
                bad(f"image digest {observed['imageDigest'] or 'unknown'} is not the locked one")
        elif candidate_images is not None:
            if "sha256:" + observed["imageId"] not in candidate_images:
                bad(f"image {observed['imageId'][:12]} is not a candidate artifact")

        if not rule["oneShot"] or observed["state"] == "running":
            status = observed.get("procStatus")
            if status is None:
                bad("effective identity could not be read from /proc/1/status")
            else:
                uid = int(status["Uid"].split()[1])
                gid = int(status["Gid"].split()[1])
                if (uid == 0 or gid == 0) and "nonRoot" not in skip:
                    bad(f"PID 1 runs as uid {uid} gid {gid}")
                if rule["uid"] and uid != rule["uid"]:
                    bad(f"PID 1 runs as uid {uid}, the policy says {rule['uid']}")
                effective = decode_capabilities(status["CapEff"])
                unexpected = sorted(effective - set(rule["capAdd"]))
                if unexpected:
                    bad(f"holds effective capabilities {', '.join(unexpected)}")
                if status.get("NoNewPrivs") != "1" and "noNewPrivileges" not in skip:
                    bad("NoNewPrivs is not set on PID 1")

        host = observed["host"]
        if host["privileged"] or host["pidMode"] == "host" or host["ipcMode"] == "host" or host["networkMode"] == "host":
            bad("shares the host namespaces or is privileged")
        if host["devices"]:
            bad("has a device mounted")
        if not host["readonlyRootfs"] and "readOnlyRoot" not in skip:
            bad("root filesystem is writable")
        if "limits" not in skip:
            limits = rule["limits"]
            if not host["memoryBytes"] or host["memoryBytes"] > limits["memoryMiB"] * (1 << 20):
                bad(f"memory limit {host['memoryBytes']} is missing or above {limits['memoryMiB']} MiB")
            if not host["nanoCpus"] or host["nanoCpus"] > limits["cpus"] * 1e9:
                bad(f"cpu limit {host['nanoCpus']} is missing or above {limits['cpus']}")
            if not host["pidsLimit"] or host["pidsLimit"] <= 0 or host["pidsLimit"] > limits["pids"]:
                bad(f"pids limit {host['pidsLimit']} is missing or above {limits['pids']}")
            if host["nofile"] is None or host["nofile"] > limits["nofile"]:
                bad(f"nofile {host['nofile']} is missing or above {limits['nofile']}")
        size, files = host["logMaxSizeBytes"], host["logMaxFiles"]
        if size is None or size > bounds["maxSizeMiB"] * (1 << 20) or files is None or files > bounds["maxFiles"]:
            bad("log rotation is unbounded or missing")
        if host["restart"] != rule["restart"]:
            bad(f"restart policy {host['restart']!r} differs from {rule['restart']!r}")
        if not rule["oneShot"] and (host["stopTimeout"] is None or host["stopTimeout"] < rule["stopGraceSeconds"]):
            bad(f"stop timeout {host['stopTimeout']} is below {rule['stopGraceSeconds']}s")

        allowed_writable = set(rule["writable"]["volumes"]) | set(rule["writable"]["tmpfs"])
        for mount in observed["mounts"]:
            source = mount.get("source") or ""
            if SOCKET_SOURCES.search(source) or source in ("/", "/run", "/var/run"):
                bad(f"mounts the container runtime or host root ({source})")
            if mount["writable"] and mount["destination"] not in allowed_writable:
                bad(f"writable mount {mount['destination']} is not in the policy")
        if sorted(observed["networks"]) != sorted(rule["networks"]):
            bad(f"attached to networks {sorted(observed['networks'])}, the policy says {sorted(rule['networks'])}")
        published = {(str(p["hostPort"]), str(p["containerPort"])) for p in observed["publishedPorts"]}
        expected = {(str(p["published"]), str(p["target"])) for p in rule["publishedPorts"]}
        if published != expected:
            bad(f"published ports {sorted(published)} differ from the policy {sorted(expected)}")
        if observed.get("listeners") is not None and not rule["oneShot"]:
            unexpected_ports = sorted(set(observed["listeners"]) - set(rule["listeners"]))
            missing_ports = sorted(set(rule["listeners"]) - set(observed["listeners"]))
            if unexpected_ports:
                bad(f"listens on unexpected ports {unexpected_ports}")
            if missing_ports and observed["state"] == "running":
                bad(f"does not listen on expected ports {missing_ports}")
    return findings


# ------------------------------------------------------------------ live collection

def _nofile(ulimits):
    for item in ulimits or []:
        if str(item.get("Name", "")).upper() in ("RLIMIT_NOFILE", "NOFILE"):
            return min(int(item.get("Soft", 0)), int(item.get("Hard", 0)))
    return None


def project_inspect(inspected, status_text, tcp_text):
    """Reduce one ``podman inspect`` record (and the PID 1 /proc views) to what is judged.

    The projection carries no environment, command line or secret: only identity,
    limits, mounts and exposure.
    """
    state = inspected.get("State") or {}
    host = inspected.get("HostConfig") or {}
    config = inspected.get("Config") or {}
    log = host.get("LogConfig") or {}
    log_options = log.get("Config") or {}
    nano = host.get("NanoCpus") or 0
    if not nano and host.get("CpuQuota") and host.get("CpuPeriod"):
        nano = int(host["CpuQuota"] * 1e9 / host["CpuPeriod"])
    ports = []
    for container_port, bindings in ((inspected.get("NetworkSettings") or {}).get("Ports") or {}).items():
        for binding in bindings or []:
            ports.append({"hostPort": binding.get("HostPort"), "containerPort": container_port.split("/")[0]})
    return {
        "state": state.get("Status"),
        "exitCode": state.get("ExitCode"),
        "health": (state.get("Health") or {}).get("Status"),
        "imageId": (inspected.get("Image") or "").replace("sha256:", ""),
        "imageDigest": inspected.get("ImageDigest") or "",
        "procStatus": parse_proc_status(status_text) if status_text else None,
        "listeners": sorted(parse_listening_ports(tcp_text)) if tcp_text is not None else None,
        "host": {
            "privileged": bool(host.get("Privileged")),
            "pidMode": host.get("PidMode") or "",
            "ipcMode": host.get("IpcMode") or "",
            "networkMode": host.get("NetworkMode") or "",
            "devices": host.get("Devices") or [],
            "readonlyRootfs": bool(host.get("ReadonlyRootfs")),
            "memoryBytes": host.get("Memory") or 0,
            "nanoCpus": nano,
            "pidsLimit": host.get("PidsLimit") or 0,
            "nofile": _nofile(host.get("Ulimits")),
            "logMaxSizeBytes": parse_bytes(log_options.get("max-size")),
            "logMaxFiles": int(log_options["max-file"]) if str(log_options.get("max-file", "")).isdigit() else None,
            "restart": (host.get("RestartPolicy") or {}).get("Name") or "no",
            "stopTimeout": config.get("StopTimeout"),
        },
        "mounts": [{"source": m.get("Source", ""), "destination": m.get("Destination", ""), "writable": bool(m.get("RW")), "type": m.get("Type", "")}
                   for m in inspected.get("Mounts") or []],
        "networks": sorted((inspected.get("NetworkSettings") or {}).get("Networks") or {}),
        "publishedPorts": ports,
        "pid": state.get("Pid"),
    }


def collect_runtime(policy, inspect_container, read_proc):
    """{service: projection or None} for every policy service.

    ``inspect_container(name)`` returns the parsed inspect record or None when no
    such container exists; ``read_proc(pid, name)`` returns the text of
    /proc/<pid>/<name> from the host (these processes live in the host's PID space,
    so images without a shell or ``cat`` -- Traefik's -- can still be inspected).
    """
    observations = {}
    for name, rule in policy["services"].items():
        inspected = inspect_container(rule["container"])
        if inspected is None:
            observations[name] = None
            continue
        pid = (inspected.get("State") or {}).get("Pid") if (inspected.get("State") or {}).get("Status") == "running" else None
        status = read_proc(pid, "status") if pid else None
        tcp = None
        if pid:
            tcp4, tcp6 = read_proc(pid, "net/tcp"), read_proc(pid, "net/tcp6")
            tcp = (tcp4 or "") + "\n" + (tcp6 or "") if (tcp4 is not None or tcp6 is not None) else None
        observations[name] = project_inspect(inspected, status, tcp)
    return observations
