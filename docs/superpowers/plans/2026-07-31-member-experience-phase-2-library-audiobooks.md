# Member Experience Phase 2: Grimmory Library and Audiobooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Grimmory into the sole Library provider for EPUB, PDF, and audiobooks, with real author/series/recent browsing, a Library-owned audiobook player, commentary, and local resume.

**Architecture:** Extend the focused Grimmory adapter with normalized application and audiobook DTOs, then expose narrow `view_library` routes. The browser uses canonical IDs from Phase 1; range streams pass through the hardened binary proxy and shared stream-capacity gate, while all member progress remains in Telos PostgreSQL.

**Tech Stack:** Go 1.26.5, Grimmory 3.2.4 HTTP API, PostgreSQL/pgx, Redis, Next.js/React 19, TypeScript, Zustand, HTMLMediaElement, Vitest, Playwright, Podman.

## Global Constraints

- Phase 1 (`2026-07-31-member-experience-phase-1-catalog-continuity.md`) is complete and its canonical ID/continuity interfaces are the only identity and progress APIs used here.
- Grimmory remains an isolated AGPL service; integrate only over authenticated HTTP.
- The shared Grimmory admin JWT never stores or receives per-member progress, ratings, reviews, notes, or playstate.
- Audiobook extensions verified in Grimmory 3.2.4 are `.m4b`, `.m4a`, `.mp3`, and `.opus`; do not advertise unverified formats.
- Keep the existing 100 MiB member-upload ceiling. Larger existing audiobooks enter through the operator migration in Phase 4.
- Comics, physical books, provider shelves, recommendations, statistics, OPDS, Kobo, and KoReader are out of scope.
- Every binary request is authorized against `GRIMMORY_LIBRARY_IDS`, uses the Range/header allowlists, and acquires the same node/per-user stream slot as Jellyfin.
- Preserve the Books/Files Library structure and do not add a top-level Audiobooks module.
- Do not modify or stage unrelated worktree changes.

---

## File structure

- Create `backend/grimmory_catalog.go` — normalized app book, author, series, and recent queries.
- Create `backend/grimmory_catalog_test.go` — response-shape, pagination, and authorization tests.
- Create `backend/audiobooks.go` — audiobook info DTOs, routes, comments, and range proxy handlers.
- Create `backend/audiobooks_test.go` — pure handler/proxy tests.
- Create `backend/audiobooks_integration_test.go` — progress/commentary isolation and catalog mapping tests.
- Modify `backend/library.go` — recognize audiobook kind and delegate new adapter concerns.
- Modify `backend/main.go` — register explicit Library routes and extend bookdrop upload formats.
- Modify `backend/upload_integration_test.go` and `backend/content_validation_test.go` — verified audiobook upload types.
- Create `frontend/src/components/library/LibraryItemDetail.tsx` — accessible detail surface shared by book formats.
- Create `frontend/src/components/library/AudiobookPlayer.tsx` — track/chapter playback and bounded progress saves.
- Create `frontend/src/components/library/AudiobookPlayer.test.tsx`.
- Create `frontend/src/hooks/useMediaProgress.ts` — reusable 15-second/pause/seek/ended progress coordinator.
- Create `frontend/src/hooks/useMediaProgress.test.ts`.
- Modify `frontend/src/stores/useLibraryStore.ts` — normalized items, shelves, authors, series, and detail loading.
- Create `frontend/src/stores/useLibraryStore.test.ts` — normalized shelf/detail state tests.
- Modify `frontend/src/app/(shell)/library/page.tsx` — Continue/Recent/Browse shelves and detail routing.
- Modify `frontend/src/components/files/FilesBrowser.tsx` and `frontend/src/stores/useFilesStore.ts` — audiobook bookdrop copy/validation.
- Create `frontend/src/stores/useFilesStore.test.ts` — audiobook upload validation tests.
- Create `frontend/e2e/library-audiobooks.spec.ts`.
- Modify `frontend/e2e/library.spec.ts` and `frontend/e2e/accessibility.spec.ts`.
- Modify `documentation/operations/release-inputs.pathspec`.

