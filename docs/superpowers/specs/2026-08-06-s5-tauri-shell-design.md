# Tauri Shell — Design

The Tauri Shell sub-project packages the Telos Next.js frontend as native desktop and mobile applications targeting Windows, macOS, Linux, iOS, and Android using Tauri v2. By wrapping the existing static export without refactoring application components, Telos delivers lightweight native binaries (~5 MB footprint vs Electron's ~120 MB) while preserving 100% of the UI design system. This spec defines platform integration shims (external links, clipboard, mobile file pickers, background audio media sessions), routing adjustments, static asset handling, and explicit fallbacks if the platform de-risking spike (S0) fails.

## 1. Purpose

### 1.1 Decisions

| Decision | Specification |
|---|---|
| Shell Framework | Tauri v2, one shell, five platforms. Rationale is size and the single-codebase requirement; the cost is that it uses each OS's native webview, which is the entire S0 risk. |
| S0 Fallback Contingency | This spec must state the S0 fallback branch explicitly: if the S0 spike fails on epub.js, the shell decision reverts to Electron (desktop) plus Capacitor (mobile) and this spec is superseded. Do not write it as though S0 has already passed. |
| Client Routing | Routing converts to a hash or memory router. The app has static routes and zero dynamic routes, so this is mechanical. |
| Platform Shims | Platform shims required: external-link opening, clipboard, native file picker on mobile in place of drag-and-drop, and a native media session for background audio and lock-screen controls. |
| Asset Serving | The static export uses absolute `/_next/...` asset paths and cannot be served under a path prefix. This must be verified against Tauri's asset scheme, not assumed. |
| Distribution Fallbacks | iOS App Store approval is not assumed. Apps requiring a user-supplied server draw review scrutiny. TestFlight and sideloading are the declared fallbacks. |

### 1.2 Success criteria

1. Native client binaries build and execute cleanly on macOS, Windows, Linux, iOS, and Android from the single Next.js frontend codebase.
2. If S0 de-risking passes, native WebViews render EPUB books (`EpubReader.tsx`), PDF documents (`PdfReader.tsx`), and HLS streams (`HlsPlayer.tsx`) without cross-frame or asset scheme errors.
3. External web links open in the host OS browser rather than inside the app webview.
4. Mobile uploads utilize native system file pickers in place of desktop drag-and-drop zones.
5. Background audio playback on mobile devices responds to OS lock-screen and media center controls via native MediaSession integration.

## 2. Verified current state

- **External Link (`window.open`) Sites:** `window.open` is called raw at 3 sites:
  1. `frontend/src/app/(shell)/library/page.tsx:153` (`window.open(libraryContentUrl(book.id), "_blank", "noopener")`).
  2. `frontend/src/components/AppShell.tsx:198` (`window.open(documentationUrl, "_blank")`).
  3. `frontend/src/components/chat/ShareCard.tsx:37` (`window.open(embedUrl, "_blank")`).
- **Clipboard Access Site:** Single raw clipboard write at `frontend/src/components/settings/AdminInvitesSection.tsx:103` (`navigator.clipboard.writeText(newToken)`).
- **Drag-and-Drop Upload Region:** `frontend/src/components/files/FilesBrowser.tsx:375-397` defines a dropzone `<div>` with `onDragOver`, `onDragLeave`, and `onDrop` handling `e.dataTransfer.files` directly, wrapping a hidden `<input type="file">` at line 398.
- **Background Audio Sites:** `<audio>` element ref created at `frontend/src/components/library/AudiobookPlayer.tsx:59` (`const audioRef = useRef<HTMLAudioElement>(null)`). Audio stream URLs constructed at `frontend/src/app/(shell)/stream/page.tsx:43-46`. Neither site integrates with `navigator.mediaSession` for native lock-screen or background controls.
- **Static Route Inventory:** 7 static routes, zero dynamic routes:
  1. `/` (`frontend/src/app/page.tsx`)
  2. `/login` (`frontend/src/app/login/page.tsx`)
  3. `/chat` (`frontend/src/app/(shell)/chat/page.tsx`)
  4. `/files` (`frontend/src/app/(shell)/files/page.tsx`)
  5. `/library` (`frontend/src/app/(shell)/library/page.tsx`)
  6. `/settings` (`frontend/src/app/(shell)/settings/page.tsx`)
  7. `/stream` (`frontend/src/app/(shell)/stream/page.tsx`)
- **Next.js Framework Imports:** Exactly 9 `next/*` import sites in `frontend/src/`, exclusively consuming `Link`, `usePathname`, and `useRouter`: `frontend/src/app/(shell)/library/page.tsx:4`, `frontend/src/app/login/page.tsx:4`, `frontend/src/app/page.tsx:3`, `frontend/src/components/AppShell.tsx:4,5`, `frontend/src/components/MobileNavigation.tsx:4,5`, `frontend/src/components/chat/ShareCard.tsx:6`, `frontend/src/components/rail/ModuleRail.tsx:3`. Zero usage of `next/image`, `next/head`, or server components.
- **Asset Path Constraint:** Documented in `frontend/AGENTS.md:42-43`: *"Note the static export uses absolute `/_next/...` asset paths, so it cannot be served under a path prefix."*

## 3. Architecture

### 3.1 Boundaries

- Tauri configuration (`src-tauri/`) houses native Rust wrapper code, capability manifests, and platform build target definitions.
- The web application codebase (`frontend/src/`) remains unified across web and native targets via abstraction helpers.
- Backend gateway code (`backend/`) is unaffected by native frontend shell packaging.

### 3.2 Platform Shims & Native Integration Surface

Native platform integrations are unified behind a platform abstraction layer (`frontend/src/lib/platform.ts`):

```
                       ┌─────────────────────────┐
                       │ React UI Components     │
                       └────────────┬────────────┘
                                    │
                       ┌────────────▼────────────┐
                       │ platform.ts Interface   │
                       └─────┬──────────────┬────┘
                             │              │
        ┌────────────────────▼┐            ┌▼───────────────────┐
        │ Native Tauri Shim   │            │ Web Browser Shim   │
        │ (@tauri-apps/api)   │            │ (Standard Web APIs)│
        └─────────────────────┘            └────────────────────┘
```

1. **External Links:** `openExternal(url: string)` routes to `@tauri-apps/plugin-shell` in native mode, or `window.open(url, '_blank')` in web mode.
2. **Clipboard:** `writeClipboard(text: string)` uses `@tauri-apps/plugin-clipboard-manager` when native, falling back to `navigator.clipboard`.
3. **Mobile File Selection:** On iOS/Android, `FilesBrowser.tsx:375-397` dropzone triggers `@tauri-apps/plugin-dialog` to invoke the OS native file picker instead of desktop drag-and-drop.
4. **Media Session:** `AudiobookPlayer.tsx:59` registers metadata (title, author, cover) and action handlers (`play`, `pause`, `seekbackward`, `seekforward`) with `navigator.mediaSession` to sync lock-screen/notification controls on mobile OS devices.

### 3.3 Routing Adaptation & Static Asset Scheme

- **Router Mode:** Next.js static export produces HTML pages for the 7 static routes. In native Tauri environments, router navigation is configured to use hash-based routing (`HashRouter`) or memory routing to prevent file protocol asset lookup failures (`file://` or `tauri://localhost`).
- **Asset Scheme:** `frontend/AGENTS.md:42-43` mandates that asset paths begin at `/_next/...`. Tauri v2 custom protocols (`asset://` or `http://tauri.localhost`) map root-relative asset requests directly to the embedded `out/` static directory root, preserving Next.js chunk references without path prefix rewriting.

### 3.4 S0 Fallback Contingency & iOS Distribution Strategy

- **S0 Spike Fallback:** If the S0 platform de-risking spike determines that iOS WKWebView cannot reliably support `epub.js` cross-frame selection (`EpubReader.tsx`) or `pdf.js` worker loading (`PdfReader.tsx`), **this Tauri v2 plan is immediately superseded**. The native desktop target will revert to Electron (bundling Chromium) and mobile targets will revert to Capacitor.
- **iOS App Store Strategy:** Apple App Store review policies often scrutinize applications requiring self-hosted server URLs. Primary distribution for iOS will utilize Apple TestFlight and direct IPA sideloading (AltStore/TrollStore), treating App Store listing as a secondary opt-in path.

## 4. Contract changes

- **New Native Packaging Directory:** `frontend/src-tauri/` added containing `tauri.conf.json`, Rust entry points (`main.rs`, `lib.rs`), and platform icons.
- **Dependencies Added:** `@tauri-apps/api`, `@tauri-apps/plugin-shell`, `@tauri-apps/plugin-clipboard-manager`, `@tauri-apps/plugin-dialog`, `@tauri-apps/plugin-store`.

## 5. Implementation surface

| Path | Change | Rationale |
|---|---|---|
| `frontend/src-tauri/tauri.conf.json` | Create Tauri v2 configuration | Configures bundle IDs, window parameters, custom asset schemes, and permissions. |
| `frontend/src/lib/platform.ts` | Create platform abstraction layer | Wraps external link opening, clipboard, and file pickers for web vs native environments. |
| `frontend/src/app/(shell)/library/page.tsx` | Update line 153 to use `platform.openExternal` | Prevents `window.open` from launching inside webview. |
| `frontend/src/components/AppShell.tsx` | Update line 198 to use `platform.openExternal` | Routes documentation link to host browser. |
| `frontend/src/components/chat/ShareCard.tsx` | Update line 37 to use `platform.openExternal` | Routes embedded share links to host browser. |
| `frontend/src/components/settings/AdminInvitesSection.tsx` | Update line 103 to use `platform.writeClipboard` | Uses native clipboard plugin in Tauri shell. |
| `frontend/src/components/files/FilesBrowser.tsx` | Add native file picker fallback at line 375 | Enables native mobile file selection on Android/iOS. |
| `frontend/src/components/library/AudiobookPlayer.tsx` | Integrate MediaSession API at line 59 | Connects background audio playback to OS lock-screen media controls. |

## 6. Failure handling and observability

- **Webview Crash / Reload:** Unhandled JS exceptions in native webview trigger error boundary logging to native console logs.
- **Asset Load Failure:** Custom URI scheme (`asset://`) logs missing file lookups to Tauri stdout for diagnostic debugging.
- **Permission Denial:** Denial of native file picker or clipboard access presents non-blocking inline warning toasts in the UI.

## 7. Security and privacy

- Tauri capability permissions are locked down strictly in `src-tauri/capabilities/default.json`.
- Remote domain navigation is blocked within the main webview; all external links strictly delegate to `plugin-shell:open` after scheme validation (`http:` / `https:` only).
- Local filesystem access is restricted strictly to user-selected files via dialog pickers.

## 8. Verification

Verification requires running frontend test and build suites per `AGENTS.md` section 5:

```bash
# Frontend build and test suite
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build
```

Tauri native build verification (when Rust toolchain is present):

```bash
# Desktop native build test
cd frontend
npx tauri build --no-bundle
```

## 9. Documentation changes required with implementation

- Update `CLAUDE.md` and `AGENTS.md` to document Tauri native app build commands (`npm run tauri:build`).
- Create `documentation/operations/building-native-clients.md` detailing multi-platform compilation requirements (Xcode for iOS/macOS, Android Studio/NDK for Android, WiX for Windows MSI).

## 10. Deferred / out of scope

- Custom native C++ audio engine integration (uses native browser `<audio>` / Web Audio API).
- Desktop system tray minimization / background daemon execution.
