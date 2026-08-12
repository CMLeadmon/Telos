# Telos native shell

The Tauri v2 shell that wraps the static export in `frontend/out/` as a desktop
and mobile client.

## What the shell exists to do

The frontend is one static export, shared by the web build served from the Go
gateway and by this client. It never imports Tauri modules — it feature-detects
two bridges this shell installs on `window`, and falls back when they are
absent:

| Bridge | Contract lives in | Without it |
|---|---|---|
| `__TELOS_NATIVE_TLS__` | `frontend/src/lib/certPinning.ts` | Certificate pinning reports `unsupported`; the connect screen says so plainly instead of claiming a guarantee it cannot keep. |
| `__TELOS_NATIVE_STORE__` | `frontend/src/lib/secureStorage.ts` | Secrets fall back to an in-memory map: the refresh token is lost on every launch, so the client re-registers a device each time it starts and no certificate pin ever survives to be compared. |

Both fall back **silently**, by design — that is what makes the web build work.
It also means a renamed global disables a security feature with no error
anywhere, so `src/lib.rs` has tests pinning the exact strings.

## Why the TLS bridge is native

Pinning needs the certificate the peer actually presented. No web API exposes
it: `fetch`, `XMLHttpRequest` and `WebSocket` all complete the handshake inside
the webview and hand the page nothing. `src/tls.rs` therefore opens its own TLS
connection, reads the leaf, and reports the SHA-256 over its DER encoding — the
same value `openssl x509 -fingerprint -sha256` prints, so an operator can read
one out and a member can compare it.

That handshake accepts any chain, and the connection carries no request and no
credential. Self-hosted nodes are frequently self-signed or privately issued, so
refusing to look at unverifiable chains would make pinning impossible on exactly
the nodes that most need it. The trust decision is not made in Rust: the
fingerprint goes to the member, who confirms it once, and every later connection
is compared against what they confirmed.

## Before a real build

- **Icons are generated, not committed.** `tauri.conf.json` lists
  `icons/*`, which `npm run tauri icon <source.png>` produces. The build fails
  loudly until they exist, which is preferable to shipping a placeholder.
- **The node must allow this client's origin.** Every state-changing request is
  checked against `TELOS_ALLOWED_ORIGINS` by `requireTrustedOrigin` in
  `backend/security.go`, and a Tauri webview's origin is `tauri://localhost`
  (`http://tauri.localhost` on Windows). Without those in the node's `.env`,
  device registration, token refresh and every write return
  `403 origin_forbidden`.
