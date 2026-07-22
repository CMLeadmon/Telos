import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, m: string) {
      super(m);
      this.status = status;
    }
  },
}));
const chatConnect = vi.fn();
const voiceJoin = vi.fn();
vi.mock("./useChatSessionStore", () => ({
  useChatSessionStore: { getState: () => ({ connect: chatConnect }) },
}));
vi.mock("./useVoiceSessionStore", () => ({
  useVoiceSessionStore: { getState: () => ({ join: voiceJoin }) },
}));

import { api } from "@/lib/api";
import { useWatchPartyStore, HOST_HEARTBEAT_MS } from "./useWatchPartyStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

const party = { id: "p1", mediaItemId: "m", textChannelId: "tc", voiceChannelId: "vc", hostId: "h", hostGeneration: 1 };
const state = { version: 1, action: "paused", positionSeconds: 0, playbackRate: 1, mediaItemId: "m", serverTime: "t", leaseExpiresAt: "t" };

beforeEach(() => {
  apiMock.mockReset();
  chatConnect.mockReset();
  voiceJoin.mockReset();
  useWatchPartyStore.setState({ party: null, state: null, isHost: false, detached: false, error: null });
  useWatchPartyStore.getState().stopHostHeartbeat();
});
afterEach(() => {
  useWatchPartyStore.getState().stopHostHeartbeat();
  vi.useRealTimers();
});

describe("useWatchPartyStore", () => {
  it("create starts a 5s host heartbeat", async () => {
    vi.useFakeTimers();
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/watch-parties" && init?.method === "POST") return party;
      if (path.endsWith("/state")) return state;
      return {};
    });
    await useWatchPartyStore.getState().create("m", "tc", "vc");
    expect(useWatchPartyStore.getState().isHost).toBe(true);

    const beats = () => apiMock.mock.calls.filter((c) => String(c[0]).endsWith("/host-lease")).length;
    const initial = beats();
    await vi.advanceTimersByTimeAsync(HOST_HEARTBEAT_MS * 2 + 100);
    expect(beats()).toBe(initial + 2);

    useWatchPartyStore.getState().stopHostHeartbeat();
    const afterStop = beats();
    await vi.advanceTimersByTimeAsync(HOST_HEARTBEAT_MS * 2);
    expect(beats()).toBe(afterStop);
  });

  it("only the host may issue controls, and a stale version reloads state", async () => {
    useWatchPartyStore.setState({ party, state: { ...state, version: 2 }, isHost: false });
    await useWatchPartyStore.getState().control({ action: "pause", positionSeconds: 1 });
    // Not host: no PUT issued.
    expect(apiMock.mock.calls.some((c) => c[1] && (c[1] as RequestInit).method === "PUT")).toBe(false);

    // As host, a stale version rejection triggers a reload.
    useWatchPartyStore.setState({ isHost: true });
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (init?.method === "PUT") {
        const { ApiError } = await import("@/lib/api");
        throw new (ApiError as unknown as new (s: number, m: string) => Error)(409, "stale");
      }
      return { ...state, version: 3 };
    });
    await expect(useWatchPartyStore.getState().control({ action: "pause", positionSeconds: 1 })).rejects.toBeTruthy();
    expect(useWatchPartyStore.getState().state?.version).toBe(3);
  });

  it("claiming an expired lease makes the caller host and restarts heartbeat", async () => {
    vi.useFakeTimers();
    apiMock.mockResolvedValue(state);
    useWatchPartyStore.setState({ party, isHost: false });
    await useWatchPartyStore.getState().claimExpiredHostLease();
    expect(useWatchPartyStore.getState().isHost).toBe(true);
    expect(apiMock.mock.calls.some((c) => String(c[0]).endsWith("/host-lease/claim"))).toBe(true);
  });

  it("linked chat and voice reuse the existing stores", async () => {
    useWatchPartyStore.setState({ party });
    useWatchPartyStore.getState().openLinkedChat();
    await useWatchPartyStore.getState().joinLinkedVoice();
    expect(chatConnect).toHaveBeenCalledWith("tc");
    expect(voiceJoin).toHaveBeenCalledWith("vc");
  });
});
