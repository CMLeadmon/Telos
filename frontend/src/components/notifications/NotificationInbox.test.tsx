import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {},
}));

import { api } from "@/lib/api";
import { NotificationInbox } from "./NotificationInbox";
import { useNotificationStore } from "@/stores/useNotificationStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  apiMock.mockReset();
  useNotificationStore.setState({
    items: [],
    nextCursor: null,
    unreadCount: 0,
    lastEventSequence: 0,
    catchUpHighWater: 0,
    status: "idle",
    error: null,
  });
});

const page = {
  items: [
    {
      id: "a",
      kind: "mention",
      resourceType: "message",
      resourceId: "m1",
      payload: {},
      sequence: 5,
      read: false,
      createdAt: "2026-07-20T00:00:05Z",
    },
  ],
  nextCursor: undefined,
};

describe("NotificationInbox", () => {
  it("is a labeled, modal dialog listing notifications", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path.startsWith("/api/v1/notifications/unread-count")) return { count: 1 };
      return page;
    });
    render(<NotificationInbox open onClose={() => {}} />);
    const dialog = await screen.findByRole("dialog", { name: "Notifications" });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    await waitFor(() => expect(screen.getByTestId("notif-a")).toBeInTheDocument());
  });

  it("Escape closes the dialog", async () => {
    apiMock.mockResolvedValue({ items: [], nextCursor: undefined });
    const onClose = vi.fn();
    render(<NotificationInbox open onClose={onClose} />);
    await screen.findByRole("dialog");
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("mark-all read invokes the store action", async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path.startsWith("/api/v1/notifications/unread-count")) return { count: 1 };
      return page;
    });
    render(<NotificationInbox open onClose={() => {}} />);
    await screen.findByTestId("notif-a");
    fireEvent.click(screen.getByText("Mark all read"));
    await waitFor(() => expect(useNotificationStore.getState().unreadCount).toBe(0));
  });

  it("renders nothing when closed", () => {
    const { container } = render(<NotificationInbox open={false} onClose={() => {}} />);
    expect(container.firstChild).toBeNull();
  });
});
