#!/usr/bin/env python3
"""The upgrade/rollback drill: upgrade a real node to the candidate, undo it the way the candidate's schema allows, upgrade again.

Run from a machine that is NOT the node:

    upgrade_drill.py --target-file FILE (--rehearsal --source-commit SHA --rehearsal-lock FILE | --candidate-lock FILE)
                     --result-out FILE

The node must already be serving the PREDECESSOR generation (named by the descriptor's ``predecessorGeneration``) with the
candidate generation installed beside it (scripts/install-candidate.sh), and the descriptor must carry the explicit
authorization ``"confirmation": "UPGRADE-AND-ROLL-BACK-THIS-NODE"``. The drill

  1. proves the machine answering is the one the descriptor names, that it is not the machine running the drill, and that the
     node starts from a settled state (predecessor active and receipted, no unfinished or blocking operation, gateway ok);
  2. leaves a marker (a chat message) as the owner, then runs the CANDIDATE's own upgrade.sh through a fixed node-side program
     (scripts/lib/upgrade_node.py) and judges the journal, snapshot, receipt, active generation, the marker and the member journey;
  3. takes the branch the declared compatibility selects, computed here from the two generation manifests and the schema the
     upgrade left, never from what the scripts report:
       compatible    rollback.sh must swap the binaries back and keep every byte written since (both markers survive),
                     then the candidate is upgraded again;
       incompatible  rollback.sh must REFUSE and change nothing, then restore.sh --mode rollback must bring back the recorded
                     snapshot (the schema returns, the marker left after the upgrade is gone, the marker left before survives, the
                     failed state is kept under telos-rollback/), then the candidate is upgraded again;
  4. ends on the candidate: receipt, running services (the observed-runtime subject), public TLS and a final member journey.

Only the branch the candidate's own compatibility selects can be exercised against a candidate; the other branch is covered by the
hermetic lifecycle suites and, as rehearsal evidence only, by a pair of rehearsal generations. A step that fails stops the
sequence; steps never run are reported ``not_run``. Exit codes: 0 passed, 1 failed, 2 unsafe or malformed input, 3 not_run.
"""

import argparse
import json
import os
import platform
import re
import secrets
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import browser_node_smoke as smoke_client  # noqa: E402
import capacity_drill as cap  # noqa: E402
import clean_install_drill as cid  # noqa: E402
import drill_common as dc  # noqa: E402
import release_graph as rg  # noqa: E402

GATE_ID = "upgrade-rollback"
CONFIRMATION = "UPGRADE-AND-ROLL-BACK-THIS-NODE"
NODE_PROGRAM = Path(__file__).resolve().with_name("upgrade_node.py")
PREDECESSOR_PATH = re.compile(r"(/.+)/generations/([0-9a-f]{64})\Z")
SETTLED = (None, "complete", "aborted", "rolled-back", "restored")
STATE_TIMEOUT = 180
SCRIPT_TIMEOUT = 1800
SNAPSHOT = re.compile(r"[0-9a-f]{64}\Z")
assertion, check, not_run, worst = cid.assertion, cid.check, cid.not_run, cid.worst


def get(document, *keys):
    for key in keys:
        if not isinstance(document, dict):
            return None
        document = document.get(key)
    return document


def names(state, generation):
    """Does the installed-generation receipt name this generation?"""
    return get(state, "receipt", "candidateLockDigest") == "sha256:" + generation


def serving(state, generation):
    return state.get("current") == generation and names(state, generation) and state.get("gateway") == "ok"


# ------------------------------------------------------------------ pure judgement

def expected_branch(predecessor, candidate, post_schema):
    """``rollback`` when the predecessor's binaries accept the schema the candidate left, else ``restore``."""
    low = get(predecessor, "migrations", "min")
    high = candidate.get("rollbackCompatibleThrough")
    if not all(isinstance(value, int) for value in (low, high, post_schema)):
        return None
    return "rollback" if low <= post_schema <= high else "restore"


