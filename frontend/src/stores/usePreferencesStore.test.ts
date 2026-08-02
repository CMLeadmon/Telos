import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/stores/useThemeStore", () => ({
  useThemeStore: {
    getState: () => ({
      theme: "synthwave",
      setTheme: vi.fn(),
    }),
  },
}));

vi.mock("@/lib/api", () => ({
  api: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(public status: number, message: string) {
      super(message);
    }
  },
}));

import { api, ApiError } from "@/lib/api";
import { usePreferencesStore } from "./usePreferencesStore";

const apiMock = api as unknown as ReturnType<typeof vi.fn>;

describe("usePreferencesStore failure recovery", () => {
  beforeEach(() => {
    apiMock.mockReset();
  });

  it("updates draft and persisted on successful save", async () => {
    apiMock.mockResolvedValue({});

    await usePreferencesStore.getState().save({ sceneEnabled: false });

    expect(usePreferencesStore.getState().prefs.sceneEnabled).toBe(false);
    expect(usePreferencesStore.getState().saveStatus).toBe("saved");
    expect(usePreferencesStore.getState().saveError).toBeNull();
  });

  it("retains draft and sets error status on save failure", async () => {
    apiMock.mockRejectedValue(new ApiError(500, "Server error"));

    await usePreferencesStore.getState().save({ reducedMotion: true });

    expect(usePreferencesStore.getState().prefs.reducedMotion).toBe(true);
    expect(usePreferencesStore.getState().saveStatus).toBe("error");
    expect(usePreferencesStore.getState().saveError).toBe("Server error");
  });

  // Regression: rows written before the voice feature was removed still carry
  // keys like voiceInputGain. load() absorbed them and save() echoed them back,
  // so the gateway's allow-list rejected the whole PUT with 400 — silently.
  // Every preference save was dead for those accounts, and the theme appeared
  // to "not survive refresh" because the stale server value won on reload.
  it("never sends keys the gateway does not accept", async () => {
    apiMock.mockResolvedValue({
      theme: "ink",
      sceneEnabled: true,
      reducedMotion: false,
      voiceInputGain: 1.3,
      voiceNoiseSuppression: true,
      voiceOutputVolume: 1,
      voiceInputDeviceId: "abc123",
    });
    await usePreferencesStore.getState().load();

    apiMock.mockReset();
    apiMock.mockResolvedValue({});
    await usePreferencesStore.getState().save({ theme: "synthwave" });

    const body = JSON.parse(apiMock.mock.calls[0][1].body);
    expect(Object.keys(body).sort()).toEqual([
      "reducedMotion",
      "sceneEnabled",
      "theme",
    ]);
    expect(body.theme).toBe("synthwave");
    expect(usePreferencesStore.getState().saveStatus).toBe("saved");
  });

  it("keeps unknown remote keys out of state entirely", async () => {
    apiMock.mockResolvedValue({ theme: "ink", voiceOutputVolume: 1 });
    await usePreferencesStore.getState().load();

    expect(usePreferencesStore.getState().prefs).not.toHaveProperty(
      "voiceOutputVolume",
    );
    expect(usePreferencesStore.getState().prefs.theme).toBe("ink");
  });

  it("reverts draft to last persisted state when revertDraft is called", async () => {
    apiMock.mockRejectedValue(new ApiError(500, "Server error"));

    await usePreferencesStore.getState().save({ reducedMotion: true });
    expect(usePreferencesStore.getState().saveStatus).toBe("error");

    usePreferencesStore.getState().revertDraft();

    expect(usePreferencesStore.getState().prefs.reducedMotion).toBe(false);
    expect(usePreferencesStore.getState().saveStatus).toBe("idle");
    expect(usePreferencesStore.getState().saveError).toBeNull();
  });
});
