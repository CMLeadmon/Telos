# Stream Page Media Hierarchy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the stream page browse shows and audiobooks as a hierarchy (Library → Series → Season → Episode, Library → Book → Chapter) instead of flattening every episode/track into one poster row, so a leaf like "King of the Hill: Pilot" is reached by drilling into the show first.

**Architecture:** The gateway's `/api/v1/media/items` endpoint currently asks Jellyfin for the full recursive subtree of a library. Dropping `Recursive=true` makes Jellyfin return just the direct children of whatever `parentId` is requested, at every level. The frontend media store gains a navigation stack (`path`) and re-fetches children each time the user opens a folder; the stream page renders a breadcrumb bar + grid when drilled, or the existing hero + library rows at the top. The Library page's audiobook shelf, which also reads from this store, is updated to fetch one level deeper per book so it keeps listing playable chapters instead of book folders.

**Tech Stack:** Go 1.22 (gateway), Next.js 16 / React 19 / Zustand 5 (frontend), Jellyfin HTTP API, Playwright (e2e).

## Global Constraints

- Go is not installed on the host — run all `go` commands in the container: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go <cmd>`.
- The frontend has **no JS unit test framework** (no jest/vitest) — only `eslint`, `next build` (which runs `tsc`), and Playwright e2e. Frontend tasks below are verified by lint + build, and functionally proven by the Playwright spec in the final task plus manual verification.
- Credentialed Playwright specs (`E2E_USERNAME`/`E2E_PASSWORD`) require `npm run dev` running first (no `webServer` in `playwright.config.ts`) and race if run in the same parallel batch as other credentialed specs — run `npx playwright test stream` in isolation.
- Never hardcode secrets; all config comes from `.env`.
- Never link/compile Jellyfin or Grimmory source into the Go gateway — this plan only talks to Jellyfin over its HTTP API, which is already the case.
- Follow existing code style exactly: Go section-banner comments (`// ═══...`) already present in `main.go`/`main_test.go`; React components as plain function components with no new state libraries.

---

### Task 1: Backend — stop flattening `/api/v1/media/items`

**Files:**
- Modify: `backend/main.go:1580-1684` (the `MediaPlayableItem` struct and `handleMediaItems`)
- Test: `backend/main_test.go` (new tests, inserted after the existing `TestGetJellyfinUserIDPrefersConfiguredUser` at line 315, before the `// WebSocket Origin Policy Tests` banner)

**Interfaces:**
- Produces: `MediaPlayableItem{ID, Title, Duration, Type, IsFolder, ChildCount}` (JSON: `id,title,duration,type,isFolder,childCount`) — the wire shape every later frontend task consumes.
- Produces: `mapJellyfinChildren(items []jellyfinChildItem) []MediaPlayableItem` — pure function, no network/Redis dependency.
- Produces: `handleMediaItems` now issues Jellyfin's `ParentId` query **without** `Recursive=true`, returning direct children only.

- [ ] **Step 1: Write the failing unit test for `mapJellyfinChildren`**

