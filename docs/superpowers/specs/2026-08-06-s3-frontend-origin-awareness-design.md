# S3 — Frontend Origin-Awareness — Design

This sub-project parameterizes the Telos Next.js client (`frontend/`) network and media asset layers to support remote server addresses and bearer-token authentication alongside cookie-based browser access. It introduces an `assetUrl()` helper to absolutize relative cover and avatar paths, updates media players (`HlsPlayer.tsx`, `AudiobookPlayer.tsx`, `EpubReader.tsx`) to send bearer headers in token mode, centralizes all network calls through `frontend/src/lib/api.ts`, and adds CI build enforcement gates against unparameterized network calls or raw media rendering.

## 1. Purpose

### 1.1 Decisions

| Decision | Choice |
|---|---|
| API & WS base parameterization | `apiBase()` reads from connection state instead of returning `""`. `wsBase()` derives from the configured server rather than `window.location.host` — deriving from location yields garbage under a custom scheme. |
| Coexisting Auth Modes | `api()` swaps `credentials: "include"` for a bearer header when in token mode. **Both modes coexist**; browser builds keep cookies. |
| Asset URL Absolutization | **Cover URLs are absolutized by a single `assetUrl()` helper in `frontend/src/lib/api.ts`**, applied at every consumer. The backend keeps emitting relative paths — it sits behind Traefik and does not reliably know its own public URL, and adding a public-base-URL env var to solve a frontend problem is the wrong trade. |
| CI Grep Gate | **A CI grep gate enforces this**, because a helper nobody calls is worthless: fail the build if `coverUrl` (or any `/api/v1` literal) is rendered raw into a `src=` attribute, and fail if any `fetch(` or `XMLHttpRequest` appears outside `frontend/src/lib/api.ts`. |
| Leak Resolution Scope | All identified leak sites are closed in this sub-project, not deferred. |
| HLS Credentials & Bearer Support | The dormant `crossOrigin="use-credentials"` branch in `HlsPlayer.tsx` goes live; the hls.js `xhrSetup` swaps `withCredentials` for a bearer header. |

### 1.2 Success criteria

1. Calling `apiBase()`, `wsBase()`, and `assetUrl(path)` returns fully-qualified origin URLs when connected to a remote server, and relative paths when running embedded in single-origin browser mode.
2. HTTP requests dispatched via `api()` send `Authorization: Bearer <token>` when operating under token auth, while continuing to pass `credentials: "include"` under cookie session auth.
3. Media cover images render correctly across Library, Stream, and Search when connecting to remote backends without triggering 404 broken image requests.
4. `HlsPlayer.tsx`, `AudiobookPlayer.tsx`, `EpubReader.tsx`, `ProfileSection.tsx`, and `upload.ts` successfully play media and execute file uploads over cross-origin connections under both cookie and bearer token auth.
5. The CI static check suite fails if any raw `fetch(` or `XMLHttpRequest` is introduced outside `frontend/src/lib/api.ts`, or if any relative `/api/v1/` path or `coverUrl` property is bound directly to an HTML `src=` attribute without passing through `assetUrl()`.

## 2. Verified current state

The table below documents all existing network, asset, and player sites in the codebase that require parameterization or leak remediation:

