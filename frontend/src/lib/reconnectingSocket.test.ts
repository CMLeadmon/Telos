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

  constructor() {
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
