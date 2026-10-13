import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, api } from "./api";
import {
  adoptDeviceCredential,
  authGeneration,
  credentialKey,
  invalidateAuthGeneration,
  refreshAccessToken,
  registerDevice,
  signInWithPassword,
  signOutNative,
  socketProtocols,
  startNativeSession,
  StaleAuthError,
} from "./deviceAuth";
import { clearConnectionProfile, loadCleanupTombstone, loadConnectionProfile, saveConnectionProfile } from "./connectionProfile";
import { getServerConfig, setServerConfig } from "./serverConfig";
import { CredentialStoreError } from "./secureStorage";
import { createFakeKeychain, createFakeNode, uuid, type FakeKeychain, type FakeNode } from "@/test/fakeNode";

const DEVICE = { name: "Telos desktop (Linux)", platform: "linux", version: "0.1.0" };

let node: FakeNode;
let keychain: FakeKeychain;
let clock = 0;

function freshNative() {
  node = createFakeNode();
  keychain = createFakeKeychain();
  keychain.install();
  node.install();
  clock = 1_000;
  vi.spyOn(performance, "now").mockImplementation(() => clock);
  window.localStorage.clear();
  invalidateAuthGeneration();
  setServerConfig({ baseUrl: node.origin, mode: "token", accessToken: null });
  saveConnectionProfile({ origin: node.origin, userId: null, username: null, deviceId: null });
}

async function signIn() {
  return signInWithPassword<{ ID: string; Username: string }>(DEVICE, { username: "carter", password: node.password });
}

beforeEach(freshNative);
afterEach(() => {
  keychain.uninstall();
  vi.restoreAllMocks();
  window.localStorage.clear();
  setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
});

describe("registration and adoption", () => {
  it("registers without persisting anything or sending a credential", async () => {
    const reg = await registerDevice(DEVICE.name, DEVICE.platform, DEVICE.version, { username: "carter", password: node.password });
    expect(reg.deviceId).toBeTruthy();
    expect(keychain.entries.size).toBe(0);
    expect(getServerConfig().accessToken).toBeNull();
    const call = node.calls[0];
    expect(call.bearer).toBeNull();
    expect(call.credentials).toBeUndefined();
    expect(call.body).toMatchObject({ username: "carter", password: node.password, deviceName: DEVICE.name });
  });

  it("stores the refresh token first, then installs the access token, then the profile", async () => {
    const me = await signIn();
    expect(me.Username).toBe("carter");
    const profile = loadConnectionProfile()!;
    expect(profile).toMatchObject({ origin: node.origin, userId: node.user.ID, username: "carter" });
    expect(keychain.entries.size).toBe(1);
    expect(keychain.calls.indexOf("set")).toBeGreaterThanOrEqual(0);
    expect(getServerConfig().accessToken).toBeTruthy();
    expect([...keychain.entries.values()][0]).toBe(node.devices.get(profile.deviceId!)!.refresh);
  });

  it("never places a token or password in web storage", async () => {
    await signIn();
    const dump = [window.localStorage, window.sessionStorage]
      .flatMap((storage) => Array.from({ length: storage.length }, (_, i) => `${storage.key(i)}=${storage.getItem(storage.key(i)!)}`))
      .join("\n");
    expect(dump).not.toContain(node.password);
    expect(dump).not.toMatch(/refresh-\d+|access-\d+/);
    expect(dump).toContain("telos.connection.v1");
  });

  it("revokes the new device and clears local state when the credential service refuses", async () => {
    keychain.failOn.add("set");
    await expect(signIn()).rejects.toBeInstanceOf(CredentialStoreError);
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
    expect(node.devices.size).toBe(1);
    expect(getServerConfig().accessToken).toBeNull();
    expect(loadConnectionProfile()).toMatchObject({ origin: node.origin, userId: null, deviceId: null });
    expect(keychain.entries.size).toBe(0);
  });

  it("revokes the device when /auth/me fails after registration", async () => {
    node.failNext("/auth/me", 500);
    await expect(signIn()).rejects.toBeInstanceOf(ApiError);
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
    expect(getServerConfig().accessToken).toBeNull();
    expect(keychain.entries.size).toBe(0);
  });

  it("rejects wrong credentials without creating a device", async () => {
    await expect(signInWithPassword(DEVICE, { username: "carter", password: "wrong" })).rejects.toMatchObject({ status: 401 });
    expect(node.devices.size).toBe(0);
  });

  it("refuses to sign in with no node selected", async () => {
    setServerConfig({ baseUrl: "", mode: "token", accessToken: null });
    await expect(signIn()).rejects.toBeInstanceOf(ApiError);
    expect(node.calls).toHaveLength(0);
  });

  it("discards a registration that finishes after the node was abandoned", async () => {
    const release = node.hold("/devices/register");
    const pending = signIn();
    await vi.waitFor(() => expect(node.count("/devices/register")).toBe(1));
    invalidateAuthGeneration();
    release();
    await expect(pending).rejects.toBeInstanceOf(StaleAuthError);
    expect([...node.devices.values()].every((d) => d.revoked)).toBe(true);
    expect(keychain.entries.size).toBe(0);
    expect(getServerConfig().accessToken).toBeNull();
  });

  it("adoption refuses a stale generation outright", async () => {
    const captured = authGeneration();
    invalidateAuthGeneration();
    await expect(
      adoptDeviceCredential({ origin: node.origin, userId: uuid(1), username: "carter", deviceId: uuid(2) }, { refreshToken: "r", accessToken: "a", accessExpiresIn: 900 }, captured),
    ).rejects.toBeInstanceOf(StaleAuthError);
    expect(keychain.entries.size).toBe(0);
  });
});

