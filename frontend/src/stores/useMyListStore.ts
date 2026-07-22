import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface MediaListEntry {
  itemId: string;
  title: string;
  mediaType: string;
  position: number;
  available: boolean;
}

interface MyListState {
  entries: MediaListEntry[];
  nextCursor: string | null;
  listRevision: number;
  status: "idle" | "loading" | "ready" | "error";
  error: string | null;
  conflict: boolean;

  load: () => Promise<void>;
  loadMore: () => Promise<void>;
  add: (itemId: string, snapshot?: { title: string; mediaType: string }) => Promise<void>;
  remove: (itemId: string) => Promise<void>;
  moveBefore: (itemId: string, beforeItemId: string | null) => Promise<void>;
}

interface ListResponse {
  items: MediaListEntry[];
  nextCursor?: string;
  listRevision: number;
}

function message(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong.";
}

export const useMyListStore = create<MyListState>((set, get) => ({
  entries: [],
  nextCursor: null,
  listRevision: 0,
  status: "idle",
  error: null,
  conflict: false,

  load: async () => {
    set({ status: "loading", error: null, conflict: false });
    try {
      const res = await api<ListResponse>("/api/v1/users/me/media-list?limit=30");
      set({ entries: res.items ?? [], nextCursor: res.nextCursor ?? null, listRevision: res.listRevision, status: "ready" });
    } catch (e) {
      set({ status: "error", error: message(e) });
    }
  },

  loadMore: async () => {
    const { nextCursor, status } = get();
    if (!nextCursor || status === "loading") return;
    set({ status: "loading" });
    try {
      const res = await api<ListResponse>(`/api/v1/users/me/media-list?limit=30&cursor=${encodeURIComponent(nextCursor)}`);
      set((s) => ({
        entries: mergeEntries(s.entries, res.items ?? []),
        nextCursor: res.nextCursor ?? null,
        listRevision: res.listRevision,
        status: "ready",
      }));
    } catch (e) {
      // A stale cursor conflicts (409): discard it and reload from page one.
      if (e instanceof ApiError && e.status === 409) {
        await get().load();
        set({ conflict: true });
        return;
      }
      set({ status: "error", error: message(e) });
    }
  },

  add: async (itemId, snapshot) => {
    try {
      await api("/api/v1/users/me/media-list", { method: "POST", body: JSON.stringify({ itemId }) });
      // Reload to get the server-assigned position and revision.
      await get().load();
    } catch (e) {
      set({ error: message(e) });
      throw e;
    }
    void snapshot;
  },

  remove: async (itemId) => {
    const prev = get().entries;
    // Optimistic; roll back on failure.
    set((s) => ({ entries: s.entries.filter((e) => e.itemId !== itemId) }));
    try {
      await api(`/api/v1/users/me/media-list/${encodeURIComponent(itemId)}`, { method: "DELETE" });
      await get().load();
    } catch (e) {
      set({ entries: prev, error: message(e) });
      throw e;
    }
  },

  moveBefore: async (itemId, beforeItemId) => {
    const prev = get().entries;
    const expectedRevision = get().listRevision;
    // Optimistic local reorder.
    set((s) => ({ entries: reorderLocal(s.entries, itemId, beforeItemId) }));
    try {
      const res = await api<{ listRevision: number }>("/api/v1/users/me/media-list/order", {
        method: "PUT",
        body: JSON.stringify({ itemId, beforeItemId, expectedRevision }),
      });
      set({ listRevision: res.listRevision });
      await get().load();
    } catch (e) {
      // On a 409 the list changed under us: roll back, flag the conflict, and
      // reload from page one; the caller re-requests the move after confirming.
      set({ entries: prev, error: message(e) });
      if (e instanceof ApiError && e.status === 409) {
        await get().load();
        set({ conflict: true });
      }
      throw e;
    }
  },
}));

function mergeEntries(existing: MediaListEntry[], incoming: MediaListEntry[]): MediaListEntry[] {
  const byId = new Map<string, MediaListEntry>();
  for (const e of existing) byId.set(e.itemId, e);
  for (const e of incoming) byId.set(e.itemId, e);
  return Array.from(byId.values()).sort((a, b) => a.position - b.position);
}

function reorderLocal(entries: MediaListEntry[], itemId: string, beforeItemId: string | null): MediaListEntry[] {
  const without = entries.filter((e) => e.itemId !== itemId);
  const moved = entries.find((e) => e.itemId === itemId);
  if (!moved) return entries;
  if (!beforeItemId) return [...without, moved];
  const out: MediaListEntry[] = [];
  for (const e of without) {
    if (e.itemId === beforeItemId) out.push(moved);
    out.push(e);
  }
  if (!out.includes(moved)) out.push(moved);
  return out;
}
