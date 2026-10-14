# Upgrade and Rollback

> **Status:** the generation layout, durable operation journal, `scripts/install-candidate.sh`, `scripts/upgrade.sh`,
> `scripts/rollback.sh` and `scripts/restore.sh --mode rollback` are implemented and covered by hermetic tests
> (`tests/operations/upgrade_rollback_fixtures_test.sh`, `candidate_install_test.py`: stub generations, a SIGKILL at every
> step). The live rehearsal on the reference VMs is recorded in `docs/implementation/standalone-beta-progress.md`; it is
> **not** certification evidence. `release/predecessor-policy.json` names no exact predecessor (no release has been
> published), so `scripts/build-predecessor.sh` reports `not_run` and the `upgrade-rollback` external gate stays blocked
> until a frozen candidate, an exact signed predecessor and the release builder's output exist.

## Layout

| Path | Meaning |
|---|---|
| `/srv/telos/generations/<64-hex candidate digest>/` | One immutable release: mode 0755, root-owned, no symlinks, nothing group- or world-writable. It holds a complete release tree (its own `telos`, `scripts/`, Compose files) and `generation.json`. |
| `/srv/telos/current` | Symlink to the active generation. The only mutable pointer; swapped with `rename(2)`, never edited in place. |
| `/var/lib/telos/state/installed-generation.json` | The receipt: which candidate and manifest digest are installed. Not part of any release. |
| `/var/lib/telos/state/operation.json` | The journal of the upgrade or rollback in progress or last run; older ones are kept in `state/operations/`. |

`generation.json` states the version, image digests, the migration window it supports (`min`/`current`/`max`), the
generations it may be upgraded **from** (`predecessorIds`, candidate-lock digests) and `rollbackCompatibleThrough` — the
highest schema version at which the *previous* generation's binaries still run. Every operation re-verifies the files
against it.

## Install the target first

`scripts/upgrade.sh` moves between generations that are already installed. Install the target candidate without activating it:

```bash
sudo scripts/install-candidate.sh --candidate /root/candidate-NEW      # verifies the graph, loads and proves the images, copies the generation
```

It keeps the candidate lock in `/var/lib/telos/state/candidates/<digest>/candidate-lock.json`; those files are the `--candidate-lock` and
`--current-candidate-lock` arguments below. The first install on a node uses `--activate` instead (see `release.md`).

## Upgrade

```bash
scripts/upgrade.sh --generation /srv/telos/generations/<hex> --candidate-lock NEW.lock \
  --current-candidate-lock OLD.lock --result-out OUT.json --confirm UPGRADE-THIS-NODE
```

1. Take the host-wide maintenance lock (a competing operation exits 75).
2. **Recover first.** An unfinished journal is resolved before anything new starts (below).
3. Verify the target generation, the installed generation's receipt, that the target lists the installed one as a
   supported predecessor, and that the database's schema is inside the target's window.
4. Journal `started`. Take and verify an encrypted snapshot with the **running** generation's `backup.sh`
   (`snapshot-verified`).
5. `maintenance`: stop Traefik, the gateway, the client, Jellyfin and Grimmory; the databases stay up.
6. `migrating`: run the target's migrations (`telos migrate`); the journal already holds the schema they must reach.
7. `migrated` → `activated`: swap `current` atomically, stop and start the whole stack on the target's images.
8. Wait for readiness; `serving`; write the new receipt; `complete`.

Every state is written (fsync, rename, fsync of the directory) **before** it is acted on. A failure before the first
migration returns to the installed generation and ends `aborted`. After a migration the compatibility declared in the
manifests decides: the previous binaries accept the schema → **fast rollback** (`rolled-back`); they do not →
`restore-required`, with the snapshot id in the journal and the exact restore command printed.

## Recovery of an interrupted operation

Run the same command again. The journal and two observed facts (the schema, and whether the node is ready) decide, in
`generations.py plan-recovery`:

| Journal state | Schema | Outcome |
|---|---|---|
| `started`, `snapshot-verified`, `maintenance` | any | abort: reactivate the previous generation and start it |
| `migrating` | unchanged | abort |
| `migrating`, `migrated` | the target's | continue: activate and start the target |
| `migrating`, `migrated` | anything else | `restore-required` — the database is in a state nothing explains; no guessing |
| `activated`, `serving` | the target's | start once; ready → complete; not ready → fast rollback if compatible, else `restore-required` |

A journal that cannot be parsed or validated stops every operation until a person reads it.

## Rollback

```bash
scripts/rollback.sh --operation-id <id> --result-out OUT.json --confirm ROLLBACK-THIS-NODE
```

