#!/usr/bin/env python3
"""Host exposure audit: what a Telos node lets the network reach, judged against a policy.

    exposure_audit.py source --policy FILE [--compose FILE ...]
        Static: the policy is well formed and the Compose files publish exactly what it allows
        (only Traefik, only 80/443, no UDP, every overlay port on loopback).
    exposure_audit.py live --target-file FILE (--rehearsal --source-commit SHA | --candidate-lock FILE)
                           --result-out FILE [--policy FILE] [--scan-timeout SECONDS]
        Live: from a machine that is NOT the node, scan the node's address (every TCP port, a fixed
        UDP list) and compare with what the node itself reports (published ports, listeners,
        networks). Telos ports are exactly Traefik's 80/443; everything else that answers must be
        a host-management port the OPERATOR declared in the policy -- an SSH port is never labelled
        a Telos port, and an undeclared open port is a failure whoever owns it.

Exit codes: 0 passed, 1 failed, 2 unsafe or malformed input, 3 not_run (no scanner, no target).
"""

import argparse
import json
import os
import platform
import re
import shutil
import socket
import subprocess
import sys
import urllib.parse
import xml.etree.ElementTree as ElementTree
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402

GATE_ID = "host-exposure"
DEFAULT_POLICY = Path(__file__).resolve().parent.parent.parent / "config" / "exposure-policy.json"
DEFAULT_COMPOSE = ("docker-compose.yml",)
LOOPBACK = {"127.0.0.1", "::1", "[::1]"}   # plus anything in 127.0.0.0/8, see is_loopback
WILDCARD = {"", "0.0.0.0", "::", "[::]", "*"}
PORT_LIST = re.compile(r"[0-9]+(?:-[0-9]+)?(?:,[0-9]+(?:-[0-9]+)?)*\Z")
assertion, check, not_run = cid.assertion, cid.check, cid.not_run


def is_loopback(address):
    return address in LOOPBACK or str(address).split("%", 1)[0].startswith("127.")


def routable(address, networks):
    """Could a machine outside this host reach a socket bound to ``address``?

    Loopback cannot. Neither can an address inside a container network's subnet: that is a bridge
    gateway such as the one aardvark-dns answers container DNS on, and no packet from outside is routed
    to it. Everything else (a wildcard, a LAN or public address) can; the external scan then decides
    whether anything actually answers.
    """
    import ipaddress
    bare = str(address).split("%", 1)[0].strip("[]")
    if is_loopback(bare):
        return False
    try:
        ip = ipaddress.ip_address(bare)
    except ValueError:
        return True
    for network in networks or []:
        for subnet in network.get("subnets") or []:
            if ip in ipaddress.ip_network(subnet, strict=False):
                return False
    return True


class PolicyError(Exception):
    """The exposure policy is malformed."""


# ------------------------------------------------------------------ policy

def ports_of(spec):
    out = set()
    for part in str(spec).split(","):
        low, _, high = part.partition("-")
        out.update(range(int(low), int(high or low) + 1))
    return out


