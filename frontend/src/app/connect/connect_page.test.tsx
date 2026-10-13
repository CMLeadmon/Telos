import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ConnectPage from "./page";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { getServerConfig } from "@/lib/serverConfig";
import { loadConnectionProfile } from "@/lib/connectionProfile";
import { createFakeKeychain, type FakeKeychain } from "@/test/fakeNode";

const push = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));

// A Response body can only be read once, and a test that connects twice would
// otherwise get an already-consumed body on the second probe and misreport it
// as an unreachable node. Build a fresh one per call.
function mockHealth(body: Record<string, unknown>, status = 200) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}

function typeAndConnect(value: string) {
  fireEvent.change(screen.getByLabelText(/server address/i), { target: { value } });
  fireEvent.click(screen.getByRole("button", { name: /^connect$/i }));
}

beforeEach(() => {
  push.mockClear();
  useConnectionStore.getState().reset();
  vi.restoreAllMocks();
});

describe("ConnectPage", () => {
  it("configures the node and points the API layer at it", async () => {
    mockHealth({ status: "ok", version: "0.1.0", minClientVersion: "0.1.0" });
    render(<ConnectPage />);
    typeAndConnect("https://telos.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("configured");
    });
    expect(useConnectionStore.getState().serverUrl).toBe("https://telos.example.com");
    // The point of configuring: every later request has to leave for that node.
    expect(getServerConfig().baseUrl).toBe("https://telos.example.com");
    expect(push).toHaveBeenCalledWith("/login/");
  });

  it("defaults a bare host to https rather than downgrading the transport", async () => {
    const fetchSpy = mockHealth({ status: "ok" });
    render(<ConnectPage />);
    typeAndConnect("telos.example.com");

    await waitFor(() => expect(fetchSpy).toHaveBeenCalled());
    expect(String(fetchSpy.mock.calls[0][0])).toBe("https://telos.example.com/api/v1/health");
  });

  it("rejects an address that answers without a health report", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html>hello</html>", { status: 200 }));
    render(<ConnectPage />);
    typeAndConnect("https://not-telos.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("error");
    });
    expect(useConnectionStore.getState().error).toBe("unreachable");
    expect(screen.getByRole("alert")).toHaveTextContent(/not like a telos node/i);
    expect(push).not.toHaveBeenCalled();
  });

  it("refuses a node that requires a newer client", async () => {
    mockHealth({ status: "ok", version: "9.0.0", minClientVersion: "9.0.0" });
    render(<ConnectPage />);
    typeAndConnect("https://telos.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().error).toBe("version_skew_hard");
    });
    expect(getServerConfig().baseUrl).toBe("");
    expect(push).not.toHaveBeenCalled();
  });

  it("still connects to a degraded node, which is when a member most needs in", async () => {
    mockHealth({ status: "fail", version: "0.1.0", minClientVersion: "0.1.0" }, 503);
    render(<ConnectPage />);
    typeAndConnect("https://telos.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("configured");
    });
  });
});

describe("ConnectPage trust and transport", () => {
  it("refuses plain http before any request is made", async () => {
    const fetchSpy = mockHealth({ status: "ok" });
    render(<ConnectPage />);
    typeAndConnect("http://telos.example.com");

    await waitFor(() => expect(useConnectionStore.getState().error).toBe("https_required"));
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent(/https/i);
  });

  it.each(["https://user:pw@telos.example.com", "https://telos.example.com/telos", "https://telos.example.com/?x=1"])(
    "refuses %s without contacting it",
    async (address) => {
      const fetchSpy = mockHealth({ status: "ok" });
      render(<ConnectPage />);
      typeAndConnect(address);
      await waitFor(() => expect(useConnectionStore.getState().state).toBe("error"));
      expect(fetchSpy).not.toHaveBeenCalled();
    },
  );

  it("offers no fingerprint, pin or override and never asks the shell to inspect a certificate", async () => {
    mockHealth({ status: "ok" });
    render(<ConnectPage />);
    typeAndConnect("https://telos.example.com");
    await waitFor(() => expect(useConnectionStore.getState().state).toBe("configured"));
    expect(screen.queryByRole("heading", { name: /trust this node/i })).toBeNull();
    expect(screen.queryByText(/fingerprint|pinned|sha-256/i)).toBeNull();
    expect(screen.queryByRole("button", { name: /trust|continue anyway|ignore/i })).toBeNull();
    expect((window as unknown as Record<string, unknown>).__TELOS_NATIVE_TLS__).toBeUndefined();
  });

  it("explains platform trust when the connection fails, with no way to continue", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Load failed"));
    render(<ConnectPage />);
    typeAndConnect("https://self-signed.example.com");
    await waitFor(() => expect(useConnectionStore.getState().error).toBe("unreachable"));
    expect(screen.getByRole("alert")).toHaveTextContent(/trusted by this computer/i);
    expect(screen.queryByRole("button", { name: /trust|continue|override/i })).toBeNull();
    expect(push).not.toHaveBeenCalled();
  });
});

describe("ConnectPage in the native shell", () => {
  let keychain: FakeKeychain;
  beforeEach(() => {
    keychain = createFakeKeychain();
    keychain.install();
    window.localStorage.clear();
  });
  afterEach(() => {
    keychain.uninstall();
    window.localStorage.clear();
  });

  it("saves only the normalized origin as the connection profile", async () => {
    mockHealth({ status: "ok", version: "0.1.0" });
    render(<ConnectPage />);
    typeAndConnect("Telos.Example.com:443");
    await waitFor(() => expect(useConnectionStore.getState().state).toBe("configured"));
    expect(loadConnectionProfile()).toEqual({ origin: "https://telos.example.com", userId: null, username: null, deviceId: null });
    expect(keychain.calls).toHaveLength(0);
  });
});
