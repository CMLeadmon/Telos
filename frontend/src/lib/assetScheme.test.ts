import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { apiBase, assetUrl, wsBase } from "@/lib/api";
import { setServerConfig } from "@/lib/serverConfig";

// Under the native shell the document is not the node. Tauri v2 serves the
// static export from the app bundle over tauri://localhost (http://tauri.localhost
// on Windows), so anything that resolves against the document origin — a
// relative path, a scheme derived from window.location — lands inside the
// bundle, where none of the API exists. apiBase, wsBase and assetUrl are the
// three places that decide, and this pins all three under that scheme.
//
// api_origin.test.ts and asset_url.test.ts already cover these helpers under an
// ordinary web origin; what is specific here is the custom scheme.

const NODE = "https://node.example.com";

let originalLocation: Location;

function serveFrom(href: string) {
  const url = new URL(href);
  Object.defineProperty(window, "location", {
    value: {
      href,
      protocol: url.protocol,
      host: url.host,
      hostname: url.hostname,
      origin: url.origin,
    },
    configurable: true,
    writable: true,
  });
}

describe("asset and API resolution under the Tauri custom scheme", () => {
  beforeEach(() => {
    originalLocation = window.location;
    // What Tauri v2 actually serves on macOS, Linux and iOS.
    serveFrom("tauri://localhost/index.html");
    setServerConfig({ baseUrl: NODE, mode: "token", accessToken: "device-token" });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", {
      value: originalLocation,
      configurable: true,
      writable: true,
    });
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
  });

  it("sends API calls to the configured node, not to the app bundle", () => {
    expect(apiBase()).toBe(NODE);
    expect(`${apiBase()}/api/v1/health`).toBe(`${NODE}/api/v1/health`);
  });

  // The socket is the one call that used to be assembled differently from the
  // rest; under a custom scheme a document-derived base would open ws://localhost
  // against the bundle and simply never connect.
  it("opens the socket against the node, and never derives it from the scheme", () => {
    const ws = wsBase();
    expect(ws).toBe("wss://node.example.com");
    expect(ws.startsWith("tauri:")).toBe(false);
    expect(ws).not.toContain("localhost");
  });

  // The OS fetches lock-screen artwork itself, outside the document, so an
  // artwork path that stayed relative would be resolved by the OS against
  // nothing at all.
  it("absolutizes cover and media paths so they leave the bundle", () => {
    expect(assetUrl("/api/v1/library/books/abc/cover")).toBe(
      `${NODE}/api/v1/library/books/abc/cover`,
    );
    expect(assetUrl("api/v1/library/books/abc/cover")).toBe(
      `${NODE}/api/v1/library/books/abc/cover`,
    );
  });

  it("leaves a URL that already carries its own origin alone", () => {
    expect(assetUrl("https://images.example.com/cover.jpg")).toBe(
      "https://images.example.com/cover.jpg",
    );
    expect(assetUrl("blob:tauri://localhost/9f2c")).toBe("blob:tauri://localhost/9f2c");
  });

  // blob: was guarded and data: was not, so a node that answered with an inline
  // cover produced https://node.example.com/data:image/png;base64,… — an address
  // for nothing, and a broken image in the one place the fallback should work.
  it("leaves a data URI alone rather than prefixing a node address to it", () => {
    const inline = "data:image/png;base64,iVBORw0KGgo=";
    expect(assetUrl(inline)).toBe(inline);
  });
});

// A web build is served *by* the node, so the document origin is the right
// answer and none of the above applies. Kept here so the two modes are read
// side by side: the same helpers must not absolutize in a browser tab.
describe("the same helpers on a web origin", () => {
  beforeEach(() => {
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
  });

  it("leaves paths relative so they stay on the serving origin", () => {
    expect(apiBase()).toBe("");
    expect(assetUrl("/api/v1/library/books/abc/cover")).toBe(
      "/api/v1/library/books/abc/cover",
    );
  });
});