### Task 1: Normalize Grimmory Library records and audiobook kind

**Files:**
- Create: `backend/grimmory_catalog.go`
- Create: `backend/grimmory_catalog_test.go`
- Modify: `backend/library.go:144-323`
- Modify: `backend/library_test.go`

**Interfaces:**
- Consumes: `CatalogRepository.Observe` from Phase 1 and existing `grimmoryGET`/JWT refresh.
- Produces: `LibraryItem`, `GrimmoryCatalog`, `ListItems`, `GetItem`, and strict format-to-kind mapping.

- [ ] **Step 1: Write failing mapping tests for EPUB, PDF, and AUDIOBOOK**

Use a fake `/api/v1/books` response and assert normalized fields:

```go
func TestLibraryKindFromGrimmory(t *testing.T) {
    cases := map[string]CatalogKind{
        "EPUB": "epub",
        "PDF": "pdf",
        "AUDIOBOOK": "audiobook",
    }
    for format, want := range cases {
        got, err := libraryKindFromGrimmory(format)
        if err != nil || got != want { t.Fatalf("%s: got=%q err=%v", format, got, err) }
    }
    if _, err := libraryKindFromGrimmory("CBX"); !errors.Is(err, errUnsupportedLibraryKind) {
        t.Fatalf("CBX err=%v", err)
    }
}
```

Also assert audiobook narrator, duration, authors, series, description, added
date, cover URL, and canonical ID are present without exposing Grimmory's ID.

- [ ] **Step 2: Run the focused test and confirm missing adapter types**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestLibraryKindFromGrimmory|TestGrimmoryCatalog'
```

Expected: FAIL because the adapter and normalized type do not exist.

- [ ] **Step 3: Define the normalized Library contract**

```go
type LibraryItem struct {
    ID string `json:"id"`
    Title string `json:"title"`
    Subtitle string `json:"subtitle"`
    Authors []string `json:"authors"`
    Narrator string `json:"narrator,omitempty"`
    Categories []string `json:"categories"`
    Description string `json:"description"`
    Language string `json:"language"`
    SeriesName string `json:"seriesName"`
    SeriesNumber *float64 `json:"seriesNumber"`
    PublishedDate string `json:"publishedDate"`
    AddedOn string `json:"addedOn"`
    Kind CatalogKind `json:"kind"`
    DurationMS int64 `json:"durationMs,omitempty"`
    CoverURL string `json:"coverUrl"`
    Progress MemberProgress `json:"progress"`
}

type GrimmoryCatalog struct {
    repo *CatalogRepository
}

func (c *GrimmoryCatalog) ListItems(ctx context.Context, userID string) ([]LibraryItem, error)
func (c *GrimmoryCatalog) GetItem(ctx context.Context, userID, rawID string) (LibraryItem, CatalogResolution, error)
```

Map only the selected kinds. Unsupported Grimmory records are omitted from the
main catalog with a bounded warning counter, not mislabeled or made playable.
After canonicalizing the page, hydrate progress with one
`ContinuityRepository.GetMany` call rather than a query per book.
After a complete successful catalog response, group observed upstream IDs by
configured Grimmory library and call `CatalogRepository.CompleteScan`. A
timeout, decode error, or pagination gap must skip `CompleteScan` so known
sources remain available. Record the bounded reconciliation duration and
success/failure outcome through the Phase 1 metrics helper. Any shared
metadata/cover cache key that identifies one item must include the canonical
ID and catalog revision (`...:<canonical-id>:r<revision>`); member progress is
merged only after cache lookup and is never stored in that value.

- [ ] **Step 4: Reuse the verified complete catalog and preserve management DTOs**

Keep the existing `GET /api/v1/books` complete-catalog request for member
browsing and administrator metadata operations; its deployed 3.2.4 `Book`
shape includes primary file kind and audiobook metadata. Use the paginated app
endpoints only for the dedicated recent/author/series experiences in Task 2.
Move only shared translation helpers out of `library.go`; do not duplicate JWT
or cache logic.

- [ ] **Step 5: Run Library mapping and management regression tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(Grimmory|Library|Metadata|Cover|Delete)'
```

