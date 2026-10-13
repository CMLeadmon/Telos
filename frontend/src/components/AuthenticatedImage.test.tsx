import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { AuthenticatedImage, isNodePath } from "./AuthenticatedImage";
import { setServerConfig } from "@/lib/serverConfig";
import { invalidateAuthGeneration } from "@/lib/deviceAuth";
import { trackedObjectUrlCount } from "@/lib/objectUrls";

const created: string[] = [];
const revoked: string[] = [];

function png(bytes = 4, type = "image/png") {
  return new Response(new Uint8Array(bytes), { status: 200, headers: { "Content-Type": type } });
}

beforeEach(() => {
  created.length = 0;
  revoked.length = 0;
  let n = 0;
  vi.stubGlobal("URL", Object.assign(URL, {
    createObjectURL: vi.fn(() => {
      const url = `blob:test/${++n}`;
      created.push(url);
      return url;
    }),
    revokeObjectURL: vi.fn((url: string) => void revoked.push(url)),
  }));
  setServerConfig({ baseUrl: "https://node.example.com", mode: "token", accessToken: "bearer-1" });
});
afterEach(() => {
  invalidateAuthGeneration();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
});

describe("AuthenticatedImage in token mode", () => {
  it("fetches with the bearer and shows a blob URL, never the protected URL", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(async () => png());
    const { container } = render(<AuthenticatedImage path="/api/v1/library/books/b1/cover" alt="cover" />);
    await waitFor(() => expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:test/1"));
    const [url, init] = spy.mock.calls[0];
    expect(String(url)).toBe("https://node.example.com/api/v1/library/books/b1/cover");
    expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer bearer-1");
    expect(container.querySelector("img")?.getAttribute("src")).not.toContain("/api/v1/");
  });

  it("accepts an absolute URL on the configured node and keeps its query", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(async () => png());
    render(<AuthenticatedImage path="https://node.example.com/api/v1/users/u1/avatar?v=3" />);
    await waitFor(() => expect(spy).toHaveBeenCalled());
    expect(String(spy.mock.calls[0][0])).toBe("https://node.example.com/api/v1/users/u1/avatar?v=3");
  });

  it("revokes the blob URL on unmount and on replacement", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => png());
    const { rerender, unmount, container } = render(<AuthenticatedImage path="/api/v1/a" />);
    await waitFor(() => expect(container.querySelector("img")).not.toBeNull());
    rerender(<AuthenticatedImage path="/api/v1/b" />);
    await waitFor(() => expect(created).toHaveLength(2));
    expect(revoked).toContain("blob:test/1");
    unmount();
    expect(revoked).toContain("blob:test/2");
    expect(trackedObjectUrlCount()).toBe(0);
  });

  it("revokes every live blob URL when authority ends", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async () => png());
    const { container } = render(<AuthenticatedImage path="/api/v1/a" />);
    await waitFor(() => expect(container.querySelector("img")).not.toBeNull());
    expect(trackedObjectUrlCount()).toBe(1);
    invalidateAuthGeneration();
    expect(revoked).toContain("blob:test/1");
    expect(trackedObjectUrlCount()).toBe(0);
  });

  it("never attaches the bearer to an off-node URL, and asks for no referrer", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(async () => png());
    const { container } = render(<AuthenticatedImage path="https://evil.example/pixel.png" />);
    expect(spy).not.toHaveBeenCalled();
    const img = container.querySelector("img")!;
    expect(img.getAttribute("src")).toBe("https://evil.example/pixel.png");
    expect(img.getAttribute("referrerpolicy")).toBe("no-referrer");
  });

  it("shows nothing and reports failure for an error, a non-image, or an oversize body", async () => {
    for (const response of [
      () => new Response("nope", { status: 404 }),
      () => png(4, "text/html"),
      () => png(9 * 1024 * 1024),
    ]) {
      vi.spyOn(globalThis, "fetch").mockImplementation(async () => response());
      const onError = vi.fn();
      const { container, unmount } = render(<AuthenticatedImage path="/api/v1/bad" onError={onError} />);
      await waitFor(() => expect(onError).toHaveBeenCalled());
      expect(container.querySelector("img")).toBeNull();
      unmount();
      vi.restoreAllMocks();
    }
  });

  it("ignores a late response for a path it no longer shows", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (String(input).endsWith("/old")) await gate;
      return png();
    });
    const { rerender, container } = render(<AuthenticatedImage path="/api/v1/old" />);
    rerender(<AuthenticatedImage path="/api/v1/new" />);
    await waitFor(() => expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:test/1"));
    release();
    await Promise.resolve();
    await Promise.resolve();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:test/1");
    expect(created).toHaveLength(1);
  });
});

describe("AuthenticatedImage in cookie mode", () => {
  it("renders an ordinary image against the node and fetches nothing itself", () => {
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    const spy = vi.spyOn(globalThis, "fetch");
    const { container } = render(<AuthenticatedImage path="/api/v1/users/u1/avatar" alt="" />);
    expect(spy).not.toHaveBeenCalled();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("/api/v1/users/u1/avatar");
  });
});

describe("isNodePath", () => {
  it("is true for rooted paths and the configured origin only", () => {
    expect(isNodePath("/api/v1/x")).toBe(true);
    expect(isNodePath("https://node.example.com/api/v1/x")).toBe(true);
    expect(isNodePath("https://node.example.com.evil.example/api/v1/x")).toBe(false);
    expect(isNodePath("http://node.example.com/api/v1/x")).toBe(false);
    expect(isNodePath("//node.example.com/api/v1/x")).toBe(false);
    expect(isNodePath("data:image/png;base64,AAAA")).toBe(false);
    expect(isNodePath("relative/path.png")).toBe(false);
  });
});
