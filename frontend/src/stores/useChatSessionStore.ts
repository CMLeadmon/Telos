import { create } from "zustand";
import { api, wsBase } from "@/lib/api";
import { useAuthStore } from "./useAuthStore";

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
    online: {
      userId: string;
      username: string;
      displayName: string;
      role: string;
      avatar: string;
      avatarUrl: string;
    }[];
    count: number;
  };
}

interface ChatSessionState {
  channels: Channel[];
  activeChannelId: string | null;
  messages: ChatMessage[];
  connection: "idle" | "connecting" | "open" | "closed";
  fetchChannels: () => Promise<void>;
  connect: (channelId: string) => void;
  disconnect: () => void;
  send: (content: string) => Promise<void>;
  editMessage: (id: string, content: string) => Promise<void>;
  deleteMessage: (id: string) => Promise<void>;
  toggleReaction: (messageId: string, emoji: string) => Promise<void>;
}

let socket: WebSocket | null = null;

export const useChatSessionStore = create<ChatSessionState>()((set, get) => ({
  channels: [],
  activeChannelId: null,
  messages: [],
  connection: "idle",

  fetchChannels: async () => {
    const channels = await api<Channel[]>("/api/v1/channels");
    set({ channels });
  },

  connect: (channelId) => {
    get().disconnect();
    set({ activeChannelId: channelId, messages: [], connection: "connecting" });

    const ws = new WebSocket(
      `${wsBase()}/api/v1/chat/ws?channel=${encodeURIComponent(channelId)}`,
    );
    socket = ws;

    ws.onopen = () => {
      if (socket === ws) set({ connection: "open" });
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
      }
    };
    ws.onclose = () => {
      if (socket === ws) set({ connection: "closed" });
    };
  },

  disconnect: () => {
    if (socket) {
      const ws = socket;
      socket = null;
      ws.close();
    }
    set({ connection: "idle" });
  },

  send: async (content) => {
    const channelId = get().activeChannelId;
    if (!channelId || !content.trim()) return;
    await api(`/api/v1/channels/${channelId}/messages`, {
      method: "POST",
      body: JSON.stringify({ content }),
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
}));
