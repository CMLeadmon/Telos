import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface MediaLibrary {
  id: string;
  name: string;
  type: "video" | "audio";
  collectionType?: string;
}

export interface MediaItem {
  id: string;
  title: string;
  duration: string;
  type: string; // Jellyfin item type: Series, Season, Movie, Episode, Audio, Audiobook, ...
  isFolder: boolean;
  childCount?: number;
}

export interface MediaCrumb {
  id: string;
  title: string;
}

type LoadStatus = "idle" | "loading" | "ready" | "error";

export interface NowPlaying {
  item: MediaItem;
  library: MediaLibrary;
}

interface MediaState {
  libraries: MediaLibrary[];
  libraryStatus: LoadStatus;
  error: string | null;
  activeLibraryId: string | null;
  itemsByParent: Record<string, MediaItem[]>;
  itemsStatusByParent: Record<string, LoadStatus>;
  path: MediaCrumb[];
  rootLibrary: MediaLibrary | null;
  nowPlaying: NowPlaying | null;
  fetchLibraries: () => Promise<void>;
  fetchItems: (parentId: string) => Promise<void>;
  selectLibrary: (id: string) => void;
  open: (item: MediaItem, library: MediaLibrary) => void;
  navigateTo: (index: number) => void;
  goHome: () => void;
  play: (item: MediaItem, library: MediaLibrary) => void;
  stop: () => void;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

export const useMediaStore = create<MediaState>()((set, get) => ({
  libraries: [],
  libraryStatus: "idle",
  error: null,
  activeLibraryId: null,
  itemsByParent: {},
  itemsStatusByParent: {},
  path: [],
  rootLibrary: null,
  nowPlaying: null,

  fetchLibraries: async () => {
    if (get().libraryStatus === "loading") return;
    set({ libraryStatus: "loading", error: null });
    try {
      const libraries = asList(await api<MediaLibrary[] | null>("/api/v1/media"));
      set((s) => ({
        libraries,
        libraryStatus: "ready",
        activeLibraryId: s.activeLibraryId ?? libraries[0]?.id ?? null,
      }));
      await Promise.all(libraries.map((lib) => get().fetchItems(lib.id)));
    } catch (err) {
      set({
        libraryStatus: "error",
        error:
          err instanceof ApiError && err.status === 503
            ? "jellyfin unreachable — the node is running degraded"
            : "could not load media libraries",
      });
    }
  },

  fetchItems: async (parentId) => {
    if (get().itemsStatusByParent[parentId] === "loading") return;
    set((s) => ({
      itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "loading" },
    }));
    try {
      const items = asList(
        await api<MediaItem[] | null>(
          `/api/v1/media/items?parentId=${encodeURIComponent(parentId)}`,
        ),
      );
      set((s) => ({
        itemsByParent: { ...s.itemsByParent, [parentId]: items },
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "ready" },
      }));
    } catch {
      set((s) => ({
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "error" },
      }));
    }
  },

  selectLibrary: (id) => set({ activeLibraryId: id }),

  open: (item, library) => {
    if (!item.isFolder) {
      get().play(item, library);
      return;
    }
    set((s) => ({
      rootLibrary: s.rootLibrary ?? library,
      path: [...s.path, { id: item.id, title: item.title }],
    }));
    void get().fetchItems(item.id);
  },

  navigateTo: (index) => {
    if (index < 0) {
      set({ path: [], rootLibrary: null });
      return;
    }
    set((s) => ({ path: s.path.slice(0, index + 1) }));
  },

  goHome: () => get().navigateTo(-1),

  play: (item, library) => set({ nowPlaying: { item, library } }),

  stop: () => set({ nowPlaying: null }),
}));
