import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mintAudioTicket, mintVideoLocator, resolveLocatorUrl } from "./mediaLocator";
import { setServerConfig } from "./serverConfig";

const TOKEN = "eyJ5IjoiaGxzIn0.c2ln";

beforeEach(() => {
  setServerConfig({ baseUrl: "https://node.example.com", mode: "token", accessToken: "a1" });
});
afterEach(() => {
  vi.restoreAllMocks();
  setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
});

function respond(body: unknown, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify(body), { status }));
}

describe("resolveLocatorUrl", () => {
  it("resolves the node's capability paths against the configured node", () => {
    expect(resolveLocatorUrl(`/api/v1/hls/${TOKEN}`)).toBe(`https://node.example.com/api/v1/hls/${TOKEN}`);
    expect(resolveLocatorUrl(`/api/v1/stream/audio-ticket/${TOKEN}`)).toBe(
      `https://node.example.com/api/v1/stream/audio-ticket/${TOKEN}`,
    );
  });

  it.each([
    "https://evil.example/api/v1/hls/x",
    "//evil.example/api/v1/hls/x.y",
    "/api/v1/media",
    "/api/v1/hls/../auth/me",
    "/api/v1/hls/x?token=1",
    "/api/v1/hls/",
    "javascript:alert(1)",
    "",
    null,
    undefined,
    42,
  ])("refuses %j", (value) => {
    expect(() => resolveLocatorUrl(value)).toThrow(/unexpected playback address/);
  });
});

describe("minting", () => {
  it("asks the authenticated endpoint for a video locator and returns an absolute URL", async () => {
    const spy = respond({ url: `/api/v1/hls/${TOKEN}`, expiresIn: 43200 });
    const out = await mintVideoLocator("item 1");
    expect(out).toEqual({ url: `https://node.example.com/api/v1/hls/${TOKEN}`, expiresIn: 43200 });
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("https://node.example.com/api/v1/stream/video/item%201/playback-locator");
    expect(init?.method).toBe("POST");
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer a1");
  });

  it("asks for a direct audio ticket", async () => {
    const spy = respond({ url: `/api/v1/stream/audio-ticket/${TOKEN}`, expiresIn: 100 });
    const out = await mintAudioTicket("track-1");
    expect(out.url).toBe(`https://node.example.com/api/v1/stream/audio-ticket/${TOKEN}`);
    expect(String(spy.mock.calls[0][0])).toBe("https://node.example.com/api/v1/stream/audio/track-1/stream-ticket");
  });

  it("rejects a response that names anything but a capability path", async () => {
    respond({ url: "https://evil.example/x", expiresIn: 1 });
    await expect(mintVideoLocator("a")).rejects.toThrow(/unexpected playback address/);
  });

  it("propagates a refusal from the node", async () => {
    respond({ error: { code: "forbidden", message: "no" } }, 403);
    await expect(mintVideoLocator("a")).rejects.toMatchObject({ status: 403 });
  });

  it("never logs the minted address", async () => {
    const logs = [vi.spyOn(console, "log"), vi.spyOn(console, "warn"), vi.spyOn(console, "error"), vi.spyOn(console, "info")];
    respond({ url: `/api/v1/hls/${TOKEN}`, expiresIn: 1 });
    await mintVideoLocator("a");
    for (const log of logs) expect(JSON.stringify(log.mock.calls)).not.toContain(TOKEN);
  });
});
