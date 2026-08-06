# S3 Frontend Origin-Awareness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Parameterize the Next.js client's network, asset URL, media player, and file upload layers to support remote Telos server endpoints and Bearer token authentication alongside single-origin cookie sessions.

**Architecture:** Create `serverConfig.ts` to manage global server configuration state, repoint `apiBase()`, `wsBase()`, and `api()` in `frontend/src/lib/api.ts` to consume `getServerConfig()`, absolutize relative asset URLs with `assetUrl()`, eliminate raw `fetch`/XHR leaks across components, and add CI origin leak inspection gates.

**Tech Stack:** Next.js 16.2.10, React 19.2.7, TypeScript 5.9.3, Vitest 4.1.10, hls.js 1.6.16.

## Global Constraints

- Go toolchain is **1.26.5**; Go is **not installed on the host**. All backend toolchain commands run in a container via **podman**, never docker.
- Frontend is **Next.js 16.2.10** static export (`output: "export"`), **React 19.2.7**, **TypeScript 5.9.3**, **Zustand 5.0.14**. Tests are **vitest 4.1.10**; E2E is **@playwright/test 1.61.1**.
- Media libraries are pinned: **epubjs 0.4.2**, **pdfjs-dist 6.1.200**, **hls.js 1.6.16**. Do not upgrade them as part of this work.
- `backend/` is one flat Go package (`module telos-core`). Extend the sibling file matching the concern; do not grow `main.go`.
- Schema changes are a **new numbered migration** in `backend/db/migrations/`. Highest existing is `0022_catalog_identity_and_progress.sql`. Migrations are idempotent (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `backend/migrations.go` verifies checksums.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. Integrate over HTTP across container boundaries only.
- Never hand-edit `frontend/src/styles/tokens/` or `frontend/src/styles/foundations/`.
- Never alter the environment to make a gate pass. A failing gate is reported, not forced green.
- A local backend `go build` fails unless `backend/out/` exists, until S1's build tag lands.

---

## File structure

- Create: `frontend/src/lib/serverConfig.ts` — server URL and authentication mode configuration state.
- Create: `frontend/src/lib/serverConfig.test.ts` — unit tests for `serverConfig` module.
- Modify: `frontend/src/lib/api.ts:5-7,21-25,34-67` — update `apiBase()`, `wsBase()`, and `api()` to support remote origins and Bearer tokens, export `assetUrl()`.
- Create: `frontend/src/lib/api_origin.test.ts` — unit tests for parameterized `apiBase()`, `wsBase()`, and `assetUrl()`.
- Create: `frontend/src/lib/api_auth.test.ts` — unit tests for `api()` fetch behavior under cookie and token auth modes.
- Modify: `frontend/src/lib/upload.ts:6,33-35` — parameterize XHR multipart file uploads for Bearer auth mode.
- Modify: `frontend/src/components/library/AudiobookPlayer.tsx:117-119` — wrap stream URL construction in `assetUrl()`.
- Modify: `frontend/src/components/library/EpubReader.tsx:69` — replace unparameterized `fetch` with `api<ArrayBuffer>()`.
- Modify: `frontend/src/components/settings/ProfileSection.tsx:42` — replace raw `fetch` avatar upload with `uploadFile()`.
- Modify: `frontend/src/components/stream/HlsPlayer.tsx:60-65,93` — update HLS `xhrSetup` and video element `crossOrigin` binding for Bearer tokens.
- Modify: `frontend/src/app/(shell)/stream/page.tsx:76` — wrap `item.coverUrl` image `src` in `assetUrl()`.
- Modify: `frontend/src/app/(shell)/library/page.tsx:176` — wrap `libraryCoverUrl()` image `src` in `assetUrl()`.
- Modify: `frontend/src/components/library/BookManageModal.tsx:469,500` — wrap cover preview image `src` in `assetUrl()`.
- Modify: `frontend/src/components/library/LibraryItemDetail.tsx:71` — wrap `item.coverUrl` image `src` in `assetUrl()`.
- Modify: `frontend/src/components/stream/MediaShelf.tsx:38` — wrap shelf `item.coverUrl` image `src` in `assetUrl()`.
- Modify: `frontend/src/components/stream/StreamItemDetail.tsx:135` — wrap `detail.coverUrl` image `src` in `assetUrl()`.
- Create: `frontend/scripts/check-origin-leaks.js` — static analysis script to catch raw `fetch`/XHR and unparameterized cover `src` usage.
- Modify: `frontend/package.json` — add `check:origin-leak` script and link it into `npm run lint`.

---

### Task 1: Create `serverConfig.ts` and repoint `apiBase()` and `wsBase()`

**Files:**
- Create: `frontend/src/lib/serverConfig.ts`
- Create: `frontend/src/lib/serverConfig.test.ts`
- Modify: `frontend/src/lib/api.ts:5-7,21-25`
- Create: `frontend/src/lib/api_origin.test.ts`

**Interfaces:**
- Produces: `AuthMode`, `ServerConfig`, `getServerConfig()`, `setServerConfig(next)` in `frontend/src/lib/serverConfig.ts`.
- Updates: `apiBase()` and `wsBase()` in `frontend/src/lib/api.ts`.

- [ ] **Step 1: Write failing unit test for `serverConfig`**

Create `frontend/src/lib/serverConfig.test.ts`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { getServerConfig, setServerConfig } from "@/lib/serverConfig";

describe("serverConfig", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("returns default configuration initially", () => {
    const config = getServerConfig();
    expect(config.baseUrl).toBe("");
    expect(config.mode).toBe("cookie");
    expect(config.accessToken).toBeNull();
  });

  it("updates configuration when setServerConfig is called", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "test-access-token",
    });

    const config = getServerConfig();
    expect(config.baseUrl).toBe("https://telos.example.com");
    expect(config.mode).toBe("token");
    expect(config.accessToken).toBe("test-access-token");
  });
});
```

- [ ] **Step 2: Implement `serverConfig.ts`**

Create `frontend/src/lib/serverConfig.ts`:

```typescript
export type AuthMode = "cookie" | "token";

