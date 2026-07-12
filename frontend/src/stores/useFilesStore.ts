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
  files: FileEntry[];
  page: number;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  hasNextPage: boolean;
  uploads: ActiveUpload[];
  notice: string | null;
  fetchPage: (page: number) => Promise<void>;
  deleteFile: (id: string) => Promise<void>;
  upload: (file: File, destination: UploadDestination) => Promise<void>;
  dismissUpload: (key: number) => void;
  setNotice: (notice: string | null) => void;
}

export const useFilesStore = create<FilesState>()((set, get) => ({
  files: [],
  page: 1,
  status: "idle",
  error: null,
  hasNextPage: false,
  uploads: [],
  notice: null,

  fetchPage: async (page) => {
    set({ status: "loading", error: null });
    try {
      const rows = await api<FileEntry[] | null>(`/api/v1/files?page=${page}`);
      const files = asList(rows);
      set({
        files,
        page,
        status: "ready",
        hasNextPage: files.length === PAGE_SIZE,
      });
    } catch (err) {
      set({
        status: "error",
        error: err instanceof Error ? err.message : "failed to load files",
      });
    }
  },

  deleteFile: async (id) => {
    try {
      await api(`/api/v1/files/${id}`, { method: "DELETE" });
      const { page, files } = get();
      // Deleting the last row of a later page: step back so the view
      // doesn't land on an empty page.
      const target = files.length === 1 && page > 1 ? page - 1 : page;
      await get().fetchPage(target);
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

    const path =
      destination === "bookdrop" ? "/api/v1/files/books" : "/api/v1/files";
    try {
      await uploadFile(path, file, (phase, percent) =>
        patch(
          phase === "scanning"
            ? { phase: "scanning" }
            : { phase: "uploading", percent },
        ),
      );
      set((s) => ({ uploads: s.uploads.filter((u) => u.key !== key) }));
      await get().fetchPage(1);
    } catch (err) {
      let message = "upload failed";
      if (err instanceof UploadError) {
        if (err.status === 403)
          message = "you don't have permission to upload here";
        else if (err.status === 422) {
          message = "rejected by security scan";
          // The gateway keeps infected uploads as audit rows — show it.
          void get().fetchPage(1);
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