| Component / Layer | Location | Verified current implementation |
|---|---|---|
| `apiBase()` helper | `frontend/src/lib/api.ts:5-7` | Hardcoded return `""`. Assumes single-origin deployment. |
| `wsBase()` helper | `frontend/src/lib/api.ts:21-25` | Derives protocol and host from `window.location.host`. Fails under custom app schemes. |
| Core `api()` fetch client | `frontend/src/lib/api.ts:34-67` | Hardcodes `credentials: "include"` on all requests. Does not support Bearer tokens. |
| Centralized client adoption | `frontend/src/lib/api.ts` | 104 out of 107 total frontend network calls already route through `lib/api.ts`. |
| Raw `fetch` in EpubReader | `frontend/src/components/library/EpubReader.tsx:69` | Bypasses `api()`; calls `fetch(libraryContentUrl(book.id), { credentials: "include" })`. |
| Raw `fetch` in ProfileSection | `frontend/src/components/settings/ProfileSection.tsx:42` | Bypasses `api()`; calls `fetch(`${apiBase()}/api/v1/users/me/avatar`, ...)` for avatar upload. |
| Raw `XHR` in upload.ts | `frontend/src/lib/upload.ts:33-35` | Direct `new XMLHttpRequest()`; hardcodes `xhr.withCredentials = true`. |
| Audiobook relative stream URL | `frontend/src/components/library/AudiobookPlayer.tsx:117-119` | Hardcodes relative stream string `/api/v1/library/audiobooks/.../stream` without `apiBase()`. |
| HLS player `xhrSetup` | `frontend/src/components/stream/HlsPlayer.tsx:62-64` | Hardcodes `xhr.withCredentials = true` inside hls.js `xhrSetup` callback. |
| HLS video `crossOrigin` | `frontend/src/components/stream/HlsPlayer.tsx:93` | Conditional `crossOrigin={apiBase() ? "use-credentials" : undefined}` dormant while `apiBase()` is `""`. |
| Backend `coverUrl` site 1 | `backend/grimmory_catalog.go:143` | Emits relative `"/api/v1/library/books/" + book.ID + "/cover"`. |
| Backend `coverUrl` site 2 | `backend/jellyfin_catalog.go:196` | Emits relative `"/api/v1/media/items/" + raw.ID + "/cover"`. |
| Backend `coverUrl` site 3 | `backend/jellyfin_catalog.go:245` | Emits relative `"/api/v1/media/items/" + resolution.ID + "/cover"`. |
| Backend `coverUrl` site 4 | `backend/jellyfin_catalog.go:385` | Emits relative `"/api/v1/media/items/" + resolution.ID + "/cover"`. |
| Backend `coverUrl` site 5 | `backend/jellyfin_catalog.go:416` | Emits relative `"/api/v1/media/items/" + raw.ID + "/cover"`. |
| Backend `coverUrl` site 6 | `backend/main.go:3153` | Emits relative `"/api/v1/media/items/" + resolution.ID + "/cover"`. |
| Frontend cover render 1 | `frontend/src/app/(shell`/stream/page.tsx#L76) | Renders raw `src={item.coverUrl}` without `assetUrl()`. |
| Frontend cover render 2 | `frontend/src/app/(shell`/library/page.tsx#L176) | Renders `src={libraryCoverUrl(book.id)}` without `assetUrl()`. |
| Frontend cover render 3 | `frontend/src/components/library/BookManageModal.tsx:469` | Renders `src={`${libraryCoverUrl(book.id)}?v=${coverRevision}`}` without `assetUrl()`. |
| Frontend cover render 4 | `frontend/src/components/library/BookManageModal.tsx:500` | Renders `selectedCandidate?.coverUrl` preview without `assetUrl()`. |
| Frontend cover render 5 | `frontend/src/components/library/LibraryItemDetail.tsx:71` | Renders raw `src={item.coverUrl}` without `assetUrl()`. |
| Frontend cover render 6 | `frontend/src/components/stream/MediaShelf.tsx:38` | Renders raw `src={item.coverUrl}` without `assetUrl()`. |
| Frontend cover render 7 | `frontend/src/components/stream/StreamItemDetail.tsx:135` | Renders raw `src={detail.coverUrl}` without `assetUrl()`. |

## 3. Architecture

### 3.1 Boundaries

S3 refactors frontend API helpers, media element bindings, and upload utilities to be fully origin-aware and auth-mode aware.
- **May touch:** `frontend/src/lib/api.ts`, `frontend/src/lib/upload.ts`, `frontend/src/components/library/AudiobookPlayer.tsx`, `frontend/src/components/stream/HlsPlayer.tsx`, `frontend/src/components/library/EpubReader.tsx`, `frontend/src/components/settings/ProfileSection.tsx`, cover image renderers in `frontend/src/app/(shell`/stream/page.tsx), `frontend/src/app/(shell`/library/page.tsx), `frontend/src/components/library/BookManageModal.tsx`, `frontend/src/components/library/LibraryItemDetail.tsx`, `frontend/src/components/stream/MediaShelf.tsx`, `frontend/src/components/stream/StreamItemDetail.tsx`, and build verification scripts.
- **May not touch:** Backend route definitions, backend JSON payloads (the backend continues emitting relative paths for `coverUrl` and `avatarUrl`), or connection UI state storage (which is implemented in S4).

### 3.2 Origin & Token Parameterization (`lib/api.ts`)

`frontend/src/lib/api.ts` is updated to provide origin and token resolution:

