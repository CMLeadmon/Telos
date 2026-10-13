# Telos native shell

The Tauri v2 shell that wraps the static export in `frontend/out/` as a desktop
and mobile client.

## What the shell exists to do

The frontend is one static export, shared by the hosted browser client served by
the `telos-client` container and by this client. It never imports Tauri modules:
it feature-detects the bridges this shell installs on `window`.

| Bridge | Contract lives in | Without it |
|---|---|---|
| `__TELOS_NATIVE_CREDENTIALS__` | `frontend/src/lib/secureStorage.ts` | In a native shell the credential operations **reject** and the sign-in fails visibly; there is no in-memory or file fallback. In a browser tab the bridge is simply absent and cookie authentication is used. |
| `__TELOS_NATIVE_SHELL__` | `frontend/src/lib/platform.ts` | External links open in a browser tab and copy uses the web clipboard API. |

A renamed global would turn a feature off, so `src/lib.rs` and
`frontend/src/lib/nativeShellContract.test.ts` pin the exact strings in both
directions.

## Credentials live in the operating system

The device refresh token is stored in the Linux Secret Service (for example GNOME
Keyring) through the `keyring` crate, pinned at 4.1.6 with the `v1` feature. The
page names a structured key (node origin, account id, device id) and the shell
derives the keychain strings itself, so the page cannot address another
application's secrets or another node's credentials. A missing, locked or denied
Secret Service is a visible error. Access tokens stay in memory only.

The earlier shell kept the refresh token in a plaintext `telos-secrets.json`. At
launch the shell deletes that file **unread** (and refuses a symlink or other
non-file), and the next sign-in tells the member to remove their old devices in
the hosted browser. Deletion is not secure erasure and does not revoke anything
remotely.

## Trust is the platform's

The client connects over HTTPS only and the operating system decides whether the
node's certificate is trusted. There is no fingerprint probe, no first-use
confirmation, no pin store, no accept-any verifier and no HTTP production path;
an operator-installed private CA works because the platform trusts it. A
debug-only exception allows plain HTTP to `localhost`, `127.0.0.1` and `[::1]`;
a release build cannot enable it.

## Before a real build

Operator-facing build instructions — toolchains per platform, mobile targets,
where bundles land — are in
[`documentation/operations/building-native-clients.md`](../../documentation/operations/building-native-clients.md).
Two things worth knowing here:

- **Icons are committed**, under `icons/`, and `tauri.conf.json` lists them.
  Regenerate with `npm run tauri icon <source.png>` when the mark changes; the
  build fails loudly if they are ever removed.
- **The node must allow this client's origin.** Every state-changing request is
  checked against `TELOS_ALLOWED_ORIGINS` by `requireTrustedOrigin` in
  `backend/security.go`, and a Tauri webview's origin is `tauri://localhost`
  (`http://tauri.localhost` on Windows). The beta configuration is exactly that
  pair (`.env.example` ships it and `telos start` validates it). Without them,
  device registration, token refresh and every write return
  `403 origin_forbidden`.
