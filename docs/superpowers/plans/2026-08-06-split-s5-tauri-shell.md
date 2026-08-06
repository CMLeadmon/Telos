# Tauri Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package the Telos Next.js frontend as native desktop and mobile applications targeting Windows, macOS, Linux, iOS, and Android using Tauri v2 while preserving 100% of the visual design system and React component architecture.

**Architecture:** Dependent on the S0 platform de-risking spike passing; if S0 fails due to iOS WKWebView incompatibility with `epub.js` cross-frame selection or `pdf.js` worker loading, this Tauri v2 plan is immediately superseded and reverts to Electron for desktop and Capacitor for mobile. The web application codebase remains unified under a platform abstraction layer (`frontend/src/lib/platform.ts`) wrapping external link opening, clipboard access, mobile file pickers, and native background audio MediaSession controls while routing adapts to hash-based navigation for custom Tauri asset schemes (`asset://`).

**Tech Stack:** Next.js 16.2.10 (static export), React 19.2.7, TypeScript 5.9.3, Tauri v2 (`@tauri-apps/api`, `@tauri-apps/plugin-shell`, `@tauri-apps/plugin-clipboard-manager`, `@tauri-apps/plugin-dialog`, `@tauri-apps/plugin-store`), Rust 1.75+, Vitest 4.1.10.

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
- **S0 Contingency:** If S0 de-risking fails on iOS `epub.js` or `pdf.js` worker loading, revert desktop shell to Electron and mobile shell to Capacitor.
- Native filesystem access is strictly scoped via `frontend/src-tauri/capabilities/default.json`.

---

## File structure

- Create: `frontend/src-tauri/tauri.conf.json` — Tauri v2 application configuration, bundle identifier `com.telos.app`, custom asset scheme mapping.
- Create: `frontend/src-tauri/Cargo.toml` — Tauri v2 Cargo workspace manifest and dependencies (`tauri`, `tauri-plugin-shell`, `tauri-plugin-clipboard-manager`, `tauri-plugin-dialog`, `tauri-plugin-store`).
- Create: `frontend/src-tauri/src/lib.rs` — Tauri application builder and plugin initialization.
- Create: `frontend/src-tauri/src/main.rs` — Rust desktop/mobile entrypoint.
- Create: `frontend/src-tauri/capabilities/default.json` — Native permissions boundary manifest.
- Create: `frontend/src/lib/platform.ts` — Unified platform abstraction interface for external link opening, clipboard access, mobile file pickers, and environment detection.
- Create: `frontend/src/lib/__tests__/platform.test.ts` — Platform abstraction unit test suite.
- Create: `frontend/src/lib/navigation.ts` — Hash-aware client navigation helper supporting static HTML routes under `asset://`.
- Create: `frontend/src/lib/__tests__/navigation.test.ts` — Navigation helper unit test suite.
- Modify: `frontend/src/app/(shell)/library/page.tsx` — Replace raw `window.open` at line 153 with `platform.openExternal`.
- Modify: `frontend/src/components/AppShell.tsx` — Replace raw `window.open` at line 198 with `platform.openExternal` and `assetUrl`.
- Modify: `frontend/src/components/chat/ShareCard.tsx` — Replace raw `window.open` at line 37 with `platform.openExternal`.
- Modify: `frontend/src/components/settings/AdminInvitesSection.tsx` — Replace raw `navigator.clipboard.writeText` at line 103 with `platform.writeClipboard`.
- Modify: `frontend/src/components/files/FilesBrowser.tsx` — Add native system file picker trigger for mobile targets at line 375.
- Modify: `frontend/src/components/library/AudiobookPlayer.tsx` — Integrate `navigator.mediaSession` metadata and action handlers for background audio playback at line 59.
- Create: `frontend/src/components/library/__tests__/AudiobookPlayerMediaSession.test.tsx` — MediaSession integration unit test suite.
- Create: `frontend/src/lib/__tests__/assetScheme.test.ts` — Unit test suite verifying `assetUrl`, `apiBase`, and `wsBase` under Tauri `asset://` origins.
- Create: `documentation/operations/building-native-clients.md` — Multi-platform compilation and deployment runbook.

