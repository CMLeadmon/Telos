# Deployment

> **Superseded in part by the less-is-more redesign (2026-07).** The `livekit`
> service, its WebRTC/TURN published ports, and the voice/Watch Party surfaces
> were removed. References below to `livekit`, TURN ports, or LiveKit secrets are
> historical. See [beta-feature-status](../product/beta-feature-status.md).

> Spec for AI coding agents and human developers. Statements are normative
> unless marked *(informative)*.

This document is the authoritative production deployment specification for
Telos. The executable Compose definition is [`../../docker-compose.yml`](../../docker-compose.yml);
do not duplicate it into documentation because the copy will drift.

## 1. Trust boundaries

| Network | Exposure | Members |
| --- | --- | --- |
| `telos-ingress` | edge bridge | `traefik`, `telos-core`, `livekit` |
| `telos-backend` | internal | `telos-core`, `redis`, `clamav`, `jellyfin`, `grimmory`, `livekit` |
| `telos-db` | internal | `telos-core`, `telos-migrate`, `postgres`, `grimmory`, `grimmory-db` |

**Database role split.** `telos_owner` owns every Telos schema object and is
used only by the one-shot `telos-migrate` service (`DATABASE_OWNER_URL`).
`telos-core` connects as the least-privilege `telos_runtime` role
(`DATABASE_URL`) — schema usage, table DML, sequence usage, and read-only
migration-state access, but no DDL, ownership, grants, or migration writes.
On a fresh volume, `deploy/postgres/init/001-create-telos-roles.sh` creates
both roles; on a pre-split existing volume, run `scripts/provision-db-roles.sh`
once (it takes a verified pre-change backup and transfers ownership).
`telos-core` starts only after `telos-migrate` completes successfully.

Only Traefik publishes HTTP ports 80 and 443. `telos-core:8080`,
`livekit:7880`, Jellyfin, Grimmory, PostgreSQL, Redis, MariaDB, and ClamAV must
not be published on host interfaces. LiveKit's WebRTC media ports remain
published because they are not HTTP traffic: 7881/TCP, 3478/UDP, and the
configured UDP relay range.

Traefik uses its read-only file provider. It must never receive the Podman or
Docker runtime socket, and its SELinux label must not be disabled. Dynamic
routing lives in [`../../config/dynamic/routes.yaml`](../../config/dynamic/routes.yaml).
The only public application services are the narrow Telos API/client and
LiveKit signaling routes; Jellyfin and Grimmory admin APIs stay internal.

## 2. Environment and secrets

Copy `.env.example` to `.env`, replace every example credential, and keep `.env`
out of version control. Production requires:

- `TELOS_ENV=production`;
- a real `TELOS_DOMAIN` whose A/AAAA records point to this host;
- `ACME_EMAIL` for Let's Encrypt notices;
- a newly generated `TELOS_BOOTSTRAP_TOKEN` of at least 32 characters;
- unique PostgreSQL, Redis, MariaDB, LiveKit, Jellyfin, and Grimmory secrets;
- the rootless runtime user's `APP_UID` and `APP_GID`.

Generate independent values instead of copying the examples:

```bash
openssl rand -hex 24
```

The gateway refuses to start in production when a required value is empty,
contains a known placeholder (including `generate-me`), or when the bootstrap
token is shorter than 32 characters. Development mode intentionally relaxes
cookie and origin protections and is never valid for an internet-facing node.

## 3. TLS and ingress

Traefik obtains certificates for `${TELOS_DOMAIN}` and
`turn.${TELOS_DOMAIN}` from Let's Encrypt with HTTP-01. Ports 80 and 443 must
reach Traefik, and both DNS names must resolve publicly before first boot. ACME
state is retained in the private `traefik_acme` volume. The static self-signed
`telos.local` certificate is not a production trust path.

The dynamic configuration uses Traefik's `env` template function, so the
`TELOS_DOMAIN` environment variable passed to Traefik is part of routing as well
as certificate issuance. Requests with an unrelated Host header do not reach
the application.

For development, layer the repository's separate override over the production
definition. It binds the gateway and LiveKit signaling to loopback; the public
frontend development server proxies their HTTP and WebSocket paths so clients
only need access to port 3000 for page, API, and signaling reachability:

