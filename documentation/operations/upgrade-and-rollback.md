# Upgrade and Rollback

> **Status:** the generation layout, durable operation journal, `scripts/upgrade.sh`, `scripts/rollback.sh` and
> `scripts/restore.sh --mode rollback` are implemented and covered by `tests/operations/upgrade_rollback_fixtures_test.sh`
> (stub generations; a SIGKILL at every step). They have **not** yet been exercised on a node against a real
> predecessor release: no release has been published, so `release/predecessor-policy.json` names none and
> `scripts/build-predecessor.sh` reports `not_run`. The `upgrade-rollback` external gate stays blocked until a
> frozen candidate, an exact signed predecessor and the release builder exist.

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

That restore works only on the host that ran the upgrade, only for an operation that ended `restore-required` or `failed`,
and only from the snapshot **recorded in the journal** (never one named on the command line). It brings the stack
down, keeps the failed state under `telos-rollback/<time>/` (node files, shared storage, and an export of every data
volume), reactivates the generation the upgrade left, and restores the databases, volumes and storage as in
[`backup-and-restore.md`](./backup-and-restore.md), sanitizing sessions, devices and invites. The journal then reads
`restored`. A same-host restore never satisfies the `separate-host-restore` gate.

Neither command deletes a generation or a journal. Operators remove old generations deliberately, once the retention
they want has passed.

## What the tests prove, and what they do not

`tests/operations/upgrade_rollback_test.sh` drives the real scripts over real generation directories (verified by
`generations.py`) with stub `telos`, `backup.sh` and `podman`: success and ordering; refusals that change nothing
(wrong confirmation, unlisted predecessor, tampered or group-writable or symlinked generation, path escape, stale receipt,
schema outside the window, unreadable database, lock contention); backup failure; a failing migration with the schema
unchanged and a partial one; an unready target with a compatible and with an incompatible schema; `kill -9` at the
backup, the writer stop, before and after the migration, and during the target stop and start, each followed by the
same command; and compatible, incompatible and wrong-operation rollbacks. It proves the state machine, not a real
Podman stack or real migrations.
