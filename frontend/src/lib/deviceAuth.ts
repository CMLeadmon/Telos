// The native client's token lifecycle: register a device, keep its refresh token
// in the operating-system credential service, rotate it, and sign out.
//
// Browsers never run this. A browser authenticates with its HttpOnly session
// cookie and `getServerConfig().mode` stays "cookie"; the code that follows is
// reached only in the desktop shell, and token mode NEVER falls back to cookies.
//
// Three rules shape everything here:
//
// 1. A refresh token is single-use, and the server treats a second presentation
//    as a leak and revokes the whole device. So one rotation is shared by every
//    concurrent caller, and a consumed token is never presented again -- not even
//    when storing its replacement fails.
//
// 2. Every operation captures an immutable "auth generation" when it starts and
//    checks it before adopting any result. Logging out or switching nodes bumps
//    the generation, so a late response from the old node can neither resurrect
//    the session nor send a credential to the new node.
//
// 3. Failures are explicit. A credential-service error is surfaced, never papered
//    over with in-memory storage, and a failed local cleanup leaves a non-secret
//    tombstone that blocks automatic sign-in until the deletion succeeds.

import { ApiError, api, setAccessRefresher, type AccessRefresher } from "./api";
import {
  clearCleanupTombstone,
  clearConnectionIdentity,
  loadCleanupTombstone,
  loadConnectionProfile,
  saveCleanupTombstone,
  saveConnectionProfile,
  type ConnectionProfile,
} from "./connectionProfile";
import { revokeAllTrackedObjectUrls } from "./objectUrls";
import { getServerConfig, setServerConfig } from "./serverConfig";
import {
  CredentialStoreError,
  deleteCredential,
  getCredential,
  setCredential,
  type CredentialKey,
} from "./secureStorage";

