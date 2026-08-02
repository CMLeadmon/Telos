import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
}));

vi.mock("@/stores/useAuthStore", () => ({
  useAuthStore: (selector: (s: unknown) => unknown) =>
    selector({
      user: {
        ID: "u1",
        Username: "testuser",
        Roles: ["Member"],
        Permissions: ["view_library", "view_media", "view_files"],
      },
    }),
}));

import { api } from "@/lib/api";
import { SharePicker } from "./SharePicker";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

// A node whose media is not flat: the Movies library holds a film directly,
// but a show is library → series → season → episode. The gateway's media
// endpoint returns one level at a time, so anything below the top is only
// reachable by walking.
// The Movies ID deliberately carries characters that must survive URL
// encoding — library IDs are opaque gateway strings, not slugs.
const MOVIES_ID = "library/root ?kind=video";
const LIBRARIES = [
  { id: MOVIES_ID, name: "Movies", type: "video" },
  { id: "lib-shows", name: "Shows", type: "video" },
];
const CHILDREN: Record<string, unknown[]> = {
  [MOVIES_ID]: [
    { id: "movie-1", title: "Raising Helen", duration: "1h 59m", type: "Movie", isFolder: false },
  ],
  "lib-shows": [
    { id: "series-1", title: "King of the Hill", duration: "", type: "Series", isFolder: true, childCount: 1 },
  ],
  "series-1": [
    { id: "season-1", title: "Season 1", duration: "", type: "Season", isFolder: true, childCount: 1 },
  ],
  "season-1": [
    { id: "episode-1", title: "Pilot", duration: "22m", type: "Episode", isFolder: false },
  ],
};

// Books carry canonical Telos catalog IDs since the identity cutover, not the
// numeric Grimmory ID they used to expose.
const BOOK_ID = "11111111-1111-4111-8111-111111111111";

function routeApi(overrides: Record<string, unknown> = {}) {
  apiMock.mockImplementation((path: string) => {
    for (const [key, value] of Object.entries(overrides)) {
      if (path.startsWith(key)) return Promise.resolve(value);
    }
    if (path === "/api/v1/media") return Promise.resolve(LIBRARIES);
    if (path.startsWith("/api/v1/media/items?parentId=")) {
      const id = decodeURIComponent(path.split("parentId=")[1]);
      return Promise.resolve(CHILDREN[id] ?? []);
    }
    if (path === "/api/v1/library/books") {
      return Promise.resolve([{ id: BOOK_ID, title: "Pride and Prejudice", authors: ["Austen"] }]);
    }
    if (path.startsWith("/api/v1/files")) {
      return Promise.resolve({
        folders: [{ id: "UGljdHVyZXM", name: "Pictures", path: "Pictures" }],
        files: [
          {
            id: "Sm9lXzIuanBn",
            filename: "Joe_2.jpg",
            size_bytes: 2048,
            mime_type: "image/jpeg",
          },
        ],
        hasNext: false,
      });
    }
    return Promise.resolve({ books: [], media: [], files: [] });
  });
}

const rowByName = async (name: string) => {
  const row = await screen.findByText(name);
  return row.closest("button")!;
};