Expected: PASS; existing metadata, cover, content, and delete flows retain their
current permission checks.

- [ ] **Step 6: Commit the normalized catalog slice**

```bash
git add backend/grimmory_catalog.go backend/grimmory_catalog_test.go backend/library.go backend/library_test.go
git commit -m "feat: normalize grimmory library items"
```

### Task 2: Add Continue, Recent, author, and series routes

**Files:**
- Modify: `backend/grimmory_catalog.go`
- Modify: `backend/grimmory_catalog_test.go`
- Modify: `backend/main.go:448-492`
- Modify: `backend/main_test.go`

**Interfaces:**
- Consumes: `ContinuityRepository.Continue`, `GrimmoryCatalog.GetItem`, and canonical IDs.
- Produces: `handleLibraryContinue`, `handleLibraryRecent`, `handleLibraryAuthors`, `handleLibraryAuthorBooks`, `handleLibrarySeries`, `handleLibrarySeriesBooks`.

- [ ] **Step 1: Write failing route translation tests**

Assert these Telos-to-Grimmory mappings and `view_library` gating:

```text
GET /api/v1/library/continue              -> Telos member_progress + batched Grimmory item fetch
GET /api/v1/library/recent                -> GET /api/v1/app/books/recently-added?limit=20
GET /api/v1/library/authors               -> paginated GET /api/v1/app/authors
GET /api/v1/library/authors/{id}/books    -> GET author detail, then paginated /api/v1/app/books?authors={name}
GET /api/v1/library/series                -> paginated GET /api/v1/app/series
GET /api/v1/library/series/{name}/books   -> GET /api/v1/app/series/{seriesName}/books
```

Tests require public shelf limit 20, upstream page size at most 100, traversal
until `hasNext=false`, URL-escaped author/series values, canonical item IDs,
empty arrays instead of `null`, and no call to Grimmory's shared
continue-reading/listening endpoints. Author-book lookup first resolves the
numeric author ID through `/api/v1/app/authors/{authorId}` and uses its returned
name as the exact `authors` filter; it never treats the numeric ID as a name.

- [ ] **Step 2: Run and observe 404 failures**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestLibrary(Continue|Recent|Authors|Series)'
```

Expected: FAIL because routes are not registered.

- [ ] **Step 3: Implement explicit adapter methods**

```go
func (c *GrimmoryCatalog) Recent(ctx context.Context, userID string, limit int) ([]LibraryItem, error)
func (c *GrimmoryCatalog) Authors(ctx context.Context) ([]LibraryAuthor, error)
func (c *GrimmoryCatalog) AuthorBooks(ctx context.Context, userID, authorID string) ([]LibraryItem, error)
func (c *GrimmoryCatalog) Series(ctx context.Context) ([]LibrarySeries, error)
func (c *GrimmoryCatalog) SeriesBooks(ctx context.Context, userID, name string) ([]LibraryItem, error)
```

Continue loads canonical IDs from Telos, then resolves current Grimmory sources.
If one item disappeared, omit that row and record a bounded warning; do not fail
the entire shelf.

- [ ] **Step 4: Register permission-gated handlers**

Register all six routes beside existing Library routes with `view_library`.
Return JSON API errors through `writeAPIError`, 502/503 on upstream failure, and
404 for an unknown author/series detail.

- [ ] **Step 5: Run focused and authorization tests**

```bash
scripts/test-backend.sh all -run 'TestLibrary(Continue|Recent|Authors|Series)|Test.*Authorization'
```

Expected: PASS.

- [ ] **Step 6: Commit deterministic Library discovery**

```bash
git add backend/grimmory_catalog.go backend/grimmory_catalog_test.go backend/main.go backend/main_test.go
git commit -m "feat: add library continuity and discovery routes"
```

### Task 3: Add hardened Grimmory audiobook info and streams

**Files:**
- Create: `backend/audiobooks.go`
- Create: `backend/audiobooks_test.go`
- Modify: `backend/main.go:448-492`
- Modify: `backend/proxy_test.go`
- Modify: `backend/capacity_test.go`

**Interfaces:**
- Consumes: `CatalogRepository.ResolveFor`, `authorizeBookHTTP`, `grimmoryRequest`, `proxyGrimmoryBinary`, and `acquireStreamSlot`.
- Produces: `AudiobookInfo`, `AudiobookTrack`, `AudiobookChapter`, info and range-stream handlers.

- [ ] **Step 1: Write failing DTO and proxy tests**

Use the live 3.2.4 response field names exactly:

```go
type AudiobookTrack struct {
    Index int `json:"index"`
    Title string `json:"title"`
    DurationMS int64 `json:"durationMs"`
    CumulativeStartMS int64 `json:"cumulativeStartMs"`
}

