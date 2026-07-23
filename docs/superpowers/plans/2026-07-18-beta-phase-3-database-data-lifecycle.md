# Telos Beta Phase 3: Database and Data Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make PostgreSQL privileges, migrations, query behavior, event delivery, account deletion, backup, and restore safe for durable confidential beta data with no planned reset.

**Architecture:** Give schema changes to a one-shot owner service and DML to a least-privilege runtime role; verify immutable migration history under an advisory lock; make all list/query work bounded; join durable mutations to real-time delivery with an outbox; express deletion as an idempotent lifecycle; and produce encrypted off-node recovery artifacts.

**Tech Stack:** PostgreSQL 16.14, pgx/v5, Go 1.26.5, Redis 7, `pg_stat_statements`, `pg_trgm`, restic, Podman/Docker Compose

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its focused commit.
- Begin only after Phase 2 security/authentication is accepted.
- Preserve migration bytes `0001` through `0007`; all repairs use new migrations.
- Reserve `0008_database_invariants.sql`, `0009_search_indexes.sql`, `0010_transactional_outbox.sql`, and `0011_account_lifecycle.sql`.
- PostgreSQL is authoritative. Redis stores no only copy of durable state.
- Runtime never receives schema-owner credentials and cannot create, alter, drop, own, grant, or write migration history.
- All persistence integration tests run against disposable PostgreSQL 16.14 and Redis 7 without skips.
- Backup plaintext exists only in a mode-`0700` temporary directory and is removed on every exit path.
- A backup is successful only after the encrypted snapshot is present in the configured off-node repository.

## Entry Gate

- [ ] Phase 2 evidence identifies the exact starting commit.
- [ ] Migrations `0001` through `0007` are tracked, contiguous, and match the reviewed live deployment bytes.
- [ ] Phase 2 auth, concurrency, channel, socket, proxy, and shutdown suites pass.
- [ ] Record the current live migration metadata shape and schema fingerprint without printing row data.

## File Responsibility Map

| Area | Files |
|---|---|
| Migration engine | `backend/migrations.go`, migration unit/integration tests, `backend/main.go` |
| Roles/database config | `deploy/postgres/init/001-create-telos-roles.sh`, `scripts/provision-db-roles.sh`, `backend/database.go`, Compose/environment/docs |
| Schema/search | migrations `0008` and `0009`, schema/search integration tests |
| Pagination/query work | `backend/pagination.go`, `backend/auth.go`, `backend/chat.go`, `backend/settings.go`, frontend API/list consumers |
| Durable delivery | migration `0010`, `backend/outbox.go`, mutation call sites |
| Account lifecycle | migration `0011`, `backend/account_lifecycle.go`, retention documentation |
| Recovery mechanics | `backend/backup_manifest.go`, `scripts/backup.sh`, `scripts/restore.sh`, restic retention/drill tests |

---

## P3-T1: Add a Locked, Checksum-Verified Migration Engine

**Closes:** D02

**Files:**

- Create: `backend/migrations.go`
- Create: `backend/migrations_test.go`
- Create: `backend/migrations_integration_test.go`
- Modify: `backend/main.go`
- Modify: `scripts/test-backend.sh`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type Migration struct {
    Version int
    Name    string
    SQL     []byte
    SHA256  string
}

type MigrationState struct {
    CurrentVersion  int
    ExpectedVersion int
    ChecksumSet     string
}