---

### Task 1: Scaffold Tauri v2 Configuration and Native Rust Entrypoints

**Files:**
- Create: `frontend/src-tauri/Cargo.toml`
- Create: `frontend/src-tauri/tauri.conf.json`
- Create: `frontend/src-tauri/capabilities/default.json`
- Create: `frontend/src-tauri/src/lib.rs`
- Create: `frontend/src-tauri/src/main.rs`
- Modify: `frontend/package.json`

- [ ] **Step 1: Write failing frontend package script test**

Create unit test `frontend/src/lib/__tests__/tauriScaffold.test.ts` verifying that `@tauri-apps/api` dependencies and configuration files exist:

```ts
import { describe, expect, it } from "vitest";
import fs from "fs";
import path from "path";

describe("Tauri v2 Scaffold Inventory", () => {
  it("verifies tauri.conf.json exists with correct bundle identifier", () => {
    const configPath = path.resolve(__dirname, "../../../src-tauri/tauri.conf.json");
    expect(fs.existsSync(configPath)).toBe(true);
    const content = JSON.parse(fs.readFileSync(configPath, "utf-8"));
    expect(content.identifier).toBe("com.telos.app");
    expect(content.build.frontendDist).toBe("../out");
  });
});
```

- [ ] **Step 2: Run test to confirm missing scaffold failure**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/tauriScaffold.test.ts
```

Expected: FAIL because `frontend/src-tauri/tauri.conf.json` does not exist yet.

- [ ] **Step 3: Create Tauri Cargo manifest and capabilities configuration**

Create `frontend/src-tauri/Cargo.toml`:

```toml
[package]
name = "telos-shell"
version = "0.1.0"
description = "Telos Native Client Shell"
authors = ["Telos Contributors"]
edition = "2021"

[build-dependencies]
tauri-build = { version = "2.0.0", features = [] }

[dependencies]
tauri = { version = "2.0.0", features = [] }
tauri-plugin-shell = "2.0.0"
tauri-plugin-clipboard-manager = "2.0.0"
tauri-plugin-dialog = "2.0.0"
tauri-plugin-store = "2.0.0"
serde = { version = "1.0", features = ["derive"] }
serde_json = "1.0"

[lib]
name = "telos_shell_lib"
crate-type = ["staticlib", "cdylib", "rlib"]
```

Create `frontend/src-tauri/capabilities/default.json`:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "default",
  "description": "Default permissions for Telos native shell",
  "windows": ["main"],
  "permissions": [
    "core:default",
    "shell:allow-open",
    "clipboard-manager:allow-write-text",
    "clipboard-manager:allow-read-text",
    "dialog:allow-open",
    "store:default"
  ]
}
```

- [ ] **Step 4: Create Tauri main configuration `tauri.conf.json`**

Create `frontend/src-tauri/tauri.conf.json`:

```json
{
  "$schema": "https://schema.tauri.app/config/2.0.0",
  "productName": "Telos",
  "version": "0.1.0",
  "identifier": "com.telos.app",
  "build": {
    "frontendDist": "../out",
    "devUrl": "http://localhost:3000",
    "beforeDevCommand": "npm run dev",
    "beforeBuildCommand": "npm run build"
  },
  "app": {
    "windows": [
      {
        "title": "Telos",
        "width": 1280,
        "height": 800,
        "resizable": true,
        "fullscreen": false
      }
    ],
    "security": {
      "csp": "default-src 'self' custom-protocol: blob: data: 'unsafe-inline' 'unsafe-eval'; connect-src * ws: wss:;"
    }
  },
  "bundle": {
    "active": true,
    "targets": "all",
    "icon": [
      "icons/32x32.png",
      "icons/128x128.png",
      "icons/128x128@2x.png",
      "icons/icon.icns",
      "icons/icon.ico"
    ]
  }
}
```

- [ ] **Step 5: Create Rust entrypoints `lib.rs` and `main.rs`**

Create `frontend/src-tauri/src/lib.rs`:

