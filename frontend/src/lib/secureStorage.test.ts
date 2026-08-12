import { describe, expect, it, beforeEach, vi, afterEach } from "vitest";
import {
  getSecret,
  setSecret,
  deleteSecret,
  secretsPersist,
  type NativeSecretStore,
} from "@/lib/secureStorage";

type BridgeWindow = { __TELOS_NATIVE_STORE__?: NativeSecretStore };

function installBridge(store: NativeSecretStore) {
  (window as unknown as BridgeWindow).__TELOS_NATIVE_STORE__ = store;
}

describe("secureStorage", () => {
  beforeEach(async () => {
    await deleteSecret("test_token");
    await deleteSecret("another_key");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    delete (window as unknown as BridgeWindow).__TELOS_NATIVE_STORE__;
  });

  it("returns null for non-existent keys", async () => {
    const value = await getSecret("non_existent_key");
    expect(value).toBeNull();
  });

  it("stores and retrieves secret values using in-memory store", async () => {
    await setSecret("test_token", "secret_value_123");
    const value = await getSecret("test_token");
    expect(value).toBe("secret_value_123");
  });

  it("deletes secret values from in-memory store", async () => {
    await setSecret("test_token", "secret_value_123");
    await deleteSecret("test_token");
    const value = await getSecret("test_token");
    expect(value).toBeNull();
  });

  it("uses the native store bridge when the shell installs one", async () => {
    const mockStore = {
      get: vi.fn().mockResolvedValue("tauri_secret"),
      set: vi.fn().mockResolvedValue(undefined),
      delete: vi.fn().mockResolvedValue(undefined),
    };
    installBridge(mockStore);

    await setSecret("another_key", "tauri_secret");
    expect(mockStore.set).toHaveBeenCalledWith("another_key", "tauri_secret");

    const val = await getSecret("another_key");
    expect(mockStore.get).toHaveBeenCalledWith("another_key");
    expect(val).toBe("tauri_secret");

    await deleteSecret("another_key");
    expect(mockStore.delete).toHaveBeenCalledWith("another_key");
  });

  it("falls back to the in-memory store if the bridge throws", async () => {
    const mockStore = {
      get: vi.fn().mockRejectedValue(new Error("bridge error")),
      set: vi.fn().mockRejectedValue(new Error("bridge error")),
      delete: vi.fn().mockRejectedValue(new Error("bridge error")),
    };
    installBridge(mockStore);

    await setSecret("test_token", "fallback_val");
    const val = await getSecret("test_token");
    expect(val).toBe("fallback_val");
  });

  // The fallback works, which is exactly what makes a missing bridge invisible.
  // A caller that needs a secret to outlive the process — the refresh token, a
  // certificate pin — has to be able to ask whether it will.
  it("reports whether writes will outlive the process", () => {
    expect(secretsPersist()).toBe(false);
    installBridge({
      get: async () => null,
      set: async () => {},
      delete: async () => {},
    });
    expect(secretsPersist()).toBe(true);
  });

  // The name is the contract. Reading a global nothing installs looks identical
  // to having no bridge at all, and the last one was invented rather than
  // observed, so pin it: only the documented name is honoured.
  it("ignores a store published under the old invented Tauri name", async () => {
    const impostor = {
      get: vi.fn().mockResolvedValue("from_impostor"),
      set: vi.fn().mockResolvedValue(undefined),
      delete: vi.fn().mockResolvedValue(undefined),
    };
    (window as unknown as Record<string, unknown>).__TAURI_OS_PLUGIN_STORE__ = impostor;
    try {
      await setSecret("test_token", "written_to_memory");
      expect(impostor.set).not.toHaveBeenCalled();
      expect(await getSecret("test_token")).toBe("written_to_memory");
      expect(secretsPersist()).toBe(false);
    } finally {
      delete (window as unknown as Record<string, unknown>).__TAURI_OS_PLUGIN_STORE__;
    }
  });
});
