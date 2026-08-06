import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface MediaLibrary {
  // Catalog IDs are opaque gateway-issued strings, never provider IDs.
  id: string;
  name: string;
  type: "video" | "audio";
  collectionType?: string;
}

export interface MediaItem {
  // Preserve this value exactly and encode it only at URL boundaries.
  id: string;
  title: string;
  duration?: string;
  durationMs?: number;
  type: string; // Jellyfin item type: Series, Season, Movie, Episode, Audio, Audiobook, ...
  isFolder: boolean;
  childCount?: number;
  coverUrl?: string;
}

export interface MediaChapter {
  index: number;
  title: string;
  startMs: number;
}

export interface MemberProgress {
  locator?: Record<string, unknown>;
  positionMs?: number;
  durationMs?: number;
  percent?: number;
  completed?: boolean;
}

export interface MediaDetail extends MediaItem {
  overview?: string;
  year?: number;
  genres?: string[];
  seriesName?: string;
  seasonName?: string;
  episodeNumber?: number;
  studios?: string[];
  chapters?: MediaChapter[];
  progress?: MemberProgress;
}

export interface PlaybackAudioTrack {
  index: number;
  title: string;
  language: string;
  codec: string;
  channels: number;
  isDefault: boolean;
}

export interface PlaybackSubtitleTrack {
  index: number;
  title: string;
  language: string;
  codec: string;
  isExternal: boolean;
  isDefault: boolean;
  deliveryUrl?: string;
}

export interface PlaybackOptions {
  itemId: string;
  audioTracks: PlaybackAudioTrack[];
  subtitles: PlaybackSubtitleTrack[];
  directPlay: boolean;
  streamUrl: string;
}

export interface MediaCrumb {
  id: string;
  title: string;
}

type LoadStatus = "idle" | "loading" | "ready" | "error";
type MediaRefreshPhase =
  | "idle"
  | "starting"
  | "scanning"
  | "refreshing"
  | "complete"
  | "error";

interface MediaRefreshResponse {
  status:
    | "idle"
    | "starting"
    | "scanning"
    | "refreshing"
    | "complete"
    | "failed"
    | "timeout";
  message: string;
  progress?: number;
}

interface MediaNotice {
  kind: "success" | "error";
  text: string;
}

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
  continueItems: MediaDetail[];
  recentItems: MediaItem[];
  path: MediaCrumb[];
  rootLibrary: MediaLibrary | null;
  nowPlaying: NowPlaying | null;
  refreshing: boolean;
  refreshPhase: MediaRefreshPhase;
  refreshProgress: number | null;
  refreshNotice: MediaNotice | null;
  fetchLibraries: () => Promise<void>;
  fetchContinue: () => Promise<void>;
  fetchRecent: () => Promise<void>;
  fetchDetail: (id: string) => Promise<MediaDetail | null>;
  fetchPlaybackOptions: (id: string) => Promise<PlaybackOptions | null>;
  refresh: () => Promise<void>;
  clearRefreshNotice: () => void;
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

function isActiveRefreshPhase(
  status: MediaRefreshResponse["status"],
): status is "starting" | "scanning" | "refreshing" {
  return status === "starting" || status === "scanning" || status === "refreshing";
}