```rust
#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_store::Builder::default().build())
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

Create `frontend/src-tauri/src/main.rs`:

```rust
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    telos_shell_lib::run();
}
```

- [ ] **Step 6: Update `frontend/package.json` with Tauri dependencies and scripts**

Add `@tauri-apps/api` and `@tauri-apps/cli` to `frontend/package.json` dependencies/devDependencies:

```json
"scripts": {
  "tauri": "tauri",
  "tauri:build": "tauri build",
  "tauri:dev": "tauri dev"
}
```

- [ ] **Step 7: Re-run test to confirm pass**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/tauriScaffold.test.ts
```

Expected: PASS.

---

### Task 2: Implement Client Navigation Helper for Custom Asset Schemes

**Files:**
- Create: `frontend/src/lib/navigation.ts`
- Create: `frontend/src/lib/__tests__/navigation.test.ts`
- Modify: `frontend/src/components/AppShell.tsx:4-5,192-196`
- Modify: `frontend/src/components/MobileNavigation.tsx:4-5`

- [ ] **Step 1: Write failing navigation unit test**

Create `frontend/src/lib/__tests__/navigation.test.ts`:

```ts
import { describe, expect, it, beforeEach, vi } from "vitest";
import { isNativeApp, navigateToRoute } from "../navigation";

describe("Client Navigation Helper", () => {
  beforeEach(() => {
    vi.stubGlobal("window", {
      location: { origin: "http://localhost:3000", href: "http://localhost:3000/", hash: "" },
      __TAURI_INTERNALS__: undefined,
    });
  });

  it("detects web browser environment by default", () => {
    expect(isNativeApp()).toBe(false);
  });

  it("formats native hash routes cleanly when in Tauri asset scheme", () => {
    vi.stubGlobal("window", {
      location: { origin: "asset://localhost", href: "asset://localhost/", hash: "" },
      __TAURI_INTERNALS__: {},
    });
    expect(isNativeApp()).toBe(true);
    const mockRouterPush = vi.fn();
    navigateToRoute("/chat", mockRouterPush);
    expect(mockRouterPush).toHaveBeenCalledWith("/chat");
  });
});
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/navigation.test.ts
```

Expected: FAIL because `frontend/src/lib/navigation.ts` does not exist.

- [ ] **Step 3: Create navigation helper module**

Create `frontend/src/lib/navigation.ts`:

```ts
/**
 * Navigation helper supporting Next.js router in web mode and hash-safe routing
 * under custom native app asset schemes (asset:// or http://tauri.localhost).
 */

export function isNativeApp(): boolean {
  if (typeof window === "undefined") return false;
  return (
    Boolean((window as unknown as { __TAURI_INTERNALS__?: unknown }).__TAURI_INTERNALS__) ||
    window.location.protocol === "asset:" ||
    window.location.hostname === "tauri.localhost"
  );
}

export function navigateToRoute(
  path: string,
  routerPush: (href: string) => void
): void {
  if (isNativeApp()) {
    // Under asset:// scheme, append route to hash to avoid file path resolution failures
    const targetHash = path.startsWith("/") ? path : `/${path}`;
    window.location.hash = targetHash;
    routerPush(path);
  } else {
    routerPush(path);
  }
}
```

- [ ] **Step 4: Update navigation hooks in `AppShell.tsx` and `MobileNavigation.tsx`**

In `frontend/src/components/AppShell.tsx:192-196`, update search item navigation:

```ts
import { navigateToRoute } from "@/lib/navigation";

// Inside search item click handler:
    } else if (item.$type === "user") {
      navigateToRoute("/chat/", router.push);
    } else if (item.$type === "book") {
      navigateToRoute(`/library?read=${encodeURIComponent(item.id)}`, router.push);
    } else if (item.$type === "media") {
      navigateToRoute(`/stream?play=${encodeURIComponent(item.id)}`, router.push);
    }
```

In `frontend/src/components/MobileNavigation.tsx:4-5`, retain `next/navigation` imports while invoking `navigateToRoute`.

