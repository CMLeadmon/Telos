# Platform De-risking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** De-risk Telos's three fragile media components (`EpubReader.tsx`, `PdfReader.tsx`, `HlsPlayer.tsx`) on physical iOS WebKit and establish cross-platform Windows backend compilation checks in CI.

**Architecture:** This plan is a diagnostic spike and CI configuration plan; it produces findings and throwaway code, not shipped software. Testing progresses through two staged gates (Stage A on mobile Safari over LAN, Stage B under a minimal Tauri v2 iOS asset scheme), evaluating each fragile media component against a pre-declared failure branch before decision-forcing fallbacks are triggered. Continuous Integration workflow `.github/workflows/ci.yml` is updated to include a `GOOS` matrix (`linux`, `windows`) to validate backend compilation portability on every pull request.

**Tech Stack:** Next.js 16.2.10 static export, React 19.2.7, TypeScript 5.9.3, epubjs 0.4.2, pdfjs-dist 6.1.200, hls.js 1.6.16, Tauri v2 (iOS harness), GitHub Actions (`ci.yml`), Go 1.26.5.

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

## Verification commands — use these exact forms

```bash
# Backend unit + vet (containerized)
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend integration (selectors: auth|realtime|security|db|storage|product)
bash scripts/test-backend.sh auth

# Frontend (from frontend/)
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build

# Repo guards
bash scripts/validate-compose.sh --env-file .env
bash scripts/check-product-truth.sh
```

---

## File structure

- Create temporary spike branch `spike/s0-ios-derisking` (throwaway code, not merged to `main`).
- Modify `.github/workflows/ci.yml:13-24` — add `GOOS` build matrix (`linux`, `windows`) to `backend-gates`.
- Create `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md` — record pass/fail evidence and fallback decisions.

---

### Task 1: Stage A — Mobile Safari physical iOS LAN validation

**Files:**
- Reference: `frontend/src/components/library/EpubReader.tsx:90-129`
- Reference: `frontend/src/components/library/PdfReader.tsx:91-103`
- Reference: `frontend/src/components/stream/HlsPlayer.tsx:10-93`
- Create (temporary): `frontend/src/app/(shell)/spike/page.tsx`

**Interfaces:**
- Evaluates `epubjs` 0.4.2 rendition iframe creation and `window.getSelection()`.
- Evaluates `pdfjs-dist` 6.1.200 `getDocument` with `withCredentials: true`.
- Evaluates `hls.js` 1.6.16 MSE support via `Hls.isSupported()` vs native WebKit HLS fallback `video.canPlayType("application/vnd.apple.mpegurl")`.

- [ ] **Step 1: Setup local HTTPS dev server for physical iOS device access**

Configure the Next.js development server to listen on local network IP with HTTPS certificates for secure context evaluation:

```bash
cd frontend
npx devcert-cli generate localhost 192.168.1.50
npm run dev -- -H 0.0.0.0 -p 3000
```

Verify that a physical iOS device (iOS 17+) connected to the same LAN can open `https://192.168.1.50:3000/spike`.

- [ ] **Step 2: Execute Stage A test for EpubReader.tsx**

Load a sample EPUB file in `EpubReader.tsx`. Inspect iframe manager instantiation at `frontend/src/components/library/EpubReader.tsx:95-100` (`view: registry.Views?.iframe`) and verify global assignment `(window as unknown as { ePub?: unknown }).ePub = ePub` at `frontend/src/components/library/EpubReader.tsx:90`. Attempt text selection inside the iframe and trigger selection handlers at `frontend/src/components/library/EpubReader.tsx:127-129` (`contents.window.getSelection?.()`).

Record evidence via macOS Safari Web Inspector connected to physical iOS device over USB:
- Console error log output (check for `SecurityError` or blocked cross-origin iframe access).
- Selection text payload output.

Evaluate exit criterion: Pass if nested-iframe rendition renders properly and `contents.window.getSelection()` returns highlighted text without security origin exceptions.

**Pre-declared failure branch:** If WKWebView blocks cross-frame `getSelection()` or nested iframe rendering, revert shell architecture to Electron (desktop) + Capacitor (mobile), or replace `epubjs` with non-iframe reader.

- [ ] **Step 3: Execute Stage A test for PdfReader.tsx**

Load a sample PDF document in `PdfReader.tsx`. Inspect worker initialization at `frontend/src/components/library/PdfReader.tsx:91-94` (`pdfjs.GlobalWorkerOptions.workerSrc = new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url).toString()`) and credentialed document load at `frontend/src/components/library/PdfReader.tsx:102-103` (`pdfjs.getDocument({ url: ..., withCredentials: true })`).

Record evidence via Safari Web Inspector:
- Worker initialization state and worker thread error events.
- Page rendering on HTML5 canvas element.

Evaluate exit criterion: Pass if PDF pages render cleanly and credentialed network requests succeed.

**Pre-declared failure branch:** If worker fails to load under Safari mobile webview, reconfigure PDF worker loading strategy or fallback to non-worker rendering.

- [ ] **Step 4: Execute Stage A test for HlsPlayer.tsx**

Load an HLS stream in `HlsPlayer.tsx`. Inspect native HLS check at `frontend/src/components/stream/HlsPlayer.tsx:44` (`video.canPlayType("application/vnd.apple.mpegurl")`), MSE support check at `frontend/src/components/stream/HlsPlayer.tsx:56` (`HlsCtor.isSupported()`), and `xhrSetup` credential configuration at `frontend/src/components/stream/HlsPlayer.tsx:62-64` (`xhr.withCredentials = true`).

