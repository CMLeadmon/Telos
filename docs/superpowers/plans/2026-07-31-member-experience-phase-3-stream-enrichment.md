# Member Experience Phase 3: Jellyfin Stream Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Stream's synthetic/basic catalog with real Jellyfin artwork and details, deterministic shelves, local resume, chapters, audio-track selection, and subtitles.

**Architecture:** Extract the existing Jellyfin catalog handlers from `main.go` into a focused adapter/handler pair, normalize only selected Jellyfin fields into canonical Telos DTOs, and extend the hardened HLS boundary with validated playback selections. Stream progress uses the Phase 1 continuity repository and the Phase 2 browser progress hook, never Jellyfin's shared-user playstate.

**Tech Stack:** Go 1.26.5, Jellyfin 10.11.11 HTTP API, PostgreSQL/pgx, Redis, HLS.js, HTMLMediaElement, Next.js/React 19, TypeScript, Zustand, Vitest, Playwright, Podman.

## Global Constraints

- Phases 1 and 2 are complete; use their canonical IDs, `MemberProgress`, and `useMediaProgress` contracts unchanged.
- Jellyfin remains an isolated GPL service accessed only through explicit server-side HTTP translations.
- Never call Jellyfin `PlayingItems`, `Sessions/Playing`, `UserItems/Resume`, favorites, ratings, or personalized suggestions for member state.
- Continue comes from Telos PostgreSQL. Recently Added and Related are transparent provider catalog queries, not personalized ranking.
- Resolve canonical identity, then enforce `view_media` and `JELLYFIN_LIBRARY_IDS`, before every catalog, image, playback, HLS, subtitle, or progress operation.
- HLS manifests remain rewritten to opaque session-bound Telos locators; provider tokens, hosts, media-source IDs, redirects, and arbitrary subpaths never reach the browser.
- Keep current global/per-user stream capacity, query/header allowlists, and HLS resource limits.
- Audiobooks must not appear in Stream after Phase 4 cutover, but legacy audiobook aliases must redirect rather than fail.
- Do not add My List, Watch Parties, SyncPlay, Live TV, playlists, lyrics, remote subtitle downloads, or trickplay in this phase.
- Do not modify or stage unrelated worktree changes.

---

## File structure

- Create `backend/jellyfin_catalog.go` — provider DTOs and normalized catalog/detail/latest/related methods.
- Create `backend/jellyfin_catalog_test.go` — provider translation and normalization tests.
- Create `backend/media_handlers.go` — existing and new Stream HTTP handlers.
- Create `backend/media_handlers_test.go` — route, authorization, cache, and degradation tests.
- Create `backend/jellyfin_playback.go` — PlaybackInfo normalization and validated selection.
- Create `backend/jellyfin_playback_test.go` — track/chapter/subtitle/transcoding-path tests.
- Modify `backend/main.go` — remove moved handler bodies and register the same plus new routes.
- Modify `backend/media.go` — extend strict selection query allowlist and canonical authorization helpers.
- Modify `backend/hls.go` and `backend/hls_test.go` — preserve selected stream parameters inside signed locators only.
- Modify `frontend/src/stores/useMediaStore.ts` and create its test — normalized details and shelf state.
- Create `frontend/src/components/stream/StreamItemDetail.tsx` and tests.
- Create `frontend/src/components/stream/MediaShelf.tsx` and tests.
- Modify `frontend/src/components/stream/HlsPlayer.tsx` and tests — resume, tracks, subtitles, chapters.
- Modify `frontend/src/app/(shell)/stream/page.tsx` and `frontend/src/styles/stream.css`.
- Create `frontend/e2e/stream-playback-options.spec.ts`.
- Modify `frontend/e2e/stream.spec.ts`, `stream-refresh.spec.ts`, and `accessibility.spec.ts`.
- Modify `documentation/operations/release-inputs.pathspec`.

### Task 1: Extract and normalize the Jellyfin catalog adapter

**Files:**
- Create: `backend/jellyfin_catalog.go`
- Create: `backend/jellyfin_catalog_test.go`
- Create: `backend/media_handlers.go`
- Create: `backend/media_handlers_test.go`
- Modify: `backend/main.go:2387-3072`
- Modify: `backend/main_test.go`

