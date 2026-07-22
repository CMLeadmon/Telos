import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {},
  wsBase: () => "ws://localhost",
}));

import { api } from "@/lib/api";
import { ThreadPanel } from "./ThreadPanel";
import { useChatSessionStore, type ChatMessage } from "@/stores/useChatSessionStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

function msg(id: string, content = "hi"): ChatMessage {
  return { id, sender: "u", avatar: "UU", role: "Member", content, timestamp: "2026-07-20T00:00:00Z" };
}

beforeEach(() => {
  apiMock.mockReset();
  useChatSessionStore.setState({
    activeChannelId: "chan-1",
    activeThreadRoot: null,
    threadReplies: [],
    threadNextCursor: null,
    threadStatus: "idle",
    threadDraftError: null,
    pendingReplyMutationId: null,
  });
});

describe("ThreadPanel", () => {
  it("renders nothing when no thread is open", () => {
    const { container } = render(<ThreadPanel />);
    expect(container.firstChild).toBeNull();
  });

  it("shows the root and replies, and a reply composer without nested reply controls", () => {
    useChatSessionStore.setState({
      activeThreadRoot: msg("9", "the root"),
      threadReplies: [msg("1", "a reply")],
      threadStatus: "ready",
    });
    render(<ThreadPanel />);
    expect(screen.getByTestId("thread-panel")).toBeInTheDocument();
    expect(screen.getByTestId("thread-root")).toHaveTextContent("the root");
    const replyRow = screen.getByTestId("thread-reply-1");
    expect(replyRow).toHaveTextContent("a reply");
    // The reply composer exists...
    expect(screen.getByTestId("thread-reply-input")).toBeInTheDocument();
    // ...but a reply row offers no further "reply in thread" control.
    expect(within(replyRow).queryByLabelText("Reply in thread")).toBeNull();
  });

  it("close button resets thread state", () => {
    useChatSessionStore.setState({ activeThreadRoot: msg("9"), threadStatus: "ready" });
    render(<ThreadPanel />);
    fireEvent.click(screen.getByLabelText("Close thread"));
    expect(useChatSessionStore.getState().activeThreadRoot).toBeNull();
  });

  it("submitting a reply clears the input only after acknowledgement", async () => {
    useChatSessionStore.setState({ activeThreadRoot: msg("9"), threadStatus: "ready" });
    apiMock.mockResolvedValue({ message: msg("1", "posted") });
    render(<ThreadPanel />);
    const input = screen.getByTestId("thread-reply-input") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "posted" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(input.value).toBe(""));
    expect(useChatSessionStore.getState().threadReplies.some((m) => m.id === "1")).toBe(true);
  });
});
