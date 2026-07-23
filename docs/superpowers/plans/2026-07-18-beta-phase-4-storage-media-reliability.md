# Telos Beta Phase 4: Storage and Media Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make file ingestion, physical storage, malware scanning, quotas, media authorization, long/range-aware streaming, controlled egress, reconciliation, and readiness reliable enough for durable beta data.

**Architecture:** Represent every asset by purpose and lifecycle in PostgreSQL, confine physical I/O beneath trusted directory descriptors, stage/validate/scan before no-replace promotion, reconcile interrupted boundaries, authorize every upstream item against configured libraries, and route narrowly allowed metadata/signature traffic through a deny-by-default proxy.

**Tech Stack:** Go 1.26.5, PostgreSQL 16.14, Linux `openat2`/`renameat2`, ClamAV, Jellyfin, Grimmory, Traefik, Squid egress proxy, Redis/outbox, Podman/Docker Compose

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its focused commit.
- Begin only after the Phase 3 migration/checksum, role, outbox, deletion, and recovery gate is accepted.
- Reserve only `backend/db/migrations/0012_file_catalog_audit.sql` in this phase.
- Avatar input is limited to 5 MiB; normal file/book input is limited to 100 MiB before buffering.
- Uploads fail closed when validation, malware scanning, quota, free-space, database, sync, close, or promotion fails.
- No browser path, filename, MIME declaration, Jellyfin/Grimmory identifier, or upstream header is trusted.
- Production requires a Linux kernel with `openat2` and `renameat2(RENAME_NOREPLACE)` support; preflight fails rather than falling back to lexical confinement.
- General file listing/search/download exposes only authorized `shared_file`, `community`, `clean`, `available` records.
- Jellyfin and Grimmory remain isolated processes. Administrative endpoints/tokens never reach public routes.
- Production readiness is false if any advertised required service or storage invariant is unavailable.

## Entry Gate

- [ ] Phase 3 evidence identifies the exact starting commit and checksum set through `0011`.
- [ ] Runtime-role, migration, constraint/index, cursor/search, outbox, deletion, backup, and restore suites pass.
- [ ] Capture current ClamAV/Jellyfin/Grimmory/LiveKit reachability, mounts, free space, and false-green health behavior.
- [ ] Inventory current file rows and storage roots by purpose without exposing filenames or content in committed evidence.

## File Responsibility Map

| Area | Files |
|---|---|
| Catalog/audit | migration `0012`, `backend/files.go`, file store/integration tests |
| Safe physical I/O | `backend/storage.go`, `backend/content_validation.go`, `backend/epub.go` |
| Scanner/quota/upload/capacity | `backend/clamav.go`, `backend/quota.go`, `backend/capacity.go`, `backend/upload.go` |
| Reconciliation | `backend/reconciliation.go`, account lifecycle and audit call sites |
| Jellyfin/Grimmory | `backend/media.go`, `backend/library.go`, `backend/proxy.go` |
| Controlled egress | `deploy/egress-proxy/`, `config/egress/`, Compose/environment/docs |
| Honest health | `backend/health.go`, readiness integration tests, Compose health checks |

---

## P4-T1: Add a Purpose-Aware File Catalog and Durable Audit Schema

**Closes:** M03

**Files:**

- Create: `backend/db/migrations/0012_file_catalog_audit.sql`
- Create: `backend/files.go`
- Create: `backend/files_test.go`
- Create: `backend/files_integration_test.go`
- Modify: `backend/main.go`
- Modify: `backend/search.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`

**Interfaces:**

```go
type FilePurpose string
type FileVisibility string
type FileState string
type StorageArea string

type StoredFile struct {
    ID, Filename, SHA256, UploaderID string
    StorageKey, LogicalPath, MIMEType string
    Purpose       FilePurpose
    Visibility    FileVisibility
    State         FileState
    ScanStatus    string
    SizeBytes     int64
    CreatedAt     time.Time
}

type LogicalFolder struct {
    ID, ParentID, OwnerID, NormalizedName, DisplayName string
    Visibility FileVisibility
    CreatedAt  time.Time
}
```

States are `staged`, `validating`, `scanning`, `promoting`, `available`,
`handoff_pending`, `handed_off`, `quarantined`, `missing`, `deleting`,
`deleted`, and `consumed`. Every nonterminal ingestion state has a random lease
owner, 15-minute expiry renewed no more than once per minute on measurable
progress, attempt count, and last stable transition. Logical folders are
database-only nodes with normalized sibling uniqueness; physical storage
remains content-addressed and never mirrors user path names. Audit actions are
`create`, `promote`, `rename`, `move`, `download`, `delete`, `moderate`,
`handoff`, `quarantine`, `missing`, `reconcile`, and `consume`.

