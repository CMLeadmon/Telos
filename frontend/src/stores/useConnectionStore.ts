import { create } from "zustand";
import { setServerConfig } from "@/lib/serverConfig";

export type ConnectionState = "unconfigured" | "validating" | "configured" | "error";
export type ConnectionError =
  | "unreachable"
  | "version_skew_hard"
  | "untrusted_certificate"
  | "credential_revoked";

export interface ConnectionStoreState {
  state: ConnectionState;
  serverUrl: string;
  pinnedCertFingerprint: string | null;
  serverVersion: string | null;
  minClientVersion: string | null;
  versionSkewSoft: boolean;
  error: ConnectionError | null;
  errorMessage: string | null;

  setValidating: (url: string) => void;
  setConfigured: (certFingerprint?: string, serverVersion?: string) => void;
  setError: (error: ConnectionError, message?: string) => void;
  setVersionSkewSoft: (skew: boolean) => void;
  reset: () => void;
  syncServerConfig: (accessToken?: string | null) => void;
}

export const useConnectionStore = create<ConnectionStoreState>((set, get) => ({
  state: "unconfigured",
  serverUrl: "",
  pinnedCertFingerprint: null,
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

  setConfigured: (certFingerprint, serverVersion) => {
    set({
      state: "configured",
      pinnedCertFingerprint: certFingerprint ?? null,
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
    set({
      state: "unconfigured",
      serverUrl: "",
      pinnedCertFingerprint: null,
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

  syncServerConfig: (accessToken = null) => {
    const { serverUrl } = get();
    setServerConfig({
      baseUrl: serverUrl,
      mode: serverUrl ? "token" : "cookie",
      accessToken: accessToken ?? null,
    });
  },
}));
