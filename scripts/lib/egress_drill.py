#!/usr/bin/env python3
"""Controlled-egress drill: prove, from inside each service's own network namespace, that the
node's only way to the Internet for ClamAV, Jellyfin and Grimmory is the deny-by-default proxy.

Run from a machine that is not the node:

    egress_drill.py --target-file FILE (--rehearsal --source-commit SHA | --candidate-lock FILE) --result-out FILE

A fixed program (built from code in this file) runs on the node. For each service it starts a *locked*
curl image sharing that service's network namespace (``--network container:NAME``, no capabilities,
read-only, no volumes) and records only HTTP status codes and exit codes:

  * every internal-only service (ClamAV, Jellyfin, Grimmory, PostgreSQL, Redis, MariaDB, Prometheus):
    a direct request to an allowlisted hostname and to an IP literal must FAIL;
  * the three proxied services: an allowlisted HTTPS destination through the proxy must succeed (unless
    the node itself has no Internet, which is ``not_run``, not a pass), and an unlisted domain, an IP
    literal, a private address, the link-local metadata address, an alternate port and a plaintext
    unlisted request must all be refused;
  * a request carrying a random query token goes through the proxy; the proxy's access log must show the
    request and must NOT contain the token;
  * every result is bound to the container, image and network identities that were running.

A missing engine, probe image or Internet is ``not_run``. Exit codes: 0 passed, 1 failed, 2 unsafe input, 3 not_run.
"""

import argparse
import json
import os
import platform
import secrets
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402
import remote_program as rp  # noqa: E402

GATE_ID = "controlled-egress"
ROOT = Path(__file__).resolve().parent.parent.parent
PROXY = "telos-egress-proxy"
PROXIED = ("telos-clamav", "telos-jellyfin", "telos-grimmory")
INTERNAL_ONLY = PROXIED + ("telos-postgres", "telos-redis", "telos-grimmory-db", "telos-prometheus")
ALLOWED_URL = "https://openlibrary.org/"
DENIED = (
    ("unlisted-domain", "https://example.com/"),
    ("ip-literal", "https://93.184.216.34/"),
    ("private-address", "https://10.0.0.1/"),
    ("link-local-metadata", "https://169.254.169.254/latest/meta-data/"),
    ("alternate-port", "https://openlibrary.org:8443/"),
    ("plaintext-unlisted", "http://example.com/"),
)
DIRECT = (("direct-hostname", "https://openlibrary.org/"), ("direct-ip", "https://1.1.1.1/"))
SUCCESS = {"200", "301", "302", "304"}
REFUSED = {"000", "403", "407"}
assertion, check, not_run = cid.assertion, cid.check, cid.not_run

