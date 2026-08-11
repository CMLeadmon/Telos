export type AuthMode = "cookie" | "token";

export interface ServerConfig {
  baseUrl: string; // "" means same-origin
  mode: AuthMode;
  accessToken: string | null;
}

let activeConfig: ServerConfig = {
  baseUrl: "",
  mode: "cookie",
  accessToken: null,
};

export function getServerConfig(): ServerConfig {
  // A copy: the active config is process-wide, and handing out the live object
  // lets any reader mutate every caller's server address by assignment.
  return { ...activeConfig };
}

// normalizeBaseUrl validates once, here, rather than at each use. apiBase()
// concatenates the value directly while wsBase() has to parse it, so an
// unchecked address fails two different ways at two different call sites — a
// missing scheme throws out of the socket helper but silently produces a
// relative HTTP path. Trailing slashes are stripped so every caller can append
// a leading-slash path without doubling the separator.
export function normalizeBaseUrl(raw: string): string {
  if (!raw) return "";
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    throw new Error(`Telos server address is not a valid URL: ${raw}`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error(`Telos server address must be http or https: ${raw}`);
  }
  return `${parsed.origin}${parsed.pathname.replace(/\/+$/, "")}`;
}

export function setServerConfig(next: ServerConfig): void {
  activeConfig = { ...next, baseUrl: normalizeBaseUrl(next.baseUrl) };
}

/**
 * Whether this build has to ask which node to talk to.
 *
 * A build served over the web is served *by* a Telos node, so its own origin is
 * the answer and asking would be nonsense. Only a native shell, which loads its
 * assets from disk, starts out not knowing. Detected by the bridge the shell
 * injects rather than by URL scheme, because Tauri v2 serves over
 * http://tauri.localhost on Windows and a scheme check would misread it.
 */
export function requiresServerSelection(): boolean {
  if (typeof window === "undefined") return false;
  return "__TAURI_INTERNALS__" in window || "__TELOS_NATIVE_TLS__" in window;
}
