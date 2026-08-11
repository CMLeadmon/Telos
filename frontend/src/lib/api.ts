// Single-origin gateway client. In production the static export is served by
// the Go gateway. The development server proxies these same paths to the local
// gateway, so browsers never need direct access to an additional port.

import { getServerConfig, normalizeBaseUrl } from "@/lib/serverConfig";

export function apiBase(): string {
  return getServerConfig().baseUrl;
}

export function avatarUrl(userId: string): string {
  return `${apiBase()}/api/v1/users/${userId}/avatar`;
}

export function libraryCoverUrl(bookId: string): string {
  return `${apiBase()}/api/v1/library/books/${encodeURIComponent(bookId)}/cover`;
}

export function libraryContentUrl(bookId: string): string {
  return `${apiBase()}/api/v1/library/books/${encodeURIComponent(bookId)}/content`;
}

export function assetUrl(path: string | undefined): string {
  if (!path) return "";
  if (
    path.startsWith("http://") ||
    path.startsWith("https://") ||
    path.startsWith("blob:")
  ) {
    return path;
  }
  const base = apiBase();
  if (!base) return path;
  return `${base}${path.startsWith("/") ? "" : "/"}${path}`;
}

export function wsBase(): string {
  const cfg = getServerConfig();
  if (!cfg.baseUrl) {
    if (typeof window === "undefined") return "";
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${window.location.host}`;
  }
  // setServerConfig has already validated this as an absolute http(s) URL.
  const u = new URL(cfg.baseUrl);
  const proto = u.protocol === "https:" ? "wss:" : "ws:";
  // Keep any path prefix. apiBase() returns baseUrl whole, so a node served
  // under https://host/telos would take every HTTP call to /telos/api/... while
  // the socket went to the bare host — the one request that silently goes
  // somewhere else than all the others.
  return `${proto}//${u.host}${u.pathname.replace(/\/+$/, "")}`;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export interface NodeProbe {
  version: string | null;
  minClientVersion: string | null;
}

/**
 * Probes a candidate node before it becomes the configured one.
 *
 * api() resolves its origin from getServerConfig(), which is precisely what the
 * connect screen is still trying to decide, so this takes the base explicitly.
 * It sends no credentials: the host is unverified at this point and has no
 * business receiving any. It lives in this module because this is where network
 * calls are allowed to originate — check-origin-leaks.js fails a raw fetch
 * anywhere else.
 */
export async function probeNode(
  baseUrl: string,
  timeoutMs = 5000,
): Promise<NodeProbe> {
  const base = normalizeBaseUrl(baseUrl);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  let res: Response;
  try {
    res = await fetch(`${base}/api/v1/health`, { signal: controller.signal });
  } catch {
    throw new ApiError(
      0,
      "Cannot reach this Telos node. Check the server address and try again.",
    );
  } finally {
    clearTimeout(timer);
  }

  // A health report is served on both 200 and 503 — a degraded node is still a
  // Telos node, and refusing to connect to one would leave the member with no
  // way in precisely when something is wrong.
  const body = (await res.json().catch(() => null)) as {
    status?: string;
    version?: string;
    minClientVersion?: string;
  } | null;
  if (!body || typeof body.status !== "string") {
    throw new ApiError(
      res.status,
      "That address answered, but not like a Telos node.",
    );
  }
  return {
    version: body.version ?? null,
    minClientVersion: body.minClientVersion ?? null,
  };
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  const cfg = getServerConfig();
  try {
    const headers = new Headers(init?.headers);
    if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }

    const requestInit: RequestInit = {
      ...init,
      headers,
    };

    if (cfg.mode === "token") {
      // Token mode never falls back to cookies. Branching on the token's
      // presence instead would make a client that has not authenticated yet
      // send a credentialed cross-origin request, which is precisely what the
      // device-token flow exists to avoid; with no token the request should
      // reach the server bare and come back a clean 401.
      if (cfg.accessToken) {
        headers.set("Authorization", `Bearer ${cfg.accessToken}`);
      }
    } else {
      requestInit.credentials = "include";
    }

    res = await fetch(`${apiBase()}${path}`, requestInit);
  } catch {
    if (init?.signal?.aborted) {
      throw new ApiError(408, "Request timed out.");
    }
    throw new ApiError(
      0,
      "Cannot reach this Telos node. Check the server address and try again.",
    );
  }

  const contentType = res.headers.get("Content-Type") || "";
  if (
    contentType.includes("application/epub+zip") ||
    contentType.includes("application/pdf") ||
    contentType.includes("application/octet-stream") ||
    contentType.includes("application/x-mobipocket-ebook") ||
    path.endsWith("/content")
  ) {
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new ApiError(res.status, text.trim() || res.statusText);
    }
    return (await res.arrayBuffer()) as unknown as T;
  }

  const text = await res.text().catch(() => "");
  if (!res.ok) {
    throw new ApiError(res.status, text.trim() || res.statusText);
  }
  if (!text.trim()) return undefined as T;

  try {
    return JSON.parse(text) as T;
  } catch {
    throw new ApiError(res.status, "Server returned an invalid response.");
  }
}
