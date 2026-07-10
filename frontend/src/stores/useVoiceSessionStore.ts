import { create } from 'zustand';
import { ConnectionState, Participant, Room, RoomEvent } from 'livekit-client';

interface VoiceSessionState {
  room: Room | null;
  activeChannelId: string | null;
  connectionStatus: ConnectionState;
  remoteParticipants: Participant[];
  isMuted: boolean;

  joinVoiceRoom: (gatewayUrl: string, userJwt: string, targetChannel: string) => Promise<void>;
  terminateVoiceSession: () => Promise<void>;
  toggleMicrophone: () => Promise<void>;
}

export const useVoiceSessionStore = create<VoiceSessionState>((set, get) => ({
  room: null,
  activeChannelId: null,
  connectionStatus: ConnectionState.Disconnected,
  remoteParticipants: [],
  isMuted: false,

  joinVoiceRoom: async (gatewayUrl, userJwt, targetChannel) => {
    // Tear down any existing call before joining a new one.
    if (get().room) {
      await get().room?.disconnect();
    }

    const room = new Room({
      adaptiveStream: true,
      dynacast: true,
      audioCaptureDefaults: {
        echoCancellation: true,
        noiseSuppression: true,
        autoGainControl: true,
      },
    });

    const syncParticipants = () =>
      set({ remoteParticipants: Array.from(room.remoteParticipants.values()) });

    room
      .on(RoomEvent.ConnectionStateChanged, (status) => set({ connectionStatus: status }))
      .on(RoomEvent.ParticipantConnected, syncParticipants)
      .on(RoomEvent.ParticipantDisconnected, syncParticipants)
      .on(RoomEvent.Disconnected, () =>
        set({
          room: null,
          activeChannelId: null,
          connectionStatus: ConnectionState.Disconnected,
          remoteParticipants: [],
        }),
      );

    set({ connectionStatus: ConnectionState.Connecting });
    try {
      await room.connect(gatewayUrl, userJwt);
      await room.localParticipant.setMicrophoneEnabled(true);
      set({
        room,
        activeChannelId: targetChannel,
        connectionStatus: ConnectionState.Connected,
        remoteParticipants: Array.from(room.remoteParticipants.values()),
        isMuted: false,
      });
    } catch (error) {
      set({ connectionStatus: ConnectionState.Disconnected });
      throw error;
    }
  },

  terminateVoiceSession: async () => {
    await get().room?.disconnect();
  },

  toggleMicrophone: async () => {
    const { room, isMuted } = get();
    if (!room) return;
    await room.localParticipant.setMicrophoneEnabled(isMuted);
    set({ isMuted: !isMuted });
  },
}));
