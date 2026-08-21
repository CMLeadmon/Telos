# Backend/client split — handoff

**Branch:** `split/backend-and-universal-frontend` (42 commits ahead of `main`)
**Date:** 2026-08-20
**Program:** the seven sub-projects specified in
[`specs/2026-08-06-backend-client-split-program.md`](./specs/2026-08-06-backend-client-split-program.md)

---

## Where the program stands

**44 of 46 planned tasks are implemented.** The two that are not are S0 Tasks 1
and 3, which require a physical iOS device, a macOS host and Safari Web
Inspector over USB.

| | Sub-project | Tasks | State |
|---|---|---|---|
| S0 | Platform de-risking | 3/5 | T1 and T3 need hardware. T2's gate decision and T5 are recorded in [`specs/2026-08-06-s0-platform-derisking-results.md`](./specs/2026-08-06-s0-platform-derisking-results.md). |
| S1 | Headless backend + API contract | 6/6 | Complete |
| S2 | Device token auth | 10/10 | Complete |
| S3 | Frontend origin awareness | 6/6 | Complete |
| S4 | Connection layer | 7/7 | Complete |
| S5 | Tauri shell | 6/6 | Complete |
| S6 | Windows installer | 6/6 | Complete |

Do not read the checkboxes in `plans/2026-08-06-split-s*.md` as status. All 173
of them are still `[ ]` despite 44 tasks being done; progress has to be
reconstructed from commit history. That is worth fixing or the plans should be
marked historical.

---

## Verification, as of the last commit

