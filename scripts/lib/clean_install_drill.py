#!/usr/bin/env python3
"""The clean-install drill: observe a freshly installed node and run the member journey.

Run from a *verifier* machine that is not the node. It
  1. loads a protected drill target and proves the node's machine identity matches it;
  2. refuses to claim outside reachability unless the verifier is a different machine;
  3. collects install facts with the fixed read-only collector (node_observe.py);
  4. probes public TLS with the platform trust store, and the port-80 redirect;
  5. runs scripts/smoke-browser-node.sh against the origin;
  6. evaluates every fact against the install contract and writes one gate-result record
     plus sanitized attachments. The runner, not this script, writes the evidence envelope.

It changes nothing on the node. Installing is the operator's job, following
documentation/operations/clean-install-evidence.md; this drill only observes and judges.
Exit codes: 0 passed, 1 failed, 2 unsafe or malformed input, 3 not_run.
"""

import argparse
import datetime
import json
import os
import platform
import re
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import urllib.parse
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402

GATE_ID = "clean-install"
SMOKE = Path(__file__).resolve().parent.parent / "smoke-browser-node.sh"
PRODUCTION_NODE_PATH = "/var/lib/telos"
REFERENCE_RELEASE = (26, 4)   # the release the live checks run on; 24.04 is the policy floor
RUNTIME_UID = RUNTIME_GID = 10001
CLIENT_USER = {"101", "101:101"}
SIGNING_FILES = ("cursor-keys.json", "hls-key")
MIN_SIGNING_BYTES = 32
PRODUCTION_SECRETS = (
    "POSTGRES_PASSWORD", "TELOS_OWNER_PASSWORD", "TELOS_RUNTIME_PASSWORD", "REDIS_PASSWORD",
    "GRIMMORY_ADMIN_PASSWORD", "GRIMMORY_DB_PASSWORD", "MARIADB_ROOT_PASSWORD", "TELOS_BOOTSTRAP_TOKEN",
    "JELLYFIN_ADMIN_TOKEN",
)
LONG_RUNNING = (
    "telos-traefik", "telos-core", "telos-client", "telos-postgres", "telos-redis",
    "telos-egress-proxy", "telos-clamav", "telos-jellyfin", "telos-grimmory-db", "telos-grimmory",
)
ONE_SHOT = "telos-migrate"
PUBLIC_PORTS = {80, 443}
FORBIDDEN_PUBLIC_PORTS = {3000, 3306, 3310, 5432, 6060, 6379, 8080, 8081, 8096, 8443}
WILDCARD_HOSTS = {"", "0.0.0.0", "::", "[::]"}
SKEW_SECONDS = 120
CERT_MIN_DAYS = 14


def assertion(identifier, description, status, detail):
    return {"id": identifier, "description": description, "status": status, "detail": detail[:300]}


def check(identifier, description, ok, detail_ok, detail_bad):
    return assertion(identifier, description, "passed" if ok else "failed", detail_ok if ok else detail_bad)


def not_run(identifier, description, detail):
    return assertion(identifier, description, "not_run", detail)


def parse_time(text):
    """Parse the RFC 3339 forms Podman emits (nanoseconds, Z or numeric offsets)."""
    if not isinstance(text, str) or not text or text.startswith("0001-"):
        return None
    match = re.fullmatch(r"(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.(\d+))?\s*(Z|[+-]\d{2}:?\d{2})?(?: [A-Z]{3,5})?", text.strip())
    if not match:
        return None
    day, clock, fraction, zone = match.groups()
    zone = zone or "Z"
    if zone != "Z" and ":" not in zone:
        zone = zone[:3] + ":" + zone[3:]
    micro = (fraction or "0")[:6].ljust(6, "0")
    value = datetime.datetime.fromisoformat(f"{day}T{clock}.{micro}{'+00:00' if zone == 'Z' else zone}")
    return value.timestamp()


def version_tuple(text):
    return tuple(int(part) for part in text.split(".")) if text and re.fullmatch(r"\d+(?:\.\d+)*", text) else None


def mode_ok(entry, mode, uid, gid=None):
    return (entry.get("exists") and entry.get("mode") == mode and entry.get("uid") == uid
            and (gid is None or entry.get("gid") == gid))


