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
  type: string; // Jellyfin item type: Movie, Episode, Audio, Audiobook
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
  itemsByLibrary: Record<string, MediaItem[]>;
  itemsStatus: Record<string, LoadStatus>;
  nowPlaying: NowPlaying | null;
  fetchLibraries: () => Promise<void>;
  fetchItems: (libraryId: string) => Promise<void>;
  selectLibrary: (id: string) => void;
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
  itemsByLibrary: {},
  itemsStatus: {},
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

  fetchItems: async (libraryId) => {
    if (get().itemsStatus[libraryId] === "loading") return;
    set((s) => ({ itemsStatus: { ...s.itemsStatus, [libraryId]: "loading" } }));
    try {
      const items = asList(
        await api<MediaItem[] | null>(
          `/api/v1/media/items?parentId=${encodeURIComponent(libraryId)}`,
        ),
      );
      set((s) => ({
        itemsByLibrary: { ...s.itemsByLibrary, [libraryId]: items },
        itemsStatus: { ...s.itemsStatus, [libraryId]: "ready" },
      }));
    } catch {
      set((s) => ({ itemsStatus: { ...s.itemsStatus, [libraryId]: "error" } }));
    }
  },

  selectLibrary: (id) => set({ activeLibraryId: id }),

  play: (item, library) => set({ nowPlaying: { item, library } }),

  stop: () => set({ nowPlaying: null }),
}));
