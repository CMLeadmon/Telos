import { create } from "zustand";
import { api, ApiError, wsBase } from "@/lib/api";
import { useAuthStore } from "./useAuthStore";

// mergeReplies upserts replies by id (idempotent with socket delivery) and
// keeps ascending order.
function mergeReplies(existing: ChatMessage[], incoming: ChatMessage[]): ChatMessage[] {
  const byId = new Map<string, ChatMessage>();
  for (const m of existing) byId.set(m.id, m);
  for (const m of incoming) byId.set(m.id, m);
  return Array.from(byId.values()).sort((a, b) => {
    const ta = a.timestamp ?? "";
    const tb = b.timestamp ?? "";
    if (ta === tb) return a.id < b.id ? -1 : 1;
    return ta < tb ? -1 : 1;
  });
}

function apiMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  return "Something went wrong.";
}

export interface Channel {
  id: string;
  name: string;
  type: "text" | "voice";
}

export interface ChatMessage {
  id: string;
  sender: string;
  senderId?: string;
  displayName?: string;
  avatar: string;
  avatarUrl?: string;
  role: string;
  content: string;
  timestamp: string;
  reactions?: {
    emoji: string;
    count: number;
    users: string[];
  }[];
  editedAt?: string;
  deleted?: boolean;
  pinned?: boolean;
  embed?: {
    kind: "library_book" | "stream_film" | "file";
    ref: string;
    snapshot: unknown;
  };
}

export interface PresenceUser {
  userId: string;
  username: string;
  displayName: string;
  role: string;
  avatar: string;
  avatarUrl: string;
}

interface WSNotification {
  type: "history" | "message" | "message.update" | "message.delete" | "reaction" | "pin" | "presence";
  messages?: ChatMessage[];
  message?: ChatMessage;
  messageId?: string;
  channelId?: string;
  reaction?: {
    messageId: string;
    emoji: string;
    userId: string;
    op: "add" | "remove";
    count: number;
  };
  pin?: {
    messageId: string;
    op: "add" | "remove";
  };
  presence?: {
    channelId?: string;
    online: PresenceUser[];
    count: number;
  };
}

interface ChatSessionState {
  channels: Channel[];
  activeChannelId: string | null;
  messages: ChatMessage[];
  connection: "idle" | "connecting" | "open" | "closed";
  online: PresenceUser[];
  onlineCount: number;
  pins: ChatMessage[];
  fetchChannels: () => Promise<void>;
  connect: (channelId: string) => void;
  disconnect: () => void;
  send: (content: string, embed?: { kind: string; ref: string }) => Promise<void>;
  editMessage: (id: string, content: string) => Promise<void>;
  deleteMessage: (id: string) => Promise<void>;
  toggleReaction: (messageId: string, emoji: string) => Promise<void>;
  togglePin: (messageId: string) => Promise<void>;

  // One-level threads.
  activeThreadRoot: ChatMessage | null;
  threadReplies: ChatMessage[];
  threadNextCursor: string | null;
  threadStatus: "idle" | "loading" | "ready" | "error";
  threadDraftError: string | null;
  // A stable client mutation id retained with the current unacknowledged draft
  // so every retry maps to the same durable message.
  pendingReplyMutationId: string | null;
  openThread: (root: ChatMessage) => Promise<void>;
  loadMoreReplies: () => Promise<void>;
  sendReply: (content: string) => Promise<void>;
  closeThread: () => void;
}

interface ReplyPage {
  items: ChatMessage[];
  nextCursor?: string;
}