describe("SharePicker", () => {
  beforeEach(() => {
    apiMock.mockReset();
    routeApi();
  });

  // The regression. The old picker fetched each library's direct children and
  // kept only the leaves, so a node whose media is nested — every TV show,
  // every audiobook — offered nothing to share.
  it("walks down to media nested below the top level", async () => {
    const onPick = vi.fn();
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="film" />);

    fireEvent.click(await rowByName("Shows"));
    fireEvent.click(await rowByName("King of the Hill"));
    fireEvent.click(await rowByName("Season 1"));
    fireEvent.click(await rowByName("Pilot"));

    expect(onPick).toHaveBeenCalledWith("stream_film", "episode-1", "Pilot");
  });

  it("offers folders as navigation, never as something to share", async () => {
    const onPick = vi.fn();
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="film" />);

    fireEvent.click(await rowByName("Shows"));
    // The series is a folder: clicking it descends rather than staging a share.
    fireEvent.click(await rowByName("King of the Hill"));
    await screen.findByText("Season 1");
    expect(onPick).not.toHaveBeenCalled();
  });

  it("lets a breadcrumb climb back out of the trail", async () => {
    render(<SharePicker onPick={vi.fn()} onClose={vi.fn()} defaultTab="film" />);

    fireEvent.click(await rowByName("Shows"));
    await screen.findByText("King of the Hill");

    fireEvent.click(screen.getByRole("button", { name: "Stream" }));
    expect(await screen.findByText("Movies")).toBeInTheDocument();
  });

  // The file kind was fully implemented on the gateway and in ShareCard, but
  // the picker had no Files tab, so it was unreachable.
  it("shares a file", async () => {
    const onPick = vi.fn();
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="file" />);

    fireEvent.click(await rowByName("Joe_2.jpg"));
    expect(onPick).toHaveBeenCalledWith("file", "Sm9lXzIuanBn", "Joe_2.jpg");
  });

  it("shares a book", async () => {
    const onPick = vi.fn();
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="book" />);

    fireEvent.click(await rowByName("Pride and Prejudice"));
    expect(onPick).toHaveBeenCalledWith("library_book", BOOK_ID, "Pride and Prejudice");
  });

  // Search reaches what browsing would otherwise require four clicks to find.
  it("finds nested media by search without walking the tree", async () => {
    const onPick = vi.fn();
    routeApi({
      "/api/v1/search": {
        books: [],
        media: [{ id: "episode-1", title: "Pilot", duration: "22m", type: "Episode", isFolder: false }],
        files: [],
      },
    });
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="film" />);

    fireEvent.change(screen.getByLabelText("search shareable content"), {
      target: { value: "pilot" },
    });

    fireEvent.click(await rowByName("Pilot"));
    expect(onPick).toHaveBeenCalledWith("stream_film", "episode-1", "Pilot");
  });

  it("keeps a search term under the gateway's minimum out of the wire", async () => {
    render(<SharePicker onPick={vi.fn()} onClose={vi.fn()} defaultTab="film" />);
    await screen.findByText("Movies");

    fireEvent.change(screen.getByLabelText("search shareable content"), {
      target: { value: "p" },
    });

    await waitFor(() => {
      expect(
        apiMock.mock.calls.filter((c) => String(c[0]).startsWith("/api/v1/search")),
      ).toHaveLength(0);
    });
    // Still browsing.
    expect(screen.getByText("Movies")).toBeInTheDocument();
  });

  // Library and item IDs are opaque; a raw interpolation breaks any ID
  // carrying a slash, space, or query character.
  it("URL-encodes an opaque parent ID when descending", async () => {
    const onPick = vi.fn();
    render(<SharePicker onPick={onPick} onClose={vi.fn()} defaultTab="film" />);

    fireEvent.click(await rowByName("Movies"));
    fireEvent.click(await rowByName("Raising Helen"));

    expect(onPick).toHaveBeenCalledWith("stream_film", "movie-1", "Raising Helen");
    expect(
      apiMock.mock.calls.some(
        (c) =>
          String(c[0]) ===
          `/api/v1/media/items?parentId=${encodeURIComponent(MOVIES_ID)}`,
      ),
    ).toBe(true);
  });

  it("hides tabs the viewer has no capability for", async () => {
    vi.resetModules();
    vi.doMock("@/stores/useAuthStore", () => ({
      useAuthStore: (selector: (s: unknown) => unknown) =>
        selector({
          user: { ID: "u1", Username: "t", Roles: ["Member"], Permissions: ["view_library"] },
        }),
    }));
    const { SharePicker: Scoped } = await import("./SharePicker");

    render(<Scoped onPick={vi.fn()} onClose={vi.fn()} defaultTab="file" />);

    expect(screen.queryByRole("tab", { name: /Files/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: /Stream/i })).not.toBeInTheDocument();
    // Asking for a forbidden tab falls back to one the viewer can use.
    expect(await screen.findByText("Pride and Prejudice")).toBeInTheDocument();

    vi.doUnmock("@/stores/useAuthStore");
  });
});