- [ ] Add failing catalog/list/search/download tests proving infected, avatar, book-ingestion, nonterminal/missing/deleted, and another user’s private rows can leak, plus folder-cycle/sibling-collision and expired-versus-live ingestion lease fixtures.
- [ ] Add storage-area/state/logical-parent/purpose/visibility constraints, virtual folders, unique physical keys, upload/capacity reservations, transition leases, book-handoff records, and immutable audit rows.
- [ ] Backfill known avatar/book/shared rows by deterministic existing references; quarantine ambiguous rows instead of guessing public visibility.
- [ ] Implement `FileStore.FindAvailable`, `ListVisible`, and `RecordAudit`; move listing/local file search from raw directory scans to purpose-aware cursor queries.
- [ ] Require every lookup to name the expected purpose and current viewer; extend the Phase 3 retention document plus lifecycle fixtures for catalog, folder, handoff, reservation, and audit behavior.
- [ ] Run `bash scripts/test-backend.sh storage -run 'Test(FileStore|FilePurposeFiltering|FileAuditSchema|LogicalFolderSchema|IngestionLeaseSchema|BookHandoffSchema)'`; expected result: only intended clean/available assets return, illegal transitions/cycles fail, and pages remain stable.
- [ ] Commit with message `feat(files): add purpose-aware durable catalog`.

## P4-T2: Confine and Atomically Promote Physical Assets

**Closes:** M01, M03

**Files:**

- Create: `backend/storage.go`
- Create: `backend/storage_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type StorageRef struct {
    Area StorageArea
    Key  string
}

func OpenConfinedStorage(roots map[StorageArea]string) (*ConfinedStorage, error)
func (s *ConfinedStorage) CreateExclusive(area StorageArea, key string, perm fs.FileMode) (*os.File, error)
func (s *ConfinedStorage) OpenRead(area StorageArea, key string) (*os.File, error)
func (s *ConfinedStorage) PromoteNoReplace(ctx context.Context, source, destination StorageRef, expectedSHA256 string) (PromotionResult, error)
func (s *ConfinedStorage) Remove(area StorageArea, key string) error
```

The Linux contract uses `openat2` with `RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS`, exclusive mode-`0600` staging, `renameat2(RENAME_NOREPLACE)`, verified cross-device copy, file/directory sync, and checked close/remove.

- [ ] Add failing traversal, intermediate/leaf symlink, magic-link, collision, cross-device, short-write, hash mismatch, sync, close, rename, and cancellation tests.
- [ ] Open trusted root descriptors once and resolve every sensitive operation relative to them; user strings never become absolute paths.
- [ ] Generate cryptographically random exclusive staging keys and propagate full-read/random/I/O failures.
- [ ] Implement no-replace same-filesystem promotion and copy-to-exclusive-temp/sync/no-replace fallback across filesystems; preserve original destinations on collision.
- [ ] Mount one controlled `/data/shared` root into core while retaining only required read-only/narrow mounts for Jellyfin and Grimmory.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race -count=10 -run 'Test(ConfinedStorage|PromoteNoReplace|StorageTraversal|StorageSymlink|StorageCrossDevice)' ./... && podman compose --env-file tests/fixtures/compose.env config --quiet`; expected result: every escape/failure is denied, the nonsecret fixture renders, and mounts remain private.
- [ ] Commit with message `feat(storage): confine and atomically promote assets`.

## P4-T3: Validate Content and EPUB Containers Within Explicit Bounds

**Closes:** M01

**Files:**

- Create: `backend/content_validation.go`
- Create: `backend/content_validation_test.go`
- Create: `backend/epub.go`
- Create: `backend/epub_test.go`

**Interfaces:**

```go
type EPUBLimits struct {
    MaxEntries          int
    MaxUncompressedBytes int64
    MaxEntryBytes       int64
    MaxCompressionRatio float64
}

func ValidateEPUB(r io.ReaderAt, size int64, limits EPUBLimits) error
func ValidateUploadContent(filename string, purpose FilePurpose, r io.ReaderAt, size int64) (ContentInfo, error)
```

EPUB defaults are 10,000 entries, 500 MiB expanded total, 100 MiB per entry, and 100:1 ratio. The first entry must be uncompressed `mimetype` with exact EPUB media type; container/rootfile/package paths are bounded and confined.

- [ ] Build failing fixtures for compressed/wrong/reordered mimetype, missing/oversized XML, missing rootfile/package, traversal, duplicates, encryption, ZIP64 abuse, excessive entries, and expansion/ratio limits.
- [ ] Preflight central-directory metadata before entry reads and use bounded XML decoding with external entities disabled.
- [ ] Validate confined rootfile path and present package document; return stable categories without archive internals.
- [ ] Add PDF header/trailer and declared-format consistency checks; general shared files still require bounded MIME detection and malware scan rather than a broad unsafe extension trust.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -run 'Test(ValidateEPUB|ValidateUploadContent)' ./...`; expected result: valid EPUB/PDF fixtures pass and all malformed/bomb fixtures fail before promotion.
- [ ] Commit with message `feat(files): validate EPUB containers and content`.

