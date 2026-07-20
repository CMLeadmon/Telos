# Frontend Architecture

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

## 1. Principle: State Separation

Real-time state lives strictly outside the component tree. If WebRTC call state or connection state were coupled to component lifecycles, navigating routes within the application shell would unmount the active components, dropping the call. Telos utilizes global Zustand stores defined at module scope to hold all real-time connection, room, and participant state. This ensures voice sessions persist across all module switches.

---

## 2. Voice Session Store

The following store handles room connections and participant synchronization using the `livekit-client` v2 API. The room state is reset when a `Disconnected` event is received.

```typescript
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
    await get().room?.disconnect();

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
    // State reset happens in the RoomEvent.Disconnected handler.
    await get().room?.disconnect();
  },

  toggleMicrophone: async () => {
    const { room, isMuted } = get();
    if (!room) return;
    await room.localParticipant.setMicrophoneEnabled(isMuted);
    set({ isMuted: !isMuted });
  },
}));
```

---

## 3. Persistent App Shell

The application shell mounts the voice call bar at the shell level. This guarantees that navigation does not interrupt the active voice session.

```tsx
import React from 'react';
import { ConnectionState } from 'livekit-client';
import { Mic, MicOff, PhoneOff } from 'lucide-react';
import { useVoiceSessionStore } from '../stores/useVoiceSessionStore';
import { SidebarNavigation } from './SidebarNavigation';
import { SubModuleRenderer } from './SubModuleRenderer';

export const CoreAppShell: React.FC = () => {
  const { connectionStatus, activeChannelId, isMuted, toggleMicrophone, terminateVoiceSession } =
    useVoiceSessionStore();

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-slate-950 font-sans text-slate-50 antialiased">
      <SidebarNavigation />

      <main className="relative flex min-w-0 flex-1 flex-col bg-slate-900">
        <SubModuleRenderer />
      </main>

      {/* Floating call bar: mounted at shell level so navigation never drops the call. */}
      {connectionStatus === ConnectionState.Connected && activeChannelId && (
        <div className="absolute bottom-6 right-6 z-50 flex items-center gap-4 rounded-xl border border-sky-500/20 bg-slate-950 p-4">
          <div className="flex flex-col gap-0.5">
            <span className="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-sky-400">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-sky-500 motion-reduce:animate-none" />
              Voice Active
            </span>
            <span className="font-mono text-xs text-slate-400">{activeChannelId}</span>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={toggleMicrophone}
              aria-label={isMuted ? 'Unmute microphone' : 'Mute microphone'}
              className={`rounded-lg p-2 transition-colors ${
                isMuted
                  ? 'bg-slate-50 text-slate-900'
                  : 'bg-slate-800 text-slate-200 hover:bg-slate-700'
              }`}
            >
              {isMuted ? <MicOff className="h-4 w-4" /> : <Mic className="h-4 w-4" />}
            </button>
            <button
              onClick={terminateVoiceSession}
              aria-label="Leave voice channel"
              className="flex items-center gap-1.5 rounded-lg bg-slate-50 px-3 py-2 text-xs font-semibold text-slate-900 transition-colors hover:bg-white"
            >
              <PhoneOff className="h-4 w-4" />
              Leave
            </button>
          </div>
        </div>
      )}
    </div>
  );
};
```

---

## 4. Module Rendering

The `SubModuleRenderer` component swaps page views (Chat, Stream, Books, Files) based on the active module navigation. While the page-level DOM is disposable, the application shell, stores, and active media connections persist.

*(informative)* The recommended frontend stack is React, TypeScript, and Zustand, with HLS.js powering the media streaming player. The structure and hooks may be adapted to other host frameworks (such as Next.js, Vue, or Nuxt) provided the overall architecture and DOM behavior conform to this specification.

---

## 5. Theming State

Theme selection (`synthwave`, the default, or `ink`) is persisted in a local storage store managed via Zustand. The theme is applied as a `data-theme` attribute on the root element. Components must consume the CSS variables exposed under the selected theme rather than conditionally branching styles within components.