**Interfaces:**
- Consumes: existing Jellyfin token/user resolution, `CatalogRepository`, `JellyfinAuthorizer`, Redis cache, and API error helpers.
- Produces: `JellyfinCatalog`, `MediaLibrary`, `MediaItem`, `MediaDetail`, and focused HTTP handlers.

- [ ] **Step 1: Add characterization tests before moving code**

Lock down current behavior for views, child sorting, folder counts, duration
formatting, item detail 404/502 behavior, cover proxy, refresh lifecycle, and
cache invalidation. Add one test that asserts all browser IDs are canonical and
the fake Jellyfin server receives upstream IDs.

- [ ] **Step 2: Run characterization tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(Media|Jellyfin|Refresh)'
```

Expected: PASS before extraction.

- [ ] **Step 3: Define normalized Stream types**

```go
type MediaLibrary struct {
    ID string `json:"id"`
    Name string `json:"name"`
    Type string `json:"type"`
    CollectionType string `json:"collectionType"`
}

type MediaItem struct {
    ID string `json:"id"`
    Title string `json:"title"`
    Kind CatalogKind `json:"kind"`
    JellyfinType string `json:"type"`
    IsFolder bool `json:"isFolder"`
    ChildCount int `json:"childCount,omitempty"`
    DurationMS int64 `json:"durationMs,omitempty"`
    CoverURL string `json:"coverUrl"`
}

type MediaDetail struct {
    MediaItem
    Overview string `json:"overview"`
    ProductionYear int `json:"year,omitempty"`
    Genres []string `json:"genres"`
    SeriesName string `json:"seriesName,omitempty"`
    SeasonName string `json:"seasonName,omitempty"`
    EpisodeNumber *int `json:"episodeNumber,omitempty"`
    Studios []string `json:"studios"`
    Chapters []MediaChapter `json:"chapters"`
    Progress MemberProgress `json:"progress"`
}

type MediaChapter struct {
    Index int `json:"index"`
    Title string `json:"title"`
    StartMS int64 `json:"startMs"`
}
```

Request Jellyfin fields explicitly:
`Overview,Genres,ProductionYear,ImageTags,BackdropImageTags,Studios,SeriesName,SeasonName,IndexNumber,RunTimeTicks,ChildCount,Chapters`.
Normalize nil slices to empty slices and never relay the raw provider object.

- [ ] **Step 4: Move provider operations without changing routes**

Implement:

```go
type JellyfinCatalog struct {
    repo *CatalogRepository
}

func (c *JellyfinCatalog) Libraries(ctx context.Context) ([]MediaLibrary, error)
func (c *JellyfinCatalog) Children(ctx context.Context, parentID string) ([]MediaItem, error)
func (c *JellyfinCatalog) Detail(ctx context.Context, userID, rawID string) (MediaDetail, CatalogResolution, error)
```

Move `handleMedia`, refresh handlers/state, item list/detail, and cover handler
to `media_handlers.go`. Leave route registration and shared startup state in
`main.go`. Do not move unrelated upload/file/search functions.

When Jellyfin's RefreshLibrary task completes, recursively enumerate each
configured library with bounded pagination, observe every folder/playable item,
then call `CatalogRepository.CompleteScan` for that library. If any page or
child traversal fails, retain the current catalog/cache and do not mark unseen
sources unavailable. Record the bounded reconciliation duration and
success/failure outcome through the Phase 1 metrics helper.

- [ ] **Step 5: Run tests and compare route inventory**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(Media|Jellyfin|Refresh|Catalog)'
rg -n 'GET /api/v1/media|POST /api/v1/media/refresh' backend/main.go
```

Expected: tests PASS and every pre-existing media route appears once.

- [ ] **Step 6: Commit the extraction**

```bash
git add backend/jellyfin_catalog.go backend/jellyfin_catalog_test.go backend/media_handlers.go backend/media_handlers_test.go backend/main.go backend/main_test.go
git commit -m "refactor: isolate jellyfin catalog handlers"
```

### Task 2: Add artwork, richer details, Continue, Recent, and Related

**Files:**
- Modify: `backend/jellyfin_catalog.go`
- Modify: `backend/jellyfin_catalog_test.go`
- Modify: `backend/media_handlers.go`
- Modify: `backend/media_handlers_test.go`
- Modify: `backend/main.go:437-447`

**Interfaces:**
- Consumes: normalized DTOs, `ContinuityRepository.Continue`, and canonical source resolution.
- Produces: detail enrichment plus `handleMediaContinue`, `handleMediaRecent`, `handleMediaRelated`.

