#!/usr/bin/env python3
"""The separate-host recovery drill: judge a node that was restored from an off-node snapshot.

Run from a *third* machine -- neither the host that made the backup nor the host that was
restored. It
  1. loads a protected drill target describing the RECOVERED host and proves the machine
     answering is that host (and not the verifier itself);
  2. collects, with the fixed read-only collector, the restore journal and result, the
     sanitizer's audit row, the credentials still live in the database, and whether each
     stale credential the operator captured before the backup is in the restored database
     and dead;
  3. checks DNS cutover (the origin resolves to an address the recovered host owns) and public
     TLS against the platform trust store;
  4. probes the old cookie, device tokens and invite over HTTPS (all must be refused) and signs
     in with the owner's password (it must still work);
  5. runs the real member journey (Chat, Stream, Library, Files, commentary, range media)
     against restored content with scripts/smoke-browser-node.sh;
  6. computes RPO = incidentDeclaredAt - consistencyPointAt and RTO = journey passed -
     incidentDeclaredAt and judges them against 24 h and 4 h.

It changes nothing on either host except the member account and invite the journey creates.
The restore itself is the operator's job (scripts/restore.sh); this drill observes and judges.
The credentials file carries, besides the journey's owner/member, the extras that the gate
runner cannot pass as options:  "fixtureManifestFile" (absolute path) and "stale" ({
"capturedAt", "cookies", "inviteToken", "deviceAccessToken", "deviceRefreshToken"}).
A fixture drill (restore-drill.sh --fixture) can never produce a pass: only this module can.
Exit codes: 0 passed, 1 failed, 2 unsafe or malformed input, 3 not_run.
"""

import argparse
import hashlib
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
import time
import urllib.parse
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import browser_node_smoke as smoke_client  # noqa: E402
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402

GATE_ID = "separate-host-restore"
RPO_MAX_SECONDS = 86400
RTO_MAX_SECONDS = 14400
REQUIRED_STEPS = (
    "configuration", "empty-host check", "snapshot retrieval", "verification", "preserve prior state",
    "configuration restore", "volume restore", "start databases", "postgres restore", "mariadb restore",
    "migrations", "sanitize", "start stack", "result",
)
STALE_REJECTED = (401, 403)
INVITE_REJECTED = (400, 401, 403, 404, 410)
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
now_epoch = time.time

assertion, check, not_run, parse_time, worst = cid.assertion, cid.check, cid.not_run, cid.parse_time, cid.worst