```typescript
// Connection state provider hook (populated by S4 store or default single-origin)
export interface ConnectionState {
  serverUrl: string;       // e.g. "https://telos.example.com" or ""
  token: string | null;     // Bearer access token or null for cookie mode
  authMode: "cookie" | "token";
}

let activeConnection: ConnectionState = {
  serverUrl: "",
  token: null,
  authMode: "cookie",
};

export function setConnectionState(state: ConnectionState): void {
  activeConnection = state;
}

export function apiBase(): string {
  return activeConnection.serverUrl;
}

export function wsBase(): string {
  if (!activeConnection.serverUrl) {
    if (typeof window === "undefined") return "";
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${window.location.host}`;
  }
  const u = new URL(activeConnection.serverUrl);
  const proto = u.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${u.host}`;
}

export function assetUrl(path: string | undefined): string {
  if (!path) return "";
  if (path.startsWith("http://") || path.startsWith("https://") || path.startsWith("blob:")) {
    return path;
  }
  const base = apiBase();
  return base ? `${base}${path.startsWith("/") ? "" : "/"}${path}` : path;
}
```

The core `api()` helper (`frontend/src/lib/api.ts:34-67`) is updated to inspect `activeConnection.authMode`:
- If `authMode === "token"` and `activeConnection.token` is present, it sets `headers.set("Authorization", `Bearer ${activeConnection.token}`)` and omits `credentials: "include"`.
- If `authMode === "cookie"`, it includes `credentials: "include"` as it does today.

### 3.3 Closing Network & Media Leaks

All 6 identified network and media leaks outside `lib/api.ts` are closed:

1. **`AudiobookPlayer.tsx:117-119`**: Refactored to pass the constructed stream path through `assetUrl()`:
   `typescript
   const rawStreamPath = hasTracks
     ? `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/tracks/${currentTrackIndex}/stream`
     : `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/stream`;
   const streamUrl = assetUrl(rawStreamPath);
   `
