# Files Module — Folders & Hierarchical Navigation — Design

**Date:** 2026-07-14
**Status:** Approved
**Scope:** Files module (backend `main.go` file handlers + frontend Files page/store).

## Goal

Let users create and delete folders in the shared file library, and represent
the directory hierarchy in the UI: a file inside a folder is only visible after
entering that folder. Folders nest arbitrarily.

## Current reality (what exists today)

The Files module is **filesystem-backed**. `/data/shared/media` is the real
tree; there is no folder concept in the database for library files.

- `GET /api/v1/files?page=N` (`view_files`) — `filepath.WalkDir` over
  `/data/shared/media` recursively, returns **files only** (never directories),
  each as `{ id: base64url(relPath), filename: relPath, sha256, uploader_id:"",
  scan_status:"clean", size_bytes, mime_type, created_at }`, sorted newest-first,
  paginated globally at 20/page.
- `POST /api/v1/files` (`upload_files`) — stages → ClamAV scans → promotes into
  `/data/shared/media/<getUniqueFilename(name)>` (always the root). Returns
  `201 { id: base64url(destKey), filename, sha256, scan_status }`.
- `POST /api/v1/files/books` (`upload_books`) — flat bookdrop, unchanged by this work.
- `GET /api/v1/files/{id}/download` (`view_files`) — base64-decodes id → relPath,
  serves `/data/shared/media/<relPath>` behind a `HasPrefix("/data/shared/media")`
  guard; falls back to a DB lookup for legacy rows.
- `DELETE /api/v1/files/{id}` (`manage_files`) — same base64→path resolution,
  `os.Remove` the file; **refuses directories** (`!info.IsDir()` gate).

The base dirs are created at startup with `os.MkdirAll` (main.go ~line 146–155).
`getUniqueFilename(dir, name)` resolves collisions as `name_1.ext`, `name_2.ext`, …

Frontend: `useFilesStore` (flat list + paging) and `files/page.tsx` (table with
download + inline-confirm delete, drop zone, library/bookdrop toggle, blind pager).

## Decisions (from clarifying questions)

1. **Folders are real OS directories** under `/data/shared/media`.
2. **Arbitrary nesting**, breadcrumb navigation.
3. **Delete refuses non-empty folders** (rmdir semantics); recursive delete is out of scope.
4. **Fill folders by uploading into the current folder** — no moving of existing files yet.
5. **Permissions:** create folder = `upload_files`; delete folder = `manage_files`;
   list = `view_files` (unchanged).
6. **Verification:** full stack — rebuild telos-core and verify against real disk.

## Architecture (Approach A: per-directory listing)

### Backend — shared path-safety helper (new, unit-tested)

```
const mediaRoot = "/data/shared/media"

// resolveMediaPath joins a caller-supplied relative path to the media root,
// lexically cleans it, and confirms the result stays inside the root.
// Returns the absolute on-disk path or an error for traversal/escape attempts.
func resolveMediaPath(rel string) (string, error)
```

Rules: reject absolute input and any `..` that escapes; `rel == ""` resolves to
the root itself. Implementation: `clean := filepath.Clean(filepath.Join(mediaRoot, rel))`,
require `clean == mediaRoot || strings.HasPrefix(clean, mediaRoot+"/")`. This
replaces the guard currently duplicated in `handleDownloadFile` and
`handleDeleteFile`, and is reused by list, mkdir, rmdir, and upload targeting.

### Backend — endpoints

**`GET /api/v1/files?path=<rel>&page=N`** (`view_files`) — list **immediate
children** of `mediaRoot/<path>`:

```json
{
  "path": "vacation/2024",
  "folders": [ { "id": "<base64url(rel)>", "name": "beach", "path": "vacation/2024/beach" } ],
  "files":   [ { "id": "<base64url(rel)>", "filename": "sunset.jpg", "sha256": "",
                 "uploader_id": "", "scan_status": "clean", "size_bytes": 12345,
                 "mime_type": "image/jpeg", "created_at": "..." } ],
  "page": 1,
  "hasNext": false
}
```

- Reads directory entries with `os.ReadDir` (not a recursive walk). Directories →
  `folders`; regular files → `files`. Dotfiles/dotdirs skipped.
- `files[].filename` is the **basename**; `id` remains `base64url(fullRelPath)` so
  download/delete are untouched. `sha256` is dropped to `""` (listing no longer
  hashes paths — it was a synthetic value anyway).
- Ordering: folders alpha-sorted first, then files newest-first. Pagination at 20
  over the combined `folders ++ files` sequence; `hasNext = (offset+20) < total`.
- Invalid/escaping `path` → 400; missing dir → 404.

**`POST /api/v1/folders`** (`upload_files`) — body `{ "path": "<parentRel>", "name": "<segment>" }`:
- Validate `name`: non-empty after trim, length ≤ 255, no `/`, no NUL, not `.` or `..`.
- Resolve parent via `resolveMediaPath(path)`; target = `join(parent, name)`.
- `os.Mkdir(target, 0755)` (single level; parent must already exist).
- Already exists → 409; parent missing → 404; invalid name → 400.
- 201 `{ "id": base64url(rel), "name": name, "path": rel }`.

