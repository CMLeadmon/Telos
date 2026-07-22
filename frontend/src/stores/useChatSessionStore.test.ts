import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {},
  wsBase: () => "ws://localhost",
}));

import { api } from "@/lib/api";
import { useChatSessionStore, type ChatMessage } from "./useChatSessionStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function msg(id: string, content = "hi"): ChatMessage {
  return { id, sender: "u", avatar: "UU", role: "Member", content, timestamp: `2026-07-20T00:00:0${id}Z` };
}

beforeEach(() => {
  apiMock.mockReset();
  useChatSessionStore.setState({
    activeChannelId: "chan-1",
    activeThreadRoot: null,
    threadReplies: [],
    threadNextCursor: null,
    threadStatus: "idle",
    threadDraftError: null,
    pendingReplyMutationId: null,
  });
});

describe("useChatSessionStore threads", () => {
  it("openThread loads replies and keeps the root visible", async () => {
    apiMock.mockResolvedValue({ items: [msg("1"), msg("2")], nextCursor: "c1" });
    await useChatSessionStore.getState().openThread(msg("9", "root"));
    const s = useChatSessionStore.getState();
    expect(s.activeThreadRoot?.id).toBe("9");
    expect(s.threadReplies).toHaveLength(2);
    expect(s.threadNextCursor).toBe("c1");
    expect(s.threadStatus).toBe("ready");
  });

  it("sendReply reuses one mutation id across retries and clears it on ack", async () => {
    useChatSessionStore.setState({ activeThreadRoot: msg("9", "root") });

    // First attempt fails: draft + mutation id are retained.
    apiMock.mockRejectedValueOnce(new Error("boom"));
    await expect(useChatSessionStore.getState().sendReply("my reply")).rejects.toBeTruthy();
    const mid = useChatSessionStore.getState().pendingReplyMutationId;
    expect(mid).toBeTruthy();
    expect(useChatSessionStore.getState().threadDraftError).toBeTruthy();

    // Retry: same mutation id is sent, and on success it is cleared.
    let sentMutationId = "";
    apiMock.mockImplementationOnce(async (_path: string, init?: RequestInit) => {
      sentMutationId = JSON.parse(String(init?.body)).clientMutationId;
      return { message: msg("1", "my reply") };
    });
    await useChatSessionStore.getState().sendReply("my reply");
    expect(sentMutationId).toBe(mid);
    expect(useChatSessionStore.getState().pendingReplyMutationId).toBeNull();
    expect(useChatSessionStore.getState().threadReplies).toHaveLength(1);
  });

  it("a duplicate reply merges by id exactly once", async () => {
    useChatSessionStore.setState({ activeThreadRoot: msg("9", "root"), threadReplies: [msg("1", "dup")] });
    apiMock.mockResolvedValue({ message: msg("1", "dup") });
    await useChatSessionStore.getState().sendReply("dup");
    expect(useChatSessionStore.getState().threadReplies).toHaveLength(1);
  });

  it("closeThread resets thread state", () => {
    useChatSessionStore.setState({ activeThreadRoot: msg("9"), threadReplies: [msg("1")] });
    useChatSessionStore.getState().closeThread();
    const s = useChatSessionStore.getState();
    expect(s.activeThreadRoot).toBeNull();
    expect(s.threadReplies).toHaveLength(0);
    expect(s.threadStatus).toBe("idle");
  });
});