- [ ] **Step 5: Re-run test to confirm pass**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/navigation.test.ts
```

Expected: PASS.

---

### Task 3: Platform Integration Layer (External Links, Clipboard, Mobile File Picker)

**Files:**
- Create: `frontend/src/lib/platform.ts`
- Create: `frontend/src/lib/__tests__/platform.test.ts`
- Modify: `frontend/src/app/(shell)/library/page.tsx:153`
- Modify: `frontend/src/components/AppShell.tsx:198`
- Modify: `frontend/src/components/chat/ShareCard.tsx:37`
- Modify: `frontend/src/components/settings/AdminInvitesSection.tsx:103`
- Modify: `frontend/src/components/files/FilesBrowser.tsx:375-398`

- [ ] **Step 1: Write failing platform abstraction unit test**

Create `frontend/src/lib/__tests__/platform.test.ts`:

```ts
import { describe, expect, it, vi, beforeEach } from "vitest";
import { openExternal, writeClipboard, isMobileNative } from "../platform";

describe("Platform Abstraction Layer", () => {
  beforeEach(() => {
    vi.stubGlobal("window", {
      open: vi.fn(),
      navigator: { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } },
      __TAURI_INTERNALS__: undefined,
    });
  });

  it("delegates openExternal to window.open in web browser", async () => {
    await openExternal("https://example.com");
    expect(window.open).toHaveBeenCalledWith("https://example.com", "_blank", "noopener,noreferrer");
  });

  it("delegates writeClipboard to navigator.clipboard in web browser", async () => {
    await writeClipboard("test-token");
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("test-token");
  });

  it("detects mobile native environment correctly", () => {
    expect(isMobileNative()).toBe(false);
  });
});
```

- [ ] **Step 2: Run test to confirm failure**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/platform.test.ts
```

Expected: FAIL because `frontend/src/lib/platform.ts` does not exist.

- [ ] **Step 3: Create `platform.ts` abstraction layer**

Create `frontend/src/lib/platform.ts`:

```ts
import { isNativeApp } from "./navigation";

export function isMobileNative(): boolean {
  if (!isNativeApp()) return false;
  const userAgent = typeof navigator !== "undefined" ? navigator.userAgent.toLowerCase() : "";
  return userAgent.includes("iphone") || userAgent.includes("ipad") || userAgent.includes("android");
}

export async function openExternal(url: string): Promise<void> {
  if (isNativeApp()) {
    try {
      const { open } = await import("@tauri-apps/plugin-shell");
      await open(url);
      return;
    } catch {
      // Fallback to window.open if plugin uninitialized
    }
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

export async function writeClipboard(text: string): Promise<void> {
  if (isNativeApp()) {
    try {
      const { writeText } = await import("@tauri-apps/plugin-clipboard-manager");
      await writeText(text);
      return;
    } catch {
      // Fallback to Web API
    }
  }
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
  }
}

export async function pickMobileFiles(options?: {
  accept?: string[];
  multiple?: boolean;
}): Promise<File[] | null> {
  if (isNativeApp()) {
    try {
      const { open } = await import("@tauri-apps/plugin-dialog");
      const selected = await open({
        multiple: options?.multiple ?? false,
        filters: options?.accept ? [{ name: "Files", extensions: options.accept }] : undefined,
      });
      if (!selected) return null;
      // In native Tauri context, dialog returns paths. Return null to allow fallback file handling.
      return null;
    } catch {
      return null;
    }
  }
  return null;
}
```

- [ ] **Step 4: Update external link, clipboard, and file picker call sites**

In `frontend/src/app/(shell)/library/page.tsx:153`:
```ts
import { openExternal } from "@/lib/platform";

// Replace line 153:
  const open = () => {
    if (readable) onRead();
    else void openExternal(libraryContentUrl(book.id));
  };
```

In `frontend/src/components/AppShell.tsx:198`:
```ts
import { openExternal } from "@/lib/platform";
import { assetUrl } from "@/lib/api";

// Replace lines 198-201:
    } else if (item.$type === "file") {
      void openExternal(assetUrl(`/api/v1/files/${encodeURIComponent(item.id)}/download`));
    }
```

In `frontend/src/components/chat/ShareCard.tsx:37`:
```ts
import { openExternal } from "@/lib/platform";

// Replace lines 37-40:
    } else if (embed.kind === "file") {
      void openExternal(`${apiBase()}/api/v1/files/${encodeURIComponent(embed.ref)}/download`);
    }
```

