// An in-memory Telos node for lifecycle tests: it implements just enough of the
// device-token API to make rotation, replay detection, revocation and logout
// behave like the real server, so tests assert outcomes rather than call shapes.

import { vi } from "vitest";

export const uuid = (n: number) => `00000000-0000-4000-8000-${n.toString(16).padStart(12, "0")}`;

interface Device {
  id: string;
  userId: string;
  refresh: string;
  revoked: boolean;
}

export interface NodeCall {
  method: string;
  path: string;
  bearer: string | null;
  body: unknown;
  credentials: RequestCredentials | undefined;
}

export interface FakeNode {
  origin: string;
  calls: NodeCall[];
  devices: Map<string, Device>;
  /** Make the next request to this path suffix fail: an HTTP status, or "network". */
  failNext: (pathSuffix: string, outcome: number | "network") => void;
  /** Hold the next response to this path suffix until the returned function is called. */
  hold: (pathSuffix: string) => () => void;
  count: (pathSuffix: string) => number;
  /** Expire every access token (the node stops accepting them) without revoking devices. */
  expireAccessTokens: () => void;
  user: { ID: string; Username: string };
  password: string;
  install: () => ReturnType<typeof vi.spyOn>;
}

export function createFakeNode(origin = "https://node.example.com"): FakeNode {
  let counter = 100;
  const next = (prefix: string) => `${prefix}-${++counter}`;
  const devices = new Map<string, Device>();
  const accessTokens = new Map<string, string>(); // access token -> device id
  const failures: Array<{ suffix: string; outcome: number | "network" }> = [];
  const holds: Array<{ suffix: string; release: Promise<void> }> = [];
  const calls: NodeCall[] = [];
  const user = { ID: uuid(1), Username: "carter" };
  const password = "correct horse battery staple";

  const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  const consumed = new Map<string, string>(); // rotated-away refresh token -> device id
  const issue = (device: Device) => {
    if (device.refresh) consumed.set(device.refresh, device.id);
    device.refresh = next("refresh");
    const access = next("access");
    accessTokens.set(access, device.id);
    return { refreshToken: device.refresh, accessToken: access, accessExpiresIn: 900 };
  };
  const authed = (bearer: string | null): Device | null => {
    const id = bearer ? accessTokens.get(bearer) : undefined;
    const device = id ? devices.get(id) : undefined;
    return device && !device.revoked ? device : null;
  };

  async function handle(input: unknown, init?: RequestInit): Promise<Response> {
    const url = new URL(String(input));
    const path = url.pathname;
    const headers = new Headers(init?.headers);
    const auth = headers.get("Authorization");
    const bearer = auth?.startsWith("Bearer ") ? auth.slice(7) : null;
    const method = (init?.method ?? "GET").toUpperCase();
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ method, path, bearer, body, credentials: init?.credentials });

    const failIndex = failures.findIndex((f) => path.endsWith(f.suffix));
    if (failIndex >= 0) {
      const [failure] = failures.splice(failIndex, 1);
      if (failure.outcome === "network") throw new TypeError("network down");
      return json(failure.outcome, { error: { code: "injected", message: "injected failure" } });
    }
    const holdIndex = holds.findIndex((h) => path.endsWith(h.suffix));
    if (holdIndex >= 0) {
      const [held] = holds.splice(holdIndex, 1);
      await held.release;
    }

    if (url.origin !== origin) throw new TypeError(`unexpected origin ${url.origin}`);

    if (path === "/api/v1/auth/devices/register" && method === "POST") {
      if (body.username !== user.Username || body.password !== password) {
        return json(401, { error: { code: "invalid_credentials", message: "Invalid username or password." } });
      }
      const device: Device = { id: uuid(500 + devices.size), userId: user.ID, refresh: "", revoked: false };
      devices.set(device.id, device);
      return json(200, { deviceId: device.id, ...issue(device) });
    }
    if (path === "/api/v1/auth/devices/refresh" && method === "POST") {
      const device = [...devices.values()].find((d) => d.refresh === body.refreshToken);
      if (!device || device.revoked) {
        // A replayed (already rotated) token revokes the whole device, as the real node does.
        const owner = devices.get(consumed.get(body.refreshToken) ?? "");
        if (owner) owner.revoked = true;
        return json(401, { error: { code: "invalid_token", message: "invalid token" } });
      }
      return json(200, issue(device));
    }
    if (path === "/api/v1/auth/me" && method === "GET") {
      return authed(bearer) ? json(200, { ...user, Roles: ["Member"], Permissions: [], DisplayName: "Carter", HasAvatar: false }) : json(401, { error: { code: "unauthorized", message: "Authentication required" } });
    }
    if (path === "/api/v1/auth/logout" && method === "POST") {
      const device = authed(bearer);
      if (!device) return json(401, { error: { code: "unauthorized", message: "Authentication required" } });
      device.revoked = true;
      return json(200, { status: "success" });
    }
    if (path === "/api/v1/auth/ws-ticket" && method === "POST") {
      return authed(bearer) ? json(200, { ticket: next("ticket"), expiresIn: 30 }) : json(401, { error: { code: "unauthorized", message: "Authentication required" } });
    }
    const revoke = /^\/api\/v1\/users\/me\/devices\/([^/]+)$/.exec(path);
    if (revoke && method === "DELETE") {
      const caller = authed(bearer);
      const target = devices.get(decodeURIComponent(revoke[1]));
      if (!caller || !target) return json(401, { error: { code: "unauthorized", message: "Authentication required" } });
      target.revoked = true;
      return json(200, { status: "revoked" });
    }
    if (path === "/api/v1/probe" && method === "GET") {
      return authed(bearer) ? json(200, { ok: true }) : json(401, { error: { code: "unauthorized", message: "Authentication required" } });
    }
    if (path === "/api/v1/probe" && method === "POST") {
      return authed(bearer) ? json(200, { ok: true }) : json(401, { error: { code: "unauthorized", message: "Authentication required" } });
    }
    if (path === "/api/v1/forbidden") return json(403, { error: { code: "forbidden", message: "forbidden" } });
    return json(404, { error: { code: "not_found", message: "not found" } });
  }

  return {
    origin,
    calls,
    devices,
    user,
    password,
    failNext: (suffix, outcome) => void failures.push({ suffix, outcome }),
    hold: (suffix) => {
      let release!: () => void;
      holds.push({ suffix, release: new Promise<void>((resolve) => (release = resolve)) });
      return () => release();
    },
    count: (suffix) => calls.filter((c) => c.path.endsWith(suffix)).length,
    expireAccessTokens: () => accessTokens.clear(),
    install: () => vi.spyOn(globalThis, "fetch").mockImplementation(handle as typeof fetch),
  };
}