Undoes the **last** upgrade without touching data, and only if the previous generation accepts the current schema.
Otherwise nothing changes and the command prints the restore to run:

```bash
scripts/restore.sh --mode rollback --operation-id <id> --result-out OUT.json \
  (--candidate-lock OLD.lock | --rehearsal) --confirm RESTORE-RECORDED-ROLLBACK
```

That restore works only on the host that ran the upgrade, only for an operation that ended `restore-required` or `failed`
**or that finished `complete` on a schema the previous generation cannot run on**, and only from the snapshot **recorded in
the journal** (never one named on the command line). For a `complete` upgrade the restore first claims the journal
(`generations.py journal claim-restore`): it moves `complete` to `restore-required` only after re-verifying both
generations and proving from their manifests that the previous binaries cannot run on the schema the upgrade left. When
they can, the claim is refused and `rollback.sh` — which keeps every byte written since — is the way back. (The live
rehearsal found that `rollback.sh` named the restore for a finished schema upgrade and the restore then refused it.) A
claimed operation blocks the next upgrade until the restore closes it, exactly like one that failed. It brings the stack
down, keeps the failed state under `telos-rollback/<time>/` (node files, shared storage, and an export of every data
volume), reactivates the generation the upgrade left, and restores the databases, volumes and storage as in
[`backup-and-restore.md`](./backup-and-restore.md), sanitizing sessions, devices and invites. The journal then reads
`restored`. A same-host restore never satisfies the `separate-host-restore` gate.

Neither command deletes a generation or a journal. Operators remove old generations deliberately, once the retention
they want has passed.

## The live drill (`upgrade-rollback` gate)

```bash
scripts/drills/upgrade-rollback.sh --target-file TARGET.json \
  (--rehearsal --source-commit SHA --rehearsal-lock LOCK | --candidate-lock LOCK) --result-out OUT/result.json
```

Run from a machine that is not the node. The node serves the **predecessor** (the target descriptor's
`predecessorGeneration`, `<base>/generations/<64 hex>`), the candidate is installed beside it, and the descriptor carries
`"confirmation": "UPGRADE-AND-ROLL-BACK-THIS-NODE"` — without it the drill is `not_run`; with another value it refuses. The drill sends
one fixed node-side program (`scripts/lib/upgrade_node.py`) that either observes the lifecycle state or runs exactly one of the
candidate's own `upgrade.sh`, `rollback.sh`, `restore.sh --mode rollback` with their own confirmation tokens.

1. It proves the node's identity, that the node starts settled (predecessor active and receipted, no unfinished or blocking
   operation, gateway ok, candidate installed) and changes nothing if it does not.
2. It leaves a marker (a chat message) as the owner, upgrades, and judges the journal, the verified snapshot, the schema
   before/after, the receipt, the active generation, the marker, and a member journey over HTTPS.
3. **The branch is computed here** from the two generation manifests and the schema the upgrade left, not from what the scripts say.
   *Compatible*: `rollback.sh` must succeed and keep both markers, then the candidate is upgraded again.
   *Incompatible*: `rollback.sh` must refuse with the journal, schema and active generation untouched; `restore.sh --mode rollback`
   must then restore — schema back, the marker left after the snapshot gone, the earlier marker present, the failed state kept
   under `telos-rollback/` — and the candidate is upgraded again.
4. It ends on the candidate (receipt, services, public TLS, a final journey). A failing step stops the sequence; later steps are `not_run`.

A candidate exercises only the branch its own compatibility selects. The other branch is proven by the hermetic suites and, as
rehearsal evidence only, by a pair of rehearsal generations (one schema apart); it is never claimed for the candidate.

## What the tests prove, and what they do not

`tests/operations/upgrade_drill_test.py` proves the drill's judgement against a simulated node (both branches, every injected
fault, authorization and start-state refusals) and the node-side program against a stub tree; it is not evidence about a node.

`tests/operations/upgrade_rollback_test.sh` drives the real scripts over real generation directories (verified by
`generations.py`) with stub `telos`, `backup.sh` and `podman`: success and ordering; refusals that change nothing
(wrong confirmation, unlisted predecessor, tampered or group-writable or symlinked generation, path escape, stale receipt,
schema outside the window, unreadable database, lock contention); backup failure; a failing migration with the schema
unchanged and a partial one; an unready target with a compatible and with an incompatible schema; `kill -9` at the
backup, the writer stop, before and after the migration, and during the target stop and start, each followed by the
same command; and compatible, incompatible and wrong-operation rollbacks. It proves the state machine, not a real
Podman stack or real migrations.