# ------------------------------------------------------------------- evaluation

def evaluate_platform(facts):
    host, tools = facts["host"], facts.get("tools") or {}
    out = []
    version = version_tuple(host.get("osVersion"))
    ubuntu = host.get("osId") == "ubuntu" and version is not None and version >= (24, 4)
    reference = ubuntu and version == REFERENCE_RELEASE
    out.append(check("host.platform", "Ubuntu 24.04+ on x86_64", ubuntu and host.get("arch") == "x86_64",
                     f"Ubuntu {host.get('osVersion')} {host.get('arch')}" + ("" if reference else " (not the 26.04 verified reference release: unverified combination)"),
                     f"{host.get('osId')} {host.get('osVersion')} {host.get('arch')} is outside the support policy"))
    podman = tools.get("podman")
    pv = version_tuple((podman or {}).get("version"))
    out.append(check("runtime.podman", "rootful Podman 5+", pv is not None and pv >= (5, 0) and podman.get("rootless") is False,
                     f"podman {podman and podman.get('version')} rootful",
                     f"podman {podman and podman.get('version')} rootless={podman and podman.get('rootless')}" if podman else "podman is not installed or not runnable as this user"))
    provider = tools.get("provider")
    out.append(check("runtime.provider", "podman-compose 1.6.0 provider", bool(provider) and provider.get("version") == "1.6.0"
                     and os.path.isabs(provider.get("path") or ""),
                     f"podman-compose {provider and provider.get('version')} at an absolute path",
                     f"provider is {provider and provider.get('version')} (certified pin is 1.6.0)" if provider else "no podman-compose provider found"))
    return out


def evaluate_node(facts, target):
    node = facts["node"]
    out = []
    ancestors = node.get("ancestors") or []
    unsafe = [a["path"] for a in ancestors if a.get("exists") is False or a.get("type") == "symlink"
              or a.get("uid") != 0 or (int(a.get("mode", "0777"), 8) & 0o022)]
    node_dir = node.get("dir") or {}
    out.append(check("node.path", "node path is the production path, root-owned, and not redirectable",
                     node.get("path") == PRODUCTION_NODE_PATH == target["nodePath"] and node_dir.get("type") == "dir"
                     and node_dir.get("uid") == 0 and not unsafe,
                     f"{node.get('path')} is a root-owned directory under root-owned, non-writable ancestors",
                     f"node path {node.get('path')} / descriptor {target['nodePath']}; unsafe ancestors: {unsafe[:3]}; dir {node_dir.get('type')}/{node_dir.get('uid')}"))
    env = node.get("nodeEnv") or {}
    out.append(check("node.env", "node.env is root-owned mode 0600", env.get("type") == "file" and mode_ok(env, "0600", 0),
                     "node.env is a root-owned 0600 regular file",
                     f"node.env type={env.get('type')} uid={env.get('uid')} mode={env.get('mode')}" if env.get("exists") else "node.env is missing"))
    keys = set(env.get("keys") or [])
    placeholders = set(env.get("placeholderKeys") or [])
    values = env.get("values") or {}
    absent = sorted(set(PRODUCTION_SECRETS) - keys)
    unset = sorted(set(PRODUCTION_SECRETS) & placeholders)
    out.append(check("node.secrets", "every production secret is present and not a placeholder", not absent and not unset,
                     f"{len(PRODUCTION_SECRETS)} production secrets are set (values not read)",
                     f"missing: {absent[:4]}; placeholder: {unset[:4]}"))
    libs = {name: [x for x in values.get(name, "").split(",") if x.strip()] for name in ("JELLYFIN_LIBRARY_IDS", "GRIMMORY_LIBRARY_IDS")}
    out.append(check("node.libraries", "stable Jellyfin and Grimmory library IDs are configured",
                     values.get("TELOS_ENV") == "production" and all(libs.values()),
                     f"{len(libs['JELLYFIN_LIBRARY_IDS'])} Jellyfin and {len(libs['GRIMMORY_LIBRARY_IDS'])} Grimmory library IDs; TELOS_ENV=production",
                     f"TELOS_ENV={values.get('TELOS_ENV')!r}; library counts {({k: len(v) for k, v in libs.items()})}"))
    config = node.get("config") or {}
    files = {f["path"].rsplit("/", 1)[-1]: f for f in config.get("files") or []}
    signing_ok = (config.get("type") == "dir" and mode_ok(config, "0700", RUNTIME_UID, RUNTIME_GID)
                  and set(files) == set(SIGNING_FILES)
                  and all(f.get("type") == "file" and mode_ok(f, "0600", RUNTIME_UID, RUNTIME_GID) and f.get("size", 0) >= MIN_SIGNING_BYTES
                          for f in files.values()))
    out.append(check("node.signing", "signing files are exactly cursor+HLS, 10001:10001, 0700/0600", signing_ok,
                     "config/ is 10001:10001 0700 holding only the two non-empty 0600 signing files",
                     f"config {config.get('mode')} {config.get('uid')}:{config.get('gid')}; files {sorted(files)}"))
    state = node.get("state") or {}
    out.append(check("node.state", "state/ is root-owned 0700", state.get("type") == "dir" and mode_ok(state, "0700", 0),
                     "state/ is a root-owned 0700 directory",
                     f"state type={state.get('type')} mode={state.get('mode')} uid={state.get('uid')}" if state.get("exists") else "state/ is missing"))
    return out


