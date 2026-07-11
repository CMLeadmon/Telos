import { create } from "zustand";
import { api, wsBase } from "@/lib/api";

export interface Channel {
  id: string;
  name: string;
  type: "text" | "voice";
}

export interface ChatMessage {
  id: string;
  sender: string;
  avatar: string;
  role: string;
  content: string;
  timestamp: string;
}

interface WSNotification {
  type: "history" | "message";
  messages?: ChatMessage[];
  message?: ChatMessage;
}

interface ChatSessionState {
  channels: Channel[];
  activeChannelId: string | null;
  messages: ChatMessage[];
  connection: "idle" | "connecting" | "open" | "closed";
  fetchChannels: () => Promise<void>;
  connect: (channelId: string) => void;
  disconnect: () => void;
  send: (content: string) => void;
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

  send: (content) => {
    if (socket?.readyState === WebSocket.OPEN && content.trim()) {
      socket.send(JSON.stringify({ content }));
    }
  },
}));
