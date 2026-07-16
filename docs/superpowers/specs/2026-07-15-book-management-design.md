# Book Management (Library Gear Menu) — Design Spec

**Date:** 2026-07-15
**Status:** Approved for implementation
**Scope:** Add per-book management (edit metadata, fetch metadata, replace cover, delete) to the Library module, surfaced via a gear icon on each book card, backed by new permission-gated gateway endpoints that translate to the Grimmory (BookLore-derived) admin API.

---

## 1. Background and constraints

Telos fronts Grimmory through `telos-core` (`backend/library.go`); clients never talk to Grimmory directly. Grimmory is addressed as a **single shared admin account** via a JWT minted from `GRIMMORY_ADMIN_USER`/`GRIMMORY_ADMIN_PASSWORD` (cached in memory, ~2h expiry, re-minted on 401). See `documentation/architecture/03-gateway-and-api.md` §3–4.

Hard constraints that shape this design:

- **No catch-all proxy.** The gateway holds admin upstream credentials, so every management action gets an explicit, whitelisted, permission-gated `telos-core` endpoint. Never forward arbitrary client paths/methods to Grimmory.
- **Copyleft boundary.** Integrate over HTTP only; never link or compile Grimmory code into the gateway.
- **Explicit degraded behavior.** Upstream failures return 502/503; never fabricate records.
- **Shared catalog.** There is no per-user book ownership — edits and deletes affect everyone. This drives the permission model (§3) and delete confirmation UX (§6.4).

### 1.1 Verified Grimmory endpoints (deployed image, not upstream docs)

Extracted from the running container's `app.jar` controller classes on 2026-07-15. **The deployed build diverges from upstream BookLore** (e.g. its facets endpoint 500s), so these — not upstream docs — are the source of truth. Verify request/response shapes against the live instance during implementation (see §8).

| Feature | Grimmory endpoint (internal, `http://grimmory:6060`) | Controller |
|---|---|---|
| Update metadata | `PUT /api/v1/books/{bookId}/metadata` | `MetadataController` |
| Prospective (online) metadata fetch | `POST /api/v1/books/{bookId}/metadata/prospective` | `MetadataController` |
| Cover upload | `POST /api/v1/books/{bookId}/metadata/cover/upload` (multipart) | `BookCoverController` |
| Delete book(s) | `DELETE /api/v1/books` (bulk, by ids; removes book **and** file) | `BookController` |

Auth: `Authorization: Bearer <admin JWT>` — reuse the existing token mint/refresh helper in `backend/library.go`.

Probing note: unknown API paths on this build return **500** (not 404), and unknown non-API paths fall through to the SPA with 200 — status codes alone cannot confirm a route. The jar's controller strings are authoritative.

---

## 2. Feature summary

A gear (settings) icon on each library book card, next to the Share button, opens a **management modal** with three sections:

1. **Metadata** — edit whitelisted fields; a "Fetch metadata" flow retrieves online candidates and lets the user review and selectively apply fields before saving.
2. **Cover** — preview current cover; replace via file upload or by applying a fetched candidate's cover.
3. **Danger zone** — delete the book (and its file) for everyone, behind a confirm dialog.

The gear is visible only to users holding the new `manage_library` permission.

---

## 3. Permissions

- **New permission:** `manage_library`.
- **Grant:** the existing `Librarian` role (which already holds file/book powers such as delete-files and upload). Admin roles that hold `manage_community` should also receive it if role seeding works that way — follow the existing seed pattern.
- **Migration:** new file `backend/db/migrations/0005_manage_library.sql`, following the established migration style (`IF NOT EXISTS` / `ON CONFLICT` idempotent patterns — see `0004_settings.sql` for reference). Inserts the permission and the Librarian role grant.
- **Enforcement:** every new endpoint is wrapped `withAuth(handler, "manage_library")`, same as existing routes in `backend/main.go`.
- **Frontend visibility:** the auth store (`frontend/src/stores/useAuthStore.ts`) already exposes the user's permissions; the gear renders only when `manage_library` is present. Hiding the icon is UX only — the server check is the security boundary.

---

## 4. Gateway API (new endpoints in `backend/library.go`)

All routes registered in `backend/main.go` beside the existing `/api/v1/library/*` block. All translate to Grimmory over `telos-backend`, reusing the admin-JWT helper and the existing Grimmory base-URL config. On upstream error: return **502** with a terse JSON error; on Grimmory 404 for the book id: return **404**. Never fabricate success.

### 4.1 `PUT /api/v1/library/books/{id}/metadata`

