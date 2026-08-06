# Platform De-risking — Design

**Date:** 2026-08-06
**Status:** Approved design
**Sub-project ID:** S0
**Parent program:** `docs/superpowers/specs/2026-08-06-backend-client-split-program.md`

This sub-project de-risks Telos's three fragile media components (`EpubReader.tsx`, `PdfReader.tsx`, `HlsPlayer.tsx`) against iOS WebKit execution models before any budget or implementation effort is committed to the Tauri shell (`docs/superpowers/specs/2026-08-06-backend-client-split-program.md:57`). It also establishes cross-platform Windows backend compilation signaling in Continuous Integration. Completing S0 proves webview viability and provides early decision-forcing data for client architecture choices.

## 1. Purpose

### 1.1 Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Spike execution structure | Two staged gates (Stage A $\rightarrow$ Stage B) | Stage A tests mobile Safari on physical iOS to cheaply validate WKWebView iframe and MSE rules before building native packaging binaries. |
| Custom scheme coverage | Stage B only | Stage A does not cover Tauri's custom `asset:` scheme. Stage B isolates `pdf.js` web worker loading under `asset:`. |
| Target components | `EpubReader.tsx`, `PdfReader.tsx`, `HlsPlayer.tsx` | Identifies the exact fragile dependencies: `epubjs` 0.4.2, `pdfjs-dist` 6.1.200, and `hls.js` 1.6.16. |
| Spike code lifecycle | Throwaway only | Spike code must not be merged to `main`. Only diagnostic findings and decision logs persist. |
| Backend compilation matrix | Add `GOOS` matrix to CI | `.github/workflows/ci.yml` currently tests Linux only. Adding Windows validates backend portability early. |
| Exit criteria branching | Decision-forcing pre-declared fallbacks | If `epubjs` fails, evaluate alternate readers or revert shell architecture to Electron + Capacitor; if `pdf.js` worker fails under `asset:`, bundle same-origin; if `hls.js` MSE fails, fallback to native `<video>` HLS. |

### 1.2 Success criteria

1. Stage A validates that `EpubReader.tsx` nested-iframe renditions render and cross-frame selection text extraction operates on iOS mobile Safari without security origin exceptions.
2. Stage A validates that `HlsPlayer.tsx` MSE media playback functions or degrades cleanly to native WebKit HLS handling on iOS mobile Safari.
3. Stage B validates that `PdfReader.tsx` web worker initializes successfully under Tauri's `asset:` scheme on a physical iOS test device.
4. Continuous Integration workflow `.github/workflows/ci.yml` executes Go unit and vet steps for `GOOS=windows` alongside Linux on every pull request and push to `main`.
5. Clear resolution is documented for all three fragile media components with either pass confirmation or activation of pre-declared fallbacks prior to starting `docs/superpowers/specs/2026-08-06-backend-client-split-program.md:57`.

## 2. Verified current state

| Symbol / Feature | Source File & Line Range | Verified Property |
|---|---|---|
| Nested iframe rendition | `frontend/src/components/library/EpubReader.tsx:95-100` | `epub.renderTo` instantiates iframe manager (`view: registry.Views?.iframe`). |
| Cross-frame selection | `frontend/src/components/library/EpubReader.tsx:127-129` | Rendition selection listener invokes `contents.window.getSelection?.()`. |
| Global `ePub` workaround | `frontend/src/components/library/EpubReader.tsx:90` | Explicit global assignment `(window as unknown as { ePub?: unknown }).ePub = ePub` required for 0.4.2 runtime. |
| PDF.js worker constructor | `frontend/src/components/library/PdfReader.tsx:91-94` | `pdfjs.GlobalWorkerOptions.workerSrc` assigned via `new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url).toString()`. |
| PDF.js credentialed request | `frontend/src/components/library/PdfReader.tsx:102-103` | `pdfjs.getDocument` called with `{ url: libraryContentUrl(book.id), withCredentials: true }`. |
| Native HLS check | `frontend/src/components/stream/HlsPlayer.tsx:44` | Bypasses `hls.js` if `video.canPlayType("application/vnd.apple.mpegurl")` returns true. |
| `hls.js` MSE support test | `frontend/src/components/stream/HlsPlayer.tsx:56` | Instantiates player only when `HlsCtor.isSupported()` returns true. |
| `hls.js` XHR credential setup | `frontend/src/components/stream/HlsPlayer.tsx:62-64` | Configures `xhrSetup` to set `xhr.withCredentials = true`. |
| HLS 302 redirect mechanism | `frontend/src/components/stream/HlsPlayer.tsx:10-13` | Gateway resolves `PlaybackInfo` and issues HTTP 302 to `main.m3u8` sub-path. |
| Dormant CORS attribute | `frontend/src/components/stream/HlsPlayer.tsx:93` | Sets `crossOrigin={apiBase() ? "use-credentials" : undefined}` on `<video>` element. |
| Media package versions | `frontend/package.json:16` | Standardized dependencies: `epubjs` 0.4.2, `hls.js` 1.6.16, `pdfjs-dist` 6.1.200. |
| CI Backend job definition | `.github/workflows/ci.yml:13-24` | Single OS runner (`ubuntu-latest`) executing `go test ./...` with no cross-compile matrix. |

## 3. Architecture

### 3.1 Boundaries

Sub-project S0 is strictly diagnostic and infrastructure-signaling. It may modify `.github/workflows/ci.yml` to establish cross-compilation matrix checks. All media component testing code must remain isolated in temporary spike branches. S0 may not alter production application code in `frontend/src/` or `backend/` except as required for CI configuration.

### 3.2 Component Spike Architecture

