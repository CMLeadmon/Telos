import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAuthStore, NATIVE_ACCOUNT_MESSAGE } from "./useAuthStore";
import { useConnectionStore } from "./useConnectionStore";
import { invalidateAuthGeneration } from "@/lib/deviceAuth";
import { loadCleanupTombstone, loadConnectionProfile, saveConnectionProfile } from "@/lib/connectionProfile";
import { getServerConfig, setServerConfig } from "@/lib/serverConfig";
import { CredentialStoreError } from "@/lib/secureStorage";
import { createFakeKeychain, createFakeNode, type FakeKeychain, type FakeNode } from "@/test/fakeNode";

let node: FakeNode;
let keychain: FakeKeychain;

const reset = () =>
  useAuthStore.setState({ user: null, status: "unknown", connectivity: "online", credentialIssue: null, cleanupRequired: false, notice: null });

beforeEach(() => {
  node = createFakeNode();
  keychain = createFakeKeychain();
  keychain.install();
  node.install();
  window.localStorage.clear();
  invalidateAuthGeneration();
  setServerConfig({ baseUrl: node.origin, mode: "token", accessToken: null });
  saveConnectionProfile({ origin: node.origin, userId: null, username: null, deviceId: null });
  useConnectionStore.setState({ state: "configured", serverUrl: node.origin });
  reset();
});

afterEach(() => {
  keychain.uninstall();
  vi.restoreAllMocks();
  window.localStorage.clear();
  useConnectionStore.getState().reset();
  reset();
});

describe("native token login", () => {
  it("registers, learns the account, stores the credential and profile, and becomes authenticated", async () => {
    await useAuthStore.getState().login("carter", node.password);
    const state = useAuthStore.getState();
    expect(state.status).toBe("authenticated");
    expect(state.user?.Username).toBe("carter");
    expect(node.calls.map((c) => c.path)).toEqual(["/api/v1/auth/devices/register", "/api/v1/auth/me"]);
    expect(keychain.entries.size).toBe(1);
    expect(loadConnectionProfile()?.userId).toBe(node.user.ID);
    // No cookie flow was touched, and the password is nowhere in the store.
    expect(node.calls.some((c) => c.path.endsWith("/auth/login"))).toBe(false);
    expect(JSON.stringify(useAuthStore.getState())).not.toContain(node.password);
  });

  it("leaves the member anonymous with a visible credential-store error when the keychain refuses", async () => {
    keychain.failOn.add("set");
    await expect(useAuthStore.getState().login("carter", node.password)).rejects.toBeInstanceOf(CredentialStoreError);
    expect(useAuthStore.getState().status).not.toBe("authenticated");
    expect(useAuthStore.getState().user).toBeNull();
    expect(getServerConfig().accessToken).toBeNull();
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
  });

  it("sends bootstrap and invite members to the hosted browser instead of device registration", async () => {
    await expect(useAuthStore.getState().bootstrap("carter", "x".repeat(20), "tok")).rejects.toMatchObject({ message: NATIVE_ACCOUNT_MESSAGE });
    await expect(useAuthStore.getState().acceptInvite("carter", "x".repeat(20), "tok")).rejects.toMatchObject({ message: NATIVE_ACCOUNT_MESSAGE });
    expect(node.calls).toHaveLength(0);
  });
});

