import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { ReconnectingSocket } from "./reconnectingSocket";

let mockInstances: MockWebSocket[] = [];

class MockWebSocket {
  public readyState = 0;
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

describe("ReconnectingSocket lifecycle listeners", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockInstances = [];
    vi.stubGlobal("WebSocket", MockWebSocket);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("retries immediately when the network returns", () => {
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    const ws1 = mockInstances[0];
    ws1.onclose?.({ code: 1006, reason: "" });

    expect(mockInstances.length).toBe(1);
    expect(socket["timer"]).not.toBeNull();

    window.dispatchEvent(new Event("online"));

    expect(socket["currentDelay"]).toBe(500);
    expect(mockInstances.length).toBe(2);
    socket.close();
  });

  it("retries when the document becomes visible", () => {
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    const ws1 = mockInstances[0];
    ws1.onclose?.({ code: 1006, reason: "" });

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));

    expect(socket["currentDelay"]).toBe(500);
    expect(mockInstances.length).toBe(2);
    socket.close();
  });

  it("does not retry on visibilitychange when hidden", () => {
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    const ws1 = mockInstances[0];
    ws1.onclose?.({ code: 1006, reason: "" });

    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));

    expect(mockInstances.length).toBe(1);
    socket.close();
  });

  it("stops listening after close", () => {
    const removeWindow = vi.spyOn(window, "removeEventListener");
    const removeDoc = vi.spyOn(document, "removeEventListener");
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    socket.close();
    expect(removeWindow).toHaveBeenCalledWith("online", expect.any(Function));
    expect(removeDoc).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
  });

  it("does not trigger reconnect if already connected", () => {
    const socket = new ReconnectingSocket({ url: "ws://localhost/ws" });
    const ws1 = mockInstances[0];
    ws1.onopen?.();

    window.dispatchEvent(new Event("online"));

    expect(mockInstances.length).toBe(1);
    socket.close();
  });
});
