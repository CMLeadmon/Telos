import { create } from "zustand";

// Which settings section is open. Lives outside the page because the module
// rail renders the section list — same pattern every other module follows,
// rather than Settings carrying a second sidebar inside the arena.
interface SettingsState {
  activeSection: string;
  setSection: (id: string) => void;
}

export const useSettingsStore = create<SettingsState>()((set) => ({
  activeSection: "profile",
  setSection: (id) => set({ activeSection: id }),
}));
