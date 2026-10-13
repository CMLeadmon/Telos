// Durable storage for the one secret this client must keep between launches: the
// device refresh token.
//
// In the native shell it lives in the operating-system credential service (the
// Secret Service on Linux), reached through a bridge the shell installs on
// `window`. The page names a structured key -- (node origin, account, device) --
// and the shell derives every keychain string itself, so the page can never
// address another application's secrets or another node's credentials.
//
// Native failures REJECT. There is no in-memory or file fallback in native mode:
// a client that quietly cannot persist its refresh token would forget it on exit
// and register a fresh device on every launch, and a rejected sign-in the member
// can see is strictly better than that. The web build has no keychain and no
// device credential (the browser authenticates with its session cookie); the
// in-process map below exists only so isolated unit tests and development builds
// can exercise the interface, and token-mode authentication never reaches it.

import { isNativeApp } from "./nativeEnv";

/** The structured address of one credential. Mirrors `CredentialKey` in credentials.rs. */
export interface CredentialKey {
  origin: string;
  accountId: string;
  deviceId: string;
}

/**
 * The contract the native shell satisfies. Installed on `window` by the shell
 * before the frontend loads. The shell rejects with strings of the form
 * `<code>: <sentence>`; `legacyStoreRetired` reports (once) that the previous
 * plaintext secret file was found and deleted at launch.
 */
export interface NativeCredentialBridge {
  get: (key: CredentialKey) => Promise<string | null>;
  set: (key: CredentialKey, secret: string) => Promise<void>;
  delete: (key: CredentialKey) => Promise<void>;
  legacyStoreRetired?: () => Promise<boolean>;
}

interface NativeWindow {
  __TELOS_NATIVE_CREDENTIALS__?: NativeCredentialBridge;
}

/** A credential-service failure the member can act on. Never carries a secret. */
export class CredentialStoreError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = "CredentialStoreError";
    this.code = code;
  }
}

const memoryStore = new Map<string, string>();

function memoryKey(key: CredentialKey): string {
  return JSON.stringify([key.origin, key.accountId, key.deviceId]);
}

function bridge(): NativeCredentialBridge | null {
  if (typeof window === "undefined") return null;
  return (window as unknown as NativeWindow).__TELOS_NATIVE_CREDENTIALS__ ?? null;
}

/** Whether credentials written here will outlive the process. */
export function credentialsPersist(): boolean {
  return bridge() !== null;
}

function toStoreError(error: unknown): CredentialStoreError {
  if (error instanceof CredentialStoreError) return error;
  // The shell rejects with a "code: sentence" string; an Error wrapping one is
  // handled the same way. Anything else is reported generically -- the raw value
  // is not echoed, because a failing call's argument list could hold the secret.
  const text = typeof error === "string" ? error : error instanceof Error ? error.message : "";
  const match = /^(credential_[a-z_]+): (.+)$/s.exec(text);
  if (match) return new CredentialStoreError(match[1], match[2]);
  return new CredentialStoreError(
    "credential_store_error",
    "The operating-system credential service reported an error.",
  );
}

function missingBridge(): CredentialStoreError {
  return new CredentialStoreError(
    "credential_store_unavailable",
    "This app cannot reach the operating-system credential service, so it cannot keep you signed in.",
  );
}

export async function getCredential(key: CredentialKey): Promise<string | null> {
  const native = bridge();
  if (native) {
    try {
      return await native.get(key);
    } catch (error) {
      throw toStoreError(error);
    }
  }
  if (isNativeApp()) throw missingBridge();
  return memoryStore.get(memoryKey(key)) ?? null;
}

export async function setCredential(key: CredentialKey, secret: string): Promise<void> {
  const native = bridge();
  if (native) {
    try {
      await native.set(key, secret);
      return;
    } catch (error) {
      throw toStoreError(error);
    }
  }
  if (isNativeApp()) throw missingBridge();
  memoryStore.set(memoryKey(key), secret);
}

export async function deleteCredential(key: CredentialKey): Promise<void> {
  const native = bridge();
  if (native) {
    try {
      await native.delete(key);
      return;
    } catch (error) {
      throw toStoreError(error);
    }
  }
  if (isNativeApp()) throw missingBridge();
  memoryStore.delete(memoryKey(key));
}

/**
 * True once after a launch that deleted the previous shell's plaintext secret
 * file, so the app can say why the member has to sign in again. Absent bridge or
 * a failing call is simply "no notice".
 */
export async function consumeLegacyStoreNotice(): Promise<boolean> {
  const native = bridge();
  if (!native?.legacyStoreRetired) return false;
  try {
    return (await native.legacyStoreRetired()) === true;
  } catch {
    return false;
  }
}

/** Test helper: clears the isolated in-process map. */
export function __resetMemoryCredentialsForTests(): void {
  memoryStore.clear();
}