- [ ] **Step 1: Write failing shelf/detail translation tests**

Assert these mappings:

```text
GET /api/v1/media/continue           -> Telos member_progress + Jellyfin detail batch
GET /api/v1/media/recent             -> GET /Items/Latest per configured allowed library
GET /api/v1/media/items/{id}/related -> GET /Items/{upstream}/Similar?UserId=...&Limit=12
```

Recent results from multiple libraries are merged by provider `DateCreated`,
deduplicated by canonical ID, capped at 20, and omit active audiobook sources.
Related results are capped at 12 and filtered through library authorization.
No test may observe a call to `/UserItems/Resume` or `/Items/Suggestions`.

- [ ] **Step 2: Run and confirm missing route failures**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'TestMedia(Continue|Recent|Related|Detail)'
```

Expected: FAIL because new handlers and fields are absent.

- [ ] **Step 3: Implement deterministic provider queries**

```go
func (c *JellyfinCatalog) Continue(ctx context.Context, userID string, limit int) ([]MediaDetail, error)
func (c *JellyfinCatalog) Recent(ctx context.Context, userID string, limit int) ([]MediaItem, error)
func (c *JellyfinCatalog) Related(ctx context.Context, userID, rawID string, limit int) ([]MediaItem, error)
```

Continue is driven solely by canonical IDs returned from Telos continuity.
Missing items are omitted with a bounded warning. For cover URLs use only
`/api/v1/media/items/{canonical}/cover`; the browser never builds Jellyfin image
URLs or tags. Any list/detail batch hydrates member progress through
`ContinuityRepository.GetMany`, never a per-card query loop.

- [ ] **Step 4: Register handlers and cache safely**

Register all three routes with `view_media`. Cache provider Recent/Related
results by canonical item/library set and catalog revision for at most five
minutes (`...:<canonical-id>:r<revision>` wherever one item is the cache
subject); merge current member progress after cache retrieval so one member's
position can never enter a shared Redis value.

- [ ] **Step 5: Run focused, Redis, and isolation tests**

```bash
scripts/test-backend.sh all -run 'TestMedia(Continue|Recent|Related|Detail)|Test.*Cache|TestContinuity'
```

Expected: PASS.

- [ ] **Step 6: Commit Stream discovery APIs**

```bash
git add backend/jellyfin_catalog.go backend/jellyfin_catalog_test.go backend/media_handlers.go backend/media_handlers_test.go backend/main.go
git commit -m "feat: enrich stream catalog and shelves"
```

### Task 3: Normalize PlaybackInfo, chapters, tracks, and subtitles

**Files:**
- Create: `backend/jellyfin_playback.go`
- Create: `backend/jellyfin_playback_test.go`
- Modify: `backend/media_handlers.go`
- Modify: `backend/main.go:442-447`

**Interfaces:**
- Consumes: canonical resolution, Jellyfin `/Items/{id}/PlaybackInfo`, and configured shared user ID.
- Produces: `PlaybackOptions`, `PlaybackSelection`, `getPlaybackOptions`, and strict selection validation.

- [ ] **Step 1: Write failing provider-normalization tests**

Use a fixture containing two audio streams, internal/external subtitle streams,
chapters, and one media source. Assert the public response has no Jellyfin media
source ID or URL:

```go
type PlaybackTrack struct {
    Index int `json:"index"`
    Title string `json:"title"`
    Language string `json:"language"`
    Codec string `json:"codec"`
    Default bool `json:"default"`
}

type PlaybackSubtitle struct {
    Index int `json:"index"`
    Title string `json:"title"`
    Language string `json:"language"`
    Codec string `json:"codec"`
    Default bool `json:"default"`
}

type PlaybackOptions struct {
    AudioTracks []PlaybackTrack `json:"audioTracks"`
    Subtitles []PlaybackSubtitle `json:"subtitles"`
    Chapters []MediaChapter `json:"chapters"`
    Progress MemberProgress `json:"progress"`
}
```

- [ ] **Step 2: Run and observe undefined playback types**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run TestPlaybackOptions
```

Expected: FAIL.

- [ ] **Step 3: Implement strict PlaybackInfo decoding**

```go
type PlaybackSelection struct {
    AudioIndex *int
}

func getPlaybackOptions(ctx context.Context, userID, rawID string) (PlaybackOptions, jellyfinPlaybackSource, CatalogResolution, error)
func validatePlaybackSelection(options PlaybackOptions, in PlaybackSelection) error
```

