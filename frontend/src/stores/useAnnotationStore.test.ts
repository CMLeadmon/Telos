import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

import { api } from "@/lib/api";
import { useAnnotationStore, type Annotation } from "./useAnnotationStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function ann(id: string, visibility: "private" | "community" = "private", note = "n"): Annotation {
  return {
    id,
    targetType: "book",
    targetId: "42",
    ownerId: "o",
    visibility,
    locator: { kind: "epub", cfi: "x" },
    selectedText: "s",
    note,
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
}

beforeEach(() => {
  apiMock.mockReset();
  useAnnotationStore.setState({ bookId: 42, annotations: [], replies: {}, activeId: null, status: "idle", error: null });
});

describe("useAnnotationStore", () => {
  it("creates a private-default annotation and sends visibility=private", () => {
    let sentBody: Record<string, unknown> = {};
    apiMock.mockImplementation(async (_p: string, init?: RequestInit) => {
      sentBody = JSON.parse(String(init?.body));
      return ann("a1", "private");
    });
    return useAnnotationStore
      .getState()
      .create({ locator: { kind: "epub", cfi: "x" }, selectedText: "s", note: "n" })
      .then(() => {
        expect(sentBody.visibility).toBe("private");
        expect(useAnnotationStore.getState().annotations[0].id).toBe("a1");
      });
  });

  it("update is optimistic and rolls back on failure", async () => {
    useAnnotationStore.setState({ annotations: [ann("a1", "private", "old")] });
    apiMock.mockRejectedValue(new Error("boom"));
    await expect(
      useAnnotationStore.getState().update("a1", { visibility: "community", note: "new" }),
    ).rejects.toBeTruthy();
    const a = useAnnotationStore.getState().annotations[0];
    expect(a.note).toBe("old");
    expect(a.visibility).toBe("private");
    expect(useAnnotationStore.getState().error).toBeTruthy();
  });

  it("remove is optimistic and rolls back on failure", async () => {
    useAnnotationStore.setState({ annotations: [ann("a1")] });
    apiMock.mockRejectedValue(new Error("boom"));
    await expect(useAnnotationStore.getState().remove("a1")).rejects.toBeTruthy();
    expect(useAnnotationStore.getState().annotations).toHaveLength(1);
  });

  it("reply appends and preserves the body for retry on failure", async () => {
    useAnnotationStore.setState({ annotations: [ann("a1", "community")] });
    apiMock.mockRejectedValueOnce(new Error("boom"));
    await expect(useAnnotationStore.getState().reply("a1", "nice")).rejects.toBeTruthy();
    expect(useAnnotationStore.getState().error).toBeTruthy();
    // A retry succeeds and appends exactly one reply.
    apiMock.mockResolvedValueOnce({ id: "r1", annotationId: "a1", authorId: "u", body: "nice", createdAt: "t" });
    await useAnnotationStore.getState().reply("a1", "nice");
    expect(useAnnotationStore.getState().replies["a1"]).toHaveLength(1);
  });
});
