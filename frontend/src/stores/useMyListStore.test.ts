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

import { api, ApiError } from "@/lib/api";
import { useMyListStore, type MediaListEntry } from "./useMyListStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function entry(itemId: string, position: number, available = true): MediaListEntry {
  return { itemId, title: "T-" + itemId, mediaType: "Movie", position, available };
}

beforeEach(() => {
  apiMock.mockReset();
  useMyListStore.setState({ entries: [], nextCursor: null, listRevision: 0, status: "idle", error: null, conflict: false });
});

describe("useMyListStore", () => {
  it("load populates ordered entries and revision", async () => {
    apiMock.mockResolvedValue({ items: [entry("a", 1), entry("b", 2)], listRevision: 3 });
    await useMyListStore.getState().load();
    const s = useMyListStore.getState();
    expect(s.entries.map((e) => e.itemId)).toEqual(["a", "b"]);
    expect(s.listRevision).toBe(3);
  });

  it("remove is optimistic and rolls back on failure", async () => {
    useMyListStore.setState({ entries: [entry("a", 1)] });
    apiMock.mockRejectedValue(new Error("boom"));
    await expect(useMyListStore.getState().remove("a")).rejects.toBeTruthy();
    expect(useMyListStore.getState().entries).toHaveLength(1);
  });

  it("a 409 on reorder rolls back, flags a conflict, and reloads from page one", async () => {
    useMyListStore.setState({ entries: [entry("a", 1), entry("b", 2)], listRevision: 5 });
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (init?.method === "PUT") throw new ApiError(409, "list_changed");
      // The reload after conflict returns the authoritative order.
      return { items: [entry("a", 1), entry("b", 2)], listRevision: 6 };
    });
    await expect(useMyListStore.getState().moveBefore("b", "a")).rejects.toBeTruthy();
    const s = useMyListStore.getState();
    expect(s.conflict).toBe(true);
    expect(s.listRevision).toBe(6); // reloaded
    expect(s.entries.map((e) => e.itemId)).toEqual(["a", "b"]); // rolled back to server truth
  });

  it("reorder optimistically moves an item before another", async () => {
    useMyListStore.setState({ entries: [entry("a", 1), entry("b", 2), entry("c", 3)], listRevision: 1 });
    apiMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (init?.method === "PUT") return { listRevision: 2 };
      return { items: [entry("c", 1), entry("a", 2), entry("b", 3)], listRevision: 2 };
    });
    await useMyListStore.getState().moveBefore("c", "a");
    expect(useMyListStore.getState().entries.map((e) => e.itemId)).toEqual(["c", "a", "b"]);
  });
});
