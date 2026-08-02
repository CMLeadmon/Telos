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

// The gateway validates preference keys against an allow-list and rejects the
// whole payload with 400 on anything unknown. Rows written before the voice
// feature was removed still carry keys like voiceInputGain, so absorbing the
// remote blob wholesale and echoing it back broke every save for those
// accounts — silently, which is why it read as "the theme won't stick".
// Filter on both sides: nothing unknown enters state, nothing unknown is sent.
const KNOWN_KEYS = Object.keys(DEFAULTS) as (keyof Prefs)[];

function pickKnown(source: Partial<Prefs> | null | undefined): Partial<Prefs> {
  const out: Partial<Prefs> = {};
  if (!source) return out;
  for (const key of KNOWN_KEYS) {
    if (source[key] !== undefined) {
      // Each key is narrowed by KNOWN_KEYS, so this assignment is sound.
      (out as Record<string, unknown>)[key] = source[key];
    }
  }
  return out;
}

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
      const prefs = {
        ...DEFAULTS,
        theme: useThemeStore.getState().theme,
        ...pickKnown(remote),
      };
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
        body: JSON.stringify(pickKnown(draft)),
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
