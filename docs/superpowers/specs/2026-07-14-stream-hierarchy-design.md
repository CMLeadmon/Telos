# Stream Page Media Hierarchy — Design

Date: 2026-07-14
Status: Approved

## Problem

Shows and audiobooks in the stream page are not stored under their parent — every
episode and every audiobook track appears as a directly-playable poster alongside
movies. E.g. clicking a "King of the Hill" poster plays the pilot immediately;
there is no way to browse "King of the Hill" as a show and pick an episode.

## Root cause

`handleMediaItems` (backend/main.go:1614) queries Jellyfin with
`Recursive=true&IncludeItemTypes=Movie,Episode,Audio,Audiobook`. `Recursive=true`
flattens the entire Series → Season → Episode (and Book → Chapter) tree into one
list of leaves per library. Jellyfin already tracks the hierarchy; Telos is
discarding it at the proxy layer.

This assumes Jellyfin libraries are typed as TV Shows / Music-Audiobooks so
Jellyfin recognizes the Series/Season/Book structure from folder layout. If a
library's files aren't organized in a way Jellyfin can parse into that
hierarchy, this design doesn't fix it — that would be a Jellyfin library
configuration issue, not a Telos gateway issue.

## Scope

In: drill-down browsing (Library → Series → Season → Episode, Library → Book →
Chapter), breadcrumb navigation, adaptive Open/Play affordance, backend query
change, store/UI rework, tests.

Out: mock fallback data for media (none exists today, none is being added — a
Jellyfin outage still 503s, matching current behavior), "continue watching" /
resume position, watch party, "my list".

## Design

### 1. Backend — `handleMediaItems` (main.go)

Replace the flattening query with a direct-children query:

```
- ?ParentId=%s&Recursive=true&IncludeItemTypes=Movie,Episode,Audio,Audiobook
+ ?ParentId=%s&SortBy=IndexNumber,SortName&SortOrder=Ascending&Fields=ChildCount
```

Dropping `Recursive=true` and the `IncludeItemTypes` filter makes Jellyfin
return exactly the direct children of any `parentId`: a TV library's children
are `Series`; a `Series`'s children are `Season`s; a `Season`'s children are
`Episode`s; a Movies library's children are `Movie`s (already leaves — no
behavior change for movies); an audiobook library's children are book/author
folders, whose children are `Audio` tracks. `SortBy=IndexNumber,SortName` keeps
seasons/episodes/chapters in order while unordered items (series, books) sort
by name.

Response shape changes:
- Jellyfin decode struct gains `IsFolder bool` and `ChildCount int json:"ChildCount"`.
- `MediaPlayableItem` gains `IsFolder bool json:"isFolder"` and
  `ChildCount int json:"childCount,omitempty"`.
- Extract the per-item mapping (duration formatting + isFolder/childCount) into
  a pure helper function `mapJellyfinChildren(items []jellyfinChild) []MediaPlayableItem`
  so it is unit-testable without a live Jellyfin.

Redis caching is unchanged: it's already keyed per `parentId`
(`telos:jellyfin:library-items:%s`), so every level of the hierarchy gets its
own 5-minute cache entry for free.

`handleMedia` (the libraries/views endpoint) is unchanged — libraries are
already the top of the tree.

### 2. Store — `useMediaStore.ts`

- `MediaItem` gains `isFolder: boolean` and `childCount?: number`.
- `itemsByLibrary` is renamed `itemsByParent` — a library is just the top-level
  parent; the same map now holds children for any parentId in the drill path.
  `itemsStatus` is renamed `itemsStatusByParent` for the same reason.
- New navigation state:
  - `path: { id: string; title: string }[]` — empty means "home" (browsing all
    libraries as rows); non-empty means "drilled into `path[path.length - 1]`".
  - `rootLibrary: MediaLibrary | null` — the library the current drill started
    from, carried along so a leaf item several levels deep can still be
    classified audio vs video and routed to the right stream endpoint.
