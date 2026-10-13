import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  CredentialStoreError,
  __resetMemoryCredentialsForTests,
  consumeLegacyStoreNotice,
  credentialsPersist,
  deleteCredential,
  getCredential,
  setCredential,
  type CredentialKey,
  type NativeCredentialBridge,
} from "@/lib/secureStorage";

type BridgeWindow = {
  __TELOS_NATIVE_CREDENTIALS__?: NativeCredentialBridge;
  __TELOS_NATIVE_SHELL__?: unknown;
};

const KEY: CredentialKey = {
  origin: "https://node.example.com",
  accountId: "0f8fad5b-d9cb-469f-a165-70867728950e",
  deviceId: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
};
const OTHER_DEVICE: CredentialKey = { ...KEY, deviceId: "99999999-2222-3333-4444-555555555555" };

function installBridge(bridge: NativeCredentialBridge) {
  (window as unknown as BridgeWindow).__TELOS_NATIVE_CREDENTIALS__ = bridge;
}

function clearBridge() {
  delete (window as unknown as BridgeWindow).__TELOS_NATIVE_CREDENTIALS__;
  delete (window as unknown as BridgeWindow).__TELOS_NATIVE_SHELL__;
}

describe("secureStorage in the web build", () => {
  beforeEach(() => {
    clearBridge();
    __resetMemoryCredentialsForTests();
  });

  it("keeps credentials in process memory only and says they do not persist", async () => {
    expect(credentialsPersist()).toBe(false);
    await setCredential(KEY, "web-token");
    expect(await getCredential(KEY)).toBe("web-token");
    expect(await getCredential(OTHER_DEVICE)).toBeNull();
    await deleteCredential(KEY);
    expect(await getCredential(KEY)).toBeNull();
  });

  it("never writes to localStorage or sessionStorage", async () => {
    await setCredential(KEY, "web-token");
    for (const storage of [window.localStorage, window.sessionStorage]) {
      for (let i = 0; i < storage.length; i++) {
        const name = storage.key(i)!;
        expect(storage.getItem(name)).not.toContain("web-token");
      }
    }
  });
});

describe("secureStorage through the native bridge", () => {
  const store = new Map<string, string>();
  const slot = (key: CredentialKey) => JSON.stringify(key);

  beforeEach(() => {
    store.clear();
    __resetMemoryCredentialsForTests();
    installBridge({
      get: vi.fn(async (key) => store.get(slot(key)) ?? null),
      set: vi.fn(async (key, secret) => void store.set(slot(key), secret)),
      delete: vi.fn(async (key) => void store.delete(slot(key))),
    });
  });
  afterEach(clearBridge);

  it("reports persistence and round-trips through the structured key", async () => {
    expect(credentialsPersist()).toBe(true);
    await setCredential(KEY, "refresh-1");
    expect(await getCredential(KEY)).toBe("refresh-1");
    expect(await getCredential(OTHER_DEVICE)).toBeNull();
    await deleteCredential(KEY);
    expect(await getCredential(KEY)).toBeNull();
  });

  it("passes the structured key to the shell, never a raw string", async () => {
    const bridge = (window as unknown as BridgeWindow).__TELOS_NATIVE_CREDENTIALS__!;
    await setCredential(KEY, "refresh-1");
    expect(vi.mocked(bridge.set).mock.calls[0][0]).toEqual(KEY);
    expect(typeof vi.mocked(bridge.set).mock.calls[0][0]).toBe("object");
  });

  it("rejects instead of falling back to memory when the shell fails", async () => {
    const failing: NativeCredentialBridge = {
      get: vi.fn().mockRejectedValue("credential_store_locked: the operating-system credential service is locked"),
      set: vi.fn().mockRejectedValue("credential_store_unavailable: no operating-system credential service is available"),
      delete: vi.fn().mockRejectedValue("credential_store_error: the operating-system credential service reported an error"),
    };
    installBridge(failing);
    await expect(setCredential(KEY, "secret-value-123")).rejects.toMatchObject({ code: "credential_store_unavailable" });
    await expect(getCredential(KEY)).rejects.toMatchObject({ code: "credential_store_locked" });
    await expect(deleteCredential(KEY)).rejects.toBeInstanceOf(CredentialStoreError);
    // Nothing was kept in the web fallback map: with a working shell it is absent.
    installBridge({ get: async () => null, set: async () => {}, delete: async () => {} });
    expect(await getCredential(KEY)).toBeNull();
  });

  it("never echoes a secret in the error it throws", async () => {
    installBridge({
      get: async () => null,
      set: vi.fn().mockRejectedValue(new Error("boom with secret-value-123 inside")),
      delete: async () => {},
    });
    const error = (await setCredential(KEY, "secret-value-123").then(() => null, (e: unknown) => e)) as CredentialStoreError;
    expect(error).toBeInstanceOf(CredentialStoreError);
    expect(String(error.message)).not.toContain("secret-value-123");
    expect(error.code).toBe("credential_store_error");
  });
});

describe("secureStorage in a native shell without the credential bridge", () => {
  beforeEach(() => {
    clearBridge();
    __resetMemoryCredentialsForTests();
    // Another native global is present, the credential bridge is not.
    (window as unknown as BridgeWindow).__TELOS_NATIVE_SHELL__ = {};
  });
  afterEach(clearBridge);

  it("fails closed on every operation and never uses the memory map", async () => {
    await expect(setCredential(KEY, "t")).rejects.toMatchObject({ code: "credential_store_unavailable" });
    await expect(getCredential(KEY)).rejects.toMatchObject({ code: "credential_store_unavailable" });
    await expect(deleteCredential(KEY)).rejects.toMatchObject({ code: "credential_store_unavailable" });
  });
});

describe("legacy store notice", () => {
  afterEach(clearBridge);

  it("is true once when the shell deleted the old plaintext file, and false without a bridge", async () => {
    expect(await consumeLegacyStoreNotice()).toBe(false);
    installBridge({
      get: async () => null,
      set: async () => {},
      delete: async () => {},
      legacyStoreRetired: vi.fn().mockResolvedValueOnce(true).mockResolvedValue(false),
    });
    expect(await consumeLegacyStoreNotice()).toBe(true);
    expect(await consumeLegacyStoreNotice()).toBe(false);
  });

  it("treats a failing probe as no notice", async () => {
    installBridge({ get: async () => null, set: async () => {}, delete: async () => {}, legacyStoreRetired: vi.fn().mockRejectedValue("x") });
    expect(await consumeLegacyStoreNotice()).toBe(false);
  });
});
