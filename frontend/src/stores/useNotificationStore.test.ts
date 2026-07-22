import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
    }
  },
}));

import { api } from "@/lib/api";
import { useNotificationStore, type AppNotification } from "./useNotificationStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function notif(id: string, seq: number, read = false): AppNotification {
  return {
    id,
    kind: "mention",
    resourceType: "message",
    resourceId: "m" + id,
    payload: {},
    sequence: seq,
    read,
    createdAt: `2026-07-20T00:00:0${seq}Z`,
  };
}

function reset() {
  useNotificationStore.setState({
    items: [],
    nextCursor: null,
    unreadCount: 0,
    lastEventSequence: 0,
    catchUpHighWater: 0,
    status: "idle",
    error: null,
  });
}

beforeEach(() => {
  apiMock.mockReset();
  reset();
});

describe("useNotificationStore", () => {
  it("loadInitial populates items, cursor, unread count, and high sequence", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path.startsWith("/api/v1/notifications/unread-count")) return { count: 2 };
      if (path.startsWith("/api/v1/notifications")) {
        return { items: [notif("a", 5), notif("b", 4)], nextCursor: "cur1" };
      }
      throw new Error("unexpected " + path);
    });

    await useNotificationStore.getState().loadInitial();
    const s = useNotificationStore.getState();
    expect(s.items).toHaveLength(2);
    expect(s.nextCursor).toBe("cur1");
    expect(s.unreadCount).toBe(2);
    expect(s.lastEventSequence).toBe(5);
    expect(s.status).toBe("ready");
  });

  it("loadMore appends the next page and de-dups by id", async () => {
    useNotificationStore.setState({ items: [notif("a", 5)], nextCursor: "cur1" });
    apiMock.mockImplementation(async () => ({
      items: [notif("a", 5), notif("c", 3)],
      nextCursor: undefined,
    }));
    await useNotificationStore.getState().loadMore();
    const s = useNotificationStore.getState();
    expect(s.items.map((n) => n.id).sort()).toEqual(["a", "c"]);
    expect(s.nextCursor).toBeNull();
  });

  it("markRead is optimistic and rolls back on failure", async () => {
    useNotificationStore.setState({ items: [notif("a", 5)], unreadCount: 1 });
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (init?.method === "PUT") throw new Error("boom");
      if (path.startsWith("/api/v1/notifications/unread-count")) return { count: 1 };
      return {};
    });
    await useNotificationStore.getState().markRead("a");
    const s = useNotificationStore.getState();
    // Rolled back: the item is unread again.
    expect(s.items[0].read).toBe(false);
    expect(s.error).toBeTruthy();
  });

  it("markAllRead clears the badge and marks every item read", async () => {
    useNotificationStore.setState({ items: [notif("a", 5), notif("b", 4)], unreadCount: 2 });
    apiMock.mockResolvedValue({});
    await useNotificationStore.getState().markAllRead();
    const s = useNotificationStore.getState();
    expect(s.unreadCount).toBe(0);
    expect(s.items.every((n) => n.read)).toBe(true);
  });

  it("catchUp fills a socket gap across pages exactly once", async () => {
    useNotificationStore.setState({ lastEventSequence: 2 });
    let loadInitialCalls = 0;
    apiMock.mockImplementation(async (path: string) => {
      if (path.startsWith("/api/v1/events")) {
        // Two pages of catch-up up to high-water 7.
        if (path.includes("afterSequence=2")) {
          return { items: [{ sequence: 5 }], nextAfter: 5, highWater: 7, hasMore: true };
        }
        return { items: [{ sequence: 7 }], nextAfter: 7, highWater: 7, hasMore: false };
      }
      if (path.startsWith("/api/v1/notifications/unread-count")) return { count: 1 };
      if (path.startsWith("/api/v1/notifications")) {
        loadInitialCalls++;
        return { items: [notif("g", 7)], nextCursor: undefined };
      }
      throw new Error("unexpected " + path);
    });

    await useNotificationStore.getState().catchUp();
    const s = useNotificationStore.getState();
    expect(s.catchUpHighWater).toBe(7);
    expect(s.lastEventSequence).toBe(7);
    // The inbox refresh (loadInitial) happened exactly once for the gap.
    expect(loadInitialCalls).toBe(1);
  });

  it("applyEvent ignores a duplicate or stale sequence", () => {
    useNotificationStore.setState({ lastEventSequence: 5 });
    const spy = vi.spyOn(useNotificationStore.getState(), "catchUp");
    useNotificationStore.getState().applyEvent({ sequence: 5 });
    useNotificationStore.getState().applyEvent({ sequence: 3 });
    expect(useNotificationStore.getState().lastEventSequence).toBe(5);
    spy.mockRestore();
  });
});