- New/changed actions:
  - `open(item: MediaItem, library: MediaLibrary)` — if `item.isFolder`, sets
    `rootLibrary` (on first drill) or keeps it, pushes `{id: item.id, title:
    item.title}` onto `path`, and lazily fetches that id's children via the
    existing `fetchItems`-style logic (now keyed by arbitrary parent id, not
    just library id). If not a folder, delegates to `play(item, rootLibrary ??
    library)`.
  - `navigateTo(index: number)` — sets `path` to `path.slice(0, index + 1)`,
    i.e. clicking breadcrumb segment `path[i]` keeps everything up to and
    including that segment. `index === -1` empties `path` and clears
    `rootLibrary` (home).
  - `goHome()` — `navigateTo(-1)`.
  - `play`, `stop` — unchanged behavior, only the type of the second arg
    (`library`) is now always the drill's `rootLibrary`.
  - `fetchItems(parentId)` — same fetch, now writes into `itemsByParent` /
    `itemsStatusByParent` instead of the library-keyed maps.

### 3. Frontend UI — `stream/page.tsx`

- **Home (`path` empty):** unchanged hero + per-library rows, except posters
  now render the *direct children* returned by the new query (Series / Books /
  Movies, not raw episodes/tracks). Poster click calls `open(item, lib)`
  instead of `play(item, lib)`. A folder poster's meta line shows
  `${childCount} season${s}` / `${childCount} chapter${s}` (derived from
  `item.type`) instead of duration; a leaf poster keeps today's
  `duration · type`. The hero's primary CTA reads **"Open"** and calls `open`
  when the featured item is a folder, **"Play"** and calls `play` when it's a
  leaf — same button, same position, label/handler swap on `featured.isFolder`.
- **Drilled (`path` non-empty):** a breadcrumb bar above the grid — `Home /
  King of the Hill / Season 1`, each segment clickable via `navigateTo`. Below
  it, one `.posters` grid (reusing existing styles) of the current parent's
  children, same folder/leaf click behavior as home. No hero section while
  drilled — it only makes sense for the top-level "featured" concept.
- Movies remain one-click-to-play: their children from the new query are
  already leaves (`IsFolder: false`), so `open()` on a movie immediately calls
  `play()` — no UI branch needed, this falls out of the generic folder/leaf
  handling.

### 4. Error handling / edge cases

- Empty folder (e.g. a season with no aired episodes yet): render "nothing
  here yet" in place of the grid, matching the existing empty-library tone.
- Per-parent fetch error: render "unavailable" in the grid, same as today's
  per-library error state. No mock fallback is introduced — this matches
  current behavior for `handleMedia`/`handleMediaItems`, which is the one
  proxy surface without a mock (unlike chat's Redis-down mock fallback).
- Leaf classification for playback: `item.type === "Episode"` → video,
  `item.type === "Audio"` → audio, with `rootLibrary.type` as the backstop for
  ambiguous types (unchanged `isAudioItem` logic, now fed by `rootLibrary`
  instead of the immediate `lib` argument since that may be several levels
  removed from the clicked poster).
- Stopping playback (`stop()`) does not touch `path`, so the user returns to
  wherever they were browsing, not back to home.

### 5. Testing

- **Go:** unit test `mapJellyfinChildren` directly (table-driven: folder vs
  leaf, duration formatting incl. the 0-duration folder case, childCount
  passthrough) — no network needed. Optionally add a handler-level test that
  points the package-level `jellyfinBaseURL` var at an `httptest.Server`
  stubbing a Series → Season → Episode response, asserting `handleMediaItems`
  passes through `isFolder`/`childCount` end to end.
- **Frontend:** new `frontend/e2e/stream.spec.ts`, credentialed and skipped
  without `E2E_USERNAME`/`E2E_PASSWORD` (matching `library.spec.ts`'s
  pattern): open `/stream/`, click a known series poster, assert a breadcrumb
  appears and season posters render, click a season, assert episode posters
  render, click an episode, assert `stream-video` appears and playback starts.
  New `data-testid`s: `stream-crumbs` on the breadcrumb bar; folder vs leaf
  posters distinguished by existing `data-testid`-free click targets (no new
  testid needed there — the spec asserts on breadcrumb text and poster count
  instead).
- **Manual:** `podman-compose up -d --build`, drill King of the Hill → Season
  1 → Pilot and confirm it plays; drill an audiobook → a chapter and confirm
  audio playback; watch `podman logs telos-core` for unexpected Jellyfin
  fallback/error logging during the drill.