Insert into `backend/main_test.go` after line 315 (right after `TestGetJellyfinUserIDPrefersConfiguredUser`'s closing `}`, before the `// WebSocket Origin Policy Tests` banner comment):

```go
// ═══════════════════════════════════════════════════════════════════════════
// Jellyfin Item Hierarchy Tests
// ═══════════════════════════════════════════════════════════════════════════

func TestMapJellyfinChildren(t *testing.T) {
	cases := []struct {
		name  string
		input jellyfinChildItem
		want  MediaPlayableItem
	}{
		{
			name: "folder with children has no duration",
			input: jellyfinChildItem{
				ID: "series-1", Name: "King of the Hill", Type: "Series",
				IsFolder: true, ChildCount: 13,
			},
			want: MediaPlayableItem{
				ID: "series-1", Title: "King of the Hill", Duration: "0m",
				Type: "Series", IsFolder: true, ChildCount: 13,
			},
		},
		{
			name: "leaf under an hour shows minutes only",
			input: jellyfinChildItem{
				ID: "ep-1", Name: "Pilot", Type: "Episode",
				RunTimeTicks: 13_000_000_000, IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "ep-1", Title: "Pilot", Duration: "21m",
				Type: "Episode", IsFolder: false,
			},
		},
		{
			name: "leaf over an hour shows hours and minutes",
			input: jellyfinChildItem{
				ID: "movie-1", Name: "Office Space", Type: "Movie",
				RunTimeTicks: 54_000_000_000, IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "movie-1", Title: "Office Space", Duration: "1h 30m",
				Type: "Movie", IsFolder: false,
			},
		},
		{
			name: "zero runtime leaf falls back to 0m",
			input: jellyfinChildItem{
				ID: "track-1", Name: "Chapter 1", Type: "Audio", IsFolder: false,
			},
			want: MediaPlayableItem{
				ID: "track-1", Title: "Chapter 1", Duration: "0m",
				Type: "Audio", IsFolder: false,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapJellyfinChildren([]jellyfinChildItem{tc.input})
			if len(got) != 1 {
				t.Fatalf("mapJellyfinChildren returned %d items, want 1", len(got))
			}
			if got[0] != tc.want {
				t.Errorf("mapJellyfinChildren(%+v) = %+v, want %+v", tc.input, got[0], tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails to compile**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./... -run TestMapJellyfinChildren`
Expected: FAIL — `undefined: jellyfinChildItem` / `undefined: mapJellyfinChildren` / `MediaPlayableItem` has no field `IsFolder` (compile error, not a runtime failure — that's expected at this stage).

- [ ] **Step 3: Add `IsFolder`/`ChildCount` to `MediaPlayableItem` and extract `mapJellyfinChildren`**

Replace `backend/main.go:1580-1584`:

```go
type MediaPlayableItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Duration string `json:"duration"`
	Type     string `json:"type"`
}
```

with:

```go
type MediaPlayableItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Duration   string `json:"duration"`
	Type       string `json:"type"`
	IsFolder   bool   `json:"isFolder"`
	ChildCount int    `json:"childCount,omitempty"`
}

type jellyfinChildItem struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RunTimeTicks int64  `json:"RunTimeTicks"`
	Type         string `json:"Type"`
	IsFolder     bool   `json:"IsFolder"`
	ChildCount   int    `json:"ChildCount"`
}

// mapJellyfinChildren converts Jellyfin's raw child items (the direct
// children of a ParentId — a library's series, a series' seasons, a
// season's episodes, an audiobook's chapters, etc.) into the gateway's
// wire format.
func mapJellyfinChildren(items []jellyfinChildItem) []MediaPlayableItem {
	result := make([]MediaPlayableItem, 0, len(items))
	for _, item := range items {
		durationStr := ""
		if item.RunTimeTicks > 0 {
			seconds := item.RunTimeTicks / 10000000
			h := seconds / 3600
			m := (seconds % 3600) / 60
			if h > 0 {
				durationStr = fmt.Sprintf("%dh %dm", h, m)
			} else {
				durationStr = fmt.Sprintf("%dm", m)
			}
		} else {
			durationStr = "0m"
		}

		result = append(result, MediaPlayableItem{
			ID:         item.ID,
			Title:      item.Name,
			Duration:   durationStr,
			Type:       item.Type,
			IsFolder:   item.IsFolder,
			ChildCount: item.ChildCount,
		})
	}
	return result
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./... -run TestMapJellyfinChildren -v`
Expected: PASS for all 4 subtests.

- [ ] **Step 5: Write the failing integration test for `handleMediaItems`**

Insert immediately after the `TestMapJellyfinChildren` function from Step 1:

```go
func TestHandleMediaItemsReturnsDirectChildren(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/Users":
			json.NewEncoder(w).Encode([]map[string]string{{"Id": "user-1", "Name": "admin"}})
		case r.URL.Path == "/Users/user-1/Items":
			if got := r.URL.Query().Get("ParentId"); got != "series-1" {
				t.Errorf("ParentId = %q, want series-1", got)
			}
			if got := r.URL.Query().Get("Recursive"); got != "" {
				t.Errorf("Recursive = %q, want unset — recursion must stay off so only direct children come back", got)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"Items": []map[string]any{
					{"Id": "season-1", "Name": "Season 1", "Type": "Season", "IsFolder": true, "ChildCount": 13},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldBase := jellyfinBaseURL
	jellyfinBaseURL = srv.URL
	defer func() { jellyfinBaseURL = oldBase }()

	oldRedis := redisClient
	redisClient = nil
	defer func() { redisClient = oldRedis }()

	req := httptest.NewRequest("GET", "/api/v1/media/items?parentId=series-1", nil)
	rr := httptest.NewRecorder()
	handleMediaItems(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got []MediaPlayableItem
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	if !got[0].IsFolder || got[0].ChildCount != 13 || got[0].Title != "Season 1" {
		t.Errorf("got %+v, want a Season 1 folder with ChildCount 13", got[0])
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./... -run TestHandleMediaItemsReturnsDirectChildren -v`
Expected: FAIL — the live query still sends `Recursive=true` and `IncludeItemTypes=...`, so the `Recursive` assertion fails (and the returned item won't carry `IsFolder`/`ChildCount` since the old handler didn't decode or map them).

- [ ] **Step 7: Change the Jellyfin query and wire `mapJellyfinChildren` into the handler**

Replace `backend/main.go:1613-1671` (from `token := getJellyfinAdminToken()` through the closing `}` of the `for _, item := range jResp.Items` loop) with:

```go
	token := getJellyfinAdminToken()
	reqURL := fmt.Sprintf("%s/Users/%s/Items?ParentId=%s&SortBy=IndexNumber,SortName&SortOrder=Ascending&Fields=ChildCount", jellyfinBaseURL, userID, parentId)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Jellyfin Items request failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Jellyfin Items returned error: "+resp.Status, http.StatusServiceUnavailable)
		return
	}

	var jResp struct {
		Items []jellyfinChildItem `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jResp); err != nil {
		http.Error(w, "Failed to decode items response", http.StatusInternalServerError)
		return
	}

	items := mapJellyfinChildren(jResp.Items)
```

The line right after this block, `respJSON, err := json.Marshal(items)`, already exists and needs no change — `items` is now populated by `mapJellyfinChildren` instead of the old inline loop.

- [ ] **Step 8: Run both new tests and the full backend suite**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./... -v`
Expected: PASS, including `TestMapJellyfinChildren`, `TestHandleMediaItemsReturnsDirectChildren`, and every pre-existing test (no regressions).

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go vet ./...`
Expected: no output (clean).

- [ ] **Step 9: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "$(cat <<'EOF'
feat(media): return direct children instead of flattening recursive Jellyfin items

handleMediaItems queried Jellyfin with Recursive=true, so every episode
and audiobook track came back flattened as a sibling of its show/book.
Drop Recursive and IncludeItemTypes so each ParentId query returns only
its direct children (Series -> Season -> Episode, Book -> Chapter),
carrying IsFolder/ChildCount so the frontend can drill down.
EOF
)"
```

---

### Task 2: Frontend store — hierarchical navigation state

**Files:**
- Modify: `frontend/src/stores/useMediaStore.ts` (full-file rewrite; the file is ~100 lines)

**Interfaces:**
- Consumes: `MediaPlayableItem` JSON shape from Task 1 (`id,title,duration,type,isFolder,childCount`).
- Produces: `MediaItem{id,title,duration,type,isFolder,childCount?}`, `MediaCrumb{id,title}`.
- Produces: state fields `itemsByParent: Record<string, MediaItem[]>`, `itemsStatusByParent: Record<string, LoadStatus>`, `path: MediaCrumb[]`, `rootLibrary: MediaLibrary | null` (renamed from `itemsByLibrary`/`itemsStatus` — every consumer must be updated, see Task 3 and Task 4).
- Produces: `open(item: MediaItem, library: MediaLibrary): void`, `navigateTo(index: number): void`, `goHome(): void` — `open` plays leaves and drills into folders; `navigateTo(-1)` clears the drill; `navigateTo(i)` truncates `path` to `path.slice(0, i + 1)`.
- Produces (unchanged behavior, same signatures): `fetchLibraries()`, `fetchItems(parentId: string)`, `selectLibrary(id)`, `play(item, library)`, `stop()`.

- [ ] **Step 1: Rewrite the store**

Replace the full contents of `frontend/src/stores/useMediaStore.ts` with:

```ts
import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface MediaLibrary {
  id: string;
  name: string;
  type: "video" | "audio";
  collectionType?: string;
}

export interface MediaItem {
  id: string;
  title: string;
  duration: string;
  type: string; // Jellyfin item type: Series, Season, Movie, Episode, Audio, Audiobook, ...
  isFolder: boolean;
  childCount?: number;
}

export interface MediaCrumb {
  id: string;
  title: string;
}

type LoadStatus = "idle" | "loading" | "ready" | "error";

export interface NowPlaying {
  item: MediaItem;
  library: MediaLibrary;
}

interface MediaState {
  libraries: MediaLibrary[];
  libraryStatus: LoadStatus;
  error: string | null;
  activeLibraryId: string | null;
  itemsByParent: Record<string, MediaItem[]>;
  itemsStatusByParent: Record<string, LoadStatus>;
  path: MediaCrumb[];
  rootLibrary: MediaLibrary | null;
  nowPlaying: NowPlaying | null;
  fetchLibraries: () => Promise<void>;
  fetchItems: (parentId: string) => Promise<void>;
  selectLibrary: (id: string) => void;
  open: (item: MediaItem, library: MediaLibrary) => void;
  navigateTo: (index: number) => void;
  goHome: () => void;
  play: (item: MediaItem, library: MediaLibrary) => void;
  stop: () => void;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

export const useMediaStore = create<MediaState>()((set, get) => ({
  libraries: [],
  libraryStatus: "idle",
  error: null,
  activeLibraryId: null,
  itemsByParent: {},
  itemsStatusByParent: {},
  path: [],
  rootLibrary: null,
  nowPlaying: null,

  fetchLibraries: async () => {
    if (get().libraryStatus === "loading") return;
    set({ libraryStatus: "loading", error: null });
    try {
      const libraries = asList(await api<MediaLibrary[] | null>("/api/v1/media"));
      set((s) => ({
        libraries,
        libraryStatus: "ready",
        activeLibraryId: s.activeLibraryId ?? libraries[0]?.id ?? null,
      }));
      await Promise.all(libraries.map((lib) => get().fetchItems(lib.id)));
    } catch (err) {
      set({
        libraryStatus: "error",
        error:
          err instanceof ApiError && err.status === 503
            ? "jellyfin unreachable — the node is running degraded"
            : "could not load media libraries",
      });
    }
  },

  fetchItems: async (parentId) => {
    if (get().itemsStatusByParent[parentId] === "loading") return;
    set((s) => ({
      itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "loading" },
    }));
    try {
      const items = asList(
        await api<MediaItem[] | null>(
          `/api/v1/media/items?parentId=${encodeURIComponent(parentId)}`,
        ),
      );
      set((s) => ({
        itemsByParent: { ...s.itemsByParent, [parentId]: items },
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "ready" },
      }));
    } catch {
      set((s) => ({
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "error" },
      }));
    }
  },

  selectLibrary: (id) => set({ activeLibraryId: id }),

  open: (item, library) => {
    if (!item.isFolder) {
      get().play(item, library);
      return;
    }
    set((s) => ({
      rootLibrary: s.rootLibrary ?? library,
      path: [...s.path, { id: item.id, title: item.title }],
    }));
    void get().fetchItems(item.id);
  },

  navigateTo: (index) => {
    if (index < 0) {
      set({ path: [], rootLibrary: null });
      return;
    }
    set((s) => ({ path: s.path.slice(0, index + 1) }));
  },

  goHome: () => get().navigateTo(-1),

  play: (item, library) => set({ nowPlaying: { item, library } }),

  stop: () => set({ nowPlaying: null }),
}));
```

- [ ] **Step 2: Confirm the two existing consumers now fail to type-check (expected — fixed in Tasks 3 and 4)**

Run (from `frontend/`): `npm run build`
Expected: FAIL — `frontend/src/app/(shell)/library/page.tsx` and `frontend/src/app/(shell)/stream/page.tsx` reference the removed `itemsByLibrary`/`itemsStatus` fields. This confirms the rename actually changed the type surface; both are fixed in the next two tasks.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/stores/useMediaStore.ts
git commit -m "$(cat <<'EOF'
feat(media): add hierarchical navigation state to media store

Rename itemsByLibrary/itemsStatus to itemsByParent/itemsStatusByParent
since a "parent" is now any folder in the tree, not just a library.
Add path/rootLibrary navigation state and open()/navigateTo()/goHome()
actions: open() plays leaf items and drills into folder items.
EOF
)"
```

Note: this intentionally leaves the build red until Task 3 and Task 4 land — both are small, immediate follow-ups in this same plan, not a shippable checkpoint on their own.

---

### Task 3: Frontend — fix the Library page's audiobook shelf

**Files:**
- Modify: `frontend/src/app/(shell)/library/page.tsx:12,16-72`

**Interfaces:**
- Consumes: `useMediaStore()` fields `libraries`, `libraryStatus`, `itemsByParent`, `itemsStatusByParent`, `fetchLibraries`, `fetchItems` (from Task 2) and the `MediaItem` type (`id,title,duration,isFolder`).
- Produces: no change to the module's public shape — `AudiobookShelf` is still a self-contained component rendered from `LibraryPage`.

Why this task exists: `AudiobookShelf` previously relied on the flattened `itemsByLibrary[lib.id]` to list every audio track directly under an audiobook library. After Task 1, a library's direct children are book folders, not tracks — Task 2's rename plus Task 1's de-flattening would otherwise silently break this shelf (it would list unplayable book folders instead of chapters). Fix: fetch one level deeper per book folder, matching the "Book → Chapters" drill depth already established for the stream page.

- [ ] **Step 1: Replace the `AudiobookShelf` function and its import**

In `frontend/src/app/(shell)/library/page.tsx`, change line 12 from:

```tsx
import { useMediaStore } from "@/stores/useMediaStore";
```

to:

```tsx
import { useMediaStore, type MediaItem } from "@/stores/useMediaStore";
```

Then replace the whole `AudiobookShelf` function, `frontend/src/app/(shell)/library/page.tsx:16-72` (from `function AudiobookShelf() {` through its closing `}`), with:

```tsx
function AudiobookRow({
  item,
  playing,
  onToggle,
}: {
  item: MediaItem;
  playing: boolean;
  onToggle: () => void;
}) {
  return (
    <div className="audiorow">
      <Music size={15} />
      <span className="aname">{item.title}</span>
      <span className="ameta">{item.duration}</span>
      <button
        className="btn-ghost btn-sm"
        aria-label={`play ${item.title}`}
        onClick={onToggle}
      >
        {playing ? "playing" : "play"}
      </button>
      {playing && (
        <audio
          controls
          autoPlay
          data-testid="audiobook-player"
          src={`${apiBase()}/api/v1/stream/audio/${encodeURIComponent(item.id)}`}
        />
      )}
    </div>
  );
}

