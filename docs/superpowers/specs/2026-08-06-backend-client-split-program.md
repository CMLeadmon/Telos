# Telos Backend/Frontend Split — Feasibility Report & Refactor Plan

**Status:** Approved program · **Date:** 2026-08-06 · **Branch:** `split/backend-and-universal-frontend` @ `6d1f6c0`

This is the **parent program document** for the backend/client split. Each of the seven
sub-projects below has its own design spec in this directory and, later, an execution plan
under `docs/superpowers/plans/`. Sub-project specs reference this document for shared
context rather than restating it.

---

## Context

Telos today is a single-origin monolith: one Go gateway (`backend/`, ~30,400 LOC across ~50 files)
that embeds and serves a statically-exported Next.js frontend (`frontend/`, 14,591 LOC) and fronts
all bundled services. Browser access is the only client model, and the security architecture
enforces that deliberately — `SameSite=Strict` cookies, exact-origin CSRF checks on every write,
and zero CORS in production.

**The goal is to decouple client development from backend deployment.** After this refactor:

- **Telos Backend** is a standalone host application. Running it is complete and sufficient — it
  exposes the API and nothing else is required. Serving a web UI becomes an operator *option*.
- **Telos Frontend** is a separately-downloadable client stack. One codebase, two delivery shapes:
  native apps (Tauri v2 → Windows/macOS/Linux/iOS/Android) that a community member installs and
  points at their server, plus a web build the operator may choose to host.

Clients ship on their own cadence against a stable API; operators aren't forced to host a UI they
don't want; users choose how they connect.

### Decisions locked

| Decision | Choice |
|---|---|
| Windows backend | Docker Desktop / WSL2 runs the same Linux images — **installer port only** |
| Frontend shell | **Tauri v2** — one shell, all 5 platforms, wrapping the existing static export |
| Client model | **Anyone in a community** connects to their own server (needs a connection layer) |
| Auth | **Device tokens alongside cookies** — browsers keep `SameSite=Strict` unchanged |
| Web UI hosting | **Both** — gateway-served + strict by default; independent origin opt-in via explicit CORS allowlist |
| Repo layout | **One repo, two release artifacts** (`telos-backend-vX`, `telos-client-vY`) |

---

## Sub-project decomposition

The program is too large for a single spec. It decomposes into seven sub-projects, each
with its own design spec and execution plan. The phase numbering below supersedes the
Execution Plan phase numbering later in this document, which it maps to one-for-one.

| ID | Sub-project | Depends on | Spec |
|---|---|---|---|
| **S0** | Platform de-risking spike | — | `2026-08-06-s0-platform-derisking-design.md` |
| **S1** | Headless backend + API contract | — | `2026-08-06-s1-headless-backend-api-contract-design.md` |
| **S2** | Device-token auth | — | `2026-08-06-s2-device-token-auth-design.md` |
| **S3** | Frontend origin-awareness | S2; S1 (soft) | `2026-08-06-s3-frontend-origin-awareness-design.md` |
| **S4** | Connection layer | S3 | `2026-08-06-s4-connection-layer-design.md` |
| **S5** | Tauri v2 shell | S0, S4 | `2026-08-06-s5-tauri-shell-design.md` |
| **S6** | Windows backend installer | — | `2026-08-06-s6-windows-installer-design.md` |

**Critical path: S2 → S3 → S4 → S5.** S0, S1, and S6 are independent and run alongside it.

Two sequencing amendments to the Execution Plan below:

1. **S0 is staged, not monolithic.** The report's Phase 0 assumes a full Tauri iOS target,
   which needs a Mac, a physical device, and an Apple Developer account. A cheaper first
   pass loads the existing static export in **mobile Safari** on any iOS device — WKWebView
   and Safari share WebKit, so epub.js's cross-frame `getSelection()` and hls.js's MSE
   support get real signal for near-zero setup. It does **not** cover Tauri's custom
   `asset:` scheme, which is precisely the pdf.js worker risk, so the full target is still
   required — just not first.
2. **S1 does not gate the program.** It is independently valuable even if no client ever
   ships, because it stops forcing operators to host a UI they do not want.

---

## Verdict

**Feasible as a refactor. Do not rebuild.** The codebase is unusually well-positioned for this
split — better than you'd expect from a project never designed for it. The seams are already there.