## P4-T4: Add Deadline-Bounded ClamAV Scanning and Freshness

**Closes:** M02

**Files:**

- Create: `backend/clamav.go`
- Create: `backend/clamav_test.go`
- Create: `config/clamav/clamd.conf`
- Modify: `.env.example`
- Modify: `docker-compose.yml`

**Interfaces:**

```go
type MalwareScanner interface {
    Scan(ctx context.Context, r io.Reader) (ScanResult, error)
    Health(ctx context.Context) (ClamAVHealth, error)
}
```

Defaults are connect 3 seconds, renewed idle read/write 15 seconds, total scan 120 seconds, response cap 4 KiB, and maximum signature age 48 hours.

- The checked-in clamd profile sets `StreamMaxLength 110M`,
  `MaxFileSize 110M`, `MaxScanSize 550M`, `MaxFiles 12000`,
  `MaxRecursion 32`, `MaxScanTime 120000`, and `AlertExceedsMax yes`. These
  values completely cover the 100 MiB upload and the permitted 500 MiB/10,000
  entry EPUB expansion. Startup parses `clamconf` output and refuses a profile
  below any floor. Only an explicit terminal `OK` is clean; limit, truncation,
  timeout, `FOUND`, `ERROR`, or unknown replies fail closed.

- [ ] Add fake-clamd failing tests for connect/read/write stalls, partial writes, oversized/malformed/truncated response, infected result, every configured limit, below-floor rendered config, stale/unknown signature time, cancellation, and filename/payload log leakage.
- [ ] Implement context-aware INSTREAM with renewed deadlines, exact framing, bounded response parsing, and stable clean/infected/error categories; accept only a complete explicit `OK`.
- [ ] Add version/signature freshness probe and make scanner unavailable/stale a fail-closed upload condition.
- [ ] Mount the checked-in complete-scan profile and persistent signature storage, verify effective values with `clamconf`, and add an internal-only container health check that validates PING plus freshness rather than process existence.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -count=1 -run 'Test(ClamAV|ClamdConfig)' ./...` and `bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env --checks clamav`; expected result: stalls/limits/infected/stale cases fail, effective limits meet every floor, ClamAV has no published port, and no rendered secret value is printed.
- [ ] Commit with message `feat(files): enforce bounded fresh malware scans`.

## P4-T5: Enforce Per-User Quotas and Node Free-Space Reserve

**Closes:** M02

**Files:**

- Create: `backend/quota.go`
- Create: `backend/quota_test.go`
- Create: `backend/quota_integration_test.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type QuotaPolicy struct {
    PerUserPhysicalBytes int64
    NodeReserveBytes int64
    ReservationTTL time.Duration
}