// A shelf library's direct children are now book folders (Task 1 dropped
// Jellyfin's Recursive=true), so each book needs its own one-level-deeper
// fetch to surface its playable chapters.
function AudiobookBook({
  book,
  playingId,
  onToggle,
}: {
  book: MediaItem;
  playingId: string | null;
  onToggle: (id: string) => void;
}) {
  const { itemsByParent, itemsStatusByParent, fetchItems } = useMediaStore();

  useEffect(() => {
    if (book.isFolder && itemsStatusByParent[book.id] === undefined) {
      void fetchItems(book.id);
    }
  }, [book.id, book.isFolder, itemsStatusByParent, fetchItems]);

  const tracks = book.isFolder ? (itemsByParent[book.id] ?? []) : [book];
  return (
    <>
      {tracks.map((item) => (
        <AudiobookRow
          key={item.id}
          item={item}
          playing={playingId === item.id}
          onToggle={() => onToggle(item.id)}
        />
      ))}
    </>
  );
}

function AudiobookShelf() {
  const { libraries, libraryStatus, itemsByParent, fetchLibraries } =
    useMediaStore();
  const [playingId, setPlayingId] = useState<string | null>(null);

  useEffect(() => {
    if (libraryStatus === "idle") void fetchLibraries();
  }, [libraryStatus, fetchLibraries]);

  // Jellyfin (verified live on 10.11.11) reports no CollectionType at all
  // for "books"-typed libraries via /Users/{id}/Views, and a plain "music"
  // collectionType is indistinguishable from a real music library — so name
  // is the only reliable signal here. Admins name the Jellyfin library with
  // "audiobook" in it (e.g. "Audiobooks").
  const shelves = libraries.filter(
    (lib) => lib.type === "audio" && lib.name.toLowerCase().includes("audiobook"),
  );
  if (shelves.length === 0) return null;

  return (
    <div className="audioshelf">
      {shelves.map((lib) => {
        const books = itemsByParent[lib.id] ?? [];
        if (books.length === 0) return null;
        return (
          <div key={lib.id}>
            <span className="railhead">{`// ${lib.name}`}</span>
            {books.map((book) => (
              <AudiobookBook
                key={book.id}
                book={book}
                playingId={playingId}
                onToggle={(id) => setPlayingId(playingId === id ? null : id)}
              />
            ))}
          </div>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 2: Type-check and lint**

Run (from `frontend/`): `npm run build`
Expected: this file no longer errors. (The build may still fail on `stream/page.tsx` until Task 4 — that's expected at this checkpoint.)

Run (from `frontend/`): `npm run lint`
Expected: no errors in `library/page.tsx`.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/app/\(shell\)/library/page.tsx
git commit -m "$(cat <<'EOF'
fix(library): adapt audiobook shelf to de-flattened media items

A shelf library's direct children are now book folders, not flat
tracks (Task 1 dropped Jellyfin's Recursive=true). Fetch each book's
children on demand so the shelf still lists playable chapters.
EOF
)"
```

---

### Task 4: Frontend — breadcrumb drill-down on the stream page

**Files:**
- Modify: `frontend/src/app/(shell)/stream/page.tsx` (full-file rewrite; the file is ~240 lines)
- Modify: `frontend/src/styles/stream.css` (append breadcrumb styles)

**Interfaces:**
- Consumes: `useMediaStore()` fields `libraries`, `libraryStatus`, `error`, `activeLibraryId`, `itemsByParent`, `itemsStatusByParent`, `path`, `rootLibrary`, `nowPlaying`, `fetchLibraries`, `open`, `navigateTo`, `play`, `stop` (from Task 2).
- Produces: `data-testid`s `stream-browse` (unchanged), `stream-player` (unchanged), `stream-video` (unchanged), `stream-crumbs` (new), `poster-folder` / `poster-leaf` (new, replace the old untagged poster buttons) — all consumed by Task 5's e2e spec.

- [ ] **Step 1: Rewrite the page**

Replace the full contents of `frontend/src/app/(shell)/stream/page.tsx` with:

```tsx
"use client";

import { useEffect, useRef, useState } from "react";
import { ChevronRight, Play, Plus, Users, X } from "lucide-react";
import type Hls from "hls.js";
import { apiBase } from "@/lib/api";
import {
  useMediaStore,
  type MediaItem,
  type MediaLibrary,
} from "@/stores/useMediaStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

const POSTER_CLASSES = ["c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"];
const DARK_TEXT = new Set(["c2", "c6"]);

function posterClass(id: string): string {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return POSTER_CLASSES[h % POSTER_CLASSES.length];
}

function isAudioItem(item: MediaItem, library: MediaLibrary): boolean {
  return (
    library.type === "audio" ||
    item.type === "Audio" ||
    item.type === "Audiobook"
  );
}

function streamUrl(item: MediaItem, library: MediaLibrary): string {
  const kind = isAudioItem(item, library) ? "audio" : "video";
  return `${apiBase()}/api/v1/stream/${kind}/${encodeURIComponent(item.id)}`;
}

// A folder's meta line describes what's inside instead of a duration —
// folders (series, seasons, audiobooks) carry no runtime of their own.
function folderNoun(type: string): string {
  switch (type) {
    case "Series":
      return "season";
    case "Season":
      return "episode";
    default:
      return "item";
  }
}

function posterMeta(item: MediaItem): string {
  if (item.isFolder) {
    const count = item.childCount ?? 0;
    const noun = folderNoun(item.type);
    return `${count} ${noun}${count === 1 ? "" : "s"}`;
  }
  return `${item.duration} · ${item.type.toLowerCase()}`;
}

// Video items stream as HLS: the gateway resolves PlaybackInfo and 302s to a
// main.m3u8 sub-path. hls.js follows the redirect and resolves segments
// against the final URL; Safari plays it natively. Audio is a static proxy.
function MediaPlayer({ item, library }: { item: MediaItem; library: MediaLibrary }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<string | null>(null);
  const audio = isAudioItem(item, library);
  const src = streamUrl(item, library);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setError(null);

    if (audio || video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = src;
      return () => {
        video.removeAttribute("src");
        video.load();
      };
    }

    let hls: Hls | null = null;
    let cancelled = false;
    void import("hls.js").then(({ default: HlsCtor }) => {
      if (cancelled) return;
      if (!HlsCtor.isSupported()) {
        setError("hls playback is not supported in this browser");
        return;
      }
      hls = new HlsCtor({
        // Dev runs cross-origin (:3000 → :8080); the session cookie must ride
        // along on manifest and segment requests.
        xhrSetup: (xhr) => {
          xhr.withCredentials = true;
        },
      });
      hls.on(HlsCtor.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError(`stream failed: ${data.details}`);
          hls?.destroy();
        }
      });
      hls.loadSource(src);
      hls.attachMedia(video);
    });

    return () => {
      cancelled = true;
      hls?.destroy();
    };
  }, [src, audio]);

  return (
    <div className="playerstage">
      {error ? (
        <p className="playererr">{`// ${error}`}</p>
      ) : (
        <video
          ref={videoRef}
          controls
          autoPlay
          playsInline
          crossOrigin={apiBase() ? "use-credentials" : undefined}
          data-testid="stream-video"
        />
      )}
    </div>
  );
}

function PosterGrid({
  items,
  status,
  onOpen,
}: {
  items: MediaItem[];
  status: "idle" | "loading" | "ready" | "error";
  onOpen: (item: MediaItem) => void;
}) {
  if (status === "error") {
    return <p className="desc">{`// this folder is unavailable`}</p>;
  }
  if (status === "ready" && items.length === 0) {
    return <p className="desc">{`// nothing here yet`}</p>;
  }
  return (
    <div className="posters">
      {items.map((item) => {
        const cls = posterClass(item.id);
        return (
          <button
            key={item.id}
            className={`poster ${cls}${DARK_TEXT.has(cls) ? " pdark" : ""}`}
            data-testid={item.isFolder ? "poster-folder" : "poster-leaf"}
            onClick={() => onOpen(item)}
          >
            <div className="motif" />
            <span className="pt">{item.title}</span>
            <span className="pm">{posterMeta(item)}</span>
          </button>
        );
      })}
    </div>
  );
}

export default function StreamPage() {
  const {
    libraries,
    libraryStatus,
    error,
    activeLibraryId,
    itemsByParent,
    itemsStatusByParent,
    path,
    rootLibrary,
    nowPlaying,
    fetchLibraries,
    open,
    navigateTo,
    play,
    stop,
  } = useMediaStore();

  useEffect(() => {
    if (libraryStatus === "idle") void fetchLibraries();
  }, [libraryStatus, fetchLibraries]);

  const activeLibrary =
    libraries.find((l) => l.id === activeLibraryId) ?? libraries[0];
  const featured = activeLibrary
    ? (itemsByParent[activeLibrary.id] ?? [])[0]
    : undefined;

  if (nowPlaying) {
    return (
      <div className="streammain" data-testid="stream-player">
        <div className="playerwrap">
          <div className="playerhead">
            <button className="iconbtn" aria-label="stop playback" onClick={stop}>
              <X size={18} />
            </button>
            <span className="ptitle">{nowPlaying.item.title}</span>
            <span className="kicker">
              {isAudioItem(nowPlaying.item, nowPlaying.library)
                ? "// direct stream"
                : "// hls · main.m3u8"}
            </span>
          </div>
          <MediaPlayer item={nowPlaying.item} library={nowPlaying.library} />
        </div>
      </div>
    );
  }

  if (path.length > 0 && rootLibrary) {
    const current = path[path.length - 1];
    const items = itemsByParent[current.id] ?? [];
    const status = itemsStatusByParent[current.id] ?? "idle";
    return (
      <div className="streammain" data-testid="stream-browse">
        <div className="streamscroll">
          <nav className="crumbs" data-testid="stream-crumbs">
            <button className="crumb" onClick={() => navigateTo(-1)}>
              Home
            </button>
            {path.map((crumb, i) => (
              <span key={crumb.id} className="crumbseg">
                <ChevronRight size={13} />
                <button
                  className="crumb"
                  disabled={i === path.length - 1}
                  onClick={() => navigateTo(i)}
                >
                  {crumb.title}
                </button>
              </span>
            ))}
          </nav>
          <section className="row">
            <PosterGrid
              items={items}
              status={status}
              onOpen={(item) => open(item, rootLibrary)}
            />
          </section>
        </div>
      </div>
    );
  }

  return (
    <div className="streammain" data-testid="stream-browse">
      <div className="streamscroll">
        <section className="hero2">
          <VaporwaveScene />
          <div className="scrim" />
          <div className="hc">
            <span className="fchip">Featured on the node</span>
            {libraryStatus === "error" ? (
              <>
                <h1>Signal lost.</h1>
                <p className="desc">{`// ${error}`}</p>
              </>
            ) : featured ? (
              <>
                <h1>{featured.title}</h1>
                <div className="metarow">
                  <span>{posterMeta(featured)}</span>
                  <span className="b">{featured.type}</span>
                  {activeLibrary && <span className="b">{activeLibrary.name}</span>}
                </div>
                <div className="cta">
                  <button
                    className="btn rose btn-lg"
                    onClick={() =>
                      activeLibrary &&
                      (featured.isFolder
                        ? open(featured, activeLibrary)
                        : play(featured, activeLibrary))
                    }
                  >
                    <Play size={17} /> {featured.isFolder ? "Open" : "Play"}
                  </button>
                  <button className="btn-ghost btn-lg">
                    <Plus size={17} /> My list
                  </button>
                  <button className="btn-ghost btn-lg">
                    <Users size={17} /> Watch party
                  </button>
                </div>
              </>
            ) : (
              <>
                <h1>The projector is warming up.</h1>
                <p className="desc">
                  {libraryStatus === "ready"
                    ? "no media on the node yet — drop files into the shared library and jellyfin will pick them up."
                    : "tuning into jellyfin…"}
                </p>
              </>
            )}
          </div>
        </section>

        {libraries.map((lib) => {
          const items = itemsByParent[lib.id] ?? [];
          const status = itemsStatusByParent[lib.id] ?? "idle";
          return (
            <section className="row" key={lib.id}>
              <div className="rowhead">
                <h2>{lib.name}</h2>
                <span className="more">
                  {status === "error"
                    ? "unavailable"
                    : status === "ready"
                      ? `${items.length} item${items.length === 1 ? "" : "s"}`
                      : "…"}
                </span>
              </div>
              <PosterGrid
                items={items}
                status={status}
                onOpen={(item) => open(item, lib)}
              />
            </section>
          );
        })}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Add breadcrumb styles**

Append to the end of `frontend/src/styles/stream.css`:

```css

/* breadcrumbs */
.crumbs{display:flex;align-items:center;flex-wrap:wrap;gap:6px;padding:20px 28px 4px;font-family:var(--f-mono);font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:var(--faint)}
.crumbseg{display:flex;align-items:center;gap:6px}
.crumb{background:none;border:none;padding:0;font:inherit;color:inherit;cursor:pointer}
.crumb:hover:not(:disabled){color:var(--ink)}
.crumb:disabled{color:var(--ink);cursor:default}
```

- [ ] **Step 3: Type-check, lint, build**

Run (from `frontend/`): `npm run build`
Expected: PASS — this was the last file with type errors from Task 2's rename; the build should now be clean end-to-end.

Run (from `frontend/`): `npm run lint`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/app/\(shell\)/stream/page.tsx frontend/src/styles/stream.css
git commit -m "$(cat <<'EOF'
feat(stream): add breadcrumb drill-down and adaptive open/play

Poster clicks now call open() instead of play() directly: folders
(series/seasons/audiobooks) push a breadcrumb and fetch their
children, leaves (episodes/movies/tracks) play immediately. The hero
CTA reads "Open" or "Play" depending on whether the featured item is a
folder. Extracts PosterGrid to share rendering between the top-level
library rows and the drilled-in view.
EOF
)"
```

---

### Task 5: E2E coverage for hierarchical browsing

**Files:**
- Create: `frontend/e2e/stream.spec.ts`

**Interfaces:**
- Consumes: `data-testid`s `stream-browse`, `stream-crumbs`, `poster-folder`, `poster-leaf`, `stream-player`, `stream-video` from Task 4, and the accessible name `"Home"` on the breadcrumb's first button.
- Consumes: the `login()` helper pattern and `E2E_USERNAME`/`E2E_PASSWORD` gating already used by `frontend/e2e/library.spec.ts` and `frontend/e2e/files.spec.ts`.

There is no seeded Jellyfin fixture (unlike Grimmory's seeded "Pride and Prejudice" book) — Jellyfin media depends on `${STORAGE_PATH}`, which is host-specific and outside the repo. Both tests below are written to be content-agnostic: they operate on whatever folder/leaf items actually exist on the node under test, and skip themselves with a clear reason if the node has none.

- [ ] **Step 1: Write the spec**

Create `frontend/e2e/stream.spec.ts`:

```ts
import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway with real Jellyfin libraries:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test stream
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("drilling into a folder poster shows breadcrumbs and its children", async ({
  page,
}) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });

  const folder = page.getByTestId("poster-folder").first();
  test.skip(
    (await folder.count()) === 0,
    "no folder-type media (series/audiobook) on this node to drill into",
  );

  const title = (await folder.locator(".pt").textContent())!.trim();
  await folder.click();

  const crumbs = page.getByTestId("stream-crumbs");
  await expect(crumbs).toBeVisible();
  await expect(crumbs).toContainText(title);

  // Home breadcrumb clears the drill and returns to the top-level rows.
  await page.getByRole("button", { name: "Home" }).click();
  await expect(page.getByTestId("stream-crumbs")).toHaveCount(0);
});

test("drilling down to a leaf item starts playback", async ({ page }) => {
  await login(page);
  await page.goto("/stream/");
  await expect(page.getByTestId("stream-browse")).toBeVisible({
    timeout: 15_000,
  });

  // Descend through folders (series -> season -> episode, or book ->
  // chapter) until a leaf poster appears, then play it.
  let leafFound = false;
  for (let i = 0; i < 5 && !leafFound; i++) {
    const leaf = page.getByTestId("poster-leaf").first();
    if (await leaf.count()) {
      await leaf.click();
      leafFound = true;
      break;
    }
    const folder = page.getByTestId("poster-folder").first();
    test.skip((await folder.count()) === 0, "no playable media on this node");
    await folder.click();
    await expect(page.getByTestId("stream-crumbs")).toBeVisible();
  }
  test.skip(!leafFound, "no leaf item reachable within 5 levels");

  await expect(page.getByTestId("stream-player")).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTestId("stream-video")).toBeVisible();
});
```

- [ ] **Step 2: Run it against a live dev gateway**

Prerequisites: `podman-compose up -d --build` running with a Jellyfin instance that has at least one show or audiobook, plus a credentialed test account (see the project's e2e-test-account notes: mint an invite row directly in Postgres, accept it with an `Origin: http://localhost:8080` header to pass CSRF, since invited users only need `Member` for this — no elevated role required to browse/stream). Then, from `frontend/`: `npm run dev` in one terminal.

