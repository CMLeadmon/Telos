import { create } from "zustand";
import { api, ApiError } from "@/lib/api";

export interface CurrentUser {
  ID: string;
  Username: string;
  Roles: string[];
  Permissions: string[];
  DisplayName: string;
  HasAvatar: boolean;
}

interface AuthState {
  user: CurrentUser | null;
  status: "unknown" | "authenticated" | "anonymous";
  fetchMe: () => Promise<void>;
  login: (
    username: string,
    password: string,
    signal?: AbortSignal,
  ) => Promise<void>;
  bootstrap: (
    username: string,
    password: string,
    token: string,
    signal?: AbortSignal,
  ) => Promise<void>;
  acceptInvite: (
    username: string,
    password: string,
    token: string,
    signal?: AbortSignal,
  ) => Promise<void>;
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
        set((state) =>
          state.status === "authenticated"
            ? state
            : { user: null, status: "anonymous" },
        );
      } else {
        set((state) =>
          state.status === "authenticated" ? state : { status: "anonymous" },
        );
      }
    }
  },

  login: async (username, password, signal) => {
    await api("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
      signal,
    });
    const user = await api<CurrentUser>("/api/v1/auth/me", { signal });
    set({ user, status: "authenticated" });
  },

  bootstrap: async (username, password, token, signal) => {
    await api("/api/v1/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
      signal,
    });
    if (signal?.aborted) throw new ApiError(408, "Request timed out.");
    await get().login(username, password, signal);
  },

  acceptInvite: async (username, password, token, signal) => {
    await api("/api/v1/auth/invites/accept", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
      signal,
    });
    if (signal?.aborted) throw new ApiError(408, "Request timed out.");
    await get().login(username, password, signal);
  },

  logout: async () => {
    try {
      await api("/api/v1/auth/logout", { method: "POST" });
    } finally {
      set({ user: null, status: "anonymous" });
    }
  },
}));
