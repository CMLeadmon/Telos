import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({ api: vi.fn() }));

import { api } from "@/lib/api";
import { SharePicker } from "./SharePicker";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  apiMock.mockReset();
});

describe("SharePicker", () => {
  it("encodes an opaque media library ID and preserves the selected item ID", async () => {
    const onPick = vi.fn();
    apiMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/library/books") return [];
      if (path === "/api/v1/media") {
        return [{ id: "library/root ?kind=video", name: "Films", type: "video" }];
      }
      if (
        path ===
        "/api/v1/media/items?parentId=library%2Froot%20%3Fkind%3Dvideo"
      ) {
        return [
          {
            id: "11111111-1111-4111-8111-111111111112",
            title: "Opaque Film",
            duration: "2h",
            isFolder: false,
          },
        ];
      }
      throw new Error(`unexpected API path: ${path}`);
    });

    render(
      <SharePicker
        defaultTab="film"
        onClose={vi.fn()}
        onPick={onPick}
      />,
    );

    const film = await screen.findByRole("button", { name: /Opaque Film/ });
    fireEvent.click(film);

    await waitFor(() =>
      expect(onPick).toHaveBeenCalledWith(
        "stream_film",
        "11111111-1111-4111-8111-111111111112",
        "Opaque Film",
      ),
    );
  });
});