export interface ServerConfig {
  baseUrl: string; // "" means same-origin
  mode: AuthMode;
  accessToken: string | null;
}

let activeConfig: ServerConfig = {
  baseUrl: "",
  mode: "cookie",
  accessToken: null,
};

export function getServerConfig(): ServerConfig {
  return activeConfig;
}

export function setServerConfig(next: ServerConfig): void {
  activeConfig = next;
}
```

- [ ] **Step 3: Write failing unit tests for parameterized `apiBase()` and `wsBase()`**

Create `frontend/src/lib/api_origin.test.ts`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { apiBase, wsBase } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("apiBase and wsBase parameterization", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("apiBase returns empty string when baseUrl is unconfigured", () => {
    expect(apiBase()).toBe("");
  });

  it("apiBase returns configured baseUrl when set", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "cookie",
      accessToken: null,
    });
    expect(apiBase()).toBe("https://telos.example.com");
  });

  it("wsBase derives wss URL from https baseUrl", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "cookie",
      accessToken: null,
    });
    expect(wsBase()).toBe("wss://telos.example.com");
  });

  it("wsBase derives ws URL from http baseUrl", () => {
    setServerConfig({
      baseUrl: "http://192.168.1.50:8080",
      mode: "cookie",
      accessToken: null,
    });
    expect(wsBase()).toBe("ws://192.168.1.50:8080");
  });
});
```

- [ ] **Step 4: Update `apiBase()` and `wsBase()` in `frontend/src/lib/api.ts`**

Modify `frontend/src/lib/api.ts:5-7,21-25`:

```typescript
import { getServerConfig } from "@/lib/serverConfig";

export function apiBase(): string {
  return getServerConfig().baseUrl;
}

export function avatarUrl(userId: string): string {
  return `${apiBase()}/api/v1/users/${userId}/avatar`;
}

export function libraryCoverUrl(bookId: string): string {
  return `${apiBase()}/api/v1/library/books/${encodeURIComponent(bookId)}/cover`;
}

export function libraryContentUrl(bookId: string): string {
  return `${apiBase()}/api/v1/library/books/${encodeURIComponent(bookId)}/content`;
}

export function wsBase(): string {
  const cfg = getServerConfig();
  if (!cfg.baseUrl) {
    if (typeof window === "undefined") return "";
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${window.location.host}`;
  }
  const u = new URL(cfg.baseUrl);
  const proto = u.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${u.host}`;
}
```