func (g *QuotaGuard) Reserve(ctx context.Context, userID string, finalBytes, peakTemporaryBytes int64) (*QuotaReservation, error)
```

Beta defaults are 1 GiB per user, 5 GiB node reserve, and 15-minute reservations; operators may raise them, but a zero/unparseable production value is invalid.

- Charge physical bytes, not only visible/logical assets: every user-owned
  `staged`, validating, scanning, promoting, available, handoff-pending,
  handed-off, quarantined, missing-on-disk metadata pending resolution, and
  deleting asset remains charged until verified physical removal or verified
  Grimmory consumption; ownership handoff alone does not waive bytes still in
  the bookdrop. Replacements charge old and new copies concurrently;
  cross-device promotion reserves source plus destination temporary/final peak.
  Node admission also subtracts unowned quarantine/orphan bytes and all live
  reservations from current `statfs` free space.

- [ ] Add failing exact-boundary, signed/unsigned overflow, every lifecycle state, replacement/cross-device double occupancy, orphan/quarantine charge, concurrent reservation, expiry, repeated commit/release, changing-free-space, and reserve-breach tests.
- [ ] Parse and Compose-wire nonzero quota/reserve/TTL settings, sum all user-owned physical lifecycle bytes plus live reservations under a database transaction/advisory lock, and account for unowned orphan/quarantine bytes in the node reserve without double-counting one inode/storage key.
- [ ] Check `statfs` and reservation-adjusted free bytes before staging and promotion; reserve the endpoint maximum when `Content-Length` is absent and otherwise the larger of declared/observed final bytes plus worst-case temporary duplication, without reading past the endpoint limit.
- [ ] Implement idempotent commit/release and bounded expired-reservation cleanup; emit quota/rejection metrics without filenames.
- [ ] Run `bash scripts/test-backend.sh storage -race -count=10 -run 'Test(QuotaGuard|QuotaPhysicalLifecycle|QuotaCrossDevicePeak)'`; expected result: concurrent requests/replacements cannot overbook user or node physical capacity and pathological sizes fail.
- [ ] Commit with message `feat(storage): reserve quotas and free space`.

## P4-T6: Build One Transactional Upload Pipeline

**Closes:** M01, M02, M03

**Files:**

- Create: `backend/capacity.go`
- Create: `backend/capacity_test.go`
- Create: `backend/upload.go`
- Create: `backend/upload_test.go`
- Create: `backend/upload_integration_test.go`
- Create: `scripts/tests/upload_ingress_test.sh`
- Modify: `backend/files.go`
- Modify: `backend/settings.go`
- Modify: `backend/main.go`
- Modify: `.env.example`
- Modify: `docker-compose.yml`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type UploadIntent struct {
    UserID          string
    Filename        string
    Purpose         FilePurpose
    Visibility      FileVisibility
    LogicalDirectory string
    DeclaredBytes   int64
    MaxBytes        int64
}

func (s *UploadService) Store(ctx context.Context, src io.Reader, intent UploadIntent) (StoredFile, error)

type CapacityLease interface {
    Renew(ctx context.Context) error
    Release(ctx context.Context) error
}

func (l *CapacityLimiter) Acquire(ctx context.Context, class, userID string) (CapacityLease, error)
```

Pipeline order is acquire capacity, reserve physical bytes, create a
database-backed staging lease, exclusive stage/hash through the P2 renewable
15-second body idle deadline, validate, acquire scan capacity, scan, record
`promoting` plus audit, no-replace promote, mark `available`, and commit quota.
Lease heartbeat and every state transition are transactional; cancellation
releases capacity and quota but retains an actionable expired lifecycle row when
physical cleanup cannot be proved.

Capacity defaults are four active uploads globally/two per user, two active
malware scans globally, and (for P4-T9) 20 long streams globally/four per user.
One Redis Lua operation atomically checks global/user counts and creates an
opaque expiring lease before allocating a body, file, scanner, upstream
connection, or goroutine. Leases expire after 45 seconds and renew at most every
15 seconds while measurable progress is made; release is idempotent. Redis
failure rejects admission, and a bounded local semaphore also caps work per
process.

- [ ] Add barrier-synchronized failing tests for capacity oversubscription/expiry/renew/release/Redis loss, concurrent name, unauthorized/over-capacity chunked uploads causing no Traefik or multipart temp body, stalled/trickled/over-limit body, invalid EPUB/PDF, infected/stale scanner, quota/free-space loss, copy/close/sync/promotion collision, disconnect, and database-after-promotion.
- [ ] Implement atomic expiring capacity leases and bounded local semaphores; authenticate and acquire upload capacity before reading multipart data, acquire scan capacity before opening clamd, renew only on measurable progress, and return `401/403/503` without work allocation when authorization/Redis/admission is unavailable.
- [ ] Share the leased pipeline across normal files, books, and avatars with explicit purpose/visibility, durable UUIDs, a renewable 15-second body idle-read deadline, endpoint size limits enforced before multipart buffering, and the Phase 2 streaming upload router with route-wide Traefik buffering removed.
- [ ] Keep every non-clean/non-available, infected, avatar, and book-ingestion row out of general listing/search/download; expose no storage key, lease owner, or scanner detail to clients.
- [ ] Transactionally record/heartbeat the staging lease and actionable lifecycle/audit state across every database/filesystem boundary so P4-T7 distinguishes live work from expired interruption and can reconcile a failure after promotion.
- [ ] Run `bash scripts/test-backend.sh storage -race -count=10 -run 'Test(CapacityLimiter|UploadService|UploadPreAdmission|UploadBodyIdleDeadline|UploadVisibility|UploadLease)' && sh scripts/tests/upload_ingress_test.sh`; expected result: limits never oversubscribe, rejected uploads allocate no edge/core temp body, stalled bodies release capacity, nothing overwrites, only clean promoted rows appear, and every injected interruption is recoverable.
- [ ] Commit with message `feat(files): make uploads transactional and atomic`.

## P4-T7: Audit, Delete, and Reconcile Database/Filesystem State

**Closes:** M03

**Files:**