Run (from `frontend/`, in a second terminal): `E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test stream`
Expected: both tests PASS, or self-skip with a clear reason if the node truly has no folder/leaf media — either outcome is acceptable to commit; a hard failure is not.

- [ ] **Step 3: Commit**

```bash
git add frontend/e2e/stream.spec.ts
git commit -m "$(cat <<'EOF'
test(stream): add e2e coverage for hierarchical media browsing

Content-agnostic since there's no seeded Jellyfin fixture: finds
whatever folder/leaf posters exist on the node under test, drills in,
and verifies breadcrumbs and playback. Self-skips with a clear reason
if the node has no folder or no playable media at all.
EOF
)"
```

---

## Final manual verification (not a task — do after Task 5)

1. `podman-compose up -d --build`.
2. Log in, go to Stream, and drill: pick a TV show → confirm breadcrumb reads `Home / <Show>` and season posters render → click a season → confirm episode posters render, including the pilot → click the pilot → confirm it plays.
3. Drill an audiobook → confirm a chapter poster renders → click it → confirm audio plays.
4. Go to Library → confirm the audiobook shelf still lists individual chapters (not book folders) and each still plays inline.
5. Confirm movies in a Movies library still play with a single click (no drill).
6. Watch `podman logs telos-core` throughout for unexpected Jellyfin fallback/error log lines.
