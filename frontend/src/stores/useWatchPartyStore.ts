import { create } from "zustand";
import { api, ApiError } from "@/lib/api";
import { useChatSessionStore } from "./useChatSessionStore";
import { useVoiceSessionStore } from "./useVoiceSessionStore";
import { isNewerVersion, type WatchPartyState } from "@/lib/playbackSync";

export interface WatchParty {
  id: string;
  mediaItemId: string;
  textChannelId?: string;
  voiceChannelId?: string;
  hostId: string;
  hostGeneration: number;
}

interface WatchPartyStoreState {
  party: WatchParty | null;
  state: WatchPartyState | null;
  isHost: boolean;
  detached: boolean;
  error: string | null;

  create: (mediaItemId: string, textChannelId?: string, voiceChannelId?: string) => Promise<void>;
  loadState: () => Promise<void>;
  control: (input: { action: string; positionSeconds: number; playbackRate?: number; mediaItemId?: string }) => Promise<void>;
  invite: (inviteeId: string) => Promise<void>;
  detach: () => Promise<void>;
  rejoin: () => Promise<void>;
  leave: () => Promise<void>;
  offerSuccessor: (successorId: string) => Promise<void>;
  acceptSuccessorOffer: () => Promise<void>;
  cancelSuccessorOffer: () => Promise<void>;
  completeHostTransfer: () => void;
  claimExpiredHostLease: () => Promise<void>;
  startHostHeartbeat: () => void;
  stopHostHeartbeat: () => void;
  openLinkedChat: () => void;
  joinLinkedVoice: () => Promise<void>;
}

// Module-scoped heartbeat handle so it survives across store calls.
let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
export const HOST_HEARTBEAT_MS = 5000;

function message(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong.";
}

export const useWatchPartyStore = create<WatchPartyStoreState>((set, get) => ({
  party: null,
  state: null,
  isHost: false,
  detached: false,
  error: null,

  create: async (mediaItemId, textChannelId, voiceChannelId) => {
    try {
      const party = await api<WatchParty>("/api/v1/watch-parties", {
        method: "POST",
        body: JSON.stringify({ mediaItemId, textChannelId, voiceChannelId }),
      });
      set({ party, isHost: true, detached: false, error: null });
      await get().loadState();
      get().startHostHeartbeat();
    } catch (e) {
      set({ error: message(e) });
      throw e;
    }
  },

  loadState: async () => {
    const p = get().party;
    if (!p) return;
    try {
      const next = await api<WatchPartyState>(`/api/v1/watch-parties/${p.id}/state`);
      // Ignore stale versions (out-of-order delivery).
      const cur = get().state;
      if (!cur || isNewerVersion(cur.version, next.version) || next.version === cur.version) {
        set({ state: next });
      }
    } catch (e) {
      set({ error: message(e) });
    }
  },

  control: async (input) => {
    const p = get().party;
    const s = get().state;
    if (!p || !s || !get().isHost) return;
    try {
      const next = await api<WatchPartyState>(`/api/v1/watch-parties/${p.id}/state`, {
        method: "PUT",
        body: JSON.stringify({ ...input, expectedVersion: s.version }),
      });
      set({ state: next, error: null });
    } catch (e) {
      // A stale version means someone else advanced state; reload authoritative.
      set({ error: message(e) });
      await get().loadState();
      throw e;
    }
  },

  invite: async (inviteeId) => {
    const p = get().party;
    if (!p) return;
    await api(`/api/v1/watch-parties/${p.id}/invitations`, { method: "POST", body: JSON.stringify({ inviteeId }) });
  },

  detach: async () => {
    const p = get().party;
    if (!p) return;
    await api(`/api/v1/watch-parties/${p.id}/detach`, { method: "POST" });
    set({ detached: true });
  },
  rejoin: async () => {
    const p = get().party;
    if (!p) return;
    await api(`/api/v1/watch-parties/${p.id}/rejoin`, { method: "POST" });
    set({ detached: false });
    await get().loadState();
  },
  leave: async () => {
    const p = get().party;
    if (!p) return;
    get().stopHostHeartbeat();
    await api(`/api/v1/watch-parties/${p.id}/leave`, { method: "POST" });
    set({ party: null, state: null, isHost: false, detached: false });
  },

  offerSuccessor: async (successorId) => {
    const p = get().party;
    if (!p || !get().isHost) return;
    await api(`/api/v1/watch-parties/${p.id}/host-transfer/offer`, { method: "POST", body: JSON.stringify({ successorId }) });
  },
  acceptSuccessorOffer: async () => {
    const p = get().party;
    if (!p) return;
    await api(`/api/v1/watch-parties/${p.id}/host-transfer/accept`, { method: "POST" });
  },
  cancelSuccessorOffer: async () => {
    const p = get().party;
    if (!p || !get().isHost) return;
    await api(`/api/v1/watch-parties/${p.id}/host-transfer/cancel`, { method: "POST" });
  },
  // Completing a transfer = the host stops heartbeating so the accepted
  // successor can claim the expired lease. There is no general "claim host".
  completeHostTransfer: () => {
    get().stopHostHeartbeat();
    set({ isHost: false });
  },
  claimExpiredHostLease: async () => {
    const p = get().party;
    if (!p) return;
    await api(`/api/v1/watch-parties/${p.id}/host-lease/claim`, { method: "POST" });
    set({ isHost: true });
    get().startHostHeartbeat();
    await get().loadState();
  },

  startHostHeartbeat: () => {
    const p = get().party;
    if (!p || heartbeatTimer) return;
    const beat = () => {
      void api(`/api/v1/watch-parties/${p.id}/host-lease`, { method: "PUT" }).catch(() => {});
    };
    beat();
    heartbeatTimer = setInterval(beat, HOST_HEARTBEAT_MS);
  },
  stopHostHeartbeat: () => {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
  },

  // Linked communication reuses the existing chat/voice stacks — no new one.
  openLinkedChat: () => {
    const ch = get().party?.textChannelId;
    if (ch) useChatSessionStore.getState().connect(ch);
  },
  joinLinkedVoice: async () => {
    const ch = get().party?.voiceChannelId;
    if (ch) await useVoiceSessionStore.getState().join(ch);
  },
}));