def load_policy(path):
    try:
        with open(path, encoding="utf-8") as handle:
            policy = json.load(handle)
    except (OSError, ValueError) as error:
        raise PolicyError(f"cannot read the exposure policy ({error.__class__.__name__})")
    if not isinstance(policy, dict) or policy.get("schemaVersion") != 1 or set(policy) != {"schemaVersion", "telos", "operatorDeclared", "scan"}:
        raise PolicyError("the exposure policy must be a schemaVersion 1 object with exactly telos, operatorDeclared and scan")
    telos = policy["telos"]
    if not isinstance(telos, dict) or set(telos) != {"publisher", "publicTcp", "publicUdp", "neverReachable", "setupContainers", "networks"}:
        raise PolicyError("telos must name publisher, publicTcp, publicUdp, neverReachable, setupContainers and networks")
    for key in ("publicTcp", "publicUdp"):
        if not isinstance(telos[key], list) or any(not isinstance(p, int) or isinstance(p, bool) or not 1 <= p <= 65535 for p in telos[key]):
            raise PolicyError(f"{key} must be a list of ports")
    if telos["publicUdp"]:
        raise PolicyError("Telos publishes no UDP port; publicUdp must be empty")
    if sorted(telos["publicTcp"]) != [80, 443]:
        raise PolicyError("Telos publishes exactly TCP 80 and 443")
    for item in telos["neverReachable"]:
        if not isinstance(item, dict) or set(item) != {"port", "what"} or not isinstance(item["port"], int):
            raise PolicyError("neverReachable entries are {port, what}")
    if set(item["port"] for item in telos["neverReachable"]) & set(telos["publicTcp"]):
        raise PolicyError("a port cannot be both public and never reachable")
    declared = policy["operatorDeclared"]
    if not isinstance(declared, dict) or set(declared) != {"tcp", "udp"}:
        raise PolicyError("operatorDeclared must be {tcp: [...], udp: [...]}")
    for key in ("tcp", "udp"):
        if not isinstance(declared[key], list) or any(not isinstance(p, int) or isinstance(p, bool) or not 1 <= p <= 65535 for p in declared[key]):
            raise PolicyError(f"operatorDeclared.{key} must be a list of ports")
    overlap = (set(declared["tcp"]) | set(declared["udp"])) & {item["port"] for item in telos["neverReachable"]}
    if overlap:
        raise PolicyError(f"an operator may not declare a Telos-internal port as host management: {sorted(overlap)}")
    if set(declared["tcp"]) & set(telos["publicTcp"]):
        raise PolicyError("an operator may not declare 80 or 443 as host management; they are Telos's")
    scan = policy["scan"]
    if not isinstance(scan, dict) or set(scan) != {"tcp", "udp"} or scan["tcp"] != "1-65535" or not PORT_LIST.match(str(scan["udp"])):
        raise PolicyError("scan must cover TCP 1-65535 and a UDP port list")
    return policy


# ------------------------------------------------------------------ static source check

def published_ports(compose_path):
    """{container name: [{hostIp, hostPort, containerPort, protocol}]} read from a Compose file's `ports:` lists."""
    import yaml
    with open(compose_path, encoding="utf-8") as handle:
        document = yaml.safe_load(handle)
    out = {}
    for name, service in (document.get("services") or {}).items():
        rows = []
        for entry in service.get("ports") or []:
            if isinstance(entry, dict):
                rows.append({"hostIp": entry.get("host_ip", ""), "hostPort": entry.get("published"), "containerPort": entry.get("target"),
                             "protocol": entry.get("protocol", "tcp")})
                continue
            text, _, protocol = str(entry).partition("/")
            text = re.sub(r"\$\{[A-Za-z_][A-Za-z0-9_]*:?-([^}]*)\}", r"\1", text)
            parts = text.split(":")
            host_ip = parts[0] if len(parts) == 3 else ""
            rows.append({"hostIp": host_ip, "hostPort": int(parts[-2]) if len(parts) >= 2 and parts[-2].isdigit() else None,
                         "containerPort": int(parts[-1]) if parts[-1].isdigit() else None, "protocol": protocol or "tcp"})
        if rows:
            out[service.get("container_name", name)] = rows
    return out


def check_source(policy, compose_paths):
    problems = []
    telos = policy["telos"]
    for path in compose_paths:
        base = os.path.basename(path)
        for service, rows in published_ports(path).items():
            for row in rows:
                loopback_only = is_loopback(row["hostIp"])
                if base == "docker-compose.yml":
                    if service != telos["publisher"]:
                        problems.append(f"{base}: {service} publishes a port; only {telos['publisher']} may")
                    elif row["protocol"] != "tcp" or row["hostPort"] not in telos["publicTcp"] or row["hostIp"] not in WILDCARD:
                        problems.append(f"{base}: {service} publishes {row}; it may publish only TCP {telos['publicTcp']} on all addresses")
                elif not loopback_only:
                    problems.append(f"{base}: {service} publishes {row['hostPort']} on {row['hostIp'] or 'all addresses'}; an overlay may publish on 127.0.0.1 only")
                if row["protocol"] != "tcp":
                    problems.append(f"{base}: {service} publishes a {row['protocol']} port; Telos publishes no UDP")
                if row["hostPort"] in {item["port"] for item in telos["neverReachable"]} and not loopback_only and base == "docker-compose.yml":
                    problems.append(f"{base}: {service} publishes a never-reachable port {row['hostPort']}")
    return problems