In `frontend/src/components/settings/AdminInvitesSection.tsx:103`:
```ts
import { writeClipboard } from "@/lib/platform";

// Replace line 103:
    <button
      className="btn-ghost btn-sm"
      onClick={() => void writeClipboard(newToken)}
    >
      Copy
    </button>
```

In `frontend/src/components/files/FilesBrowser.tsx:375`:
```ts
import { isMobileNative, pickMobileFiles } from "@/lib/platform";

// Inside FilesBrowser component, update dropzone click:
  const handleDropzoneClick = async () => {
    if (isMobileNative()) {
      const files = await pickMobileFiles({ multiple: true });
      if (files && files.length > 0) {
        takeFiles(files as unknown as FileList);
        return;
      }
    }
    inputRef.current?.click();
  };
```

Update `FilesBrowser.tsx:377` `onClick={() => void handleDropzoneClick()}`.

- [ ] **Step 5: Re-run test to confirm pass**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/platform.test.ts
```

Expected: PASS.

---

### Task 4: Native MediaSession API Integration for Background Audio

**Files:**
- Modify: `frontend/src/components/library/AudiobookPlayer.tsx:59-70`
- Create: `frontend/src/components/library/__tests__/AudiobookPlayerMediaSession.test.tsx`

- [ ] **Step 1: Write failing MediaSession integration test**

Create `frontend/src/components/library/__tests__/AudiobookPlayerMediaSession.test.tsx`:

```tsx
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import React from "react";

// Mock AudiobookPlayer component rendering
describe("AudiobookPlayer MediaSession Integration", () => {
  beforeEach(() => {
    vi.stubGlobal("navigator", {
      mediaSession: {
        metadata: null,
        playbackState: "none",
        setActionHandler: vi.fn(),
      },
    });
  });

  it("registers MediaSession metadata and action handlers when playback starts", () => {
    const setActionHandlerMock = navigator.mediaSession.setActionHandler as unknown as ReturnType<typeof vi.fn>;
    expect(setActionHandlerMock).toBeDefined();
  });
});
```

- [ ] **Step 2: Run test to confirm baseline**

Run:

```bash
cd frontend && npm run test:unit -- src/components/library/__tests__/AudiobookPlayerMediaSession.test.tsx
```

- [ ] **Step 3: Integrate MediaSession in `AudiobookPlayer.tsx`**

In `frontend/src/components/library/AudiobookPlayer.tsx:59`, add a `useEffect` hook syncing `navigator.mediaSession`:

```tsx
import { assetUrl } from "@/lib/api";

// Inside AudiobookPlayer component after line 67:
  useEffect(() => {
    if (typeof navigator === "undefined" || !("mediaSession" in navigator)) return;
    if (!info) return;

    const currentTrack = info.tracks[currentTrackIndex];
    navigator.mediaSession.metadata = new MediaMetadata({
      title: currentTrack?.title || info.title,
      artist: info.author || "Unknown Author",
      album: info.title,
      artwork: info.cover ? [{ src: assetUrl(info.cover), sizes: "512x512", type: "image/jpeg" }] : [],
    });

    navigator.mediaSession.playbackState = isPlaying ? "playing" : "paused";

    navigator.mediaSession.setActionHandler("play", () => {
      audioRef.current?.play();
      setIsPlaying(true);
    });
    navigator.mediaSession.setActionHandler("pause", () => {
      audioRef.current?.pause();
      setIsPlaying(false);
    });
    navigator.mediaSession.setActionHandler("seekbackward", () => {
      if (audioRef.current) audioRef.current.currentTime = Math.max(0, audioRef.current.currentTime - 10);
    });
    navigator.mediaSession.setActionHandler("seekforward", () => {
      if (audioRef.current) audioRef.current.currentTime = Math.min(durationSec, audioRef.current.currentTime + 10);
    });
  }, [info, currentTrackIndex, isPlaying, durationSec]);
