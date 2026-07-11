import { create } from 'zustand';
import { ConnectionState, Participant, Room, RoomEvent } from 'livekit-client';

interface VoiceSessionState {
  room: Room | null;
  activeChannelId: string | null;
  connectionStatus: ConnectionState;
  remoteParticipants: Participant[];
  isMuted: boolean;

  // Microphone device management
  audioInputs: MediaDeviceInfo[];
  selectedAudioInputId: string | null;

  joinVoiceRoom: (gatewayUrl: string, userJwt: string, targetChannel: string) => Promise<void>;
  terminateVoiceSession: () => Promise<void>;
  toggleMicrophone: () => Promise<void>;
  updateAudioInputs: () => Promise<void>;
  setAudioInput: (deviceId: string) => Promise<void>;
}

export const useVoiceSessionStore = create<VoiceSessionState>((set, get) => ({
  room: null,
  activeChannelId: null,
  connectionStatus: ConnectionState.Disconnected,
  remoteParticipants: [],
  isMuted: false,
  audioInputs: [],
  selectedAudioInputId: null,

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

    // Set room + channel immediately so the UI can show a Connecting state
    // with a functional Leave button before the connection completes.
    set({
      room,
      activeChannelId: targetChannel,
      connectionStatus: ConnectionState.Connecting,
    });

    try {
      await room.connect(gatewayUrl, userJwt);
      await room.localParticipant.setMicrophoneEnabled(true);

      // Switch to previously selected mic if the user chose one
      const preferredMic = get().selectedAudioInputId;
      if (preferredMic) {
        await room.switchActiveDevice('audioinput', preferredMic);
      }

      set({
        connectionStatus: ConnectionState.Connected,
        remoteParticipants: Array.from(room.remoteParticipants.values()),
        isMuted: false,
      });
    } catch (error) {
      // Clean up the room so we don't hold a broken reference
      try { await room.disconnect(); } catch { /* already disconnected */ }
      set({
        room: null,
        activeChannelId: null,
        connectionStatus: ConnectionState.Disconnected,
        remoteParticipants: [],
      });
      throw error;
    }
  },

  terminateVoiceSession: async () => {
    const { room } = get();
    if (room) {
      await room.disconnect();
    } else {
      // If room.disconnect never fires (e.g. during Connecting), reset manually
      set({
        room: null,
        activeChannelId: null,
        connectionStatus: ConnectionState.Disconnected,
        remoteParticipants: [],
      });
    }
  },

  toggleMicrophone: async () => {
    const { room, isMuted } = get();
    if (!room) return;
    await room.localParticipant.setMicrophoneEnabled(isMuted);
    set({ isMuted: !isMuted });
  },

  updateAudioInputs: async () => {
    try {
      const devices = await Room.getLocalDevices('audioinput');
      set({ audioInputs: devices });
    } catch (err) {
      console.error('Failed to enumerate audio input devices:', err);
    }
  },

  setAudioInput: async (deviceId: string) => {
    const { room } = get();
    set({ selectedAudioInputId: deviceId });
    if (room) {
      try {
        await room.switchActiveDevice('audioinput', deviceId);
      } catch (err) {
        console.error('Failed to switch audio input device:', err);
      }
    }
  },
}));