# ------------------------------------------------------------------ live evaluation

def evaluate_local(policy, facts):
    telos, declared = policy["telos"], policy["operatorDeclared"]
    containers = facts.get("containers")
    out = []
    if containers is None:
        return [assertion("exposure.publishers", "only Traefik publishes, only 80/443", "failed", "the container list could not be read")]
    rows = [c for c in containers if c.get("telos")]
    offenders, traefik_ports = [], set()
    for container in rows:
        for port in container.get("ports") or []:
            if container["name"] == telos["publisher"] and port["protocol"] == "tcp" and port["hostIp"] in WILDCARD:
                traefik_ports.add(port["hostPort"])
            else:
                offenders.append(f"{container['name']}:{port['hostIp'] or '*'}:{port['hostPort']}/{port['protocol']}")
    out.append(check("exposure.publishers", "only Traefik publishes host ports, only TCP 80 and 443, on all addresses",
                     not offenders and traefik_ports == set(telos["publicTcp"]),
                     f"{telos['publisher']} alone publishes TCP {sorted(traefik_ports)}", f"unexpected bindings {offenders[:5]}; Traefik ports {sorted(traefik_ports)}"))
    setup = sorted(c["name"] for c in rows if c["name"] in telos["setupContainers"] and c.get("state") == "running")
    networks_seen = facts.get("networks") or []
    setup_listening = sorted({l["port"] for l in facts.get("listeners") or [] if l["port"] in (8096, 6060) and routable(l["address"], networks_seen)})
    out.append(check("exposure.setup", "no guided-setup container or port is left behind", not setup and not setup_listening,
                     "no setup relay is running and nothing listens on the setup ports off-loopback", f"setup containers {setup}, off-loopback setup listeners {setup_listening}"))
    networks = {n["name"]: n for n in facts.get("networks") or []}
    network_problems = []
    for name, rule in telos["networks"].items():
        row = networks.get(name)
        if row is None:
            continue   # a network that is not there exposes nothing (profile-gated or not started)
        if bool(row["internal"]) != bool(rule["internal"]):
            network_problems.append(f"{name} internal={row['internal']} (policy {rule['internal']})")
    external_members = {name for name, rule in telos["networks"].items() if not rule["internal"]}
    for container in rows:
        for attached in container.get("networks") or []:
            if attached in external_members and container["name"] not in telos["networks"][attached].get("members", []):
                network_problems.append(f"{container['name']} is attached to non-internal {attached}")
    out.append(check("exposure.networks", "internal networks are internal and only declared services join the routable ones", not network_problems,
                     "telos-backend, telos-db and telos-observability are internal; only declared services join telos-ingress and telos-egress", "; ".join(network_problems[:4])))
    allowed_tcp = set(telos["publicTcp"]) | set(declared["tcp"])
    allowed_udp = set(telos["publicUdp"]) | set(declared["udp"])
    exposed_tcp = sorted({l["port"] for l in facts.get("listeners") or [] if routable(l["address"], networks_seen) and l["port"] not in allowed_tcp})
    exposed_udp = sorted({l["port"] for l in facts.get("udpListeners") or [] if routable(l["address"], networks_seen) and l["port"] not in allowed_udp})
    forbidden = sorted({l["port"] for l in (facts.get("listeners") or []) + (facts.get("udpListeners") or [])
                        if routable(l["address"], networks_seen) and l["port"] in {i["port"] for i in telos["neverReachable"]}})
    listeners_known = facts.get("listeners") is not None and facts.get("udpListeners") is not None
    out.append(check("exposure.listeners", "every listener a machine outside the host could reach is Telos's 80/443 or an operator-declared management port",
                     listeners_known and not exposed_tcp and not exposed_udp and not forbidden,
                     "no undeclared TCP or UDP listener on a routable address (loopback and container-network gateways such as container DNS are not routable)",
                     f"undeclared TCP {exposed_tcp[:6]}, undeclared UDP {exposed_udp[:6]}, never-reachable {forbidden}" if listeners_known else "listening sockets could not be read"))
    return out


