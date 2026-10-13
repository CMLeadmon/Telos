import { create } from "zustand";
import { api, ApiError } from "@/lib/api";
import {
  retryCredentialCleanup,
  signInWithPassword,
  signOutNative,
  startNativeSession,
} from "@/lib/deviceAuth";
import { isNativeApp } from "@/lib/nativeEnv";
import { useConnectionStore } from "@/stores/useConnectionStore";
import { nativeDeviceInfo } from "@/lib/platform";
import { CredentialStoreError, consumeLegacyStoreNotice } from "@/lib/secureStorage";

export interface CurrentUser {
  ID: string;
  Username: string;
  Roles: string[];
  Permissions: string[];
  DisplayName: string;
  HasAvatar: boolean;
}

export const NATIVE_ACCOUNT_MESSAGE =
  "Create your account in the hosted browser first, then sign in here with the same username and passphrase.";

interface AuthState {
  user: CurrentUser | null;
  status: "unknown" | "authenticated" | "anonymous";
  connectivity: "online" | "reconnecting" | "error";
  /** A native credential-service problem the member should read (never a secret). */
  credentialIssue: string | null;
  /** A local credential could not be deleted; automatic sign-in is suppressed until it can. */
  cleanupRequired: boolean;
  /** A one-time message after startup or sign-out (not an error). */
  notice: string | null;
  setConnectivity: (connectivity: "online" | "reconnecting" | "error") => void;
  /** Startup: cookie browsers ask /auth/me; the native client rotates its stored credential first. */
  initializeAuth: () => Promise<void>;
  /** A plain /auth/me refresh of the signed-in user (no rotation of its own). */
  fetchMe: () => Promise<void>;
  login: (username: string, password: string, signal?: AbortSignal) => Promise<void>;
  bootstrap: (username: string, password: string, token: string, signal?: AbortSignal) => Promise<void>;
  acceptInvite: (username: string, password: string, token: string, signal?: AbortSignal) => Promise<void>;
  logout: () => Promise<void>;
  /** Retry a failed local credential deletion. */
  retryCleanup: () => Promise<void>;
}

const REVOKE_PENDING_NOTICE =
  "You are signed out on this device, but the node could not be reached to revoke it. Remove this device from Settings in the hosted browser.";

function storeMessage(error: unknown): string {
  return error instanceof CredentialStoreError
    ? error.message
    : "The operating-system credential service reported an error.";
}

export const useAuthStore = create<AuthState>()((set, get) => ({
  user: null,
  status: "unknown",
  connectivity: "online",
  credentialIssue: null,
  cleanupRequired: false,
  notice: null,

  setConnectivity: (connectivity) => set({ connectivity }),

  initializeAuth: async () => {
    if (!isNativeApp()) {
      await get().fetchMe();
      return;
    }
    // Restore the remembered node origin before anything is sent.
    useConnectionStore.getState().hydrateFromProfile();
    set({ connectivity: "reconnecting" });
    const result = await startNativeSession<CurrentUser>();
    switch (result.kind) {
      case "authenticated":
        set({ user: result.me, status: "authenticated", connectivity: "online", credentialIssue: null, cleanupRequired: false });
        return;
      case "anonymous": {
        const legacy = await consumeLegacyStoreNotice();
        set({
          user: null,
          status: "anonymous",
          connectivity: "online",
          notice: legacy
            ? "This version keeps your sign-in in the operating system's credential service. Sign in again, and remove your old devices from Settings in the hosted browser."
            : result.reason === "revoked"
              ? "This device was signed out by the node. Sign in again."
              : null,
        });
        return;
      }
      case "cleanup-required":
        set({
          user: null,
          status: "anonymous",
          connectivity: "online",
          cleanupRequired: true,
          credentialIssue: storeMessage(result.error),
        });
        return;
      case "credential-error":
        set({ user: null, status: "anonymous", connectivity: "online", credentialIssue: storeMessage(result.error) });
        return;
      case "unreachable":
        // The credential and profile are kept; only the node is unreachable.
        set({ connectivity: "error" });
        return;
      case "stale":
        return;
    }
  },

  fetchMe: async () => {
    try {
      const user = await api<CurrentUser>("/api/v1/auth/me");
      set({ user, status: "authenticated", connectivity: "online" });
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        set({ user: null, status: "anonymous", connectivity: "online" });
      } else {
        set((state) => ({
          ...state,
          status: state.status === "authenticated" ? "authenticated" : isNativeApp() ? "unknown" : "anonymous",
          connectivity: "error",
        }));
      }
    }
  },

  login: async (username, password, signal) => {
    if (isNativeApp()) {
      const user = await signInWithPassword<CurrentUser>(nativeDeviceInfo(), { username, password }, signal);
      set({ user, status: "authenticated", connectivity: "online", credentialIssue: null, cleanupRequired: false, notice: null });
      return;
    }
    await api("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
      signal,
    });
    const user = await api<CurrentUser>("/api/v1/auth/me", { signal });
    set({ user, status: "authenticated" });
  },

  // Account creation stays in the hosted browser for the beta: bootstrap and
  // invite secrets are never sent through device registration.
  bootstrap: async (username, password, token, signal) => {
    if (isNativeApp()) throw new ApiError(400, NATIVE_ACCOUNT_MESSAGE);
    await api("/api/v1/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
      signal,
    });
    if (signal?.aborted) throw new ApiError(408, "Request timed out.");
    await get().login(username, password, signal);
  },

  acceptInvite: async (username, password, token, signal) => {
    if (isNativeApp()) throw new ApiError(400, NATIVE_ACCOUNT_MESSAGE);
    await api("/api/v1/auth/invites/accept", {
      method: "POST",
      body: JSON.stringify({ username, password, token }),
      signal,
    });
    if (signal?.aborted) throw new ApiError(408, "Request timed out.");
    await get().login(username, password, signal);
  },

  logout: async () => {
    if (isNativeApp()) {
      const result = await signOutNative();
      set({
        user: null,
        status: "anonymous",
        cleanupRequired: result.cleanup === "failed",
        credentialIssue: result.cleanupError ? storeMessage(result.cleanupError) : null,
        notice: result.remote === "pending" ? REVOKE_PENDING_NOTICE : null,
      });
      return;
    }
    try {
      await api("/api/v1/auth/logout", { method: "POST" });
    } finally {
      set({ user: null, status: "anonymous" });
    }
  },

  retryCleanup: async () => {
    const result = await retryCredentialCleanup();
    set({
      cleanupRequired: !result.done,
      credentialIssue: result.done ? null : storeMessage(result.error),
    });
  },
}));