- Create: `backend/file_mutations.go`
- Create: `backend/file_mutations_test.go`
- Create: `backend/reconciliation.go`
- Create: `backend/reconciliation_test.go`
- Create: `backend/reconciliation_integration_test.go`
- Modify: `backend/files.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/library.go`
- Modify: `backend/main.go`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type ReconcileReport struct {
    Missing, Orphaned, Quarantined int
    Repaired, Deleted, Consumed    int
    Scanned, Deferred              int
    NextCursor                     string
}

func (r *FileReconciler) RunOnce(ctx context.Context) (ReconcileReport, error)
func (s *FileStore) ListAudit(ctx context.Context, page PageRequest) (Page[FileAuditEvent], error)
func (s *FileService) CreateFolder(ctx context.Context, actor *UserContext, parentID, name string) (LogicalFolder, error)
func (s *FileService) RenameFile(ctx context.Context, actor *UserContext, fileID, name string) error
func (s *FileService) MoveFile(ctx context.Context, actor *UserContext, fileID, folderID string) error
func (s *FileService) RenameFolder(ctx context.Context, actor *UserContext, folderID, name string) error
func (s *FileService) MoveFolder(ctx context.Context, actor *UserContext, folderID, parentID string) error
func (s *FileService) DeleteFile(ctx context.Context, actor *UserContext, fileID string) error
func (s *FileService) DeleteFolder(ctx context.Context, actor *UserContext, folderID string) error
```

File rename/move changes only the authorized database logical parent/name under
row locks; a physical content key never changes. Folder create/rename/move
locks source/destination ancestry, rejects cycles and normalized sibling
collisions, and deletes only an empty folder (`409 folder_not_empty`). File
delete atomically changes `available -> deleting`, hides it, and enqueues the
idempotent hash/key-verified asset job; only verified physical absence permits
`deleted`. Retried operations return the same stable result.

The reconciler claims at most 100 expired lifecycle rows with `FOR UPDATE SKIP
LOCKED`, scans at most 1,000 directory entries or 30 seconds per run, persists
an opaque root cursor, and permits only one root walker per node. It never
mutates a live lease; an orphan must be older than twice the 15-minute lease.
Two reconcilers and an active uploader may race without taking over, deleting,
or quarantining live work.

For Grimmory ingestion, Telos owns the staged input through fsync and
no-replace promotion into the dedicated bookdrop. It records
`handoff_pending` before promotion and `handed_off` with hash/size after the
directory sync; only then does Grimmory own and consume the drop file. Absence
becomes `consumed` only after a bounded Grimmory catalog lookup identifies the
imported book and stores its ID. Unmatched absence becomes `missing` and an
operator alert, never assumed success; retry/collision cannot duplicate an
import or delete either owner's copy.

- [ ] Add barrier-synchronized failing fixtures for file/folder rename/move/delete authorization, cycles/collisions/nonempty folders, missing/orphan/live-versus-expired stage, half-promotion, hash mismatch, repeated deletion, every Grimmory handoff interruption/duplicate/consumption outcome, symlinks, two reconcilers, concurrent upload heartbeat, root-scan bounds, and cursor overflow.
- [ ] Implement the row-locked virtual folder/file mutation state machine and idempotent deletion jobs; require resource ownership or `manage_files` (and `manage_files` for shared-root structure), preserve immutable physical keys on logical moves, hide `deleting` immediately, and mark `deleted` only after verified removal.
- [ ] Claim only expired lifecycle leases in bounded batches and walk only descriptor-confined roots under one per-node walker lock/cursor; quarantine old unknown regular files, reject symlinks, mark missing rows unavailable, and repair promotion only when size/hash/destination prove identity.
- [ ] Implement the journaled Telos-to-Grimmory ownership handoff and bounded catalog acknowledgement; distinguish verified consumption from unexplained loss and make every retry/collision transition idempotent.
- [ ] Write durable audit rows for create/promote/rename/move/download/delete/moderate/handoff/quarantine/missing/reconcile/consume with actor/request correlation and no secret/storage path; startup plus periodic runs must converge with no new mutation/audit on a second identical pass.
- [ ] Run `bash scripts/test-backend.sh storage -race -count=10 -run 'Test(FileMutation|FileReconciler|ReconcilerUploaderRace|BookHandoff|FileAudit)'`; expected result: logical operations are authorized/idempotent, live work is untouched, each run stays bounded, ownership is unambiguous, and audit pagination is stable.
- [ ] Commit with message `feat(storage): audit and reconcile file state`.

## P4-T8: Authorize Every Jellyfin and Grimmory Item

**Closes:** M04

**Files:**

- Create: `backend/media_test.go`
- Create: `backend/library_authorization_test.go`
- Modify: `backend/media.go`
- Modify: `backend/library.go`
- Modify: `backend/main.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces:**

