import { api, ApiError } from "./api";
import { getServerConfig, setServerConfig } from "./serverConfig";
import { deleteSecret, getSecret, setSecret } from "./secureStorage";

export const REFRESH_TOKEN_KEY = "telos.refreshToken";

export interface DeviceRegistration {
  deviceId: string;
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

interface TokenPair {
  refreshToken: string;
  accessToken: string;
  accessExpiresIn: number;
}

/** Installs a freshly issued pair: refresh token to the keychain, access token to config. */
async function adoptTokens(pair: TokenPair): Promise<void> {
  await setSecret(REFRESH_TOKEN_KEY, pair.refreshToken);
  setServerConfig({ ...getServerConfig(), mode: "token", accessToken: pair.accessToken });
}

/**
 * Registers this install with the configured node and adopts the issued tokens.
 *
 * The credentials go in the body rather than a session cookie because a client
 * loaded from disk has no cookie to present: the session cookie is HttpOnly and
 * SameSite=Strict, so it can be neither read nor attached to a cross-site
 * request. Registration is the exchange that turns a password into a device
 * credential, and it is the only call in the client that carries a password.
 */
export async function registerDevice(
  deviceName: string,
  platform: string,
  clientVersion: string,
  credentials: { username: string; password: string },
): Promise<DeviceRegistration> {
  const reg = await api<DeviceRegistration>("/api/v1/auth/devices/register", {
    method: "POST",
    body: JSON.stringify({
      deviceName,
      platform,
      clientVersion,
      username: credentials.username,
      password: credentials.password,
    }),
  });
  await adoptTokens(reg);
  return reg;
}

// Rotation is single-use and the server treats a second presentation of the same
// refresh token as a leak — it revokes the whole device. Two callers refreshing
// at once (an expiring request and a reconnecting socket is the ordinary case)
// would present the same stored token and lock the member out of their own node.
// One in-flight rotation is shared by every caller that asks while it runs.
let inFlightRefresh: Promise<string> | null = null;

/** Rotates the stored refresh token and installs a fresh access token. */
export function refreshAccessToken(): Promise<string> {
  if (!inFlightRefresh) {
    inFlightRefresh = rotate().finally(() => {
      inFlightRefresh = null;
    });
  }
  return inFlightRefresh;
}

async function rotate(): Promise<string> {
  const refreshToken = await getSecret(REFRESH_TOKEN_KEY);
  if (!refreshToken) {
    throw new ApiError(401, "No device credential is stored for this server.");
  }

  let next: TokenPair;
  try {
    next = await api<TokenPair>("/api/v1/auth/devices/refresh", {
      method: "POST",
      body: JSON.stringify({ refreshToken }),
    });
  } catch (err) {
    // A rejected refresh means the credential is dead or was replayed, and
    // retaining it would loop the client through failing refreshes forever.
    // Only on rejection: an unreachable node or a 5xx says nothing about the
    // credential, and discarding it there would turn a passing outage into a
    // re-registration.
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      await deleteSecret(REFRESH_TOKEN_KEY);
      // The access token it would have replaced is no better than the refresh
      // token that was just rejected; keeping it only sends a dead credential.
      setServerConfig({ ...getServerConfig(), accessToken: null });
    }
    throw err;
  }

  await adoptTokens(next);
  return next.accessToken;
}

/** Obtains a short-lived single-use ticket for a WebSocket upgrade. */
export async function acquireWSTicket(): Promise<string> {
  const { ticket } = await api<{ ticket: string; expiresIn: number }>(
    "/api/v1/auth/ws-ticket",
    { method: "POST" },
  );
  return ticket;
}

/**
 * Subprotocol list for a socket upgrade. Empty in cookie mode, where the browser
 * attaches the session cookie to the handshake and a ticket would be redundant.
 * A ticket is single-use, so this is called once per connection attempt.
 */
export async function socketProtocols(): Promise<string[]> {
  if (getServerConfig().mode !== "token") return [];
  return [`telos-ticket.${await acquireWSTicket()}`];
}
