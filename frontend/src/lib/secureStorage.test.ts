import { describe, expect, it, beforeEach, vi, afterEach } from "vitest";
import { getSecret, setSecret, deleteSecret } from "@/lib/secureStorage";

describe("secureStorage", () => {
  beforeEach(async () => {
    await deleteSecret("test_token");
    await deleteSecret("another_key");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    delete (window as unknown as { __TAURI_OS_PLUGIN_STORE__?: unknown }).__TAURI_OS_PLUGIN_STORE__;
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

  it("uses window.__TAURI_OS_PLUGIN_STORE__ when available", async () => {
    const mockStore = {
      get: vi.fn().mockResolvedValue("tauri_secret"),
      set: vi.fn().mockResolvedValue(undefined),
      delete: vi.fn().mockResolvedValue(undefined),
    };

    (window as unknown as { __TAURI_OS_PLUGIN_STORE__?: unknown }).__TAURI_OS_PLUGIN_STORE__ = mockStore;

    await setSecret("another_key", "tauri_secret");
    expect(mockStore.set).toHaveBeenCalledWith("another_key", "tauri_secret");

    const val = await getSecret("another_key");
    expect(mockStore.get).toHaveBeenCalledWith("another_key");
    expect(val).toBe("tauri_secret");

    await deleteSecret("another_key");
    expect(mockStore.delete).toHaveBeenCalledWith("another_key");
  });

  it("falls back to in-memory store if Tauri store throws an error", async () => {
    const mockStore = {
      get: vi.fn().mockRejectedValue(new Error("Tauri store error")),
      set: vi.fn().mockRejectedValue(new Error("Tauri store error")),
      delete: vi.fn().mockRejectedValue(new Error("Tauri store error")),
    };

    (window as unknown as { __TAURI_OS_PLUGIN_STORE__?: unknown }).__TAURI_OS_PLUGIN_STORE__ = mockStore;

    await setSecret("test_token", "fallback_val");
    const val = await getSecret("test_token");
    expect(val).toBe("fallback_val");
  });
});