def evaluate_services(facts):
    out = []
    containers = facts.get("containers")
    if containers is None:
        return [assertion("services.running", "the Telos services are running", "failed", "the container list could not be read (is this a rootful Podman host?)")]
    by_name = {c["name"]: c for c in containers if c.get("telos")}
    stopped = [n for n in LONG_RUNNING if by_name.get(n, {}).get("state") != "running"]
    migrate = by_name.get(ONE_SHOT, {})
    unexpected = sorted(set(by_name) - set(LONG_RUNNING) - {ONE_SHOT})
    out.append(check("services.running", "every long-running service is running and migrations completed",
                     not stopped and migrate.get("state") == "exited" and migrate.get("exitCode") == 0 and not unexpected,
                     f"{len(LONG_RUNNING)} services running; telos-migrate exited 0",
                     f"not running: {stopped[:5]}; migrate {migrate.get('state')}/{migrate.get('exitCode')}; unexpected: {unexpected[:3]}"))
    published, offenders = [], []
    for name, row in by_name.items():
        for port in row.get("ports") or []:
            published.append((name, port["hostPort"], port["hostIp"], port["protocol"]))
            ok = (name == "telos-traefik" and port["hostPort"] in PUBLIC_PORTS and port["protocol"] == "tcp"
                  and port["hostIp"] in WILDCARD_HOSTS)
            if not ok:
                offenders.append(f"{name}:{port['hostIp'] or '*'}:{port['hostPort']}")
    traefik_ports = {p for (n, p, _, _) in published if n == "telos-traefik"}
    out.append(check("ports.published", "only Traefik publishes host ports, and only 80 and 443",
                     not offenders and traefik_ports == PUBLIC_PORTS,
                     "Traefik alone publishes 80 and 443; no other service publishes a host port",
                     f"unexpected host bindings: {offenders[:5]}; traefik ports {sorted(traefik_ports)}"))
    exposed = sorted({l["port"] for l in facts.get("listeners") or []
                      if l["port"] in FORBIDDEN_PUBLIC_PORTS and l["address"] not in ("127.0.0.1", "::1")})
    out.append(check("ports.listeners", "no internal service port listens on a non-loopback address",
                     facts.get("listeners") is not None and not exposed,
                     "no internal service port is reachable on a public interface",
                     f"non-loopback listeners on internal ports: {exposed}" if facts.get("listeners") is not None else "listening sockets could not be read"))
    client = by_name.get("telos-client", {})
    mounted = [m["destination"] for m in client.get("mounts") or [] if m.get("type") in ("bind", "volume")]
    networks = [n for n in client.get("networks") or []]
    isolated = bool(networks) and all(n.endswith("telos-ingress") for n in networks)
    out.append(check("client.isolation", "telos-client is unprivileged, ingress-only and mounts no host data",
                     bool(client) and isolated and not mounted and client.get("user") in CLIENT_USER and not client.get("ports"),
                     "telos-client runs as 101, is attached only to telos-ingress and has no bind or volume mounts",
                     f"client networks {networks}, user {client.get('user')!r}, bind/volume mounts {mounted[:3]}" if client else "telos-client is absent"))
    core = by_name.get("telos-core", {})
    out.append(check("core.identity", "telos-core runs as 10001:10001", core.get("user") == f"{RUNTIME_UID}:{RUNTIME_GID}",
                     "telos-core runs as 10001:10001", f"telos-core user is {core.get('user')!r}"))
    return out


