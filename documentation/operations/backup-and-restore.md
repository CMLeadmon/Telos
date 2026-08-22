# Backup and Restore

> Normative operations runbook. Run these commands as the same rootless user
> that owns the Telos Podman containers and volumes.
>
> **Status:** these are current interim recovery mechanics, not certified beta
> recovery. Encrypted off-node backup and separate-host recovery remain blocked
> until Phase 4 evidence proves encrypted snapshots, 14-daily/8-weekly
> retention, atomic all-or-nothing backups, release/schema-version gating,
> session/invite invalidation on restore, and RPO ≤ 24 h / RTO ≤ 4 h drills.

Telos backups must preserve the Telos PostgreSQL database, Grimmory's MariaDB
database, Traefik ACME state, Jellyfin and Grimmory configuration, and the shared storage tree.
Redis contains only rebuildable cache, rate-limit, presence, and pub/sub state;
durable accounts and sessions live in PostgreSQL, so Redis is intentionally not
restored. This prevents stale presence and cache keys from returning after a
recovery.

## Create a backup

Keep the stack running and run:

```bash
STORAGE_PATH=/mnt/storage/shared scripts/backup.sh /srv/telos-backups
```

The database dumps use transactionally consistent formats. Service
configuration and shared files are copied immediately afterward; schedule the
job during a quiet period if uploads or library scans are active. The output
directory is mode-restricted by the script and includes `SHA256SUMS`. It
contains password hashes, session records, private configuration, and community
files, so encrypt it at rest and copy it off the Telos host.

Retain several generations and regularly test a restore on a separate node.

## Restore a backup

Create the containers once from the same or a compatible Telos release, keep
PostgreSQL and MariaDB available, and run:

```bash
STORAGE_PATH=/mnt/storage/shared \
TELOS_DOMAIN=community.example.org \
TELOS_RESTORE_CONFIRM=restore \
scripts/restore.sh /srv/telos-backups/20260715T120000Z
```

The restore script verifies checksums, stops the application services, replaces
both relational databases, overlays the service configuration, preserves the
current shared tree under a timestamped rollback name, restores the backed-up
tree, then starts the application again. It is destructive by design. After
restore, confirm `/api/v1/health`, log in, open a recent chat channel, play one
media item, and open one book before admitting users.

Regenerate the one-time bootstrap token only for a genuinely new node. Never
bootstrap an Owner into a restored database.