def evaluate_start(state, predecessor, candidate):
    journal_state = get(state, "journal", "state")
    present = get(state, "generations", candidate, "present")
    return [
        check("start.predecessor", "the node starts serving the predecessor generation (active, receipted)", state.get("current") == predecessor and names(state, predecessor),
              f"generation {predecessor[:12]} is active and receipted", f"active {str(state.get('current'))[:12]}, receipt names {str(get(state, 'receipt', 'candidateLockDigest'))[:19]}"),
        check("start.settled", "no earlier lifecycle operation is unfinished or blocking", journal_state in SETTLED,
              f"the last operation ended {journal_state or 'never started'}", f"the last operation is {journal_state}"),
        check("start.candidate", "the candidate generation is installed and verifiable beside the predecessor", bool(present),
              f"generation {candidate[:12]} is installed", f"generation {candidate[:12]} is not installed on the node"),
        check("start.healthy", "the gateway reports ok and the schema is readable", state.get("gateway") == "ok" and isinstance(state.get("schema"), int),
              f"gateway ok on schema {state.get('schema')}", f"gateway {state.get('gateway')}, schema {state.get('schema')}"),
    ]


def run_ok(run, expected_status):
    result = run.get("result") or {}
    return run.get("exitCode") == 0 and not run.get("timedOut") and result.get("status") == expected_status


def evaluate_upgrade(prefix, before, run, after, predecessor, candidate):
    journal, result = after.get("journal") or {}, run.get("result") or {}
    exit_ok = run_ok(run, "complete")
    facts_ok = (journal.get("kind") == "upgrade" and journal.get("state") == "complete" and journal.get("fromGeneration") == predecessor
                and journal.get("toGeneration") == candidate and journal.get("operationId") == result.get("operationId"))
    snapshot = journal.get("snapshotId")
    schemas_ok = journal.get("preSchema") == before.get("schema") and journal.get("postSchema") == after.get("schema") and isinstance(after.get("schema"), int)
    return [
        check(f"{prefix}.exit", f"{prefix}: upgrade.sh finished and reported complete", exit_ok,
              f"upgrade.sh exited 0 in {run.get('seconds')}s and wrote a complete result",
              f"exit {run.get('exitCode')}, timed out {run.get('timedOut')}, result status {result.get('status')}: {' | '.join((run.get('outputTail') or [])[-2:])}"[:300]),
        check(f"{prefix}.journal", f"{prefix}: the journal records this upgrade from the predecessor to the candidate", facts_ok,
              f"operation {str(journal.get('operationId'))[:8]} complete: {predecessor[:12]} -> {candidate[:12]}",
              f"journal kind {journal.get('kind')} state {journal.get('state')} from {str(journal.get('fromGeneration'))[:12]} to {str(journal.get('toGeneration'))[:12]}"),
        check(f"{prefix}.snapshot", f"{prefix}: an encrypted pre-upgrade snapshot was verified and recorded", isinstance(snapshot, str) and bool(SNAPSHOT.fullmatch(snapshot)),
              f"snapshot {str(snapshot)[:8]} recorded", "the journal records no full snapshot id"),
        check(f"{prefix}.schema", f"{prefix}: the journal's before/after schema match what the node reports", schemas_ok,
              f"schema {journal.get('preSchema')} -> {journal.get('postSchema')}",
              f"journal {journal.get('preSchema')} -> {journal.get('postSchema')}, node before {before.get('schema')}, after {after.get('schema')}"),
        check(f"{prefix}.serving", f"{prefix}: the candidate is active, receipted and healthy", serving(after, candidate),
              f"generation {candidate[:12]} serves", f"active {str(after.get('current'))[:12]}, receipt {str(get(after, 'receipt', 'candidateLockDigest'))[:19]}, gateway {after.get('gateway')}"),
    ]