def evaluate_provenance(facts):
    env = (facts.get("node") or {}).get("nodeEnv") or {}
    birth = env.get("birth")
    if not birth:
        return [not_run("clean.provenance", "no Telos state predates initialization",
                        "the filesystem reports no birth time for node.env, so pre-existing volumes cannot be ruled out")]
    volumes = facts.get("volumes")
    if volumes is None:
        return [assertion("clean.provenance", "no Telos state predates initialization", "failed", "volumes could not be listed")]
    older = []
    for volume in volumes:
        created = parse_time(volume.get("created"))
        if volume["name"] and volume["name"].startswith("telos") and (created is None or created < birth - SKEW_SECONDS):
            older.append(volume["name"])
    for row in facts.get("containers") or []:
        created = parse_time(row.get("created"))
        if row.get("telos") and (created is None or created < birth - SKEW_SECONDS):
            older.append(row["name"])
    return [check("clean.provenance", "no Telos state predates initialization", not older,
                  "every Telos volume and container was created after node.env", f"created before initialization (or undated): {sorted(older)[:5]}")]


def evaluate_setup_watch(facts):
    watch = (facts.get("node") or {}).get("setupWatch")
    description = "guided setup bound Jellyfin and Grimmory to loopback only"
    if watch is None:
        return [not_run("setup.loopback", description, "no setup watch was recorded; run the watch-setup step while `telos configure integrations --setup` runs")]
    if not isinstance(watch, dict) or watch.get("invalid") or not isinstance(watch.get("bindings"), list):
        return [assertion("setup.loopback", description, "failed", "the recorded setup watch is malformed")]
    bindings = watch["bindings"]
    seen = {b["container"] for b in bindings}
    if not {"telos-jellyfin", "telos-grimmory"} <= seen:
        return [not_run("setup.loopback", description, f"the watch observed bindings only for {sorted(seen)}; setup did not run inside the window")]
    bad = [f"{b['container']}:{b['hostIp'] or '*'}:{b['hostPort']}" for b in bindings if b["hostIp"] != "127.0.0.1"]
    return [check("setup.loopback", description, not bad, f"{len(bindings)} setup bindings, all on 127.0.0.1",
                  f"non-loopback setup bindings: {bad[:4]}")]


def evaluate_public(tls, redirect, origin):
    out = []
    ok = tls.get("verified") is True and tls.get("daysRemaining", 0) >= CERT_MIN_DAYS and not tls.get("selfSigned")
    out.append(check("tls.chain", "the public certificate verifies against the platform trust store",
                     ok, f"verified for {tls.get('hostname')}, issuer {tls.get('issuer')}, {tls.get('daysRemaining')} days remain",
                     f"TLS did not verify: {tls.get('error') or 'certificate expires in %s days' % tls.get('daysRemaining')}"))
    if redirect.get("skipped"):
        out.append(not_run("public.http-redirect", "port 80 answers and redirects to HTTPS", redirect["skipped"]))
    else:
        out.append(check("public.http-redirect", "port 80 answers and redirects to HTTPS", redirect.get("ok") is True,
                         f"http://{urllib.parse.urlsplit(origin).hostname}/ redirects to {redirect.get('location')}",
                         f"port 80 check failed: {redirect.get('error') or redirect.get('status')}"))
    return out


def worst(assertions):
    statuses = {a["status"] for a in assertions}
    return "failed" if "failed" in statuses else "not_run" if "not_run" in statuses else "passed"


# ------------------------------------------------------------------ verifier side