```bash
podman compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

Plain HTTP on a non-loopback origin is not a browser secure context, so
microphone capture and voice publishing are unavailable through that port-3000
path. Use localhost for local voice development or place a trusted HTTPS proxy
or tunnel in front of the development server for a remote test. Never expose
port 3000 as the production entry point.

The frontend dev server must also allow the hostname used by that browser.
`frontend/next.config.ts` reads a comma-separated allowlist from
`TELOS_DEV_ORIGINS`. Put persistent machine-local values in the ignored
`frontend/.env.development.local`, or supply temporary LAN or Tailscale names
when starting the server:

```bash
cd frontend
TELOS_DEV_ORIGINS=telos-dev.lan,100.64.0.10 npm run dev
```

Never bind the development gateway or LiveKit signaling directly to public
interfaces, and never merge the development override into the production
Compose file.

## 4. Startup and health

Validate interpolation before starting:

```bash
podman compose config
podman compose up -d
```

`telos-core` has `restart: unless-stopped` and splits health into liveness and
readiness (`backend/health.go`):

- **`GET /api/v1/health/live`** reports process state only — O(1), no network,
  database, disk, or `statfs` work — so a liveness probe never restarts the
  gateway because a dependency is slow.
- **`GET /api/v1/health/ready`** (aliased by `/api/v1/health`, and the Compose
  healthcheck target) runs cheap dependency probes — PostgreSQL connectivity and
  verified migrations, Redis, a writable shared mount, and Jellyfin/Grimmory —
  each with a 2s child timeout under a 3s overall bound. The sanitized report is
  cached for 5 seconds and every concurrent miss coalesces onto **one** internal
  probe (detached from any caller's cancellation), so 500 simultaneous requests
  invoke each dependency once, never a per-request probe storm. A required
  dependency failure returns HTTP 503; the outbox backlog warns above 100
  pending or 30s age and fails above 1,000 or 2 minutes. Reports carry only
  stable status codes and bounded numbers — never a hostname, path, credential,
  or raw error. In development the media upstreams are advisory (`warn`) rather
  than required.

After first boot, use the one-time bootstrap token to create the Owner account.
Rotate or remove `TELOS_BOOTSTRAP_TOKEN` from the runtime environment after the
Owner exists; the endpoint also refuses a second Owner bootstrap at the database
layer.

### 4.0 Confined storage

`telos-core` mounts one controlled `${STORAGE_PATH}` root at `/data/shared` and
performs every physical file operation beneath it via `openat2` with
`RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS` (`backend/storage.go`)
— a user-supplied key can never escape its area through traversal, symlinks, or
magic links, and startup fails on a kernel without `openat2`/`renameat2`
support rather than falling back to lexical confinement. Uploads stage into
exclusive mode-0600 files, are hashed/validated/scanned, then promoted with
`renameat2(RENAME_NOREPLACE)` (or an exclusive copy+sync+no-replace rename
across filesystems). Jellyfin and Grimmory keep only their own narrow config/
data mounts.

### 4.0.1 Storage quotas

Uploads are admitted through a `QuotaGuard` (`backend/quota.go`) that, under a
PostgreSQL advisory lock, charges **physical** bytes — every user-owned
non-terminal lifecycle asset plus that user's live reservations — against a
per-user limit (`TELOS_USER_QUOTA_BYTES`, default 1 GiB), and checks `statfs`
free space minus all live reservations and unowned orphan/quarantine bytes
against a node reserve (`TELOS_NODE_RESERVE_BYTES`, default 5 GiB) before
staging or promotion. A zero or unparseable production value is invalid.
Reservations expire (15 min) and are released idempotently, so concurrent
uploads cannot overbook user or node capacity.

### 4.0.2 File lifecycle and reconciliation

Logical file and folder mutations (`backend/file_mutations.go`) run under
per-row `FOR UPDATE` locks: rename and move change only the database logical
name/parent, never the immutable content key; folder create/rename/move rejects
normalized sibling collisions and parent cycles; a folder deletes only when
empty (`409 folder_not_empty`); and a file delete atomically moves
`available → deleting` and hides the row immediately. The mutation actor must
own the resource or hold `manage_files`. Every mutation writes an immutable
`file_audit` row; the log is exposed as a stable cursor-paginated list at
`GET /api/v1/files/audit` (requires `manage_files`).

A single per-node `FileReconciler` (`backend/reconciliation.go`) converges the
database with physical and catalog state under a session advisory lock, so a
second walker on the node yields. Each bounded pass (≤100 rows per category,
30s deadline) finalizes `deleting → deleted` only after **proving physical
absence**, repairs an expired-lease `promoting` row to `available` only when the
staged asset's hash and size prove identity (otherwise marks it `missing`), and
acknowledges a delivered Grimmory handoff as `consumed` only once the catalog
confirms the import — unobserved absence is never assumed to be success. Passes
are idempotent: a second identical pass makes no new mutation or audit row.
Until confined physical storage and the book catalog are wired, the reconciler
is a deliberate no-op — it never marks a row `deleted` or `missing` without a
way to verify physical state.

### 4.1 Schema migrations

Migrations live in `backend/db/migrations/NNNN_name.sql` with contiguous
four-digit versions from `0001`. On startup the migration engine
(`backend/migrations.go`) computes a SHA-256 per file, takes a PostgreSQL
advisory lock (bounded by a 30s lock timeout), applies each pending migration
in its own transaction, and records `(version, name, checksum)`. Applied
history is immutable: a changed checksum or name, a version gap, or an unknown
future version aborts startup rather than proceeding. A legacy version-only
`schema_migrations` table is upgraded in place only when its versions are
exactly contiguous from `0001`; otherwise the engine refuses to bless an
unknown history. Concurrent migrators serialize on the advisory lock so each
version applies exactly once. Phase 3's P3-T2 splits this into a one-shot
`telos-migrate` service (which applies) and `telos-core serve` (which only
verifies and fails readiness on a mismatch).

## 5. Storage

The default shared tree is `/mnt/storage/shared` and can be moved with
`STORAGE_PATH`:

- `media/` is the community media library and is mounted read-only in Jellyfin;
- `books/` is Grimmory's organized catalog;
- `bookdrop/` is the upload/ingestion inbox shared by Telos and Grimmory;
- `staging/` holds scanned Telos uploads.

PostgreSQL, Redis, Jellyfin configuration, Grimmory configuration, and MariaDB
use named volumes. Database and configuration durability does not replace a
backup. Follow the normative
[`../operations/backup-and-restore.md`](../operations/backup-and-restore.md)
runbook before admitting multiple users.

## 6. Security invariants

- All credentials come from `.env` interpolation; no secret is literal in
  source, Compose, or Traefik configuration.
- Jellyfin and Grimmory remain separate containerized processes. Their copyleft
  code is never linked or compiled into `telos-core`.
- The runtime socket is outside the perimeter container.
- Long-lived media responses are streamed, while connection establishment and
  upstream response headers are bounded by gateway timeouts.
- Production traffic reaches the application only through Traefik TLS.
