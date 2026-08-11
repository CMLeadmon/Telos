import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import ConnectPage from "./page";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { getServerConfig } from "@/lib/serverConfig";
import type { NativeTlsBridge } from "@/lib/certPinning";

const push = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ push }) }));

const CERT_A = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99";
const CERT_B = "99:88:77:66:55:44:33:22:11:00:FF:EE:DD:CC:BB:AA";

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

function installBridge(fingerprint: string) {
  (window as unknown as { __TELOS_NATIVE_TLS__?: NativeTlsBridge }).__TELOS_NATIVE_TLS__ = {
    leafCertificate: async () => ({
      fingerprintSha256: fingerprint,
      subject: "CN=telos.example.com",
      issuer: "CN=Telos Self-Signed",
    }),
  };
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

afterEach(() => {
  delete (window as unknown as { __TELOS_NATIVE_TLS__?: NativeTlsBridge }).__TELOS_NATIVE_TLS__;
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

  // A node that answers but is not Telos must not be accepted as one.
  it("rejects an address that answers without a health report", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("<html>hello</html>", { status: 200 }),
    );
    render(<ConnectPage />);
    typeAndConnect("https://not-telos.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("error");
    });
    expect(useConnectionStore.getState().error).toBe("unreachable");
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

describe("ConnectPage certificate trust", () => {
  it("asks for confirmation on first use instead of pinning silently", async () => {
    installBridge(CERT_A);
    mockHealth({ status: "ok" });
    render(<ConnectPage />);
    // A host of its own: secureStorage's in-memory map is module-level, so a
    // pin written here would outlive this test and make the next one start
    // already-trusted.
    typeAndConnect("https://first-use.example.com");

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /trust this node/i })).toBeInTheDocument();
    });
    // Nothing is configured until the member actually confirms.
    expect(useConnectionStore.getState().state).not.toBe("configured");
    expect(push).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /trust and continue/i }));
    await waitFor(() => {
      expect(useConnectionStore.getState().state).toBe("configured");
    });
    expect(useConnectionStore.getState().pinnedCertFingerprint).toBeTruthy();
  });

  it("blocks when the node presents a different certificate than the pinned one", async () => {
    installBridge(CERT_A);
    mockHealth({ status: "ok" });
    const { unmount } = render(<ConnectPage />);
    typeAndConnect("https://mismatch.example.com");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /trust and continue/i })).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /trust and continue/i }));
    await waitFor(() => expect(useConnectionStore.getState().state).toBe("configured"));
    unmount();

    // Same host, different certificate — the interception TOFU exists to catch.
    useConnectionStore.getState().reset();
    push.mockClear();
    installBridge(CERT_B);
    render(<ConnectPage />);
    typeAndConnect("https://mismatch.example.com");

    await waitFor(() => {
      expect(useConnectionStore.getState().error).toBe("untrusted_certificate");
    });
    expect(useConnectionStore.getState().state).toBe("error");
    expect(push).not.toHaveBeenCalled();
  });

  it("does not claim pinning when the platform cannot pin", () => {
    render(<ConnectPage />);
    expect(screen.getByText(/certificate pinning needs the desktop app/i)).toBeInTheDocument();
  });
});
