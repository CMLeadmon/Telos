// Single-origin gateway client. In production the static export is served by
// the Go gateway itself, so all paths are relative. In `next dev` (port 3000)
// the gateway runs separately on :8080.

export function apiBase(): string {
  if (typeof window !== "undefined" && window.location.port === "3000") {
    return `${window.location.protocol}//${window.location.hostname}:8080`;
  }
  return "";
}

export function wsBase(): string {
  if (typeof window === "undefined") return "";
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  if (window.location.port === "3000") {
    return `${proto}//${window.location.hostname}:8080`;
  }
  return `${proto}//${window.location.host}`;
}

export function livekitUrl(): string {
  const configured = process.env.NEXT_PUBLIC_LIVEKIT_URL;
  if (configured) return configured;
  if (typeof window !== "undefined" && window.location.port === "3000") {
    return `ws://${window.location.hostname}:7880`;
  }
  if (typeof window !== "undefined") {
    return `wss://${window.location.host}/livekit`;
  }
  return "";
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

// Session cookie is HttpOnly; credentials must ride along in dev where the
// gateway is a different origin.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...init?.headers },
    ...init,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new ApiError(res.status, text.trim() || res.statusText);
  }
  return res.json() as Promise<T>;
}