Fetch PlaybackInfo for sources/streams and the authorized item detail for
chapters. Cap media sources at 8, streams at 128, chapters at 512, and each
decoded body at 2 MiB. Reject duplicate indices, unknown selected audio indices,
and a provider transcoding path that is absolute, cross-item, contains userinfo,
or is outside `/Videos/{upstream}/`.

- [ ] **Step 4: Add the public playback-options route**

Register `GET /api/v1/media/items/{id}/playback` with `view_media`. Return the
normalized options and local progress. A no-subtitle/no-chapter item returns
empty arrays and remains playable.

- [ ] **Step 5: Run malformed provider and authorization tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(PlaybackOptions|PlaybackSelection|Authorization|ContentValidation)'
```

Expected: PASS.

- [ ] **Step 6: Commit playback normalization**

```bash
git add backend/jellyfin_playback.go backend/jellyfin_playback_test.go backend/media_handlers.go backend/main.go
git commit -m "feat: expose safe jellyfin playback options"
```

### Task 4: Extend the HLS and subtitle proxy boundary

**Files:**
- Modify: `backend/media.go:230-430`
- Modify: `backend/media_handlers.go`
- Modify: `backend/hls.go`
- Modify: `backend/hls_test.go`
- Modify: `backend/proxy_test.go`
- Modify: `backend/jellyfin_playback_test.go`

**Interfaces:**
- Consumes: `PlaybackSelection`, validated provider source, HLS signing, `proxyRequest`, and capacity control.
- Produces: selected HLS playback and `handleStreamSubtitle`.

- [ ] **Step 1: Write failing selected-playback and subtitle proxy tests**

Assert that only server-validated values can affect these Jellyfin query keys:

```go
"MediaSourceId"
"AudioStreamIndex"
```

Browser `api_key`, `token`, `MediaSourceId`, duplicate selection keys, unknown
indices, alternate hostnames, and arbitrary subtitle formats must be rejected
or dropped. The public subtitle URL contains only canonical ID and numeric
subtitle index.

- [ ] **Step 2: Run proxy/HLS tests and observe missing selection behavior**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(HLS|Subtitle|PlaybackSelection|Proxy)'
```

Expected: FAIL for selected audio/subtitles.

- [ ] **Step 3: Accept a small Telos selection query on the entry route**

`GET /api/v1/stream/video/{id}` accepts only:

```text
audio=<nonnegative stream index>
```

Fetch PlaybackInfo, validate selection, and construct a server-owned upstream
query containing the chosen default media source and audio stream. Resume seeks
through the browser media element after metadata loads; it is not forwarded as
a provider query. Never copy the browser query wholesale.

- [ ] **Step 4: Preserve server-owned selection inside signed HLS locators**

Extend the HLS query filter only for selection keys minted by the gateway.
Signed locators remain bound to upstream item, Telos user, and Telos session;
verification rejects a resource whose item prefix or selection query changes.

- [ ] **Step 5: Implement explicit WebVTT subtitle streaming**

Register:

```text
GET /api/v1/stream/video/{id}/subtitles/{index}.vtt
```

Resolve and authorize the item, refetch PlaybackInfo, validate that subtitle
index, find
the server-owned media source, and proxy only Jellyfin's verified
`/Videos/{item}/{mediaSource}/Subtitles/{index}/Stream.vtt` route. Force
`text/vtt; charset=utf-8`, reject redirects, cap response headers, and acquire a
stream slot.