type AudiobookChapter struct {
    Index int `json:"index"`
    Title string `json:"title"`
    StartTimeMS int64 `json:"startTimeMs"`
    EndTimeMS int64 `json:"endTimeMs"`
    DurationMS int64 `json:"durationMs"`
}
```

Assert malformed/negative indexes are rejected before upstream work; Range is
forwarded; provider redirects/cookies/auth headers are stripped; content headers
are allowlisted; and capacity exhaustion returns 503 with `Retry-After: 5`.

- [ ] **Step 2: Run and confirm undefined handlers**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestAudiobook|TestGrimmoryAudio'
```

Expected: FAIL because `audiobooks.go` does not exist.

- [ ] **Step 3: Implement normalized info fetching**

```go
type AudiobookInfo struct {
    ID string `json:"id"`
    Title string `json:"title"`
    Author string `json:"author"`
    Narrator string `json:"narrator"`
    DurationMS int64 `json:"durationMs"`
    Codec string `json:"codec"`
    Tracks []AudiobookTrack `json:"tracks"`
    Chapters []AudiobookChapter `json:"chapters"`
    Progress MemberProgress `json:"progress"`
}

func fetchGrimmoryAudiobookInfo(ctx context.Context, userID, rawID string) (AudiobookInfo, CatalogResolution, error)
```

Resolve to `SurfaceLibrary`, require kind `audiobook`, authorize the upstream
book and configured Grimmory library, call `/api/v1/audiobooks/{upstream}/info`,
and normalize nil lists to empty lists.

- [ ] **Step 4: Implement explicit stream handlers**

Register:

```text
GET /api/v1/library/audiobooks/{id}/info
GET /api/v1/library/audiobooks/{id}/stream
GET /api/v1/library/audiobooks/{id}/tracks/{index}/stream
```

Both binary handlers authorize before `acquireStreamSlot`, defer its release,
then call `proxyGrimmoryBinary` with only the verified paths
`/api/v1/audiobooks/{upstream}/stream` and
`/api/v1/audiobooks/{upstream}/track/{index}/stream`.

