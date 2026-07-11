import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Theme = "synthwave" | "ink";

interface ThemeState {
  theme: Theme;
  toggle: () => void;
  setTheme: (theme: Theme) => void;
}

export const useThemeStore = create<ThemeState>()(
  persist(
    (set) => ({
      theme: "synthwave",
      toggle: () =>
        set((s) => ({ theme: s.theme === "synthwave" ? "ink" : "synthwave" })),
      setTheme: (theme) => set({ theme }),
    }),
    { name: "telos-theme" },
  ),
);