- [ ] **Step 6: Run security, HLS, proxy, and capacity suites**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -run 'Test.*(HLS|Subtitle|Proxy|Capacity|Security)'
```

Expected: PASS with no upstream token/host/source ID in any recorded browser
response.

- [ ] **Step 7: Commit the playback proxy extension**

```bash
git add backend/media.go backend/media_handlers.go backend/hls.go backend/hls_test.go backend/proxy_test.go backend/jellyfin_playback_test.go
git commit -m "feat: add safe stream track and subtitle selection"
```

### Task 5: Add Stream progress routes and client shelf/detail state

**Files:**
- Modify: `backend/media_handlers.go`
- Modify: `backend/media_handlers_test.go`
- Create: `backend/media_progress_integration_test.go`
- Modify: `backend/main.go:437-447`
- Modify: `frontend/src/stores/useMediaStore.ts`
- Create: `frontend/src/stores/useMediaStore.test.ts`
- Create: `frontend/src/components/stream/MediaShelf.tsx`
- Create: `frontend/src/components/stream/MediaShelf.test.tsx`
- Create: `frontend/src/components/stream/StreamItemDetail.tsx`
- Create: `frontend/src/components/stream/StreamItemDetail.test.tsx`

**Interfaces:**
- Consumes: continuity repository and enriched Stream APIs.
- Produces: progress handlers, normalized browser store, deterministic shelves, and accessible detail surface.

- [ ] **Step 1: Write failing progress isolation and store tests**

Register the intended contract in tests:

```text
GET /api/v1/media/items/{id}/progress
PUT /api/v1/media/items/{id}/progress
```

Two-member integration fixtures must prove independent positions and Continue
shelves. Frontend tests must prove empty Continue is omitted, recent/related
labels are transparent, real `coverUrl` is used, and an image error swaps to a
deterministic accessible placeholder.

- [ ] **Step 2: Run focused backend/frontend tests**

```bash
scripts/test-backend.sh all -run 'TestMediaProgress|TestMediaContinueIsolation'
cd frontend
npx vitest run src/stores/useMediaStore.test.ts src/components/stream/MediaShelf.test.tsx src/components/stream/StreamItemDetail.test.tsx
```

Expected: FAIL because routes/components do not exist.

- [ ] **Step 3: Add canonical Stream progress handlers**

Resolve `SurfaceStream`, reject `folder`, validate kind `video` or `audio`, and
call continuity `Get`/`Put`. Return the same `MemberProgress` JSON used by
Library. A save failure uses structured API errors but does not mutate provider
playstate.

- [ ] **Step 4: Refactor the media store around normalized entities**

Add `itemsById`, `continueIds`, `recentIds`, `relatedIdsByItem`, `selectedItemID`,
and explicit loaders. Preserve refresh rollback behavior and path navigation.
When a refreshed folder/item is missing, use canonical IDs to make the same
decisions as current tests.

- [ ] **Step 5: Replace synthetic posters and add item details**

Use real cover URLs, lazy loading, fixed aspect ratio, and a deterministic
fallback only after image failure. Selecting a folder navigates; selecting a
leaf opens `StreamItemDetail` with artwork, overview, year, genres, series/
episode context, duration, progress, commentary, Related shelf, and Play/
Continue. Manage focus identically to the Library detail component.

- [ ] **Step 6: Run store/component/lint tests**

```bash
cd frontend
npm run lint
npx vitest run src/stores/useMediaStore.test.ts src/components/stream/MediaShelf.test.tsx src/components/stream/StreamItemDetail.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit Stream browse/detail UX**

```bash
git add backend/media_handlers.go backend/media_handlers_test.go backend/media_progress_integration_test.go backend/main.go frontend/src/stores/useMediaStore.ts frontend/src/stores/useMediaStore.test.ts frontend/src/components/stream/MediaShelf.tsx frontend/src/components/stream/MediaShelf.test.tsx frontend/src/components/stream/StreamItemDetail.tsx frontend/src/components/stream/StreamItemDetail.test.tsx
git commit -m "feat: add stream details and continuity shelves"
```

### Task 6: Add resume, chapters, audio tracks, and subtitles to HLS player

