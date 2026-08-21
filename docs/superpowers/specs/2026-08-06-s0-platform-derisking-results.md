# S0 Platform De-risking — Results

**Status: partial. Two of three Stage A findings and the Stage B analysis were
determined statically; the rest remain device-gated and are recorded here as
open with the procedure to run them.**

## Why this record is partial

Stage A and Stage B as planned require a physical iOS 17+ device, a macOS host,
and Safari Web Inspector attached over USB. None of that was available, so the
spike was never executed as written.

That is not the same as no evidence. Three of the risks the spike exists to find
are determinable from the code and from the built static export, without any
device — and one of them turned out to be live and severe. Those are recorded
below as **determined statically**, with the evidence. Everything genuinely
requiring WebKit-on-hardware is marked **OPEN**.

This document should be updated, not replaced, when the device tests run.

The spike also ran late: it was written to gate S5, and S5 shipped first. The
findings below are therefore about code that already exists rather than about
whether to build it.

---

## Stage A — Mobile Safari, iOS 17+

### EpubReader.tsx — OPEN (needs hardware)

**Risk:** epubjs 0.4.2 renders into a nested iframe and reads the selection
across the frame boundary (`contents.window.getSelection()`,
`EpubReader.tsx:129-131`). It also has to publish a bare `ePub` global before
rendering, because 0.4 reaches for one in its manager/view lookup. Whether
WKWebView permits the cross-frame selection read is a WebKit behaviour; no
static reading of the code answers it.

**Exit criterion:** nested rendition renders and `getSelection()` returns the
highlighted text with no security-origin exception.

**Pre-declared failure branch:** replace the shell architecture with Electron +
Capacitor, or replace epubjs with a non-iframe reader.

**Note on that branch:** it is now very expensive. The Tauri shell shipped in S5
(`frontend/src-tauri/`) with a Rust TLS bridge and a durable secret store, and
both are load-bearing for certificate pinning and device credentials. Triggering
this fallback means rewriting them. Prefer the narrower fallback — swapping the
reader — unless the failure is demonstrably the webview and not epubjs.

### PdfReader.tsx — PASS (determined statically)

The risk the plan named no longer exists. It cited a hardcoded
`withCredentials: true` at `PdfReader.tsx:102-103`; that code was replaced
during S3. PDF.js issues its own range requests, and the current load branches
on the auth mode (`PdfReader.tsx:105-113`): cookie mode gets
`withCredentials: true`, token mode gets an `Authorization: Bearer` header and
`withCredentials: false`. A token-mode client therefore authenticates its range
requests correctly, which is exactly what the Stage A test was to confirm.

Worker instantiation is a separate question and belongs to Stage B, below.

### HlsPlayer.tsx — **FAIL (determined statically)**

**A token-mode client cannot play video on any WebKit webview — iOS, iPadOS,
and macOS.** This is the finding the spike existed to produce, and it does not
need a device.

The chain:

1. `HlsPlayer.tsx:51` branches on
   `audio || video.canPlayType("application/vnd.apple.mpegurl")`. On Safari and
   WKWebView that call returns `"probably"`/`"maybe"` — truthy — so **WebKit
   always takes the native branch** and hls.js never loads.
2. The native branch sets `video.src = src` (`HlsPlayer.tsx:52`). A media
   element fetches its own source, and nothing can attach a header to that
   fetch.
3. `GET /api/v1/hls/{locator}` is still wrapped in
   `withAuth(..., "view_media")` (`backend/main.go:526`). The locator is signed,
   but the route additionally requires an authenticated session.
4. Token mode has no cookie by construction, and the element cannot send the
   Bearer token. The request arrives bare and comes back **401**.

There is no guard: `HlsPlayer` is rendered unconditionally from
`frontend/src/app/(shell)/stream/page.tsx:226`, with no token-mode branch.

The comment at `HlsPlayer.tsx:44-50` names this gap and assigns it to S4. S4
came and went without it — the same way the audiobook version of this bug
survived until D11.

**Blast radius by platform**, since the shell's webview differs:

| Platform | Webview | Path taken | Token-mode video |
|---|---|---|---|
| iOS / iPadOS | WKWebView | native HLS | **broken (401)** |
| macOS | WKWebView | native HLS | **broken (401)** |
| Windows | WebView2 (Chromium) | hls.js + `xhrSetup` | works |
| Linux | WebKitGTK | hls.js (no native HLS) | works |

Cookie-mode clients — every browser tab served by the node itself — are
unaffected throughout.

**The pre-declared failure branch does not apply.** It says to "degrade to
native HLS playback via `<video src=...>`", which is already precisely what the
code does. The problem is not the codec path; it is that the URL carries no
authority.

**The fix already has a precedent in this repo.** D11 solved the identical
problem for audiobooks: a signed ticket naming one item, one user and one
session, spent through `LoadAuthenticatedUser` so revocation still works
(`backend/streamticket.go`). That machinery is audiobook-only —
`handleAudiobookStreamTicket` rejects anything whose
`resolution.Kind != "audiobook"` (`streamticket.go:159`), and `HlsPlayer` has no
ticket path at all. Extending it to video is the natural fix and is scoped in
the handoff, not done here.

