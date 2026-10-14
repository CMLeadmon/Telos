# Backup and Restore

> Normative operations runbook. Run these commands as **root** on the Telos host
> (rootful Podman, Ubuntu 24.04+; verified on 26.04).
>
> **Status:** the encrypted off-node backup and the separate-host restore are implemented and
> were rehearsed end to end on two reference VMs (snapshot restored onto a second machine,
> sanitized, observed from a third location: RPO 33 s, RTO 223 s). That rehearsal is not
> certification evidence: the `encrypted-backup` and `separate-host-restore` gates need a frozen
> candidate, a real off-node repository, a real observer machine and public DNS/TLS cutover.
> The restore drill is described in [`restore-drill.md`](./restore-drill.md); retention in
> [`data-retention.md`](./data-retention.md).

## What a backup contains

One consistent snapshot of: the Telos PostgreSQL database (custom-format dump, ownership kept),
Grimmory's MariaDB database, `node.env` and the node's `config/` (signing keys), the Traefik ACME,
Jellyfin and Grimmory volumes, the shared storage tree, a release capsule (the installed release,
less build output — `config/release-exclude.txt`) and the migration manifest. A strict manifest
records the size and SHA-256 of every component, the source machine's identity hash, the release,
the candidate lock digest and the **consistency point** (the moment the writers were stopped).
Redis is rebuildable and is not backed up.

The snapshot holds password hashes, session records, signing keys and community files. It is
encrypted by restic before it leaves the host and is never stored on the Telos node; the staging
directory is root-only and removed on every exit path.

## Configure it once

1. Create an **off-node** restic repository (SFTP, S3, B2, REST, rclone, Azure or GS — never a local
   path) and initialize it yourself: `restic init`. Nothing here creates a repository.
2. Put the repository password in a root-only file and keep a second copy **offline**. Without it the
   backups are unreadable, and nothing can recover it.
3. Write `/etc/telos/backup.env` (root-owned, mode 0600):
   `RESTIC_REPOSITORY=…`, `RESTIC_PASSWORD_FILE=/etc/telos/restic.pw`, optionally
   `RESTIC_CREDENTIALS_FILE=` for backend keys (same ownership and mode). For SFTP use a key that
   can only reach the repository directory (`restrict,command="internal-sftp -d …"`).
4. `systemctl enable --now telos-backup.timer` (installed under `deploy/systemd/`). It runs at
   00:00, 06:00, 12:00 and 18:00 with up to 30 minutes of jitter and catches up after downtime.

## What a backup does

`scripts/backup.sh` takes the host-wide maintenance lock (a competing operation exits 75), stops the
writers (Traefik, the gateway, Jellyfin, Grimmory) while both databases stay up, dumps and copies
everything, **restarts exactly the writers it stopped before the long upload**, uploads one snapshot
tagged `scheduled`, `telos`, `release:…`, `candidate:…`, then proves it: tags, listing, the manifest
read back from the repository, and a 5 % data read check. Retention (14 daily / 8 weekly) is applied
last, and the outcome is written atomically to `<node>/ops/last-backup.prom`, which the gateway
exposes as `telos_backup_last_success_timestamp_seconds` and `telos_backup_last_result` (alerts:
[`incidents.md`](./incidents.md)). On failure after the snapshot exists the log names it and never
calls it verified. A killed run is cleaned up by the next one from its journal.

Writers are down for the dump window only (about a minute on the reference VM).

## Restore onto an empty reference host

A restore is only for an **empty host that is not the one that made the backup**. It replaces
databases, configuration, volumes and shared storage.

1. Install the release (`install.sh`), make Podman work, and put the restic password and backend
   credentials on the host. Place TLS material (private CA directory, or let ACME run once DNS
   points here) where `node.env` expects it.
2. Load or build the release's images; `restore.sh` builds only the ones that are missing.
3. Run:

```bash
RESTIC_REPOSITORY=… RESTIC_PASSWORD_FILE=/etc/telos/restic.pw \
scripts/restore.sh --mode separate-host --snapshot <id> \
  (--candidate-lock PATH | --rehearsal) \
  --incident-declared-at 2026-10-14T04:30:00Z \
  --result-out /var/lib/telos/state/restore-result.json \
  --confirm RESTORE-TO-EMPTY-REFERENCE-HOST
```

Everything that can be checked is checked **before the first change**: the manifest and every
component hash, that this machine differs from the one that made the backup, that the candidate
matches (a `--rehearsal` states that it skips this and its result is never evidence), that the
backup's schema is no newer than this release and no applied migration's checksum drifted, free
disk, and every archive scanned for traversal, links out of the tree, devices and FIFOs. Then it
preserves any prior node files under `telos-rollback/<time>` (mode 0700), restores configuration,
storage and volumes, starts **only** the two databases, restores both, runs migrations, runs
`telos-core sanitize-restore` (revokes every session, device, refresh token and unused invite the
backup carried, in one transaction, and records it in `restore_events`), and only then starts the
rest. Every transition is appended to `<node>/state/restore-journal.jsonl`. On any failure the
stack is brought down again (a node holding unsanitized credentials must not serve) and the staging
directory is deleted; the host is then **not** empty, so reset it by hand (remove the `telos_*`
volumes and `/var/lib/telos`) before retrying.

4. Point DNS at the restored host, confirm a valid certificate, and run the recovery drill from a
   third machine ([`restore-drill.md`](./restore-drill.md)). Admit users only after it passes.

Accounts, roles and content come back unchanged; everyone signs in again with their password.
Never bootstrap an Owner into a restored database and never run `telos init --force` on it.
