import { isNativeApp } from "./nativeEnv";

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
 * assets from disk, starts out not knowing.
 *
 * This is exactly "is this the native client", which platform.ts also has to
 * answer — and two copies of that test would drift the moment the shell gained
 * or renamed a bridge, leaving one caller treating the same build as native and
 * the other as web. Imported rather than repeated.
 */
export function requiresServerSelection(): boolean {
  return isNativeApp();
}

/** Why a node address was refused. Distinct so each gets its own message. */
export type NodeUrlProblem = "invalid_url" | "https_required" | "credentials" | "not_an_origin";

export class NodeUrlError extends Error {
  readonly problem: NodeUrlProblem;
  constructor(problem: NodeUrlProblem, message: string) {
    super(message);
    this.name = "NodeUrlError";
    this.problem = problem;
  }
}

/**
 * Plain-HTTP loopback nodes exist for development only. The flag is decided at
 * build time (`next build` sets NODE_ENV=production, so a release bundle cannot
 * enable it) and the Rust credential normalizer applies the same rule through
 * `debug_assertions`.
 */
export function debugLoopbackHttpAllowed(): boolean {
  return process.env.NODE_ENV !== "production";
}

const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "[::1]"]);

/**
 * Turns what a member typed into a node origin, or refuses it.
 *
 * Release clients speak platform-trusted HTTPS only: there is no accept-any
 * verifier, no fingerprint override and no plain-HTTP path. A private CA works
 * because the operating system's trust store accepts it -- not because this code
 * waives anything. A bare host gains `https://`. The result is an origin, never
 * a reverse-proxy path prefix.
 */
export function normalizeTrustedNodeUrl(
  raw: string,
  allowLoopbackHttp: boolean = debugLoopbackHttpAllowed(),
): string {
  const trimmed = raw.trim();
  const withScheme = trimmed.includes("://") ? trimmed : `https://${trimmed}`;
  let url: URL;
  try {
    url = new URL(withScheme);
  } catch {
    throw new NodeUrlError("invalid_url", "That is not a valid server address.");
  }
  if (!url.hostname) {
    throw new NodeUrlError("invalid_url", "That is not a valid server address.");
  }
  if (url.username || url.password) {
    throw new NodeUrlError("credentials", "A server address cannot contain a username or password.");
  }
  if (url.search || url.hash || (url.pathname !== "/" && url.pathname !== "")) {
    throw new NodeUrlError("not_an_origin", "Enter the server address only, without a path, query or fragment.");
  }
  if (url.protocol === "http:" && allowLoopbackHttp && LOOPBACK_HOSTS.has(url.hostname)) {
    return url.origin;
  }
  if (url.protocol !== "https:") {
    throw new NodeUrlError("https_required", "This client connects over HTTPS only.");
  }
  return url.origin;
}
