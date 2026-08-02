import { create } from "zustand";
import { api, ApiError } from "@/lib/api";
import { uploadFile, UploadError } from "@/lib/upload";

export interface FileEntry {
  id: string;
  filename: string;
  sha256: string;
  uploader_id: string;
  scan_status: string;
  size_bytes: number;
  mime_type: string;
  created_at: string;
}

export interface FolderEntry {
  id: string;
  name: string;
  path: string;
}

interface DirResponse {
  path: string;
  folders: FolderEntry[] | null;
  files: FileEntry[] | null;
  page: number;
  hasNext: boolean;
}

export type UploadDestination = "library" | "bookdrop";

export type UploadState =
  | { phase: "uploading"; percent: number }
  | { phase: "scanning" }
  | { phase: "error"; message: string };

export interface ActiveUpload {
  key: number;
  filename: string;
  destination: UploadDestination;
  state: UploadState;
}

export const PAGE_SIZE = 20;
export const MAX_UPLOAD_BYTES = 104857600; // 100 MiB, mirrors the gateway cap

// Mirrors the gateway's processUpload allowlists exactly.
const LIBRARY_EXTENSIONS = [
  ".pdf", ".epub", ".jpg", ".jpeg", ".png", ".webp",
  ".mp3", ".m4a", ".ogg", ".wav", ".mp4", ".webm",
];
const BOOK_EXTENSIONS = [".pdf", ".epub"];

export function validateFile(
  file: File,
  destination: UploadDestination,
): string | null {
  const dot = file.name.lastIndexOf(".");
  const ext = dot === -1 ? "" : file.name.slice(dot).toLowerCase();
  const allowed =
    destination === "bookdrop" ? BOOK_EXTENSIONS : LIBRARY_EXTENSIONS;
  if (!allowed.includes(ext)) {
    return destination === "bookdrop"
      ? "bookdrop only accepts .pdf and .epub"
      : `file type ${ext || "(none)"} is not allowed`;
  }
  if (file.size > MAX_UPLOAD_BYTES) return "file exceeds the 100 MiB limit";
  return null;
}

// The gateway marshals empty Go slices as JSON null — normalize before use.
function asList<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

let uploadCounter = 0;

interface FilesState {
  path: string;
  folders: FolderEntry[];
  // The top-level folders, cached the first time root is listed. The rail's
  // Locations group needs them at any depth, and caching here means the rail
  // never has to issue a request of its own.
  rootFolders: FolderEntry[];
  files: FileEntry[];
  page: number;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  hasNextPage: boolean;
  uploads: ActiveUpload[];
  notice: string | null;
  fetchDir: (path: string, page: number) => Promise<void>;
  fetchPage: (page: number) => Promise<void>;
  enterFolder: (path: string) => Promise<void>;
  navigateTo: (path: string) => Promise<void>;
  createFolder: (name: string) => Promise<void>;
  deleteFolder: (id: string) => Promise<void>;
  deleteFile: (id: string) => Promise<void>;
  upload: (file: File, destination: UploadDestination) => Promise<void>;
  dismissUpload: (key: number) => void;
  setNotice: (notice: string | null) => void;
}

export const useFilesStore = create<FilesState>()((set, get) => ({
  path: "",
  folders: [],
  rootFolders: [],
  files: [],
  page: 1,
  status: "idle",
  error: null,
  hasNextPage: false,
  uploads: [],
  notice: null,

  fetchDir: async (path, page) => {
    set({ status: "loading", error: null });
    try {
      const res = await api<DirResponse>(
        `/api/v1/files?path=${encodeURIComponent(path)}&page=${page}`,
      );
      const folders = asList(res.folders);
      const resolvedPath = res.path ?? path;
      set({
        path: resolvedPath,
        folders,
        // Listing root is also how the rail's Locations group gets populated.
        ...(resolvedPath === "" ? { rootFolders: folders } : {}),
        files: asList(res.files),
        page,
        status: "ready",
        hasNextPage: Boolean(res.hasNext),
      });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load files",
      });
    }
  },

  fetchPage: async (page) => {
    await get().fetchDir(get().path, page);
  },

  enterFolder: async (path) => {
    await get().fetchDir(path, 1);
  },

  navigateTo: async (path) => {
    await get().fetchDir(path, 1);
  },

  createFolder: async (name) => {
    try {
      await api("/api/v1/folders", {
        method: "POST",
        body: JSON.stringify({ path: get().path, name }),
      });
      await get().fetchDir(get().path, 1);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to create folders" });
      } else if (err instanceof ApiError && err.status === 409) {
        set({ notice: "a folder with that name already exists" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "couldn't create folder",
        });
      }
    }
  },

  deleteFolder: async (id) => {
    try {
      await api(`/api/v1/folders/${id}`, { method: "DELETE" });
      await get().fetchDir(get().path, get().page);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        set({ notice: "folder isn't empty — clear it first" });
      } else if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to delete folders" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "delete failed",
        });
      }
    }
  },

  deleteFile: async (id) => {
    try {
      await api(`/api/v1/files/${id}`, { method: "DELETE" });
      const { path, page, folders, files } = get();
      // Deleting the last row of a later page: step back a page.
      const target =
        folders.length + files.length === 1 && page > 1 ? page - 1 : page;
      await get().fetchDir(path, target);
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        set({ notice: "you don't have permission to delete files" });
      } else {
        set({
          notice: err instanceof Error ? err.message : "delete failed",
        });
      }
    }
  },

  upload: async (file, destination) => {
    const key = ++uploadCounter;
    const entry: ActiveUpload = {
      key,
      filename: file.name,
      destination,
      state: { phase: "uploading", percent: 0 },
    };
    set((s) => ({ uploads: [...s.uploads, entry] }));
    const patch = (state: UploadState) =>
      set((s) => ({
        uploads: s.uploads.map((u) => (u.key === key ? { ...u, state } : u)),
      }));

    // Library uploads land in the current folder; bookdrop stays flat.
    const path =
      destination === "bookdrop" ? "/api/v1/files/books" : "/api/v1/files";
    const extra =
      destination === "bookdrop" ? undefined : { path: get().path };
    try {
      await uploadFile(
        path,
        file,
        (phase, percent) =>
          patch(
            phase === "scanning"
              ? { phase: "scanning" }
              : { phase: "uploading", percent },
          ),
        extra,
      );
      set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) }));
      await get().fetchDir(get().path, 1);
    } catch (err) {
      let message = "upload failed";
      if (err instanceof UploadError) {
        if (err.status === 403)
          message = "you don't have permission to upload here";
        else if (err.status === 422) {
          message = "rejected by security scan";
          void get().fetchDir(get().path, 1);
        } else if (err.status === 503)
          message = "security scanner unavailable — try again later";
        else if (err.message) message = err.message;
      }
      patch({ phase: "error", message });
    }
  },

  dismissUpload: (key) =>
    set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) })),

  setNotice: (notice) => set({ notice }),
}));
