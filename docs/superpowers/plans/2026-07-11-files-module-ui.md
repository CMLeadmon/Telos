# Files Module UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Files placeholder page with a feature-complete module: paginated file list, drag-and-drop upload (library + bookdrop) with progress and scan states, download, and delete — wired to the existing gateway `/api/v1/files*` endpoints.

**Architecture:** One Zustand store (`useFilesStore`) + one client page component + one module CSS file, mirroring the Chat module's structure. A small XHR wrapper (`lib/upload.ts`) exists only because `fetch` cannot report upload progress and the shared `api()` helper forces a JSON content type (wrong for multipart).

**Tech Stack:** Next.js 16 static export, React 19, Zustand, lucide-react icons, Playwright for e2e. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-11-files-module-ui-design.md`

## Global Constraints

- **No backend changes.** The gateway endpoints are the fixed contract (see spec table).
- **Do not touch the uncommitted Stream WIP**: `frontend/src/stores/useMediaStore.ts`, `frontend/src/app/(shell)/stream/page.tsx`, `frontend/src/styles/stream.css`. `frontend/src/app/layout.tsx` may gain ONE import line (`files.css`) — change nothing else in it. Stage files individually in every commit; never `git add -A`.
- **No mock data in the frontend.** Errors surface as error states.
- **No unit-test framework exists in `frontend/`** (Playwright e2e only, by repo convention). Per-task verification is `npm run lint && npm run build`; behavior is verified by the e2e task and live full-stack checks.
- All uploads: 100 MiB cap; extensions `.pdf .epub .jpg .jpeg .png .webp .mp3 .m4a .ogg .wav .mp4 .webm`; bookdrop restricted to `.pdf .epub`. These mirror `backend/main.go` `processUpload` — copy exactly.
- Backend list endpoint returns JSON `null` for an empty page; always normalize to `[]`.
- Commands run from `frontend/`. Commit messages end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.

---

### Task 1: XHR upload helper

**Files:**
- Create: `frontend/src/lib/upload.ts`

**Interfaces:**
- Consumes: `apiBase()` from `@/lib/api`.
- Produces: `uploadFile(path: string, file: File, onProgress: (phase: UploadPhase, percent: number) => void): Promise<UploadOutcome>`, `type UploadPhase = "uploading" | "scanning"`, `interface UploadOutcome { id; filename; sha256; scan_status: string }`, `class UploadError extends Error { status: number }`. Task 2 imports all of these.

- [ ] **Step 1: Write the file**

```typescript
// XHR-based multipart upload. The shared api() fetch helper can't report
// upload progress and forces a JSON content type, so uploads go through here.
// Phases: "uploading" (0-100% of bytes sent) then "scanning" (server-side
// hash + ClamAV pass before it responds).

import { apiBase } from "@/lib/api";

export type UploadPhase = "uploading" | "scanning";

export interface UploadOutcome {
  id: string;
  filename: string;
  sha256: string;
  scan_status: string;
}

export class UploadError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export function uploadFile(
  path: string,
  file: File,
  onProgress: (phase: UploadPhase, percent: number) => void,
): Promise<UploadOutcome> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${apiBase()}${path}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      const percent = Math.round((e.loaded / e.total) * 100);
      onProgress(percent >= 100 ? "scanning" : "uploading", percent);
    };
    xhr.upload.onload = () => onProgress("scanning", 100);
    xhr.onerror = () =>
      reject(new UploadError(0, "network error during upload"));
    xhr.onload = () => {
      if (xhr.status === 201) {
        try {
          resolve(JSON.parse(xhr.responseText) as UploadOutcome);
        } catch {
          reject(new UploadError(xhr.status, "malformed upload response"));
        }
      } else {
        reject(
          new UploadError(
            xhr.status,
            xhr.responseText.trim() || xhr.statusText,
          ),
        );
      }
    };
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });
}
```

- [ ] **Step 2: Verify it compiles and lints**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0. (`upload.ts` is not imported yet; the build proves it parses/typechecks.)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/upload.ts
git commit -m "feat(frontend): add XHR upload helper with progress + scan phases"
```

---

### Task 2: Files store