// newMutationId returns a stable idempotency key for a draft submission.
function newMutationId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `m-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempts = 0;

// Bounded exponential backoff with jitter for WebSocket reconnection.
const RECONNECT_BASE_MS = 500;
const RECONNECT_MAX_MS = 15_000;

function nextReconnectDelay(): number {
  const exp = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** reconnectAttempts);
  reconnectAttempts += 1;
  return exp / 2 + Math.random() * (exp / 2); // full-range jitter over [exp/2, exp]
}

function clearReconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  reconnectAttempts = 0;
}

export const useChatSessionStore = create<ChatSessionState>()((set, get) => ({
  channels: [],
  activeChannelId: null,
  messages: [],
  connection: "idle",
  online: [],
  onlineCount: 0,
  pins: [],

  activeThreadRoot: null,
  threadReplies: [],
  threadNextCursor: null,
  threadStatus: "idle",
  threadDraftError: null,
  pendingReplyMutationId: null,

  fetchChannels: async () => {
    const channels = await api<Channel[]>("/api/v1/channels");
    set({ channels });
  },

  openThread: async (root) => {
    set({
      activeThreadRoot: root,
      threadReplies: [],
      threadNextCursor: null,
      threadStatus: "loading",
      threadDraftError: null,
      pendingReplyMutationId: null,
    });
    const channelId = get().activeChannelId;
    if (!channelId) return;
    try {
      const page = await api<ReplyPage>(
        `/api/v1/channels/${channelId}/messages/${root.id}/replies?limit=50`,
      );
      set({ threadReplies: page.items ?? [], threadNextCursor: page.nextCursor ?? null, threadStatus: "ready" });
    } catch (e) {
      set({ threadStatus: "error", threadDraftError: apiMessage(e) });
    }
  },

  loadMoreReplies: async () => {
    const { activeThreadRoot, threadNextCursor, activeChannelId, threadStatus } = get();
    if (!activeThreadRoot || !threadNextCursor || !activeChannelId || threadStatus === "loading") return;
    set({ threadStatus: "loading" });
    try {
      const page = await api<ReplyPage>(
        `/api/v1/channels/${activeChannelId}/messages/${activeThreadRoot.id}/replies?limit=50&cursor=${encodeURIComponent(threadNextCursor)}`,
      );
      set((s) => ({
        threadReplies: mergeReplies(s.threadReplies, page.items ?? []),
        threadNextCursor: page.nextCursor ?? null,
        threadStatus: "ready",
      }));
    } catch (e) {
      set({ threadStatus: "error", threadDraftError: apiMessage(e) });
    }
  },

  sendReply: async (content) => {
    const { activeThreadRoot, activeChannelId } = get();
    if (!activeThreadRoot || !activeChannelId || !content.trim()) return;
    // One mutation id per draft, reused across retries until acknowledgement.
    const mutationId = get().pendingReplyMutationId ?? newMutationId();
    set({ pendingReplyMutationId: mutationId, threadDraftError: null });
    try {
      const res = await api<{ message: ChatMessage }>(
        `/api/v1/channels/${activeChannelId}/messages/${activeThreadRoot.id}/replies`,
        { method: "POST", body: JSON.stringify({ content, clientMutationId: mutationId }) },
      );
      // Acknowledged: merge by id (idempotent with any socket delivery) and
      // clear the draft's mutation id so the input can be cleared.
      set((s) => ({
        threadReplies: mergeReplies(s.threadReplies, res.message ? [res.message] : []),
        pendingReplyMutationId: null,
        threadDraftError: null,
      }));
    } catch (e) {
      // Preserve the draft + mutation id so a retry maps to one message.
      set({ threadDraftError: apiMessage(e) });
      throw e;
    }
  },

  closeThread: () => {
    set({
      activeThreadRoot: null,
      threadReplies: [],
      threadNextCursor: null,
      threadStatus: "idle",
      threadDraftError: null,
      pendingReplyMutationId: null,
    });
  },

  connect: (channelId) => {
    get().disconnect();
    clearReconnect();
    set({ activeChannelId: channelId, messages: [], connection: "connecting" });

    api<{ online: PresenceUser[]; count: number }>(`/api/v1/channels/${channelId}/members`)
      .then((res) => {
        set({ online: res.online, onlineCount: res.count });
      })
      .catch((err) => {
        console.error("Failed to fetch channel members:", err);
      });

    api<{ pins: ChatMessage[] }>(`/api/v1/channels/${channelId}/pins`)
      .then((res) => {
        set({ pins: res.pins });
      })
      .catch((err) => {
        console.error("Failed to fetch channel pins:", err);
      });

    const ws = new WebSocket(
      `${wsBase()}/api/v1/chat/ws?channel=${encodeURIComponent(channelId)}`,
    );
    socket = ws;

    ws.onopen = () => {
      if (socket === ws) {
        reconnectAttempts = 0;
        set({ connection: "open" });
      }
    };
    ws.onmessage = (event) => {
      if (socket !== ws) return;
      let notification: WSNotification;
      try {
        notification = JSON.parse(event.data);
      } catch {
        return;
      }
      if (notification.type === "history" && notification.messages) {
        set({ messages: notification.messages });
      } else if (notification.type === "message" && notification.message) {
        set((s) => ({ messages: [...s.messages, notification.message!] }));
      } else if (notification.type === "message.update" && notification.message) {
        set((s) => ({
          messages: s.messages.map((m) => (m.id === notification.message!.id ? notification.message! : m)),
        }));
      } else if (notification.type === "message.delete" && notification.messageId) {
        set((s) => ({
          messages: s.messages.map((m) =>
            m.id === notification.messageId
              ? { ...m, deleted: true, content: "", embed: undefined, reactions: [] }
              : m,
          ),
        }));
      } else if (notification.type === "reaction" && notification.reaction) {
        const { messageId, emoji, userId, op, count } = notification.reaction;
        set((s) => ({
          messages: s.messages.map((m) => {
            if (m.id !== messageId) return m;
            const reactions = [...(m.reactions ?? [])];
            const i = reactions.findIndex((x) => x.emoji === emoji);
            if (op === "add") {
              if (i === -1) {
                reactions.push({ emoji, count, users: [userId] });
              } else {
                reactions[i] = {
                  ...reactions[i],
                  count,
                  users: [...reactions[i].users.filter((u) => u !== userId), userId],
                };
              }
            } else if (i !== -1) {
              const users = reactions[i].users.filter((u) => u !== userId);
              if (count <= 0) {
                reactions.splice(i, 1);
              } else {
                reactions[i] = { ...reactions[i], count, users };
              }
            }
            return { ...m, reactions };
          }),
        }));
      } else if (notification.type === "presence" && notification.presence) {
        set({ online: notification.presence.online, onlineCount: notification.presence.count });
      } else if (notification.type === "pin" && notification.pin) {
        const { messageId, op } = notification.pin;
        const activeId = get().activeChannelId;
        set((s) => ({
          messages: s.messages.map((m) =>
            m.id === messageId ? { ...m, pinned: op === "add" } : m,
          ),
        }));
        if (activeId) {
          api<{ pins: ChatMessage[] }>(`/api/v1/channels/${activeId}/pins`)
            .then((res) => {
              set({ pins: res.pins });
            })
            .catch((err) => {
              console.error("Failed to refresh pins:", err);
            });
        }
      }
    };
    ws.onclose = (event) => {
      if (socket !== ws) return;
      socket = null;
      set({ connection: "closed" });
      // A policy-violation close (1008) means the server revoked access
      // (session invalidated, account disabled, permission removed). Do not
      // reconnect; surface it to the auth layer to re-authenticate.
      if (event.code === 1008) {
        void useAuthStore.getState().fetchMe();
        return;
      }
      // Otherwise reconnect with bounded exponential backoff + jitter, then
      // reconcile durable state via the REST history/members/pins fetch that
      // connect() performs.
      const active = get().activeChannelId;
      if (active === channelId) {
        reconnectTimer = setTimeout(() => {
          if (get().activeChannelId === channelId) {
            get().connect(channelId);
          }
        }, nextReconnectDelay());
      }
    };
  },

  disconnect: () => {
    clearReconnect();
    if (socket) {
      const ws = socket;
      socket = null;
      ws.onclose = null;
      ws.close(1000, "client disconnect");
    }
    set({ connection: "idle" });
  },

  send: async (content, embed) => {
    const channelId = get().activeChannelId;
    if (!channelId || (!content.trim() && !embed)) return;
    await api(`/api/v1/channels/${channelId}/messages`, {
      method: "POST",
      body: JSON.stringify({ content, embed }),
    });
  },

  editMessage: async (id, content) => {
    const channelId = get().activeChannelId;
    if (!channelId) return;
    await api(`/api/v1/channels/${channelId}/messages/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ content }),
    });
  },

  deleteMessage: async (id) => {
    const channelId = get().activeChannelId;
    if (!channelId) return;
    await api(`/api/v1/channels/${channelId}/messages/${id}`, {
      method: "DELETE",
    });
  },

  toggleReaction: async (messageId, emoji) => {
    const channelId = get().activeChannelId;
    if (!channelId) return;
    const msg = get().messages.find((m) => m.id === messageId);
    if (!msg) return;
    const myId = useAuthStore.getState().user?.ID;
    if (!myId) return;
    const rx = msg.reactions?.find((r) => r.emoji === emoji);
    const hasReacted = rx?.users.includes(myId);

    if (hasReacted) {
      await api(
        `/api/v1/channels/${channelId}/messages/${messageId}/reactions/${encodeURIComponent(emoji)}`,
        {
          method: "DELETE",
        },
      );
    } else {
      await api(`/api/v1/channels/${channelId}/messages/${messageId}/reactions`, {
        method: "POST",
        body: JSON.stringify({ emoji }),
      });
    }
  },

  togglePin: async (messageId) => {
    const channelId = get().activeChannelId;
    if (!channelId) return;
    const msg = get().messages.find((m) => m.id === messageId);
    const pinned = msg?.pinned;
    if (pinned) {
      await api(`/api/v1/channels/${channelId}/pins/${messageId}`, {
        method: "DELETE",
      });
    } else {
      await api(`/api/v1/channels/${channelId}/pins`, {
        method: "POST",
        body: JSON.stringify({ messageId }),
      });
    }
  },
}));