func DiscoverMigrations(fsys fs.FS, dir string) ([]Migration, error)
func PlanMigrations(local []Migration, applied []AppliedMigration) ([]Migration, error)
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (MigrationState, error)
func VerifyMigrations(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (MigrationState, error)
```

Four-digit versions are contiguous from `0001`; applied name/checksum history is immutable; `migrate` applies under one PostgreSQL advisory lock and `serve` only verifies.

- [ ] Add failing table tests for malformed/duplicate names, local/applied gaps, changed checksum/name, unknown future version, and deterministic checksum-set ordering.
- [ ] Add failing disposable-PostgreSQL tests for empty application, live `0001–0007` upgrade, concurrent migrators, SQL rollback, lock timeout, and a 60-second migration startup context distinct from request contexts.
- [ ] Upgrade legacy version-only metadata only when versions are exactly contiguous and known schema-fingerprint probes match the reviewed `0001–0007` schema; otherwise fail without blessing history.
- [ ] Implement SHA-256 metadata, advisory locking, per-migration transactions, future/gap/change rejection, and deterministic state; make `serve` fail readiness when verification fails.
- [ ] Extend `scripts/test-backend.sh` to accept a suite plus Go test arguments and guarantee disposable services/cleanup.
- [ ] Run `scripts/test-backend.sh db -run 'Test(DiscoverMigrations|PlanMigrations|Migration)'`; expected result: one concurrent migrator applies each version and all invalid histories fail with stable public codes.
- [ ] Commit with message `feat(db): add locked checksum-verified migrations`.

## P3-T2: Separate Schema-Owner and Runtime Roles

**Closes:** D01

**Files:**

- Create: `deploy/postgres/init/001-create-telos-roles.sh`
- Create: `scripts/provision-db-roles.sh`
- Create: `scripts/tests/database_roles_test.sh`
- Create: `backend/database_roles_integration_test.go`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `documentation/architecture/02-deployment.md`
- Modify: `documentation/operations/backup-and-restore.md`

**Contract:** `telos_owner` owns Telos schema objects and is used only by one-shot `telos-migrate`; `telos_runtime` has schema usage, required table DML, sequence usage, and migration-state read access. `DATABASE_OWNER_URL` never reaches `telos-core`; `DATABASE_URL` never owns objects.

- [ ] Add failing integration/static tests proving the current runtime can create/alter/drop and rendered Compose leaks the owner-equivalent database account.
- [ ] Implement idempotent clean-volume role creation and an existing-volume provisioning script that takes a verified pre-change backup, transfers ownership, grants exact current/default privileges, and aborts on any unexpected owner/object.
- [ ] Add a one-shot `telos-migrate` service with `["./telos-core","migrate"]`; make `telos-core` run `serve` after `service_completed_successfully`.
- [ ] Remove `POSTGRES_USER` reuse from runtime configuration and ensure owner/admin credentials are neither in runtime environment nor in its readable mounts.
- [ ] Run `scripts/test-backend.sh db -run 'Test(SchemaOwner|RuntimeRole)'` and `sh scripts/tests/database_roles_test.sh`; expected result: owner migrations/runtime CRUD succeed while runtime DDL, grants, ownership, and migration writes fail.
- [ ] Run `podman compose --env-file tests/fixtures/compose.env config --quiet`; expected result: the committed nonsecret fixture renders, the migrator has no route/persistent lifecycle, and core has no owner URL.
- [ ] Commit with message `feat(db): isolate migration owner from runtime`.

## P3-T3: Enforce Relational Invariants, Indexes, and Safe Query Statistics

**Closes:** D03

**Files:**

- Create: `backend/db/migrations/0008_database_invariants.sql`
- Create: `backend/database_invariants_integration_test.go`
- Create: `deploy/postgres/init/002-create-telos-observer.sh`
- Create: `scripts/provision-db-observer.sh`
- Modify: `scripts/tests/database_roles_test.sh`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Migration contract:** Add a collision-checked unique canonical-username index plus named checks for username canonical form, channel type, override values, message length/deletion state, session/invite dates, file hash/size/scan state, progress range, notification shape, and JSON-object fields; correct `manage_messages` grants through new statements; add a leading index for every foreign-key join/delete path.

- `telos_owner` creates `pg_stat_statements` and owns its extension objects.
  An idempotently provisioned, non-login-by-default `telos_observer` role receives
  only `pg_read_all_stats`, `CONNECT`, and read access to the normalized
  statistics views; owner provisioning revokes view access and reset-function
  execution from `PUBLIC` and `telos_runtime`. An optional mode-`0600`
  `DATABASE_OBSERVER_URL_FILE` activates a separate operator login that is never
  mounted into `telos-core`, frontend, Jellyfin, or Grimmory. Runtime receives no
  statistics role, direct grant, `SECURITY DEFINER` view, or observer credential.

- [ ] Add failing invalid-row fixtures, a catalog query enumerating foreign keys without leading indexes, and a grant assertion proving Moderator lacks the intended message-management mapping.
- [ ] Preflight existing rows, including case-folded username collisions; abort for operator resolution on any ambiguous identity, otherwise add named constraints as `NOT VALID`, remediate only deterministic invalid values, validate constraints, and add all 20 missing leading indexes without editing earlier migrations.
- [ ] Grant `manage_messages` to Owner, Administrator, and Moderator, preserving custom grants; correct the `manage_members` description to shipped disable/enable actions without password replacement.
- [ ] Enable `pg_stat_statements`, `compute_query_id=on`, `log_statement=none`, and bind-parameter redaction; provision the exact observer role/login through clean-volume init and existing-volume scripts while denying runtime and every application container access to statistics and its credentials.
- [ ] Run `bash scripts/test-backend.sh db -run 'Test(DatabaseConstraints|ForeignKeyIndexes|QueryStatisticsIsolation|MessageManagementGrant)'` and `sh scripts/tests/database_roles_test.sh`; expected result: invalid rows fail, the index audit is empty, grants are correct, observer sees normalized statistics, and runtime cannot read statement text.
- [ ] Run `bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env --checks database-observer` and `sh scripts/provision-db-observer.sh --check`; expected result: PostgreSQL statistics settings are present, observer provisioning is idempotent, no application service contains the observer URL/file, and no rendered secret value is printed.
- [ ] Commit with message `feat(db): enforce relational invariants and indexes`.

## P3-T4: Bound Pools, Timeouts, Authentication Queries, and Session Touches

**Closes:** D03, D04

**Files:**

- Create: `backend/database.go`
- Create: `backend/database_test.go`
- Create: `backend/query_builder.go`
- Create: `backend/query_builder_test.go`
- Create: `backend/auth_store_test.go`
- Modify: `backend/auth.go`
- Modify: `backend/main.go`
- Modify: `.env.example`
- Modify: `docker-compose.yml`

**Interfaces:**

```go
type DatabaseConfig struct {
    URL                    string
    MaxConns               int32
    MinConns               int32
    MaxConnLifetime        time.Duration
    MaxConnIdleTime        time.Duration
    ConnectTimeout         time.Duration
    StatementTimeout       time.Duration
    LockTimeout            time.Duration
    IdleTransactionTimeout time.Duration
    RequestTimeout         time.Duration
}

func LoadAuthenticatedUser(ctx context.Context, q DBTX, tokenHash string) (*UserContext, error)

type QueryRoute string
type QuerySort string

type ListQuery struct {
    Route      QueryRoute
    Sort       QuerySort
    FilterKeys map[string]string
    Page       PageRequest
}

func BuildListQuery(q ListQuery) (sql string, args []any, err error)
```

Defaults are max/min connections 20/2, connect/statement/lock/idle-transaction/request timeouts 5s/5s/2s/5s/10s. A coalescing session-touch worker has capacity 256 and batches at most 100.

- [ ] Add failing config, statement/lock/idle timeout, request cancellation, authentication query-count, touch coalescing/saturation, shutdown, unknown route/sort/filter, identifier-injection, and query-count-as-page-size-grows tests.
- [ ] Centralize pgx pool construction and set timeouts on every connection; reject unsafe zero/negative/out-of-range configuration.
- [ ] Replace per-request session/user/role/permission fan-out with one aggregate query and message-history actor/reaction/pin/embed fan-out with bounded aggregate queries; assert one authentication select and a fixed documented history-query count as page size grows.
- [ ] Implement a closed route registry whose entry fixes its table/join projection, allowed filter keys, allowed sort enums, seek columns, authorization predicate, and hard limit; interpolate no client value as SQL syntax and reject every route/sort/filter absent from that registry.
- [ ] Replace unbounded last-seen goroutines with the bounded worker and wire startup/drain into the Phase 2 lifecycle; saturation drops a redundant touch metric, never an authentication result or a new goroutine.
- [ ] Run `bash scripts/test-backend.sh db -race -count=1 -run 'Test(DatabaseTimeouts|LoadAuthenticatedUser|HistoryQueryCount|BuildListQuery|SessionTouchWorker)'`; expected result: bounded timeouts, fixed query counts, all non-allowlisted query shapes fail, and saturation/shutdown are race-free.
- [ ] Commit with message `perf(auth): bound database work and session touches`.

## P3-T5: Standardize Stable Cursor Pagination

**Closes:** D04

**Files:**

- Create: `backend/pagination.go`
- Create: `backend/pagination_test.go`
- Create: `backend/pagination_integration_test.go`
- Create: `backend/list_policy_test.go`
- Create: `frontend/src/lib/pagination.test.ts`
- Create: `documentation/operations/api-list-inventory.md`
- Modify: `backend/chat.go`
- Modify: `backend/settings.go`
- Modify: `backend/main.go`
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `frontend/src/components/settings/AdminUsersSection.tsx`
- Modify: `frontend/src/components/settings/AdminInvitesSection.tsx`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces:**

```go
type CursorValue struct {
    Kind  string `json:"kind"` // "time", "text", "int", or "uuid"
    Value string `json:"value"`
}

type PageCursor struct {
    Version    uint8         `json:"version"`
    KeyID      string        `json:"keyId"`
    Scope      string        `json:"scope"`
    Sort       string        `json:"sort"`
    FilterHash string        `json:"filterHash"`
    Values     []CursorValue `json:"values"`
    ID         string        `json:"id"`
    IssuedAt   time.Time     `json:"issuedAt"`
}

type CursorCodec interface {
    Encode(scope string, cursor PageCursor) (string, error)
    Decode(scope string, token string) (PageCursor, error)
}

type PageRequest struct {
    Scope string
    Sort  string
    Limit int
    After string
}

type Page[T any] struct {
    Items      []T    `json:"items"`
    NextCursor string `json:"nextCursor,omitempty"`
}
```

Default ordering uses descending `(created_at,id)` seeks, `limit+1`, default
50, maximum 100, and no offset/page arithmetic. Each route registry entry from
P3-T4 fixes cursor scope, schema version, allowed sort, typed seek tuple,
normalized-filter hash, and direction. Phase 5 My List explicitly uses
`manual=(list_revision,position,id)` in ascending position order and rejects a
cursor after its list revision changes instead of forcing explicit user order
into a timestamp sort.

Cursor envelopes are canonical-JSON/base64url encoded, HMAC-authenticated, and
expire after 24 hours. A mode-`0600` `TELOS_CURSOR_KEYS_FILE` contains a
nonempty key-ID map plus one active signing key; every decoded key has at least
32 bytes of cryptographically random material, IDs and byte values are unique,
and cursor keys are distinct from session/HLS keys. Encode uses only the active
key, decode accepts active/retiring keys, and a key cannot be removed until the
24-hour cursor lifetime has elapsed. Scope, version, sort, filter hash, value
count/types, ID, issue time, and key ID are validated before a query is built.

`api-list-inventory.md` is exhaustive: each JSON array or decoded upstream
collection names its method/route/field, authorization predicate, ordering,
query builder, and either cursor policy or hard cap. Initial hard caps are
channels 100, roles 100, permissions 64, reactions per message 100, pins per
channel 100, and each search scope 15. Message history, users, members, invites,
and sessions are cursor-paginated here. A registration test fails for an
unlisted list response; later phases must register their catalogs, audit
streams, notifications, annotations, My List, Watch Party, and reconciliation
lists.

- [ ] Add failing malformed/tampered cursor, excessive limit, arithmetic overflow, equal tuple, concurrent insert, deletion-between-pages, wrong scope/version/sort/filter/type/key ID, expired cursor/retiring-key, short/duplicate/example/cross-purpose key, My List revision/position tuple, and unregistered/unbounded list tests.
- [ ] Implement canonical encode/decode/page helpers with stable `invalid_cursor`, constant-time signature verification, exact route-schema binding, 24-hour expiry, active/retiring key rotation, typed seek tuples, and no caller-controlled SQL identifiers.
- [ ] Inventory every current list; convert message history, users, members, invites, and sessions to page envelopes/indexed seeks, enforce the documented fixed caps for channels, roles, permissions, reactions, pins, and search, and reserve registered policies for Phase 4 audit/catalog routes.
- [ ] Update current frontend consumers to follow opaque `nextCursor` values and merge stable IDs; register the same contract and sort-specific tuple definitions for Phase 4/5 catalogs, notifications, annotations, My List, Watch Party, and reconciliation.
- [ ] Run `bash scripts/test-backend.sh db -run 'Test(CursorPagination|CursorScope|CursorKeyRotation|MyListCursorRevision|ListPolicyRegistry)'` and `cd frontend && npm run test:unit -- src/lib/pagination.test.ts`; expected result: all lists are registered/bounded, pre-existing rows are neither duplicated nor skipped, and wrong-scope/version/sort/filter/key input returns `400`.
- [ ] Commit with message `feat(api): add stable cursor pagination`.

## P3-T6: Index Local Search and Bound Upstream Search

**Closes:** D04

**Files:**

- Create: `backend/db/migrations/0009_search_indexes.sql`
- Create: `backend/search.go`
- Create: `backend/search_test.go`
- Create: `backend/search_integration_test.go`
- Modify: `backend/main.go`
- Modify: `backend/library.go`

**Contract:** Search terms are 2–128 characters; each scope returns at most 15 authorized results. Local normalized users/channels/files use `pg_trgm` GIN indexes; files require clean/intended purpose. Grimmory decoding caps at 8 MiB/5,000 books and Jellyfin requests `Limit=15`.

- [ ] Add failing `EXPLAIN`/bound tests showing current substring scans, in-memory channel filtering, unbounded upstream decoding, and unauthorized file results.
- [ ] Add normalized trigram indexes for usernames, display names, channel names, and filenames; make the index expressions match query expressions.
- [ ] Move local permission/purpose filtering into bounded SQL and cancel all scope work on the 10-second request deadline.
- [ ] Bound Grimmory bytes/records and Jellyfin result counts; partial scope failure returns a named degraded scope without leaking raw error text.
- [ ] Run `scripts/test-backend.sh db -run 'Test(SearchUsesIndexes|SearchBounds)'`; expected result: intended GIN plans appear and every scope returns at most 15 authorized results.
- [ ] Commit with message `perf(search): index local search and bound upstreams`.

## P3-T7: Add Transactional Outbox Delivery

**Closes:** D05, S08

**Files:**

- Create: `backend/db/migrations/0010_transactional_outbox.sql`
- Create: `backend/outbox.go`
- Create: `backend/outbox_test.go`
- Create: `backend/outbox_integration_test.go`
- Modify: `backend/auth.go`
- Modify: `backend/chat.go`
- Modify: `backend/main.go`
- Modify: `backend/server.go`
- Modify: `backend/settings.go`

**Interfaces:**

```go
type OutboxEvent struct {
    Topic           string
    EventType       string
    AggregateType   string
    AggregateID     string
    IdempotencyKey  string
    Payload         json.RawMessage
}

func EnqueueOutbox(ctx context.Context, tx pgx.Tx, event OutboxEvent) (string, error)

func (s *OutboxSecurityEventSink) Record(ctx context.Context, tx pgx.Tx, intent SecurityEventIntent) error
func (d *OutboxDispatcher) Drain(ctx context.Context) error
```

The dispatcher claims batches of 100 with `FOR UPDATE SKIP LOCKED`, polls every 250ms, caps exponential retry at one minute, retains published rows seven days, and exposes pending count/oldest age.

- [ ] Add failing rollback, Redis outage, crash-after-publish, duplicate key, payload/topic validation, cleanup, concurrent-dispatcher, and security-mutation-without-event tests for bootstrap, invite, session, password, account status, and role/permission paths.
- [ ] Add constrained outbox schema plus claim/retry/cleanup indexes and unique idempotency keys.
- [ ] Implement transaction-bound enqueue, bounded dispatch/retry, duplicate-tolerant publication, health readout, cancellation, the Phase 2 `SecurityEventSink`, and the Phase 2 `OutboxDrainer`; remove the transitional no-op adapters.
- [ ] Call `EnqueueOutbox` in the same transaction as chat create/edit/delete/reaction/pin and each existing P2 security intent: bootstrap completion; invite create/accept/revoke; login lockout; password/session revoke; account enable/disable; and role or permission mutation. Use stable idempotency keys, actor/target IDs rather than names, and payloads containing no secret, token, note text, IP address, or raw request data; P3-T8 wires the deletion call site after creating it and P5-T12 wires channel overrides.
- [ ] Start one dispatcher per process, permit concurrent replicas safely, and drain in-flight work for the Phase 2 shutdown interval.
- [ ] Run `bash scripts/test-backend.sh db -race -count=1 -run 'Test(Outbox|ChatMutationEnqueuesOutbox|SecurityMutationEnqueuesOutbox)'`; expected result: rollback publishes nothing, every currently implemented security mutation has exactly one durable event, Redis loss retains work, and dispatchers never double-claim.
- [ ] Commit with message `feat(events): deliver durable mutations through outbox`.

## P3-T8: Implement Complete Account Deletion and Retention

**Closes:** D06

**Files:**

- Create: `backend/db/migrations/0011_account_lifecycle.sql`
- Create: `backend/account_lifecycle.go`
- Create: `backend/account_lifecycle_test.go`
- Create: `backend/account_lifecycle_integration_test.go`
- Create: `documentation/operations/data-retention.md`
- Modify: `backend/settings.go`
- Modify: `backend/auth.go`

**Interfaces:**

```go
type DeletionReceipt struct {
    RequestID   string
    UserID      string
    CompletedAt time.Time
}

type AssetRemover interface {
    Remove(ctx context.Context, area, storageKey string) error
}
```

The migration adds soft-deleted user identity, revoked invites, explicit file purpose/visibility, idempotent deletion requests, and physical-asset deletion jobs.

- [ ] Add a matrix-backed failing test covering every current durable table, session/socket invalidation, avatar/private-upload removal, retained public messages, interrupted asset deletion, and repeat request.
- [ ] Publish exact delete/anonymize/retain behavior for current and Phase 5 tables; later migrations must extend the same fixture before passing.
- [ ] In one transaction revoke sessions/invites, delete private preferences/progress/reactions/reads/notifications/uploads, anonymize permitted public authorship as `Deleted User`, enqueue the account-deletion `SecurityEventIntent` through the Phase 3 outbox, enqueue assets, and record an idempotent receipt.
- [ ] Close sockets before reporting success; run deletion jobs after commit with hash/key verification, retry safety, and reconciliation visibility.
- [ ] Run `scripts/test-backend.sh db -run 'Test(AccountDeletion|RetentionMatrix|AssetDeletionJobs)' -race`; expected result: no private state/session survives, retained content is anonymous, and repeat deletion returns the same receipt.
- [ ] Commit with message `feat(accounts): implement durable deletion lifecycle`.

## P3-T9: Create Complete Encrypted Off-Node Backups and Retention

**Closes:** D07

**Files:**

- Create: `backend/backup_manifest.go`
- Create: `backend/backup_manifest_test.go`
- Create: `scripts/maintenance-lock.sh`
- Create: `scripts/prune-backups.sh`
- Create: `scripts/verify-recovery-escrow.sh`
- Create: `scripts/tests/backup_test.sh`
- Create: `deploy/systemd/telos-backup.service`
- Create: `deploy/systemd/telos-backup.timer`
- Create: `documentation/operations/recovery-key-escrow.md`
- Modify: `scripts/backup.sh`
- Modify: `.env.example`
- Modify: `.gitignore`
- Modify: `documentation/operations/backup-and-restore.md`

**Manifest contract:** format version, release, schema version, migration checksum set, UTC creation time, component hashes/sizes, and tool versions. Required components are PostgreSQL, Grimmory MariaDB, Compose/service config, encrypted environment material, ACME state, Jellyfin/Grimmory config, shared storage, the installed signed release-recovery capsule (application plus every runtime/migration/operations OCI image and its trust material), manifest, and checksums. Restic deduplication prevents each scheduled snapshot from storing unchanged capsule bytes again.

Required settings are `RESTIC_REPOSITORY` using a non-local backend, `RESTIC_PASSWORD_FILE` mode `0600`, and `TELOS_RELEASE`. Retention provides recoverable coverage for the latest 14 distinct daily buckets and 8 distinct weekly buckets; one snapshot may satisfy both a daily and weekly bucket.

- `backup`, `prune`, `restore`, `install`, `upgrade`, `rollback`, and drill scripts all source
  `scripts/maintenance-lock.sh` and hold one exclusive, nonblocking
  `flock(2)` on `/run/telos/maintenance.lock` from preflight through service
  recovery. Contention exits with `75 maintenance_in_progress`; no script uses
  a private lock name or proceeds unlocked.
- Restic repository access material and the encryption password are excluded
  from every snapshot, environment bundle, log, and repository-local file. The
  operator maintains two independently recoverable out-of-band escrow copies:
  one off-node password-manager record and one offline sealed copy, both paired
  with repository URL/access credentials. Quarterly verification records only
  secret fingerprints, locations, dates, and successful disposable restores;
  never secret bytes.
- The persistent timer fires hourly, but `backup.sh` performs a scheduled
  snapshot only when the last verified success is at least 12 hours old. A
  lock collision (`75`) or transient dump/copy/restic failure leaves the
  operation due so the next hourly firing retries automatically. Each
  database dump has a 30-minute deadline, each filesystem/capsule copy and
  `restic check` a one-hour deadline, remote backup/prune a two-hour deadline,
  and the service has a four-hour watchdog plus unconditional plaintext-stage
  cleanup. Tests advance a fake clock through collision, timeout, remote
  outage, retry, and recovery and prove a single failure cannot silently defer
  the next attempt for another day.

- [ ] Add fake-runtime failing tests for lock contention/retry, per-command and service-watchdog timeout, stuck child cleanup, missing/failed dump or copy, checksum failure, missing release capsule, repository-local target, unreadable/loose password file, accidentally included restic material, unavailable escrow copy, remote publication failure/recovery, and retention counts.
- [ ] Acquire the shared maintenance lock, enter the scheduled window, and stop Traefik, `telos-core`, Jellyfin, and Grimmory with checked exits; take consistent PostgreSQL/MariaDB dumps plus configuration/ACME/shared-storage/release-capsule snapshots under the absolute deadlines, restart and verify services, hash every component, and remove the mode-`0700` plaintext stage plus kill bounded children on success, signal, timeout, or failure.
- [ ] Verify `restic check`, publish only the complete set with stable `scheduled` plus release tags and one canonical host/path identity, and declare success only after the remote snapshot ID is returned.
- [ ] Implement `restic forget --tag scheduled --group-by host,paths --keep-daily 14 --keep-weekly 8 --prune`, the hourly persistent due/retry scheduler, backup-age/due/maintenance status, an ignored non-repository cache, and fingerprint-only escrow verification that proves both recovery copies can open and restore a disposable fixture repository.
- [ ] Run `sh scripts/tests/backup_test.sh`, `sh scripts/verify-recovery-escrow.sh --fixture`, and `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -count=1 -run 'TestBackupManifest' ./...`; expected result: concurrent maintenance retries automatically, hung/partial failures publish nothing and clean up, a recovered remote completes before the RPO threshold, restic material/plaintext remains absent, both escrow fixtures recover, and the same production grouping policy retains 14 daily and 8 weekly recoverable buckets with overlaps counted honestly.
- [ ] Commit with message `feat(ops): encrypt and rotate complete backups`.

## P3-T10: Make Restore Fail Closed and Measure Recovery

**Closes:** D07

**Files:**

- Create: `scripts/verify-restore.sh`
- Create: `scripts/restore-state.sh`
- Create: `scripts/restore-drill.sh`
- Create: `scripts/tests/restore_test.sh`
- Create: `documentation/operations/restore-drill.md`
- Modify: `backend/backup_manifest.go`
- Modify: `backend/backup_manifest_test.go`
- Modify: `scripts/restore.sh`
- Modify: `scripts/maintenance-lock.sh`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `documentation/operations/backup-and-restore.md`

**Restore order:** acquire the shared lock; fetch/decrypt and verify
checksums/release/schema/checksum set; restore and probe a candidate generation
in isolation; stop every writer; fsync the rollback journal; atomically activate
the candidate; invalidate sessions/invites; clear Redis; run migrations; reopen
internal services; pass functional probes; then reopen Traefik. Any failure
after activation automatically reactivates and probes the preserved prior
generation before returning.

Drill JSON records backup/restore timestamps, RPO/RTO seconds, release/checksum set, and probe results; it fails above 86,400-second RPO or 14,400-second RTO. For clean-node disaster recovery, the RTO clock starts when host-loss recovery is declared and ends only after automated firewall and A/AAAA cutover for the documented low-TTL DNS zone plus valid app/`turn.` certificates pass external HTTPS redirect/SNI/chain, authenticated WSS, range-media, voice-join, and TURN probes. The reference recovery environment and required DNS/provider credentials are explicit certification prerequisites; a same-host-only restore cannot satisfy the beta RTO claim.

- [ ] Add table-driven failure injection before/after every restore transition plus corrupt/incomplete data, future/incompatible schema, changed history, maintenance-lock contention, writer-stop, database/config/storage activation, Redis-clear, migration, probe, rollback, abrupt termination/re-entry, premature edge opening, stale backup, and slow-drill cases.
- [ ] Acquire the shared node lock and validate the complete remote snapshot/compatibility before changing active state; restore databases into generation-scoped temporary database names and config/storage into same-filesystem generation directories, verify them on an isolated internal Compose project, and never ignore a stop/copy/checksum/import status.
- [ ] Persist and fsync a transition journal, stop every writer, then atomically rename one mode-`0600` active-generation pointer consumed by Compose; recreate only internal services against the candidate, invalidate restored sessions/invites, clear Redis, run the locked migrator, and keep the previous database names/directories untouched until final acceptance.
- [ ] On any post-activation error or signal, automatically close the edge, swap the active-generation pointer back, recreate the prior internal services, and verify prior readiness before returning failure; after an untrappable exit, the next invocation must read the journal and complete rollback before accepting another operation. If rollback verification fails, keep Traefik closed and preserve both generations for operator recovery.
- [ ] Verify readiness, authentication, recent chat, one authorized Jellyfin item, one EPUB, and one PDF before opening Traefik; Phase 5 extends this same probe with notifications, annotations, My List, and Watch Party, and only a fully passing candidate permits old-generation cleanup.
- [ ] Run `sh scripts/tests/restore_test.sh --all-failure-points` and `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -count=1 -run 'TestBackupManifestCompatibility' ./...`; expected result: every preflight failure leaves the active generation untouched, every later failure automatically returns to the verified prior generation, and no test opens the edge early.
- [ ] Commit with message `feat(ops): verify restores and measure recovery objectives`.

## Phase 3 Exit Gate

- [ ] Empty, live-upgrade, concurrent, failed-SQL, gap, checksum-change, and future-version migration cases pass.
- [ ] Runtime can perform required DML but cannot perform DDL, ownership/grant changes, migration writes, query-statistics reads, or access the separately provisioned observer credential.
- [ ] Schema constraint and leading-index audits return no gap; the corrected permission descriptions/grants match product behavior.
- [ ] Authentication uses one aggregate query and bounded session-touch work; all configured database deadlines fire.
- [ ] Every list/upstream collection is inventory-registered and cursor-paginated or hard-capped; cursor scope/version/sort/filter/key rotation and sort-specific seek tuples are enforced.
- [ ] Every dynamic query shape is built from its route-specific allowlist; pagination/search plans are stable, bounded, indexed, and authorization-filtered.
- [ ] Redis failure/recovery and dispatcher concurrency prove durable outbox delivery for chat and every security/admin mutation.
- [ ] The retention matrix covers every current durable row/asset and deletion is repeatable with socket/session invalidation.
- [ ] Backup/restore tests prove encryption, off-node publication, out-of-band credential recovery, one shared node lock, 14 daily/8 weekly retention, compatibility/security invalidation, atomic activation, and automatic rollback at every injected failure.
- [ ] Go tests, `-race`, `go vet`, frontend affected tests, Compose config, and common master verification pass.
- [ ] Stop for human review; Phase 4 starts only from this accepted checksum set and begins with migration `0012`.