Record evidence via Safari Web Inspector:
- Log whether `Hls.isSupported()` returns `true` or `false` on iOS 17+.
- Log `video.canPlayType` return value (`"probably"` / `"maybe"`).
- Test gateway 302 redirect resolution (`frontend/src/components/stream/HlsPlayer.tsx:10-13`) and media playback stream events (`hls.on(HlsCtor.Events.ERROR, ...)`).

Evaluate exit criterion: Pass if `hls.js` MSE functions cleanly or degrades to native WebKit HLS handling without fatal media stalls.

**Pre-declared failure branch:** If `hls.js` MSE fails on WKWebView, degrade to native HLS playback via `<video src="...">` relying on native WebKit manifest parsing.

---

### Task 2: Stage A Gate evaluation

**Files:**
- Create: `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md`

**Interfaces:**
- Aggregates diagnostic evidence from Task 1 to issue Go/No-Go decision for Stage B.

- [ ] **Step 1: Compile Stage A diagnostic findings**

Document all logged console output, WebKit security exceptions, worker states, and HLS event payloads in `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md`:

```markdown
# S0 Platform De-risking Spike Results

## Stage A Evaluation (Mobile Safari iOS 17+)

- **EpubReader.tsx**: [PASS/FAIL] - Nested iframe rendering and `contents.window.getSelection()` operational.
- **PdfReader.tsx**: [PASS/FAIL] - Worker initialization and canvas rendering operational.
- **HlsPlayer.tsx**: [PASS/FAIL] - MSE / Native WebKit fallback operational.

## Stage A Decision
- Status: [GO for Stage B / TRIGGER FALLBACK]
```

- [ ] **Step 2: Evaluate gate criteria**

If all three components pass Stage A exit criteria, proceed to Task 3 (Stage B). If `EpubReader.tsx` fails, execute pre-declared fallback: document architecture change from Tauri shell to Electron (desktop) + Capacitor (mobile) in `docs/superpowers/specs/2026-08-06-backend-client-split-program.md`.

---

### Task 3: Stage B — Minimal Tauri v2 iOS harness under custom `asset:` scheme

**Files:**
- Reference: `frontend/src/components/library/PdfReader.tsx:91-103`
- Create (temporary harness): `src-tauri/` (minimal Tauri v2 iOS scaffold)

**Interfaces:**
- Evaluates `PdfReader.tsx` web worker instantiation via `new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url)` under Tauri v2's custom `asset:` scheme on physical iOS hardware.

- [ ] **Step 1: Scaffold throwaway Tauri v2 iOS harness**

Initialize a minimal Tauri v2 mobile harness wrapping `frontend/out/`:

```bash
npx @tauri-apps/cli@v2 init --app-name telos-spike --window-title "Telos Spike" --dist-dir "../frontend/out"
npx @tauri-apps/cli@v2 ios init
```

- [ ] **Step 2: Build and deploy to physical iOS test device via Xcode**

Open generated Xcode project and deploy to physical iOS device:

```bash
npx @tauri-apps/cli@v2 ios dev --target <physical-device-udid>
```

- [ ] **Step 3: Test PdfReader.tsx under asset: scheme**

Navigate to PDF reader view inside Tauri iOS webview. Inspect PDF worker URL construction at `frontend/src/components/library/PdfReader.tsx:91-94`:

```ts
pdfjs.GlobalWorkerOptions.workerSrc = new URL(
  "pdfjs-dist/build/pdf.worker.min.mjs",
  import.meta.url
).toString();
```

Record evidence via Safari Web Inspector connected to Tauri iOS app process:
- Check if `asset:` scheme blocks worker file fetching or worker script execution.
- Capture `worker.onerror` events or CORS/origin errors.

Evaluate exit criterion: Pass if `pdf.worker.min.mjs` initializes successfully under `asset:` scheme.

**Pre-declared failure branch:** If web worker fails to instantiate under custom `asset:` scheme, bundle `pdf.worker.min.mjs` as explicit same-origin static asset served without dynamic worker workerSrc construction.

- [ ] **Step 4: Record Stage B evidence**

Append Stage B test results to `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md`:

```markdown
## Stage B Evaluation (Tauri v2 iOS Asset Scheme)

- **PdfReader.tsx Worker under asset:**: [PASS/FAIL]
- **Applied Fallbacks**: [NONE / Bundled static worker / Native HLS]
```

---

### Task 4: Add GOOS matrix to CI workflow

**Files:**
- Modify: `.github/workflows/ci.yml:13-24`

**Interfaces:**
- Adds `windows` to `backend-gates` matrix in GitHub Actions workflow `.github/workflows/ci.yml`.

- [ ] **Step 1: Update .github/workflows/ci.yml with GOOS matrix strategy**

Modify `.github/workflows/ci.yml:13-24` to introduce matrix execution for `linux` and `windows`:

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

- [ ] **Step 2: Verify workflow file validity**

Verify syntax of `.github/workflows/ci.yml` using `podman` cross-compilation test command:

```bash
podman run --rm -v ./backend:/app:z -w /app -e GOOS=windows docker.io/library/golang:1.26.5 go build -v ./...
```

---

### Task 5: Document findings and purge throwaway spike code

**Files:**
- Modify: `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md`
- Delete: temporary spike files (`frontend/src/app/(shell)/spike/`, `src-tauri/`)

**Interfaces:**
- Finalizes S0 de-risking records and restores clean repository state.

- [ ] **Step 1: Finalize results document**

Ensure `docs/superpowers/specs/2026-08-06-s0-platform-derisking-results.md` contains complete diagnostic details, WebKit findings, active fallbacks, and the CI `GOOS` matrix status.

- [ ] **Step 2: Remove throwaway code**

Delete temporary spike routes and harness files:

```bash
git checkout main
git branch -D spike/s0-ios-derisking
```

Confirm repository inventory matches tracked paths using:

```bash
bash scripts/verify-clean-checkout.sh --inventory-only
```