---

## Stage B — Tauri v2 custom asset scheme

### PDF worker under the custom scheme — LIKELY PASS, one residual risk

**The plan's pre-declared fallback is already the shipped state.** It says that
if the worker fails to instantiate, bundle `pdf.worker.min.mjs` as an explicit
same-origin static asset instead of constructing `workerSrc` dynamically. Next.js
already does exactly that.

Evidence from the built export (`npm run build`, this branch):

- The worker is emitted as a bundled asset:
  `frontend/out/_next/static/media/pdf.worker.min.43wid0v0nfx-f.mjs`.
- `new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url)`
  (`PdfReader.tsx:92-95`) is resolved **at build time**. The emitted chunk
  contains the literal root-relative path
  `/_next/static/media/pdf.worker.min.43wid0v0nfx-f.mjs` — not a runtime
  `import.meta.url` join.
- `next.config.ts` sets no `assetPrefix` and no `basePath`, and the export
  contains no absolute `http(s)://` origins.

A root-relative path under `tauri://localhost` resolves to
`tauri://localhost/_next/static/media/...`, which is where the file actually is
inside the bundle. The worker therefore loads same-origin, with no cross-scheme
fetch and no network involved.

**Residual risk, still OPEN:** whether WKWebView permits `new Worker()` from a
custom scheme *at all*. That is a webview policy question, not a URL question,
and only hardware answers it. If it is refused, the remaining fallback is
non-worker rendering — pdf.js supports it, at a real cost in main-thread jank on
large documents.

---

## Task 4 — CI GOOS matrix: DONE

`.github/workflows/ci.yml` carries a `check-cross-compile` job building the
operator CLI for `linux` and `windows`. Verified locally beyond the CI matrix:
`linux/amd64`, `windows/amd64` and `darwin/arm64` all build clean.

---

## Task 5 — spike purge and repository state

**There is nothing to purge.** `frontend/src/app/(shell)/spike/` was never
created and no `spike/s0-ios-derisking` branch exists — the spike was not run,
so it left no debris.

**One instruction in the plan is superseded and must not be followed.** Task 5
lists `src-tauri/` among the throwaway files to delete. That was written when
the harness was to be a temporary scaffold. S5 Task 1 made
`frontend/src-tauri/` the real, permanent native shell, containing the Rust TLS
bridge and the secret store. Deleting it would delete the client.

**Inventory gate.** `scripts/verify-clean-checkout.sh --inventory-only` was
failing with exit 10:

```
inventory: frontend/package.json contains floating (^/~) dependency ranges
```

`@tauri-apps/cli` had been added by S5 Task 1 as `^2.11.4` — the only floating
range in the repository, and not present on `main`. Pinned to `2.11.4` (the
already-resolved version; `package-lock.json` synced, one line, no dependency
changed). The gate now passes: exit 0, 23 migrations contiguous.

---

## Stage A gate decision

**The gate cannot be issued as designed, and should not be forced.**

Of the three Stage A criteria: PdfReader passes on static evidence, HlsPlayer
fails on static evidence, and EpubReader is untested. The plan makes the
architecture fallback — Tauri to Electron + Capacitor — conditional on
*EpubReader* failing, and there is no evidence either way.

**Recommendation: do not trigger the architecture fallback.** It is the most
expensive branch in the program, it now means discarding shipped and tested Rust
code, and nothing observed argues for it. The HlsPlayer failure is real but is a
credential bug with a known in-repo fix, not a webview-capability failure, so it
does not bear on the shell choice at all.

**Do not ship a native client to an Apple platform until:**

1. the video credential gap is closed (see the handoff), and
2. the two OPEN device tests have actually been run.

---

## Running the remaining device tests

Requires an iOS 17+ device, a macOS host, and both on one LAN.

**Stage A** — serve the app over HTTPS on the LAN (a secure context is required
for the APIs under test), open it on the device, and attach Safari Web Inspector
over USB (Settings → Safari → Advanced → Web Inspector on the device; Develop
menu on the Mac).

- *EpubReader:* open an EPUB, select text across the rendition. Record whether
  `selected` fires with non-empty text, and any `SecurityError` in the console.
- *HlsPlayer:* already failed statically — confirm the 401 on the native branch
  rather than re-deriving it, and re-test after the credential fix lands.

**Stage B** — build the real shell rather than a throwaway harness; it already
exists:

```bash
cd frontend
npm run tauri -- ios init
npm run tauri:build:ios
```

See [`documentation/operations/building-native-clients.md`](../../../documentation/operations/building-native-clients.md)
for toolchains and for the `TELOS_ALLOWED_ORIGINS` requirement, which will
otherwise 403 every write from the device and look like an unrelated failure.

- *PDF worker:* open a PDF in the shell. Record whether the worker instantiates,
  and capture any `worker.onerror`.

Update this document in place with what is observed.
