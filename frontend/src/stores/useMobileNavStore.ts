import { create } from "zustand";

// The channel drawer is rendered by MobileNavigation but opened from the chat
// header, so its open state has to live outside both. Deliberately not
// persisted: a drawer left open across reloads is never what you want.
interface MobileNavState {
  channelDrawerOpen: boolean;
  openChannelDrawer: () => void;
  closeChannelDrawer: () => void;
  toggleChannelDrawer: () => void;
}

export const useMobileNavStore = create<MobileNavState>()((set) => ({
  channelDrawerOpen: false,
  openChannelDrawer: () => set({ channelDrawerOpen: true }),
  closeChannelDrawer: () => set({ channelDrawerOpen: false }),
  toggleChannelDrawer: () =>
    set((s) => ({ channelDrawerOpen: !s.channelDrawerOpen })),
}));
