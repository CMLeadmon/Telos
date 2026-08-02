import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({ api: vi.fn(), ApiError: class ApiError extends Error {} }));

import { api } from "@/lib/api";
import { AnnotationPanel } from "./AnnotationPanel";

const BOOK_ID = "11111111-1111-4111-8111-111111111111";
import { useAnnotationStore, type Annotation } from "@/stores/useAnnotationStore";
import { useAuthStore } from "@/stores/useAuthStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function ann(id: string, ownerId: string, visibility: "private" | "community"): Annotation {
  return {
    id,
    targetType: "book",
    targetId: "42",
    ownerId,
    visibility,
    locator: { kind: "epub", cfi: "x" },
    selectedText: `text-${id}`,
    note: `note-${id}`,
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
  };
}

const mine = ann("mine", "me", "private");
const otherCommunity = ann("oc", "other", "community");
const otherPrivate = ann("op", "other", "private");

beforeEach(() => {
  apiMock.mockReset();
  // The server only ever returns rows the viewer may see; simulate that by
  // omitting another user's private annotation from the listing.
  apiMock.mockImplementation(async (path: string) => {
    if (path.endsWith("/replies")) return { replies: [] };
    return { annotations: [mine, otherCommunity] };
  });
  useAuthStore.setState({ user: { ID: "me", Username: "me", DisplayName: "Me" } as never });
  useAnnotationStore.setState({ bookId: BOOK_ID, annotations: [], replies: {}, activeId: null, status: "idle", error: null });
});

describe("AnnotationPanel", () => {
  it("shows my annotations under Mine and community ones under Community", async () => {
    render(<AnnotationPanel bookId={BOOK_ID} canModerate={false} />);
    // Mine filter (default): my private annotation is present.
    await waitFor(() => expect(screen.getByTestId("annotation-mine")).toBeInTheDocument());
    expect(screen.queryByTestId("annotation-oc")).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Community" }));
    expect(screen.getByTestId("annotation-oc")).toBeInTheDocument();
    // Another user's PRIVATE annotation never appears (server never returned it).
    expect(screen.queryByTestId("annotation-op")).toBeNull();
    expect(useAnnotationStore.getState().annotations.some((a) => a.id === otherPrivate.id)).toBe(false);
  });

  it("owner sees edit/delete; a moderator sees remove on others' community notes", async () => {
    render(<AnnotationPanel bookId={BOOK_ID} canModerate={true} />);
    await waitFor(() => expect(screen.getByTestId("annotation-mine")).toBeInTheDocument());
    expect(screen.getByTestId("annotation-edit-mine")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Community" }));
    // Not the owner but a moderator: a Remove control is offered.
    expect(screen.getByTestId("annotation-moderate-oc")).toBeInTheDocument();
    expect(screen.queryByTestId("annotation-edit-oc")).toBeNull();
  });
});