describe("native startup", () => {
  async function signedInThenRestarted() {
    await useAuthStore.getState().login("carter", node.password);
    // A relaunch: process memory is gone, the credential service and profile remain.
    invalidateAuthGeneration();
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    useConnectionStore.setState({ state: "unconfigured", serverUrl: "" });
    reset();
    node.calls.length = 0;
    keychain.calls.length = 0;
  }

  it("hydrates the origin, rotates once, rewrites the credential and authenticates", async () => {
    await signedInThenRestarted();
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(useConnectionStore.getState().serverUrl).toBe(node.origin);
    expect(node.calls.map((c) => c.path)).toEqual(["/api/v1/auth/devices/refresh", "/api/v1/auth/me"]);
    expect(keychain.calls).toEqual(["get", "set"]);
  });

  it("becomes anonymous after a rejected refresh, dropping the identity but keeping the node", async () => {
    await signedInThenRestarted();
    node.failNext("/devices/refresh", 403);
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().notice).toMatch(/signed out/i);
    expect(keychain.entries.size).toBe(0);
    expect(loadConnectionProfile()).toMatchObject({ origin: node.origin, userId: null });
  });

  it("stays undecided and offers a retry when the node is unreachable", async () => {
    await signedInThenRestarted();
    node.failNext("/devices/refresh", "network");
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("unknown");
    expect(useAuthStore.getState().connectivity).toBe("error");
    expect(keychain.entries.size).toBe(1);
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("authenticated");
  });

  it("reports a locked credential service without discarding the credential", async () => {
    await signedInThenRestarted();
    keychain.failOn.add("get");
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().credentialIssue).toMatch(/locked/i);
    expect(keychain.entries.size).toBe(1);
  });

  it("mentions the retired plaintext store once, when the shell removed it", async () => {
    const bridge = (window as unknown as { __TELOS_NATIVE_CREDENTIALS__: Record<string, unknown> }).__TELOS_NATIVE_CREDENTIALS__;
    bridge.legacyStoreRetired = vi.fn().mockResolvedValueOnce(true).mockResolvedValue(false);
    await useAuthStore.getState().initializeAuth();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().notice).toMatch(/sign in again/i);
  });
});

describe("native logout", () => {
  it("ends the session locally, revokes the device and deletes the credential", async () => {
    await useAuthStore.getState().login("carter", node.password);
    await useAuthStore.getState().logout();
    const state = useAuthStore.getState();
    expect(state.status).toBe("anonymous");
    expect(state.user).toBeNull();
    expect(state.notice).toBeNull();
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
    expect(keychain.entries.size).toBe(0);
    expect(loadConnectionProfile()?.userId).toBeNull();
  });

  it("warns that remote revocation is pending when the node cannot be reached", async () => {
    await useAuthStore.getState().login("carter", node.password);
    node.failNext("/auth/logout", "network");
    await useAuthStore.getState().logout();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().notice).toMatch(/could not be reached/i);
    expect(keychain.entries.size).toBe(0);
  });

  it("never claims the credential is gone when deletion failed, and recovers on retry", async () => {
    await useAuthStore.getState().login("carter", node.password);
    keychain.failOn.add("delete");
    await useAuthStore.getState().logout();
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().cleanupRequired).toBe(true);
    expect(useAuthStore.getState().credentialIssue).toBeTruthy();
    expect(loadCleanupTombstone()).not.toBeNull();
    await useAuthStore.getState().retryCleanup();
    expect(useAuthStore.getState().cleanupRequired).toBe(true);
    keychain.failOn.clear();
    await useAuthStore.getState().retryCleanup();
    expect(useAuthStore.getState().cleanupRequired).toBe(false);
    expect(loadCleanupTombstone()).toBeNull();
    expect(keychain.entries.size).toBe(0);
  });
});

describe("cookie mode (browser build)", () => {
  beforeEach(() => {
    keychain.uninstall();
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
  });

  it("logs in with the cookie flow and never touches a keychain", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(async (input) =>
      String(input).endsWith("/auth/me")
        ? new Response(JSON.stringify({ ID: "u", Username: "carter", Roles: [], Permissions: [], DisplayName: "C", HasAvatar: false }), { status: 200 })
        : new Response("{}", { status: 200 }),
    );
    await useAuthStore.getState().login("carter", "pw");
    expect(useAuthStore.getState().status).toBe("authenticated");
    expect(fetchSpy.mock.calls.map((c) => String(c[0]))).toEqual(["/api/v1/auth/login", "/api/v1/auth/me"]);
    expect(keychain.calls).toHaveLength(0);
    for (const call of fetchSpy.mock.calls) expect(call[1]?.credentials).toBe("include");
  });

  it("logs out through /auth/logout and clears the user", async () => {
    useAuthStore.setState({ user: { ID: "u", Username: "c", Roles: [], Permissions: [], DisplayName: "c", HasAvatar: false }, status: "authenticated" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("{}", { status: 200 }));
    await useAuthStore.getState().logout();
    expect(String(fetchSpy.mock.calls[0][0])).toBe("/api/v1/auth/logout");
    expect(useAuthStore.getState().status).toBe("anonymous");
    expect(useAuthStore.getState().user).toBeNull();
  });
});
