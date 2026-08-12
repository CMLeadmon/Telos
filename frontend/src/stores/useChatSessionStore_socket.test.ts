import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api", () => ({
  api: vi.fn().mockResolvedValue({ online: [], count: 0, pins: [] }),
  ApiError: class ApiError extends Error {},
  wsBase: () => "ws://localhost",
}));
vi.mock("@/lib/deviceAuth", () => ({ socketProtocols: vi.fn() }));

import { socketProtocols } from "@/lib/deviceAuth";
import { useChatSessionStore } from "./useChatSessionStore";

let mockInstances: MockWebSocket[] = [];

class MockWebSocket {
  public readyState = 1;
  public onopen?: () => void;
  public onmessage?: (event: { data: string }) => void;
  public onerror?: (event: Event) => void;
  public onclose?: ((event: { code: number; reason: string }) => void) | null;
  public url: string;
  public protocols?: string | string[];
  public close = vi.fn();

  constructor(url: string, protocols?: string | string[]) {
    this.url = url;
    this.protocols = protocols;
    mockInstances.push(this);
  }
}

beforeEach(() => {
  mockInstances = [];
  vi.stubGlobal("WebSocket", MockWebSocket);
  vi.mocked(socketProtocols).mockReset();
});

afterEach(() => {
  useChatSessionStore.getState().disconnect();
});

describe("chat socket authentication", () => {
  // Cookie mode is the browser: the session cookie rides the handshake and a
  // ticket would be a wasted round trip.
  it("opens a bare handshake when no subprotocol is offered", async () => {
    vi.mocked(socketProtocols).mockResolvedValue([]);
    useChatSessionStore.getState().connect("chan-1");

    await vi.waitFor(() => expect(mockInstances.length).toBe(1));
    expect(mockInstances[0].url).toBe("ws://localhost/api/v1/chat/ws?channel=chan-1");
    expect(mockInstances[0].protocols).toBeUndefined();
  });

  // Token mode: the upgrade carries a single-use ticket, because a bearer header
  // cannot be attached to a browser WebSocket handshake at all.
  it("carries the ticket subprotocol into the handshake", async () => {
    vi.mocked(socketProtocols).mockResolvedValue(["telos-ticket.abc"]);
    useChatSessionStore.getState().connect("chan-1");

    await vi.waitFor(() => expect(mockInstances.length).toBe(1));
    expect(mockInstances[0].protocols).toEqual(["telos-ticket.abc"]);
  });

  // The ticket resolves after the member left. Installing the socket then would
  // connect them to a channel they are no longer looking at, and every handler
  // guard reads `socket === ws`, which a freshly installed socket satisfies.
  it("installs nothing when disconnected while the ticket is in flight", async () => {
    let release: (v: string[]) => void = () => {};
    vi.mocked(socketProtocols).mockReturnValue(
      new Promise<string[]>((r) => (release = r)),
    );

    useChatSessionStore.getState().connect("chan-1");
    useChatSessionStore.getState().disconnect();
    release(["telos-ticket.abc"]);
    await Promise.resolve();
    await Promise.resolve();

    expect(mockInstances.length).toBe(0);
    expect(useChatSessionStore.getState().connection).toBe("idle");
  });

  // A node that will not issue a ticket has to leave the member somewhere other
  // than a spinner that never resolves.
  it("reports closed when the ticket cannot be obtained", async () => {
    vi.mocked(socketProtocols).mockRejectedValue(new Error("no ticket"));
    useChatSessionStore.getState().connect("chan-1");

    await vi.waitFor(() =>
      expect(useChatSessionStore.getState().connection).toBe("closed"),
    );
    expect(mockInstances.length).toBe(0);
  });
});