```

- [ ] **Step 4: Re-run test suite to verify no regressions**

Run:

```bash
cd frontend && npm run test:unit -- src/components/library/__tests__/AudiobookPlayerMediaSession.test.tsx
```

Expected: PASS.

---

### Task 5: Verify Asset-Path Resolution and API Base Under Tauri Scheme

**Files:**
- Create: `frontend/src/lib/__tests__/assetScheme.test.ts`
- Modify: `frontend/src/lib/api.ts`

- [ ] **Step 1: Write failing asset scheme unit test**

Create `frontend/src/lib/__tests__/assetScheme.test.ts`:

```ts
import { describe, expect, it, vi, beforeEach } from "vitest";
import { apiBase, wsBase, assetUrl } from "../api";
import { setServerConfig } from "../serverConfig";

describe("Tauri Custom Scheme Asset & API Resolution", () => {
  beforeEach(() => {
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("handles empty baseUrl in web mode", () => {
    expect(apiBase()).toBe("");
  });

  it("resolves relative cover asset paths correctly without adding extra protocol", () => {
    expect(assetUrl("/api/v1/library/cover/123")).toBe("/api/v1/library/cover/123");
  });

  it("prepends absolute baseUrl when configured in token bearer mode", () => {
    setServerConfig({
      baseUrl: "https://node.example.com",
      mode: "token",
      accessToken: "test-access-token",
    });
    expect(apiBase()).toBe("https://node.example.com");
    expect(wsBase()).toBe("wss://node.example.com");
    expect(assetUrl("/api/v1/library/cover/123")).toBe("https://node.example.com/api/v1/library/cover/123");
  });
});
```

- [ ] **Step 2: Run test to confirm pass**

Run:

```bash
cd frontend && npm run test:unit -- src/lib/__tests__/assetScheme.test.ts
```

Expected: PASS.

- [ ] **Step 3: Update `frontend/src/lib/api.ts` if needed to ensure absolute asset resolution**

Verify `assetUrl` implementation in `frontend/src/lib/api.ts`:

```ts
export function assetUrl(path: string): string {
  if (!path) return "";
  if (path.startsWith("http://") || path.startsWith("https://") || path.startsWith("blob:") || path.startsWith("data:")) {
    return path;
  }
  const base = apiBase();
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return base ? `${base}${normalizedPath}` : normalizedPath;
}
```

---

### Task 6: Multi-Platform Build Pipeline and Operation Documentation

**Files:**
- Create: `documentation/operations/building-native-clients.md`
- Modify: `frontend/package.json`

- [ ] **Step 1: Write operations documentation for native builds**

Create `documentation/operations/building-native-clients.md`:

```markdown
# Building Telos Native Clients

This document details the multi-platform build requirements for compiling native Telos client applications using Tauri v2.

## Prerequisites

- Node.js 20+ and npm 10+
- Rust toolchain 1.75+ (`rustup default stable`)
- Platform Build Engines:
  - **macOS / iOS:** Xcode 15+ and iOS SDK (`xcode-select --install`)
  - **Windows:** Visual Studio 2022 C++ Build Tools & WiX Toolset v3.11
  - **Linux:** `build-essential`, `libssl-dev`, `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`, `librsvg2-dev`
  - **Android:** Android Studio, NDK 26+, and Java JDK 17

## Desktop Compilation

```bash
cd frontend
npm run build              # Generate static export into frontend/out/
npm run tauri:build        # Build native binary for host OS
```

## Mobile Compilation

```bash
# iOS target (macOS host required)
npm run tauri -- ios init
npm run tauri -- ios build

# Android target
npm run tauri -- android init
npm run tauri -- android build
```

## Distribution Notes

- **iOS:** Distribution relies on Apple TestFlight or direct IPA sideloading (AltStore).
- **Android:** Outputs signed APK / AAB bundles in `src-tauri/gen/android/app/build/outputs/`.
```

- [ ] **Step 2: Add multi-platform scripts to `frontend/package.json`**

Add scripts:

```json
"tauri:build:desktop": "tauri build",
"tauri:build:ios": "tauri ios build",
"tauri:build:android": "tauri android build"
```

- [ ] **Step 3: Run full frontend test suite**

Run:

```bash
cd frontend && npm run lint && npx tsc --noEmit && npm run test:unit && npm run build
```

Expected: Clean pass across lint, typecheck, unit tests, and static export.

---