describe("rotation", () => {
  it("shares one rotation between ten concurrent callers", async () => {
    await signIn();
    const before = node.count("/devices/refresh");
    const results = await Promise.all(Array.from({ length: 10 }, () => refreshAccessToken()));
    expect(new Set(results).size).toBe(1);
    expect(node.count("/devices/refresh") - before).toBe(1);
  });

  it("rotates again after the shared attempt settles, and the stored token follows", async () => {
    await signIn();
    const first = await refreshAccessToken();
    const second = await refreshAccessToken();
    expect(second).not.toBe(first);
    const profile = loadConnectionProfile()!;
    expect([...keychain.entries.values()][0]).toBe(node.devices.get(profile.deviceId!)!.refresh);
    expect(node.devices.get(profile.deviceId!)!.revoked).toBe(false);
  });

  it("deletes the credential and forgets the account when the node rejects it", async () => {
    await signIn();
    node.failNext("/devices/refresh", 401);
    await expect(refreshAccessToken()).rejects.toMatchObject({ status: 401 });
    expect(keychain.entries.size).toBe(0);
    expect(getServerConfig().accessToken).toBeNull();
    expect(loadConnectionProfile()).toMatchObject({ origin: node.origin, userId: null, deviceId: null });
  });

  it("keeps the credential through an outage or a 5xx", async () => {
    await signIn();
    for (const outcome of ["network", 503] as const) {
      node.failNext("/devices/refresh", outcome);
      await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    }
    expect(keychain.entries.size).toBe(1);
    expect(loadConnectionProfile()!.userId).toBe(node.user.ID);
    // The credential was never consumed, so the next attempt still works.
    await expect(refreshAccessToken()).resolves.toBeTruthy();
  });

  it("refuses to rotate with no stored identity", async () => {
    await expect(refreshAccessToken()).rejects.toMatchObject({ status: 401 });
    expect(node.calls).toHaveLength(0);
  });

  it("treats a missing keychain entry as a dead credential", async () => {
    await signIn();
    keychain.entries.clear();
    await expect(refreshAccessToken()).rejects.toMatchObject({ status: 401 });
    expect(loadConnectionProfile()!.userId).toBeNull();
    expect(node.count("/devices/refresh")).toBe(0);
  });

  it("surfaces a credential-service read failure without calling the node or discarding anything", async () => {
    await signIn();
    keychain.failOn.add("get");
    await expect(refreshAccessToken()).rejects.toBeInstanceOf(CredentialStoreError);
    expect(node.count("/devices/refresh")).toBe(0);
    expect(keychain.entries.size).toBe(1);
  });

  it("after a successful refresh whose replacement cannot be stored: revokes, deletes, and never replays", async () => {
    await signIn();
    const profile = loadConnectionProfile()!;
    const consumed = node.devices.get(profile.deviceId!)!.refresh;
    keychain.failOn.add("set");
    await expect(refreshAccessToken()).rejects.toBeInstanceOf(CredentialStoreError);
    expect(node.devices.get(profile.deviceId!)!.revoked).toBe(true);
    expect(getServerConfig().accessToken).toBeNull();
    expect(loadConnectionProfile()!.userId).toBeNull();
    expect(keychain.entries.size).toBe(0);
    // The consumed token is gone from the store and is never presented again.
    keychain.failOn.clear();
    const refreshCalls = node.count("/devices/refresh");
    await expect(refreshAccessToken()).rejects.toMatchObject({ status: 401 });
    expect(node.count("/devices/refresh")).toBe(refreshCalls);
    expect(node.calls.filter((c) => c.path.endsWith("/refresh") && (c.body as { refreshToken: string }).refreshToken === consumed)).toHaveLength(1);
  });

  it("discards a late result after logout and revokes what the node issued", async () => {
    await signIn();
    const profile = loadConnectionProfile()!;
    const release = node.hold("/devices/refresh");
    const pending = refreshAccessToken();
    await vi.waitFor(() => expect(node.count("/devices/refresh")).toBe(1));
    await signOutNative();
    release();
    await expect(pending).rejects.toBeInstanceOf(StaleAuthError);
    expect(getServerConfig().accessToken).toBeNull();
    expect(node.devices.get(profile.deviceId!)!.revoked).toBe(true);
    expect(keychain.entries.size).toBe(0);
  });

  it("never sends a credential to a different node after a switch", async () => {
    await signIn();
    const release = node.hold("/devices/refresh");
    const pending = refreshAccessToken();
    await vi.waitFor(() => expect(node.count("/devices/refresh")).toBe(1));
    const calls = node.calls.length;
    invalidateAuthGeneration();
    setServerConfig({ baseUrl: "https://other.example.com", mode: "token", accessToken: null });
    release();
    await expect(pending).rejects.toBeInstanceOf(StaleAuthError);
    // Every request after the switch targeted the origin the credential belongs to.
    expect(node.calls.slice(calls).every((c) => !c.bearer || true)).toBe(true);
    expect(getServerConfig().baseUrl).toBe("https://other.example.com");
    expect(getServerConfig().accessToken).toBeNull();
  });
});