**Files:**
- Modify: `frontend/src/components/stream/HlsPlayer.tsx`
- Create: `frontend/src/components/stream/HlsPlayer.test.tsx`
- Modify: `frontend/src/components/stream/StreamItemDetail.tsx`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/styles/stream.css`
- Reuse: `frontend/src/hooks/useMediaProgress.ts`

**Interfaces:**
- Consumes: PlaybackOptions, selected HLS entry route, VTT subtitle route, and `useMediaProgress`.
- Produces: full selected playback and local resume behavior.

- [ ] **Step 1: Write media-element and fake-timer tests**

Mock HLS.js and HTMLMediaElement. Assert:

- a meaningful saved position offers Continue or Start Over;
- Continue seeks after metadata and Start Over begins at zero;
- audio selection rebuilds the HLS URL using an integer public selection and
  resumes current time;
- subtitle selection mounts the explicit canonical VTT route without rebuilding
  HLS, and Off removes the subtitle track;
- chapter selection seeks to `startMs`;
- pause/seek/stop/ended and the 15-second interval call `useMediaProgress`;
- selected track labels and subtitle Off state are keyboard accessible;
- video with empty options still plays.

- [ ] **Step 2: Run the player test and capture missing-control failures**

```bash
cd frontend
npx vitest run src/components/stream/HlsPlayer.test.tsx
```

Expected: FAIL because the player accepts only `src` and `audio`.

- [ ] **Step 3: Replace the player contract**

```ts
interface HlsPlayerProps {
  itemId: string;
  audio: boolean;
  options: PlaybackOptions;
  onStop(): void;
}
```

Construct entry URLs with `URLSearchParams` from the selected audio index. For
native HLS and HLS.js, destroy/detach the old source before loading the new
source, then seek to the retained browser time after metadata loads. Never reuse
or display provider URLs.

- [ ] **Step 4: Add resume and playback controls**

Show the resume decision only when progress is incomplete and greater than 30
seconds. Render chapter buttons, audio select, subtitle select with Off,
existing playback rates, and Picture-in-Picture. Mount `<track kind="subtitles"
src="/api/v1/stream/video/{id}/subtitles/{index}.vtt">` only for the selected
subtitle.

- [ ] **Step 5: Integrate local progress and detail-to-player transition**

Use one latest-payload progress hook. Mark completed only on `ended`; Stop saves
the current incomplete position before returning to detail. Preserve commentary
on the stable media item while playing.

- [ ] **Step 6: Run player, regression, and lint suites**

```bash
cd frontend
npm run lint
npx vitest run src/components/stream/HlsPlayer.test.tsx src/hooks/useMediaProgress.test.ts
```

Expected: PASS.

- [ ] **Step 7: Commit selected playback UI**

```bash
git add frontend/src/components/stream/HlsPlayer.tsx frontend/src/components/stream/HlsPlayer.test.tsx frontend/src/components/stream/StreamItemDetail.tsx frontend/src/app/'(shell)'/stream/page.tsx frontend/src/styles/stream.css
git commit -m "feat: add stream resume and playback options"
```

### Task 7: Certify Stream behavior and accessibility

**Files:**
- Create: `frontend/e2e/stream-playback-options.spec.ts`
- Modify: `frontend/e2e/stream.spec.ts`
- Modify: `frontend/e2e/stream-refresh.spec.ts`
- Modify: `frontend/e2e/accessibility.spec.ts`
- Modify: `documentation/operations/release-inputs.pathspec`

**Interfaces:**
- Consumes: complete backend and frontend Stream phase.
- Produces: browser certification and tracked release inventory.

- [ ] **Step 1: Add deterministic E2E fixtures/assertions**

Use seeded local media with artwork, two audio streams, one WebVTT subtitle,
and chapter metadata. Cover:

1. real artwork and fallback;
2. rich details and Related label;
3. Continue and Start Over;
4. two accounts with isolated progress;
5. audio-track switch preserving time;
6. subtitle selection and Off;
7. chapter seek;
8. pause/reload resume;
9. refresh preserving canonical navigation and removing absent items;
10. no Jellyfin host/token/source ID in DOM, network URLs, or responses;
11. phone portrait/landscape and keyboard accessibility.

- [ ] **Step 2: Update release inventory**

Add all new Go/TypeScript/runtime test paths from this plan to
`documentation/operations/release-inputs.pathspec`.

- [ ] **Step 3: Run backend, frontend, and focused E2E checks**

```bash
scripts/verify-clean-checkout.sh --inventory-only
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend
npm run lint
npx vitest run
npx playwright test stream.spec.ts stream-refresh.spec.ts stream-playback-options.spec.ts accessibility.spec.ts
```

Expected: all commands PASS.

- [ ] **Step 4: Commit Stream certification**

```bash
git add frontend/e2e/stream-playback-options.spec.ts frontend/e2e/stream.spec.ts frontend/e2e/stream-refresh.spec.ts frontend/e2e/accessibility.spec.ts documentation/operations/release-inputs.pathspec
git commit -m "test: certify enriched stream playback"
```

## Phase 3 completion gate

The phase is complete when Stream uses real artwork/details, deterministic
Continue/Recent/Related shelves, independent Telos resume, chapters, audio
tracks, and subtitles; all HLS/subtitle requests remain canonical,
session-bound, capacity-limited, token-free, and authorized against stable
Jellyfin library IDs.