def probe_tls(origin):
    parsed = urllib.parse.urlsplit(origin)
    host, port = parsed.hostname, parsed.port or 443
    context = ssl.create_default_context()
    try:
        with socket.create_connection((host, port), timeout=15) as raw, context.wrap_socket(raw, server_hostname=host) as tls:
            cert = tls.getpeercert()
            protocol = tls.version()
    except ssl.SSLCertVerificationError as error:
        return {"verified": False, "hostname": host, "error": (error.verify_message or "certificate verification failed")[:120]}
    except (OSError, ssl.SSLError) as error:
        return {"verified": False, "hostname": host, "error": error.__class__.__name__}
    flat = lambda rdns: {k: v for rdn in rdns for k, v in rdn}  # noqa: E731
    subject, issuer = flat(cert.get("subject", ())), flat(cert.get("issuer", ()))
    expires = ssl.cert_time_to_seconds(cert["notAfter"])
    return {
        "verified": True, "hostname": host, "protocol": protocol,
        "issuer": issuer.get("organizationName") or issuer.get("commonName") or "unknown",
        "selfSigned": cert.get("subject") == cert.get("issuer"),
        "notAfter": datetime.datetime.fromtimestamp(expires, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "daysRemaining": int((expires - datetime.datetime.now(datetime.timezone.utc).timestamp()) // 86400),
        "san": sorted(v for k, v in cert.get("subjectAltName", ()) if k == "DNS")[:10],
        "commonName": subject.get("commonName"),
    }


def probe_http_redirect(origin):
    import http.client
    parsed = urllib.parse.urlsplit(origin)
    if parsed.port not in (None, 443):
        return {"skipped": "the origin uses a non-default HTTPS port, so the public port-80 redirect cannot be probed"}
    try:
        connection = http.client.HTTPConnection(parsed.hostname, 80, timeout=15)
        connection.request("GET", "/", headers={"Host": parsed.hostname})
        response = connection.getresponse()
        location = response.getheader("Location") or ""
        connection.close()
    except OSError as error:
        return {"ok": False, "error": error.__class__.__name__}
    return {"ok": response.status in (301, 302, 307, 308) and location.startswith(f"https://{parsed.hostname}"),
            "status": response.status, "location": location[:200]}


def run_smoke(args, mode, commit, lock_digest, target, scratch, manifest, host_descriptor):
    credentials = Path(target["credentialsFile"]).read_bytes()
    bootstrap = b'"bootstrapToken"' in credentials
    host_path = scratch / "host-identity.json"
    host_path.write_bytes(rg.canonical_bytes(host_descriptor))
    out = scratch / "smoke"
    out.mkdir(mode=0o700)
    argv = ["bash", str(SMOKE), "--public-url", target["origin"], "--result-out", str(out / "result.json"),
            "--fixture-manifest", manifest, "--host-identity", str(host_path)]
    if mode == "rehearsal":
        argv += ["--rehearsal", "--source-commit", commit]
        if args.rehearsal_lock:
            argv += ["--rehearsal-lock", args.rehearsal_lock]
    else:
        argv += ["--candidate-lock", args.candidate_lock]
    if bootstrap:
        argv.append("--bootstrap-empty-node")
    env = {k: v for k, v in os.environ.items() if k in ("PATH", "HOME", "SSL_CERT_FILE", "SSL_CERT_DIR", "TMPDIR", "LANG")}
    completed = subprocess.run(argv, input=credentials, capture_output=True, env=env, timeout=900, check=False)
    report_path = out / "report.json"
    report = rg.parse_json(report_path.read_bytes()) if report_path.is_file() else None
    return completed.returncode, report, completed.stdout.decode("utf-8", "replace").strip()[-300:]


def podman_version(facts):
    return ((facts.get("tools") or {}).get("podman") or {}).get("version") or "unavailable"


def host_descriptor_for(facts):
    host = facts["host"]
    return [{"hostId": host["machineIdSha256"], "identityMethod": "machine-id-sha256", "os": host.get("osId") or "unknown",
             "osVersion": host.get("osVersion") or "unknown", "arch": host.get("arch") or "unknown"}]


def run(argv, remote_factory=dc.Remote, tls_probe=probe_tls, redirect_probe=probe_http_redirect,
        verifier_identity=dc.local_machine_id, smoke=run_smoke):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target-file", required=True)
    parser.add_argument("--fixture-manifest", required=True)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    out_dir = Path(args.result_out).resolve().parent
    if not out_dir.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    remote = remote_factory(target)
    dc.verify_node_identity(remote, target)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the verifier is the node itself, so reachability from outside the node cannot be demonstrated; run the drill from another machine over SSH")

    facts = remote.collect("install", target["nodePath"])
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the drill")
    tls, redirect = tls_probe(target["origin"]), redirect_probe(target["origin"])
    scratch = Path(tempfile.mkdtemp(prefix="telos-clean-install."))
    os.chmod(scratch, 0o700)
    try:
        code, report, summary = smoke(args, mode, commit, lock_digest, target, scratch, args.fixture_manifest, host_descriptor_for(facts))
        assertions = (evaluate_platform(facts) + evaluate_node(facts, target) + evaluate_services(facts)
                      + evaluate_provenance(facts) + evaluate_setup_watch(facts) + evaluate_public(tls, redirect, target["origin"]))
        if report is None:
            assertions.append(assertion("journey.smoke", "the member journey over HTTPS", "failed", f"the smoke probe produced no report (exit {code}): {summary}"))
        else:
            rows = report["assertions"]
            bad = [r["id"] for r in rows if r["status"] == "failed"]
            skipped = [r["id"] for r in rows if r["status"] == "not_run"]
            assertions.append(assertion("journey.smoke", "the member journey over HTTPS", "failed" if bad else "not_run" if skipped else "passed",
                                        f"{len(rows)} journey assertions" + (f"; failed {bad[:5]}" if bad else "") + (f"; not run {skipped[:5]}" if skipped else "")))
        status = worst(assertions)
        reason = {
            "passed": f"{len(assertions)} install and journey assertions passed",
            "failed": "failed: " + ", ".join(a["id"] for a in assertions if a["status"] == "failed")[:240],
            "not_run": "not run: " + ", ".join(a["id"] for a in assertions if a["status"] == "not_run")[:240],
        }[status]
        observation = {"schemaVersion": 1, "origin": target["origin"], "facts": facts, "tls": tls, "httpRedirect": redirect}
        evaluation = {"schemaVersion": 1, "assertions": assertions}
        attachments = [dc.write_json(out_dir, "observation.json", observation) | {"kind": "report"},
                       dc.write_json(out_dir, "assertions.json", evaluation) | {"kind": "report"}]
        if report is not None:
            attachments.append(dc.write_json(out_dir, "journey-report.json", report) | {"kind": "report"})
        record = {
            "schemaVersion": 1, "recordKind": mode, "gateId": os.environ.get("TELOS_GATE_ID", GATE_ID), "sourceCommit": commit,
            "candidateLockDigest": lock_digest, "status": status, "reason": reason, "subjectFile": None,
            "attachments": attachments,
            "toolVersions": {"python3": platform.python_version(), "podman": podman_version(facts)},
        }
        if status == "passed":
            descriptor = {
                "schemaVersion": 1, "kind": "external-host", "sourceCommit": commit, "candidateLockDigest": lock_digest,
                "observedAt": facts["observedAt"], "purpose": record["gateId"], "hosts": host_descriptor_for(facts),
                "records": sorted(({"path": a["path"], "sha256": a["sha256"]} for a in attachments), key=lambda r: r["path"]),
            }
            attachments.append(dc.write_json(out_dir, "subject.json", descriptor) | {"kind": "subject"})
            record["subjectFile"] = "subject.json"
        attachments.sort(key=lambda item: item["path"])
        rg.reject_secret_text(json.dumps(record) + json.dumps(observation) + json.dumps(evaluation), "drill output")
        rg.validate_record("gate-result", record)
        rg.write_atomic_exclusive(args.result_out, rg.canonical_bytes(record))
        print(f"clean-node-install: {status}: {reason}")
        return {"passed": dc.EXIT_PASSED, "failed": dc.EXIT_FAILED, "not_run": dc.EXIT_NOT_RUN}[status]
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"clean-node-install: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"clean-node-install: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"clean-node-install: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