def evaluate_rollback(before, run, after, predecessor, candidate, operation):
    journal = after.get("journal") or {}
    return [
        check("rollback.exit", "rollback.sh finished and reported complete", run_ok(run, "complete"),
              f"rollback.sh exited 0 in {run.get('seconds')}s", f"exit {run.get('exitCode')}: {' | '.join((run.get('outputTail') or [])[-2:])}"[:300]),
        check("rollback.journal", "the journal records a rollback from the candidate to the predecessor", journal.get("kind") == "rollback" and journal.get("state") == "complete"
              and journal.get("fromGeneration") == candidate and journal.get("toGeneration") == predecessor,
              f"rollback complete: {candidate[:12]} -> {predecessor[:12]}", f"journal kind {journal.get('kind')} state {journal.get('state')}"),
        check("rollback.serving", "the predecessor is active, receipted and healthy again", serving(after, predecessor),
              f"generation {predecessor[:12]} serves", f"active {str(after.get('current'))[:12]}, gateway {after.get('gateway')}"),
        check("rollback.kept", "the upgrade's journal and the candidate generation are kept for investigation",
              f"{operation}.json" in (after.get("operationsKept") or []) and bool(get(after, "generations", candidate, "present")),
              "both are kept", "the upgrade's journal or the candidate generation is gone"),
        check("rollback.schema", "a binary rollback leaves the schema alone", after.get("schema") == before.get("schema"),
              f"schema stays {after.get('schema')}", f"schema {before.get('schema')} became {after.get('schema')}"),
    ]


def evaluate_refusal(before, run, after, candidate):
    unchanged = after.get("journal") == before.get("journal") and after.get("schema") == before.get("schema")
    return [
        check("refusal.exit", "rollback.sh refuses an incompatible schema and names the restore", run.get("exitCode") not in (0, None) and not run.get("timedOut")
              and any("RESTORE-RECORDED-ROLLBACK" in line for line in run.get("outputTail") or []),
              "rollback.sh refused and printed the restore command", f"exit {run.get('exitCode')}: {' | '.join((run.get('outputTail') or [])[-2:])}"[:300]),
        check("refusal.unchanged", "the refusal changed nothing: same journal, same schema, candidate still serving", unchanged and serving(after, candidate),
              "nothing changed", "the journal, the schema or the active generation moved during a refusal"),
    ]


def evaluate_restore(before, run, after, predecessor, candidate, operation, pre_schema):
    journal, result = after.get("journal") or {}, run.get("result") or {}
    new_dirs = sorted(set(after.get("rollbackDirs") or []) - set(before.get("rollbackDirs") or []))
    return [
        check("restore.exit", "restore.sh --mode rollback finished and reported restored", run.get("exitCode") == 0 and not run.get("timedOut") and result.get("status") == "restored"
              and result.get("mode") == "rollback" and result.get("operationId") == operation,
              f"restore.sh exited 0 in {run.get('seconds')}s and restored operation {operation[:8]}", f"exit {run.get('exitCode')}, result {result.get('status')}: {' | '.join((run.get('outputTail') or [])[-2:])}"[:300]),
        check("restore.journal", "the upgrade's journal is closed as restored", journal.get("state") == "restored" and journal.get("operationId") == operation,
              "the upgrade is closed as restored", f"journal state {journal.get('state')}"),
        check("restore.serving", "the predecessor is active, receipted and healthy", serving(after, predecessor),
              f"generation {predecessor[:12]} serves", f"active {str(after.get('current'))[:12]}, gateway {after.get('gateway')}"),
        check("restore.schema", "the schema is back to what the predecessor ran on", after.get("schema") == pre_schema,
              f"schema {after.get('schema')}", f"schema {after.get('schema')}, expected {pre_schema}"),
        check("restore.kept", "the failed state was kept under telos-rollback/", bool(new_dirs),
              f"kept in {new_dirs[0] if new_dirs else ''}", "no new telos-rollback directory appeared"),
    ]


def evaluate_markers(prefix, wanted, seen):
    """wanted: {nonce: should_exist}; seen: {nonce: exists-or-None-when-unreadable}."""
    out = []
    for label, (nonce, should) in wanted.items():
        found = seen.get(nonce)
        description = f"{prefix}: the {label} marker {'survived' if should else 'is gone, as the snapshot predates it'}"
        if found is None:
            out.append(assertion(f"{prefix}.{label}", description, "failed", "the marker could not be read back"))
        else:
            out.append(check(f"{prefix}.{label}", description, found == should, "as expected", "present" if found else "missing"))
    return out


