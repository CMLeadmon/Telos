# Linux desktop beta (unsigned AppImage)

The first native client is an **unsigned, invite-only Linux x86-64 AppImage** built on
Ubuntu. Windows and macOS applications are future clients and have no build, signing or
smoke requirement here. The package is signed by nobody: public signing and notarization
are outside the beta.

## Status

| Item | Status |
|---|---|
| Rust shell (OS credential bridge, no TLS probe), auth lifecycle, minted media URLs | Implemented; unit and integration tested |
| AppImage build and artifact verifier | Built on an Ubuntu 26.04.1 VM and verified without executing it; `.github/workflows/desktop.yml` builds it on `ubuntu-24.04` |
| Runtime on a real desktop (Secret Service persistence, restart, WebKitGTK playback) | See *Manual smoke*; **not yet recorded** against a frozen candidate |

CI success proves the package builds and has the expected shape. It does not prove a working
Secret Service, codec stack or WebKitGTK media element; only the smoke below does.

## Prerequisites on the reference desktop

- Ubuntu 24.04 or newer, x86-64, a graphical session.
- A **Secret Service** on the session D-Bus with an **unlocked** default collection (GNOME
  Keyring on a normal Ubuntu desktop). Without it sign-in is refused; the client never
  falls back to memory or a file.
- AppImage support: `libfuse2t64` (`sudo apt install libfuse2t64`), or run the file with
  `--appimage-extract-and-run`.
- GStreamer plugins able to play the fixtures' codecs (H.264/AAC video, MP3/AAC audio):
  `sudo apt install gstreamer1.0-plugins-good gstreamer1.0-plugins-bad gstreamer1.0-libav`.
- WebKitGTK 4.1 (installed with the AppImage's dependencies on Ubuntu 24.04+).
- The node's certificate must be trusted by this computer: a public CA, or the operator's
  private CA installed in the operating-system trust store. There is no override.

## Build (CI and local)

```bash
sudo apt-get install -y build-essential curl wget file pkg-config libwebkit2gtk-4.1-dev \
  libxdo-dev libssl-dev librsvg2-dev libayatana-appindicator3-dev squashfs-tools xdg-utils patchelf
npm --prefix frontend ci
APPIMAGE_EXTRACT_AND_RUN=1 NO_STRIP=1 npm --prefix frontend run tauri -- build --bundles appimage
```

`xdg-utils` is required: the AppImage bundles `xdg-open`, and the bundler fails without it.
The artifact lands in `frontend/src-tauri/target/release/bundle/appimage/`. Write and verify
its identity (the sidecar is written beside the artifact and never embedded in it):

```bash
python3 scripts/write-artifact-identity.py --artifact "$IMAGE" --source-commit "$(git rev-parse HEAD)" \
  --version 0.1.0-beta.1 --run-id 1 --run-attempt 1 --out "$IMAGE.identity.json"
python3 scripts/verify-desktop-artifact.py --platform linux --artifact "$(dirname "$IMAGE")" --identity "$IMAGE.identity.json"
```

The verifier lists the SquashFS payload with `unsquashfs`; it never runs the AppImage. It
rejects any extension but `.AppImage`, a second artifact, updater or signing metadata, a
bundled `telos-secrets.json`, a checksum, size or version mismatch, and a missing sidecar.

## Install (testers)

1. Download the `.AppImage` **and** its `.identity.json` from the invite.
2. Verify the checksum: `sha256sum Telos_*.AppImage` must equal `sha256` in the identity file.
3. `chmod +x Telos_*.AppImage && ./Telos_*.AppImage`.

Because the package is unsigned there is no publisher identity to check: the checksum from
the invite is the only assurance, so obtain it over a channel you trust. Your desktop may warn
that the file is from an unidentified source; confirm only if the checksum matched.

- **Uninstall:** delete the AppImage; remove app data under `~/.local/share/com.telos.app`
  and the sign-in from your keyring (Seahorse → *Passwords* → entries named
  `com.telos.app/…`, or `secret-tool clear service com.telos.app/<hash> account <user>/<device>`).
- **Revoke a device:** Settings → Devices in the hosted browser removes the device; its
  refresh token and every media URL it minted stop working at their next request.
- **Previous builds:** an earlier build kept the refresh token in `telos-secrets.json`.
  The new build deletes that file unread at launch and asks you to sign in again; remove your
  old devices in the hosted browser.

## Manual smoke (`desktop-linux`)

Run against the exact AppImage and the node under test, with `scripts/record-guided-smoke.py --matrix desktop --platform linux-appimage` (its eleven questions are this list; see [browser-certification.md](browser-certification.md)). Record the OS build, WebKitGTK and
GStreamer versions, the AppImage digest, the node and candidate identity, start and end time,
and each assertion's result. A skipped assertion makes the platform `not_run`.

1. Install the exact artifact (checksum matches).
2. Connect to the trusted HTTPS node (no certificate prompt appears; an untrusted one is refused).
3. Sign in; the device appears in Settings → Devices in the hosted browser.
4. Chat, Stream, Library and Files open; **covers and avatars render**; an ebook reads.
5. Play and seek HLS video, a direct audio item, and a multi-track audiobook in WebKitGTK
   (record whether native HLS or hls.js played).
6. Create and read commentary.
7. Quit fully; relaunch; you are signed in without typing the password.
8. Leave the app running past the access-token lifetime (15 minutes) with chat and playback
   active; nothing drops and no second device appears.
9. Revoke the device from the browser: the next request, media segment and socket fail.
10. Sign in again, sign out, quit, relaunch: no credential remains in the keyring.

## Automated rehearsal

`scripts/rehearse-desktop.sh` (used on the Ubuntu 26.04 reference VM) drives items 2–3 and
7–10 headlessly under Xvfb with a throw-away session bus and unlocked GNOME Keyring, using
the real AppImage and a real node. It is a **rehearsal** (`recordKind: rehearsal`): it never
satisfies the `desktop-linux` gate, which is re-run on the frozen candidate on a real desktop.
