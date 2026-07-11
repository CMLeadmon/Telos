import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface CurrentUser {
  ID: string;
  Username: string;
  Roles: string[];
}

interface AuthState {
  user: CurrentUser | null;
  status: "unknown" | "authenticated" | "anonymous";
  fetchMe: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  bootstrap: (username: string, password: string, token: string) => Promise<void>;
  acceptInvite: (username: string, password: string, token: string) => Promise<void>;
  logout: () => Promise<void>;
}

export const useAuthStore = create<AuthState>()((set, get) => ({
  user: null,
  status: "unknown",

  fetchMe: async () => {
    try {
      const user = await api<CurrentUser>("/api/v1/auth/me");
      set({ user, status: "authenticated" });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        set({ user: null, status: "anonymous" });
      } else {
        set({ status: "anonymous" });
      }
    }
  },

  login: async (username, password) => {
    await api("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
    await get().fetchMe();
  },

  bootstrap: async (username, password, token) => {
    await api("/api/v1/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
    });
    await get().login(username, password);
  },

  acceptInvite: async (username, password, token) => {
    await api("/api/v1/auth/invites/accept", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
    });
    await get().login(username, password);
  },

  logout: async () => {
    try {
      await api("/api/v1/auth/logout", { method: "POST" });
    } finally {
      set({ user: null, status: "anonymous" });
    }
  },
}));