- [ ] **Step 5: Run proxy, capacity, and security tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(Audiobook|Proxy|Capacity|Security)'
```

Expected: PASS; no path wildcard or browser-supplied provider path exists.

- [ ] **Step 6: Commit audiobook gateway routes**

```bash
git add backend/audiobooks.go backend/audiobooks_test.go backend/main.go backend/proxy_test.go backend/capacity_test.go
git commit -m "feat: proxy grimmory audiobook playback"
```

### Task 4: Add audiobook progress, commentary, and upload acceptance

**Files:**
- Modify: `backend/audiobooks.go`
- Modify: `backend/audiobooks_test.go`
- Create: `backend/audiobooks_integration_test.go`
- Modify: `backend/annotation_handlers.go`
- Modify: `backend/annotations_integration_test.go`
- Modify: `backend/main.go:3382-3515`
- Modify: `backend/upload_integration_test.go`
- Modify: `backend/content_validation_test.go`

**Interfaces:**
- Consumes: `ContinuityRepository`, canonical annotation helpers, upload scanner/quota pipeline.
- Produces: audiobook progress/comments and safe `.m4b/.m4a/.mp3/.opus` bookdrop acceptance.

- [ ] **Step 1: Write failing isolation, comment, and upload tests**

Assert:

- two members get independent track/position progress;
- plain audiobook comments store target type `book`, canonical ID, empty locator,
  and no selected text;
- EPUB/PDF annotation validation is unchanged;
- `.m4b`, `.m4a`, `.mp3`, and `.opus` are accepted for bookdrop after ClamAV;
- `.ogg`, `.wav`, `.flac`, `.cbz`, and a 100 MiB-plus request are rejected by
  the member bookdrop path.

- [ ] **Step 2: Run focused tests and observe rejection/404 failures**

```bash
scripts/test-backend.sh all -run 'Test.*(AudiobookProgress|AudiobookComment|BookUploadAudio)'
```

Expected: FAIL because routes and upload allowlists are absent.

- [ ] **Step 3: Register canonical audiobook progress and comment routes**

```text
GET /api/v1/library/audiobooks/{id}/progress
PUT /api/v1/library/audiobooks/{id}/progress
GET /api/v1/library/items/{id}/comments
POST /api/v1/library/items/{id}/comments
```

Progress derives kind server-side and calls `ValidateProgress`. Comment create
requires kind `audiobook`, calls `CreateAnnotation` with `target_type=book`,
canonical ID, `{}` locator, empty selected text, and the existing visibility/note
limits.

- [ ] **Step 4: Add a dedicated bookdrop extension set**

Do not reuse the broader shared-file extension map. Define:

```go
var bookdropExts = map[string]bool{
    ".pdf": true, ".epub": true,
    ".m4b": true, ".m4a": true, ".mp3": true, ".opus": true,
}
```

Use the existing 104857600-byte cap, magic/MIME validation, ClamAV scan,
staging, quota, and handoff logic. Extend MIME acceptance only for the verified
audio extensions and keep octet-stream fallback tied to extension validation.

- [ ] **Step 5: Run upload, annotation, and continuity suites**

```bash
scripts/test-backend.sh all -run 'Test.*(Upload|ContentValidation|Annotation|Audiobook|Continuity)'
```

Expected: PASS.

- [ ] **Step 6: Commit member-owned audiobook state**

```bash
git add backend/audiobooks.go backend/audiobooks_test.go backend/audiobooks_integration_test.go backend/annotation_handlers.go backend/annotations_integration_test.go backend/main.go backend/upload_integration_test.go backend/content_validation_test.go
git commit -m "feat: add audiobook progress and commentary"
```

### Task 5: Build Library shelves and item details

**Files:**
- Create: `frontend/src/components/library/LibraryItemDetail.tsx`
- Create: `frontend/src/components/library/LibraryItemDetail.test.tsx`
- Modify: `frontend/src/stores/useLibraryStore.ts`
- Create: `frontend/src/stores/useLibraryStore.test.ts`
- Modify: `frontend/src/app/(shell)/library/page.tsx`
- Modify: `frontend/src/styles/library.css`
- Modify: `frontend/src/lib/api.ts`

**Interfaces:**
- Consumes: normalized Library, Continue, Recent, author, and series APIs.
- Produces: `LibraryItem`, `LibraryAuthor`, `LibrarySeries`, store loaders, and an accessible detail surface.

- [ ] **Step 1: Write failing store and detail component tests**

Define the client contract:

```ts
export type LibraryKind = "epub" | "pdf" | "audiobook";
export interface LibraryItem {
  id: string;
  title: string;
  kind: LibraryKind;
  authors: string[];
  narrator?: string;
  description: string;
  seriesName: string;
  seriesNumber: number | null;
  durationMs?: number;
  coverUrl: string;
  progress: MemberProgress;
}
```

Tests assert empty Continue is omitted, Recently Added is not labeled as
personalized, author/series controls load their routes, and selecting a leaf
opens details before the reader/player.

- [ ] **Step 2: Run Vitest and observe missing types/components**

```bash
cd frontend
npx vitest run src/stores/useLibraryStore.test.ts src/components/library/LibraryItemDetail.test.tsx
```

Expected: FAIL because the normalized store and detail component are absent.

- [ ] **Step 3: Refactor the store into explicit shelf/detail actions**

Add:

```ts
fetchCatalog(): Promise<void>
fetchContinue(): Promise<void>
fetchRecent(): Promise<void>
fetchAuthors(): Promise<void>
fetchSeries(): Promise<void>
fetchAuthorBooks(authorId: string): Promise<void>
fetchSeriesBooks(seriesName: string): Promise<void>
fetchItem(id: string): Promise<LibraryItem>
```

Normalize every nullable provider list at the API boundary. Keep existing facet
filtering for Browse and avoid storing duplicate item objects in multiple
mutable maps; shelves hold canonical IDs and `itemsById` owns item data.

- [ ] **Step 4: Implement the Library browse/detail experience**

Render Continue, Recently Added, and Browse in that order, omitting empty
shelves. Use provider cover URLs with a deterministic no-art placeholder. The
detail surface contains title, authors/narrator, description, year/date,
duration, genres/categories, series link, progress, commentary area, and one
primary Read/Listen/Continue action. It traps focus while open and restores
focus to the invoking card on close.

- [ ] **Step 5: Run component, lint, and responsive tests**

```bash
cd frontend
npm run lint
npx vitest run src/stores/useLibraryStore.test.ts src/components/library/LibraryItemDetail.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit Library discovery UI**