REMOTE = r'''
import json, subprocess

CFG = json.loads(CFG_JSON)


def podman(*args, timeout=90):
    try:
        return subprocess.run(["podman", *args], capture_output=True, text=True, timeout=timeout, check=False)
    except (OSError, subprocess.SubprocessError):
        return None


def probe(namespace, *curl):
    result = podman("run", "--rm", "--pull=never", "--network", "container:" + namespace, "--cap-drop=ALL",
                    "--security-opt=no-new-privileges", "--read-only", "--memory=64m", "--pids-limit=32", CFG["image"],
                    "curl", "-sS", "--max-time", "8", "-o", "/dev/null", "-w", "%{http_code}", *curl)
    if result is None:
        return {"httpCode": None, "exit": None}
    code = result.stdout.strip()[-3:]
    return {"httpCode": code if code.isdigit() else "000", "exit": result.returncode}


def running(name):
    result = podman("inspect", "--type", "container", name)
    if result is None or result.returncode != 0:
        return None
    try:
        item = json.loads(result.stdout)[0]
    except (ValueError, IndexError):
        return None
    if not item.get("State", {}).get("Running"):
        return None
    return {"id": item.get("Id", "")[:64], "imageId": item.get("Image", ""), "networks": sorted((item.get("NetworkSettings") or {}).get("Networks", {}))}


out = {"engine": podman("version") is not None and podman("version").returncode == 0}
if not out["engine"]:
    print(json.dumps(out))
    raise SystemExit(0)
pulled = podman("pull", "--quiet", CFG["image"], timeout=240)
inspected = podman("image", "inspect", CFG["image"], "--format", "{{.Id}}") if pulled is not None and pulled.returncode == 0 else None
out["probeImage"] = {"reference": CFG["image"], "id": inspected.stdout.strip() if inspected is not None and inspected.returncode == 0 else None}
if out["probeImage"]["id"] is None:
    print(json.dumps(out))
    raise SystemExit(0)

out["identities"] = {name: running(name) for name in CFG["internalOnly"] + [CFG["proxy"]]}
networks = {}
for name in ("telos-backend", "telos-db", "telos-observability", "telos-egress", "telos-ingress"):
    result = podman("network", "inspect", name)
    try:
        document = json.loads(result.stdout)[0] if result is not None and result.returncode == 0 else None
    except (ValueError, IndexError):
        document = None
    networks[name] = {"id": document.get("id"), "internal": bool(document.get("internal"))} if document else None
out["networks"] = networks

# Does the node itself reach the Internet? Without that the allowed path cannot be shown.
if out["identities"].get(CFG["proxy"]):
    out["control"] = probe(CFG["proxy"], CFG["allowedUrl"])
else:
    out["control"] = {"httpCode": None, "exit": None}

probes = []
for name in CFG["internalOnly"]:
    if not out["identities"].get(name):
        continue
    for label, url in CFG["direct"]:
        probes.append(dict(service=name, probe=label, **probe(name, "--noproxy", "*", url)))
    if name in CFG["proxied"]:
        proxy = "http://" + CFG["proxy"] + ":3128"
        probes.append(dict(service=name, probe="allowed", **probe(name, "-x", proxy, CFG["allowedUrl"])))
        for label, url in CFG["denied"]:
            probes.append(dict(service=name, probe=label, **probe(name, "-x", proxy, url)))
out["probes"] = probes

# Query redaction: send a token through the proxy, then look for it (never print the log).
redaction = {"requested": False, "logged": False, "tokenLeaked": None}
first = next((n for n in CFG["proxied"] if out["identities"].get(n)), None)
if first and out["identities"].get(CFG["proxy"]):
    sent = probe(first, "-x", "http://" + CFG["proxy"] + ":3128", "http://openlibrary.org/?telos_probe=" + CFG["token"])
    redaction["requested"] = sent["httpCode"] is not None
    log = podman("exec", CFG["proxy"], "cat", "/var/log/squid/access.log")
    if log is not None and log.returncode == 0:
        redaction["logged"] = "openlibrary.org" in log.stdout
        redaction["tokenLeaked"] = CFG["token"] in log.stdout
out["redaction"] = redaction
print(json.dumps(out))
'''


# ------------------------------------------------------------------ evaluation

def probe_map(result):
    return {(p["service"], p["probe"]): p for p in result.get("probes") or []}