def judge_journey(identifier, description, code, report, summary):
    rows = [row for row in (report or {}).get("assertions", []) if not (row["id"].startswith("outage.") and row["status"] == "not_run")]
    bad = [row["id"] for row in rows if row["status"] == "failed"]
    skipped = [row["id"] for row in rows if row["status"] == "not_run"]
    if report is None or not rows:
        return assertion(identifier, description, "failed", f"the smoke probe produced no report (exit {code}): {summary}"[:300])
    status = "failed" if bad else "not_run" if skipped else "passed"
    return assertion(identifier, description, status, f"{len(rows)} journey assertions" + (f"; failed {bad[:5]}" if bad else "") + (f"; not run {skipped[:5]}" if skipped else ""))


# ------------------------------------------------------------------ owner markers

class Markers:
    """A chat message left as the owner, and a look for it later."""

    def __init__(self, origin, owner, context=None):
        self.origin, self.owner, self.context = origin, owner, context or ssl.create_default_context()

    def _channel(self, session):
        session.json("POST", "/api/v1/auth/login", json_body={"username": self.owner["username"], "password": self.owner["password"]})
        channels = session.json("GET", "/api/v1/channels") or []
        named = [c for c in channels if (c.get("name") or c.get("Name")) == "general"]
        channel = (named or channels)[0]
        return channel.get("id") or channel.get("ID")

    def _session(self):
        directory = tempfile.mkdtemp(prefix="telos-upgrade-marker.")
        os.chmod(directory, 0o700)
        return smoke_client.Session(self.origin, self.context, os.path.join(directory, "jar.json")), directory

    def post(self, nonce):
        session, directory = self._session()
        try:
            channel = self._channel(session)
            session.json("POST", f"/api/v1/channels/{channel}/messages", expected=(200, 201), json_body={"content": f"upgrade-drill marker {nonce}"})
        finally:
            shutil.rmtree(directory, ignore_errors=True)

    def present(self, nonce):
        session, directory = self._session()
        try:
            channel = self._channel(session)
            return f"upgrade-drill marker {nonce}" in json.dumps(session.json("GET", f"/api/v1/channels/{channel}/roots"))
        finally:
            shutil.rmtree(directory, ignore_errors=True)


# ------------------------------------------------------------------ the sequence

def parse_predecessor(target):
    path = target["predecessorGeneration"]
    if path is None:
        raise dc.NotRun("the target descriptor names no predecessorGeneration, so there is nothing to upgrade from")
    match = PREDECESSOR_PATH.fullmatch(path)
    if not match:
        raise dc.DrillError("predecessorGeneration must look like <release base>/generations/<64 hex>")
    return match.group(1), match.group(2)