The de-risking spike evaluates three high-risk components across two testing stages:

```
+-----------------------------------------------------------------------------------+
|                                STAGE A (Zero-Setup)                               |
|   Target: Mobile Safari on physical iOS Device (iOS 17+)                          |
|   - Serve standard Next.js static export over local HTTPS dev server              |
|   - Test EpubReader nested iframe & window.getSelection()                         |
|   - Test HlsPlayer MSE vs Native <video> HLS fallback                             |
+-----------------------------------------+-----------------------------------------+
                                          |
                                    [Stage A Pass]
                                          |
                                          v
+-----------------------------------------------------------------------------------+
|                                STAGE B (Native Shell)                             |
|   Target: Tauri v2 iOS harness on physical device via Xcode                       |
|   - Bundle static export under Tauri custom asset: scheme                         |
|   - Test PdfReader web worker initialization: new URL(..., import.meta.url)       |
|   - Validate cross-scheme fetch and streaming range requests                      |
+-----------------------------------------------------------------------------------+
```

### 3.3 Decision-Forcing Fallback Matrix

| Component | Failure Mode | Pre-declared Fallback Branch | Architectural Impact |
|---|---|---|---|
| `EpubReader.tsx` | WKWebView blocks cross-frame `getSelection()` or nested iframe rendering | Revert shell architecture to Electron (desktop) + Capacitor (mobile), or replace `epubjs` with non-iframe reader. | Super-project architecture reverts from unified Tauri shell to dual Electron/Capacitor model. |
| `PdfReader.tsx` | Web worker fails to instantiate under custom `asset:` scheme | Bundle `pdf.worker.min.mjs` as explicit same-origin static asset served without dynamic worker workerSrc construction. | Component rewrite in `PdfReader.tsx`; no framework change. |
| `HlsPlayer.tsx` | `hls.js` MSE fails on WKWebView | Degrade to native HLS playback via `<video src="...">` relying on native WebKit manifest parsing. | Simplified player logic on iOS; `hls.js` retained for desktop/Android. |

## 4. Contract changes

### 4.1 CI Workflow Modifications

The Continuous Integration workflow `.github/workflows/ci.yml:13-24` adds a `matrix` strategy to `backend-gates` defining `GOOS`:

```yaml
jobs:
  backend-gates:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        goos: [linux, windows]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
      - name: Run Backend Tests & Compilation
        env:
          GOOS: ${{ matrix.goos }}
        run: |
          cd backend
          if [ "${{ matrix.goos }}" = "windows" ]; then
            go vet ./...
            go build -v ./...
          else
            go test -race -v -coverprofile=coverage.out ./...
          fi
```

## 5. Implementation surface

| File Path | Change | Rationale |
|---|---|---|
| `.github/workflows/ci.yml` | Add matrix strategy for `GOOS: [linux, windows]` to `backend-gates` job | Validates Windows compilation without requiring Windows runners. |
| `docs/superpowers/specs/2026-08-06-s0-platform-derisking-design.md` | Create sub-project design specification | Documents platform risk analysis, spike protocol, and decision boundaries. |

## 6. Failure handling and observability

Spike test execution relies on physical device diagnostic logging:

1. **Safari Web Inspector**: Mobile Safari on iOS connects via USB to macOS Safari Web Inspector to capture console output, uncaught promise rejections, and frame security errors.
2. **Worker Error Events**: `PdfReader.tsx` worker initialization errors must be trapped via `worker.onerror` listeners and rendered directly into the `.reader-error` DOM element.
3. **HLS Diagnostic Logging**: `hls.on(HlsCtor.Events.ERROR, ...)` events (`frontend/src/components/stream/HlsPlayer.tsx:66-71`) log detailed error codes (`data.details`, `data.fatal`) to evaluate MSE recovery vs unrecoverable stream failures.

## 7. Security and privacy

1. **Origin Isolation**: Testing over local network requires binding local development servers to local IP addresses with temporary TLS certificates to replicate secure context requirements (`https://` or `localhost`).
2. **Credential Safety**: Spike testing must use disposable dev tokens and synthetic media files. No production credentials or community media items may be embedded in spike builds.

## 8. Verification

Verification requires manual execution of the two-stage spike on physical hardware and automated CI verification:

```bash
# Backend — unit suite and static analysis (run via podman per AGENTS.md section 5)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Cross-compilation verification for Windows target
podman run --rm -v ./backend:/app:z -w /app -e GOOS=windows docker.io/library/golang:1.26.5 go build -v ./...

# Frontend validation suite
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build

# Repository validation guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

## 9. Documentation changes required with implementation

1. **Spike Findings Record**: Upon completion of Stage A and Stage B testing, document the outcome in `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md` (or append to operational logs) detailing pass/fail status per component.
2. **Architecture Status Updates**: If `EpubReader.tsx` triggers the pre-declared fallback (reverting shell choice to Electron/Capacitor), update `docs/superpowers/specs/2026-08-06-backend-client-split-program.md` and `docs/superpowers/specs/2026-08-06-s5-tauri-shell-design.md` to reflect the revised desktop/mobile runtime target.

## 10. Deferred / out of scope

1. **Tauri Shell Production Implementation**: Packaging production binaries, window management, auto-updates, and desktop system tray integration are deferred to `docs/superpowers/specs/2026-08-06-s5-tauri-shell-design.md`.
2. **Android WebView Testing**: Android Chrome WebView uses standard Chromium MSE and web worker implementations; iOS WKWebView is the sole critical risk path for S0.
3. **Persistent Spike Code Merges**: No component modifications from the spike harness will be merged into `main`.
