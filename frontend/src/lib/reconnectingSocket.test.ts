import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReconnectingSocket } from "./reconnectingSocket";

let mockInstances: MockWebSocket[] = [];

class MockWebSocket {
  public readyState = 1;
  public onopen?: () => void;
  public onmessage?: (event: { data: string }) => void;
  public onerror?: (event: Event) => void;
  public onclose?: (event: { code: number; reason: string }) => void;
  public send = vi.fn();
  public close = vi.fn();
  public protocols?: string | string[];

  constructor(_url: string, protocols?: string | string[]) {
    this.protocols = protocols;
    mockInstances.push(this);
  }
}

describe("ReconnectingSocket", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockInstances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
  });

  it("connects and triggers onOpen", () => {
    const onOpen = vi.fn();
    new ReconnectingSocket({ url: "ws://localhost/test", onOpen });

    const ws = mockInstances[0];
    ws.onopen?.();
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("schedules reconnection with exponential backoff on close", () => {
    new ReconnectingSocket({
      url: "ws://localhost/test",
      minDelayMs: 500,
      maxDelayMs: 5000,
    });

    const ws1 = mockInstances[0];
    ws1.onopen?.();
    ws1.onclose?.({ code: 1006, reason: "" });

    vi.advanceTimersByTime(600);
    expect(mockInstances.length).toBe(2);
  });

  it("does not reconnect if intentionally closed", () => {
    const socket = new ReconnectingSocket({ url: "ws://localhost/test" });
    socket.close();

    const ws1 = mockInstances[0];
    ws1.onclose?.({ code: 1000, reason: "Normal closure" });

    vi.advanceTimersByTime(10000);
    expect(mockInstances.length).toBe(1);
  });
});

// A token-mode client authenticates its upgrade with a single-use ticket carried
// as a subprotocol. Fetching one is a network round trip, so the attempt is no
// longer synchronous with the call — which is where the interesting cases are.
describe("ReconnectingSocket subprotocols", () => {
  beforeEach(() => {
    mockInstances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
  });

  it("offers the supplier's protocols on the handshake", async () => {
    new ReconnectingSocket({
      url: "ws://localhost/test",
      protocols: async () => ["telos-ticket.abc"],
    });

    await vi.waitFor(() => expect(mockInstances.length).toBe(1));
    expect(mockInstances[0].protocols).toEqual(["telos-ticket.abc"]);
  });

  // The ticket resolves after close() has already run. Installing the socket
  // then would leave one live connection nothing holds a reference to.
  it("installs nothing when closed while the ticket is in flight", async () => {
    let release: (v: string[]) => void = () => {};
    const socket = new ReconnectingSocket({
      url: "ws://localhost/test",
      protocols: () => new Promise<string[]>((r) => (release = r)),
    });

    socket.close();
    release(["telos-ticket.abc"]);
    await Promise.resolve();
    await Promise.resolve();

    expect(mockInstances.length).toBe(0);
  });

  it("backs off and retries when the node refuses a ticket", async () => {
    vi.useFakeTimers();
    let attempts = 0;
    new ReconnectingSocket({
      url: "ws://localhost/test",
      minDelayMs: 500,
      protocols: async () => {
        attempts += 1;
        if (attempts === 1) throw new Error("ticket refused");
        return ["telos-ticket.second"];
      },
    });

    // The rejection must schedule a retry, not surface as an unhandled rejection
    // that leaves the member on a permanently connecting socket.
    await vi.advanceTimersByTimeAsync(1000);
    expect(attempts).toBe(2);
    expect(mockInstances.length).toBe(1);
    vi.useRealTimers();
  });
});