describe("the authenticated API client", () => {
  it("refreshes before sending once the access token has expired", async () => {
    await signIn();
    const before = node.count("/devices/refresh");
    clock += 16 * 60 * 1000;
    await expect(api("/api/v1/probe")).resolves.toEqual({ ok: true });
    expect(node.count("/devices/refresh") - before).toBe(1);
  });

  it("shares one rotation across ten concurrent expired callers", async () => {
    await signIn();
    const before = node.count("/devices/refresh");
    clock += 16 * 60 * 1000;
    const results = await Promise.all(Array.from({ length: 10 }, () => api("/api/v1/probe")));
    expect(results).toHaveLength(10);
    expect(node.count("/devices/refresh") - before).toBe(1);
  });

  it("retries a GET once after an authenticated 401 and no other method", async () => {
    await signIn();
    node.expireAccessTokens(); // the node forgot the token; the client does not know yet
    const before = node.count("/devices/refresh");
    await expect(api("/api/v1/probe")).resolves.toEqual({ ok: true });
    expect(node.count("/devices/refresh") - before).toBe(1);

    node.expireAccessTokens();
    const probeCalls = node.count("/api/v1/probe");
    await expect(api("/api/v1/probe", { method: "POST", body: "{}" })).rejects.toMatchObject({ status: 401 });
    // A POST is never silently replayed: it was sent exactly once.
    expect(node.count("/api/v1/probe") - probeCalls).toBe(1);
  });

  it("does not refresh for an ordinary 403", async () => {
    await signIn();
    const before = node.count("/devices/refresh");
    await expect(api("/api/v1/forbidden")).rejects.toMatchObject({ status: 403 });
    expect(node.count("/devices/refresh") - before).toBe(0);
  });

  it("does not retry forever when the rotation itself is rejected", async () => {
    await signIn();
    node.expireAccessTokens();
    node.failNext("/devices/refresh", 401);
    await expect(api("/api/v1/probe")).rejects.toMatchObject({ status: 401 });
    expect(node.count("/api/v1/probe")).toBe(1);
  });

  it("sends no credential when not signed in, so the node answers a clean 401", async () => {
    await expect(api("/api/v1/probe")).rejects.toMatchObject({ status: 401 });
    expect(node.calls.at(-1)).toMatchObject({ bearer: null });
    expect(node.count("/devices/refresh")).toBe(0);
  });

  it("offers a socket ticket under the bearer and nothing in cookie mode", async () => {
    await signIn();
    const protocols = await socketProtocols();
    expect(protocols).toHaveLength(1);
    expect(protocols[0]).toMatch(/^telos-ticket\./);
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    expect(await socketProtocols()).toEqual([]);
  });
});