export interface FakeKeychain {
  entries: Map<string, string>;
  failOn: Set<"get" | "set" | "delete">;
  calls: string[];
  install: () => void;
  uninstall: () => void;
}

/** A controllable stand-in for the shell's credential bridge. */
export function createFakeKeychain(): FakeKeychain {
  const entries = new Map<string, string>();
  const failOn = new Set<"get" | "set" | "delete">();
  const calls: string[] = [];
  const slot = (key: { origin: string; accountId: string; deviceId: string }) => JSON.stringify([key.origin, key.accountId, key.deviceId]);
  const guard = (op: "get" | "set" | "delete") => {
    calls.push(op);
    if (failOn.has(op)) return Promise.reject("credential_store_locked: the operating-system credential service is locked");
    return null;
  };
  const bridge = {
    get: async (key: { origin: string; accountId: string; deviceId: string }) => {
      const failed = guard("get");
      if (failed) return failed as never;
      return entries.get(slot(key)) ?? null;
    },
    set: async (key: { origin: string; accountId: string; deviceId: string }, secret: string) => {
      const failed = guard("set");
      if (failed) return failed as never;
      entries.set(slot(key), secret);
    },
    delete: async (key: { origin: string; accountId: string; deviceId: string }) => {
      const failed = guard("delete");
      if (failed) return failed as never;
      entries.delete(slot(key));
    },
  };
  const w = window as unknown as { __TELOS_NATIVE_CREDENTIALS__?: unknown; __TELOS_NATIVE_SHELL__?: unknown };
  return {
    entries,
    failOn,
    calls,
    install: () => {
      w.__TELOS_NATIVE_SHELL__ = {};
      w.__TELOS_NATIVE_CREDENTIALS__ = bridge;
    },
    uninstall: () => {
      delete w.__TELOS_NATIVE_CREDENTIALS__;
      delete w.__TELOS_NATIVE_SHELL__;
    },
  };
}