**Files:**
- Create: `frontend/src/stores/useFilesStore.ts`

**Interfaces:**
- Consumes: `api`, `ApiError` from `@/lib/api`; `uploadFile`, `UploadError`, `UploadPhase` from `@/lib/upload` (Task 1).
- Produces (Task 4 imports all of these): `useFilesStore` hook; `interface FileEntry { id; filename; sha256; uploader_id; scan_status; size_bytes: number; mime_type; created_at: string }`; `type UploadDestination = "library" | "bookdrop"`; `interface ActiveUpload { key: number; filename: string; destination: UploadDestination; state: UploadState }`; `type UploadState = { phase: "uploading"; percent: number } | { phase: "scanning" } | { phase: "error"; message: string }`; `validateFile(file: File, destination: UploadDestination): string | null`; `PAGE_SIZE` (20). Store state/actions: `files: FileEntry[]`, `page: number`, `status: "idle" | "loading" | "ready" | "error"`, `error: string | null`, `hasNextPage: boolean`, `uploads: ActiveUpload[]`, `notice: string | null`, `fetchPage(page: number): Promise<void>`, `deleteFile(id: string): Promise<void>`, `upload(file: File, destination: UploadDestination): Promise<void>`, `dismissUpload(key: number): void`, `setNotice(notice: string | null): void`.

- [ ] **Step 1: Write the file**

```typescript
import { create } from "zustand";
import { api, ApiError } from "@/lib/api";
import { uploadFile, UploadError } from "@/lib/upload";

export interface FileEntry {
  id: string;
  filename: string;
  sha256: string;
  uploader_id: string;
  scan_status: string;
  size_bytes: number;
  mime_type: string;
  created_at: string;
}

export type UploadDestination = "library" | "bookdrop";

export type UploadState =
  | { phase: "uploading"; percent: number }
  | { phase: "scanning" }
  | { phase: "error"; message: string };

export interface ActiveUpload {
  key: number;
  filename: string;
  destination: UploadDestination;
  state: UploadState;
}

export const PAGE_SIZE = 20;
export const MAX_UPLOAD_BYTES = 104857600; // 100 MiB, mirrors the gateway cap

// Mirrors the gateway's processUpload allowlists exactly.
const LIBRARY_EXTENSIONS = [
  ".pdf", ".epub", ".jpg", ".jpeg", ".png", ".webp",
  ".mp3", ".m4a", ".ogg", ".wav", ".mp4", ".webm",
];
const BOOK_EXTENSIONS = [".pdf", ".epub"];

export function validateFile(
  file: File,
  destination: UploadDestination,
): string | null {
  const dot = file.name.lastIndexOf(".");
  const ext = dot === -1 ? "" : file.name.slice(dot).toLowerCase();
  const allowed =
    destination === "bookdrop" ? BOOK_EXTENSIONS : LIBRARY_EXTENSIONS;
  if (!allowed.includes(ext)) {
    return destination === "bookdrop"
      ? "bookdrop only accepts .pdf and .epub"
      : `file type ${ext || "(none)"} is not allowed`;
  }
  if (file.size > MAX_UPLOAD_BYTES) return "file exceeds the 100 MiB limit";
  return null;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

let uploadCounter = 0;

interface FilesState {
  files: FileEntry[];
  page: number;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  hasNextPage: boolean;
  uploads: ActiveUpload[];
  notice: string | null;
  fetchPage: (page: number) => Promise<void>;
  deleteFile: (id: string) => Promise<void>;
  upload: (file: File, destination: UploadDestination) => Promise<void>;
  dismissUpload: (key: number) => void;
  setNotice: (notice: string | null) => void;
}

export const useFilesStore = create<FilesState>()((set, get) => ({
  files: [],
  page: 1,
  status: "idle",
  error: null,
  hasNextPage: false,
  uploads: [],
  notice: null,

  fetchPage: async (page) => {
    set({ status: "loading", error: null });
    try {
      const rows = await api<FileEntry[] | null>(`/api/v1/files?page=${page}`);
      const files = asList(rows);
      set({
        files,
        page,
        status: "ready",
        hasNextPage: files.length === PAGE_SIZE,
      });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load files",
      });
    }
  },

  deleteFile: async (id) => {
    try {
      await api(`/api/v1/files/${id}`, { method: "DELETE" });
      const { page, files } = get();
      // Deleting the last row of a later page: step back so the view
      // doesn't land on an empty page.
      const target = files.length === 1 && page > 1 ? page - 1 : page;
      await get().fetchPage(target);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to delete files" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "delete failed",
        });
      }
    }
  },

  upload: async (file, destination) => {
    const key = ++uploadCounter;
    const entry: ActiveUpload = {
      key,
      filename: file.name,
      destination,
      state: { phase: "uploading", percent: 0 },
    };
    set((s) => ({ uploads: [...s.uploads, entry] }));
    const patch = (state: UploadState) =>
      set((s) => ({
        uploads: s.uploads.map((u) => (u.key === key ? { ...u, state } : u)),
      }));

    const path =
      destination === "bookdrop" ? "/api/v1/files/books" : "/api/v1/files";
    try {
      await uploadFile(path, file, (phase, percent) =>
        patch(
          phase === "scanning"
            ? { phase: "scanning" }
            : { phase: "uploading", percent },
        ),
      );
      set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) }));
      await get().fetchPage(1);
    } catch (err) {
      let message = "upload failed";
      if (err instanceof UploadError) {
        if (err.status === 403)
          message = "you don't have permission to upload here";
        else if (err.status === 422) {
          message = "rejected by security scan";
          // The gateway keeps infected uploads as audit rows — show it.
          void get().fetchPage(1);
        } else if (err.status === 503)
          message = "security scanner unavailable — try again later";
        else if (err.message) message = err.message;
      }
      patch({ phase: "error", message });
    }
  },

  dismissUpload: (key) =>
    set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) })),

  setNotice: (notice) => set({ notice }),
}));
```

