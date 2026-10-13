import { create } from "zustand";
import { clearAccessToken, invalidateAuthGeneration, signOutNative } from "@/lib/deviceAuth";
import {
  clearConnectionProfile,
  loadConnectionProfile,
  saveConnectionProfile,
} from "@/lib/connectionProfile";
import { isNativeApp } from "@/lib/nativeEnv";
import { setServerConfig } from "@/lib/serverConfig";

export type ConnectionState = "unconfigured" | "validating" | "configured" | "error";
export type ConnectionError =
  | "unreachable"
  | "invalid_address"
  | "https_required"
  | "version_skew_hard"
  | "credential_revoked";

export interface ConnectionStoreState {
  state: ConnectionState;
  serverUrl: string;
  serverVersion: string | null;
  minClientVersion: string | null;
  versionSkewSoft: boolean;
  error: ConnectionError | null;
  errorMessage: string | null;

  setValidating: (url: string) => void;
  /** The node answered and is acceptable: remember its origin and point the API layer at it. */
  setConfigured: (serverVersion?: string) => void;
  setError: (error: ConnectionError, message?: string) => void;
  setVersionSkewSoft: (skew: boolean) => void;
  reset: () => void;
  /** Native startup: adopt the remembered node origin (and nothing else) from the stored profile. */
  hydrateFromProfile: () => void;
  syncServerConfig: (accessToken?: string | null) => void;
}

export const useConnectionStore = create<ConnectionStoreState>((set, get) => ({
  state: "unconfigured",
  serverUrl: "",
  serverVersion: null,
  minClientVersion: null,
  versionSkewSoft: false,
  error: null,
  errorMessage: null,

  setValidating: (url: string) => {
    set({
      state: "validating",
      serverUrl: url,
      error: null,
      errorMessage: null,
    });
  },

  setConfigured: (serverVersion) => {
    const origin = get().serverUrl;
    if (isNativeApp()) {
      const previous = loadConnectionProfile();
      if (previous && previous.origin !== origin) {
        // Moving to another node abandons the current sign-in: authority ends
        // immediately, and the old node is asked to revoke the device and the
        // credential is deleted, so nothing from this session can follow the
        // member to the new origin.
        void signOutNative();
      }
      if (!previous || previous.origin !== origin) {
        saveConnectionProfile({ origin, userId: null, username: null, deviceId: null });
      }
    }
    set({
      state: "configured",
      serverVersion: serverVersion ?? null,
      error: null,
      errorMessage: null,
    });
    get().syncServerConfig();
  },

  setError: (error: ConnectionError, message?: string) => {
    set({
      state: "error",
      error,
      errorMessage: message ?? null,
    });
  },

  setVersionSkewSoft: (skew: boolean) => {
    set({ versionSkewSoft: skew });
  },

  reset: () => {
    invalidateAuthGeneration();
    clearAccessToken();
    if (isNativeApp()) clearConnectionProfile();
    set({
      state: "unconfigured",
      serverUrl: "",
      serverVersion: null,
      minClientVersion: null,
      versionSkewSoft: false,
      error: null,
      errorMessage: null,
    });
    setServerConfig({
      baseUrl: "",
      mode: "cookie",
      accessToken: null,
    });
  },

  hydrateFromProfile: () => {
    if (!isNativeApp() || get().state === "configured") return;
    const profile = loadConnectionProfile();
    if (!profile) return;
    set({ state: "configured", serverUrl: profile.origin, error: null, errorMessage: null });
    // Token mode with no access token: the first authenticated call rotates the
    // stored credential. Nothing secret is read here.
    get().syncServerConfig(null);
  },

  syncServerConfig: (accessToken = null) => {
    const { serverUrl } = get();
    setServerConfig({
      baseUrl: serverUrl,
      mode: serverUrl ? "token" : "cookie",
      accessToken: accessToken ?? null,
    });
  },
}));