export const useMediaStore = create<MediaState>()((set, get) => ({
  libraries: [],
  libraryStatus: "idle",
  error: null,
  activeLibraryId: null,
  itemsByParent: {},
  itemsStatusByParent: {},
  continueItems: [],
  recentItems: [],
  path: [],
  rootLibrary: null,
  nowPlaying: null,
  refreshing: false,
  refreshPhase: "idle",
  refreshProgress: null,
  refreshNotice: null,

  fetchLibraries: async () => {
    if (get().libraryStatus === "loading") return;
    set({ libraryStatus: "loading", error: null });
    try {
      const res = await api<MediaLibrary[]>("/api/v1/media/items");
      set({ libraries: asList(res), libraryStatus: "ready" });
    } catch (err) {
      set({
        libraryStatus: "error",
        error: err instanceof Error ? err.message : "Failed to load libraries",
      });
    }
  },

  fetchContinue: async () => {
    try {
      const res = await api<MediaDetail[]>("/api/v1/media/continue");
      set({ continueItems: asList(res) });
    } catch (err) {
      console.error("Failed to load continue watching items", err);
    }
  },

  fetchRecent: async () => {
    try {
      const res = await api<MediaItem[]>("/api/v1/media/recent");
      set({ recentItems: asList(res) });
    } catch (err) {
      console.error("Failed to load recent media", err);
    }
  },

  fetchDetail: async (id: string) => {
    try {
      const res = await api<MediaDetail>(`/api/v1/media/items/${encodeURIComponent(id)}`);
      return res;
    } catch (err) {
      console.error("Failed to load media detail", err);
      return null;
    }
  },

  fetchPlaybackOptions: async (id: string) => {
    try {
      const res = await api<PlaybackOptions>(`/api/v1/media/items/${encodeURIComponent(id)}/playback-info`);
      return res;
    } catch (err) {
      console.error("Failed to load playback options", err);
      return null;
    }
  },

  // Start one manual Jellyfin scan, wait for Jellyfin's scheduled task to
  // finish, then discard and rebuild every local catalog cache. Waiting for
  // completion is important: an immediate refetch can resurrect items that
  // Jellyfin has not removed from its database yet.
  refresh: async () => {
    if (get().refreshing) return;
    const snapshot = {
      libraries: get().libraries,
      libraryStatus: get().libraryStatus,
      error: get().error,
      activeLibraryId: get().activeLibraryId,
      itemsByParent: get().itemsByParent,
      itemsStatusByParent: get().itemsStatusByParent,
      path: get().path,
      rootLibrary: get().rootLibrary,
      nowPlaying: get().nowPlaying,
    };
    let rebuilding = false;

    set({
      refreshing: true,
      refreshPhase: "starting",
      refreshProgress: null,
      refreshNotice: null,
    });
    try {
      let scan = await api<MediaRefreshResponse>("/api/v1/media/refresh", {
        method: "POST",
      });
      while (isActiveRefreshPhase(scan.status)) {
        set({
          refreshPhase: scan.status,
          refreshProgress: scan.progress ?? null,
        });
        await new Promise((resolve) => setTimeout(resolve, 1000));
        scan = await api<MediaRefreshResponse>("/api/v1/media/refresh/status");
      }
      if (scan.status !== "complete") {
        throw new Error(scan.message || "Jellyfin could not complete the scan.");
      }

      rebuilding = true;
      set({
        libraries: [],
        libraryStatus: "idle",
        error: null,
        activeLibraryId: null,
        itemsByParent: {},
        itemsStatusByParent: {},
        path: [],
        rootLibrary: null,
        refreshPhase: "refreshing",
        refreshProgress: null,
      });
      await get().fetchLibraries();
      if (get().libraryStatus === "error") {
        throw new Error(get().error || "Could not reload the media catalog.");
      }

      let removedFromView = false;
      if (snapshot.path.length > 0 && snapshot.rootLibrary) {
        const liveRoot = get().libraries.find(
          (library) => library.id === snapshot.rootLibrary?.id,
        );
        if (!liveRoot) {
          removedFromView = true;
        } else {
          let parentId = liveRoot.id;
          const livePath: MediaCrumb[] = [];
          for (const crumb of snapshot.path) {
            await get().fetchItems(parentId);
            if (get().itemsStatusByParent[parentId] === "error") {
              throw new Error("Could not reload the open media folder.");
            }
            const liveItem = (get().itemsByParent[parentId] ?? []).find(
              (item) => item.id === crumb.id && item.isFolder,
            );
            if (!liveItem) {
              removedFromView = true;
              break;
            }
            livePath.push({ id: liveItem.id, title: liveItem.title });
            parentId = liveItem.id;
          }
          if (!removedFromView) {
            await get().fetchItems(parentId);
            if (get().itemsStatusByParent[parentId] === "error") {
              throw new Error("Could not reload the open media folder.");
            }
            set({ path: livePath, rootLibrary: liveRoot });
          }
        }
      }

      let removedFromPlayer = false;
      if (snapshot.nowPlaying) {
        try {
          await api(`/api/v1/media/items/${encodeURIComponent(snapshot.nowPlaying.item.id)}`);
        } catch (err) {
          if (err instanceof ApiError && err.status === 404) {
            set({ nowPlaying: null });
            removedFromPlayer = true;
          } else {
            throw err;
          }
        }
      }

      const removalMessage = removedFromPlayer
        ? "Jellyfin scan complete. The media that was playing has been removed."
        : removedFromView
          ? "Jellyfin scan complete. The open media folder was removed."
          : "Jellyfin scan complete.";
      const notice: MediaNotice = { kind: "success", text: removalMessage };
      set({
        refreshPhase: "complete",
        refreshNotice: notice,
      });
      setTimeout(() => {
        if (get().refreshNotice === notice) {
          set({ refreshNotice: null, refreshPhase: "idle" });
        }
      }, 5000);
    } catch (err) {
      if (rebuilding) {
        set(snapshot);
      }
      set({
        refreshPhase: "error",
        refreshProgress: null,
        refreshNotice: {
          kind: "error",
          text:
            err instanceof Error
              ? err.message.trim()
              : "Jellyfin could not complete the scan.",
        },
      });
    } finally {
      set({ refreshing: false });
    }
  },

  clearRefreshNotice: () => set({ refreshNotice: null }),

  fetchItems: async (parentId: string) => {
    if (get().itemsStatusByParent[parentId] === "loading") return;
    set((s) => ({
      itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "loading" },
    }));
    try {
      const res = await api<MediaItem[]>(`/api/v1/media/items?parentId=${encodeURIComponent(parentId)}`);
      set((s) => ({
        itemsByParent: { ...s.itemsByParent, [parentId]: asList(res) },
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "ready" },
      }));
    } catch {
      set((s) => ({
        itemsStatusByParent: { ...s.itemsStatusByParent, [parentId]: "error" },
      }));
    }
  },

  selectLibrary: (id: string) => {
    const lib = get().libraries.find((l) => l.id === id);
    if (!lib) return;
    set({
      activeLibraryId: id,
      rootLibrary: lib,
      path: [{ id: lib.id, title: lib.name }],
    });
    void get().fetchItems(id);
  },

  open: (item: MediaItem, library: MediaLibrary) => {
    if (item.isFolder) {
      set((s) => ({
        path: [...s.path, { id: item.id, title: item.title }],
      }));
      void get().fetchItems(item.id);
    } else {
      set({ nowPlaying: { item, library } });
    }
  },

  navigateTo: (index: number) => {
    const path = get().path.slice(0, index + 1);
    const target = path[path.length - 1];
    set({ path });
    if (target) void get().fetchItems(target.id);
  },

  goHome: () => set({ activeLibraryId: null, rootLibrary: null, path: [] }),

  play: (item: MediaItem, library: MediaLibrary) =>
    set({ nowPlaying: { item, library } }),

  stop: () => set({ nowPlaying: null }),
}));
