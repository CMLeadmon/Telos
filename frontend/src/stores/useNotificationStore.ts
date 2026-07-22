import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export type NotificationKind =
  | "mention"
  | "thread_reply"
  | "annotation_reply"
  | "watch_party_invite"
  | "account_security";

export interface AppNotification {
  id: string;
  actorId?: string;
  kind: NotificationKind;
  resourceType: string;
  resourceId: string;
  payload: Record<string, unknown>;
  sequence: number;
  read: boolean;
  createdAt: string;
}

interface NotificationPage {
  items: AppNotification[];
  nextCursor?: string;
}

interface UserEvent {
  sequence: number;
  kind: string;
  resourceType: string;
  resourceId: string;
  payload: Record<string, unknown>;
  createdAt: string;
}

interface CatchUpResponse {
  items: UserEvent[];
  nextAfter: number;
  highWater: number;
  hasMore: boolean;
}

type Status = "idle" | "loading" | "ready" | "error";

interface NotificationState {
  items: AppNotification[];
  nextCursor: string | null;
  unreadCount: number;
  lastEventSequence: number;
  catchUpHighWater: number;
  status: Status;
  error: string | null;

  loadInitial: () => Promise<void>;
  loadMore: () => Promise<void>;
  refreshUnread: () => Promise<void>;
  markRead: (id: string) => Promise<void>;
  markAllRead: () => Promise<void>;
  catchUp: () => Promise<void>;
  reconcile: () => Promise<void>;
  applyEvent: (hint: { sequence: number }) => void;
}

// mergeById upserts notifications, keeping the newest by createdAt/sequence and
// de-duplicating by stable id so a socket hint and a catch-up refresh cannot
// double-insert the same notification.
function mergeById(existing: AppNotification[], incoming: AppNotification[]): AppNotification[] {
  const byId = new Map<string, AppNotification>();
  for (const n of existing) byId.set(n.id, n);
  for (const n of incoming) byId.set(n.id, n);
  return Array.from(byId.values()).sort((a, b) => {
    if (a.createdAt === b.createdAt) return b.sequence - a.sequence;
    return a.createdAt < b.createdAt ? 1 : -1;
  });
}

export const useNotificationStore = create<NotificationState>((set, get) => ({
  items: [],
  nextCursor: null,
  unreadCount: 0,
  lastEventSequence: 0,
  catchUpHighWater: 0,
  status: "idle",
  error: null,

  loadInitial: async () => {
    set({ status: "loading", error: null });
    try {
      const page = await api<NotificationPage>("/api/v1/notifications?limit=25");
      const items = page.items ?? [];
      const maxSeq = items.reduce((m, n) => Math.max(m, n.sequence), get().lastEventSequence);
      set({
        items,
        nextCursor: page.nextCursor ?? null,
        status: "ready",
        lastEventSequence: maxSeq,
      });
      await get().refreshUnread();
    } catch (e) {
      set({ status: "error", error: errorMessage(e) });
    }
  },

  loadMore: async () => {
    const cursor = get().nextCursor;
    if (!cursor || get().status === "loading") return;
    set({ status: "loading" });
    try {
      const page = await api<NotificationPage>(
        `/api/v1/notifications?limit=25&cursor=${encodeURIComponent(cursor)}`,
      );
      set((s) => ({
        items: mergeById(s.items, page.items ?? []),
        nextCursor: page.nextCursor ?? null,
        status: "ready",
      }));
    } catch (e) {
      set({ status: "error", error: errorMessage(e) });
    }
  },

  refreshUnread: async () => {
    try {
      const { count } = await api<{ count: number }>("/api/v1/notifications/unread-count");
      set({ unreadCount: count });
    } catch {
      // A failed count is non-fatal; keep the last known value.
    }
  },

  markRead: async (id: string) => {
    const prev = get().items;
    const target = prev.find((n) => n.id === id);
    if (!target || target.read) return;
    // Optimistic: mark read and drop the badge, roll back on failure.
    set((s) => ({
      items: s.items.map((n) => (n.id === id ? { ...n, read: true } : n)),
      unreadCount: Math.max(0, s.unreadCount - 1),
    }));
    try {
      await api(`/api/v1/notifications/${id}/read`, { method: "PUT" });
    } catch (e) {
      set({ items: prev, error: errorMessage(e) });
      await get().refreshUnread();
    }
  },

  markAllRead: async () => {
    const prev = get().items;
    const prevCount = get().unreadCount;
    set((s) => ({ items: s.items.map((n) => ({ ...n, read: true })), unreadCount: 0 }));
    try {
      await api("/api/v1/notifications/read-all", { method: "PUT" });
    } catch (e) {
      set({ items: prev, unreadCount: prevCount, error: errorMessage(e) });
    }
  },

  // catchUp pages the durable event API from the last applied sequence through
  // its sampled high-water, then refreshes the inbox once so a socket gap is
  // recovered exactly once. The high-water is retained across pages.
  catchUp: async () => {
    let after = get().lastEventSequence;
    let through = 0; // sample the current maximum on the first page
    let sawNew = false;
    let highWater = get().catchUpHighWater;
    try {
      for (let guard = 0; guard < 100; guard++) {
        const q = `afterSequence=${after}${through ? `&throughSequence=${through}` : ""}&limit=100`;
        const res = await api<CatchUpResponse>(`/api/v1/events?${q}`);
        through = res.highWater;
        highWater = res.highWater;
        if (res.items.length > 0) sawNew = true;
        after = res.nextAfter;
        if (!res.hasMore) break;
      }
      set({ catchUpHighWater: highWater });
      if (sawNew || highWater > get().lastEventSequence) {
        await get().loadInitial();
      }
      set({ lastEventSequence: Math.max(get().lastEventSequence, highWater) });
    } catch (e) {
      set({ error: errorMessage(e) });
    }
  },

  // reconcile is called on (re)connect: it always runs a catch-up so any events
  // missed while disconnected are recovered before live hints are trusted.
  reconcile: async () => {
    await get().catchUp();
  },

  // applyEvent handles a live socket hint. A contiguous next sequence is applied
  // directly; a gap triggers a bounded catch-up; a duplicate/old sequence is
  // ignored.
  applyEvent: (hint: { sequence: number }) => {
    const last = get().lastEventSequence;
    if (hint.sequence <= last) return; // duplicate or stale
    if (hint.sequence === last + 1) {
      set({ lastEventSequence: hint.sequence });
      void get().loadInitial();
      return;
    }
    // Gap detected — fill it exactly once.
    void get().catchUp();
  },
}));

function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  return "Something went wrong.";
}
