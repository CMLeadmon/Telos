# Member Experience Phase 1: Catalog Identity and Continuity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce stable Telos catalog IDs and minimal per-member continuity while preserving every existing Library, Stream, commentary, and chat flow.

**Architecture:** Add an additive PostgreSQL identity/source schema, a focused catalog repository, and a single continuity repository. Existing Grimmory and Jellyfin handlers observe upstream records and resolve public UUIDs before authorization or proxying; a compatibility backfill and legacy resolver keep old numeric/hex links valid.

**Tech Stack:** Go 1.26.5, PostgreSQL 16 with pgx v5, Redis 7, Next.js/React 19, TypeScript, Zustand, Vitest, Playwright, Podman.

## Global Constraints

- Secrets come only from `.env` interpolation; never log or persist provider credentials.
- Jellyfin and Grimmory remain separate processes integrated only through narrow HTTP requests; never link their code into `telos-core`.
- The browser calls only `telos-core`; provider base URLs, tokens, paths, cookies, redirects, and arbitrary queries never cross the gateway.
- Stable identity does not replace provider-library authorization. Resolve first, then authorize the active upstream source against `JELLYFIN_LIBRARY_IDS` or `GRIMMORY_LIBRARY_IDS`.
- Store only the latest locator/position and completion state; do not create a playback-event history.
- Migrations are additive in this phase. Keep `book_progress` until the compatibility backfill has shipped and been verified in a later release.
- Preserve Library, Stream, Chat, and Settings as the only top-level surfaces.
- Do not modify or stage unrelated worktree changes.

---

## File structure

- Create `backend/db/migrations/0022_catalog_identity_and_progress.sql` — additive catalog/source/progress schema.
- Create `backend/catalog.go` — catalog value types, validation, repository, observation, and resolution.
- Create `backend/catalog_test.go` — pure catalog validation and fake-repository tests.
- Create `backend/catalog_integration_test.go` — PostgreSQL identity, alias, and source-state tests.
- Modify `backend/metrics.go` and `backend/metrics_test.go` — bounded catalog reconciliation counts and durations.
- Create `backend/continuity.go` — format-aware progress validation and repository operations.
- Create `backend/continuity_test.go` — pure progress validation tests.
- Create `backend/continuity_integration_test.go` — member isolation, upsert, Continue ordering, and legacy backfill tests.
- Create `backend/catalog_backfill.go` — idempotent progress/commentary/chat reference backfill.
- Create `backend/catalog_backfill_integration_test.go` — mixed legacy/canonical fixture tests.
- Modify `backend/library.go` — observe Grimmory books, resolve canonical book IDs, and dual-read/dual-write progress during compatibility.
- Modify `backend/main.go` — initialize repositories; canonicalize existing Jellyfin views/items and resolve media/stream routes.
- Modify `backend/media.go` — authorize resolved Jellyfin sources rather than browser IDs.
- Modify `backend/annotation_handlers.go` — canonicalize targets before authorization/query.
- Modify `backend/annotations.go` — require canonical target IDs for new rows.
- Modify `frontend/src/lib/api.ts` — accept string Library IDs.
- Modify `frontend/src/stores/useLibraryStore.ts` — change `LibraryBook.id` to string.
- Modify `frontend/src/stores/useMediaStore.ts` — retain string IDs while treating them as canonical opaque values.
- Modify focused frontend tests and E2E fixtures that assume numeric book IDs.
- Modify `documentation/operations/release-inputs.pathspec` — inventory all new runtime Go and migration inputs.

### Task 1: Add the additive catalog and continuity schema

**Files:**
- Create: `backend/db/migrations/0022_catalog_identity_and_progress.sql`
- Modify: `backend/migrations_test.go`
- Modify: `backend/testutil/integration.go`
- Test: `backend/migrations_integration_test.go`

**Interfaces:**
- Produces: `catalog_items`, `catalog_sources`, `member_progress`, `uq_catalog_sources_active`, `idx_member_progress_continue`.
- Preserves: `book_progress`, `annotations.target_id`, and `messages.embed_ref` for runtime backfill.

- [ ] **Step 1: Write the failing migration inventory test**

