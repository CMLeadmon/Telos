# Building Telos Native Clients

How to compile the downloadable Telos client for desktop and mobile. The client
is a Tauri v2 shell (`frontend/src-tauri/`) wrapping the **same** static export
the Go gateway serves — there is no second frontend. `frontend/src-tauri/README.md`
covers *why* the shell exists and what its two `window` bridges do; this document
covers producing binaries.

A native client is not a node. It ships no server, no database and no media: it
connects to a Telos node the member names on the `/connect` screen.

## Prerequisites

| | Required |
|---|---|
| **All hosts** | Node.js 20+, npm 10+, Rust stable (Tauri v2 needs **1.77.2+**; use current stable) |
| **Linux** | `build-essential`, `curl`, `wget`, `file`, `libssl-dev`, `libgtk-3-dev`, `libayatana-appindicator3-dev`, `librsvg2-dev`, and **`libwebkit2gtk-4.1-dev`** |
| **macOS / iOS** | Xcode 15+ with Command Line Tools (`xcode-select --install`); an Apple Developer account for anything beyond the simulator |
| **Windows** | Visual Studio 2022 C++ Build Tools, the Windows 10/11 SDK, and WebView2 (preinstalled on Windows 11) |
| **Android** | Android Studio, SDK Platform 33+, NDK 26+, JDK 17, with `ANDROID_HOME` and `NDK_HOME` exported |

`libwebkit2gtk-4.1-dev` is the version that matters — Tauri v1 used `4.0`, and
the `4.0` package satisfies neither the build nor a useful error message.

## Before the first build: let the node accept this client

Every state-changing request is checked against `TELOS_ALLOWED_ORIGINS` by
`requireTrustedOrigin` in `backend/security.go`. A Tauri webview's origin is
`tauri://localhost`, except on Windows where it is `http://tauri.localhost`. A
node whose `.env` lists neither answers `403 origin_forbidden` to device
registration, token refresh, and every write the client attempts — the client
reaches the node, reads fine, and cannot do anything.

Set both on any node meant to serve native clients, so one `.env` works for
every platform:

```bash
TELOS_ALLOWED_ORIGINS=https://telos.example.com,tauri://localhost,http://tauri.localhost
```

Restart `telos-core` after changing it.

## Desktop

```bash
cd frontend
npm ci
npm run tauri:build:desktop        # or: npm run tauri:build
```

`tauri.conf.json` sets `beforeBuildCommand` to `npm run build`, so the Next.js
static export into `frontend/out/` runs as part of this — you do not run it
separately. Bundles land in `frontend/src-tauri/target/release/bundle/`
(`.deb`/`.rpm`/`.AppImage`, `.dmg`/`.app`, `.msi`/`.exe` by host).

`bundle.targets` is `"all"`, which means *all targets valid for the host*.
Tauri does not cross-compile between desktop OSes: a Windows installer is built
on Windows, a `.dmg` on macOS. This is unlike the `telos` operator CLI, which is
Go and does cross-compile — see the `check-cross-compile` job in
`.github/workflows/ci.yml`.

## Mobile

Each target is initialized once per checkout, then built:

```bash
cd frontend

# iOS — macOS host required
npm run tauri -- ios init
npm run tauri:build:ios

# Android
npm run tauri -- android init
npm run tauri:build:android
```

`init` generates `frontend/src-tauri/gen/`, which is **not** committed
(`.gitignore`) because `tauri-build` regenerates it deterministically from
`tauri.conf.json` and `capabilities/`. Re-run `init` after a clean clone.

Android outputs land under
`frontend/src-tauri/gen/android/app/build/outputs/`; iOS builds produce an
`.ipa` through Xcode's archive.

## Distribution

- **Desktop** — bundles are unsigned. Signing and notarization (Apple) or
  Authenticode (Windows) are release-time steps, not build steps; see
  [`release.md`](./release.md).
- **iOS** — TestFlight, or direct `.ipa` sideloading. There is no unsigned
  install path on a stock device.
- **Android** — the signed APK/AAB from the outputs directory above.

## What CI does and does not check

`.github/workflows/ci.yml` builds the frontend, runs its unit tests, and
cross-compiles the Go CLI for `linux` and `windows`. **It does not compile the
Tauri shell.** Nothing automated proves `frontend/src-tauri/` still builds, on
any platform, so a Rust-side break surfaces only when someone runs the commands
above. Run a desktop build by hand before cutting a release that claims a native
client.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `failed to run custom build command for tauri-build` | `frontend/out/` is missing — run `npm run build`, or let `beforeBuildCommand` do it. |
| `403 origin_forbidden` on sign-in or any write | The node's `TELOS_ALLOWED_ORIGINS` is missing the Tauri origins above. |
| Certificate pinning reports `unsupported` | The `__TELOS_NATIVE_TLS__` bridge is absent — a web build, or a shell built without `src/tls.rs`. Expected in a browser tab. |
| Client re-registers a device on every launch | The `__TELOS_NATIVE_STORE__` bridge is absent, so secrets fall back to an in-memory map. |
| `webkit2gtk-4.0` errors on Linux | The `4.1` `-dev` package is the one Tauri v2 needs. |
