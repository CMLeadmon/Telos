import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { setServerConfig } from "@/lib/serverConfig";

const mintVideoLocator = vi.fn();
const mintAudioTicket = vi.fn();
vi.mock("@/lib/mediaLocator", () => ({
  mintVideoLocator: (...args: unknown[]) => mintVideoLocator(...args),
  mintAudioTicket: (...args: unknown[]) => mintAudioTicket(...args),
}));

const hlsInstance = {
  on: vi.fn(),
  loadSource: vi.fn(),
  attachMedia: vi.fn(),
  startLoad: vi.fn(),
  destroy: vi.fn(),
};
const HlsCtor = vi.fn(function Hls() {
  return hlsInstance;
}) as unknown as { new (config?: unknown): typeof hlsInstance; isSupported: () => boolean; Events: Record<string, string>; ErrorTypes: Record<string, string> };
HlsCtor.isSupported = () => true;
HlsCtor.Events = { ERROR: "hlsError" };
HlsCtor.ErrorTypes = { NETWORK_ERROR: "networkError" };
vi.mock("hls.js", () => ({ default: HlsCtor }));

import { HlsPlayer } from "./HlsPlayer";

const LOCATOR = "https://node.example.com/api/v1/hls/sealed.mac";
const TICKET = "https://node.example.com/api/v1/stream/audio-ticket/sealed.mac";

function stubCanPlay(value: string) {
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue(value as CanPlayTypeResult);
}

beforeEach(() => {
  vi.clearAllMocks();
  setServerConfig({ baseUrl: "https://node.example.com", mode: "token", accessToken: "bearer-xyz" });
  mintVideoLocator.mockResolvedValue({ url: LOCATOR, expiresIn: 43200 });
  mintAudioTicket.mockResolvedValue({ url: TICKET, expiresIn: 43200 });
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
  setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
});

describe("HlsPlayer", () => {
  it("native HLS: mints, then assigns the minted URL with no header requirement", async () => {
    stubCanPlay("maybe");
    const { getByTestId } = render(<HlsPlayer itemId="item-1" audio={false} />);
    await waitFor(() => expect(getByTestId("stream-video").getAttribute("src")).toBe(LOCATOR));
    expect(mintVideoLocator).toHaveBeenCalledWith("item-1", expect.any(AbortSignal));
    expect(HlsCtor).not.toHaveBeenCalled();
  });

  it("hls.js: loads the minted locator and attaches no bearer", async () => {
    stubCanPlay("");
    render(<HlsPlayer itemId="item-1" audio={false} />);
    await waitFor(() => expect(hlsInstance.loadSource).toHaveBeenCalledWith(LOCATOR));
    expect(hlsInstance.attachMedia).toHaveBeenCalled();
    // Constructed with no xhrSetup: nothing may inject the access token into a locator request.
    const config = (HlsCtor as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]?.[0];
    expect(config === undefined || !("xhrSetup" in (config as object))).toBe(true);
  });

  it("audio: mints a direct ticket before assigning src", async () => {
    stubCanPlay("");
    const { getByTestId } = render(<HlsPlayer itemId="track-1" audio />);
    await waitFor(() => expect(getByTestId("stream-video").getAttribute("src")).toBe(TICKET));
    expect(mintAudioTicket).toHaveBeenCalledWith("track-1", expect.any(AbortSignal));
    expect(mintVideoLocator).not.toHaveBeenCalled();
  });

  it("shows a bounded error and never assigns the protected route when minting is refused", async () => {
    stubCanPlay("maybe");
    mintVideoLocator.mockRejectedValue(Object.assign(new Error("forbidden: https://node.example.com/secret"), { status: 403 }));
    const { findByText, container } = render(<HlsPlayer itemId="item-1" audio={false} />);
    expect(await findByText(/playback could not be authorized/)).toBeInTheDocument();
    expect(container.querySelector("video")).toBeNull();
    expect(container.textContent).not.toContain("secret");
  });

  it("ignores a late mint after the item changed", async () => {
    stubCanPlay("maybe");
    let resolveFirst!: (value: unknown) => void;
    mintVideoLocator.mockImplementationOnce(() => new Promise((resolve) => (resolveFirst = resolve)));
    mintVideoLocator.mockResolvedValueOnce({ url: "https://node.example.com/api/v1/hls/second.sig", expiresIn: 1 });
    const { rerender, getByTestId } = render(<HlsPlayer itemId="first" audio={false} />);
    rerender(<HlsPlayer itemId="second" audio={false} />);
    await waitFor(() => expect(getByTestId("stream-video").getAttribute("src")).toBe("https://node.example.com/api/v1/hls/second.sig"));
    resolveFirst({ url: LOCATOR, expiresIn: 1 });
    await Promise.resolve();
    expect(getByTestId("stream-video").getAttribute("src")).toBe("https://node.example.com/api/v1/hls/second.sig");
  });

  it("aborts an in-flight mint on unmount and releases the element", async () => {
    stubCanPlay("maybe");
    let signal: AbortSignal | undefined;
    mintVideoLocator.mockImplementation((_id: string, s: AbortSignal) => {
      signal = s;
      return new Promise(() => {});
    });
    const { unmount } = render(<HlsPlayer itemId="item-1" audio={false} />);
    await waitFor(() => expect(signal).toBeDefined());
    unmount();
    expect(signal?.aborted).toBe(true);
  });

  it("re-mints and resumes after a fatal network error, a bounded number of times", async () => {
    stubCanPlay("");
    render(<HlsPlayer itemId="item-1" audio={false} />);
    await waitFor(() => expect(hlsInstance.on).toHaveBeenCalled());
    const onError = hlsInstance.on.mock.calls.find((c) => c[0] === "hlsError")![1] as (e: string, d: unknown) => void;
    mintVideoLocator.mockResolvedValue({ url: "https://node.example.com/api/v1/hls/renewed.sig", expiresIn: 1 });
    onError("hlsError", { fatal: true, type: "networkError", details: "manifestLoadError" });
    await waitFor(() => expect(hlsInstance.loadSource).toHaveBeenCalledWith("https://node.example.com/api/v1/hls/renewed.sig"));
    expect(hlsInstance.startLoad).toHaveBeenCalled();
    // After the bound, the failure is surfaced rather than retried forever.
    for (let i = 0; i < 4; i++) onError("hlsError", { fatal: true, type: "networkError", details: "fragLoadError" });
    await waitFor(() => expect(hlsInstance.destroy).toHaveBeenCalled());
  });
});
