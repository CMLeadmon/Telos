# Voice Chat Controls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the silent voice-join failure and add mute/deafen/leave controls (voice dock), mic/output device selection, volumes, suppression toggles, and a mic test.

**Architecture:** Extend `useVoiceSessionStore` with call controls backed by native `livekit-client` APIs; mic audio flows through a WebAudio gain pipeline (`lib/voiceAudio.ts`) published as a custom LiveKit track. Voice preferences persist server-side through the existing `usePreferencesStore` → `/api/v1/users/me/preferences` path. New UI: `VoiceDock` in the shell sidebar and a `VoiceAudioSection` on the Settings page.

**Tech Stack:** Next.js 16 static export, Zustand, livekit-client 2.x, WebAudio API, Go 1.22 (in podman container), Playwright.

**Spec:** `docs/superpowers/specs/2026-07-12-voice-controls-design.md`

## Global Constraints

- Go is NOT installed on the host. Backend commands run via: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...` (from repo root).
- Frontend has NO unit test runner — verification is `npm run lint`, `npm run build`, and Playwright e2e (dev server must already be running; there is no webServer block).
- Lint rule forbids setState-in-effect patterns; use promise-chain loaders and event-handler-driven state, not effects that set state synchronously.
- No new npm dependencies.
- Never hardcode secrets; all credentials from `.env`.
- Stylesheets: one file per module in `frontend/src/styles/`, imported from `frontend/src/app/layout.tsx`.
- Commit after every task.

## File Structure

- Modify: `backend/settings.go` — whitelist voice preference keys
- Modify: `backend/settings_test.go` — cases for the new keys
- Modify: `docker-compose.yml` — publish LiveKit port 7880
- Modify: `frontend/src/stores/usePreferencesStore.ts` — voice prefs
- Create: `frontend/src/lib/voiceAudio.ts` — mic gain pipeline + error mapping
- Rewrite: `frontend/src/stores/useVoiceSessionStore.ts` — controls, participants, error surfacing
- Create: `frontend/src/components/VoiceDock.tsx` — in-call dock UI
- Create: `frontend/src/styles/voice.css` — dock styles
- Modify: `frontend/src/app/layout.tsx` — import voice.css
- Modify: `frontend/src/components/AppShell.tsx` — render dock
- Create: `frontend/src/hooks/useMicTest.ts` — level meter + loopback
- Create: `frontend/src/components/settings/VoiceAudioSection.tsx` — settings section
- Modify: `frontend/src/app/(shell)/settings/page.tsx` — register section
- Create: `frontend/e2e/voice.spec.ts` — e2e with fake media devices

---

### Task 1: Backend preference whitelist for voice keys

**Files:**
- Modify: `backend/settings.go` (validatePreferences, ~line 49)
- Test: `backend/settings_test.go` (TestValidatePreferences, ~line 68)

**Interfaces:**
- Produces: `validatePreferences` accepts keys `voiceInputDeviceId`, `voiceOutputDeviceId` (strings, ≤256 chars), `voiceInputGain` (number 0–2), `voiceOutputVolume` (number 0–1), `voiceNoiseSuppression`, `voiceEchoCancellation`, `voiceAutoGainControl` (booleans). All other behavior unchanged.

- [ ] **Step 1: Write the failing tests**

In `backend/settings_test.go`, replace the body of `TestValidatePreferences` with:

```go
func TestValidatePreferences(t *testing.T) {
	good := []byte(`{"theme":"ink","sceneEnabled":false,"reducedMotion":true,
		"voiceInputDeviceId":"abc123","voiceOutputDeviceId":"",
		"voiceInputGain":1.5,"voiceOutputVolume":0.8,
		"voiceNoiseSuppression":true,"voiceEchoCancellation":false,
		"voiceAutoGainControl":true}`)
	prefs, err := validatePreferences(good)
	if err != nil {
		t.Fatalf("valid prefs rejected: %v", err)
	}
	if prefs["theme"] != "ink" {
		t.Errorf("theme = %v, want ink", prefs["theme"])
	}
	if prefs["voiceInputGain"] != 1.5 {
		t.Errorf("voiceInputGain = %v, want 1.5", prefs["voiceInputGain"])
	}
	bads := [][]byte{
		[]byte(`{"theme":"neon"}`),
		[]byte(`{"sceneEnabled":"yes"}`),
		[]byte(`{"evil":true}`),
		[]byte(`{`),
		[]byte(`{"theme":"` + strings.Repeat("a", 3000) + `"}`),
		[]byte(`{"voiceInputGain":3}`),
		[]byte(`{"voiceInputGain":-0.1}`),
		[]byte(`{"voiceInputGain":"loud"}`),
		[]byte(`{"voiceOutputVolume":1.1}`),
		[]byte(`{"voiceNoiseSuppression":"on"}`),
		[]byte(`{"voiceInputDeviceId":42}`),
		[]byte(`{"voiceInputDeviceId":"` + strings.Repeat("d", 300) + `"}`),
	}
	for i, b := range bads {
		if _, err := validatePreferences(b); err == nil {
			t.Errorf("case %d: invalid prefs accepted", i)
		}
	}
}
```

Note: the `good` payload contains raw newlines inside a JSON string only in this markdown rendering — keep it a single-line Go raw string or concatenate; JSON itself permits the whitespace shown between members, so the multi-line raw string literal is valid as written.

- [ ] **Step 2: Run tests to verify they fail**

Run (from repo root): `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestValidatePreferences ./...`
Expected: FAIL — `unknown preference key "voiceInputDeviceId"` (or similar) on the good payload.

- [ ] **Step 3: Extend validatePreferences**

In `backend/settings.go`, replace the `switch k` block inside `validatePreferences` with:

```go
	for k, v := range prefs {
		switch k {
		case "theme":
			s, ok := v.(string)
			if !ok || !validThemes[s] {
				return nil, errors.New("invalid theme")
			}
		case "sceneEnabled", "reducedMotion",
			"voiceNoiseSuppression", "voiceEchoCancellation", "voiceAutoGainControl":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
		case "voiceInputDeviceId", "voiceOutputDeviceId":
			s, ok := v.(string)
			if !ok || len(s) > 256 {
				return nil, fmt.Errorf("%s must be a string of at most 256 characters", k)
			}
		case "voiceInputGain":
			f, ok := v.(float64)
			if !ok || f < 0 || f > 2 {
				return nil, errors.New("voiceInputGain must be a number between 0 and 2")
			}
		case "voiceOutputVolume":
			f, ok := v.(float64)
			if !ok || f < 0 || f > 1 {
				return nil, errors.New("voiceOutputVolume must be a number between 0 and 1")
			}
		default:
			return nil, fmt.Errorf("unknown preference key %q", k)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test -run TestValidatePreferences ./...`
Expected: PASS. Then run the full suite + vet:
`podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...`
`podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go vet ./...`
Expected: PASS / no output.

- [ ] **Step 5: Commit**

```bash
git add backend/settings.go backend/settings_test.go
git commit -m "feat(backend): whitelist voice preference keys"
```

---

### Task 2: Frontend voice preferences

**Files:**
- Modify: `frontend/src/stores/usePreferencesStore.ts`

**Interfaces:**
- Produces: `Prefs` gains `voiceInputDeviceId: string`, `voiceOutputDeviceId: string`, `voiceInputGain: number`, `voiceOutputVolume: number`, `voiceNoiseSuppression: boolean`, `voiceEchoCancellation: boolean`, `voiceAutoGainControl: boolean`. Defaults: `""`, `""`, `1`, `1`, `true`, `true`, `true`. `load()`/`save()` unchanged (they already merge arbitrary `Prefs` keys).

- [ ] **Step 1: Extend the Prefs interface and defaults**

In `frontend/src/stores/usePreferencesStore.ts`, replace the `Prefs` interface and `DEFAULTS`:

```ts
export interface Prefs {
  theme: Theme;
  sceneEnabled: boolean;
  reducedMotion: boolean;
  voiceInputDeviceId: string;
  voiceOutputDeviceId: string;
  voiceInputGain: number;
  voiceOutputVolume: number;
  voiceNoiseSuppression: boolean;
  voiceEchoCancellation: boolean;
  voiceAutoGainControl: boolean;
}

const DEFAULTS: Prefs = {
  theme: "synthwave",
  sceneEnabled: true,
  reducedMotion: false,
  voiceInputDeviceId: "",
  voiceOutputDeviceId: "",
  voiceInputGain: 1,
  voiceOutputVolume: 1,
  voiceNoiseSuppression: true,
  voiceEchoCancellation: true,
  voiceAutoGainControl: true,
};
```

No other changes — `load()` spreads remote over defaults, `save()` PUTs the merged object; both already handle the new keys.

- [ ] **Step 2: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/stores/usePreferencesStore.ts
git commit -m "feat(frontend): add voice preference keys with defaults"
```

---

### Task 3: Mic pipeline + error mapping (`lib/voiceAudio.ts`)

**Files:**
- Create: `frontend/src/lib/voiceAudio.ts`

**Interfaces:**
- Produces:
  - `createMicPipeline(opts: MicPipelineOptions): Promise<MicPipeline>` where `MicPipelineOptions = { deviceId: string; gain: number; noiseSuppression: boolean; echoCancellation: boolean; autoGainControl: boolean }` and `MicPipeline = { track: MediaStreamTrack; setGain(v: number): void; stop(): void }`.
  - `voiceErrorMessage(err: unknown): string` — maps DOMExceptions to readable copy.

LiveKit has no native mic input gain, so we build the capture ourselves: `getUserMedia` → `GainNode` → `MediaStreamAudioDestinationNode`, and publish the destination's track. `deviceId` uses the `ideal` constraint so a stale (per-machine) stored ID silently falls back to the system default.

- [ ] **Step 1: Write the module**

Create `frontend/src/lib/voiceAudio.ts`:

```ts
// Mic capture pipeline. LiveKit has no native input gain, so the published
// microphone track is the output of getUserMedia -> GainNode -> destination.

export interface MicPipelineOptions {
  deviceId: string;
  gain: number;
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}

export interface MicPipeline {
  track: MediaStreamTrack;
  setGain: (v: number) => void;
  stop: () => void;
}

export function micConstraints(opts: {
  deviceId: string;
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}): MediaTrackConstraints {
  return {
    // "ideal" so a stale per-machine device ID falls back to the default mic.
    ...(opts.deviceId ? { deviceId: { ideal: opts.deviceId } } : {}),
    noiseSuppression: opts.noiseSuppression,
    echoCancellation: opts.echoCancellation,
    autoGainControl: opts.autoGainControl,
  };
}

export async function createMicPipeline(
  opts: MicPipelineOptions,
): Promise<MicPipeline> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: micConstraints(opts),
  });
  const ctx = new AudioContext();
  await ctx.resume();
  const source = ctx.createMediaStreamSource(stream);
  const gain = ctx.createGain();
  gain.gain.value = opts.gain;
  const dest = ctx.createMediaStreamDestination();
  source.connect(gain);
  gain.connect(dest);
  return {
    track: dest.stream.getAudioTracks()[0],
    setGain: (v) => {
      gain.gain.value = v;
    },
    stop: () => {
      stream.getTracks().forEach((t) => t.stop());
      dest.stream.getTracks().forEach((t) => t.stop());
      void ctx.close();
    },
  };
}

export function voiceErrorMessage(err: unknown): string {
  if (err instanceof DOMException) {
    if (err.name === "NotAllowedError" || err.name === "SecurityError") {
      return "Microphone access denied — check browser permissions.";
    }
    if (err.name === "NotFoundError" || err.name === "OverconstrainedError") {
      return "No microphone found.";
    }
  }
  return err instanceof Error ? err.message : "voice connection failed";
}
```

- [ ] **Step 2: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/voiceAudio.ts
git commit -m "feat(frontend): add mic gain pipeline and voice error mapping"
```

---

### Task 4: Voice session store — controls, participants, error surfacing

**Files:**
- Rewrite: `frontend/src/stores/useVoiceSessionStore.ts`

**Interfaces:**
- Consumes: `createMicPipeline`, `micConstraints`, `voiceErrorMessage`, `MicPipeline` from `@/lib/voiceAudio`; `usePreferencesStore` (Task 2); `api`, `livekitUrl` from `@/lib/api`.
- Produces (used by VoiceDock and AppShell):
  - State: `room: Room | null`, `channelId: string | null`, `status: "idle" | "connecting" | "connected" | "error"`, `participants: { identity: string; speaking: boolean }[]`, `error: string | null`, `micMuted: boolean`, `deafened: boolean`.
  - Actions: `join(channelId: string): Promise<void>`, `leave(): Promise<void>`, `toggleMute(): Promise<void>`, `toggleDeafen(): Promise<void>`, `setInputDevice(id: string): Promise<void>`, `setOutputDevice(id: string): Promise<void>`, `dismissError(): void`.
  - `setMicEnabled` is REMOVED (it was never called from UI).

- [ ] **Step 1: Rewrite the store**

Replace `frontend/src/stores/useVoiceSessionStore.ts` with:

```ts
import { create } from "zustand";
import {
  LocalAudioTrack,
  RemoteParticipant,
  Room,
  RoomEvent,
  Track,
} from "livekit-client";
import { api, livekitUrl } from "@/lib/api";
import {
  createMicPipeline,
  voiceErrorMessage,
  type MicPipeline,
} from "@/lib/voiceAudio";
import { usePreferencesStore } from "@/stores/usePreferencesStore";

export interface VoiceParticipant {
  identity: string;
  speaking: boolean;
}

interface VoiceSessionState {
  room: Room | null;
  channelId: string | null;
  status: "idle" | "connecting" | "connected" | "error";
  participants: VoiceParticipant[];
  error: string | null;
  micMuted: boolean;
  deafened: boolean;
  join: (channelId: string) => Promise<void>;
  leave: () => Promise<void>;
  toggleMute: () => Promise<void>;
  toggleDeafen: () => Promise<void>;
  setInputDevice: (id: string) => Promise<void>;
  setOutputDevice: (id: string) => Promise<void>;
  dismissError: () => void;
}

// Module-level call plumbing — not reactive state.
let pipeline: MicPipeline | null = null;
let micTrack: LocalAudioTrack | null = null;
let mutedBeforeDeafen = false;

function stopPipeline() {
  pipeline?.stop();
  pipeline = null;
  micTrack = null;
}

function voicePrefs() {
  const p = usePreferencesStore.getState().prefs;
  return {
    deviceId: p.voiceInputDeviceId,
    gain: p.voiceInputGain,
    noiseSuppression: p.voiceNoiseSuppression,
    echoCancellation: p.voiceEchoCancellation,
    autoGainControl: p.voiceAutoGainControl,
    outputDeviceId: p.voiceOutputDeviceId,
    outputVolume: p.voiceOutputVolume,
  };
}

function applyOutputVolume(room: Room, volume: number) {
  room.remoteParticipants.forEach((p: RemoteParticipant) => p.setVolume(volume));
}

export const useVoiceSessionStore = create<VoiceSessionState>()((set, get) => {
  const syncParticipants = (room: Room, speaking?: Set<string>) => {
    const speakingIds =
      speaking ??
      new Set(room.activeSpeakers.map((p) => p.identity));
    set({
      participants: [
        room.localParticipant,
        ...Array.from(room.remoteParticipants.values()),
      ].map((p) => ({
        identity: p.identity,
        speaking: speakingIds.has(p.identity),
      })),
    });
  };

  // Publish (or republish) the mic pipeline for the current prefs.
  const publishMic = async (room: Room) => {
    const prefs = voicePrefs();
    if (micTrack) {
      await room.localParticipant.unpublishTrack(micTrack);
    }
    stopPipeline();
    pipeline = await createMicPipeline(prefs);
    const pub = await room.localParticipant.publishTrack(pipeline.track, {
      source: Track.Source.Microphone,
    });
    micTrack = pub.track as LocalAudioTrack;
    if (get().micMuted || get().deafened) {
      await micTrack.mute();
    }
  };

  return {
    room: null,
    channelId: null,
    status: "idle",
    participants: [],
    error: null,
    micMuted: false,
    deafened: false,

    join: async (channelId) => {
      await get().leave();
      set({ status: "connecting", channelId, error: null });
      let room: Room | null = null;
      try {
        const { token } = await api<{ token: string }>(
          `/api/v1/voice/channels/${channelId}/token`,
          { method: "POST" },
        );

        room = new Room();
        room.on(RoomEvent.ParticipantConnected, (p: RemoteParticipant) => {
          p.setVolume(get().deafened ? 0 : voicePrefs().outputVolume);
          if (room) syncParticipants(room);
        });
        room.on(RoomEvent.ParticipantDisconnected, () => {
          if (room) syncParticipants(room);
        });
        room.on(RoomEvent.ActiveSpeakersChanged, (speakers) => {
          if (room)
            syncParticipants(room, new Set(speakers.map((s) => s.identity)));
        });
        room.on(RoomEvent.Disconnected, () => {
          stopPipeline();
          set({
            room: null,
            channelId: null,
            status: "idle",
            participants: [],
          });
        });

        await room.connect(livekitUrl(), token);
        await publishMic(room);
        const prefs = voicePrefs();
        if (prefs.outputDeviceId) {
          // Stale per-machine device IDs are expected; fall back silently.
          await room.switchActiveDevice("audiooutput", prefs.outputDeviceId)
            .catch(() => {});
        }
        applyOutputVolume(room, get().deafened ? 0 : prefs.outputVolume);
        set({ room, status: "connected" });
        syncParticipants(room);
      } catch (err) {
        stopPipeline();
        if (room) await room.disconnect().catch(() => {});
        set({
          room: null,
          status: "error",
          error: voiceErrorMessage(err),
        });
      }
    },

    leave: async () => {
      const { room } = get();
      stopPipeline();
      if (room) {
        await room.disconnect();
      }
      set({
        room: null,
        channelId: null,
        status: "idle",
        participants: [],
        error: null,
      });
    },

    toggleMute: async () => {
      const { micMuted, deafened } = get();
      if (deafened) return; // undeafen first; deafen implies mute
      const next = !micMuted;
      set({ micMuted: next });
      if (micTrack) {
        if (next) await micTrack.mute();
        else await micTrack.unmute();
      }
    },

    toggleDeafen: async () => {
      const { room, deafened, micMuted } = get();
      if (!deafened) {
        mutedBeforeDeafen = micMuted;
        set({ deafened: true, micMuted: true });
        if (room) applyOutputVolume(room, 0);
        if (micTrack) await micTrack.mute();
      } else {
        set({ deafened: false, micMuted: mutedBeforeDeafen });
        if (room) applyOutputVolume(room, voicePrefs().outputVolume);
        if (micTrack && !mutedBeforeDeafen) await micTrack.unmute();
      }
    },

    setInputDevice: async (id) => {
      await usePreferencesStore.getState().save({ voiceInputDeviceId: id });
      const { room, status } = get();
      if (room && status === "connected") {
        await publishMic(room);
      }
    },

    setOutputDevice: async (id) => {
      await usePreferencesStore.getState().save({ voiceOutputDeviceId: id });
      const { room, status } = get();
      if (room && status === "connected" && id) {
        await room.switchActiveDevice("audiooutput", id).catch(() => {});
      }
    },

    dismissError: () => set({ status: "idle", channelId: null, error: null }),
  };
});

// Mid-call reactions to Settings changes: gain is live via the GainNode,
// output volume re-applies to remotes, suppression toggles rebuild capture.
usePreferencesStore.subscribe((state, prev) => {
  const cur = state.prefs;
  const old = prev.prefs;
  const { room, status, deafened } = useVoiceSessionStore.getState();
  if (cur.voiceInputGain !== old.voiceInputGain) {
    pipeline?.setGain(cur.voiceInputGain);
  }
  if (
    room &&
    status === "connected" &&
    cur.voiceOutputVolume !== old.voiceOutputVolume &&
    !deafened
  ) {
    applyOutputVolume(room, cur.voiceOutputVolume);
  }
  if (
    room &&
    status === "connected" &&
    (cur.voiceNoiseSuppression !== old.voiceNoiseSuppression ||
      cur.voiceEchoCancellation !== old.voiceEchoCancellation ||
      cur.voiceAutoGainControl !== old.voiceAutoGainControl)
  ) {
    // Republish with new constraints; errors here shouldn't kill the call.
    void (async () => {
      try {
        const prefs = usePreferencesStore.getState().prefs;
        if (micTrack) await room.localParticipant.unpublishTrack(micTrack);
        stopPipeline();
        pipeline = await createMicPipeline({
          deviceId: prefs.voiceInputDeviceId,
          gain: prefs.voiceInputGain,
          noiseSuppression: prefs.voiceNoiseSuppression,
          echoCancellation: prefs.voiceEchoCancellation,
          autoGainControl: prefs.voiceAutoGainControl,
        });
        const pub = await room.localParticipant.publishTrack(pipeline.track, {
          source: Track.Source.Microphone,
        });
        micTrack = pub.track as LocalAudioTrack;
        const s = useVoiceSessionStore.getState();
        if (s.micMuted || s.deafened) await micTrack.mute();
      } catch {
        // keep the call alive on a failed republish
      }
    })();
  }
});
```

Implementation note for the executor: if `publishTrack`'s return type makes `pub.track` possibly `undefined` in this livekit-client version, guard with `if (pub.track)` before assigning. If the exact event-callback types differ, match the signatures in `node_modules/livekit-client/dist/src/room/Room.d.ts` rather than adding `any`.

- [ ] **Step 2: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors. The build will fail on `setMicEnabled` if anything still references it — grep to confirm nothing does: `grep -rn "setMicEnabled" frontend/src` → no matches expected.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/stores/useVoiceSessionStore.ts
git commit -m "feat(frontend): voice store controls — mute, deafen, devices, volume, errors"
```

---

### Task 5: Publish LiveKit port 7880 for the dev fallback URL

**Files:**
- Modify: `docker-compose.yml` (livekit service `ports`, ~line 213)

**Interfaces:**
- Produces: `ws://<host>:7880` (the `livekitUrl()` dev fallback in `frontend/src/lib/api.ts:29`) becomes reachable from the host.

- [ ] **Step 1: Reproduce the failure (if the stack is running)**

Run: `curl -s -o /dev/null -w "%{http_code}" --max-time 3 http://localhost:7880 ; echo`
Expected BEFORE the fix: `000` (connection refused). If the stack is not running, note that and proceed — the port mapping is verifiably absent from the compose file either way.

- [ ] **Step 2: Add the port mapping**

In `docker-compose.yml`, in the `livekit` service, change:

```yaml
    ports:
      - "7881:7881"
      - "3478:3478/udp"
      - "50000-50100:50000-50100/udp"
```

to:

```yaml
    ports:
      - "7880:7880" # LiveKit websocket — dev fallback URL (frontend livekitUrl())
      - "7881:7881"
      - "3478:3478/udp"
      - "50000-50100:50000-50100/udp"
```

- [ ] **Step 3: Verify (if the stack is running)**

Run: `podman-compose up -d livekit` then `curl -s -o /dev/null -w "%{http_code}" --max-time 3 http://localhost:7880 ; echo`
Expected AFTER the fix: `200` or `404` (any HTTP response proves the listener is reachable).

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "fix(compose): publish livekit 7880 so the dev websocket fallback connects"
```

---

### Task 6: VoiceDock UI + styles + AppShell integration

**Files:**
- Create: `frontend/src/components/VoiceDock.tsx`
- Create: `frontend/src/styles/voice.css`
- Modify: `frontend/src/app/layout.tsx` (add css import)
- Modify: `frontend/src/components/AppShell.tsx` (render dock above `.railfoot`)

**Interfaces:**
- Consumes: the full Task 4 store surface (`status`, `channelId`, `participants`, `error`, `micMuted`, `deafened`, `toggleMute`, `toggleDeafen`, `leave`, `join`, `dismissError`, `setInputDevice`); `Room.getLocalDevices` from livekit-client; `useChatSessionStore` channels (for the channel name).
- Produces: `<VoiceDock />` — renders nothing when `status === "idle"`; test IDs `voice-dock`, `voice-dock-error`; aria-labels `mute microphone` / `unmute microphone`, `deafen` / `undeafen`, `leave voice`, `voice input device`.

- [ ] **Step 1: Write the component**

Create `frontend/src/components/VoiceDock.tsx`:

```tsx
"use client";

import { useState } from "react";
import {
  Headphones,
  HeadphoneOff,
  Mic,
  MicOff,
  PhoneOff,
  Settings2,
} from "lucide-react";
import { Room } from "livekit-client";
import { useVoiceSessionStore } from "@/stores/useVoiceSessionStore";
import { useChatSessionStore } from "@/stores/useChatSessionStore";
import { usePreferencesStore } from "@/stores/usePreferencesStore";

export function VoiceDock() {
  const voice = useVoiceSessionStore();
  const channels = useChatSessionStore((s) => s.channels);
  const inputDeviceId = usePreferencesStore((s) => s.prefs.voiceInputDeviceId);
  const [devicesOpen, setDevicesOpen] = useState(false);
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);

  if (voice.status === "idle") return null;

  const channelName =
    channels.find((c) => c.id === voice.channelId)?.name ?? voice.channelId;

  if (voice.status === "error") {
    return (
      <div className="voicedock voicedock--error" data-testid="voice-dock">
        <div className="vderr" data-testid="voice-dock-error">
          {voice.error}
        </div>
        <div className="vdrow">
          {voice.channelId && (
            <button
              className="vdbtn"
              onClick={() => void voice.join(voice.channelId!)}
            >
              Retry
            </button>
          )}
          <button className="vdbtn" onClick={voice.dismissError}>
            Dismiss
          </button>
        </div>
      </div>
    );
  }

  const openDevices = () => {
    const next = !devicesOpen;
    setDevicesOpen(next);
    if (next) {
      Room.getLocalDevices("audioinput", true)
        .then(setDevices)
        .catch(() => setDevices([]));
    }
  };

  return (
    <div className="voicedock" data-testid="voice-dock">
      <div className="vdhead">
        <span className={`vddot${voice.status === "connected" ? " ok" : ""}`} />
        <span className="vdchan">{channelName}</span>
        <span className="vdstatus">
          {voice.status === "connected" ? "voice connected" : "connecting…"}
        </span>
      </div>
      {voice.participants.length > 0 && (
        <ul className="vdppl">
          {voice.participants.map((p) => (
            <li key={p.identity} className={p.speaking ? "speaking" : ""}>
              {p.identity}
            </li>
          ))}
        </ul>
      )}
      {devicesOpen && (
        <select
          className="vddevices"
          aria-label="voice input device"
          value={inputDeviceId}
          onChange={(e) => {
            void voice.setInputDevice(e.target.value);
            setDevicesOpen(false);
          }}
        >
          <option value="">Default microphone</option>
          {devices.map((d) => (
            <option key={d.deviceId} value={d.deviceId}>
              {d.label || d.deviceId.slice(0, 12)}
            </option>
          ))}
        </select>
      )}
      <div className="vdrow">
        <button
          className={`vdbtn${voice.micMuted ? " off" : ""}`}
          aria-label={voice.micMuted ? "unmute microphone" : "mute microphone"}
          onClick={() => void voice.toggleMute()}
        >
          {voice.micMuted ? <MicOff size={16} /> : <Mic size={16} />}
        </button>
        <button
          className={`vdbtn${voice.deafened ? " off" : ""}`}
          aria-label={voice.deafened ? "undeafen" : "deafen"}
          onClick={() => void voice.toggleDeafen()}
        >
          {voice.deafened ? (
            <HeadphoneOff size={16} />
          ) : (
            <Headphones size={16} />
          )}
        </button>
        <button
          className="vdbtn"
          aria-label="voice devices"
          onClick={openDevices}
        >
          <Settings2 size={16} />
        </button>
        <button
          className="vdbtn danger"
          aria-label="leave voice"
          onClick={() => void voice.leave()}
        >
          <PhoneOff size={16} />
        </button>
      </div>
    </div>
  );
}
```

Note: if `HeadphoneOff` doesn't exist in this lucide-react version, use `VolumeX` instead — check with `grep -o "HeadphoneOff" frontend/node_modules/lucide-react/dist/lucide-react.d.ts | head -1`.

- [ ] **Step 2: Write the stylesheet**

Create `frontend/src/styles/voice.css`:

```css
/* Voice dock — pinned above the rail footer while in a call. */
.voicedock {
  margin: 8px 10px;
  padding: 10px;
  border: 1px solid var(--line, rgba(255, 255, 255, 0.12));
  border-radius: 10px;
  background: var(--panel, rgba(0, 0, 0, 0.25));
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 12px;
}
.voicedock--error {
  border-color: var(--danger, #ff5577);
}
.vderr {
  color: var(--danger, #ff5577);
  line-height: 1.4;
}
.vdhead {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.vddot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--muted, #888);
  flex: none;
}
.vddot.ok {
  background: var(--ok, #2fd67b);
}
.vdchan {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.vdstatus {
  margin-left: auto;
  opacity: 0.65;
  flex: none;
}
.vdppl {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.vdppl li {
  opacity: 0.8;
  padding-left: 12px;
  position: relative;
}
.vdppl li::before {
  content: "";
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: transparent;
  border: 1px solid var(--muted, #888);
}
.vdppl li.speaking::before {
  background: var(--ok, #2fd67b);
  border-color: var(--ok, #2fd67b);
}
.vddevices {
  width: 100%;
}
.vdrow {
  display: flex;
  gap: 6px;
}
.vdbtn {
  flex: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 6px;
  border: 1px solid var(--line, rgba(255, 255, 255, 0.12));
  border-radius: 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.vdbtn.off {
  color: var(--danger, #ff5577);
  border-color: var(--danger, #ff5577);
}
.vdbtn.danger:hover {
  color: var(--danger, #ff5577);
  border-color: var(--danger, #ff5577);
}
```

Style note for the executor: check `frontend/src/styles/foundations/shell.css` and `settings.css` for the project's actual CSS custom property names (`--line`, `--panel`, etc.) and use those tokens; the fallbacks above are placeholders for whatever the design system defines.

- [ ] **Step 3: Import the stylesheet**

In `frontend/src/app/layout.tsx`, after the `settings.css` import (line 8), add:

```ts
import "@/styles/voice.css";
```

- [ ] **Step 4: Render the dock in AppShell**

In `frontend/src/components/AppShell.tsx`:

Add the import:

```ts
import { VoiceDock } from "@/components/VoiceDock";
```

Then insert `<VoiceDock />` between the scroll area and the rail footer — change:

```tsx
          </div>
          <div className="railfoot">
```

to:

```tsx
          </div>
          <VoiceDock />
          <div className="railfoot">
```

- [ ] **Step 5: Verify lint, build, and render**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors.
With `npm run dev` + gateway running: join a voice channel; the dock appears with mute/deafen/devices/leave, and the channel row shows "Leave". If LiveKit is unreachable the dock shows the error with Retry/Dismiss instead of failing silently.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/VoiceDock.tsx frontend/src/styles/voice.css frontend/src/app/layout.tsx frontend/src/components/AppShell.tsx
git commit -m "feat(frontend): voice dock with mute, deafen, device switch, and leave"
```

---

### Task 7: Mic test hook (`useMicTest`)

**Files:**
- Create: `frontend/src/hooks/useMicTest.ts` (new `hooks/` directory)

**Interfaces:**
- Consumes: `micConstraints` from `@/lib/voiceAudio` (Task 3).
- Produces: `useMicTest(): { level: number; running: boolean; loopback: boolean; error: string | null; start(deviceId: string, opts: { noiseSuppression: boolean; echoCancellation: boolean; autoGainControl: boolean }): Promise<void>; stop(): void; setLoopback(on: boolean, outputDeviceId: string): Promise<void> }` — `level` is 0–1 RMS of the mic input, updated via requestAnimationFrame while running. Fully independent of LiveKit.

- [ ] **Step 1: Write the hook**

Create `frontend/src/hooks/useMicTest.ts`:

```ts
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { micConstraints, voiceErrorMessage } from "@/lib/voiceAudio";

interface MicTestOptions {
  noiseSuppression: boolean;
  echoCancellation: boolean;
  autoGainControl: boolean;
}

// Sink selection is not yet in the HTMLMediaElement lib type.
type SinkableAudio = HTMLAudioElement & {
  setSinkId?: (id: string) => Promise<void>;
};

export function useMicTest() {
  const [level, setLevel] = useState(0);
  const [running, setRunning] = useState(false);
  const [loopback, setLoopbackState] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const streamRef = useRef<MediaStream | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);
  const rafRef = useRef(0);
  const audioRef = useRef<SinkableAudio | null>(null);

  const stop = useCallback(() => {
    cancelAnimationFrame(rafRef.current);
    streamRef.current?.getTracks().forEach((t) => t.stop());
    streamRef.current = null;
    if (audioRef.current) {
      audioRef.current.pause();
      audioRef.current.srcObject = null;
      audioRef.current = null;
    }
    void ctxRef.current?.close();
    ctxRef.current = null;
    setRunning(false);
    setLoopbackState(false);
    setLevel(0);
  }, []);

  useEffect(() => stop, [stop]); // teardown on unmount

  const start = useCallback(
    async (deviceId: string, opts: MicTestOptions) => {
      stop();
      setError(null);
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          audio: micConstraints({ deviceId, ...opts }),
        });
        streamRef.current = stream;
        const ctx = new AudioContext();
        ctxRef.current = ctx;
        await ctx.resume();
        const analyser = ctx.createAnalyser();
        analyser.fftSize = 512;
        ctx.createMediaStreamSource(stream).connect(analyser);
        const buf = new Uint8Array(analyser.fftSize);
        const tick = () => {
          analyser.getByteTimeDomainData(buf);
          let sum = 0;
          for (let i = 0; i < buf.length; i++) {
            const v = (buf[i] - 128) / 128;
            sum += v * v;
          }
          setLevel(Math.min(1, Math.sqrt(sum / buf.length) * 3));
          rafRef.current = requestAnimationFrame(tick);
        };
        rafRef.current = requestAnimationFrame(tick);
        setRunning(true);
      } catch (err) {
        setError(voiceErrorMessage(err));
      }
    },
    [stop],
  );

  const setLoopback = useCallback(
    async (on: boolean, outputDeviceId: string) => {
      if (!on) {
        if (audioRef.current) {
          audioRef.current.pause();
          audioRef.current.srcObject = null;
          audioRef.current = null;
        }
        setLoopbackState(false);
        return;
      }
      if (!streamRef.current) return;
      const audio = new Audio() as SinkableAudio;
      audio.srcObject = streamRef.current;
      if (outputDeviceId && audio.setSinkId) {
        await audio.setSinkId(outputDeviceId).catch(() => {});
      }
      await audio.play().catch(() => {});
      audioRef.current = audio;
      setLoopbackState(true);
    },
    [],
  );

  return { level, running, loopback, error, start, stop, setLoopback };
}
```

- [ ] **Step 2: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors (the hook is not imported anywhere yet; that's fine for the build, but if lint flags an unused export rule, proceed — Task 8 consumes it).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/hooks/useMicTest.ts
git commit -m "feat(frontend): mic test hook — level meter and loopback"
```

---

### Task 8: Voice & Audio settings section

**Files:**
- Create: `frontend/src/components/settings/VoiceAudioSection.tsx`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`

**Interfaces:**
- Consumes: `usePreferencesStore` (Task 2), `useVoiceSessionStore.setInputDevice/setOutputDevice` (Task 4), `useMicTest` (Task 7), `Room.getLocalDevices` from livekit-client.
- Produces: `VoiceAudioSection` registered in the settings nav as `{ id: "voice", label: "Voice & Audio", icon: Mic, admin: false }`. Test IDs: `voice-devices-enable`, `voice-input-select`, `voice-output-select`, `voice-mic-meter`; aria-labels `input device`, `output device`, `input gain`, `output volume`, `noise suppression`, `echo cancellation`, `auto gain control`, `mic test`, `loopback test`.

- [ ] **Step 1: Write the section component**

Create `frontend/src/components/settings/VoiceAudioSection.tsx`:

```tsx
"use client";

import { useState } from "react";
import { Room } from "livekit-client";
import { usePreferencesStore } from "@/stores/usePreferencesStore";
import { useVoiceSessionStore } from "@/stores/useVoiceSessionStore";
import { useMicTest } from "@/hooks/useMicTest";

export function VoiceAudioSection() {
  const prefs = usePreferencesStore((s) => s.prefs);
  const save = usePreferencesStore((s) => s.save);
  const setInputDevice = useVoiceSessionStore((s) => s.setInputDevice);
  const setOutputDevice = useVoiceSessionStore((s) => s.setOutputDevice);
  const mic = useMicTest();

  const [inputs, setInputs] = useState<MediaDeviceInfo[]>([]);
  const [outputs, setOutputs] = useState<MediaDeviceInfo[]>([]);
  const [devicesLoaded, setDevicesLoaded] = useState(false);
  const [deviceError, setDeviceError] = useState<string | null>(null);

  // Device labels are only exposed after a granted getUserMedia, so listing
  // is behind an explicit button instead of running on mount.
  const loadDevices = () => {
    Promise.all([
      Room.getLocalDevices("audioinput", true),
      Room.getLocalDevices("audiooutput", false),
    ])
      .then(([ins, outs]) => {
        setInputs(ins);
        setOutputs(outs);
        setDevicesLoaded(true);
        setDeviceError(null);
      })
      .catch(() =>
        setDeviceError(
          "Microphone access denied — check browser permissions.",
        ),
      );
  };

  const testOpts = {
    noiseSuppression: prefs.voiceNoiseSuppression,
    echoCancellation: prefs.voiceEchoCancellation,
    autoGainControl: prefs.voiceAutoGainControl,
  };

  return (
    <>
      <h2>Voice &amp; Audio</h2>
      <div className="setcard">
        {!devicesLoaded ? (
          <div className="setrow">
            <div className="lbl">
              <b>Audio devices</b>
              <span>
                Grant microphone access to list your input and output devices.
              </span>
            </div>
            <button
              className="setbtn"
              data-testid="voice-devices-enable"
              onClick={loadDevices}
            >
              Enable microphone access
            </button>
          </div>
        ) : (
          <>
            <div className="setrow">
              <div className="lbl">
                <b>Input device</b>
                <span>Which microphone Telos captures.</span>
              </div>
              <select
                data-testid="voice-input-select"
                aria-label="input device"
                value={prefs.voiceInputDeviceId}
                onChange={(e) => void setInputDevice(e.target.value)}
              >
                <option value="">Default</option>
                {inputs.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || d.deviceId.slice(0, 12)}
                  </option>
                ))}
              </select>
            </div>
            <hr className="hr" />
            <div className="setrow">
              <div className="lbl">
                <b>Output device</b>
                <span>Where voice audio plays.</span>
              </div>
              <select
                data-testid="voice-output-select"
                aria-label="output device"
                value={prefs.voiceOutputDeviceId}
                onChange={(e) => void setOutputDevice(e.target.value)}
              >
                <option value="">Default</option>
                {outputs.map((d) => (
                  <option key={d.deviceId} value={d.deviceId}>
                    {d.label || d.deviceId.slice(0, 12)}
                  </option>
                ))}
              </select>
            </div>
          </>
        )}
        {deviceError && <p className="seterr">{deviceError}</p>}
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Input gain</b>
            <span>Microphone boost. Applies live while in a call.</span>
          </div>
          <input
            type="range"
            aria-label="input gain"
            min={0}
            max={2}
            step={0.05}
            value={prefs.voiceInputGain}
            onChange={(e) =>
              void save({ voiceInputGain: Number(e.target.value) })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Output volume</b>
            <span>Volume of everyone else in the call.</span>
          </div>
          <input
            type="range"
            aria-label="output volume"
            min={0}
            max={1}
            step={0.05}
            value={prefs.voiceOutputVolume}
            onChange={(e) =>
              void save({ voiceOutputVolume: Number(e.target.value) })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Noise suppression</b>
            <span>Filter background noise from your microphone.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="noise suppression"
            checked={prefs.voiceNoiseSuppression}
            onChange={(e) =>
              void save({ voiceNoiseSuppression: e.target.checked })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Echo cancellation</b>
            <span>Prevent your speakers from feeding back into the mic.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="echo cancellation"
            checked={prefs.voiceEchoCancellation}
            onChange={(e) =>
              void save({ voiceEchoCancellation: e.target.checked })
            }
          />
        </div>
        <hr className="hr" />
        <div className="setrow">
          <div className="lbl">
            <b>Auto gain control</b>
            <span>Let the browser normalize your mic level.</span>
          </div>
          <input
            type="checkbox"
            className="setswitch"
            aria-label="auto gain control"
            checked={prefs.voiceAutoGainControl}
            onChange={(e) =>
              void save({ voiceAutoGainControl: e.target.checked })
            }
          />
        </div>
      </div>

      <h2>Mic test</h2>
      <div className="setcard">
        <div className="setrow">
          <div className="lbl">
            <b>Level</b>
            <span>Speak — the bar should move.</span>
          </div>
          <button
            className="setbtn"
            aria-label="mic test"
            onClick={() =>
              mic.running
                ? mic.stop()
                : void mic.start(prefs.voiceInputDeviceId, testOpts)
            }
          >
            {mic.running ? "Stop test" : "Start test"}
          </button>
        </div>
        <div
          className="micmeter"
          data-testid="voice-mic-meter"
          data-level={mic.level.toFixed(2)}
        >
          <div
            className="micmeter-fill"
            style={{ width: `${Math.round(mic.level * 100)}%` }}
          />
        </div>
        {mic.running && (
          <div className="setrow">
            <div className="lbl">
              <b>Loopback</b>
              <span>Hear your own mic through the selected output.</span>
            </div>
            <input
              type="checkbox"
              className="setswitch"
              aria-label="loopback test"
              checked={mic.loopback}
              onChange={(e) =>
                void mic.setLoopback(
                  e.target.checked,
                  prefs.voiceOutputDeviceId,
                )
              }
            />
          </div>
        )}
        {mic.error && <p className="seterr">{mic.error}</p>}
      </div>
    </>
  );
}
```

Executor notes: reuse the existing settings classnames (`setcard`, `setrow`, `lbl`, `hr`, `setswitch`); check `frontend/src/styles/settings.css` for the button (`setbtn`) and error (`seterr`) classes the other sections actually use and match them. Add the two `micmeter` rules to `frontend/src/styles/voice.css`:

```css
.micmeter {
  height: 8px;
  border-radius: 4px;
  background: var(--line, rgba(255, 255, 255, 0.12));
  overflow: hidden;
  margin: 8px 0;
}
.micmeter-fill {
  height: 100%;
  background: var(--ok, #2fd67b);
  transition: width 60ms linear;
}
```

- [ ] **Step 2: Register the section**

In `frontend/src/app/(shell)/settings/page.tsx`:

Add `Mic` to the lucide import and the section import:

```ts
import { KeyRound, Mail, Mic, Palette, Shield, User, Users } from "lucide-react";
import { VoiceAudioSection } from "@/components/settings/VoiceAudioSection";
```

In `SECTIONS`, after the appearance entry, add:

```ts
  { id: "voice", label: "Voice & Audio", icon: Mic, admin: false, C: VoiceAudioSection },
```

- [ ] **Step 3: Verify lint and build**

Run (from `frontend/`): `npm run lint && npm run build`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/settings/VoiceAudioSection.tsx frontend/src/app/\(shell\)/settings/page.tsx frontend/src/styles/voice.css
git commit -m "feat(frontend): Voice & Audio settings — devices, volumes, suppression, mic test"
```

---

### Task 9: Playwright e2e with fake media devices

**Files:**
- Create: `frontend/e2e/voice.spec.ts`

**Interfaces:**
- Consumes: test IDs/aria-labels from Tasks 6 and 8; the credentialed-login pattern from `frontend/e2e/files.spec.ts` (env `E2E_USERNAME`/`E2E_PASSWORD`, skip when unset).

- [ ] **Step 1: Write the spec**

Create `frontend/e2e/voice.spec.ts`:

```ts
import { test, expect, type Page } from "@playwright/test";

// Credentialed run against a live gateway:
//   E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test voice
const USERNAME = process.env.E2E_USERNAME;
const PASSWORD = process.env.E2E_PASSWORD;

test.skip(!USERNAME || !PASSWORD, "E2E_USERNAME/E2E_PASSWORD not set");

// Fake devices: mic emits a tone, permission prompts auto-accept.
test.use({
  launchOptions: {
    args: [
      "--use-fake-device-for-media-stream",
      "--use-fake-ui-for-media-stream",
    ],
  },
});

async function login(page: Page) {
  await page.goto("/login/");
  await page.locator("#username").fill(USERNAME!);
  await page.locator("#password").fill(PASSWORD!);
  await page.getByRole("button", { name: "Enter the node" }).click();
  await page.waitForURL("**/chat/**");
}

test("voice settings list devices and the mic meter reacts", async ({
  page,
}) => {
  await login(page);
  await page.goto("/settings/");
  await page.getByRole("button", { name: "Voice & Audio" }).click();

  await page.getByTestId("voice-devices-enable").click();
  await expect(page.getByTestId("voice-input-select")).toBeVisible();
  // Chromium's fake mic enumerates as "Fake Audio Input 1".
  await expect(page.getByTestId("voice-input-select")).toContainText(
    /Fake|Default/,
  );

  await page.getByRole("button", { name: "mic test" }).click();
  // The fake device plays a tone, so the RMS level must rise above zero.
  await expect
    .poll(
      async () =>
        Number(
          await page
            .getByTestId("voice-mic-meter")
            .getAttribute("data-level"),
        ),
      { timeout: 5_000 },
    )
    .toBeGreaterThan(0);
});

test("joining a voice channel shows the dock (or a surfaced error)", async ({
  page,
}) => {
  await login(page);
  const voiceChannel = page
    .locator(".chan", { has: page.locator(".joinlbl") })
    .first();
  test.skip(!(await voiceChannel.count()), "no voice channels provisioned");

  await voiceChannel.click();
  // The dock must appear for connecting, connected, AND error states —
  // silent failure is the bug this feature fixes.
  const dock = page.getByTestId("voice-dock");
  await expect(dock).toBeVisible({ timeout: 15_000 });

  if (await page.getByTestId("voice-dock-error").isVisible()) {
    await expect(page.getByTestId("voice-dock-error")).not.toBeEmpty();
    return; // LiveKit not reachable in this environment; error surfacing verified
  }

  // Connected: exercise mute, deafen, leave.
  await page.getByLabel("mute microphone").click();
  await expect(page.getByLabel("unmute microphone")).toBeVisible();
  await page.getByLabel("deafen").click();
  await expect(page.getByLabel("undeafen")).toBeVisible();
  await page.getByLabel("undeafen").click();
  await page.getByLabel("leave voice").click();
  await expect(dock).toBeHidden();
});
```

- [ ] **Step 2: Run the suite**

With `npm run dev` running and the gateway up (see memory note: mint credentials via the invite flow if needed):

Run (from `frontend/`): `E2E_USERNAME=<user> E2E_PASSWORD=<pass> npx playwright test voice`
Expected: both tests PASS (the join test passes via either the connected path or the surfaced-error path). Also run the full suite to check for regressions: `npx playwright test`.

- [ ] **Step 3: Commit**

```bash
git add frontend/e2e/voice.spec.ts
git commit -m "test(frontend): e2e for voice settings and dock with fake media devices"
```

---

## Final verification (after all tasks)

1. Backend: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...` and `... go vet ./...` — PASS.
2. Frontend: `npm run lint && npm run build` — clean.
3. Full stack: `podman-compose up -d --build`, then join a voice channel from two browsers — audio flows, speaking indicators track, mute/deafen/leave work, `curl http://localhost:8080/api/v1/health` OK.
4. `podman logs telos-core` — no unexpected fallback warnings from the preferences endpoints.
