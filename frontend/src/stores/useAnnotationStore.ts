import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export type AnnotationVisibility = "private" | "community";

export interface AnnotationLocator {
  kind: "epub" | "pdf";
  cfi?: string;
  page?: number;
  rects?: { x: number; y: number; w: number; h: number }[];
}

export interface Annotation {
  id: string;
  bookId: number;
  ownerId: string;
  visibility: AnnotationVisibility;
  locator: AnnotationLocator;
  selectedText: string;
  note: string;
  createdAt: string;
  updatedAt: string;
}

export interface AnnotationReply {
  id: string;
  annotationId: string;
  authorId: string;
  body: string;
  createdAt: string;
}

interface AnnotationState {
  bookId: number | null;
  annotations: Annotation[];
  replies: Record<string, AnnotationReply[]>;
  activeId: string | null;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;

  load: (bookId: number) => Promise<void>;
  create: (input: {
    locator: AnnotationLocator;
    selectedText: string;
    note: string;
    visibility?: AnnotationVisibility;
  }) => Promise<void>;
  update: (id: string, patch: { visibility: AnnotationVisibility; note: string }) => Promise<void>;
  remove: (id: string) => Promise<void>;
  loadReplies: (id: string) => Promise<void>;
  reply: (id: string, body: string) => Promise<void>;
  focus: (id: string | null) => void;
}

function message(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong.";
}

export const useAnnotationStore = create<AnnotationState>((set, get) => ({
  bookId: null,
  annotations: [],
  replies: {},
  activeId: null,
  status: "idle",
  error: null,

  load: async (bookId) => {
    set({ bookId, status: "loading", error: null });
    try {
      const res = await api<{ annotations: Annotation[] }>(`/api/v1/library/books/${bookId}/annotations`);
      set({ annotations: res.annotations ?? [], status: "ready" });
    } catch (e) {
      set({ status: "error", error: message(e) });
    }
  },

  // Visibility defaults to private; the server is authoritative for the stored
  // visibility regardless of any client field.
  create: async ({ locator, selectedText, note, visibility = "private" }) => {
    const { bookId } = get();
    if (bookId == null) return;
    try {
      const created = await api<Annotation>(`/api/v1/library/books/${bookId}/annotations`, {
        method: "POST",
        body: JSON.stringify({ locator, selectedText, note, visibility }),
      });
      set((s) => ({ annotations: [created, ...s.annotations], error: null }));
    } catch (e) {
      set({ error: message(e) });
      throw e;
    }
  },

  update: async (id, patch) => {
    const prev = get().annotations;
    // Optimistic; roll back on failure so a failed note edit is not lost.
    set((s) => ({
      annotations: s.annotations.map((a) => (a.id === id ? { ...a, ...patch } : a)),
    }));
    try {
      await api(`/api/v1/library/annotations/${id}`, { method: "PATCH", body: JSON.stringify(patch) });
    } catch (e) {
      set({ annotations: prev, error: message(e) });
      throw e;
    }
  },

  remove: async (id) => {
    const prev = get().annotations;
    set((s) => ({ annotations: s.annotations.filter((a) => a.id !== id) }));
    try {
      await api(`/api/v1/library/annotations/${id}`, { method: "DELETE" });
    } catch (e) {
      set({ annotations: prev, error: message(e) });
      throw e;
    }
  },

  loadReplies: async (id) => {
    try {
      const res = await api<{ replies: AnnotationReply[] }>(`/api/v1/library/annotations/${id}/replies`);
      set((s) => ({ replies: { ...s.replies, [id]: res.replies ?? [] } }));
    } catch (e) {
      set({ error: message(e) });
    }
  },

  reply: async (id, body) => {
    try {
      const created = await api<AnnotationReply>(`/api/v1/library/annotations/${id}/replies`, {
        method: "POST",
        body: JSON.stringify({ body }),
      });
      set((s) => ({
        replies: { ...s.replies, [id]: [...(s.replies[id] ?? []), created] },
        error: null,
      }));
    } catch (e) {
      // Keep the failed reply body available to the caller for retry.
      set({ error: message(e) });
      throw e;
    }
  },

  focus: (id) => set({ activeId: id }),
}));
