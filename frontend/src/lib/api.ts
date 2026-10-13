// Single-origin gateway client. In production the static export is served by
// the Go gateway. The development server proxies these same paths to the local
// gateway, so browsers never need direct access to an additional port.

import { avatarPath, libraryCoverPath } from "@/lib/nodePaths";
import { getServerConfig, normalizeBaseUrl } from "@/lib/serverConfig";

export function apiBase(): string {
  return getServerConfig().baseUrl;
}

export function avatarUrl(userId: string): string {
  return `${apiBase()}${avatarPath(userId)}`;
}

export function libraryCoverUrl(bookId: string): string {
  return `${apiBase()}${libraryCoverPath(bookId)}`;
}

export function libraryContentUrl(bookId: string): string {
  return `${apiBase()}/api/v1/library/books/${encodeURIComponent(bookId)}/content`;
}

export function assetUrl(path: string | undefined): string {
  if (!path) return "";
  if (
    path.startsWith("http://") ||
    path.startsWith("https://") ||
    path.startsWith("blob:") ||
    // Carries its own bytes, like blob:. Prepending a node address to one
    // produces https://node/data:image/png;base64,… — an address for nothing.
    path.startsWith("data:")
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

/**
 * Per-call overrides. They exist for the few calls that must act on a node or
 * credential other than "whatever is configured right now": revoking a device
 * that has just been abandoned, or logging out with a captured token after local
 * state was already cleared.
 */
export interface ApiOptions {
  /** Talk to this origin instead of the configured one. */
  baseUrl?: string;
  /** Present exactly this bearer token (or none, with null) instead of the configured access token. */
  bearer?: string | null;
  /** Do not refresh an expiring access token first, and do not retry on 401. Used by the refresh/register/logout calls themselves. */
  skipRefresh?: boolean;
}

/**
 * The authenticated client's view of the token lifecycle, installed by
 * deviceAuth. api.ts cannot import it (deviceAuth imports api), so it is a hook.
 * With no refresher installed -- the browser build -- none of this runs.
 */
export interface AccessRefresher {
  /** Resolves once the configured access token is fresh enough to send. */
  ensureFresh(): Promise<void>;
  /** After a 401 on `usedToken`: rotate once if that token is still current. True means retry. */
  refreshAfter401(usedToken: string | null): Promise<boolean>;
}

let refresher: AccessRefresher | null = null;

export function setAccessRefresher(next: AccessRefresher | null): void {
  refresher = next;
}

const REPLAYABLE = new Set(["GET", "HEAD"]);

interface Prepared {
  url: string;
  requestInit: RequestInit;
  usedToken: string | null;
  explicitCredential: boolean;
}

function prepare(path: string, init: RequestInit | undefined, options: ApiOptions | undefined, json: boolean): Prepared {
  const cfg = getServerConfig();
  const headers = new Headers(init?.headers);
  if (json && !(init?.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const requestInit: RequestInit = { ...init, headers };
  const explicitCredential = options?.bearer !== undefined;
  let usedToken: string | null = null;
  if (explicitCredential) {
    // The caller chose the credential; never mix in the configured one or a cookie.
    if (options?.bearer) {
      headers.set("Authorization", `Bearer ${options.bearer}`);
      usedToken = options.bearer;
    }
  } else if (cfg.mode === "token") {
    // Token mode never falls back to cookies. Branching on the token's
    // presence instead would make a client that has not authenticated yet
    // send a credentialed cross-origin request, which is precisely what the
    // device-token flow exists to avoid; with no token the request should
    // reach the server bare and come back a clean 401.
    if (cfg.accessToken) {
      headers.set("Authorization", `Bearer ${cfg.accessToken}`);
      usedToken = cfg.accessToken;
    }
  } else {
    requestInit.credentials = "include";
  }
  const base = options?.baseUrl !== undefined ? normalizeBaseUrl(options.baseUrl) : apiBase();
  return { url: `${base}${path}`, requestInit, usedToken, explicitCredential };
}

function managesToken(options: ApiOptions | undefined): boolean {
  return getServerConfig().mode === "token" && options?.bearer === undefined && !options?.skipRefresh && refresher !== null;
}

const UNREACHABLE = "Cannot reach this Telos node. Check the server address and try again.";

/**
 * Fetches a node resource as bytes, with whatever credential this client holds.
 *
 * Downloads used to be a `window.open` on the resource URL, which works only
 * because a browser attaches the session cookie to the navigation it starts. A
 * token-mode client has no cookie and cannot put a header on a navigation, so
 * that same open lands on a bare 401 -- and, opened in a native webview, on a
 * 401 page with no way back. Pulling the bytes through the authenticated client
 * is the only form that works in both modes.
 */
export async function apiBlob(path: string, options?: ApiOptions): Promise<Blob> {
  for (let attempt = 0; ; attempt++) {
    if (managesToken(options)) await refresher!.ensureFresh();
    const { url, requestInit, usedToken } = prepare(path, undefined, options, false);
    let res: Response;
    try {
      res = await fetch(url, requestInit);
    } catch {
      throw new ApiError(0, UNREACHABLE);
    }
    if (res.status === 401 && attempt === 0 && managesToken(options) && (await refresher!.refreshAfter401(usedToken))) {
      continue;
    }
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new ApiError(res.status, text.trim() || res.statusText);
    }
    return await res.blob();
  }
}

export async function api<T>(path: string, init?: RequestInit, options?: ApiOptions): Promise<T> {
  let res: Response | undefined;
  for (let attempt = 0; attempt < 2; attempt++) {
    // Refresh BEFORE dispatch rather than replaying afterwards: a POST that
    // reached the node and got a 401 has not changed anything, but a POST that
    // got no answer might have, and re-sending it blindly is how a message gets
    // posted twice. Only idempotent reads are retried after a 401.
    if (managesToken(options)) await refresher!.ensureFresh();
    const { url, requestInit, usedToken } = prepare(path, init, options, true);
    try {
      res = await fetch(url, requestInit);
    } catch {
      if (init?.signal?.aborted) {
        throw new ApiError(408, "Request timed out.");
      }
      throw new ApiError(0, UNREACHABLE);
    }
    const method = (init?.method ?? "GET").toUpperCase();
    if (
      res.status === 401 &&
      attempt === 0 &&
      REPLAYABLE.has(method) &&
      managesToken(options) &&
      (await refresher!.refreshAfter401(usedToken))
    ) {
      continue;
    }
    break;
  }
  if (!res) throw new ApiError(0, UNREACHABLE);

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
