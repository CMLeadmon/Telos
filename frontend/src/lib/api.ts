// Single-origin gateway client. In production the static export is served by
// the Go gateway. The development server proxies these same paths to the local
// gateway, so browsers never need direct access to an additional port.

export function apiBase(): string {
  return "";
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

export function wsBase(): string {
  if (typeof window === "undefined") return "";
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}`;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

// The session cookie is HttpOnly and all browser requests stay same-origin.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    const headers = new Headers(init?.headers);
    if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
    res = await fetch(`${apiBase()}${path}`, {
      ...init,
      credentials: "include",
      headers,
    });
  } catch {
    if (init?.signal?.aborted) {
      throw new ApiError(408, "Request timed out.");
    }
    throw new ApiError(
      0,
      "Cannot reach this Telos node. Check the server address and try again.",
    );
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
