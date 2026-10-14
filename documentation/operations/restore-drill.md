# Restore and Recovery Drill

> **Status:** implemented and rehearsed on two reference VMs (see the results below). The
> `separate-host-restore` external-host gate stays **not_run** for certification until it runs
> against a frozen candidate from a real third machine with public DNS and a trusted certificate.

## What the drill judges

`scripts/drills/recovery.sh` (the `separate-host-restore` gate; also
`scripts/verify-restored-node.sh` and `scripts/restore-drill.sh --live`) runs on an **observer
machine that is neither the host that made the backup nor the restored host**. It reaches the
restored host over a protected SSH configuration and a fixed read-only collector
(`scripts/lib/node_observe.py restore`), and over verified HTTPS. It changes nothing except the
member account and invite the journey creates.

```bash
scripts/drills/recovery.sh --target-file TARGET.json \
  (--candidate-lock LOCK | --rehearsal --source-commit SHA) --result-out OUT/result.json
```

`TARGET.json` is a `drill-target` record (mode 0600) for the **restored** host: its `origin`, node
path, machine-id hash, SSH target and config, and a credentials file. The credentials file holds the
journey's `owner` (the restored owner's real password) and `member` (a **new** username), the
absolute path of the fixture manifest (`fixtureManifestFile`), and `stale`: credentials captured on
the source **before** the backup (`tests/rehearsal/capture-stale-credentials.py` makes them: a
session cookie, an unused invite, a device's access and refresh tokens).

It fails closed on every one of these:

- **Identities and record:** the restore result is `restored` / `separate-host`, matches the drill
  mode, the source and restored machines differ and the restored one matches the target, and (for a
  candidate) the backup was made under the candidate lock.
- **Journal:** all 14 restore steps finished, in order, none failed — `sanitize` before `start stack`.
- **Sanitization:** no session, device, refresh token or usable invite from before the restore is
  live; `restore_events` holds the sanitizer's row for this snapshot; each stale credential's hash
  is **in the restored database and revoked**; the old cookie, device tokens and invite are
  **refused over HTTPS** (401/403, 400); the owner's password still signs in.
- **Install contract:** the same checks as the clean-install gate (platform, Podman, node path and
  files, signing files, services, only Traefik on 80/443, client isolation).
- **Public path:** the origin resolves to an address the restored host owns (DNS cutover), the
  certificate verifies against the platform trust store, port 80 redirects.
- **Journey:** Chat, Stream (HLS, ranged media), Library, Files, commentary on restored content.
  The dependency-outage stages need a controller and are left out of this verdict (A26).
- **Objectives:** `consistencyPointAt ≤ snapshotFinishedAt ≤ incidentDeclaredAt ≤ journey passed`;
  **RPO = incidentDeclaredAt − consistencyPointAt ≤ 24 h; RTO = journey passed − incidentDeclaredAt
  ≤ 4 h.** `incidentDeclaredAt` is what you pass to `restore.sh --incident-declared-at`; without it
  the objectives are `not_run`.

Exit codes: 0 passed, 1 failed, 2 unsafe or malformed input, 3 not_run. A candidate drill refuses
the machine that made the backup as observer; a rehearsal may accept it with
`--rehearsal-source-verifier` (A25) and says so in the result.

## What is not evidence

`scripts/restore-drill.sh --fixture --backup-epoch S --restore-start S --restore-end S` checks the
RPO/RTO arithmetic for tests. Its record has no gate id, host identities or journey and can never
satisfy the gate. A same-host restore, `verify-restore.sh` (pre-flight only), local restore
mechanics and a rehearsal record are not evidence either.

## Rehearsal on the reference VMs (2026-10-14)

Snapshot `80a6798a` of the node VM restored onto the verifier VM (DNS repointed, the node stopped to
play the lost host), observed from the node VM: 29 assertions passed, **RPO 33 s, RTO 223 s**,
four stale credentials refused. Artifacts: `telos-review/beta-evidence/vm/*separate-host-recovery*`.
