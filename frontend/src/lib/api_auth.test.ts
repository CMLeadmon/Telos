import { describe, expect, it, beforeEach, vi } from "vitest";
import { api } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

describe("api helper auth mode branching", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  });

  it("passes credentials: include under cookie auth mode", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await api<{ ok: boolean }>("/api/v1/test");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const [, init] = fetchSpy.mock.calls[0];
    expect(init?.credentials).toBe("include");
    const headers = new Headers(init?.headers);
    expect(headers.has("Authorization")).toBe(false);
  });

  it("attaches Authorization header and omits credentials under token auth mode", async () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: "token-secret-123",
    });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );

    await api<{ ok: boolean }>("/api/v1/test");

    expect(fetchSpy).toHaveBeenCalledOnce();
    const [, init] = fetchSpy.mock.calls[0];
    expect(init?.credentials).toBeUndefined();
    const headers = new Headers(init?.headers);
    expect(headers.get("Authorization")).toBe("Bearer token-secret-123");
  });
});

describe("api helper token mode without a token", () => {
  // Without this the fetch spy from the previous block survives and
  // mock.calls[0] is that block's request, not this one's.
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  // A client in token mode that has not authenticated yet must not silently
  // degrade to a credentialed request. It should reach the server bare.
  it("sends neither cookies nor an Authorization header", async () => {
    setServerConfig({
      baseUrl: "https://telos.example.com",
      mode: "token",
      accessToken: null,
    });

    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 }),
    );

    await api<{ ok: boolean }>("/api/v1/test");

    const [, init] = fetchSpy.mock.calls[0];
    expect(init?.credentials).toBeUndefined();
    expect(new Headers(init?.headers).has("Authorization")).toBe(false);
  });
});

describe("api helper binary responses", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
  });

  // The readers ask for book bytes through the same helper as JSON. Running
  // those through res.text() + JSON.parse corrupts the file and surfaces as
  // "Server returned an invalid response", so the binary branch is load-bearing
  // for every EPUB and PDF that opens.
  it("returns an ArrayBuffer for book content rather than parsing it as JSON", async () => {
    const bytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04]); // PK zip header
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(bytes, {
        status: 200,
        headers: { "Content-Type": "application/epub+zip" },
      }),
    );

    const out = await api<ArrayBuffer>("/api/v1/library/books/abc/content");
    expect(out).toBeInstanceOf(ArrayBuffer);
    expect(Array.from(new Uint8Array(out))).toEqual([0x50, 0x4b, 0x03, 0x04]);
  });

  it("surfaces the server's message when a binary fetch fails", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("book is unavailable", {
        status: 503,
        headers: { "Content-Type": "application/epub+zip" },
      }),
    );

    await expect(api("/api/v1/library/books/abc/content")).rejects.toMatchObject({
      status: 503,
      message: "book is unavailable",
    });
  });
});
