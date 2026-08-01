import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

import { api } from "@/lib/api";
import { useMediaStore } from "./useMediaStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;
const PARENT_ID = "catalog/root ?kind=video";
const ITEM_ID = "11111111-1111-4111-8111-111111111112";

beforeEach(() => {
  apiMock.mockReset();
  useMediaStore.setState({
    itemsByParent: {},
    itemsStatusByParent: {},
  });
});

describe("useMediaStore catalog IDs", () => {
  it("encodes an opaque parent ID at the item-list boundary", async () => {
    apiMock.mockResolvedValue([]);

    await useMediaStore.getState().fetchItems(PARENT_ID);

    expect(apiMock).toHaveBeenCalledWith(
      "/api/v1/media/items?parentId=catalog%2Froot%20%3Fkind%3Dvideo",
    );
  });

  it("preserves a canonical item ID exactly in client state", async () => {
    apiMock.mockResolvedValue([
      {
        id: ITEM_ID,
        title: "Opaque Film",
        duration: "2h",
        type: "Movie",
        isFolder: false,
      },
    ]);

    await useMediaStore.getState().fetchItems(PARENT_ID);

    expect(useMediaStore.getState().itemsByParent[PARENT_ID][0].id).toBe(
      ITEM_ID,
    );
  });
});
