import { create } from "zustand";
import { api } from "@/lib/api";
import { useThemeStore, type Theme } from "@/stores/useThemeStore";

export interface Prefs {
  theme: Theme;
  sceneEnabled: boolean;
  reducedMotion: boolean;
}

const DEFAULTS: Prefs = {
  theme: "synthwave",
  sceneEnabled: true,
  reducedMotion: false,
};

interface PreferencesState {
  prefs: Prefs;
  persisted: Prefs;
  draft: Prefs;
  saveStatus: "idle" | "saving" | "saved" | "error";
  saveError: string | null;
  loaded: boolean;
  load: () => Promise<void>;
  save: (patch: Partial<Prefs>) => Promise<void>;
  retrySave: () => Promise<void>;
  revertDraft: () => void;
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
  persisted: DEFAULTS,
  draft: DEFAULTS,
  saveStatus: "idle",
  saveError: null,
  loaded: false,

  load: async () => {
    try {
      const remote = await api<Partial<Prefs>>("/api/v1/users/me/preferences");
      const prefs = { ...DEFAULTS, theme: useThemeStore.getState().theme, ...remote };
      set({ prefs, persisted: prefs, draft: prefs, loaded: true });
      applySideEffects(prefs);
    } catch {
      // Offline/mock mode: keep defaults plus the locally persisted theme.
      const prefs = { ...DEFAULTS, theme: useThemeStore.getState().theme };
      set({
        prefs,
        persisted: prefs,
        draft: prefs,
        loaded: true,
      });
    }
  },

  save: async (patch) => {
    const draft = { ...get().prefs, ...patch };
    set({ prefs: draft, draft, saveStatus: "saving", saveError: null });
    applySideEffects(draft);
    try {
      await api("/api/v1/users/me/preferences", {
        method: "PUT",
        body: JSON.stringify(draft),
      });
      set({ persisted: draft, saveStatus: "saved", saveError: null });
    } catch (err) {
      set({
        saveStatus: "error",
        saveError: err instanceof Error ? err.message : "Failed to save preferences",
      });
    }
  },

  retrySave: async () => {
    const { draft, save } = get();
    await save(draft);
  },

  revertDraft: () => {
    const { persisted } = get();
    set({ prefs: persisted, draft: persisted, saveStatus: "idle", saveError: null });
    applySideEffects(persisted);
  },
}));