def run(argv, remote_factory=dc.Remote, verifier_identity=dc.local_machine_id, tls_probe=cid.probe_tls, redirect_probe=cid.probe_http_redirect,
        smoke=cid.run_smoke, markers=None, token=lambda: secrets.token_hex(6), clock=time.time):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--target-file", required=True)
    resolve = dc.parse_mode_arguments(parser)
    args = parser.parse_args(argv)
    mode, commit, lock_digest = resolve(args)
    if lock_digest is None:
        raise dc.DrillError("the drill needs the lock of the generation to upgrade to: --candidate-lock, or --rehearsal-lock with --rehearsal")
    if not Path(args.result_out).resolve().parent.is_dir():
        raise dc.DrillError("the --result-out directory must exist")
    target = dc.load_target(args.target_file)
    if target["confirmation"] is None:
        raise dc.NotRun("the target descriptor carries no confirmation, so the drill is not authorized to change this node")
    if target["confirmation"] != CONFIRMATION:
        raise dc.DrillError(f"the target descriptor's confirmation must be exactly {CONFIRMATION}")
    base, predecessor = parse_predecessor(target)
    candidate = lock_digest.removeprefix("sha256:")
    if candidate == predecessor:
        raise dc.DrillError("the candidate and the predecessor are the same generation")
    node_path = target["nodePath"]
    credentials, manifest = cap.load_credentials(target["credentialsFile"])
    remote = remote_factory(target)
    dc.verify_node_identity(remote, target)
    if verifier_identity() == target["hostId"]:
        raise dc.NotRun("the verifier is the node itself, so the probes would not cross the network; run the drill from another machine")
    program = NODE_PROGRAM.read_bytes()

    def on_node(*arguments, timeout=STATE_TIMEOUT):
        return remote.run_program(program, list(arguments), timeout=timeout, what="upgrade node program")

    def observe():
        return on_node("state", base, node_path, candidate, predecessor)

    facts = remote.collect("install", node_path)
    if facts["host"]["machineIdSha256"] != target["hostId"]:
        raise dc.DrillError("the node changed identity during the drill")
    assertions, trace, states = cid.evaluate_platform(facts), [], {}
    scratch = Path(tempfile.mkdtemp(prefix="telos-upgrade."))
    os.chmod(scratch, 0o700)
    markers = markers or Markers(target["origin"], credentials["owner"])
    stamp = f"{int(clock())}-{secrets.token_hex(2)}"
    branch = None

    def lifecycle(label, *arguments):
        outcome = on_node(*arguments, timeout=SCRIPT_TIMEOUT)
        trace.append({"step": label, "exitCode": outcome.get("exitCode"), "seconds": outcome.get("seconds"), "timedOut": outcome.get("timedOut"),
                      "outputTail": outcome.get("outputTail"), "result": outcome.get("result")})
        return outcome

    def seen(nonces):
        found = {}
        for nonce in nonces:
            try:
                found[nonce] = bool(markers.present(nonce))
            except (smoke_client.Assertion, ValueError, OSError):
                found[nonce] = None
        return found

    def journey(label):
        directory = scratch / label
        directory.mkdir(mode=0o700)
        member = dict(credentials["member"], username=f"journey-{secrets.token_hex(4)}")
        path = directory / "credentials.json"
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as handle:
            json.dump(dict(credentials, member=member), handle)
        smoke_target = dict(target, credentialsFile=str(path))
        args.fixture_manifest = None
        code, report, summary = smoke(args, mode, commit, lock_digest, smoke_target, directory, manifest, cid.host_descriptor_for(facts))
        return judge_journey(f"{label}.journey", f"{label}: the member journey over HTTPS", code, report, summary)

    def failed_since(count):
        return any(item["status"] == "failed" for item in assertions[count:])

    try:
        states["start"] = observe()
        assertions += evaluate_start(states["start"], predecessor, candidate)
        proceed = not failed_since(0)
        if proceed:
            before_marker, after_marker = f"before-{stamp}", f"after-{stamp}"
            try:
                markers.post(before_marker)
                assertions.append(assertion("start.marker", "a marker was left as the owner before the upgrade", "passed", "posted"))
            except (smoke_client.Assertion, ValueError, OSError) as error:
                assertions.append(assertion("start.marker", "a marker was left as the owner before the upgrade", "failed", f"could not post: {str(error)[:150]}"))
                proceed = False
        steps = []
        if proceed:
            # 1. the upgrade
            mark = len(assertions)
            run1 = lifecycle("upgrade", "upgrade", base, node_path, candidate, predecessor, f"up1-{stamp}")
            states["upgraded"] = observe()
            assertions += evaluate_upgrade("upgrade", states["start"], run1, states["upgraded"], predecessor, candidate)
            assertions += evaluate_markers("upgrade", {"before": (before_marker, True)}, seen([before_marker]))
            if not failed_since(mark):
                assertions.append(journey("upgrade"))
            proceed = not failed_since(mark)
        if proceed:
            operation = states["upgraded"]["journal"]["operationId"]
            try:
                markers.post(after_marker)
            except (smoke_client.Assertion, ValueError, OSError) as error:
                assertions.append(assertion("upgrade.after-marker", "a marker was left as the owner after the upgrade", "failed", f"could not post: {str(error)[:150]}"))
                proceed = False
        if proceed:
            predecessor_facts, candidate_facts = states["start"]["generations"][predecessor], states["start"]["generations"][candidate]
            branch = expected_branch(predecessor_facts, candidate_facts, states["upgraded"]["schema"])
            assertions.append(check("branch.selected", "the candidate's declared compatibility selects a branch", branch is not None,
                                    f"schema {states['upgraded']['schema']} against predecessor min {predecessor_facts['migrations']['min']} and rollbackCompatibleThrough "
                                    f"{candidate_facts.get('rollbackCompatibleThrough')}: {branch}", "the manifests do not declare a usable compatibility window"))
            proceed = branch is not None
        if proceed and branch == "rollback":
            mark = len(assertions)
            run2 = lifecycle("rollback", "rollback", base, node_path, candidate, operation, f"rb-{stamp}")
            states["rolled-back"] = observe()
            assertions += evaluate_rollback(states["upgraded"], run2, states["rolled-back"], predecessor, candidate, operation)
            assertions += evaluate_markers("rollback", {"before": (before_marker, True), "after": (after_marker, True)}, seen([before_marker, after_marker]))
            if not failed_since(mark):
                assertions.append(journey("rollback"))
            proceed = not failed_since(mark)
            current_state = states["rolled-back"]
        elif proceed:
            mark = len(assertions)
            run2 = lifecycle("rollback-refused", "rollback", base, node_path, candidate, operation, f"rb-{stamp}")
            states["refused"] = observe()
            assertions += evaluate_refusal(states["upgraded"], run2, states["refused"], candidate)
            proceed = not failed_since(mark)
            if proceed:
                mark = len(assertions)
                run3 = lifecycle("restore", "restore", base, node_path, candidate, predecessor, operation, f"rs-{stamp}", "candidate" if mode == "candidate" else "rehearsal")
                states["restored"] = observe()
                assertions += evaluate_restore(states["refused"], run3, states["restored"], predecessor, candidate, operation, states["start"]["schema"])
                assertions += evaluate_markers("restore", {"before": (before_marker, True), "after": (after_marker, False)}, seen([before_marker, after_marker]))
                if not failed_since(mark):
                    assertions.append(journey("restore"))
                proceed = not failed_since(mark)
            current_state = states.get("restored")
        if proceed:
            mark = len(assertions)
            run4 = lifecycle("re-upgrade", "upgrade", base, node_path, candidate, predecessor, f"up2-{stamp}")
            states["re-upgraded"] = observe()
            assertions += evaluate_upgrade("reupgrade", current_state, run4, states["re-upgraded"], predecessor, candidate)
            kept = {"before": (before_marker, True)}
            if branch == "rollback":
                kept["after"] = (after_marker, True)
            assertions += evaluate_markers("reupgrade", kept, seen([n for n, _ in kept.values()]))
            if not failed_since(mark):
                assertions.append(journey("reupgrade"))
            proceed = not failed_since(mark)
        if not proceed:
            assertions.append(not_run("sequence.complete", "every step of the sequence ran", "an earlier step failed, so later steps were not attempted"))
        final = remote.collect("install", node_path)
        final_state = observe()
        assertions.append(check("final.serving", "the node ends serving the candidate generation", serving(final_state, candidate),
                                f"generation {candidate[:12]} serves", f"active {str(final_state.get('current'))[:12]}"))
        assertions += cid.evaluate_public(tls_probe(target["origin"]), redirect_probe(target["origin"]), target["origin"])
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
    observation = {"schemaVersion": 1, "origin": target["origin"], "predecessor": predecessor, "candidate": candidate, "branch": branch,
                   "states": states, "finalState": final_state, "trace": trace, "facts": final}
    return dc.finalize(args, mode, commit, lock_digest, GATE_ID, assertions, observation, cid.host_descriptor_for(final),
                       {"python3": platform.python_version(), "podman": cid.podman_version(final)}, final["observedAt"],
                       label="upgrade-drill", runtime_facts=final)


def main(argv=None):
    try:
        return run(argv if argv is not None else sys.argv[1:])
    except dc.NotRun as error:
        print(f"upgrade-drill: not_run: {error}", file=sys.stderr)
        return dc.EXIT_NOT_RUN
    except (dc.DrillError, rg.GraphError) as error:
        print(f"upgrade-drill: {error}", file=sys.stderr)
        return dc.EXIT_USAGE
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        print(f"upgrade-drill: {error.__class__.__name__}: {str(error)[:200]}", file=sys.stderr)
        return dc.EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