def evaluate_scan(policy, scan, facts):
    telos, declared = policy["telos"], policy["operatorDeclared"]
    out = []
    if scan.get("tcp") is None:
        out.append(not_run("scan.tcp", "an external scan of every TCP port finds only 80, 443 and declared management ports", scan.get("tcpSkipped") or "no scanner"))
    else:
        open_tcp = set(scan["tcp"])
        allowed = set(telos["publicTcp"]) | set(declared["tcp"])
        extra, missing = sorted(open_tcp - allowed), sorted(set(telos["publicTcp"]) - open_tcp)
        never = sorted(open_tcp & {i["port"] for i in telos["neverReachable"]})
        complete = scan.get("tcpPortsScanned") == 65535
        out.append(check("scan.tcp", "an external scan of every TCP port finds only 80, 443 and declared management ports",
                         complete and not extra and not missing and not never,
                         f"{scan['tcpPortsScanned']} TCP ports scanned from outside; open: {sorted(open_tcp)}",
                         f"scanned {scan.get('tcpPortsScanned')} ports; undeclared open {extra[:8]}; missing {missing}; never-reachable open {never}"))
    if scan.get("udp") is None:
        out.append(not_run("scan.udp", "no Telos UDP port answers from outside", scan.get("udpSkipped") or "no UDP scan"))
    else:
        open_udp = set(scan["udp"])
        extra = sorted(open_udp - set(declared["udp"]))
        out.append(check("scan.udp", "no Telos UDP port answers from outside", not extra and scan.get("udpPortsScanned", 0) > 0,
                         f"{scan['udpPortsScanned']} UDP ports probed; open: {sorted(open_udp)}", f"undeclared UDP answering {extra[:8]}"))
    if scan.get("tcp") is not None and facts.get("listeners") is not None:
        local = {l["port"] for l in facts["listeners"] if routable(l["address"], facts.get("networks"))}
        unexplained = sorted(set(scan["tcp"]) - local)
        out.append(check("scan.agreement", "everything reachable from outside is a listener the node itself reports", not unexplained,
                         "the external scan and the node's own listener list agree", f"externally open but not listening on the node: {unexplained[:8]} (a forward or another host is answering)"))
    return out


# ------------------------------------------------------------------ scanner

def parse_nmap_xml(text):
    """(open ports by protocol, number of ports scanned, nmap version) from nmap -oX output."""
    root = ElementTree.fromstring(text)
    opened = {"tcp": set(), "udp": set()}
    for port in root.iter("port"):
        state = port.find("state")
        if state is not None and state.get("state") == "open":
            opened[port.get("protocol")].add(int(port.get("portid")))
    scanned = {}
    for info in root.iter("scaninfo"):
        scanned[info.get("protocol")] = scanned.get(info.get("protocol"), 0) + int(info.get("numservices") or 0)
    return opened, scanned, root.get("version")


def run_nmap(address, policy, timeout):
    """Scan the node from here. Returns the `scan` dict evaluate_scan expects."""
    nmap = shutil.which("nmap")
    if nmap is None:
        return {"tcp": None, "tcpSkipped": "nmap is not installed on this machine", "udp": None, "udpSkipped": "nmap is not installed on this machine"}
    scan = {"scanner": "nmap", "target": address}
    tcp = subprocess.run([nmap, "-n", "-Pn", "-sT", "-p", policy["scan"]["tcp"], "--max-retries", "2", "-T4", "-oX", "-", address],
                         capture_output=True, text=True, timeout=timeout, check=False)
    if tcp.returncode == 0:
        opened, scanned, version = parse_nmap_xml(tcp.stdout)
        scan.update(tcp=sorted(opened["tcp"]), tcpPortsScanned=scanned.get("tcp", 0), scannerVersion=version)
    else:
        scan.update(tcp=None, tcpSkipped=f"the TCP scan failed (exit {tcp.returncode})")
    prefix = [] if os.geteuid() == 0 else ["sudo", "-n"]
    udp = subprocess.run(prefix + [nmap, "-n", "-Pn", "-sU", "-p", policy["scan"]["udp"], "--max-retries", "2", "-oX", "-", address],
                         capture_output=True, text=True, timeout=timeout, check=False)
    if udp.returncode == 0:
        opened, scanned, _ = parse_nmap_xml(udp.stdout)
        scan.update(udp=sorted(opened["udp"]), udpPortsScanned=scanned.get("udp", 0))
    else:
        scan.update(udp=None, udpSkipped="the UDP scan needs root or passwordless sudo on the scanning machine")
    return scan