def evaluate(result, lock_reference):
    if not result.get("engine"):
        return [not_run("egress.engine", "a container engine is available to probe from", "the node reports no usable container engine")]
    probe_image = result.get("probeImage") or {}
    if not probe_image.get("id"):
        return [not_run("egress.probe-image", "the locked probe image is available", "the node could not obtain the locked probe image")]
    out = [check("egress.probe-image", "the probe is the locked image", probe_image.get("reference") == lock_reference,
                 f"probes ran from {lock_reference.split('@')[1][:19]}", f"the node ran {probe_image.get('reference')}, not the locked {lock_reference}")]
    identities = result.get("identities") or {}
    missing = [name for name in INTERNAL_ONLY + (PROXY,) if not identities.get(name)]
    networks = result.get("networks") or {}
    internal_ok = all((networks.get(n) or {}).get("internal") is True for n in ("telos-backend", "telos-db", "telos-observability"))
    egress_ok = (networks.get("telos-egress") or {}).get("internal") is False
    out.append(check("egress.identity", "every probed service and the proxy were running, and the networks are as designed",
                     not missing and internal_ok and egress_ok,
                     f"{len(INTERNAL_ONLY) + 1} containers and the network identities are bound to this run",
                     f"not running: {missing}; internal networks internal={internal_ok}; egress routable={egress_ok}"))
    probes = probe_map(result)
    direct_leaks = []
    for name in INTERNAL_ONLY:
        if not identities.get(name):
            continue
        for label, _ in DIRECT:
            row = probes.get((name, label))
            if row is None or row["httpCode"] is None or row["httpCode"] != "000" or row["exit"] == 0:
                direct_leaks.append(f"{name}:{label}={row and row['httpCode']}")
    out.append(check("egress.direct", "no internal-only service can reach the Internet directly (hostname or IP)", not direct_leaks and not missing,
                     f"{len(INTERNAL_ONLY)} services: direct HTTPS to a hostname and to an IP literal both fail", f"direct paths worked or were not probed: {direct_leaks[:6]}"))
    control = (result.get("control") or {}).get("httpCode")
    if control not in SUCCESS:
        out.append(not_run("egress.allowed", "an allowlisted HTTPS destination works through the proxy", f"the node's own proxy namespace could not reach {ALLOWED_URL} (HTTP {control}); with no Internet the allowed path cannot be shown"))
    else:
        failed = [n for n in PROXIED if (probes.get((n, "allowed")) or {}).get("httpCode") not in SUCCESS]
        out.append(check("egress.allowed", "an allowlisted HTTPS destination works through the proxy from each proxied service", not failed,
                         f"{len(PROXIED)} proxied services reached {ALLOWED_URL} through the proxy", f"through the proxy these could not reach an allowlisted host: {failed}"))
    breaches = []
    for name in PROXIED:
        for label, _ in DENIED:
            row = probes.get((name, label))
            if row is None or row["httpCode"] not in REFUSED:
                breaches.append(f"{name}:{label}={row and row['httpCode']}")
    out.append(check("egress.denied", "the proxy refuses an unlisted domain, an IP literal, private and link-local addresses, an alternate port and plaintext to an unlisted host",
                     not breaches, f"{len(PROXIED) * len(DENIED)} denied requests were refused", f"not refused: {breaches[:6]}"))
    redaction = result.get("redaction") or {}
    if not redaction.get("requested") or redaction.get("tokenLeaked") is None:
        out.append(not_run("egress.redaction", "the proxy's access log redacts query strings", "the proxy log could not be read"))
    else:
        out.append(check("egress.redaction", "the proxy's access log shows the request but never its query string",
                         redaction["logged"] and not redaction["tokenLeaked"], "the request was logged without its query token",
                         "the request was not logged" if not redaction["logged"] else "a query token reached the access log"))
    return out


def locked_reference():
    lock = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
    entry = lock["toolImages"]["egress-probe"]
    return f"{entry['source']}@{entry['manifestDigest']}"


def remote_program(reference, token):
    config = {"image": reference, "proxy": PROXY, "proxied": list(PROXIED), "internalOnly": list(INTERNAL_ONLY), "allowedUrl": ALLOWED_URL,
              "denied": [list(p) for p in DENIED], "direct": [list(p) for p in DIRECT], "token": token}
    return ("CFG_JSON = " + repr(json.dumps(config)) + "\n" + REMOTE).encode("utf-8")


# ------------------------------------------------------------------ command

def run(argv, remote_factory=dc.Remote, runner=rp.run_program, verifier_identity=dc.local_machine_id):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--target-file", required=True)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    if not Path(args.result_out).resolve().parent.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the verifier is the node itself; run the drill from another machine over SSH")
    reference = locked_reference()
    token = "tok" + secrets.token_hex(12)
    result = runner(args.target_file, remote_program(reference, token), timeout=600)
    remote = remote_factory(target)
    facts = remote.collect("install", target["nodePath"])
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the drill")
    assertions = evaluate(result, reference)
    text = json.dumps(result)
    if token in text:   # defence in depth: the program never returns the log, but nothing may carry the token onward
        raise dc.DrillError("the probe token appeared in the node program's output")
    observation = {"schemaVersion": 1, "origin": target["origin"], "probeImage": reference, "result": result}
    return dc.finalize(args, mode, commit, lock_digest, GATE_ID, assertions, observation, cid.host_descriptor_for(facts),
                       {"python3": platform.python_version(), "podman": cid.podman_version(facts)}, facts["observedAt"], label="egress-drill",
                       runtime_facts=facts)


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"egress-drill: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"egress-drill: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"egress-drill: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