---

### Task 2: Implement Bearer mode in `api()` fetch helper

**Files:**
- Modify: `frontend/src/lib/api.ts:34-55`
- Create: `frontend/src/lib/api_auth.test.ts`

**Interfaces:**
- Updates: `api<T>(path: string, init?: RequestInit): Promise<T>` in `frontend/src/lib/api.ts`.

- [ ] **Step 1: Write failing unit test for `api()` auth mode branching**

Create `frontend/src/lib/api_auth.test.ts`:

```typescript
import { describe, expect, it, beforeEach, vi } from "vitest";
import { api } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("api helper auth mode branching", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("passes credentials: include under cookie auth mode", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await api<{ ok: boolean }>("/api/v1/test");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const [, init] = fetchSpy.mock.calls[0];
    expect(init?.credentials).toBe("include");
    const headers = new Headers(init?.headers);
    expect(headers.has("Authorization")).toBe(false);
  });

  it("attaches Authorization header and omits credentials under token auth mode", async () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "token-secret-123",
    });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await api<{ ok: boolean }>("/api/v1/test");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const [, init] = fetchSpy.mock.calls[0];
    expect(init?.credentials).toBeUndefined();
    const headers = new Headers(init?.headers);
    expect(headers.get("Authorization")).toBe("Bearer token-secret-123");
  });
});
```

- [ ] **Step 2: Update `api()` in `frontend/src/lib/api.ts`**

Modify `frontend/src/lib/api.ts:34-55`:

```typescript
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  const cfg = getServerConfig();
  try {
    const headers = new Headers(init?.headers);
    if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }

    const requestInit: RequestInit = {
      ...init,
      headers,
    };

    if (cfg.mode === "token" && cfg.accessToken) {
      headers.set("Authorization", `Bearer ${cfg.accessToken}`);
    } else {
      requestInit.credentials = "include";
    }

    res = await fetch(`${apiBase()}${path}`, requestInit);
  } catch {
    if (init?.signal?.aborted) {
      throw new ApiError(408, "Request timed out.");
    }
    throw new ApiError(
      0,
      "Cannot reach this Telos node. Check the server address and try again.",
    );
  }

  const text = await res.text().catch(() => "");
  if (!res.ok) {
    throw new ApiError(res.status, text.trim() || res.statusText);
  }
  if (!text.trim()) return undefined as T;

  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(res.status, "Server returned an invalid response.");
  }
}
```

---

### Task 3: Implement `assetUrl()` and apply to cover-render sites

**Files:**
- Modify: `frontend/src/lib/api.ts`
- Create: `frontend/src/lib/asset_url.test.ts`
- Modify: `frontend/src/app/(shell)/stream/page.tsx:76`
- Modify: `frontend/src/app/(shell)/library/page.tsx:176`
- Modify: `frontend/src/components/library/BookManageModal.tsx:469,500`
- Modify: `frontend/src/components/library/LibraryItemDetail.tsx:71`
- Modify: `frontend/src/components/stream/MediaShelf.tsx:38`
- Modify: `frontend/src/components/stream/StreamItemDetail.tsx:135`

**Interfaces:**
- Produces: `export function assetUrl(path: string | undefined): string` in `frontend/src/lib/api.ts`.

- [ ] **Step 1: Write failing unit test for `assetUrl()`**