```go
type AuthorizedMediaItem struct {
    ID, LibraryID, Name, MediaType string
    IsFolder bool
}

func NewJellyfinAuthorizer(client *JellyfinClient, allowedLibraryIDs []string) (*JellyfinAuthorizer, error)
func (a *JellyfinAuthorizer) AuthorizeItem(ctx context.Context, itemID string) (AuthorizedMediaItem, error)
func (a *JellyfinAuthorizer) AuthorizeItems(ctx context.Context, itemIDs []string) (map[string]AuthorizedMediaItem, error)

type BookAction string

const (
    BookRead   BookAction = "read"
    BookManage BookAction = "manage"
)

type AuthorizedBook struct {
    ID        int64
    LibraryID string
    Title     string
    Format    string
}

func NewGrimmoryAuthorizer(client *GrimmoryClient, allowedLibraryIDs []string) (*GrimmoryAuthorizer, error)
func (a *GrimmoryAuthorizer) AuthorizeBook(ctx context.Context, user *UserContext, bookID string, action BookAction) (AuthorizedBook, error)
```

Production requires nonempty comma-separated exact `JELLYFIN_LIBRARY_IDS` and
`GRIMMORY_LIBRARY_IDS`, both using stable upstream IDs rather than display
names. The Grimmory adapter resolves each book's library ID from the bounded
library/detail APIs even if its book DTO exposes only a name, and fails closed
when the mapping is absent or ambiguous. `BookRead` requires `view_library`;
`BookManage` requires both view and `manage_library`. Phase 5 reader, progress,
annotation, annotation-reply, and notification lookups must call
`AuthorizeBook(BookRead)`; metadata, cover, and delete routes call
`AuthorizeBook(BookManage)`. Positive membership caches are at most 30 seconds,
negative caches at most 5 seconds, and both invalidate on upstream library
refresh, mutation, or config change.

- [ ] Add fake-Jellyfin/Grimmory failing tests proving valid item IDs outside configured stable libraries, ambiguous/missing Grimmory name-to-ID mappings, revoked Telos permission, or manage-with-read-only access can reach lists, metadata, covers, content, HLS/search, progress, or admin-token paths; assert a 50-item My List authorization uses one bounded batch rather than N+1 upstream calls.
- [ ] Parse and Compose-wire nonempty stable `JELLYFIN_LIBRARY_IDS`/`GRIMMORY_LIBRARY_IDS`, implement bounded current-library/ancestor resolution plus positive/negative caches and a deduplicated maximum-50 Jellyfin batch authorizer, parse canonical item/book IDs, require `view_library`/`manage_library` as specified, invalidate on refresh/mutation/config, and fail closed on upstream ambiguity.
- [ ] Authorize before every item/child/cover/metadata/audio/video/HLS/search and book/detail/cover/content/progress/metadata/delete request; fetch no unauthorized binary and return indistinguishable `404` for out-of-scope IDs.
- [ ] Restrict both searches to configured parent libraries with `Limit=15`, deduplicate, cap Grimmory decode per P3-T6, and remove query-string admin tokens; inject upstream credentials only server-side.
- [ ] Publish `AuthorizeItems` for paged My List, `AuthorizeItem` for insertion/playback/Watch Party, and `GrimmoryAuthorizer.AuthorizeBook` for reader/progress/annotation gates.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race -count=1 -run 'Test(JellyfinAuthorizer|GrimmoryAuthorizer|MediaAuthorization|LibraryAuthorization)' ./...`; expected result: only allowed items/actions succeed and no unauthorized binary or upstream credential reaches the browser.
- [ ] Commit with message `feat(media): authorize Jellyfin and Grimmory items`.

## P4-T9: Preserve Safe Range Semantics and Long Streams

**Closes:** M04, M05

**Files:**

- Create: `backend/hls.go`
- Create: `backend/hls_test.go`
- Modify: `backend/capacity.go`
- Modify: `backend/proxy.go`
- Modify: `backend/proxy_test.go`
- Modify: `backend/media.go`
- Modify: `backend/library.go`
- Modify: `backend/main.go`
- Modify: `backend/library_test.go`
- Modify: `.env.example`
- Modify: `docker-compose.yml`

**Interfaces:** Extend Phase 2 proxy policy with a 10-second response-header
timeout, separately renewed 30-second upstream idle-read and downstream
idle-write deadlines, optional byte cap, and the P4-T6 long-stream lease.
Request allowlist is `Accept`, `Range`, `If-Range`, `If-None-Match`,
`If-Modified-Since`; response allowlist is the master-plan media list.

HLS manifests are valid UTF-8, at most 1 MiB/10,000 lines, with URI lines and
`URI=` attributes at most 4 KiB. Parse media/master playlists and every segment,
variant, `EXT-X-KEY`, `EXT-X-MAP`, `EXT-X-MEDIA`, and iframe URI. Resolve only
relative or exact configured-Jellyfin-origin HTTP(S) paths beneath the
authorized item's HLS prefix; reject userinfo, fragments, IP/alternate hosts,
non-HTTP schemes, traversal, protocol-relative paths, and unknown URI-bearing
tags. Strip upstream tokens and rewrite each accepted URI to a two-minute
HMAC-authenticated opaque Telos resource route bound to item/user/session,
resource path, and expiry; reauthorize the item when serving it.
`TELOS_HLS_SIGNING_KEY_FILE` is a dedicated mode-`0600` secret with at least 32
random bytes and is never shared with session or cursor keys.

- [ ] Add failing credential/header stripping, cookie/CORS/server stripping, `200/206/304`, ranges/conditionals, independent upstream-read/downstream-write stalls, cancellation/body-close, stream-capacity races/expiry, oversized/malformed HLS, every URI-bearing tag, traversal/absolute-host/scheme/token smuggling, locator tamper/expiry/cross-user replay, and former 60/120-second timeout tests.
- [ ] Construct fresh upstream requests and inject credentials only server-side; apply separate cover, media, HLS, and book policies.
- [ ] Acquire the atomic global/per-user long-stream lease before any binary upstream request; renew on successful transfer progress, release on every status/cancel/error, reject admission before opening upstream work, and forward ranges/conditionals only on supported paths without buffering.
- [ ] Parse and rewrite bounded HLS manifests to opaque bound Telos locators, reauthorize each locator, and reject any URI that cannot be proved within the authorized item; no upstream hostname, path credential, admin token, or arbitrary URL reaches the browser.
- [ ] Remove Grimmory’s total two-minute deadline and `200`-only handling; retain bounded connect/header plus separately renewable upstream-read/downstream-write deadlines and propagate valid `200/206/304` range/cache headers.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race -count=10 -run 'Test(Proxy|Range|StreamCapacity|StreamIdleDeadline|HLS|LibraryContent|MediaStream)' ./...`; expected result: capacity never oversubscribes, `206/304` survive, all HLS URLs remain same-origin/authorized, credentials/cookies do not cross, active streams outlive old totals, and stalled/cancelled bodies close.
- [ ] Commit with message `fix(proxy): preserve safe ranges and long streams`.