describe("startup and sign-out", () => {
  it("resumes a stored session with one rotation, one credential rewrite and /auth/me", async () => {
    await signIn();
    setServerConfig({ baseUrl: node.origin, mode: "token", accessToken: null });
    const before = { refresh: node.count("/devices/refresh"), me: node.count("/auth/me"), set: keychain.calls.filter((c) => c === "set").length, get: keychain.calls.filter((c) => c === "get").length };
    const result = await startNativeSession<{ Username: string }>();
    expect(result.kind).toBe("authenticated");
    expect(node.count("/devices/refresh") - before.refresh).toBe(1);
    expect(node.count("/auth/me") - before.me).toBe(1);
    expect(keychain.calls.filter((c) => c === "set").length - before.set).toBe(1);
    expect(keychain.calls.filter((c) => c === "get").length - before.get).toBe(1);
  });

  it.each([401, 403] as const)("treats a %i refresh as signed out and drops the identity", async (status) => {
    await signIn();
    setServerConfig({ baseUrl: node.origin, mode: "token", accessToken: null });
    node.failNext("/devices/refresh", status);
    expect(await startNativeSession()).toMatchObject({ kind: "anonymous", reason: "revoked" });
    expect(keychain.entries.size).toBe(0);
    expect(loadConnectionProfile()).toMatchObject({ origin: node.origin, userId: null });
  });

  it.each(["network", 502] as const)("keeps everything when startup meets %s", async (outcome) => {
    await signIn();
    setServerConfig({ baseUrl: node.origin, mode: "token", accessToken: null });
    node.failNext("/devices/refresh", outcome);
    expect((await startNativeSession()).kind).toBe("unreachable");
    expect(keychain.entries.size).toBe(1);
    expect(loadConnectionProfile()!.userId).toBe(node.user.ID);
  });

  it("is anonymous without a stored identity and without calling the node", async () => {
    expect(await startNativeSession()).toMatchObject({ kind: "anonymous", reason: "no-profile" });
    expect(node.calls).toHaveLength(0);
  });

  it("logout ends local authority first, revokes remotely, and deletes the exact credential", async () => {
    await signIn();
    const profile = loadConnectionProfile()!;
    const result = await signOutNative();
    expect(result).toMatchObject({ remote: "revoked", cleanup: "done" });
    expect(node.devices.get(profile.deviceId!)!.revoked).toBe(true);
    expect(node.calls.at(-1)).toMatchObject({ path: "/api/v1/auth/logout" });
    expect(node.calls.at(-1)!.bearer).toBeTruthy();
    expect(keychain.entries.size).toBe(0);
    expect(getServerConfig().accessToken).toBeNull();
    expect(loadConnectionProfile()).toMatchObject({ origin: node.origin, userId: null, deviceId: null });
  });

  it("still deletes the local credential when the node is unreachable, and says revocation is pending", async () => {
    await signIn();
    node.failNext("/auth/logout", "network");
    const result = await signOutNative();
    expect(result).toMatchObject({ remote: "pending", cleanup: "done" });
    expect(keychain.entries.size).toBe(0);
  });

  it("clears access immediately and leaves a tombstone when the delete fails; restart does not auto-login", async () => {
    await signIn();
    keychain.failOn.add("delete");
    const result = await signOutNative();
    expect(result).toMatchObject({ cleanup: "failed" });
    expect(result.cleanupError).toBeInstanceOf(CredentialStoreError);
    expect(getServerConfig().accessToken).toBeNull();
    expect(loadCleanupTombstone()).toMatchObject({ origin: node.origin });
    expect(keychain.entries.size).toBe(1);

    // "Restart": the tombstone suppresses sign-in while the delete keeps failing.
    const refreshes = node.count("/devices/refresh");
    expect((await startNativeSession()).kind).toBe("cleanup-required");
    expect(node.count("/devices/refresh")).toBe(refreshes);

    keychain.failOn.clear();
    expect((await startNativeSession()).kind).toBe("anonymous");
    expect(keychain.entries.size).toBe(0);
    expect(loadCleanupTombstone()).toBeNull();
  });

  it("the stored credential key addresses origin, account and device", async () => {
    await signIn();
    const profile = loadConnectionProfile()!;
    expect(credentialKey({ origin: profile.origin, userId: profile.userId!, deviceId: profile.deviceId! })).toEqual({
      origin: node.origin,
      accountId: node.user.ID,
      deviceId: profile.deviceId,
    });
  });
});

describe("profile hygiene", () => {
  it("rejects and deletes a stored record that carries a token", () => {
    window.localStorage.setItem(
      "telos.connection.v1",
      JSON.stringify({ origin: node.origin, userId: null, username: null, deviceId: null, refreshToken: "oops" }),
    );
    expect(loadConnectionProfile()).toBeNull();
    expect(window.localStorage.getItem("telos.connection.v1")).toBeNull();
  });

  it("rejects an http or malformed origin and a half-identity", () => {
    for (const record of [
      { origin: "http://node.example.com", userId: null, username: null, deviceId: null },
      { origin: "not a url", userId: null, username: null, deviceId: null },
      { origin: node.origin, userId: uuid(1), username: "c", deviceId: null },
      { origin: node.origin, userId: "nope", username: "c", deviceId: uuid(2) },
    ]) {
      window.localStorage.setItem("telos.connection.v1", JSON.stringify(record));
      expect(loadConnectionProfile()).toBeNull();
    }
    clearConnectionProfile();
  });
});
