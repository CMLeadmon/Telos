import { create } from "zustand";
import {
  LocalAudioTrack,
  RemoteParticipant,
  Room,
  RoomEvent,
  Track,
  LogLevel,
  setLogExtension,
} from "livekit-client";
import { api, livekitUrl } from "@/lib/api";
import {
  createMicPipeline,
  currentVoiceCaptureEnvironmentError,
  voiceErrorMessage,
  type MicPipeline,
} from "@/lib/voiceAudio";
import { usePreferencesStore } from "@/stores/usePreferencesStore";

// Silence harmless LiveKit data channel teardown errors
setLogExtension((level, msg, context) => {
  if (
    msg.toLowerCase().includes("closed unexpectedly") &&
    (msg.toLowerCase().includes("data channel") || msg.toLowerCase().includes("data_track"))
  ) {
    return;
  }
  const logMap = {
    [LogLevel.trace]: console.trace,
    [LogLevel.debug]: console.debug,
    [LogLevel.info]: console.info,
    [LogLevel.warn]: console.warn,
    [LogLevel.error]: console.error,
    [LogLevel.silent]: () => {},
  };
  const logger = logMap[level] || console.log;
  if (context !== undefined) {
    logger(`[LiveKit] ${msg}`, context);
  } else {
    logger(`[LiveKit] ${msg}`);
  }
});


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
      speaking ?? new Set(room.activeSpeakers.map((p) => p.identity));
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
    micTrack = (pub.track as LocalAudioTrack) ?? null;
    if (micTrack && (get().micMuted || get().deafened)) {
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
      const environmentError = currentVoiceCaptureEnvironmentError();
      if (environmentError) {
        set({
          status: "error",
          channelId,
          error: environmentError,
        });
        return;
      }
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
          await room
            .switchActiveDevice("audiooutput", prefs.outputDeviceId)
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
    room.remoteParticipants.forEach((p) => p.setVolume(cur.voiceOutputVolume));
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
        micTrack = (pub.track as LocalAudioTrack) ?? null;
        const s = useVoiceSessionStore.getState();
        if (micTrack && (s.micMuted || s.deafened)) await micTrack.mute();
      } catch {
        // keep the call alive on a failed republish
      }
    })();
  }
});
