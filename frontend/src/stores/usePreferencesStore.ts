import { create } from "zustand";
import { api } from "@/lib/api";
import { useThemeStore, type Theme } from "@/stores/useThemeStore";

export interface Prefs {
  theme: Theme;
  sceneEnabled: boolean;
  reducedMotion: boolean;
  voiceInputDeviceId: string;
  voiceOutputDeviceId: string;
  voiceInputGain: number;
  voiceOutputVolume: number;
  voiceNoiseSuppression: boolean;
  voiceEchoCancellation: boolean;
  voiceAutoGainControl: boolean;
}

const DEFAULTS: Prefs = {
  theme: "synthwave",
  sceneEnabled: true,
  reducedMotion: false,
  voiceInputDeviceId: "",
  voiceOutputDeviceId: "",
  voiceInputGain: 1,
  voiceOutputVolume: 1,
  voiceNoiseSuppression: true,
  voiceEchoCancellation: true,
  voiceAutoGainControl: true,
};

interface PreferencesState {
  prefs: Prefs;
  loaded: boolean;
  load: () => Promise<void>;
  save: (patch: Partial<Prefs>) => Promise<void>;
}

function applySideEffects(prefs: Prefs) {
  useThemeStore.getState().setTheme(prefs.theme);
  if (typeof document !== "undefined") {
    document.documentElement.dataset.motion = prefs.reducedMotion
      ? "reduced"
      : "full";
  }
}

export const usePreferencesStore = create<PreferencesState>()((set, get) => ({
  prefs: DEFAULTS,
  loaded: false,

  load: async () => {
    try {
      const remote = await api<Partial<Prefs>>("/api/v1/users/me/preferences");
      const prefs = { ...DEFAULTS, theme: useThemeStore.getState().theme, ...remote };
      set({ prefs, loaded: true });
      applySideEffects(prefs);
    } catch {
      // Offline/mock mode: keep defaults plus the locally persisted theme.
      set({
        prefs: { ...DEFAULTS, theme: useThemeStore.getState().theme },
        loaded: true,
      });
    }
  },

  save: async (patch) => {
    const prefs = { ...get().prefs, ...patch };
    set({ prefs });
    applySideEffects(prefs);
    try {
      await api("/api/v1/users/me/preferences", {
        method: "PUT",
        body: JSON.stringify(prefs),
      });
    } catch {
      // Server persistence is best-effort; the UI already applied the change.
    }
  },
}));