## P4-T10: Constrain Metadata and Signature Egress

**Closes:** M06

**Files:**

- Create: `deploy/egress-proxy/Dockerfile`
- Create: `config/egress/squid.conf`
- Create: `config/egress/allowed-domains.txt`
- Create: `scripts/tests/egress_test.sh`
- Create: `documentation/operations/controlled-egress.md`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `backend/library.go`
- Modify: `documentation/product/beta-feature-status.md`

**Contract:** Only `telos-egress-proxy` joins the non-internal `telos-egress` network. Initial reviewed destinations are `database.clamav.net`, `www.googleapis.com`, `openlibrary.org`, `covers.openlibrary.org`, `api.themoviedb.org`, `image.tmdb.org`, and `www.omdbapi.com`; IP-literal CONNECT, private/link-local/CGNAT/multicast destinations after DNS resolution, and unlisted hosts/ports are denied.

- [ ] Add failing topology/config/runtime tests showing signature/metadata paths currently have either no route or unrestricted destination potential.
- [ ] Keep Jellyfin, Grimmory, and ClamAV on internal networks and configure FreshClam/JVM/.NET/HTTP clients to use the dual-homed proxy as their only Internet path.
- [ ] Allow destination TCP 443 and only provider-required TCP 80; deny IP literals, internal/private/link-local/CGNAT/multicast or mixed resolved answers, alternate ports, unsafe methods, proxy administration, and access from public networks. Pin each connection to one validated address while preserving Host/SNI, verify the connected peer, and repeat resolution/validation/pinning for every redirect so neither the proxy nor its client can re-resolve after approval.
- [ ] Route candidate-cover downloads through the same hostname allowlist while retaining DNS-result/redirect SSRF validation.
- [ ] Document expected external traffic/privacy, fail-closed enrichment behavior, access-log redaction/retention, and the operator review/test process for adding a domain.
- [ ] Run `sh scripts/tests/egress_test.sh` and `podman compose --env-file tests/fixtures/compose.env config --quiet`; expected result: reviewed fixtures pass through the pinned-address proxy, rebinding/mixed/unlisted/IP/direct routes fail, the nonsecret fixture renders, and no upstream admin port publishes.
- [ ] Commit with message `feat(network): constrain metadata and signature egress`.

## P4-T11: Report Complete Liveness and Production Readiness

**Closes:** M02, M06, O03

