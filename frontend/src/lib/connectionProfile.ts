// Non-secret facts the native client remembers about the node it is signed in to.
//
// A profile holds the normalized node origin, the account (user id, username) and
// the device id -- exactly what is needed to ADDRESS the refresh token in the
// operating-system credential service, and nothing that could authenticate by
// itself. It never contains a token, password, invite, cookie, certificate
// fingerprint or authorization header, and a stored record that does is deleted
// rather than trusted.
//
// Stored in WebView localStorage under one key, and only in the native client:
// a browser tab authenticates with its cookie and remembers nothing here.

import { isNativeApp } from "./nativeEnv";
import { NodeUrlError, normalizeTrustedNodeUrl } from "./serverConfig";

export const CONNECTION_PROFILE_KEY = "telos.connection.v1";
export const CREDENTIAL_CLEANUP_KEY = "telos.credential-cleanup.v1";

export interface ConnectionProfile {
  origin: string;
  userId: string | null;
  username: string | null;
  deviceId: string | null;
}

/** A credential whose deletion failed; suppresses automatic sign-in until it succeeds. */
export interface CleanupTombstone {
  origin: string;
  accountId: string;
  deviceId: string;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const PROFILE_FIELDS = ["origin", "userId", "username", "deviceId"];

function storage(): Storage | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage;
  } catch {
    return null;
  }
}

function readJSON(key: string): unknown {
  const text = storage()?.getItem(key);
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    storage()?.removeItem(key);
    return undefined;
  }
}

function validOrigin(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  try {
    const normalized = normalizeTrustedNodeUrl(raw);
    return normalized === raw ? normalized : null;
  } catch (error) {
    if (error instanceof NodeUrlError) return null;
    return null;
  }
}

export function loadConnectionProfile(): ConnectionProfile | null {
  if (!isNativeApp()) return null;
  const parsed = readJSON(CONNECTION_PROFILE_KEY);
  if (parsed === undefined) return null;
  const reject = () => {
    storage()?.removeItem(CONNECTION_PROFILE_KEY);
    return null;
  };
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return reject();
  const record = parsed as Record<string, unknown>;
  // Closed record: an unexpected field is how a token would sneak in.
  if (Object.keys(record).some((k) => !PROFILE_FIELDS.includes(k))) return reject();
  const origin = validOrigin(record.origin);
  if (!origin) return reject();
  const userId = record.userId ?? null;
  const deviceId = record.deviceId ?? null;
  const username = record.username ?? null;
  if (userId !== null && (typeof userId !== "string" || !UUID.test(userId))) return reject();
  if (deviceId !== null && (typeof deviceId !== "string" || !UUID.test(deviceId))) return reject();
  if (username !== null && (typeof username !== "string" || username.length === 0 || username.length > 64)) return reject();
  // An account without a device (or the reverse) cannot address a credential.
  if ((userId === null) !== (deviceId === null)) return reject();
  return { origin, userId, username, deviceId };
}

export function saveConnectionProfile(profile: ConnectionProfile): void {
  if (!isNativeApp()) return;
  const origin = validOrigin(profile.origin);
  if (!origin) throw new Error("A connection profile needs a normalized HTTPS node origin.");
  const record: ConnectionProfile = {
    origin,
    userId: profile.userId,
    username: profile.username,
    deviceId: profile.deviceId,
  };
  storage()?.setItem(CONNECTION_PROFILE_KEY, JSON.stringify(record));
}

/** Forgets who is signed in but remembers which node to talk to. */
export function clearConnectionIdentity(): void {
  const profile = loadConnectionProfile();
  if (!profile) return;
  saveConnectionProfile({ origin: profile.origin, userId: null, username: null, deviceId: null });
}

export function clearConnectionProfile(): void {
  storage()?.removeItem(CONNECTION_PROFILE_KEY);
}

export function loadCleanupTombstone(): CleanupTombstone | null {
  if (!isNativeApp()) return null;
  const parsed = readJSON(CREDENTIAL_CLEANUP_KEY);
  if (parsed === undefined) return null;
  const record = parsed as Record<string, unknown> | null;
  const origin = record && typeof record === "object" ? validOrigin(record.origin) : null;
  if (
    !record ||
    !origin ||
    Object.keys(record).sort().join() !== "accountId,deviceId,origin" ||
    typeof record.accountId !== "string" ||
    !UUID.test(record.accountId) ||
    typeof record.deviceId !== "string" ||
    !UUID.test(record.deviceId)
  ) {
    storage()?.removeItem(CREDENTIAL_CLEANUP_KEY);
    return null;
  }
  return { origin, accountId: record.accountId, deviceId: record.deviceId };
}

export function saveCleanupTombstone(tombstone: CleanupTombstone): void {
  if (!isNativeApp()) return;
  storage()?.setItem(
    CREDENTIAL_CLEANUP_KEY,
    JSON.stringify({ origin: tombstone.origin, accountId: tombstone.accountId, deviceId: tombstone.deviceId }),
  );
}

export function clearCleanupTombstone(): void {
  storage()?.removeItem(CREDENTIAL_CLEANUP_KEY);
}