- **Request body (Telos shape):**

```json
{
  "title": "string",
  "subtitle": "string",
  "authors": ["string"],
  "categories": ["string"],
  "language": "string",
  "description": "string",
  "seriesName": "string",
  "seriesNumber": 1.0,
  "publisher": "string",
  "publishedDate": "YYYY-MM-DD",
  "isbn10": "string",
  "isbn13": "string"
}
```

- **Field whitelist is exhaustive** — the handler marshals only these fields into Grimmory's update payload. Unknown fields in the request are ignored; Grimmory fields outside this list are never written. (Grimmory's own update DTO shape must be confirmed against the live instance — §8. If Grimmory's PUT is a full-replace rather than a patch, the handler must first GET the book and merge, so unlisted upstream fields are preserved.)
- **Response:** the updated book in the same shape as `GET /api/v1/library/books/{id}`.
- **Side effect:** invalidate the Redis catalog/facets cache (`telos:grimmory:*` or whatever key prefix `library.go` uses — reuse its invalidation helper) so the next list reflects the edit.

### 4.2 `POST /api/v1/library/books/{id}/metadata/fetch`

- Translates to Grimmory's prospective-metadata endpoint. The upstream request body (provider selection, etc.) must be discovered from the live instance (§8); the gateway sends a sensible default (all available providers) — the Telos API takes **no request body** in v1.
- **Response:** a normalized candidate list:

```json
{
  "candidates": [
    {
      "provider": "string",
      "title": "...", "subtitle": "...", "authors": ["..."], "categories": ["..."],
      "language": "...", "description": "...", "seriesName": "...", "seriesNumber": 1.0,
      "publisher": "...", "publishedDate": "...", "isbn10": "...", "isbn13": "...",
      "coverUrl": "https://... (upstream/external URL, passed through for preview)"
    }
  ]
}
```

- Fetch is **read-only**: nothing is written to Grimmory by this endpoint. Applying candidate values happens client-side into the edit form; persistence goes through 4.1 (and 4.3 for the cover).
- This calls out to external metadata providers via Grimmory, so it can be slow — set a generous upstream timeout (~30s) and stream no partial results.

### 4.3 `PUT /api/v1/library/books/{id}/cover`

Two accepted content types:

- `multipart/form-data` with a `cover` file part — JPEG or PNG, **max 5 MB** (reject 413 above). Forwarded to Grimmory's cover-upload endpoint.
- `application/json` `{"coverUrl": "https://..."}` — for applying a fetched candidate's cover. The **gateway** downloads the image server-side (size-capped at the same 5 MB, content-type checked, HTTP(S) only, no redirects to private address ranges — guard against SSRF) and re-posts it to Grimmory as multipart.
- **Response:** 204. The existing cover route (`GET .../cover`) already proxies live from Grimmory; invalidate any Redis-cached cover entry for the book if one exists.

### 4.4 `DELETE /api/v1/library/books/{id}`

- Translates to Grimmory's bulk delete with the single id (exact parameter shape — query `?ids=` vs JSON body — to be confirmed live, §8). This removes the book **and its file** upstream.
- **Telos-side cleanup on upstream success (and only on success):**
  - Delete `book_progress` rows for that Grimmory book id (all users).
  - Invalidate the Redis catalog/facets cache.
- **Response:** 204.

---

## 5. Frontend — gear icon on the book card

File: `frontend/src/app/(shell)/library/page.tsx`, `BookCard` component.

- Icon: `Settings` from `lucide-react`, size 12, same `btn-ghost btn-sm` styling as the Share link, placed **immediately after Share** in the `.brow` bottom row.
- The whole card is click-to-read (opens reader / PDF), so the gear's `onClick` **must call `e.stopPropagation()`** (Share already does this — mirror it).
- `aria-label={`manage ${book.title}`}`, `title="Book settings"`.
- Rendered only when the current user has `manage_library` (from `useAuthStore`).
- Clicking sets `managing: LibraryBook | null` state on the page (same pattern as the existing `reading` state) which mounts the modal.

---

## 6. Frontend — `BookManageModal`

New component: `frontend/src/components/library/BookManageModal.tsx` (beside `BookReader.tsx`; follow its overlay/portal pattern, `data-testid="book-manage-modal"`, close button labeled `close book settings`). Styles go in `frontend/src/styles/library.css` following its conventions. Respect the repo lint rule: **no setState-in-effect — use promise-chain loaders** (see existing pages for the pattern).

### 6.1 Metadata section