```bash
git add frontend/src/components/library/LibraryItemDetail.tsx frontend/src/components/library/LibraryItemDetail.test.tsx frontend/src/stores/useLibraryStore.ts frontend/src/stores/useLibraryStore.test.ts frontend/src/app/'(shell)'/library/page.tsx frontend/src/styles/library.css frontend/src/lib/api.ts
git commit -m "feat: add library shelves and item details"
```

### Task 6: Build the Library audiobook player and bounded progress hook

**Files:**
- Create: `frontend/src/hooks/useMediaProgress.ts`
- Create: `frontend/src/hooks/useMediaProgress.test.ts`
- Create: `frontend/src/components/library/AudiobookPlayer.tsx`
- Create: `frontend/src/components/library/AudiobookPlayer.test.tsx`
- Modify: `frontend/src/components/library/LibraryItemDetail.tsx`
- Modify: `frontend/src/styles/library.css`
- Modify: `frontend/src/components/CommentaryPanel.tsx`

**Interfaces:**
- Consumes: audiobook info/stream/progress/comment routes and `MemberProgress`.
- Produces: `useMediaProgress`, `AudiobookPlayer`, track-aware resume, and unsaved-state UX.

- [ ] **Step 1: Write fake-timer progress-hook tests**

Assert one save at 15 seconds while playing, immediate saves on pause, completed
seek, track change, stop, and ended, no writes while time is unchanged, one
bounded retry after failure, and cleanup flush using `fetch(..., {keepalive:
true})` only when state is dirty.

```ts
const progress = useMediaProgress({
  itemId,
  kind: "audiobook",
  initial,
  save: (payload) => api(`/api/v1/library/audiobooks/${itemId}/progress`, {
    method: "PUT",
    body: JSON.stringify(payload),
  }),
});
```

- [ ] **Step 2: Run the hook/player tests and observe missing modules**

```bash
cd frontend
npx vitest run src/hooks/useMediaProgress.test.ts src/components/library/AudiobookPlayer.test.tsx
```

Expected: FAIL because both modules are absent.

- [ ] **Step 3: Implement `useMediaProgress` without event history**

Expose `markPlaying`, `updatePosition`, `changeTrack`, `markPaused`,
`markEnded`, `flush`, `saveState`, and `lastError`. Keep only the latest pending
payload. Never queue individual events. Backoff is 1 second then 3 seconds and
stops until the next member/media event after two consecutive failures.

- [ ] **Step 4: Implement track/chapter-aware audio playback**

`AudiobookPlayer` loads info and local progress, selects the stored track,
seeks after `loadedmetadata`, and presents:

- native play/pause/time controls;
- 1×, 1.25×, 1.5×, and 2× speed;
- previous/next track or chapter;
- labelled track/chapter list;
- current/total time;
- subtle `Saving…`/`Position not saved` status;
- commentary using `/api/v1/library/items/{id}/comments`;
- share link with `library_book` and canonical ID.