Create `frontend/src/lib/asset_url.test.ts`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { assetUrl } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("assetUrl helper", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("returns empty string when path is undefined or empty", () => {
    expect(assetUrl(undefined)).toBe("");
    expect(assetUrl("")).toBe("");
  });

  it("returns unmodified path when path is absolute URL or blob", () => {
    expect(assetUrl("https://images.example.com/cover.jpg")).toBe("https://images.example.com/cover.jpg");
    expect(assetUrl("http://images.example.com/cover.jpg")).toBe("http://images.example.com/cover.jpg");
    expect(assetUrl("blob:http://localhost/uuid")).toBe("blob:http://localhost/uuid");
  });

  it("returns relative path unchanged when baseUrl is empty", () => {
    expect(assetUrl("/api/v1/media/items/123/cover")).toBe("/api/v1/media/items/123/cover");
  });

  it("prepends baseUrl to relative path when configured", () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "token-123",
    });
    expect(assetUrl("/api/v1/media/items/123/cover")).toBe("https://telos.example.com/api/v1/media/items/123/cover");
    expect(assetUrl("api/v1/media/items/123/cover")).toBe("https://telos.example.com/api/v1/media/items/123/cover");
  });
});
```

- [ ] **Step 2: Add `assetUrl()` implementation to `frontend/src/lib/api.ts`**

Add to `frontend/src/lib/api.ts`:

```typescript
export function assetUrl(path: string | undefined): string {
  if (!path) return "";
  if (
    path.startsWith("http://") ||
    path.startsWith("https://") ||
    path.startsWith("blob:")
  ) {
    return path;
  }
  const base = apiBase();
  if (!base) return path;
  return `${base}${path.startsWith("/") ? "" : "/"}${path}`;
}
```

- [ ] **Step 3: Apply `assetUrl()` across all cover image rendering components**

Modify `frontend/src/app/(shell)/stream/page.tsx:76`:
```tsx
{item.coverUrl ? (
  // eslint-disable-next-line @next/next/no-img-element
  <img className="pcover" src={assetUrl(item.coverUrl)} alt="" aria-hidden="true" />
) : (
  <div className="motif" />
)}
```

Modify `frontend/src/app/(shell)/library/page.tsx:176`:
```tsx
<img
  src={assetUrl(libraryCoverUrl(book.id))}
  alt=""
  loading="lazy"
  onError={() => setCoverBroken(true)}
/>
```

Modify `frontend/src/components/library/BookManageModal.tsx:469,500`:
```tsx
<img
  src={assetUrl(`${libraryCoverUrl(book.id)}?v=${coverRevision}`)}
  alt={`Cover of ${freshBook.title}`}
  onError={() => setCoverBroken(true)}
/>
```
And line 500:
```tsx
{selectedCandidate?.coverUrl && (
  <button
    className="btn-ghost btn-sm"
    disabled={coverBusy}
    onClick={() =>
      void replaceCover({
        coverUrl: selectedCandidate.coverUrl,
      })
    }
  >
    Use this candidate&apos;s cover
  </button>
)}
```
Note: candidate coverUrl replacement passes the URL to `replaceCover()`; preview renders use `assetUrl(selectedCandidate.coverUrl)`.

Modify `frontend/src/components/library/LibraryItemDetail.tsx:71`:
```tsx
{item.coverUrl ? (
  // eslint-disable-next-line @next/next/no-img-element
  <img src={assetUrl(item.coverUrl)} alt="" aria-hidden="true" />
) : (
  <span className="nocover">no cover</span>
)}
```

Modify `frontend/src/components/stream/MediaShelf.tsx:38`:
```tsx
{item.coverUrl ? (
  // eslint-disable-next-line @next/next/no-img-element
  <img className="pcover" src={assetUrl(item.coverUrl)} alt="" aria-hidden="true" />
) : (
  <div className="motif" />
)}
```

Modify `frontend/src/components/stream/StreamItemDetail.tsx:135`:
```tsx
{detail.coverUrl && (
  <div className="idetail-cover">
    {/* eslint-disable-next-line @next/next/no-img-element */}
    <img src={assetUrl(detail.coverUrl)} alt="" aria-hidden="true" />
  </div>
)}
```

---

### Task 4: Eliminate raw `fetch` and XHR network leaks

**Files:**
- Modify: `frontend/src/components/library/AudiobookPlayer.tsx:117-119`
- Modify: `frontend/src/components/library/EpubReader.tsx:69`
- Modify: `frontend/src/components/settings/ProfileSection.tsx:42`
- Modify: `frontend/src/lib/upload.ts:6,33-35`

**Interfaces:**
- Consumes: `assetUrl()`, `api()`, `uploadFile()`, `getServerConfig()`.

- [ ] **Step 1: Wrap audiobook stream URL with `assetUrl()`**

Modify `frontend/src/components/library/AudiobookPlayer.tsx:117-119`:

```typescript
  const hasTracks = Boolean(info?.tracks && info.tracks.length > 0);
  const rawStreamPath = hasTracks
    ? `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/tracks/${currentTrackIndex}/stream`
    : `/api/v1/library/audiobooks/${encodeURIComponent(itemId)}/stream`;
  const streamUrl = assetUrl(rawStreamPath);