export interface DeviceRegistration {
  deviceId: string;
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

interface TokenPair {
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

/** The identity of a signed-in native client: a profile with every field known. */
export interface SignedInProfile {
  origin: string;
  userId: string;
  username: string;
  deviceId: string;
}

/** An operation finished after the world it started in was abandoned. */
export class StaleAuthError extends Error {
  constructor() {
    super("The sign-in state changed while this request was running.");
    this.name = "StaleAuthError";
  }
}

// --------------------------------------------------------------- generation

let generation = 0;
const inFlight = new Map<string, Promise<string>>();

export function authGeneration(): number {
  return generation;
}

/**
 * Abandons every operation started so far. Pending refreshes are forgotten (their
 * results are discarded on arrival) and the in-memory access token is cleared.
 */
export function invalidateAuthGeneration(): number {
  generation += 1;
  inFlight.clear();
  accessDeadline = null;
  // Protected images fetched under the previous authority must not stay readable.
  revokeAllTrackedObjectUrls();
  return generation;
}

function assertCurrent(captured: number): void {
  if (captured !== generation) throw new StaleAuthError();
}

// ------------------------------------------------------------ access token

// The access token lives in process memory only (the server config), never in
// storage. Its deadline is measured on a monotonic clock where one exists, so a
// wall-clock change cannot make an expired token look fresh.
const EXPIRY_MARGIN_MS = 30_000;
let accessDeadline: number | null = null;

function monotonicNow(): number {
  return typeof performance !== "undefined" && typeof performance.now === "function" ? performance.now() : Date.now();
}

export function installAccessToken(accessToken: string, expiresInSeconds: number): void {
  setServerConfig({ ...getServerConfig(), mode: "token", accessToken });
  accessDeadline = monotonicNow() + Math.max(0, expiresInSeconds * 1000 - EXPIRY_MARGIN_MS);
}

export function clearAccessToken(): void {
  setServerConfig({ ...getServerConfig(), accessToken: null });
  accessDeadline = null;
}

function accessIsFresh(): boolean {
  const { accessToken } = getServerConfig();
  if (!accessToken) return false;
  return accessDeadline === null || monotonicNow() < accessDeadline;
}

// ----------------------------------------------------------------- helpers

function isSignedIn(profile: ConnectionProfile | null): profile is SignedInProfile {
  return !!profile && profile.userId !== null && profile.username !== null && profile.deviceId !== null;
}

export function credentialKey(profile: Pick<SignedInProfile, "origin" | "userId" | "deviceId">): CredentialKey {
  return { origin: profile.origin, accountId: profile.userId, deviceId: profile.deviceId };
}

/** Revokes a device with a captured token, ignoring any failure. True if it worked. */
export async function revokeDeviceBestEffort(origin: string, deviceId: string, accessToken: string): Promise<boolean> {
  try {
    await api(
      `/api/v1/users/me/devices/${encodeURIComponent(deviceId)}`,
      { method: "DELETE" },
      { baseUrl: origin, bearer: accessToken, skipRefresh: true },
    );
    return true;
  } catch {
    return false;
  }
}

/** Deletes a credential, leaving a tombstone when the credential service refuses. */
async function deleteOrTombstone(key: CredentialKey): Promise<boolean> {
  try {
    await deleteCredential(key);
    return true;
  } catch {
    saveCleanupTombstone({ origin: key.origin, accountId: key.accountId, deviceId: key.deviceId });
    return false;
  }
}

// ------------------------------------------------------------ registration

/**
 * Exchanges a username and password for a device credential. Returns the issued
 * tokens WITHOUT persisting or installing them; adopting them is a separate,
 * generation-checked step so a failure to store the refresh token can undo the
 * registration instead of leaving an orphaned device.
 *
 * The password is in the body because a client loaded from disk has no cookie to
 * present, and this is the only call in the client that carries one. No
 * credential of any kind is attached to the request itself.
 */
export async function registerDevice(
  deviceName: string,
  platform: string,
  clientVersion: string,
  credentials: { username: string; password: string },
  signal?: AbortSignal,
): Promise<DeviceRegistration> {
  return api<DeviceRegistration>(
    "/api/v1/auth/devices/register",
    {
      method: "POST",
      body: JSON.stringify({
        deviceName,
        platform,
        clientVersion,
        username: credentials.username,
        password: credentials.password,
      }),
      signal,
    },
    { bearer: null, skipRefresh: true },
  );
}

/**
 * Persists a freshly issued pair and makes it current: refresh token to the OS
 * credential service FIRST, access token to memory only after that succeeded,
 * profile last. Throws {@link CredentialStoreError} if the credential cannot be
 * stored, and {@link StaleAuthError} if the world changed underneath it.
 */
export async function adoptDeviceCredential(profile: SignedInProfile, tokens: TokenPair, captured: number): Promise<void> {
  assertCurrent(captured);
  const key = credentialKey(profile);
  await setCredential(key, tokens.refreshToken);
  if (captured !== generation) {
    await deleteOrTombstone(key);
    throw new StaleAuthError();
  }
  installAccessToken(tokens.accessToken, tokens.accessExpiresIn);
  saveConnectionProfile(profile);
}

/**
 * Token login: register this install, learn who the node says we are, then adopt
 * the credential. If any step after registration fails, the new device is revoked
 * and local state cleared, so a failed sign-in never leaves a live credential.
 */
export async function signInWithPassword<T extends { ID: string; Username: string }>(
  device: { name: string; platform: string; version: string },
  credentials: { username: string; password: string },
  signal?: AbortSignal,
): Promise<T> {
  const captured = generation;
  const origin = getServerConfig().baseUrl;
  if (!origin) throw new ApiError(0, "Choose a Telos node before signing in.");

  const registration = await registerDevice(device.name, device.platform, device.version, credentials, signal);
  if (captured !== generation) {
    void revokeDeviceBestEffort(origin, registration.deviceId, registration.accessToken);
    throw new StaleAuthError();
  }

  installAccessToken(registration.accessToken, registration.accessExpiresIn);
  try {
    const me = await api<T>("/api/v1/auth/me", { signal }, { bearer: registration.accessToken, skipRefresh: true, baseUrl: origin });
    assertCurrent(captured);
    await adoptDeviceCredential(
      { origin, userId: me.ID, username: me.Username, deviceId: registration.deviceId },
      registration,
      captured,
    );
    return me;
  } catch (error) {
    await revokeDeviceBestEffort(origin, registration.deviceId, registration.accessToken);
    if (captured === generation) {
      clearAccessToken();
      clearConnectionIdentity();
    }
    throw error;
  }
}

// ---------------------------------------------------------------- rotation

/**
 * Rotates the stored refresh token and installs a fresh access token. One
 * rotation is shared by every caller asking for the same (origin, account,
 * device) within one generation.
 */
export function refreshAccessToken(): Promise<string> {
  const profile = loadConnectionProfile();
  if (!isSignedIn(profile)) {
    return Promise.reject(new ApiError(401, "No device credential is stored for this server."));
  }
  const captured = generation;
  const key = credentialKey(profile);
  const id = `${captured}|${key.origin}|${key.accountId}|${key.deviceId}`;
  const existing = inFlight.get(id);
  if (existing) return existing;
  const attempt: Promise<string> = rotate(profile, key, captured).finally(() => {
    if (inFlight.get(id) === attempt) inFlight.delete(id);
  });
  inFlight.set(id, attempt);
  return attempt;
}

/** The credential is dead (rejected, or missing): forget it and who it belonged to. */
async function forgetDeadCredential(key: CredentialKey, captured: number): Promise<void> {
  await deleteOrTombstone(key);
  if (captured === generation) {
    clearAccessToken();
    clearConnectionIdentity();
  }
}

async function rotate(profile: SignedInProfile, key: CredentialKey, captured: number): Promise<string> {
  const origin = profile.origin;
  const refreshToken = await getCredential(key);
  assertCurrent(captured);
  if (!refreshToken) {
    await forgetDeadCredential(key, captured);
    throw new ApiError(401, "No device credential is stored for this server.");
  }

  let next: TokenPair;
  try {
    next = await api<TokenPair>(
      "/api/v1/auth/devices/refresh",
      { method: "POST", body: JSON.stringify({ refreshToken }) },
      { baseUrl: origin, bearer: null, skipRefresh: true },
    );
  } catch (error) {
    // Logged out or switched nodes while the request was in flight: whatever the
    // node answered belongs to a session nobody is waiting for.
    if (captured !== generation) throw new StaleAuthError();
    // A rejected refresh means the credential is dead or was replayed, and
    // retaining it would loop the client through failing refreshes forever.
    // Only on rejection: an unreachable node or a 5xx says nothing about the
    // credential, and discarding it there would turn a passing outage into a
    // re-registration.
    if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
      await forgetDeadCredential(key, captured);
    }
    throw error;
  }