**Files:**

- Create: `backend/health.go`
- Create: `backend/health_test.go`
- Create: `backend/health_integration_test.go`
- Create: `scripts/tests/readiness_stack_test.sh`
- Modify: `backend/main.go`
- Modify: `docker-compose.yml`
- Modify: `documentation/architecture/02-deployment.md`

**Interfaces:**

```go
type HealthChecker interface {
    Name() string
    Check(ctx context.Context) HealthCheck
}

func (s *HealthService) Liveness() HealthReport
func (s *HealthService) Readiness(ctx context.Context) HealthReport
```

Routes are `/api/v1/health/live`, `/api/v1/health/ready`, with `/api/v1/health` aliasing readiness. Production readiness requires PostgreSQL/connectivity/migrations, Redis, fresh ClamAV, Jellyfin, Grimmory, LiveKit, required mounts/writability/free reserve, and acceptable outbox backlog. Outbox warns above 30 seconds/100 pending and fails above 2 minutes/1,000.

- Liveness is allocation-free/O(1) and performs no network, database, disk walk,
  or `statfs`. Readiness runs each cheap dependency probe with a two-second
  child timeout and a three-second overall timeout, caches the immutable
  sanitized report for five seconds, and coalesces all concurrent misses into
  one internal probe detached from caller cancellation but bounded by that
  overall timeout. Mount checks use pre-opened sentinels plus `statfs`; they
  never walk or reconcile storage. A cache older than 15 seconds is unusable.

- [ ] Add failing checker tests for every dependency, timeout, 500 concurrent requests, caller cancellation, cache expiry/staleness, raw-error redaction, stale signature, migration mismatch, missing/read-only mount, reserve breach, outbox threshold, and accidental disk-tree/list/upstream-catalog work.
- [ ] Implement independently timed cheap checks, immutable five-second cached reports, and one coalesced in-flight probe returning stable public codes/bounded numeric metrics only; no hostname, path, query, credential, raw error, tree walk, catalog listing, or per-request goroutine fan-out.
- [ ] Make liveness report process/event-loop state only; make any required production dependency/storage failure return readiness `503`.
- [ ] Point core Compose health checks at readiness with a 10-second interval/five-second timeout and include service-level Clam/Jellyfin/Grimmory/LiveKit probes that exercise cheap useful APIs without exposing credentials.
- [ ] Run `bash scripts/test-backend.sh storage -race -count=10 -run 'Test(HealthReadiness|HealthDependencyMatrix|HealthCoalescing|HealthCache|LivenessIsCheap)'`; expected result: 500 concurrent misses invoke each dependency once, every loss maps to the documented code/status, and all requests complete within deadline.
- [ ] Run `sh scripts/tests/readiness_stack_test.sh` and `bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env --checks readiness`; expected result: the script boots the stack, stops/restarts PostgreSQL, Redis, ClamAV, Jellyfin, Grimmory, and LiveKit one at a time, observes `503 -> 200`, detects no false-green/probe storm, and prints no rendered secret value.
- [ ] Commit with message `feat(health): report complete dependency readiness`.

## Phase 4 Exit Gate

- [ ] Migration `0012` passes clean install/live upgrade/checksum verification and its backfill quarantines ambiguity.
- [ ] Catalog/list/search/download never expose wrong-purpose, non-clean, unavailable, or unauthorized assets.
- [ ] Path traversal/symlinks/collisions/cross-device/error injection cannot escape, overwrite, or silently corrupt.
- [ ] Valid EPUB/PDF fixtures pass; malformed, encrypted, traversal, and expansion-bomb fixtures fail before promotion.
- [ ] ClamAV is deadline-bounded/signature-fresh, its effective complete-scan limits cover every accepted upload/container, and every non-`OK` outcome fails closed.
- [ ] Physical-byte quota plus atomic upload/scan/stream leases cannot overbook, overwrite, publish partial state, or hide an expired interruption.
- [ ] Bounded reconciliation, ordinary file/folder mutation, audit/deletion, and Telos-to-Grimmory ownership handoff are race-safe and idempotent.
- [ ] Every Jellyfin and Grimmory operation uses its concrete library authorizer; binary paths preserve safe ranges/conditionals/long streams and HLS exposes only rewritten authorized Telos URLs.
- [ ] Controlled egress permits only reviewed provider destinations and no upstream admin interface is public.
- [ ] Coalesced cheap readiness has no false-green state or probe storm when dependencies fail one at a time and exposes no raw infrastructure errors.
- [ ] Go unit/integration/race/vet, Compose, egress, backup/restore regression, frontend media/library E2E, and master common verification pass.
- [ ] Stop for human review; Phase 5 starts only from this accepted commit and begins migration `0013`.
