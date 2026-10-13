import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { getServerConfig } from "@/lib/serverConfig";
import { loadConnectionProfile, saveConnectionProfile } from "@/lib/connectionProfile";
import { invalidateAuthGeneration } from "@/lib/deviceAuth";
import { createFakeKeychain, createFakeNode, type FakeKeychain, type FakeNode } from "@/test/fakeNode";

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

    useConnectionStore.getState().setConfigured("1.0.0");

    expect(useConnectionStore.getState().state).toBe("configured");
    expect(useConnectionStore.getState().serverVersion).toBe("1.0.0");

    const cfg = getServerConfig();
    expect(cfg.baseUrl).toBe("https://telos.example.com");
    expect(cfg.mode).toBe("token");
    expect(cfg.accessToken).toBeNull();
  });

  it("has no certificate fingerprint state", () => {
    expect(Object.keys(useConnectionStore.getState())).not.toContain("pinnedCertFingerprint");
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
    store.setConfigured("0.9.0");
    store.setVersionSkewSoft(true);

    store.reset();

    const state = useConnectionStore.getState();
    expect(state.state).toBe("unconfigured");
    expect(state.serverUrl).toBe("");
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

  it("does nothing to storage or hydration in the browser build", () => {
    useConnectionStore.getState().hydrateFromProfile();
    expect(useConnectionStore.getState().state).toBe("unconfigured");
    expect(window.localStorage.getItem("telos.connection.v1")).toBeNull();
  });
});

describe("useConnectionStore in the native shell", () => {
  let node: FakeNode;
  let keychain: FakeKeychain;

  beforeEach(() => {
    node = createFakeNode();
    keychain = createFakeKeychain();
    keychain.install();
    node.install();
    window.localStorage.clear();
    invalidateAuthGeneration();
    useConnectionStore.getState().reset();
  });
  afterEach(() => {
    keychain.uninstall();
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("remembers only the origin when a node is configured", () => {
    const store = useConnectionStore.getState();
    store.setValidating(node.origin);
    store.setConfigured("0.1.0");
    expect(loadConnectionProfile()).toEqual({ origin: node.origin, userId: null, username: null, deviceId: null });
  });

  it("hydrates the origin from the stored profile without reading any secret", () => {
    saveConnectionProfile({ origin: node.origin, userId: null, username: null, deviceId: null });
    useConnectionStore.getState().hydrateFromProfile();
    expect(useConnectionStore.getState()).toMatchObject({ state: "configured", serverUrl: node.origin });
    expect(getServerConfig()).toMatchObject({ baseUrl: node.origin, mode: "token", accessToken: null });
    expect(keychain.calls).toHaveLength(0);
  });

  it("signs out of the old node when the member switches to another", async () => {
    const { signInWithPassword } = await import("@/lib/deviceAuth");
    const store = useConnectionStore.getState();
    store.setValidating(node.origin);
    store.setConfigured();
    await signInWithPassword({ name: "d", platform: "linux", version: "0.1.0" }, { username: "carter", password: node.password });
    expect(keychain.entries.size).toBe(1);

    useConnectionStore.getState().setValidating("https://other.example.com");
    useConnectionStore.getState().setConfigured();
    await vi.waitFor(() => expect(keychain.entries.size).toBe(0));
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
    expect(loadConnectionProfile()).toEqual({ origin: "https://other.example.com", userId: null, username: null, deviceId: null });
    expect(getServerConfig()).toMatchObject({ baseUrl: "https://other.example.com", accessToken: null });
  });
});