Everything below was run on this branch, not inferred.

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go test ./...` (unit) | pass |
| `scripts/test-backend.sh` (integration, real PG 16 + Redis) | pass, 84.8s |
| Cross-compile `cmd/telos` — linux/amd64, windows/amd64, darwin/arm64 | all build |
| `gofmt -l` | clean |
| `npm run lint` | 0 errors, 6 pre-existing warnings |
| `check:origin-leak` | clean |
| `npx tsc --noEmit` | clean |
| `npm run test:unit` | 43 files, **207 tests** pass, 6 consecutive full runs clean |
| `npm run build` (static export) | succeeds |
| `genopenapi --check` (contract drift) | current, 115 routes |
| `scripts/check-product-truth.sh` | pass |
| `scripts/verify-clean-checkout.sh --inventory-only` | pass, 23 migrations contiguous |
| `scripts/validate-compose.sh --env-file .env` | **fails** — see below |
| Playwright E2E | **not run** |
| Tauri shell compile (Rust) | **never run, by anyone** |

`validate-compose.sh` fails on `core-ingress (unexpected published ports)`. This
was checked against a `main` worktree using the same `.env` and fails
identically — it is the local rootless dev `.env` (Traefik on 8081/8443), not
this branch. Do not treat it as a regression; do confirm it passes against a
production `.env` before release.

---

## What this session changed

Seven commits, `c9ad7f6..02917a7`.

1. **`c9ad7f6`** — `check-product-truth.sh` had been red since S4 T7 because a
   security comment in `auth.go` used the word "oracle" in its cryptographic
   sense. Reworded; the guard is not loosened.
2. **`c58e090`** — S5 T4–6: MediaSession lock-screen transport, the `data:`
   guard in `assetUrl`, `documentation/operations/building-native-clients.md`,
   and the `tauri:build:*` scripts.
3. **`c39fc52`** — S6 T5: `generateConfigEnv` and `runUninstallPlan` in
   `backend/cmd/telos/`, with 19 tests.
4. **`0bf1656`** — S0 results, and the `@tauri-apps/cli` pin that unblocked the
   inventory gate.
5. **`cc4083f`** — two defects found reviewing the above.
6. **`49dba8d`** — this document.
7. **`02917a7`** — a pre-existing flake in `AudiobookPlayer.test.tsx`, which
   released a held promise through a resolver the mock had not assigned yet. It
   failed one full-suite run in four before this branch and three in four after,
   purely from added load. Measured both ways before concluding it was
   pre-existing; fixed by waiting on the mint rather than raising the timeout.

The plans' illustrative code was wrong in several places and was not followed
literally. Each divergence is argued in the relevant commit message; the
important ones are that the plan's MediaSession test asserted a mock it had just
created (it passed with the feature absent), its `rand.Read` error was
discarded, and its `RunUninstall` printed a success message while doing nothing.

---

## What to do next, in priority order

### 1. Token-mode video is broken on every WebKit webview

**This is the most important item and it blocks shipping a native client to any
Apple platform.** Full analysis in the S0 results document; the summary is that
`HlsPlayer` takes the native-HLS branch on Safari and WKWebView, assigns
`video.src` directly, and `GET /api/v1/hls/{locator}` is still wrapped in
`withAuth` — so a token client with no cookie gets a 401. iOS, iPadOS and macOS
are affected; Windows (WebView2) and Linux (WebKitGTK) are not.

**The fix is much smaller than it first appears.** `rewriteHLSManifest`
(`backend/hls.go:157`) already rewrites every segment, variant and URI-bearing
attribute to an opaque signed locator, and `hlsLocator` (`hls.go:66`) already
carries item, user, session binding and expiry. The manifest is therefore
already self-authorizing. What is missing is only that the route does not use
it:

- Authorize `GET /api/v1/hls/{locator}` **from the locator** rather than from
  the session cookie — the same shape as `handleTicketedAudiobookStream`
  (`backend/streamticket.go:180`), which resolves the user through
  `LoadAuthenticatedUser` so logout, account disable and device revocation still
  kill outstanding locators on the next request.
- Raise `hlsLocatorTTL` (`hls.go:62`, currently 2 minutes) for this path. Two
  minutes is fine for hls.js, which refetches and re-signs the manifest
  continuously; a native player holds one manifest across a whole film and its
  segment locators expire underneath it. D11 hit exactly this and settled on 12
  hours, made safe by re-checking permission at spend time.
- Frontend: nothing, if the locator route stops requiring a session. Confirm
  rather than assume.

Read D11's commit message (`109e1ee`) first. It explains why the HLS signing key
and the audiobook ticket key are derived separately — the field sets overlap on
`i`, `u`, `s`, `e`, so one key would let an HLS locator be spent as an audiobook
ticket. Do not collapse them.

### 2. Wire `install`/`uninstall` into the Go CLI

`generateConfigEnv` and `runUninstallPlan` are implemented and tested but not
reachable: `dispatch.go:80` still stubs `init`, `start` and `stop`, and adding a
reachable `uninstall` before those land would mean two divergent uninstallers.
When wiring:

- `executableLocations(goos, home)` takes `home` for testability. On Windows the
  correct root for the Programs path is `%LOCALAPPDATA%`, which
  `os.UserCacheDir()` returns and which can be redirected — do not assume
  `~/AppData/Local`.
- `generateConfigEnv` covers only the secrets half of `telos init`. The storage
  tree, domain and ACME prompts, and the rootless port fallback are still only
  in `cli/commands/init.sh`.

### 3. Run the two device-gated S0 tests

Whether WKWebView permits cross-frame `getSelection()` for epubjs, and whether
it permits `new Worker()` from a custom scheme. Procedure is at the end of the
S0 results document. Note the recommendation there: **do not trigger the
Tauri→Electron+Capacitor fallback**, which is conditional on epubjs failing and
would now mean discarding the shipped Rust TLS bridge and secret store.

### 4. Nothing compiles the Tauri shell

CI builds the frontend, runs its tests, and cross-compiles the Go CLI. It does
not touch `frontend/src-tauri/`, so a Rust-side break surfaces only when someone
runs a build by hand. This is recorded in the ops doc under "What CI does and
does not check". A desktop-only job on `ubuntu-latest` needing
`libwebkit2gtk-4.1-dev` would catch most of it cheaply.

### 5. Smaller things

- **`wsBase()` document fallback.** With no configured node it derives the
  socket from `window.location` (`api.ts:42-46`), which under a Tauri scheme
  would give `ws://localhost` pointing at the app bundle. It should be
  unreachable — `/connect` gates entry, and `requiresServerSelection()` forces a
  node on native builds — so the invariant is pinned in `assetScheme.test.ts`
  rather than the behaviour changed. Worth making explicit if you want it
  belt-and-braces.
- **`CLAUDE.md` is stale.** It still says *"Current work (branch
  `installer-cli-and-frontend`)"*, two branches ago, and does not mention the
  split program, device tokens, the connection layer or the Tauri shell.
- **`AGENTS.md` §2 phase list** predates all seven sub-projects.

---

## Things that will trip you up

- **Go is not on the host.** Everything runs in podman, not docker:
  `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 …`
- **`frontend/src-tauri/` is not throwaway.** S0 Task 5 lists it among files to
  delete; that instruction was written before S5 Task 1 made it the real shell.
  Deleting it deletes the client. The S0 results document flags this.
- **Never `telos init --force` on an existing node.** It regenerates every
  secret and desynchronizes them from the existing Postgres and MariaDB volumes.
  `generateConfigEnv` refuses to overwrite for the same reason.
- **`TELOS_ALLOWED_ORIGINS` must list `tauri://localhost` and
  `http://tauri.localhost`** or every write from a native client returns
  `403 origin_forbidden` while reads work fine — a failure that looks like
  anything but its cause. See the ops doc.
- **Migrations are checksum-verified.** Never edit an applied one; add
  `0024_*.sql`.
- **`frontend/package.json` pins exactly.** `verify-clean-checkout.sh
  --inventory-only` fails on any `^` or `~` range. That is how the
  `@tauri-apps/cli` range was caught.
