# Voice Chat Controls — Design

Date: 2026-07-12
Status: Approved

## Problem

The voice feature can mint tokens and (in theory) join a LiveKit room, but in practice:

- Joining never reaches the `connected` state in dev, and the failure is silent —
  `useVoiceSessionStore.join()` sets `status: "error"` but no UI renders it, so the
  sidebar button just falls back to "Join".
- Nothing ever publishes microphone audio (`setMicEnabled` exists but is never called).
- There is no microphone/output device selection, no mic test, no mute/deafen, no
  volume control, and no visible leave affordance once connected.

## Root cause of the join failure (hypothesis — confirm by repro before fixing)

In dev, `livekitUrl()` (frontend/src/lib/api.ts) falls back to `ws://<host>:7880`, but
`docker-compose.yml` does not publish port 7880 to the host (only 7881/tcp and the UDP
media ranges; 7880 is reachable only through Traefik at `/livekit`). `room.connect()`
therefore fails and the error is swallowed.

## Scope

In: fix join + surface errors, voice dock (mute/deafen/leave/status/participants with
speaking indicator), device selection, mic test (level meter + loopback), input gain,
output volume, noise-suppression/echo-cancellation/AGC toggles, server-persisted prefs.

Out: push-to-talk, per-participant volume, LiveKit round-trip echo test.

## Design

### 1. Join fix + error surfacing

- Publish `7880:7880` on the `livekit` service in `docker-compose.yml` so the dev
  fallback URL works. Production (`wss://host/livekit` via Traefik) is unchanged.
- The voice dock renders the `error` state with the message and retry/dismiss.
- Mic permission denial maps to a readable message ("Microphone access denied — check
  browser permissions") rather than a raw DOMException.
- `join()` enables the microphone after connecting, respecting persisted mute state.

### 2. Store — `useVoiceSessionStore`

New state:
- `micMuted: boolean`, `deafened: boolean`
- `participants: { identity: string; speaking: boolean }[]` (was `string[]`), driven by
  `RoomEvent.ActiveSpeakersChanged` plus the existing connect/disconnect events.

New actions:
- `toggleMute()` — `localParticipant.setMicrophoneEnabled(...)`.
- `toggleDeafen()` — Discord semantics: volume 0 on all remote participants AND mic
  muted; undeafen restores the mute state held before deafening.
- `setInputDevice(id)` / `setOutputDevice(id)` — `room.switchActiveDevice(...)`,
  usable mid-call.
- `setOutputVolume(v)` — `RemoteParticipant.setVolume(v)` on all remotes, re-applied
  to late joiners in the `ParticipantConnected` handler.

Input gain: LiveKit has no native mic gain, so the local audio track is built from a
WebAudio pipeline — `getUserMedia` (with the suppression constraints) → `GainNode` →
`MediaStreamAudioDestinationNode` → published as the LocalAudioTrack.

`join()` reads all voice prefs from `usePreferencesStore` and applies them; the voice
store also subscribes so mid-call changes from Settings take effect live.

### 3. Preferences — frontend + backend

Extend `Prefs` (frontend/src/stores/usePreferencesStore.ts) and the backend whitelist
(`validatePreferences` in backend/settings.go) with:

| Key | Type | Default | Validation |
|---|---|---|---|
| `voiceInputDeviceId` | string | "" | any string (device IDs are opaque) |
| `voiceOutputDeviceId` | string | "" | any string |
| `voiceInputGain` | number | 1 | 0–2 |
| `voiceOutputVolume` | number | 1 | 0–1 |
| `voiceNoiseSuppression` | bool | true | — |
| `voiceEchoCancellation` | bool | true | — |
| `voiceAutoGainControl` | bool | true | — |

Device IDs are per-machine: if a stored ID is absent on the current machine, fall back
to the system default silently.

### 4. UI — `VoiceDock.tsx`

Rendered in `AppShell` between the channel scroll area and `.railfoot`, only when
`status !== "idle"`. Contents: channel name + status line, participant list with a
speaking indicator, and mute / deafen / leave icon buttons. The mic button opens a
small popover for quick input-device switching mid-call. Error state shows the message
with retry/dismiss. Styles follow the existing stylesheet idiom (new `voice.css`
following the `settings.css` precedent).

### 5. UI — `VoiceAudioSection.tsx` (Settings)

New settings section (existing section pattern): input/output device dropdowns
(`enumerateDevices`, requesting mic permission on demand so labels populate), input
gain and output volume sliders, the three suppression toggles, and the mic test —
a live level meter (AnalyserNode) plus a loopback "check" button routing the mic to
the selected output via `audio.setSinkId`. Test logic lives in a standalone
`useMicTest` hook, independent of LiveKit.

### 6. Testing

- Backend: table-driven cases for each new pref key (type + range) in
  `settings_test.go`, run in the golang 1.22 container.
- Frontend: Playwright e2e with fake media devices
  (`--use-fake-device-for-media-stream`, `--use-fake-ui-for-media-stream`):
  settings section renders devices and the meter reacts; the dock appears on join
  with working mute/deafen/leave. If LiveKit is unreachable the join test asserts the
  error is surfaced (which is itself a requirement).
- TDD: failing test first for store logic and backend validation.
