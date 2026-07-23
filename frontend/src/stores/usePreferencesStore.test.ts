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
