import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, apiBlob: vi.fn() };
});

import { apiBlob } from "./api";
import {
  isMobileNative,
  isNativeApp,
  openExternal,
  openNodeResource,
  writeClipboard,
  type NativeShellBridge,
} from "./platform";
import { setServerConfig } from "./serverConfig";

type ShellWindow = { __TELOS_NATIVE_SHELL__?: NativeShellBridge };

function installShell(bridge: Partial<NativeShellBridge>) {
  (window as unknown as ShellWindow).__TELOS_NATIVE_SHELL__ = {
    openExternal: async () => {},
    writeClipboard: async () => {},
    ...bridge,
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
  setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
});

afterEach(() => {
  delete (window as unknown as ShellWindow).__TELOS_NATIVE_SHELL__;
});

describe("platform detection", () => {
  it("is a browser until the shell says otherwise", () => {
    expect(isNativeApp()).toBe(false);
    expect(isMobileNative()).toBe(false);
  });

  it("is native once the shell installs its bridge", () => {
    installShell({});
    expect(isNativeApp()).toBe(true);
  });

  // A phone user agent in a browser tab is still a browser tab: the mobile
  // affordances this gates are native ones.
  it("does not call a mobile browser a mobile native app", () => {
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue(
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari",
    );
    expect(isMobileNative()).toBe(false);
    installShell({});
    expect(isMobileNative()).toBe(true);
  });
});

describe("openExternal", () => {
  it("opens a tab in a browser", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await openExternal("https://example.com");
    expect(open).toHaveBeenCalledWith("https://example.com", "_blank", "noopener,noreferrer");
  });

  // A plain link would navigate the one app window away from Telos, and a
  // webview has no back button to return with.
  it("hands the URL to the OS in the native client", async () => {
    const openExternalSpy = vi.fn().mockResolvedValue(undefined);
    installShell({ openExternal: openExternalSpy });
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    await openExternal("https://example.com");

    expect(openExternalSpy).toHaveBeenCalledWith("https://example.com");
    expect(open).not.toHaveBeenCalled();
  });

  it("still opens a tab if the bridge rejects", async () => {
    installShell({ openExternal: vi.fn().mockRejectedValue(new Error("no")) });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    await openExternal("https://example.com");
    expect(open).toHaveBeenCalled();
  });
});

describe("writeClipboard", () => {
  it("reports success through the web API", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
    expect(await writeClipboard("token")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("token");
  });

  // The clipboard API exists only in a secure context, and a custom-scheme
  // webview is not reliably one. A copy that silently did nothing would leave
  // the member believing they hold an invite token.
  it("reports failure rather than pretending, when there is no clipboard", async () => {
    Object.defineProperty(navigator, "clipboard", {
      value: undefined,
      configurable: true,
    });
    expect(await writeClipboard("token")).toBe(false);
  });

  it("reports failure when the web API rejects", async () => {
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
      configurable: true,
    });
    expect(await writeClipboard("token")).toBe(false);
  });

  it("prefers the shell bridge when there is one", async () => {
    const bridgeWrite = vi.fn().mockResolvedValue(undefined);
    installShell({ writeClipboard: bridgeWrite });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });

    expect(await writeClipboard("token")).toBe(true);
    expect(bridgeWrite).toHaveBeenCalledWith("token");
    expect(writeText).not.toHaveBeenCalled();
  });
});

describe("openNodeResource", () => {
  it("lets the browser navigate when a cookie will authenticate it", async () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    await openNodeResource("/api/v1/files/f1/download");

    // Absolutized: a relative URL in the native client resolves against the app
    // bundle rather than the node.
    expect(open).toHaveBeenCalledWith(
      "https://telos.example.com/api/v1/files/f1/download",
      "_blank",
      "noopener,noreferrer",
    );
    expect(apiBlob).not.toHaveBeenCalled();
  });

  // The case the whole function exists for: a navigation carries no
  // Authorization header, so in token mode window.open lands on a bare 401.
  it("pulls the bytes through the authenticated client in token mode", async () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "a1" });
    vi.mocked(apiBlob).mockResolvedValue(new Blob(["bytes"]));
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const createObjectURL = vi.fn().mockReturnValue("blob:telos/1");
    Object.defineProperty(URL, "createObjectURL", { value: createObjectURL, configurable: true });
    Object.defineProperty(URL, "revokeObjectURL", { value: vi.fn(), configurable: true });
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});

    await openNodeResource("/api/v1/files/f1/download", "report.pdf");

    expect(apiBlob).toHaveBeenCalledWith("/api/v1/files/f1/download");
    expect(open).not.toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
  });

  it("names the download from the path, falling back to the suggestion", async () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "a1" });
    vi.mocked(apiBlob).mockResolvedValue(new Blob(["bytes"]));
    Object.defineProperty(URL, "createObjectURL", {
      value: vi.fn().mockReturnValue("blob:telos/1"),
      configurable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", { value: vi.fn(), configurable: true });

    const names: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      names.push(this.download);
    });

    await openNodeResource("/api/v1/library/books/b1/content", "book.epub");
    // ".../download" is the route, not a name anyone wants on disk.
    await openNodeResource("/api/v1/files/f1/download", "invoice.pdf");

    expect(names).toEqual(["content", "invoice.pdf"]);
  });
});