The work is **not** where you'd assume. It is not disentangling the frontend from the backend
(that's 3 lines of Go). It's in three places that don't exist yet: a **device-token auth path**, a
**server-connection layer** in the client, and **validating three fragile media components**
against five platform webviews.

**Rough shape: 3–4 months of focused work**, with almost all variance concentrated in one risk
(iOS WKWebView).

### What's already in our favor

| Signal | Evidence |
|---|---|
| Frontend↔backend coupling is trivially thin | 3 lines: `main.go:117-118`, `392-396`, `549` — plus one self-contained 96-line `frontend_theme.go` |
| The seam was pre-built | `frontend/src/lib/api.ts:5` — `apiBase()` returning `""` is a deliberate hook, and `HlsPlayer.tsx:93` already has a dormant `crossOrigin="use-credentials"` branch waiting on it |
| Network calls are centralized | **104 of 107** calls funnel through one 67-line module. 97 endpoint literals ride along free. |
| Frontend is small and shell-shaped | 14,591 LOC · 7 routes · **zero dynamic routes** · no `next/image` · no service worker · only 9 `next/*` import sites |
| Styling survives a webview move intact | Plain CSS + custom properties. No Tailwind, no CSS-in-JS, no build-time style magic. |
| Mobile groundwork already done | Safe-area insets, 44px touch tokens, `visualViewport` soft-keyboard handling (`AppShell.tsx:56-68`), and a 20-combination Playwright mobile matrix |
| Container topology is well-behaved | No `privileged`, no host networking, no docker-socket mounts, no `os/exec`, no cgo anywhere |

The single most important number is **104/107**. That's why this is a refactor.

---

# Execution Plan

## Phase 0 — De-risk first (before committing budget)

Both items are cheap and both kill the largest unknowns.

### 0a. iOS webview spike

Build a throwaway Tauri v2 iOS target and load **only** these three components against a real device.
Tauri uses each OS's *native* webview rather than bundling Chromium — that is the entire risk.

| Component | File | What to prove |
|---|---|---|
| `epubjs` 0.4.2 | `frontend/src/components/library/EpubReader.tsx` | Nested-iframe rendition (`:99`) works, and cross-frame `contents.window.getSelection()` (`:126-127`) isn't blocked as cross-origin. Also verify the `window.ePub` global workaround (`:90`) survives. **Highest risk item in the entire refactor.** |
| `pdfjs-dist` 6.1.200 | `frontend/src/components/library/PdfReader.tsx` | Worker loads via `new URL(..., import.meta.url)` (`:91-92`) under Tauri's `asset:` scheme — the classic breakage point for custom schemes |
| `hls.js` 1.6.16 | `frontend/src/components/stream/HlsPlayer.tsx` | MSE available (`:54`) — thin on iOS WKWebView below 17.1; the gateway's 302 → `main.m3u8` (`:10-13`) resolves; `withCredentials` (`:62-64`) behaves once swapped for bearer auth |

**Exit criteria:** all three render. If epub.js fails → decide between replacing the reader or
falling back to Electron (desktop, bundles Chromium) + Capacitor (mobile) **before** any other
phase starts. A one-week spike here is worth more than a month of downstream planning.

### 0b. Add a `GOOS` CI matrix

`.github/workflows/ci.yml` is `ubuntu-latest` only. There is currently **zero signal** that the
backend compiles for `GOOS=windows`. Add the matrix now — cheap, and it validates the Phase 6
assumption early.

---

## Phase 1 — Headless backend + API contract

### 1a. Make the frontend embed optional

Move these into a `//go:build embedfrontend` file with a no-op counterpart. Nothing else in the
backend reads `frontendFS`.

- `backend/main.go:117-118` — `//go:embed all:out` + `var frontendFS embed.FS`
- `backend/main.go:392-396` — `fs.Sub` (currently `log.Fatalf` when `out/` is missing, so **the
  backend cannot be built headless today**) + `http.FileServer`
- `backend/main.go:549` — `mux.Handle("/", themedFrontend(fileServer))`
- `backend/frontend_theme.go` — all 96 lines; same-origin-only machinery

Headless becomes the **default** build target; embedding becomes opt-in. `backend/Dockerfile:17`
gains a build-arg to select the variant.

Frontend counterpart: `frontend/src/components/ThemeSync.tsx:23` writes the `telos_theme` cookie
purely so the gateway can pre-set `data-theme` and avoid a paint flash. Meaningless in the
headless/native config — gate it.

### 1b. Generate an API spec

There is **no machine-readable spec** for the 108 routes registered at `backend/main.go:402-549`.
Today you change a route and its caller in one commit; once a client is installed on someone's
phone, an old client hits a new backend forever. **This is the permanent tax of the split and the
part that never ends.**

Add an OpenAPI document generated from the route table, and generate the frontend's typed client
from it. This is the mechanism that makes "one repo, two artifacts" honest — CI enforces the
boundary rather than trusting discipline.

Route inventory for reference (108 + 1 catch-all, all under `/api/v1`):

| Group | Count | Group | Count |
|---|---|---|---|
| `library` | 27 | `auth` | 6 (4 public) |
| `admin` | 16 | `health` | 3 (public) |
| `channels` | 15 | `stream` | 3 |
| `media` | 12 | `folders` | 2 |
| `users` | 11 | `events` | 2 (incl. WS) |
| `files` | 8 | `search` · `hls` · `chat` | 1 each |

~25 of the 108 are binary/streaming endpoints (covers, content, avatars, audio/video, HLS
segments, downloads) rather than JSON — these are the ones that break hardest under an origin
split, since `<img>`, `<audio>`, `<video>` and hls.js each need their own credential handling.

### 1c. Boundary CI gates

- Backend builds and its tests pass with `frontend/` absent
- Frontend builds and typechecks against the generated client, not a live backend
- Two independently-versioned release jobs

---

## Phase 2 — Device-token auth (CRITICAL PATH)

Everything remote depends on this. Build it before the client work.

### Current state (all deliberate, all documented)

| Mechanism | Location |
|---|---|
| `SameSite: Strict`, HttpOnly session cookie | `backend/main.go:1031-1039`, `1066-1074` |
| Exact-origin required on every non-GET | `backend/security.go:170-192` (`requireTrustedOrigin`) |
| Identical origin check on both WS upgrades | `backend/main.go:56-59` |
| CORS emitted **only** when `TELOS_ENV=development` | `backend/security.go:240-258` |
| CSP `connect-src 'self'` | `backend/security.go:212-236` |
| Cookie-only invariant, explicitly documented | `backend/main.go:685` |

A downloaded app is cross-site to its server by definition — it receives no cookie and gets
`403 origin_forbidden` on every write. No amount of packaging work changes that.

### The change

This **rescopes** the `main.go:685` invariant rather than weakening it: cookie-only remains the
rule for *browser sessions*; device tokens are a separate credential class. **Update that comment
in place** so the next reader knows it was revised deliberately, not eroded.

- New `devices` table — `backend/db/migrations/0023_*.sql`, written idempotently
  (`IF NOT EXISTS` / `ON CONFLICT`). **Never edit an already-applied migration** — `migrations.go`
  verifies checksums.
- Registration → long-lived refresh token → short-lived access token
- `getAuthenticatedUser` (`backend/main.go:680-706`) grows a bearer branch.
  `withAuth` (`main.go:803-839`) needs no changes — it consumes `*UserContext`.
- **WebSocket auth:** browsers can't set headers on WS. Use a subprotocol or a short-lived
  single-use ticket — **not** a query param, which would break the never-relay-to-upstream-proxy
  property that `sessionTokenFromRequest` (`backend/proxy.go:17-35`) protects.
  Upgrade sites: `backend/main.go:1277` (chat), `backend/event_handlers.go:64` (events).
- Per-device revocation surfaced in Settings (`frontend/src/components/settings/`)
- Rate limiting + audit logging on registration

### Opt-in production CORS

Promote the existing dev-gated machinery at `backend/security.go:240-258` behind an explicit
`TELOS_ALLOWED_ORIGINS`. **Off by default** — an operator who independently hosts the web UI
accepts the weaker posture knowingly; nobody gets it by accident. Widen CSP `connect-src`
(`security.go:212-236`) only when that variable is set.

~400–600 LOC, but budget 1–2 weeks. Security-critical; deserves adversarial review, not just tests.

---

## Phase 3 — Frontend origin-awareness

Mostly mechanical. **The trap is that relative URLs escape the `api.ts` boundary in two ways.**

### 3a. Parameterize the core

- `frontend/src/lib/api.ts:5-7` — `apiBase()` reads from connection state instead of returning `""`
- `frontend/src/lib/api.ts:21-25` — `wsBase()` currently derives from `window.location.host`;
  must derive from the configured server (yields garbage under a custom scheme)
- `frontend/src/lib/api.ts:34-67` — `api()` swaps `credentials: "include"` for bearer headers
  when in token mode

### 3b. Close the leaks

| Site | Issue |
|---|---|
| **Backend emits relative `coverUrl` over the wire** — 6 sites: `backend/grimmory_catalog.go:143`, `backend/jellyfin_catalog.go:196,245,385,416`, `backend/main.go:3153` | Rendered raw into `<img src>` at ~14 sites. **Rewrite at the API response boundary, not in components.** Miss this and every cover image silently 404s in the app. |
| `frontend/src/components/library/AudiobookPlayer.tsx:117-119` | Hardcoded relative stream path — bypasses `apiBase()`. The one leak in an otherwise disciplined layer. |
| `frontend/src/lib/upload.ts:34-35` | XHR + `withCredentials` |
| `frontend/src/components/library/EpubReader.tsx:69` | Raw `fetch` with `credentials: "include"` |
| `frontend/src/components/settings/ProfileSection.tsx:42` | Raw `fetch` for avatar upload |
| `frontend/src/components/stream/HlsPlayer.tsx:93` | Flip the dormant `crossOrigin="use-credentials"` branch live |
| `frontend/src/components/stream/HlsPlayer.tsx:62-64` | `hls.js` `xhrSetup` — swap `withCredentials` for bearer |

---

## Phase 4 — Connection layer (largest new frontend feature)

No equivalent exists today — `apiBase()` is a constant and there is no concept of *which server*.

- Server-URL entry + validation screen (new route, ahead of `/login`)
- Connection/reachability state (new Zustand store alongside the existing 10 in `frontend/src/stores/`)
- **Token storage in the OS keychain** via the Tauri plugin — never `localStorage`.
  Note `useThemeStore.ts:13,20` is currently the only `persist` user; do **not** follow that
  pattern for credentials.
- Sign-out / forget-server
- Error states: unreachable, version-skew, untrusted certificate

### Certificate trust (genuine design work, not a UX detail)

Self-hosted community servers will have self-signed certs or live on LANs. A naive "accept any
cert" toggle destroys the transport security everything else assumes. Plan deliberately —
trust-on-first-use with pinning is the usual answer.

### Mobile backgrounding

`frontend/src/lib/reconnectingSocket.ts:1-5` documents online/visibility listening that
`:100-114` does not implement — it's exponential backoff only (500 ms → 30 s, ±20% jitter).
Harmless in a browser tab; **load-bearing** once the app is backgrounded on a phone. Implement
what the docblock claims.

---

## Phase 5 — Tauri v2 shell

Informed by the Phase 0 spike. ~100% of the 14,591 LOC comes along unchanged; plain CSS +
custom properties survive a webview move intact. Binaries ~5 MB vs Electron's ~120 MB.

Platform work beyond the three media components:

| Item | Sites |
|---|---|
| `window.open` → `shell.openExternal` | `frontend/src/app/(shell)/library/page.tsx:153`, `frontend/src/components/chat/ShareCard.tsx:37`, `frontend/src/components/AppShell.tsx:198` |
| Drag-and-drop upload → native file picker on mobile | `frontend/src/components/files/FilesBrowser.tsx:375-395` |
| `navigator.clipboard` shim | `frontend/src/components/settings/AdminInvitesSection.tsx:103` |
| Routing | 7 static routes, **zero dynamic** — trivially portable to a hash/memory router |
| Background audio / lock-screen controls | `AudiobookPlayer.tsx`, `stream/page.tsx:42-46` — native media session on mobile |
| Asset paths | `frontend/AGENTS.md:42-43` — the static export uses absolute `/_next/...` paths and cannot be served under a path prefix; verify against Tauri's asset scheme |

**iOS App Store distribution is not guaranteed.** Apps requiring a user-supplied server draw
review scrutiny. TestFlight and sideloading are viable fallbacks — don't build the plan on approval.

---

## Phase 6 — Windows installer (parallelizable from Phase 1)

The Go code barely moves; the installer is the whole job.

**Recommendation: rewrite the 1,427 lines of bash as a Go CLI, not PowerShell.** We already ship a
Go toolchain, we get one cross-platform binary instead of two divergent scripts, and it can share
config/validation code with the gateway.

| File | Lines | Issue |
|---|---|---|
| `cli/lib/preflight.sh` | 211 | **The Linux lock-in** — `/etc/os-release`, distro matrix, `fuse-overlayfs`, `/dev/net/tun`, `net.ipv4.ip_unprivileged_port_start`, `registries.conf` |
| `telos` | 114 | Dispatcher — symlink resolution, `declare -F`, process substitution |
| `cli/lib/common.sh` | 89 | `detect_runtime` / `detect_compose`, `sed`-based compose introspection |
| `cli/commands/*.sh` | ~660 | `id -un`, `sudo chown`, `chmod 600` |
| `install.sh` / `uninstall.sh` | 223 / 94 | `sudo ln -sfn` into `/usr/local/bin` |

Compose adjustments: strip `:z` on non-SELinux hosts (9 bind mounts), branch `APP_UID`/`APP_GID`
(`docker-compose.yml:262`, `:308-309`), platform-appropriate `STORAGE_PATH` default (currently
`/mnt/storage/shared`, `.env.example:46`), verify Podman-on-Windows honors the `10.201.0.0/24`
fixed IPAM feeding `TELOS_TRUSTED_PROXY_CIDRS`.

Because the gateway stays containerized, the ~12 hardcoded `/data/shared` literals
(`main.go:372-387`, `3304`, `3558`, `3636-3639`, `3779`, `3822`, `settings.go:887`,
`health.go:309`) and the base64'd `filepath.Join` file IDs (`main.go:3330-3333`, `3435-3436`,
which would silently encode `\` on Windows) **stop being problems**. Leave a comment noting they'd
bite hard on a native-binary path.

---

## Verification

**Phase 0:** three components render on a physical iOS device. `GOOS=windows` build passes in CI.

**Backend** (containerized — Go is not on the host):

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
bash scripts/test-backend.sh              # integration; selectors: auth|realtime|security|db|storage|product
bash scripts/test-backend.sh auth         # device tokens
bash scripts/test-backend.sh security     # origin/CORS policy
```

Plus a new gate: backend builds and tests pass with `frontend/` absent.

**Frontend:**

```bash
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit
npm run build
npx playwright test                       # 22 specs
npx playwright test -g "mobile matrix"    # 20 combos × 4 viewports × 5 routes
```

**Live:**

```bash
telos start --dev --build
curl http://localhost:8080/api/v1/health  # also /health/live, /health/ready
bash scripts/validate-compose.sh --env-file .env
```

**End-to-end acceptance:** headless backend running with no `out/` → Tauri client on a second
machine enters the server URL, registers a device, logs in, and can read a book, play a video,
send a chat message, and upload a file. Then revoke that device from Settings and confirm the
client is cut off.

> **Never run `telos init --force` on an existing node** — it regenerates every secret and
> desynchronizes them from the existing Postgres/MariaDB volumes.

---

## Appendix — Pre-existing issues surfaced

Not caused by this refactor, but discovered during the assessment.

1. **The Linux-only storage layer is orphaned.** `backend/storage.go`, `upload.go`, `quota.go`
   (openat2 confinement with `RESOLVE_BENEATH`, `Statfs` quota enforcement, ClamAV-gated upload)
   have **zero non-test references**. Production handlers do raw `os`/`filepath` I/O instead. Good
   news for portability; likely bad news for the security properties currently assumed. Warrants
   its own investigation.
2. **Google Fonts almost certainly broken in production.** `frontend/src/styles/styles.css:13`
   remote-`@import`s four families and survives into the shipped bundle
   (`out/_next/static/chunks/3wigib93_04cp.css`), but production CSP is `font-src 'self'`
   (`backend/security.go:207-208`). Either branded typography never loads, or the CSP isn't
   applying. Also a sovereignty-premise violation — self-host the fonts.
3. **`CLAUDE.md:93` is stale** — the "detects port 3000 → :8080" mechanism was replaced by the
   `frontend/dev-server.mjs` reverse proxy; `apiBase()` is hardcoded `""`.
4. **Dead LiveKit scaffolding** — `frontend/dev-server.mjs:15-16,29-34` still proxies
   `/livekit/*`, `bundle-budget.json` lists it under `heavyModules`, and
   `@types/dom-mediacapture-record` is still a devDependency.
5. **`metricsHandler()` never registered** (`backend/metrics.go:117-119`) — no `/metrics` route
   despite `config/prometheus` and `config/alertmanager` existing.
6. **Upstream service URLs hardcoded** — `jellyfinBaseURL` (`backend/main.go:2444`),
   `grimmoryBaseURL` (`backend/library.go:34`). No env override; any topology change beyond
   compose requires code edits.