# ------------------------------------------------------------------ commands

def command_source(args):
    try:
        policy = load_policy(args.policy)
    except PolicyError as error:
        print(f"audit-host-exposure: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    problems = check_source(policy, args.compose or [str(Path(__file__).resolve().parent.parent.parent / name) for name in DEFAULT_COMPOSE])
    if problems:
        for problem in problems:
            print(f"audit-host-exposure: {problem}", file=sys.stderr)
        return dc.EXIT_FAILED
    print("audit-host-exposure: the policy is well formed and Compose publishes only Traefik 80/443 (overlays: loopback only)")
    return dc.EXIT_PASSED


def command_live(argv, remote_factory=dc.Remote, scanner=run_nmap, verifier_identity=dc.local_machine_id, resolver=socket.gethostbyname):
    parser = argparse.ArgumentParser(prog="exposure_audit.py live")
    parser.add_argument("--target-file", required=True)
    parser.add_argument("--policy", default=str(DEFAULT_POLICY))
    parser.add_argument("--scan-timeout", type=int, default=1800)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    if not Path(args.result_out).resolve().parent.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    policy = load_policy(args.policy)
    target = dc.load_target(args.target_file)
    remote = remote_factory(target)
    dc.verify_node_identity(remote, target)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the scanner machine is the node itself, so exposure from outside cannot be shown; run the audit from another machine")
    facts = remote.collect("exposure", target["nodePath"])
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the audit")
    host = urllib.parse.urlsplit(target["origin"]).hostname
    address = resolver(host)
    own = set(facts.get("addresses") or [])
    assertions = []
    assertions.append(check("exposure.identity", "the name resolves to an address the audited node owns",
                            address in own, f"{host} resolves to {address}, one of the node's addresses",
                            f"{host} resolves to {address}, which the audited node does not report owning ({sorted(own)[:3]})"))
    assertions += evaluate_local(policy, facts)
    scan = scanner(address, policy, args.scan_timeout) if address in own else {"tcp": None, "tcpSkipped": "the scan target is not the audited node", "udp": None, "udpSkipped": "the scan target is not the audited node"}
    assertions += evaluate_scan(policy, scan, facts)
    observation = {"schemaVersion": 1, "origin": target["origin"], "policy": policy, "facts": facts, "scan": scan}
    return dc.finalize(args, mode, commit, lock_digest, GATE_ID, assertions, observation, cid.host_descriptor_for(facts),
                       {"python3": platform.python_version(), "podman": cid.podman_version(facts)},
                       facts["observedAt"], label="audit-host-exposure")


def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    try:
        if argv[:1] == ["source"]:
            parser = argparse.ArgumentParser(prog="exposure_audit.py source")
            parser.add_argument("--policy", default=str(DEFAULT_POLICY))
            parser.add_argument("--compose", action="append")
            return command_source(parser.parse_args(argv[1:]))
        if argv[:1] == ["live"]:
            return command_live(argv[1:])
        print("usage: exposure_audit.py source|live ...", file=sys.stderr)
        return dc.EXIT_USAGE
    except dc.NotRun as error:
        print(f"audit-host-exposure: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError, PolicyError) as error:
        print(f"audit-host-exposure: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError, ElementTree.ParseError) as error:
        print(f"audit-host-exposure: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
