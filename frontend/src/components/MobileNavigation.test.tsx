import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

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
      { id: "c1", name: "general", type: "text" },
      { id: "c2", name: "lounge", type: "voice" },
    ],
    activeChannelId: "c1",
    connect: vi.fn(),
  }),
}));

vi.mock("@/stores/useVoiceSessionStore", () => ({
  useVoiceSessionStore: () => ({
    status: "disconnected",
    channelId: null,
    join: vi.fn(),
    leave: vi.fn(),
  }),
}));

import { MobileNavigation } from "./MobileNavigation";

describe("MobileNavigation", () => {
  it("renders mobile bottom navigation bar and drawer toggle", () => {
    render(<MobileNavigation />);
    expect(screen.getByTestId("mobile-bottom-nav")).toBeInTheDocument();
    expect(screen.getByTestId("mobile-channels-toggle")).toBeInTheDocument();
  });

  it("opens channels drawer when toggle button is clicked", () => {
    render(<MobileNavigation />);
    fireEvent.click(screen.getByTestId("mobile-channels-toggle"));
    expect(screen.getByTestId("mobile-channels-drawer")).toBeInTheDocument();
    expect(screen.getByText("general")).toBeInTheDocument();
    expect(screen.getByText("lounge")).toBeInTheDocument();
  });
});