- [ ] **Step 2: Verify it compiles and lints**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/stores/useFilesStore.ts
git commit -m "feat(frontend): add files store (list, paging, delete, upload queue)"
```

---

### Task 3: Module stylesheet

**Files:**
- Create: `frontend/src/styles/files.css`
- Modify: `frontend/src/app/layout.tsx` (add one import line after the `stream.css` import — touch nothing else; this file has other uncommitted WIP changes)

**Interfaces:**
- Produces: CSS classes consumed by Task 4's page: `.fwrap`, `.dropzone` (+`.drag`, `.dz-hint`), `.destrow`, `.fnotice`, `.uprow` (+`.err`, `.upname`, `.upstate`), `.upbar` (+`.scan`), `.ftable`, `.frow` (+`.head`, `.infected`, `.fname`, `.fmeta`, `.ficon`), `.scanpill` (+`.clean`, `.infected`), `.facts` (+`.danger`), `.fpager` (+`.pageno`).

- [ ] **Step 1: Write `frontend/src/styles/files.css`**

```css
/* Files module — drop zone, upload rows, file table (DS tokens throughout) */
.fwrap{flex:1;min-height:0;display:flex;flex-direction:column;gap:14px;padding:22px 26px;
  overflow-y:auto;position:relative;z-index:2}

.dropzone{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:8px;
  padding:26px;border:1.5px dashed var(--line-2);border-radius:var(--r-lg);color:var(--muted);
  cursor:pointer;transition:border-color .12s,background .12s;text-align:center}
.dropzone:hover,.dropzone.drag{border-color:var(--accent);background:rgba(2,181,255,.05);color:var(--ink)}
.dropzone .dz-hint{font-family:var(--f-mono);font-size:11px;letter-spacing:.08em;color:var(--faint)}

.destrow{display:flex;align-items:center;gap:8px}

.fnotice{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:10px 14px;
  border:1px solid var(--line-2);border-left:3px solid var(--rose);border-radius:var(--r);
  font-family:var(--f-mono);font-size:11.5px;color:var(--muted);background:var(--surface-2)}

.uprow{display:flex;align-items:center;gap:12px;padding:10px 14px;border:1px solid var(--line-2);
  border-radius:var(--r);background:var(--surface-2);font-size:13px}