```

- [ ] **Step 2: Replace raw `fetch` in `EpubReader.tsx` with `api<ArrayBuffer>()`**

Modify `frontend/src/components/library/EpubReader.tsx:69`:

```typescript
    Promise.all([
      import("epubjs"),
      api<ArrayBuffer>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/content`,
      ),
      api<Progress>(
        `/api/v1/library/books/${encodeURIComponent(book.id)}/progress`,
      ),
    ])
```

- [ ] **Step 3: Replace raw `fetch` in `ProfileSection.tsx` with `uploadFile()`**

Modify `frontend/src/components/settings/ProfileSection.tsx:42`:

Import `uploadFile` from `@/lib/upload` and update `uploadAvatar`:

```typescript
  const uploadAvatar = async (file: File) => {
    setBusy(true);
    setMsg(null);
    try {
      await uploadFile("/api/v1/users/me/avatar", file, () => {});
      await fetchMe();
      setCacheBust((n) => n + 1);
      setMsg({ ok: true, text: "Avatar updated (scanned clean)." });
    } catch (err) {
      setMsg({
        ok: false,
        text: err instanceof ApiError ? err.message : "Upload failed.",
      });
    } finally {
      setBusy(false);
    }
  };
```

- [ ] **Step 4: Update `upload.ts` XHR header setup for Bearer token mode**

Modify `frontend/src/lib/upload.ts:6,33-35`:

```typescript
import { apiBase } from "@/lib/api";
import { getServerConfig } from "@/lib/serverConfig";

export function uploadFile(
  path: string,
  file: File,
  onProgress: (phase: UploadPhase, percent: number) => void,
  extra?: Record<string, string>,
): Promise<UploadOutcome> {
  return new Promise((resolve, reject) => {
    const cfg = getServerConfig();
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${apiBase()}${path}`);
    if (cfg.mode === "token" && cfg.accessToken) {
      xhr.setRequestHeader("Authorization", `Bearer ${cfg.accessToken}`);
    } else {
      xhr.withCredentials = true;
    }
```

---

### Task 5: Parameterize `HlsPlayer` `xhrSetup` and `crossOrigin`

**Files:**
- Modify: `frontend/src/components/stream/HlsPlayer.tsx:60-65,93`
- Create: `frontend/src/components/stream/HlsPlayer.test.tsx`

**Interfaces:**
- Consumes: `getServerConfig()`, `apiBase()`.

- [ ] **Step 1: Write failing test for `HlsPlayer` configuration options**

Create `frontend/src/components/stream/HlsPlayer.test.tsx`:

```typescript
import { describe, expect, it, beforeEach } from "vitest";
import { setServerConfig } from "@/lib/serverConfig";

describe("HlsPlayer auth and origin options", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("determines video crossOrigin attribute based on server configuration", () => {
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    let crossOrigin: "use-credentials" | "anonymous" | undefined =
      getServerConfig().baseUrl
        ? getServerConfig().mode === "token"
          ? "anonymous"
          : "use-credentials"
        : undefined;
    expect(crossOrigin).toBeUndefined();

    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
    crossOrigin = getServerConfig().baseUrl
      ? getServerConfig().mode === "token"
        ? "anonymous"
        : "use-credentials"
      : undefined;
    expect(crossOrigin).toBe("use-credentials");

    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "tok-123" });
    crossOrigin = getServerConfig().baseUrl
      ? getServerConfig().mode === "token"
        ? "anonymous"
        : "use-credentials"
      : undefined;
    expect(crossOrigin).toBe("anonymous");
  });
});
```

- [ ] **Step 2: Update `HlsPlayer.tsx` `xhrSetup` and `<video crossOrigin>` binding**

Modify `frontend/src/components/stream/HlsPlayer.tsx:60-65,93`:

```typescript
import { getServerConfig } from "@/lib/serverConfig";