  // The old refresh token is consumed from here on. Whatever happens next, it is
  // never presented again.
  if (captured !== generation) {
    // Logged out or switched nodes while the request was in flight: the result
    // belongs to an abandoned session. Revoke what the server just issued.
    void revokeDeviceBestEffort(origin, key.deviceId, next.accessToken);
    await deleteOrTombstone(key);
    throw new StaleAuthError();
  }

  try {
    await setCredential(key, next.refreshToken);
  } catch (error) {
    void revokeDeviceBestEffort(origin, key.deviceId, next.accessToken);
    await deleteOrTombstone(key);
    clearAccessToken();
    clearConnectionIdentity();
    throw error;
  }
  if (captured !== generation) {
    void revokeDeviceBestEffort(origin, key.deviceId, next.accessToken);
    await deleteOrTombstone(key);
    throw new StaleAuthError();
  }

  installAccessToken(next.accessToken, next.accessExpiresIn);
  return next.accessToken;
}

// The hook api.ts calls so long-running desktop sessions keep working: refresh
// before an expired token is sent, and once after an authenticated GET is
// rejected. register/refresh/logout pass `skipRefresh` and never reach this.
const refresher: AccessRefresher = {
  async ensureFresh() {
    const cfg = getServerConfig();
    if (cfg.mode !== "token") return;
    if (accessIsFresh()) return;
    if (!isSignedIn(loadConnectionProfile())) return; // not signed in: send bare, get a clean 401
    await refreshAccessToken();
  },
  async refreshAfter401(usedToken) {
    const cfg = getServerConfig();
    if (cfg.mode !== "token") return false;
    // Someone else already rotated while this request was in flight: just retry.
    if (cfg.accessToken !== usedToken && cfg.accessToken !== null) return true;
    if (!isSignedIn(loadConnectionProfile())) return false;
    try {
      await refreshAccessToken();
      return true;
    } catch {
      return false;
    }
  },
};
setAccessRefresher(refresher);

// ---------------------------------------------------------------- startup

export type NativeStartup<T = unknown> =
  | { kind: "anonymous"; reason: "no-profile" | "revoked" }
  | { kind: "cleanup-required"; error: CredentialStoreError | null }
  | { kind: "credential-error"; error: CredentialStoreError }
  | { kind: "unreachable"; error: ApiError }
  | { kind: "stale" }
  | { kind: "authenticated"; me: T };

/** Tries to finish a credential deletion that failed earlier. True once nothing is left to clean. */
export async function retryCredentialCleanup(): Promise<{ done: boolean; error: CredentialStoreError | null }> {
  const tombstone = loadCleanupTombstone();
  if (!tombstone) return { done: true, error: null };
  try {
    await deleteCredential({ origin: tombstone.origin, accountId: tombstone.accountId, deviceId: tombstone.deviceId });
  } catch (error) {
    return {
      done: false,
      error:
        error instanceof CredentialStoreError
          ? error
          : new CredentialStoreError("credential_store_error", "The operating-system credential service reported an error."),
    };
  }
  clearCleanupTombstone();
  return { done: true, error: null };
}

/**
 * Native startup: finish any pending cleanup, then (only with a stored identity)
 * rotate the refresh token once and ask the node who we are. A rejected refresh
 * deletes the credential; an unreachable node or a 5xx keeps everything and
 * reports "unreachable" so the caller can offer a retry instead of a re-login.
 */
export async function startNativeSession<T>(): Promise<NativeStartup<T>> {
  const cleanup = await retryCredentialCleanup();
  if (!cleanup.done) return { kind: "cleanup-required", error: cleanup.error };

  const profile = loadConnectionProfile();
  if (!isSignedIn(profile)) return { kind: "anonymous", reason: "no-profile" };

  const captured = generation;
  if (getServerConfig().baseUrl !== profile.origin || getServerConfig().mode !== "token") {
    setServerConfig({ baseUrl: profile.origin, mode: "token", accessToken: null });
  }
  try {
    await refreshAccessToken();
    assertCurrent(captured);
    const me = await api<T>("/api/v1/auth/me");
    assertCurrent(captured);
    return { kind: "authenticated", me };
  } catch (error) {
    if (error instanceof StaleAuthError) return { kind: "stale" };
    if (error instanceof CredentialStoreError) return { kind: "credential-error", error };
    if (error instanceof ApiError) {
      if (error.status === 401 || error.status === 403) return { kind: "anonymous", reason: "revoked" };
      return { kind: "unreachable", error };
    }
    throw error;
  }
}

// ---------------------------------------------------------------- sign out

export interface NativeSignOut {
  /** Whether the node acknowledged the revocation. "pending" means it could not be reached. */
  remote: "revoked" | "pending";
  /** Whether the local credential was deleted. */
  cleanup: "done" | "failed";
  cleanupError: CredentialStoreError | null;
}

/**
 * Native logout. Local authority ends IMMEDIATELY (generation bump, access token
 * cleared) so nothing can resurrect the session while the network call runs; the
 * node is then asked, best effort, to revoke the device with the captured token;
 * finally the exact OS credential is deleted. A deletion failure never claims
 * success: it leaves a tombstone that suppresses automatic sign-in.
 */
export async function signOutNative(): Promise<NativeSignOut> {
  const profile = loadConnectionProfile();
  const cfg = getServerConfig();
  const token = cfg.accessToken;
  const origin = profile?.origin || cfg.baseUrl;

  invalidateAuthGeneration();
  clearAccessToken();

  let remote: NativeSignOut["remote"] = "pending";
  if (token && origin) {
    try {
      await api("/api/v1/auth/logout", { method: "POST" }, { baseUrl: origin, bearer: token, skipRefresh: true });
      remote = "revoked";
    } catch {
      remote = "pending";
    }
  }

  let cleanup: NativeSignOut["cleanup"] = "done";
  let cleanupError: CredentialStoreError | null = null;
  if (isSignedIn(profile)) {
    const key = credentialKey(profile);
    try {
      await deleteCredential(key);
    } catch (error) {
      cleanup = "failed";
      cleanupError =
        error instanceof CredentialStoreError
          ? error
          : new CredentialStoreError("credential_store_error", "The operating-system credential service reported an error.");
      saveCleanupTombstone({ origin: key.origin, accountId: key.accountId, deviceId: key.deviceId });
    }
  }
  clearConnectionIdentity();
  return { remote, cleanup, cleanupError };
}

// ------------------------------------------------------------------ sockets

/** Obtains a short-lived single-use ticket for a WebSocket upgrade. */
export async function acquireWSTicket(): Promise<string> {
  const { ticket } = await api<{ ticket: string; expiresIn: number }>("/api/v1/auth/ws-ticket", { method: "POST" });
  return ticket;
}

/**
 * Subprotocol list for a socket upgrade. Empty in cookie mode, where the browser
 * attaches the session cookie to the handshake and a ticket would be redundant.
 * A ticket is single-use, so this is called once per connection attempt.
 */
export async function socketProtocols(): Promise<string[]> {
  if (getServerConfig().mode !== "token") return [];
  return [`telos-ticket.${await acquireWSTicket()}`];
}
