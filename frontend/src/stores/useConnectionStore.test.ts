import { describe, expect, it, beforeEach } from "vitest";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { getServerConfig } from "@/lib/serverConfig";

describe("useConnectionStore", () => {
  beforeEach(() => {
    useConnectionStore.getState().reset();
  });

  it("initializes with unconfigured state", () => {
    const state = useConnectionStore.getState();
    expect(state.state).toBe("unconfigured");
    expect(state.serverUrl).toBe("");
    expect(state.error).toBeNull();
  });

  it("transitions through validating to configured state", () => {
    const store = useConnectionStore.getState();
    store.setValidating("https://telos.example.com");

    expect(useConnectionStore.getState().state).toBe("validating");
    expect(useConnectionStore.getState().serverUrl).toBe("https://telos.example.com");

    useConnectionStore.getState().setConfigured("sha256-cert-fingerprint-abc", "1.0.0");

    expect(useConnectionStore.getState().state).toBe("configured");
    expect(useConnectionStore.getState().pinnedCertFingerprint).toBe("sha256-cert-fingerprint-abc");
    expect(useConnectionStore.getState().serverVersion).toBe("1.0.0");

    const cfg = getServerConfig();
    expect(cfg.baseUrl).toBe("https://telos.example.com");
    expect(cfg.mode).toBe("token");
  });

  it("sets error state cleanly", () => {
    const store = useConnectionStore.getState();
    store.setValidating("https://invalid.example.com");
    store.setError("unreachable", "Could not connect to target host");

    expect(useConnectionStore.getState().state).toBe("error");
    expect(useConnectionStore.getState().error).toBe("unreachable");
    expect(useConnectionStore.getState().errorMessage).toBe("Could not connect to target host");
  });

  it("resets state and serverConfig", () => {
    const store = useConnectionStore.getState();
    store.setValidating("https://telos.example.com");
    store.setConfigured("sha256-fingerprint", "0.9.0");
    store.setVersionSkewSoft(true);

    store.reset();

    const state = useConnectionStore.getState();
    expect(state.state).toBe("unconfigured");
    expect(state.serverUrl).toBe("");
    expect(state.pinnedCertFingerprint).toBeNull();
    expect(state.versionSkewSoft).toBe(false);

    const cfg = getServerConfig();
    expect(cfg.baseUrl).toBe("");
    expect(cfg.mode).toBe("cookie");
  });

  it("updates soft version skew state", () => {
    const store = useConnectionStore.getState();
    expect(useConnectionStore.getState().versionSkewSoft).toBe(false);
    store.setVersionSkewSoft(true);
    expect(useConnectionStore.getState().versionSkewSoft).toBe(true);
  });
});