**`DELETE /api/v1/folders/{id}`** (`manage_files`):
- Decode id → rel → `resolveMediaPath`. Reject if not a directory (400) or equals
  root (400).
- `os.Remove(target)`. On a non-empty directory `os.Remove` fails natively; detect
  and return **409 "folder isn't empty"**. Other errors → 500. Success → 200
  `{ "status": "success" }`.

**Upload targeting** — `processUploadWithLimits`, media branch only
(`isBook == false && writeResponse == true`):
- Read `r.FormValue("path")`; if set, `destDir = resolveMediaPath(path)` (400 on
  bad path); else `destDir = mediaRoot`.
- `destKey = getUniqueFilename(destDir, header.Filename)` (unchanged collision logic).
- `relKey = filepath.Join(path, destKey)`; `fileID = base64url(relKey)`.
- Bookdrop and the legacy library-move branch (`writeResponse == false`) are
  unchanged and ignore `path`.

Routes added next to the existing file routes:
```
mux.Handle("POST /api/v1/folders",        withAuth(http.HandlerFunc(handleCreateFolder), "upload_files"))
mux.Handle("DELETE /api/v1/folders/{id}", withAuth(http.HandlerFunc(handleDeleteFolder), "manage_files"))
```

### Frontend

**`useFilesStore`** — add `path: string` (""=root) and `folders: FolderEntry[]`;
`FolderEntry = { id: string; name: string; path: string }`. Response type gains
`folders`/`path`/`hasNext`; normalize null arrays with the existing `asList`.
- `fetchDir(path, page)` replaces `fetchPage` (calls `/api/v1/files?path=<enc>&page=N`).
- `enterFolder(folderPath)` → `fetchDir(folderPath, 1)`.
- `navigateTo(path)` (breadcrumb jump) → `fetchDir(path, 1)`.
- `createFolder(name)` → POST `/api/v1/folders` `{ path, name }`, then refresh; 403/409
  → notice.
- `deleteFolder(id)` → DELETE, then refresh; 409 → "folder isn't empty — clear it
  first"; 403 → permission notice.
- `upload` attaches the current `path`; after success refreshes the **current** dir
  (not page 1).

**`lib/upload.ts`** — `uploadFile(path, file, onProgress, extra?)` where
`extra?: Record<string,string>` is appended to the `FormData` (store passes `{ path }`).

**`files/page.tsx`:**
- **Breadcrumb bar**: `Home / vacation / 2024`, each segment clickable → `navigateTo`.
- **Folder rows** above file rows: folder icon, name, click row to enter, trailing
  delete action (inline-confirm like files).
- **New folder** control: a button that reveals an inline text input; Enter creates.
- **Drop zone hint** shows the current folder ("uploading to /vacation/2024").
- File rows, download, delete, pager: unchanged. Library/bookdrop toggle: unchanged;
  when bookdrop is selected the drop zone notes uploads go to the flat bookdrop
  regardless of the current folder.

**`files.css`** — `.crumbs`/`.crumb`, `.frow.folder` (clickable), reuse existing
row/pill/action classes; DS tokens only.

## Error handling summary

| Case | Status | UI |
|---|---|---|
| list: bad/escaping `path` | 400 | store error state |
| list: dir missing | 404 | store error state |
| create: invalid name | 400 | notice |
| create: exists | 409 | notice "a folder with that name already exists" |
| create/delete: no permission | 403 | permission notice |
| delete: folder not empty | 409 | notice "folder isn't empty — clear it first" |
| empty directory | 200 | empty-state placeholder |

## Testing & verification

**Backend (Go, container):**
- `resolveMediaPath` table test: `""`→root; `a/b`→joined; `../etc`, `/etc`,
  `a/../../x` → error; trailing/duplicate slashes cleaned.
- mkdir: happy (dir exists on disk), duplicate → 409, invalid name (`..`, contains
  `/`) → 400.
- rmdir: empty → 200 + gone from disk; non-empty → 409 + still present.
- list: temp root with a subdir + files returns correct `folders`/`files`, basenames,
  and pagination boundary (`hasNext`).
- To make handlers testable, `mediaRoot` becomes a package var so tests can point it
  at `t.TempDir()`.

**Frontend:**
- `npm run lint` + `npm run build` clean.
- Extend credentialed `frontend/e2e/files.spec.ts` (Librarian account): create folder
  → enter → upload inside → the file appears inside and is **absent** at root →
  delete file → delete now-empty folder → creating a folder, uploading into it, then
  attempting to delete it shows the non-empty notice.

**Full stack:**
- Rebuild + `--force-recreate` telos-core; `curl /api/v1/health`; no mock-fallback
  warnings in `podman logs telos-core`.
- Confirm on disk: `podman exec telos-core ls /data/shared/media/<folder>` appears
  after create and is gone after delete.

## Out of scope

- Moving or renaming existing files/folders.
- Recursive (non-empty) folder deletion.
- Drag-and-drop between folders.
- Per-folder permissions or ownership.
- The bookdrop and legacy DB-backed library-move paths (untouched).
- The large uncommitted WIP in the tree (settings, voice, logos) — not modified;
  `main.go`/`Dockerfile` edits staged surgically.
