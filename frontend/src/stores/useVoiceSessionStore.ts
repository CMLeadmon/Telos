import { create } from "zustand";
import { Room, RoomEvent } from "livekit-client";
import { api, livekitUrl } from "@/lib/api";

interface VoiceSessionState {
  room: Room | null;
  channelId: string | null;
  status: "idle" | "connecting" | "connected" | "error";
  participants: string[];
  error: string | null;
  join: (channelId: string) => Promise<void>;
  leave: () => Promise<void>;
  setMicEnabled: (enabled: boolean) => Promise<void>;
}

export const useVoiceSessionStore = create<VoiceSessionState>()((set, get) => ({
  room: null,
  channelId: null,
  status: "idle",
  participants: [],
  error: null,

  join: async (channelId) => {
    await get().leave();
    set({ status: "connecting", channelId, error: null });
    try {
      const { token } = await api<{ token: string }>(
        `/api/v1/voice/channels/${channelId}/token`,
        { method: "POST" },
      );

      const room = new Room();
      const syncParticipants = () =>
        set({
          participants: [
            room.localParticipant.identity,
            ...Array.from(room.remoteParticipants.values()).map((p) => p.identity),
          ],
        });

      room.on(RoomEvent.ParticipantConnected, syncParticipants);
      room.on(RoomEvent.ParticipantDisconnected, syncParticipants);
      room.on(RoomEvent.Disconnected, () =>
        set({ room: null, channelId: null, status: "idle", participants: [] }),
      );

      await room.connect(livekitUrl(), token);
      set({ room, status: "connected" });
      syncParticipants();
    } catch (err) {
      set({
        room: null,
        status: "error",
        error: err instanceof Error ? err.message : "voice connection failed",
      });
    }
  },

  leave: async () => {
    const { room } = get();
    if (room) {
      await room.disconnect();
    }
    set({ room: null, channelId: null, status: "idle", participants: [] });
  },

  setMicEnabled: async (enabled) => {
    const { room } = get();
    if (room) {
      await room.localParticipant.setMicrophoneEnabled(enabled);
    }
  },
}));