.uprow .upname{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.uprow .upstate{font-family:var(--f-mono);font-size:10.5px;letter-spacing:.1em;text-transform:uppercase;
  color:var(--faint);flex:none}
.uprow.err{border-left:3px solid var(--rose)}
.uprow.err .upstate{color:var(--rose);text-transform:none;letter-spacing:.04em}
.upbar{flex:2;max-width:280px;height:6px;border-radius:var(--r-pill);background:var(--input);overflow:hidden}
.upbar b{display:block;height:100%;background:var(--accent);transition:width .15s}
.upbar.scan b{width:40%;animation:fscan 1.1s ease-in-out infinite alternate}
@keyframes fscan{from{margin-left:0}to{margin-left:60%}}

.ftable{display:flex;flex-direction:column;border:1px solid var(--line);border-radius:var(--r-lg);
  overflow:hidden;background:var(--surface)}
.frow{display:grid;grid-template-columns:40px minmax(0,1fr) 96px 112px 120px 92px;align-items:center;
  gap:8px;padding:10px 16px;border-bottom:1px solid var(--line);font-size:13.5px}
.frow:last-child{border-bottom:0}
.frow.head{font-family:var(--f-mono);font-size:10.5px;letter-spacing:.12em;text-transform:uppercase;
  color:var(--faint);background:var(--surface-2)}
.frow .ficon{color:var(--faint);display:flex}
.frow .fname{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:500}
.frow .fmeta{font-family:var(--f-mono);font-size:11.5px;color:var(--muted)}
.frow.infected .ficon,.frow.infected .fname,.frow.infected .fmeta{opacity:.45}

.scanpill{font-family:var(--f-mono);font-size:10px;letter-spacing:.08em;text-transform:uppercase;
  padding:2px 8px;border-radius:var(--r-pill);box-shadow:inset 0 0 0 1px var(--line-2);
  color:var(--muted);justify-self:start}
.scanpill.clean{color:var(--cyan);box-shadow:inset 0 0 0 1px var(--cyan)}
.scanpill.infected{color:var(--rose);box-shadow:inset 0 0 0 1px var(--rose)}

.facts{display:flex;gap:4px;justify-content:flex-end}
.facts .iconbtn{width:32px;height:32px}
.facts .iconbtn.danger:hover{color:var(--rose)}

.fpager{display:flex;align-items:center;justify-content:center;gap:14px;padding:4px 0}
.fpager .pageno{font-family:var(--f-mono);font-size:11.5px;letter-spacing:.1em;color:var(--faint)}
```

- [ ] **Step 2: Add the import to `frontend/src/app/layout.tsx`**

Change (one line added after the existing `stream.css` import; leave everything else untouched):

```typescript
import "@/styles/stream.css";
import "@/styles/files.css";
```

- [ ] **Step 3: Verify build**

Run (from `frontend/`): `npm run build`
Expected: exit 0.

- [ ] **Step 4: Commit (stage only these two files — layout.tsx's other hunks belong to WIP)**

`layout.tsx` should contain ONLY the one-line import on top of the WIP state, so staging the whole file would drag WIP in — verify with `git diff frontend/src/app/layout.tsx` that the file's diff vs HEAD includes WIP hunks; if it does, use `git add -p` and stage only the files.css import hunk.

```bash
git add frontend/src/styles/files.css
git add -p frontend/src/app/layout.tsx   # stage ONLY the files.css import hunk
git commit -m "feat(frontend): add files module stylesheet"
```

---

### Task 4: Files page component

**Files:**
- Modify: `frontend/src/app/(shell)/files/page.tsx` (full rewrite of the 14-line placeholder)

**Interfaces:**
- Consumes: everything Task 2 produces; `apiBase` from `@/lib/api`; `VaporwaveScene` from `@/components/VaporwaveScene`; CSS classes from Task 3.
- Produces: the `/files` route. Test hooks for Task 5: `data-testid="files-table"`, `data-testid="files-empty"`, `data-testid="files-dropzone"`, `data-testid="files-input"` (the hidden `<input type="file">`), rows carry `data-testid="file-row"`.

- [ ] **Step 1: Rewrite the page**

```tsx
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import {
  BookOpen,
  Check,
  ChevronLeft,
  ChevronRight,
  Download,
  File as FileIcon,
  FileText,
  Film,
  Folder,
  Image as ImageIcon,
  Music,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { apiBase } from "@/lib/api";
import {
  type FileEntry,
  type UploadDestination,
  useFilesStore,
  validateFile,
} from "@/stores/useFilesStore";
import { VaporwaveScene } from "@/components/VaporwaveScene";

function mimeIcon(mime: string) {
  if (mime.startsWith("image/")) return ImageIcon;
  if (mime.startsWith("audio/")) return Music;
  if (mime.startsWith("video/")) return Film;
  if (mime === "application/pdf" || mime === "application/epub+zip")
    return FileText;
  return FileIcon;
}

function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

function shortDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function FileRow({
  file,
  confirming,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  file: FileEntry;
  confirming: boolean;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const Icon = mimeIcon(file.mime_type);
  const infected = file.scan_status !== "clean";
  return (
    <div
      className={`frow${infected ? " infected" : ""}`}
      data-testid="file-row"
    >
      <span className="ficon">
        <Icon size={17} />
      </span>
      <span className="fname" title={file.filename}>
        {file.filename}
      </span>
      <span className="fmeta">{humanSize(file.size_bytes)}</span>
      <span className={`scanpill ${infected ? "infected" : "clean"}`}>
        {file.scan_status}
      </span>
      <span className="fmeta">{shortDate(file.created_at)}</span>
      <span className="facts">
        {confirming ? (
          <>
            <button
              className="iconbtn danger"
              aria-label={`confirm delete ${file.filename}`}
              onClick={onDelete}
            >
              <Check size={16} />
            </button>
            <button
              className="iconbtn"
              aria-label="cancel delete"
              onClick={onCancelDelete}
            >
              <X size={16} />
            </button>
          </>
        ) : (
          <>
            {!infected && (
              <a
                className="iconbtn"
                aria-label={`download ${file.filename}`}
                href={`${apiBase()}/api/v1/files/${file.id}/download`}
              >
                <Download size={16} />
              </a>
            )}
            <button
              className="iconbtn danger"
              aria-label={`delete ${file.filename}`}
              onClick={onAskDelete}
            >
              <Trash2 size={16} />
            </button>
          </>
        )}
      </span>
    </div>
  );
}

export default function FilesPage() {
  const {
    files,
    page,
    status,
    error,
    hasNextPage,
    uploads,
    notice,
    fetchPage,
    deleteFile,
    upload,
    dismissUpload,
    setNotice,
  } = useFilesStore();
  const [destination, setDestination] = useState<UploadDestination>("library");
  const [dragging, setDragging] = useState(false);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (status === "idle") void fetchPage(1);
  }, [status, fetchPage]);

  const takeFiles = useCallback(
    (list: FileList | null) => {
      if (!list || list.length === 0) return;
      if (list.length > 1)
        setNotice("one file at a time — uploading the first only");
      const file = list[0];
      const problem = validateFile(file, destination);
      if (problem) {
        setNotice(problem);
        return;
      }
      void upload(file, destination);
    },
    [destination, upload, setNotice],
  );

  return (
    <>
      <VaporwaveScene />
      <div className="arenahead">
        <div className="name">
          <Folder size={17} />
          Files
        </div>
        <span className="kicker">{`// page ${page}`}</span>
      </div>
      <div className="banner">
        every byte is scanned before it touches the shelf
      </div>

      <div className="fwrap">
        {notice && (
          <div className="fnotice" role="status">
            <span>{notice}</span>
            <button
              className="iconbtn"
              aria-label="dismiss notice"
              onClick={() => setNotice(null)}
            >
              <X size={15} />
            </button>
          </div>
        )}

        <div
          className={`dropzone${dragging ? " drag" : ""}`}
          data-testid="files-dropzone"
          onClick={() => inputRef.current?.click()}
          onDragOver={(e) => {
            e.preventDefault();
            setDragging(true);
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            takeFiles(e.dataTransfer.files);
          }}
        >
          <UploadCloud size={26} />
          <div>
            drop a file here or <b>click to browse</b>
          </div>
          <span className="dz-hint">
            {destination === "bookdrop"
              ? "pdf / epub → bookdrop, max 100 MiB"
              : "pdf, epub, images, audio, video — max 100 MiB"}
          </span>
          <input
            ref={inputRef}
            type="file"
            hidden
            data-testid="files-input"
            onChange={(e) => {
              takeFiles(e.target.files);
              e.target.value = "";
            }}
          />
        </div>

        <div className="destrow">
          <span className="kicker">{"// destination"}</span>
          <button
            className={`chip${destination === "library" ? " on" : ""}`}
            onClick={() => setDestination("library")}
          >
            <Folder size={13} /> library
          </button>
          <button
            className={`chip${destination === "bookdrop" ? " on" : ""}`}
            onClick={() => setDestination("bookdrop")}
          >
            <BookOpen size={13} /> bookdrop
          </button>
        </div>

        {uploads.map((u) => (
          <div
            key={u.key}
            className={`uprow${u.state.phase === "error" ? " err" : ""}`}
          >
            <span className="upname">{u.filename}</span>
            {u.state.phase === "uploading" && (
              <>
                <span className="upbar">
                  <b style={{ width: `${u.state.percent}%` }} />
                </span>
                <span className="upstate">{u.state.percent}%</span>
              </>
            )}
            {u.state.phase === "scanning" && (
              <>
                <span className="upbar scan">
                  <b />
                </span>
                <span className="upstate">scanning…</span>
              </>
            )}
            {u.state.phase === "error" && (
              <>
                <span className="upstate">{u.state.message}</span>
                <button
                  className="iconbtn"
                  aria-label="dismiss upload"
                  onClick={() => dismissUpload(u.key)}
                >
                  <X size={15} />
                </button>
              </>
            )}
          </div>
        ))}

        {status === "error" && (
          <div className="placeholder">
            <h2>Shelf unreachable.</h2>
            <p>{error}</p>
            <button className="btn-ghost btn-sm" onClick={() => fetchPage(page)}>
              retry
            </button>
          </div>
        )}

        {status === "ready" && files.length === 0 && (
          <div className="placeholder" data-testid="files-empty">
            <h2>Nothing on the shelf.</h2>
            <p>drop something above — it gets scanned, hashed and kept.</p>
          </div>
        )}

        {files.length > 0 && (
          <div className="ftable" data-testid="files-table">
            <div className="frow head">
              <span />
              <span>name</span>
              <span>size</span>
              <span>scan</span>
              <span>added</span>
              <span style={{ textAlign: "right" }}>actions</span>
            </div>
            {files.map((f) => (
              <FileRow
                key={f.id}
                file={f}
                confirming={confirmingId === f.id}
                onAskDelete={() => setConfirmingId(f.id)}
                onCancelDelete={() => setConfirmingId(null)}
                onDelete={() => {
                  setConfirmingId(null);
                  void deleteFile(f.id);
                }}
              />
            ))}
          </div>
        )}

        {(page > 1 || hasNextPage) && (
          <div className="fpager">
            <button
              className="iconbtn"
              aria-label="previous page"
              disabled={page <= 1 || status === "loading"}
              onClick={() => fetchPage(page - 1)}
            >
              <ChevronLeft size={17} />
            </button>
            <span className="pageno">page {page}</span>
            <button
              className="iconbtn"
              aria-label="next page"
              disabled={!hasNextPage || status === "loading"}
              onClick={() => fetchPage(page + 1)}
            >
              <ChevronRight size={17} />
            </button>
          </div>
        )}
      </div>
    </>
  );
}
```

- [ ] **Step 2: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: both exit 0. If lint complains about the `<a>` download link (`@next/next/no-html-link-for-pages` shouldn't fire for external-ish URLs, but if it does), keep the `<a>` — it must be a real navigation so the browser handles `Content-Disposition: attachment` — and add a targeted `// eslint-disable-next-line` with the rule name.

- [ ] **Step 3: Quick visual check in dev**

With the gateway up (`podman-compose up -d`) and `npm run dev` running: open `http://localhost:3000/files/`, log in, confirm the drop zone, destination chips, and table/empty state render over the vaporwave scene in both themes (toggle via the sun/moon topbar button).

- [ ] **Step 4: Commit**

```bash
git add "frontend/src/app/(shell)/files/page.tsx"
git commit -m "feat(frontend): build Files module UI (list, upload, download, delete)"
```

---

### Task 5: E2E spec + full-stack verification

**Files:**
- Create: `frontend/e2e/files.spec.ts`

**Interfaces:**
- Consumes: Task 4's test ids; existing login page (`/login/`, `#username`, `#password`, submit button labeled `Enter the node` — verify the actual label in `frontend/src/app/login/page.tsx` before writing, and use whatever it is).
- Produces: nothing downstream.

- [ ] **Step 1: Check the login form's field ids and submit-button label**

Read `frontend/src/app/login/page.tsx`; adjust the spec's login helper to match reality (ids below assume `#username` / `#password` like the bootstrap spec).

- [ ] **Step 2: Write the spec**

Env-gated like `bootstrap.spec.ts` — needs a live gateway and a real account:
`E2E_USERNAME=owner E2E_PASSWORD=... npx playwright test files`

```typescript
import { test, expect } from "@playwright/test";

// Credentialed run against a live gateway:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test files
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

// Tiny valid PNG (1x1 transparent) — passes the gateway's magic-byte sniff.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.locator("form button[type=submit]").first().click();
  await page.waitForURL("**/chat/**");
}

test("files module lists, uploads and deletes", async ({ page }) => {
  await login(page);
  await page.goto("/files/");
  await expect(page.getByTestId("files-dropzone")).toBeVisible();

  const name = `e2e-${Math.random().toString(36).slice(2)}.png`;
  await page.getByTestId("files-input").setInputFiles({
    name,
    mimeType: "image/png",
    buffer: PNG,
  });

  const row = page
    .getByTestId("file-row")
    .filter({ hasText: name })
    .first();
  await expect(row).toBeVisible({ timeout: 30_000 }); // upload + ClamAV scan

  await expect(row.locator(".scanpill")).toHaveText("clean");

  await row.getByLabel(`delete ${name}`).click();
  await row.getByLabel(`confirm delete ${name}`).click();
  await expect(
    page.getByTestId("file-row").filter({ hasText: name }),
  ).toHaveCount(0, { timeout: 15_000 });
});
```

- [ ] **Step 3: Run the full stack and the spec**

```bash
podman-compose up -d --build        # from repo root; needs a filled .env
cd frontend && npm run dev          # separate terminal / background
E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test files
```

Expected: 1 passed. Also run `npx playwright test smoke` — 3 passed (no regressions).

- [ ] **Step 4: EICAR rejection check (manual, proves the 422 UI)**

The EICAR string is the industry-standard harmless antivirus test file. In the browser on `/files/`, upload a file named `eicar.pdf` containing exactly:
`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
Expected: upload row flips to *scanning…* then shows the red "rejected by security scan" state, and after refresh the list shows a dimmed `infected` audit row with no download action. (Note: ClamAV sniffs content, not extension — the `.pdf` name passes the client allowlist; the gateway MIME sniff returns `application/octet-stream` for it, which is allowlisted.)

- [ ] **Step 5: Commit**

```bash
git add frontend/e2e/files.spec.ts
git commit -m "test(frontend): add credentialed e2e for the Files module"
```

---

## Final verification (whole feature)

- [ ] `npm run lint && npm run build` clean from `frontend/`.
- [ ] `curl http://localhost:8080/api/v1/health` returns OK; `podman logs telos-core` shows **no mock-fallback warnings** during the e2e run.
- [ ] Download check: clicking download on the e2e-uploaded file actually saves the attachment (verify once manually — cookies must ride the top-level navigation in dev's cross-origin setup; if the dev-mode download 401s, that's a finding to report, and the production single-origin path still works via `npm run build` + gateway embed).
- [ ] Stream WIP untouched: `git status` shows `useMediaStore.ts`, `stream/page.tsx`, `stream.css` exactly as they were.
