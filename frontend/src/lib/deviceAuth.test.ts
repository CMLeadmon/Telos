import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./api";

vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: vi.fn() };
});
vi.mock("./secureStorage", () => ({
  getSecret: vi.fn(),
  setSecret: vi.fn(),
  deleteSecret: vi.fn(),
}));

import { api } from "./api";
import { deleteSecret, getSecret, setSecret } from "./secureStorage";
import {
  REFRESH_TOKEN_KEY,
  acquireWSTicket,
  refreshAccessToken,
  registerDevice,
  socketProtocols,
} from "./deviceAuth";
import { getServerConfig, setServerConfig } from "./serverConfig";

describe("deviceAuth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "cookie", accessToken: null });
  });

  it("stores the refresh token and switches to token mode on registration", async () => {
    vi.mocked(api).mockResolvedValue({
      deviceId: "d1", refreshToken: "r1", accessToken: "a1", accessExpiresIn: 900,
    });

    const reg = await registerDevice("Carter's laptop", "linux", "1.0.0", {
      username: "carter", password: "hunter2",
    });

    expect(reg.deviceId).toBe("d1");
    expect(setSecret).toHaveBeenCalledWith(REFRESH_TOKEN_KEY, "r1");
    expect(getServerConfig().mode).toBe("token");
    expect(getServerConfig().accessToken).toBe("a1");
  });

  // The credentials are the whole point of the call: a client loaded from disk
  // has no cookie, so if they are not in the body the node has nothing to
  // authenticate and registration can never succeed.
  it("sends the password credentials the node registers against", async () => {
    vi.mocked(api).mockResolvedValue({
      deviceId: "d1", refreshToken: "r1", accessToken: "a1", accessExpiresIn: 900,
    });

    await registerDevice("Desktop", "windows", "0.1.0", {
      username: "carter", password: "hunter2",
    });

    const [path, init] = vi.mocked(api).mock.calls[0];
    expect(path).toBe("/api/v1/auth/devices/register");
    expect(JSON.parse(String(init?.body))).toEqual({
      deviceName: "Desktop",
      platform: "windows",
      clientVersion: "0.1.0",
      username: "carter",
      password: "hunter2",
    });
  });

  it("discards the stored credential when refresh is rejected", async () => {
    vi.mocked(getSecret).mockResolvedValue("stale-token");
    vi.mocked(api).mockRejectedValue(new ApiError(401, "revoked"));

    await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    // Refresh tokens are single-use; keeping a rejected one loops the client forever.
    expect(deleteSecret).toHaveBeenCalledWith(REFRESH_TOKEN_KEY);
  });

  // An unreachable node says nothing about the credential. Discarding it here
  // would turn a passing outage into a forced re-registration.
  it("keeps the credential when the node is merely unreachable", async () => {
    vi.mocked(getSecret).mockResolvedValue("good-token");
    vi.mocked(api).mockRejectedValue(new ApiError(0, "offline"));

    await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    expect(deleteSecret).not.toHaveBeenCalled();
  });

  it("refuses to refresh when no credential is stored", async () => {
    vi.mocked(getSecret).mockResolvedValue(null);
    await expect(refreshAccessToken()).rejects.toBeInstanceOf(ApiError);
    expect(api).not.toHaveBeenCalled();
  });

  // The server treats a second presentation of one refresh token as a leak and
  // revokes the device. An expiring request and a reconnecting socket refreshing
  // together is ordinary, so without sharing the rotation the client would
  // regularly revoke itself.
  it("shares one rotation between concurrent callers", async () => {
    vi.mocked(getSecret).mockResolvedValue("r1");
    vi.mocked(api).mockImplementation(
      () =>
        new Promise((resolve) =>
          setTimeout(
            () => resolve({ refreshToken: "r2", accessToken: "a2", accessExpiresIn: 900 }),
            5,
          ),
        ),
    );

    const [first, second] = await Promise.all([refreshAccessToken(), refreshAccessToken()]);

    expect(first).toBe("a2");
    expect(second).toBe("a2");
    expect(api).toHaveBeenCalledTimes(1);
  });

  // Sharing must not outlive the request, or the client would keep handing back
  // one dead access token instead of rotating again.
  it("rotates again after the shared attempt settles", async () => {
    vi.mocked(getSecret).mockResolvedValue("r1");
    vi.mocked(api)
      .mockResolvedValueOnce({ refreshToken: "r2", accessToken: "a2", accessExpiresIn: 900 })
      .mockResolvedValueOnce({ refreshToken: "r3", accessToken: "a3", accessExpiresIn: 900 });

    expect(await refreshAccessToken()).toBe("a2");
    expect(await refreshAccessToken()).toBe("a3");
    expect(api).toHaveBeenCalledTimes(2);
  });
});

describe("socketProtocols", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // In cookie mode the browser attaches the session cookie to the handshake.
  // Asking for a ticket there would spend a round trip on nothing.
  it("offers no subprotocol in cookie mode", async () => {
    setServerConfig({ baseUrl: "", mode: "cookie", accessToken: null });
    expect(await socketProtocols()).toEqual([]);
    expect(api).not.toHaveBeenCalled();
  });

  it("spends a fresh ticket per attempt in token mode", async () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "a1" });
    vi.mocked(api)
      .mockResolvedValueOnce({ ticket: "t1", expiresIn: 30 })
      .mockResolvedValueOnce({ ticket: "t2", expiresIn: 30 });

    expect(await socketProtocols()).toEqual(["telos-ticket.t1"]);
    // Single-use: a second attempt must not replay the first ticket.
    expect(await socketProtocols()).toEqual(["telos-ticket.t2"]);
    expect(vi.mocked(api).mock.calls[0][0]).toBe("/api/v1/auth/ws-ticket");
    expect(vi.mocked(api).mock.calls[0][1]?.method).toBe("POST");
  });

  it("acquires a ticket without a body", async () => {
    setServerConfig({ baseUrl: "https://telos.example.com", mode: "token", accessToken: "a1" });
    vi.mocked(api).mockResolvedValue({ ticket: "t1", expiresIn: 30 });
    expect(await acquireWSTicket()).toBe("t1");
  });
});