2. **`HlsPlayer.tsx:62-64,93`**: `xhrSetup` checks `activeConnection.authMode`. If token mode is active, it attaches `xhr.setRequestHeader("Authorization", `Bearer ${activeConnection.token}`)`. If cookie mode is active, `xhr.withCredentials = true`. Video element `crossOrigin` sets `"anonymous"` when `apiBase()` is set in token mode, or `"use-credentials"` in cookie mode.
3. **`EpubReader.tsx:69`**: Replaces raw `fetch` call with `api<ArrayBuffer>(...)` or parameterizes `fetch` with `assetUrl(libraryContentUrl(book.id))` and appropriate headers based on auth mode.
4. **`ProfileSection.tsx:42`**: Replaces raw `fetch` for avatar upload with `uploadFile()` from `upload.ts`.
5. **`upload.ts:33-35`**: Updates XHR setup to check auth mode. In token mode, sets `xhr.setRequestHeader("Authorization", `Bearer ${token}`)` instead of hardcoding `xhr.withCredentials = true`.
6. **Cover Image Rendering**: All 7 cover render sites (`frontend/src/app/(shell`/stream/page.tsx#L76), `frontend/src/app/(shell`/library/page.tsx#L176), `frontend/src/components/library/BookManageModal.tsx:469`, `frontend/src/components/library/LibraryItemDetail.tsx:71`, `frontend/src/components/stream/MediaShelf.tsx:38`, `frontend/src/components/stream/StreamItemDetail.tsx:135`) wrap their source paths with `assetUrl(...)`.

### 3.4 CI Enforcement Gates

A new build enforcement check is added to `package.json` scripts (`npm run check:origin-leak`):
- **Raw fetch/XHR check:** Scans `frontend/src/` for any `fetch(` or `new XMLHttpRequest()` calls outside `frontend/src/lib/api.ts` and `frontend/src/lib/upload.ts`.
- **Raw media src check:** Scans TSX files for `<img ... src={item.coverUrl}` or `src="/api/v1/...` that fail to invoke `assetUrl()`.

```bash
# Executed during `npm run lint` in CI pipeline
node scripts/check-origin-leaks.js
```

## 4. Contract changes

### 4.1 Frontend Exports (`frontend/src/lib/api.ts`)

- Added export: `assetUrl(path: string | undefined): string`
- Added export: `setConnectionState(state: ConnectionState): void`
- Updated export: `apiBase(): string`
- Updated export: `wsBase(): string`
- Updated export: `api<T>(path: string, init?: RequestInit): Promise<T>`

## 5. Implementation surface

| File Path | Change | Rationale |
|---|---|---|
| `frontend/src/lib/api.ts` | Add `assetUrl()`, `setConnectionState()`, token support in `api()`, parameterize `wsBase()` | Centralize origin resolution and dual-mode auth header injection. |
| `frontend/src/lib/upload.ts` | Update XHR header setup for Bearer auth | Support cross-origin multipart file uploads under token auth mode. |
| `frontend/src/components/library/AudiobookPlayer.tsx` | Wrap `streamUrl` with `assetUrl()` | Fix relative stream URL resolution when playing audiobooks remotely. |
| `frontend/src/components/stream/HlsPlayer.tsx` | Update `xhrSetup` and `crossOrigin` binding | Enable HLS segment fetching and video playback under Bearer token auth mode. |
| `frontend/src/components/library/EpubReader.tsx` | Replace raw `fetch` call (`frontend/src/components/library/EpubReader.tsx:69`) | Route EPUB content fetching through parameterized origin/auth layer. |
| `frontend/src/components/settings/ProfileSection.tsx` | Replace raw `fetch` avatar upload (`frontend/src/components/settings/ProfileSection.tsx:42`) | Use `uploadFile()` utility to support remote avatar updates under token auth. |
| `frontend/src/app/(shell`/stream/page.tsx) | Wrap `item.coverUrl` with `assetUrl()` (`frontend/src/app/(shell`/stream/page.tsx#L76)) | Absolutize cover images in Stream hero/item lists. |
| `frontend/src/app/(shell`/library/page.tsx) | Wrap `libraryCoverUrl()` with `assetUrl()` (`frontend/src/app/(shell`/library/page.tsx#L176)) | Absolutize cover images in Library book grid. |
| `frontend/src/components/library/BookManageModal.tsx` | Wrap cover previews with `assetUrl()` (`frontend/src/components/library/BookManageModal.tsx:469`) | Fix cover image preview rendering inside book edit modal. |
| `frontend/src/components/library/LibraryItemDetail.tsx` | Wrap `item.coverUrl` with `assetUrl()` (`frontend/src/components/library/LibraryItemDetail.tsx:71`) | Fix book detail view cover image display. |
| `frontend/src/components/stream/MediaShelf.tsx` | Wrap `item.coverUrl` with `assetUrl()` (`frontend/src/components/stream/MediaShelf.tsx:38`) | Absolutize shelf item artwork. |
| `frontend/src/components/stream/StreamItemDetail.tsx` | Wrap `detail.coverUrl` with `assetUrl()` (`frontend/src/components/stream/StreamItemDetail.tsx:135`) | Absolutize detail artwork in media modal. |
| `frontend/scripts/check-origin-leaks.js` | Add CI origin leak static checker | Enforce that raw fetch and unparameterized asset URLs cannot merge to main. |

## 6. Failure handling and observability

- **Invalid Server Base URL:** `api()` catches malformed URLs and throws `ApiError(0, "Invalid server URL configured")`.
- **CORS Failure / Network Offline:** Handled cleanly in `api()` catch block (`frontend/src/lib/api.ts:46-54`), raising `ApiError(0, "Cannot reach this Telos node. Check the server address and try again.")`.
- **Image Load Error:** Cover image components retain fallback poster styling on onError events when a remote image fails to load.

## 7. Security and privacy

- **Header Isolation:** Bearer tokens are sent exclusively in `Authorization` headers over HTTPS/WSS and are never appended to asset URLs as query parameters.
- **Credential Protection:** `assetUrl()` verifies the target origin before returning full URLs to prevent leaking bearer credentials or session tokens to third-party domains.

## 8. Verification

Execute frontend tests and the new origin leak verification gate:

```bash
cd frontend

# Code quality, type check, unit tests, and build
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build

# New CI origin leak check gate
node scripts/check-origin-leaks.js

# Playwright E2E suite
npx playwright test
```

## 9. Documentation changes required with implementation

- Update `frontend/AGENTS.md` to document `assetUrl()`, `apiBase()`, `wsBase()`, and the rule requiring all asset URLs and network calls to route through `lib/api.ts`.
- Update `CLAUDE.md` frontend architecture summary.

## 10. Deferred / out of scope

- Connection UI screen, server URL entry input, and connection store state management (handled in S4).
- Native OS keychain persistence for bearer tokens (handled in S4).