- Form pre-filled from the book's current metadata (fetch fresh via `GET /api/v1/library/books/{id}` on open, don't trust the possibly-stale card data).
- Fields: exactly the whitelist in §4.1. Authors and categories are editable as tag-style lists or comma-separated inputs — match whatever input idiom the settings components already use.
- **Save** → `PUT .../metadata`; on success show confirmation, refresh the catalog list (existing store action), and update the form baseline. On 502 show the degraded-state error style used elsewhere.

### 6.2 Fetch-metadata review flow

- "Fetch metadata" button → `POST .../metadata/fetch` (show a loading state; can take tens of seconds).
- Results render as a candidate picker (one card per provider/candidate). Selecting a candidate shows a **current vs. candidate** two-column field comparison with a per-field apply checkbox (default: checked only for fields where the candidate has a value).
- **Apply** copies checked fields into the edit form. **Nothing persists until the user hits Save** (§6.1). This is the agreed "review before apply" model.
- If the candidate has a `coverUrl`, the cover section (§6.3) offers "Use this candidate's cover".

### 6.3 Cover section

- Shows the current cover (existing `libraryCoverUrl(book.id)`; cache-bust with a query param after replacement).
- **Upload image** file input (accept `image/jpeg,image/png`, client-side 5 MB check) → multipart `PUT .../cover`.
- **Use candidate cover** (only when a fetch candidate with `coverUrl` is selected) → JSON `PUT .../cover`.
- Cover replacement applies immediately on click (it has its own explicit button; it is not part of the metadata Save).

### 6.4 Danger zone — delete

- Visually separated section with a danger-styled Delete button.
- Confirm dialog: **“Delete “\<title\>”? This removes the book and its file for everyone.”** Confirm executes `DELETE /api/v1/library/books/{id}`.
- On success: close the modal, refresh the catalog (card disappears). On failure: surface the error, do not close.

---

## 7. Testing

### 7.1 Go (run in container — Go is not installed on the host; use podman)

Table-driven tests in `backend/library_test.go` against an `httptest` fake Grimmory, matching the existing library-handler test style:

- Metadata PUT: whitelisted fields forwarded, unknown fields dropped, upstream-shape merge preserves unlisted fields (if full-replace semantics), 502 on upstream error, 404 on unknown book, 403 without `manage_library`.
- Fetch: upstream candidates normalized to the Telos shape; upstream timeout → 502.
- Cover: multipart size/type limits (413/415), URL mode SSRF guards (reject private ranges), forwards as multipart.
- Delete: upstream success → progress rows deleted + cache invalidated; upstream failure → **no** Telos-side cleanup, 502.

### 7.2 Playwright e2e (credentialed; see repo notes on minting e2e accounts)

New spec `frontend/e2e/library-manage.spec.ts`:

- Gear hidden for a plain Member; visible for a Librarian-granted account.
- Gear click does **not** open the reader (stopPropagation).
- Edit title → save → card shows the new title; restore the original title at the end.
- Fetch metadata → candidates render → apply fills the form (no save; assert nothing persisted after closing without save).
- Delete: upload a scratch EPUB first (existing upload route), delete it via the modal, assert the card disappears. Never delete the seeded Pride and Prejudice fixture.
- Run credentialed specs in isolation (parallel workers race the shared test account — known repo gotcha).

## 8. Implementation-time verification checklist

The deployed Grimmory build's request/response DTO shapes were **not** fully verified in design — only route existence (from jar controller strings). Before writing handler code, probe the live instance (admin JWT via `POST /api/v1/auth/login` from inside `telos-core`) to confirm:

1. `PUT /books/{id}/metadata` payload shape, and whether it is patch or full-replace semantics.
2. `POST /books/{id}/metadata/prospective` request body (provider list?) and response shape.
3. Cover upload multipart part name.
4. Delete parameter shape (`?ids=` query vs body) and whether it hard-deletes the file.
5. Whether metadata updates require unlocking field locks (`MetadataController` exposes `toggle-field-locks` — locked fields may silently refuse updates).

Record what you find as a comment block above the handlers or in the plan doc.

## 9. Out of scope (v1)

- Bulk operations (Grimmory supports bulk edit/delete; Telos v1 is single-book only).
- Shelves, notebooks, reviews, ratings, physical-book fields, and any other Grimmory feature not listed above.
- Per-user ownership or edit history/audit trail.
- Paste-a-URL cover source from arbitrary user input beyond fetched candidates (candidate URLs come from Grimmory's providers; there is still an SSRF guard because the URL is client-supplied at the HTTP layer).
