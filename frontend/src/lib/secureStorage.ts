// Durable storage for the two things this client must not lose between
// launches: the device refresh token and the certificate pins.
//
// The persistent half is a bridge the native shell installs, in the same shape
// and for the same reason as __TELOS_NATIVE_TLS__ in certPinning.ts — the static
// export is shared by the web build, which must not import Tauri modules at all.
//
// The name matters and was got wrong once. An earlier version read
// window.__TAURI_OS_PLUGIN_STORE__, which is not a global Tauri defines: its
// store plugin is reached through @tauri-apps/plugin-store over invoke(). The
// tests set that global themselves and so proved only that the code reads a
// name they had just invented. In the real shell the lookup would have missed
// every time and everything would have fallen through to the in-memory map
// below — silently, because the fallback works. The visible symptom would have
// been a client that forgets its refresh token on every launch, registers a
// fresh device each time it starts, and treats every connection as first-use so
// certificate pinning never detects anything.

/**
 * The contract the native shell must satisfy for anything to survive a restart.
 * Installed on `window` by the shell before the frontend loads.
 */
export interface NativeSecretStore {
  get: (key: string) => Promise<string | null>;
  set: (key: string, value: string) => Promise<void>;
  delete: (key: string) => Promise<void>;
}

interface NativeWindow {
  __TELOS_NATIVE_STORE__?: NativeSecretStore;
}

// Fallback for the web build, which has no keychain to write to and must not
// use localStorage/sessionStorage for credentials. Process-lifetime only: a
// browser tab that reloads starts empty, which is correct there — a browser
// authenticates with its session cookie and stores no device credential.
const memoryStore = new Map<string, string>();

function nativeStore(): NativeSecretStore | null {
  if (typeof window === "undefined") return null;
  return (window as unknown as NativeWindow).__TELOS_NATIVE_STORE__ ?? null;
}

/** Whether secrets written here will outlive the process. */
export function secretsPersist(): boolean {
  return nativeStore() !== null;
}

export async function getSecret(key: string): Promise<string | null> {
  const store = nativeStore();
  if (store) {
    try {
      return await store.get(key);
    } catch {
      return memoryStore.get(key) ?? null;
    }
  }
  return memoryStore.get(key) ?? null;
}

export async function setSecret(key: string, value: string): Promise<void> {
  const store = nativeStore();
  if (store) {
    try {
      await store.set(key, value);
      return;
    } catch {
      memoryStore.set(key, value);
      return;
    }
  }
  memoryStore.set(key, value);
}

export async function deleteSecret(key: string): Promise<void> {
  const store = nativeStore();
  if (store) {
    try {
      await store.delete(key);
    } catch {
      memoryStore.delete(key);
    }
  }
  memoryStore.delete(key);
}