Add an assertion that the embedded real migration set contains version 22 with
the expected name; `DiscoverMigrations` already proves the whole set remains
contiguous, and later phases may append versions:

```go
func TestRepositoryMigrationsContainCatalogIdentity(t *testing.T) {
    ms, err := DiscoverMigrations(embeddedMigrations, "db/migrations")
    if err != nil {
        t.Fatal(err)
    }
    got := ms[21]
    if got.Version != 22 || got.Name != "catalog_identity_and_progress" {
        t.Fatalf("migration 22 = %+v", got)
    }
}
```

- [ ] **Step 2: Run the focused test and confirm the missing migration failure**

Run:

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestRepositoryMigrationsContainCatalogIdentity
```

Expected: FAIL because migration 0022 does not exist.

- [ ] **Step 3: Create the complete additive migration**

Use the exact schema below, including foreign-key indexes required by the
database invariant suite:

```sql
CREATE TABLE IF NOT EXISTS catalog_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    surface TEXT NOT NULL,
    kind TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_catalog_surface CHECK (surface IN ('library','stream')),
    CONSTRAINT chk_catalog_kind CHECK (kind IN ('epub','pdf','audiobook','video','audio','folder'))
);

CREATE TABLE IF NOT EXISTS catalog_sources (
    catalog_item_id UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_id TEXT NOT NULL,
    upstream_library_id TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    available BOOLEAN NOT NULL DEFAULT true,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    missing_since TIMESTAMPTZ,
    CONSTRAINT chk_catalog_provider CHECK (provider IN ('grimmory','jellyfin')),
    CONSTRAINT uq_catalog_provider_upstream UNIQUE (provider, upstream_id),
    CONSTRAINT uq_catalog_item_source UNIQUE (catalog_item_id, provider, upstream_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_sources_active
    ON catalog_sources (catalog_item_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_catalog_sources_item
    ON catalog_sources (catalog_item_id);
CREATE INDEX IF NOT EXISTS idx_catalog_sources_library
    ON catalog_sources (provider, upstream_library_id, active);

CREATE TABLE IF NOT EXISTS member_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    locator JSONB NOT NULL DEFAULT '{}',
    position_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    percent REAL NOT NULL DEFAULT 0,
    completed BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, catalog_item_id),
    CONSTRAINT chk_member_progress_locator CHECK (jsonb_typeof(locator) = 'object'),
    CONSTRAINT chk_member_progress_position CHECK (position_ms >= 0),
    CONSTRAINT chk_member_progress_duration CHECK (duration_ms >= 0),
    CONSTRAINT chk_member_progress_percent CHECK (percent >= 0 AND percent <= 1)
);

CREATE INDEX IF NOT EXISTS idx_member_progress_item
    ON member_progress (catalog_item_id);
CREATE INDEX IF NOT EXISTS idx_member_progress_continue
    ON member_progress (user_id, updated_at DESC, catalog_item_id)
    WHERE completed = false;
```

- [ ] **Step 4: Extend integration reset and invariant fixtures**

Add `member_progress`, `catalog_sources`, and `catalog_items` to
`testutil.Fixture.Reset` in dependency order. Add integration assertions that a
second active source for one item fails, two inactive aliases succeed, and
deleting a user cascades only that user's progress.

- [ ] **Step 5: Run migration and database invariant tests**

Run:

```bash
scripts/test-backend.sh all -run 'TestRepositoryMigrationsContainCatalogIdentity|TestMigration|TestDatabaseInvariant'
```

Expected: PASS; the integration fixture applies versions 0001 through 0022.

- [ ] **Step 6: Commit the schema slice**

```bash
git add backend/db/migrations/0022_catalog_identity_and_progress.sql backend/migrations_test.go backend/migrations_integration_test.go backend/testutil/integration.go
git commit -m "feat: add stable catalog identity schema"
```

### Task 2: Implement catalog observation and resolution

**Files:**
- Create: `backend/catalog.go`
- Create: `backend/catalog_test.go`
- Create: `backend/catalog_integration_test.go`
- Modify: `backend/metrics.go`
- Modify: `backend/metrics_test.go`

**Interfaces:**
- Consumes: tables from Task 1 and the existing `DBTX` interface.
- Produces: `CatalogRepository`, `CatalogObservation`, `CatalogResolution`, `CatalogScanReport`, `Observe`, `CompleteScan`, `Resolve`, typed sentinel errors, and bounded reconciliation metrics.

- [ ] **Step 1: Write failing validation and integration tests**

Cover a new source, repeated observation, legacy alias resolution, UUID
resolution, wrong-surface resolution, missing item, provider collision, and
folder kind:

```go
func TestCatalogValidation(t *testing.T) {
    if err := (CatalogObservation{Provider: "grimmory", UpstreamID: "7", LibraryID: "1", Surface: "library", Kind: "audiobook"}).Validate(); err != nil {
        t.Fatal(err)
    }
    if err := (CatalogObservation{Provider: "jellyfin", UpstreamID: "", LibraryID: "lib", Surface: "stream", Kind: "video"}).Validate(); !errors.Is(err, errCatalogInvalid) {
        t.Fatalf("err = %v", err)
    }
}
```

The integration test must assert that observing the same `(provider,
upstream_id)` returns the original UUID and updates `last_seen_at` without
creating a second item.

- [ ] **Step 2: Run the tests and confirm undefined catalog types**

Run:

```bash
scripts/test-backend.sh all -run 'TestCatalog'
```

Expected: FAIL because `CatalogRepository` and its value types are undefined.

- [ ] **Step 3: Define strict catalog value types and errors**

Implement these public-in-package contracts:

```go
type CatalogProvider string
type CatalogSurface string
type CatalogKind string

const (
    ProviderGrimmory CatalogProvider = "grimmory"
    ProviderJellyfin CatalogProvider = "jellyfin"
    SurfaceLibrary CatalogSurface = "library"
    SurfaceStream CatalogSurface = "stream"
)

type CatalogObservation struct {
    Provider CatalogProvider
    UpstreamID string
    LibraryID string
    Surface CatalogSurface
    Kind CatalogKind
}

type CatalogResolution struct {
    ID string
    Surface CatalogSurface
    Kind CatalogKind
    Provider CatalogProvider
    UpstreamID string
    LibraryID string
    Active bool
    Available bool
    Revision int64
}

type CatalogScanReport struct {
    Seen int
    Restored int64
    Missing int64
}

var (
    errCatalogInvalid = errors.New("catalog observation invalid")
    errCatalogNotFound = errors.New("catalog item not found")
    errCatalogWrongSurface = errors.New("catalog item belongs to another surface")
)
```

Validation accepts only the provider/surface/kind values from migration 0022,
requires nonempty upstream and library IDs, bounds each upstream string to 512
bytes, and rejects controls.

- [ ] **Step 4: Implement `CatalogRepository` transactionally**

Use these exact methods:

```go
type CatalogRepository struct { db DBTX }

func NewCatalogRepository(db DBTX) *CatalogRepository
func (r *CatalogRepository) Observe(ctx context.Context, in CatalogObservation) (CatalogResolution, error)
func (r *CatalogRepository) CompleteScan(ctx context.Context, provider CatalogProvider, libraryID string, seenUpstreamIDs []string) (CatalogScanReport, error)
func (r *CatalogRepository) Resolve(ctx context.Context, rawID string) (CatalogResolution, error)
func (r *CatalogRepository) ResolveFor(ctx context.Context, rawID string, surface CatalogSurface) (CatalogResolution, error)
```

`Observe` first selects by `(provider, upstream_id)`. When present it updates
`last_seen_at`, sets `available=true`, clears `missing_since`, and updates the
source library ID. When absent it opens a transaction,
creates `catalog_items`, inserts `catalog_sources`, and returns the UUID.
`Resolve` accepts either a canonical UUID or legacy upstream ID and always
returns the active source for that catalog item. A legacy collision across
providers is rejected as invalid rather than selected arbitrarily.

`CompleteScan` validates and deduplicates at most 100,000 seen IDs, runs only
after the caller has completed a provider/library enumeration, restores seen
sources, and marks previously available unseen sources unavailable with
`missing_since=now()`. Provider errors must prevent the caller from invoking it.

Instrument observation and completed scans with
`telos_catalog_reconciled_sources_total{provider,outcome}` and
`telos_catalog_reconcile_duration_seconds{provider,operation,result}`. The
implementation must expose a small recorder helper so provider adapters can
count upstream enumeration failures that occur before `CompleteScan`. Accept
only fixed provider, operation, result, and outcome values; never put titles,
paths, upstream IDs, canonical IDs, or member IDs in metric labels. Unit tests
must cover created, refreshed, restored, missing, ambiguous, and failed
outcomes without registering a second collector in the default registry.

- [ ] **Step 5: Run catalog tests and the race detector**

```bash
scripts/test-backend.sh all -race -run 'TestCatalog'
```

Expected: PASS, including concurrent observation returning one UUID and a
failed/incomplete scan leaving availability unchanged.

- [ ] **Step 6: Commit the repository slice**

```bash
git add backend/catalog.go backend/catalog_test.go backend/catalog_integration_test.go backend/metrics.go backend/metrics_test.go
git commit -m "feat: resolve stable catalog identities"
```

### Task 3: Implement minimal continuity storage

**Files:**
- Create: `backend/continuity.go`
- Create: `backend/continuity_test.go`
- Create: `backend/continuity_integration_test.go`

**Interfaces:**
- Consumes: canonical `CatalogResolution` from Task 2 and `member_progress` from Task 1.
- Produces: `ProgressInput`, `MemberProgress`, `ValidateProgress`, `Get`, `Put`, and `Continue`.

- [ ] **Step 1: Write failing format-validation tests**

Define table cases for valid EPUB/PDF/audiobook/video locators and invalid
cross-format fields:

```go
func TestValidateMemberProgressByKind(t *testing.T) {
    in := ProgressInput{Locator: json.RawMessage(`{"trackIndex":2}`), PositionMS: 9000, DurationMS: 12000, Percent: .75}
    got, err := ValidateProgress("audiobook", in)
    if err != nil || got.PositionMS != 9000 {
        t.Fatalf("got=%+v err=%v", got, err)
    }
    if _, err := ValidateProgress("video", ProgressInput{Locator: json.RawMessage(`{"cfi":"x"}`)}); !errors.Is(err, errProgressInvalid) {
        t.Fatalf("err=%v", err)
    }
}
```

Include bounds: 4 KiB locator, nonnegative positions, `position_ms <=
duration_ms` when duration is nonzero, `percent` in `[0,1]`, and a nonnegative
integer audiobook `trackIndex`.

- [ ] **Step 2: Run and observe undefined progress contracts**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestValidateMemberProgressByKind
```

Expected: FAIL because `ProgressInput` and `ValidateProgress` are undefined.

- [ ] **Step 3: Implement types and validation**

```go
type ProgressInput struct {
    Locator json.RawMessage `json:"locator"`
    PositionMS int64 `json:"positionMs"`
    DurationMS int64 `json:"durationMs"`
    Percent float64 `json:"percent"`
    Completed bool `json:"completed"`
}

type MemberProgress struct {
    ProgressInput
    UpdatedAt *time.Time `json:"updatedAt"`
}

type ContinuityRepository struct { db DBTX }

func NewContinuityRepository(db DBTX) *ContinuityRepository
func ValidateProgress(kind CatalogKind, in ProgressInput) (ProgressInput, error)
func (r *ContinuityRepository) Get(ctx context.Context, userID, itemID string) (MemberProgress, error)
func (r *ContinuityRepository) GetMany(ctx context.Context, userID string, itemIDs []string) (map[string]MemberProgress, error)
func (r *ContinuityRepository) Put(ctx context.Context, userID, itemID string, in ProgressInput) (MemberProgress, error)
func (r *ContinuityRepository) Continue(ctx context.Context, userID string, surface CatalogSurface, limit int) ([]string, error)
```

`Get` returns a zero value with `UpdatedAt=nil` when no row exists. `Put` uses
one `INSERT ... ON CONFLICT ... DO UPDATE ... RETURNING`. `Continue` joins
`catalog_items`, filters `completed=false`, caps limit at 50, and orders by
`updated_at DESC, catalog_item_id`.
`GetMany` deduplicates and caps input at 500 canonical UUIDs, executes one
`WHERE catalog_item_id = ANY($2::uuid[])` query scoped to the user, and omits
items with no row so callers can apply the zero progress value without N+1
queries.

- [ ] **Step 4: Add isolation and ordering integration tests**

Create two users and two catalog items. Assert each user sees only their own
position, a completed item is absent, and most recently updated incomplete item
sorts first. Assert no auxiliary event/history row or table is written.

- [ ] **Step 5: Run focused and full backend tests**

```bash
scripts/test-backend.sh all -run 'TestValidateMemberProgress|TestContinuity'
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: both commands PASS.

- [ ] **Step 6: Commit the continuity slice**

```bash
git add backend/continuity.go backend/continuity_test.go backend/continuity_integration_test.go
git commit -m "feat: store minimal member continuity"
```

### Task 4: Canonicalize existing Library and Stream handlers

**Files:**
- Modify: `backend/library.go:144-323,817-1069`
- Modify: `backend/main.go:179-520,2387-3178`
- Modify: `backend/media.go:19-229`
- Modify: `backend/library_test.go`
- Modify: `backend/main_test.go`
- Modify: `backend/media_test.go`

**Interfaces:**
- Consumes: `CatalogRepository.Observe`, `Resolve`, `ResolveFor`; `ContinuityRepository.Get` and `Put`.
- Produces: existing APIs using canonical UUIDs while accepting legacy IDs.

- [ ] **Step 1: Write failing canonical response and legacy route tests**

Extend fake Grimmory/Jellyfin tests to assert:

```go
if !looksLikeUUID(got.ID) {
    t.Fatalf("public id = %q, want canonical UUID", got.ID)
}
legacyReq := httptest.NewRequest("GET", "/api/v1/library/books/7/content", nil)
// The fake provider must still receive /api/v1/books/7/content.
```

Add equivalent tests for media library roots, folders, playable items, cover,
audio, video, and HLS routes. Assert an old Jellyfin ID is resolved before the
Jellyfin authorizer runs.

- [ ] **Step 2: Run focused tests and capture the current raw-ID failures**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*Canonical|Test.*Legacy'
```

Expected: FAIL because handlers return/pass raw upstream IDs.

- [ ] **Step 3: Initialize repositories once during startup**

Add package globals initialized after `dbPool` succeeds:

```go
var catalogRepo *CatalogRepository
var continuityRepo *ContinuityRepository

catalogRepo = NewCatalogRepository(dbPool)
continuityRepo = NewContinuityRepository(dbPool)
```

Tests set these to repositories backed by the integration fixture or a focused
fake. Do not add a no-op production fallback.

- [ ] **Step 4: Canonicalize Grimmory book mapping and routes**

Change public `LibraryBook.ID` to string and retain an unexported upstream ID:

```go
type LibraryBook struct {
    ID string `json:"id"`
    UpstreamID int64 `json:"-"`
    // existing metadata fields remain unchanged
}
```

After each authorized Grimmory book is mapped, call `Observe` with provider
`grimmory`, its stable allowed library ID, surface `library`, and kind derived
strictly from `EPUB`, `PDF`, or `AUDIOBOOK`. Resolve route IDs before building
any Grimmory path. Only EPUB, PDF, and audiobook Grimmory records participate
in the canonical Library catalog. Unknown or unsupported formats are omitted
from member-visible canonical Library responses and remain deferred; they must
never be retyped as audiobook, EPUB, PDF, or folder. This minimal scope matches
the existing deferral of comics and physical-book handling.

- [ ] **Step 5: Canonicalize Jellyfin roots, folders, items, and streams**

Observe each allowed view/folder/item with provider `jellyfin`. Derive `folder`,
`video`, or `audio` from the server-reported type and folder flag. Resolve
`parentId`, item cover IDs, direct-audio IDs, PlaybackInfo IDs, HLS item IDs,
and manifest subpaths to the active upstream source before authorization and
upstream URL construction.

Do not forward a browser UUID into Jellyfin. HLS locators continue to contain
the authorized upstream Jellyfin item ID internally; public route construction
uses the canonical UUID.

- [ ] **Step 6: Switch existing book progress handlers to continuity with compatibility dual-write**

`GET` reads `member_progress` first. If absent and the source is Grimmory, it
reads legacy `book_progress`, writes the canonical row, and returns it. `PUT`
validates using the resolved catalog kind, writes `member_progress`, and also
writes `book_progress` for EPUB/PDF during this phase. Audiobook progress has no
legacy write.

- [ ] **Step 7: Run authorization, proxy, HLS, and Library suites**

```bash
scripts/test-backend.sh all -run 'Test.*(Catalog|Library|Media|Proxy|HLS|Progress|Authorization)'
```

Expected: PASS; tests prove canonicalization happens before authorization and
provider URL construction.

- [ ] **Step 8: Commit the handler conversion**

```bash
git add backend/library.go backend/library_test.go backend/main.go backend/main_test.go backend/media.go backend/media_test.go
git commit -m "feat: canonicalize library and stream item ids"
```

### Task 5: Backfill and canonicalize commentary and chat references

**Files:**
- Create: `backend/catalog_backfill.go`
- Create: `backend/catalog_backfill_integration_test.go`
- Modify: `backend/annotations.go`
- Modify: `backend/annotation_handlers.go`
- Modify: `backend/annotations_test.go`
- Modify: `backend/annotations_integration_test.go`
- Modify: `backend/main.go:1803-1938`
- Modify: `backend/chat_test.go`

**Interfaces:**
- Consumes: observed `catalog_sources` and `CatalogRepository.Resolve`.
- Produces: `BackfillCatalogReferences(ctx, db)`, canonical annotation targets, and canonical chat embed refs.

- [ ] **Step 1: Write a failing mixed-reference integration fixture**

Seed one Grimmory source, one Jellyfin source, legacy `book_progress`, legacy
book/media annotations, and legacy `library_book`/`stream_film` messages. Assert
the backfill converts only resolvable rows, preserves visibility/replies and
embed snapshots, and is idempotent:

```go
first, err := BackfillCatalogReferences(ctx, f.DB)
if err != nil { t.Fatal(err) }
second, err := BackfillCatalogReferences(ctx, f.DB)
if err != nil || second.Updated != 0 { t.Fatalf("second=%+v err=%v", second, err) }
```

- [ ] **Step 2: Run the fixture and confirm the undefined backfill failure**

```bash
scripts/test-backend.sh all -run TestBackfillCatalogReferences
```

Expected: FAIL because the backfill does not exist.

- [ ] **Step 3: Implement bounded, idempotent backfill statements**

Define:

```go
type CatalogBackfillReport struct {
    Progress int64
    Annotations int64
    Embeds int64
    Updated int64
}

func BackfillCatalogReferences(ctx context.Context, db DBTX) (CatalogBackfillReport, error)
```

In one transaction per bounded batch:

- copy `book_progress` through the Grimmory source map into
  `member_progress` with `ON CONFLICT DO NOTHING`;
- update `annotations.target_id` where target type maps unambiguously to
  Grimmory (`book`) or Jellyfin (`media`);
- update `messages.embed_ref` for `library_book` and `stream_film` without
  changing `embed_snapshot`.

Canonical UUID strings are skipped. Ambiguous/unresolved rows remain unchanged
and count neither as success nor data loss.

- [ ] **Step 4: Resolve targets before all new commentary and embed operations**

Add:

```go
func canonicalAnnotationTarget(ctx context.Context, targetType, rawID string) (CatalogResolution, error)
```

`book` requires `SurfaceLibrary`; `media` requires `SurfaceStream`. New
annotations store `resolution.ID`. Annotation-scoped patch/delete/reply reads
canonicalize a legacy row once before authorization. `buildEmbedSnapshot`
resolves first, fetches by active source, and stores the canonical `embed_ref`.

- [ ] **Step 5: Run commentary, chat, and account lifecycle suites**

```bash
scripts/test-backend.sh all -run 'Test.*(Annotation|Comment|Embed|Backfill|AccountLifecycle)'
```

Expected: PASS; deleting an account still cascades its progress and annotations
without removing shared catalog identities.

- [ ] **Step 6: Commit the reference compatibility slice**

```bash
git add backend/catalog_backfill.go backend/catalog_backfill_integration_test.go backend/annotations.go backend/annotation_handlers.go backend/annotations_test.go backend/annotations_integration_test.go backend/main.go backend/chat_test.go
git commit -m "feat: preserve catalog references across providers"
```

### Task 6: Update frontend ID contracts and verify compatibility

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/stores/useLibraryStore.ts`
- Modify: `frontend/src/stores/useMediaStore.ts`
- Modify: `frontend/src/components/library/BookManageModal.tsx`
- Modify: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/components/library/EpubReader.tsx`
- Modify: `frontend/src/components/library/PdfReader.tsx`
- Modify: `frontend/src/components/chat/SharePicker.tsx`
- Modify: focused `*.test.tsx` and `*.test.ts` fixtures under the same directories
- Modify: `frontend/e2e/library.spec.ts`
- Modify: `frontend/e2e/stream.spec.ts`
- Modify: `frontend/e2e/annotations.spec.ts`
- Modify: `frontend/e2e/chat.spec.ts`
- Modify: `documentation/operations/release-inputs.pathspec`

**Interfaces:**
- Consumes: canonical string IDs from Task 4 and compatibility routing from Task 5.
- Produces: opaque string ID handling throughout the browser with no UUID parsing or provider assumptions.

- [ ] **Step 1: Change test fixtures to canonical-looking string IDs and observe type failures**

Use a fixed UUID such as `11111111-1111-4111-8111-111111111111` in Library
fixtures. Add an assertion that URL helpers encode arbitrary opaque string IDs:

```ts
expect(libraryCoverUrl("11111111-1111-4111-8111-111111111111"))
  .toBe("/api/v1/library/books/11111111-1111-4111-8111-111111111111/cover");
```

- [ ] **Step 2: Run frontend typecheck/lint tests and confirm numeric-ID failures**

```bash
cd frontend
npm run lint
npx vitest run
```

Expected: FAIL where `LibraryBook.id` and URL helpers still require `number`.

- [ ] **Step 3: Convert all Library IDs to opaque strings**

Change:

```ts
export function libraryCoverUrl(bookId: string): string
export function libraryContentUrl(bookId: string): string

export interface LibraryBook {
  id: string;
  // existing fields unchanged
}
```

Remove `Number`, `parseInt`, arithmetic, and numeric equality around book IDs.
Keep `encodeURIComponent` at URL boundaries. Media IDs already use strings;
add comments and tests making them explicitly opaque canonical IDs.

- [ ] **Step 4: Add E2E coverage for old and new deep links**

Exercise a canonical Library link, canonical Stream link, legacy numeric book
link, and legacy Jellyfin media link. Assert all resolve without exposing an
upstream host or token and that annotations/share cards open the same item.

- [ ] **Step 5: Update release inventory and run the phase gate**

Add every new migration and Go file from this plan to
`documentation/operations/release-inputs.pathspec`, then run:

```bash
scripts/verify-clean-checkout.sh --inventory-only
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend
npm run lint
npx vitest run
```

Expected: all commands PASS.

- [ ] **Step 6: Commit the frontend compatibility slice**

```bash
git add frontend/src/lib/api.ts frontend/src/stores/useLibraryStore.ts frontend/src/stores/useMediaStore.ts frontend/src/components/library frontend/src/components/chat/SharePicker.tsx frontend/e2e/library.spec.ts frontend/e2e/stream.spec.ts frontend/e2e/annotations.spec.ts frontend/e2e/chat.spec.ts documentation/operations/release-inputs.pathspec
git commit -m "feat: use opaque catalog ids in the client"
```

## Phase 1 completion gate

Run the repository verification block from `AGENTS.md`. Additionally verify:

```bash
scripts/test-backend.sh all -race
cd frontend
npx playwright test library.spec.ts stream.spec.ts annotations.spec.ts chat.spec.ts
```

The phase is complete only when existing Library/Stream behavior works through
canonical IDs, old links remain valid, two members retain isolated progress,
and no provider ID is needed by browser logic.