def sha256_hex(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


# ------------------------------------------------------------------ credentials file

def load_recovery_credentials(path):
    """Split the credentials file into the journey's credentials and the drill's own extras."""
    document = json.loads(Path(path).read_bytes())
    if not isinstance(document, dict):
        raise dc.DrillError("the credentials file must be a JSON object")
    extras = {key: document.pop(key, None) for key in ("fixtureManifestFile", "stale")}
    if "bootstrapToken" in document:
        raise dc.DrillError("a restored node is already bootstrapped; remove bootstrapToken from the credentials file")
    manifest = extras["fixtureManifestFile"]
    if not isinstance(manifest, str) or not os.path.isabs(manifest) or not os.path.isfile(manifest):
        raise dc.DrillError("the credentials file must name an absolute fixtureManifestFile that exists")
    stale = extras["stale"]
    if not isinstance(stale, dict):
        raise dc.NotRun("no stale credentials were captured before the backup, so rejection of old cookies, devices and invites cannot be shown")
    return document, manifest, stale


def stale_hashes(stale):
    """Collector arguments for every stale credential present; the secrets themselves stay here."""
    arguments = []
    for cookie in (stale.get("cookies") or {}).values():
        arguments.append(f"session={sha256_hex(cookie)}")
    if stale.get("deviceAccessToken"):
        arguments.append(f"session={sha256_hex(stale['deviceAccessToken'])}")
    if stale.get("inviteToken"):
        arguments.append(f"invite={sha256_hex(stale['inviteToken'])}")
    if stale.get("deviceRefreshToken"):
        arguments.append(f"refresh={sha256_hex(stale['deviceRefreshToken'])}")
    return arguments


# ------------------------------------------------------------------ evaluation

def journal_run(journal):
    """The events of the most recent restore attempt (the journal is append-only across attempts)."""
    start = max((i for i, row in enumerate(journal) if row.get("step") == "configuration" and row.get("state") == "begin"), default=None)
    return [] if start is None else journal[start:]


def evaluate_restore_record(restore, target, mode, lock_digest):
    out = []
    result = restore.get("result")
    if not isinstance(result, dict):
        return [assertion("restore.result", "the restore recorded a result", "failed", "state/restore-result.json is missing or malformed on the recovered host")]
    source, recovered = result.get("sourceHostIdSha256"), result.get("targetHostIdSha256")
    out.append(check("restore.result", "the restore recorded a completed separate-host result",
                     result.get("status") == "restored" and result.get("mode") == "separate-host" and result.get("recordKind") == mode
                     and isinstance(result.get("snapshot"), str),
                     f"snapshot {str(result.get('snapshot'))[:8]} restored, {mode} record",
                     f"result status={result.get('status')!r} mode={result.get('mode')!r} kind={result.get('recordKind')!r}; this drill is a {mode}"))
    out.append(check("restore.hosts", "the backup came from a different machine than the one restored to",
                     bool(DIGEST.match(str(source))) and recovered == target["hostId"] and source != recovered,
                     "source and recovered machine identities differ and the recovered one matches the target descriptor",
                     f"source {str(source)[:19]} recovered {str(recovered)[:19]} target {target['hostId'][:19]}"))
    if mode == "candidate":
        out.append(check("restore.candidate", "the snapshot was made under the candidate being certified",
                         result.get("candidateLockDigest") == lock_digest,
                         "the backup's candidate binding equals the candidate lock",
                         "the backup was made under a different candidate than the one under test"))
    journal = journal_run(restore.get("journal") or [])
    done = {}
    for index, row in enumerate(journal):
        if row.get("state") == "done":
            done.setdefault(row.get("step"), index)
    failed = [row.get("step") for row in journal if row.get("state") == "failed" or row.get("step") == "failure-stack-down"]
    missing = [step for step in REQUIRED_STEPS if step not in done]
    ordered = not missing and [done[step] for step in REQUIRED_STEPS] == sorted(done[step] for step in REQUIRED_STEPS)
    out.append(check("restore.journal", "every restore step finished, in order, with no failure",
                     ordered and not failed,
                     f"{len(REQUIRED_STEPS)} steps finished in order; sanitize finished before the stack started",
                     f"missing steps: {missing[:4]}; failed: {failed[:3]}; ordered: {ordered}"))
    return out


def evaluate_sanitization(restore):
    facts = restore.get("sanitization") or {}
    result = restore.get("result") or {}
    if not facts.get("available"):
        return [assertion("sanitize.state", "no credential from the backup is live", "failed", "the restored database could not be queried for live credentials")]
    snapshot = str(result.get("snapshot") or "")
    latest = facts.get("latestSnapshot") or ""
    live = {k: facts[k] for k in ("liveSessions", "liveDevices", "liveRefreshTokens", "usableInvites")}
    return [
        check("sanitize.state", "no session, device, refresh token or invite from the backup is live",
              not any(live.values()), "0 live sessions, devices, refresh tokens and usable invites after restore",
              f"credentials still live after restore: {live}"),
        check("sanitize.audit", "the sanitizer recorded this restore", facts.get("restoreEvents", 0) >= 1 and bool(snapshot) and bool(latest)
              and (snapshot.startswith(latest) or latest.startswith(snapshot)),
              f"restore_events holds the sanitizer's row for snapshot {latest[:8]}",
              f"restore_events latest snapshot {latest[:8]!r} does not match the restored snapshot {snapshot[:8]!r}"),
    ]


def evaluate_stale_known(restore, stale, hashes):
    """Each stale credential must exist in the restored database (it came from the backup) and be dead."""
    rows = restore.get("staleCredentials") or {}
    unknown = [a.split("=")[0] for a in hashes if not (rows.get(a) or {}).get("known")]
    alive = [a.split("=")[0] for a in hashes if (rows.get(a) or {}).get("known") and not rows[a].get("dead")]
    return [check("stale.provenance", "every stale credential came from the backup and was revoked by the restore",
                  bool(hashes) and not unknown and not alive,
                  f"{len(hashes)} stale credentials are in the restored database, all revoked or consumed",
                  f"not found in the restored database: {unknown[:4]}; still usable: {alive[:4]}" if hashes else "no stale credentials were supplied")]


def evaluate_timeline(result, journey_passed_at):
    description = "recovery point and time objectives hold with the timestamps in order"
    result = result or {}
    values = {name: parse_time(result.get(name)) for name in ("consistencyPointAt", "snapshotFinishedAt", "incidentDeclaredAt")}
    if values["incidentDeclaredAt"] is None:
        return [not_run("recovery.objectives", description, "the restore recorded no incidentDeclaredAt, so the recovery clock never started; re-run restore.sh with --incident-declared-at")]
    if values["consistencyPointAt"] is None or values["snapshotFinishedAt"] is None or journey_passed_at is None:
        return [assertion("recovery.objectives", description, "failed", "a timestamp needed for RPO/RTO is missing or unparseable")]
    points = [values["consistencyPointAt"], values["snapshotFinishedAt"], values["incidentDeclaredAt"], journey_passed_at]
    if points != sorted(points):
        return [assertion("recovery.objectives", description, "failed",
                          "timestamps are out of order (need consistency point <= snapshot finished <= incident declared <= journey passed)")]
    rpo, rto = int(points[2] - points[0]), int(points[3] - points[2])
    return [check("recovery.objectives", description, rpo <= RPO_MAX_SECONDS and rto <= RTO_MAX_SECONDS,
                  f"RPO {rpo}s (limit {RPO_MAX_SECONDS}s), RTO {rto}s (limit {RTO_MAX_SECONDS}s)",
                  f"RPO {rpo}s (limit {RPO_MAX_SECONDS}s), RTO {rto}s (limit {RTO_MAX_SECONDS}s)")]


def evaluate_dns(origin, addresses, resolver=socket.getaddrinfo):
    host = urllib.parse.urlsplit(origin).hostname
    try:
        resolved = sorted({row[4][0] for row in resolver(host, 443, type=socket.SOCK_STREAM)})
    except OSError as error:
        return assertion("dns.cutover", "the origin resolves to the recovered host", "failed", f"{host} did not resolve ({error.__class__.__name__})")
    return check("dns.cutover", "the origin resolves to the recovered host", bool(set(resolved) & set(addresses or [])),
                 f"{host} resolves to {resolved[:3]}, an address the recovered host owns",
                 f"{host} resolves to {resolved[:3]}, which is not one of the recovered host's addresses {sorted(addresses or [])[:3]}")


# ------------------------------------------------------------------ HTTPS probes

def probe_credentials(origin, owner, stale, context):
    """Password still works; every old credential is refused. Returns assertions."""
    out = []

    def fresh():
        directory = tempfile.mkdtemp(prefix="telos-recovery-probe.")
        os.chmod(directory, 0o700)
        return smoke_client.Session(origin, context, os.path.join(directory, "jar.json")), directory

    session, directory = fresh()
    try:
        try:
            status, _, _ = session.request("POST", "/api/v1/auth/login", json_body={"username": owner["username"], "password": owner["password"]})
            me = session.request("GET", "/api/v1/auth/me")
            body = json.loads(me[2] or b"{}") if me[0] == 200 else {}
            same = (body.get("Username") or body.get("username") or "").lower() == owner["username"].lower()
            out.append(check("auth.password", "the owner's password still signs in on the restored node", status == 200 and me[0] == 200 and same,
                             "the owner signed in with the original password and holds a session",
                             f"login returned {status}, /auth/me returned {me[0]}"))
        except (smoke_client.Assertion, ValueError) as error:
            out.append(assertion("auth.password", "the owner's password still signs in on the restored node", "failed", str(error)))
    finally:
        shutil.rmtree(directory, ignore_errors=True)

    cookies = stale.get("cookies") or {}
    if cookies:
        session, directory = fresh()
        try:
            session.cookies = dict(cookies)
            status = session.request("GET", "/api/v1/auth/me")[0]
            out.append(check("stale.cookie", "a browser session captured before the backup is refused", status in STALE_REJECTED,
                             f"the old session cookie was refused ({status})", f"the old session cookie was answered {status}, expected {STALE_REJECTED}"))
        except smoke_client.Assertion as error:
            out.append(assertion("stale.cookie", "a browser session captured before the backup is refused", "failed", str(error)))
        finally:
            shutil.rmtree(directory, ignore_errors=True)
    else:
        out.append(not_run("stale.cookie", "a browser session captured before the backup is refused", "no stale session cookie was supplied"))

    if stale.get("deviceAccessToken") and stale.get("deviceRefreshToken"):
        session, directory = fresh()
        try:
            access = session.request("GET", "/api/v1/auth/me", headers={"Authorization": f"Bearer {stale['deviceAccessToken']}"})[0]
            refresh = session.request("POST", "/api/v1/auth/devices/refresh", json_body={"refreshToken": stale["deviceRefreshToken"]})[0]
            out.append(check("stale.device", "a device captured before the backup is refused (access and refresh)",
                             access in STALE_REJECTED and refresh in STALE_REJECTED,
                             f"the old device access token ({access}) and refresh token ({refresh}) were refused",
                             f"device access answered {access}, refresh answered {refresh}; expected {STALE_REJECTED}"))
        except smoke_client.Assertion as error:
            out.append(assertion("stale.device", "a device captured before the backup is refused (access and refresh)", "failed", str(error)))
        finally:
            shutil.rmtree(directory, ignore_errors=True)
    else:
        out.append(not_run("stale.device", "a device captured before the backup is refused (access and refresh)", "no stale device credential was supplied"))

    if stale.get("inviteToken"):
        session, directory = fresh()
        try:
            name = f"stale-invite-{int(now_epoch())}"
            status = session.request("POST", "/api/v1/auth/invites/accept",
                                     json_body={"token": stale["inviteToken"], "username": name, "password": "Stale-Invite-Probe-" + name})[0]
            out.append(check("stale.invite", "an invite issued before the backup is refused", status in INVITE_REJECTED,
                             f"the old invite was refused ({status})",
                             f"the old invite answered {status}; an account may have been created"))
        except smoke_client.Assertion as error:
            out.append(assertion("stale.invite", "an invite issued before the backup is refused", "failed", str(error)))
        finally:
            shutil.rmtree(directory, ignore_errors=True)
    else:
        out.append(not_run("stale.invite", "an invite issued before the backup is refused", "no stale invite was supplied"))
    return out


# ------------------------------------------------------------------ run

def run(argv, remote_factory=dc.Remote, tls_probe=cid.probe_tls, redirect_probe=cid.probe_http_redirect,
        verifier_identity=dc.local_machine_id, smoke=cid.run_smoke, probes=probe_credentials, dns=evaluate_dns, clock=now_epoch):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target-file", required=True)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    args.fixture_manifest = None
    out_dir = Path(args.result_out).resolve().parent
    if not out_dir.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    credentials, manifest, stale = load_recovery_credentials(target["credentialsFile"])
    remote = remote_factory(target)
    dc.verify_node_identity(remote, target)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the verifier is the recovered host itself, so reachability from outside cannot be demonstrated; run the drill from a third machine over SSH")

    hashes = stale_hashes(stale)
    facts = remote.collect("restore", target["nodePath"], *hashes)
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the recovered host changed identity during the drill")
    restore = facts["restore"]
    result = restore.get("result") or {}
    if verifier_identity() == result.get("sourceHostIdSha256"):
        raise dc.NotRun("the verifier is the machine that made the backup; run the drill from a third machine")

    tls, redirect = tls_probe(target["origin"]), redirect_probe(target["origin"])
    scratch = Path(tempfile.mkdtemp(prefix="telos-recovery."))
    os.chmod(scratch, 0o700)
    try:
        probe_assertions = probes(target["origin"], credentials["owner"], stale, ssl.create_default_context())
        smoke_target = dict(target)
        creds_path = scratch / "journey-credentials.json"
        descriptor = os.open(creds_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as handle:
            json.dump(credentials, handle)
        smoke_target["credentialsFile"] = str(creds_path)
        code, report, summary = smoke(args, mode, commit, lock_digest, smoke_target, scratch, manifest, cid.host_descriptor_for(facts))
        journey_passed_at = clock() if report is not None and code == 0 else None

        assertions = (cid.evaluate_platform(facts) + cid.evaluate_node(facts, target) + cid.evaluate_services(facts)
                      + evaluate_restore_record(restore, target, mode, lock_digest) + evaluate_sanitization(restore)
                      + evaluate_stale_known(restore, stale, hashes) + probe_assertions
                      + [dns(target["origin"], restore.get("addresses"))] + cid.evaluate_public(tls, redirect, target["origin"]))
        if report is None:
            assertions.append(assertion("journey.smoke", "the member journey over HTTPS on restored content", "failed", f"the smoke probe produced no report (exit {code}): {summary}"))
        else:
            rows = report["assertions"]
            bad = [r["id"] for r in rows if r["status"] == "failed"]
            skipped = [r["id"] for r in rows if r["status"] == "not_run"]
            assertions.append(assertion("journey.smoke", "the member journey over HTTPS on restored content",
                                        "failed" if bad else "not_run" if skipped else "passed",
                                        f"{len(rows)} journey assertions" + (f"; failed {bad[:5]}" if bad else "") + (f"; not run {skipped[:5]}" if skipped else "")))
        assertions += evaluate_timeline(result, journey_passed_at)
        status = worst(assertions)
        reason = {
            "passed": f"{len(assertions)} recovery assertions passed",
            "failed": "failed: " + ", ".join(a["id"] for a in assertions if a["status"] == "failed")[:240],
            "not_run": "not run: " + ", ".join(a["id"] for a in assertions if a["status"] == "not_run")[:240],
        }[status]
        sanitized_restore = {k: v for k, v in restore.items() if k != "staleCredentials"}
        sanitized_restore["staleCredentials"] = {k.split("=")[0] + "#" + str(i): v for i, (k, v) in enumerate(sorted((restore.get("staleCredentials") or {}).items()))}
        observation = {"schemaVersion": 1, "origin": target["origin"], "facts": {k: v for k, v in facts.items() if k != "restore"},
                       "restore": sanitized_restore, "tls": tls, "httpRedirect": redirect}
        evaluation = {"schemaVersion": 1, "assertions": assertions}
        attachments = [dc.write_json(out_dir, "observation.json", observation) | {"kind": "report"},
                       dc.write_json(out_dir, "assertions.json", evaluation) | {"kind": "report"}]
        if report is not None:
            attachments.append(dc.write_json(out_dir, "journey-report.json", report) | {"kind": "report"})
        record = {
            "schemaVersion": 1, "recordKind": mode, "gateId": os.environ.get("TELOS_GATE_ID", GATE_ID), "sourceCommit": commit,
            "candidateLockDigest": lock_digest, "status": status, "reason": reason, "subjectFile": None,
            "attachments": attachments,
            "toolVersions": {"python3": platform.python_version(), "podman": cid.podman_version(facts)},
        }
        if status == "passed":
            hosts = cid.host_descriptor_for(facts)   # only the host the drill observed; the source is recorded in observation.json
            subject = {
                "schemaVersion": 1, "kind": "external-host", "sourceCommit": commit, "candidateLockDigest": lock_digest,
                "observedAt": facts["observedAt"], "purpose": record["gateId"], "hosts": hosts,
                "records": sorted(({"path": a["path"], "sha256": a["sha256"]} for a in attachments), key=lambda r: r["path"]),
            }
            attachments.append(dc.write_json(out_dir, "subject.json", subject) | {"kind": "subject"})
            record["subjectFile"] = "subject.json"
        attachments.sort(key=lambda item: item["path"])
        rg.reject_secret_text(json.dumps(record) + json.dumps(observation) + json.dumps(evaluation), "drill output")
        rg.validate_record("gate-result", record)
        rg.write_atomic_exclusive(args.result_out, rg.canonical_bytes(record))
        print(f"recovery-drill: {status}: {reason}")
        return {"passed": dc.EXIT_PASSED, "failed": dc.EXIT_FAILED, "not_run": dc.EXIT_NOT_RUN}[status]
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"recovery-drill: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"recovery-drill: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"recovery-drill: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