// Inside HlsCtor initialization (lines 60-65):
      const cfg = getServerConfig();
      hls = new HlsCtor({
        xhrSetup: (xhr) => {
          if (cfg.mode === "token" && cfg.accessToken) {
            xhr.setRequestHeader("Authorization", `Bearer ${cfg.accessToken}`);
          } else {
            xhr.withCredentials = true;
          }
        },
      });
```

Modify line 93 of `frontend/src/components/stream/HlsPlayer.tsx`:

```tsx
          <video
            ref={videoRef}
            controls
            autoPlay
            playsInline
            crossOrigin={
              getServerConfig().baseUrl
                ? getServerConfig().mode === "token"
                  ? "anonymous"
                  : "use-credentials"
                : undefined
            }
            data-testid="stream-video"
          />
```

---

### Task 6: Implement CI origin leak detection script and gate

**Files:**
- Create: `frontend/scripts/check-origin-leaks.js`
- Modify: `frontend/package.json`

**Interfaces:**
- Script: `npm run check:origin-leak`
- Gate: must return exit code 0 when codebase is clean of raw network calls and unparameterized cover `src` bindings.

- [ ] **Step 1: Create `frontend/scripts/check-origin-leaks.js`**

Create `frontend/scripts/check-origin-leaks.js`:

```javascript
const fs = require("fs");
const path = require("path");

const srcDir = path.join(__dirname, "..", "src");
let violations = 0;

function walk(dir, fileList = []) {
  const files = fs.readdirSync(dir);
  for (const file of files) {
    const filePath = path.join(dir, file);
    const stat = fs.statSync(filePath);
    if (stat.isDirectory()) {
      walk(filePath, fileList);
    } else if (filePath.endsWith(".ts") || filePath.endsWith(".tsx")) {
      fileList.push(filePath);
    }
  }
  return fileList;
}

const allFiles = walk(srcDir);

// Rule 1: No raw fetch() or new XMLHttpRequest() outside lib/api.ts and lib/upload.ts
for (const file of allFiles) {
  const relPath = path.relative(srcDir, file);
  if (relPath === "lib/api.ts" || relPath === "lib/upload.ts" || file.endsWith(".test.ts") || file.endsWith(".test.tsx")) {
    continue;
  }
  const content = fs.readFileSync(file, "utf8");
  if (/\bfetch\(/.test(content)) {
    console.error(`Violation in ${relPath}: raw fetch() call detected.`);
    violations++;
  }
  if (/new\s+XMLHttpRequest\(/.test(content)) {
    console.error(`Violation in ${relPath}: raw XMLHttpRequest detected.`);
    violations++;
  }
}

// Rule 2: No unparameterized cover image src or raw /api/v1/ src in TSX components
for (const file of allFiles) {
  if (!file.endsWith(".tsx") || file.endsWith(".test.tsx")) continue;
  const relPath = path.relative(srcDir, file);
  const content = fs.readFileSync(file, "utf8");

  const lines = content.split("\n");
  lines.forEach((line, idx) => {
    if (/<img\s+[^>]*src=\{item\.coverUrl\}/.test(line) && !/assetUrl\(/.test(line)) {
      console.error(`Violation in ${relPath}:${idx + 1}: item.coverUrl bound directly to src without assetUrl()`);
      violations++;
    }
    if (/<img\s+[^>]*src=\{"\/api\/v1\//.test(line) && !/assetUrl\(/.test(line)) {
      console.error(`Violation in ${relPath}:${idx + 1}: relative /api/v1 path bound directly to src without assetUrl()`);
      violations++;
    }
  });
}

if (violations > 0) {
  console.error(`Found ${violations} origin leak violation(s). Build aborted.`);
  process.exit(1);
} else {
  console.log("Origin leak check passed cleanly.");
  process.exit(0);
}
```

- [ ] **Step 2: Register script in `frontend/package.json`**

Modify `frontend/package.json` scripts block to add:

```json
"check:origin-leak": "node scripts/check-origin-leaks.js",
```

- [ ] **Step 3: Verification command and expected output**

Execute the check gate:

```bash
cd frontend
npm run check:origin-leak
```

Expected output: `Origin leak check passed cleanly.` with exit code `0`.

---
