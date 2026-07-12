# Files Module UI — Design

**Date:** 2026-07-11
**Status:** Approved
**Scope:** Frontend only. No backend changes.

## Goal

Replace the 14-line `ModulePlaceholder` at `frontend/src/app/(shell)/files/page.tsx` with a
feature-complete Files module: list, upload (library + bookdrop), download, and delete,
wired to the existing gateway endpoints.

## Backend contract (already implemented, not to be modified)

| Endpoint | Permission | Notes |
|---|---|---|
| `GET /api/v1/files?page=N` | `view_files` | 20/page, newest first, **no total count**. Empty result marshals as JSON `null`. Includes infected audit rows. |
| `POST /api/v1/files` | `upload_files` | Multipart, key `file`. 100 MiB cap. Extension allowlist: pdf, epub, jpg, jpeg, png, webp, mp3, m4a, ogg, wav, mp4, webm. MIME sniffed server-side. ClamAV scan; `422` if infected, `503` if scanner down. Returns `201` with `{id, filename, sha256, scan_status}`. |
| `POST /api/v1/files/books` | `upload_books` | Same, but PDF/EPUB only; lands in bookdrop. |
| `GET /api/v1/files/{id}/download` | `view_files` | `Content-Disposition: attachment`; only serves `scan_status = 'clean'`. |
| `DELETE /api/v1/files/{id}` | `manage_files` | Removes disk asset + DB row. |

List item shape: `{id, filename, sha256, uploader_id, scan_status, size_bytes, mime_type, created_at}`.

## Architecture (Approach A — matches existing module pattern)

- `frontend/src/stores/useFilesStore.ts` — Zustand store: file list, page number, load status,
  error, upload queue, actions (`fetchPage`, `deleteFile`, `enqueueUpload`, `dismissUpload`).
- `frontend/src/lib/upload.ts` — small XHR wrapper. Needed because the existing `api()` fetch
  helper cannot report upload progress and forces `Content-Type: application/json` (wrong for
  multipart). Exposes `uploadFile(path, file, onProgress) → Promise<UploadResult>`; reports
  progress 0–100, then a terminal `scanning` phase once bytes are fully sent, resolving on the
  server response.
- `frontend/src/app/(shell)/files/page.tsx` — client component, small local sub-components
  (drop zone, upload rows, table row). Single file, ~250 lines, mirroring chat/stream structure.
- `frontend/src/styles/files.css` — module stylesheet on existing tokens/foundations; imported
  from the root layout like the other module sheets.

## UI layout

Vaporwave scene + header + banner line, consistent with Chat.

1. **Header** — module title, banner (`// scanned before it touches the shelf` tone), page indicator.
2. **Drop zone strip** — drag-and-drop target + click-to-pick. Destination toggle:
   **Library** (full allowlist → `POST /api/v1/files`) or **Bookdrop** (PDF/EPUB only →
   `POST /api/v1/files/books`). Client-side pre-validation: extension allowlist, 100 MiB cap,
   bookdrop restriction — instant feedback before any bytes move.
3. **Upload rows** — transient rows above the table for in-flight/failed uploads:
   progress bar (0–100%) → indeterminate *scanning…* → success (row disappears, list refreshes
   to page 1) or terminal error state with dismiss.
4. **File table** — columns: type icon (from `mime_type`), filename, humanized size,
   scan-status pill, created date, actions. Infected rows render dimmed, no download action
   (they are audit records). Actions: **download** (real navigation to the download URL so the
   browser handles the attachment) and **delete** (inline two-step confirm, then `DELETE` +
   list refresh).
5. **Paging** — blind prev/next: "prev" disabled on page 1, "next" disabled when the current
   page returned fewer than 20 rows.

## Permissions & errors

- Optimistic UI: all controls visible regardless of role (auth store only exposes role names,
  not granular permissions). `403` responses surface as inline "you don't have permission" notices.
- `422` on upload → distinct "rejected by security scan" state.
- `503` on upload → "security scanner unavailable" state.
- Empty/`null` list → normalize with the same `asList` pattern used by the media store; show an
  empty-state placeholder.
- Gateway unreachable → store error state with retry; no mock data in the frontend.

## Testing & verification

- `npm run lint` and `npm run build` clean.
- Playwright spec `frontend/tests/files.spec.ts`: list renders; upload happy path; delete flow.
  Requires `npm run dev` + gateway running (per repo convention, no `webServer` in config).
- Full-stack check against `podman-compose up`: real upload, real download, EICAR test file
  rejected with the 422 UI state visible.

## Out of scope

- Backend changes of any kind (including exposing permissions on `/auth/me`).
- Multi-file upload queue. Uploads are single-file-at-a-time: if multiple files are dropped or
  picked, only the first is uploaded and a notice says so.
- The uncommitted Stream WIP (`useMediaStore.ts`, `stream/page.tsx`, `stream.css`) — untouched.
- Books/Grimmory reader UI (bookdrop upload only drops the file; the Library module consumes it later).