On track end, advance only when another track exists; otherwise mark completed.

- [ ] **Step 5: Run player, accessibility, and existing reader regressions**

```bash
cd frontend
npm run lint
npx vitest run src/hooks/useMediaProgress.test.ts src/components/library/AudiobookPlayer.test.tsx src/components/library/BookReader.test.tsx
```

Expected: PASS; EPUB/PDF reader behavior is unchanged.

- [ ] **Step 6: Commit audiobook playback UI**

```bash
git add frontend/src/hooks/useMediaProgress.ts frontend/src/hooks/useMediaProgress.test.ts frontend/src/components/library/AudiobookPlayer.tsx frontend/src/components/library/AudiobookPlayer.test.tsx frontend/src/components/library/LibraryItemDetail.tsx frontend/src/components/CommentaryPanel.tsx frontend/src/styles/library.css
git commit -m "feat: add library audiobook player"
```

### Task 7: Extend bookdrop UX and certify the Library phase

**Files:**
- Modify: `frontend/src/stores/useFilesStore.ts`
- Create: `frontend/src/stores/useFilesStore.test.ts`
- Modify: `frontend/src/components/files/FilesBrowser.tsx`
- Create: `frontend/e2e/library-audiobooks.spec.ts`
- Modify: `frontend/e2e/library.spec.ts`
- Modify: `frontend/e2e/accessibility.spec.ts`
- Modify: `documentation/operations/release-inputs.pathspec`

**Interfaces:**
- Consumes: the complete Library API/player surface from Tasks 1–6.
- Produces: verified member upload copy, E2E coverage, and release inventory.

- [ ] **Step 1: Write failing upload copy and E2E assertions**

Change `BOOK_EXTENSIONS` to the verified six extensions and assert exact copy:

```ts
expect(bookdropAccept).toBe(".pdf,.epub,.m4b,.m4a,.mp3,.opus");
expect(validateUpload(file("voice.ogg", 1024), "bookdrop"))
  .toBe("bookdrop accepts PDF, EPUB, M4B, M4A, MP3, or OPUS");
```

The E2E fixture uses a tiny generated MP3/OPUS test asset already tracked under
`frontend/e2e/fixtures/`; it must not depend on external media.

- [ ] **Step 2: Implement exact member-facing upload copy**

Show `pdf / epub / m4b / m4a / mp3 / opus → bookdrop (flat), max 100 MiB`.
Keep the client limit equal to 104857600 and treat browser validation as UX,
not the security boundary.

- [ ] **Step 3: Add end-to-end member journeys**

Cover:

1. real cover and item detail;
2. author and series navigation;
3. Continue and Recently Added ordering;
4. audiobook play, track change, pause, reload, and resume;
5. two accounts with independent positions;
6. audiobook private/community commentary;
7. upload validation and a successful small audiobook handoff;
8. phone portrait and landscape player layout;
9. keyboard/focus/status accessibility.

- [ ] **Step 4: Update release inventory and run all phase checks**

```bash
scripts/verify-clean-checkout.sh --inventory-only
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend
npm run lint
npx vitest run
npx playwright test library.spec.ts library-audiobooks.spec.ts accessibility.spec.ts
```

Expected: all commands PASS.

- [ ] **Step 5: Commit the certified Library phase**

```bash
git add frontend/src/stores/useFilesStore.ts frontend/src/stores/useFilesStore.test.ts frontend/src/components/files/FilesBrowser.tsx frontend/e2e/library-audiobooks.spec.ts frontend/e2e/library.spec.ts frontend/e2e/accessibility.spec.ts documentation/operations/release-inputs.pathspec
git commit -m "test: certify grimmory audiobook experience"
```

## Phase 2 completion gate

The phase is complete when EPUB/PDF regressions pass, Grimmory audiobook Range
playback works under the shared capacity gate, two members resume independently,
comments use canonical Library targets, author/series/recent shelves behave
deterministically, and no audiobook feature reads or writes shared Grimmory user
state.
