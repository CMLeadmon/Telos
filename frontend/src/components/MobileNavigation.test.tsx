import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("next/navigation", () => ({
  usePathname: () => "/chat",
  useRouter: () => ({ push: vi.fn() }),
}));

vi.mock("@/stores/useAuthStore", () => ({
  useAuthStore: (selector: (s: unknown) => unknown) =>
    selector({
      user: {
        ID: "u1",
        Username: "testuser",
        Roles: ["Member"],
        Permissions: ["view_channel", "view_media", "view_library", "view_files"],
      },
    }),
}));

vi.mock("@/stores/useChatSessionStore", () => ({
  useChatSessionStore: () => ({
    channels: [
      { id: "c1", name: "general" },
      { id: "c2", name: "lounge" },
    ],
    activeChannelId: "c1",
    connect: vi.fn(),
  }),
}));

import { MobileNavigation } from "./MobileNavigation";
import { useMobileNavStore } from "@/stores/useMobileNavStore";

describe("MobileNavigation", () => {
  beforeEach(() => {
    useMobileNavStore.setState({ channelDrawerOpen: false });
  });

  // The DS specifies a four-item tabbar: Chat, Stream, Library, Files. The
  // channel list is reached from the chat header, not a fifth tab.
  it("renders exactly the four module tabs", () => {
    render(<MobileNavigation />);
    const nav = screen.getByTestId("mobile-bottom-nav");
    expect(nav).toBeInTheDocument();
    expect(nav.querySelectorAll("a")).toHaveLength(4);
    for (const label of ["Chat", "Stream", "Library", "Files"]) {
      expect(screen.getByLabelText(label)).toBeInTheDocument();
    }
  });

  it("keeps the drawer closed until the shared store opens it", () => {
    render(<MobileNavigation />);
    expect(screen.queryByTestId("mobile-channels-drawer")).not.toBeInTheDocument();
  });

  it("renders the channel list when the store opens the drawer", () => {
    useMobileNavStore.setState({ channelDrawerOpen: true });
    render(<MobileNavigation />);
    expect(screen.getByTestId("mobile-channels-drawer")).toBeInTheDocument();
    expect(screen.getByText("general")).toBeInTheDocument();
    expect(screen.getByText("lounge")).toBeInTheDocument();
  });
});
