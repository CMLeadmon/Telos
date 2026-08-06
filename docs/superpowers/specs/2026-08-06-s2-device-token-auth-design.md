# S2 — Device-Token Auth — Design

This sub-project delivers a dual-credential authentication architecture for the Telos core gateway (`backend/`). Browser sessions retain exact-origin HttpOnly `SameSite=Strict` cookies, while native desktop/mobile clients authenticate via OAuth-style bearer tokens and single-use WebSocket tickets. This unblocks cross-origin native client connections (S3, S4, S5) without compromising the web browser security posture or exposing backend proxy handlers to header leaks.

## 1. Purpose

### 1.1 Decisions

| Decision | Choice |
|---|---|
| Migration file | New migration `backend/db/migrations/0023_device_tokens.sql`. New numbered file, idempotent (`IF NOT EXISTS` / `ON CONFLICT`). Never edit an applied migration. |
| Credential model | **Two credential classes, not one loosened class.** Cookie sessions remain `SameSite=Strict`, HttpOnly, exact-origin-checked, and unchanged for browsers. Device tokens are separate. |
| Invariant comment | The cookie-only invariant comment in `backend/main.go` is **updated in place** to record that it was rescoped deliberately, not eroded. |
| Token lifecycle | Token model: registration → long-lived refresh token → short-lived bearer access token. Refresh tokens are stored **hashed**, never plaintext, and rotate on use. |
| Authentication middleware | `getAuthenticatedUser` grows a bearer branch. `withAuth` needs no change — it consumes `*UserContext`. |
| WebSocket authentication | **WebSocket auth uses a short-lived single-use ticket presented via `Sec-WebSocket-Protocol`.** Not a query parameter — a query param would leak into access logs and would break the never-relay-to-upstream property that `sessionTokenFromRequest` in `backend/proxy.go` protects. |
| Revocation | Per-device revocation surfaced in Settings, and revocation must take effect on live connections using the existing revocation machinery in `backend/realtime.go`. |
| Rate limiting & audit | Rate limiting and audit logging on registration. |
| CORS & CSP policy | Production CORS stays **off by default**, promoted from the existing dev-gated machinery behind an explicit `TELOS_ALLOWED_ORIGINS`. CSP `connect-src` widens only when that variable is set. |

### 1.2 Success criteria

1. Browser sessions continue to authenticate via `SameSite=Strict` HttpOnly cookies and undergo exact-origin validation on every non-GET request with zero regression.
2. Native apps can register a device, receive a refresh token and access token pair, refresh access tokens upon expiry, and execute all API operations via `Authorization: Bearer <token>`.
3. WebSocket upgrades from native clients succeed using single-use tickets passed through `Sec-WebSocket-Protocol: telos-ticket.<ticket>`, without placing sensitive tokens in URL query strings or proxy headers.
4. Revoking a device from the Settings UI immediately invalidates its refresh token and terminates all active WebSocket connections belonging to that device.
5. Production deployments block cross-origin requests by default, permitting CORS and widening CSP `connect-src` only when `TELOS_ALLOWED_ORIGINS` is explicitly configured.

## 2. Verified current state

The following table documents the exact code locations in the running codebase that anchor the authentication and security model:

| Surface | Location | Verified current behavior |
|---|---|---|
| Login cookie creation | `backend/main.go:1031-1039` | Sets `telos_session` cookie with `HttpOnly: true`, `SameSite: Strict`, `Path: "/"`. |
| Logout cookie clearing | `backend/main.go:1066-1075` | Clears `telos_session` cookie with `MaxAge: -1` and `Expires: 1970-01-01`. |
| Exact-origin enforcement | `backend/security.go:170-192` | `requireTrustedOrigin` rejects state-changing requests (non-GET/HEAD/OPTIONS) failing origin checks with `403 origin_forbidden`. |
| Upgrader origin check | `backend/main.go:56-59` | `upgrader.CheckOrigin` validates `r.Header.Get("Origin")` against `originAllowed(securityConfig, ...)`. |
| Dev-gated CORS middleware | `backend/security.go:240-258` | `devCORS` emits `Access-Control-Allow-Origin` and `Credentials` only when `TELOS_ENV == "development"`. |
| Response CSP header | `backend/security.go:212-236` | `securityHeaders` constructs `Content-Security-Policy` with `connect-src 'self'`. |
| Cookie invariant comment | `backend/main.go:685-686` | Asserts session tokens are never accepted from query string, fragment, or header. |
| User authentication | `backend/main.go:680-706` | `getAuthenticatedUser` extracts token via `sessionTokenFromRequest` and hashes with SHA-256. |
| Auth wrapper middleware | `backend/main.go:803-839` | `withAuth` invokes `getAuthenticatedUser` and populates `r.Context()` with `*UserContext`. |
| Upstream proxy token isolation | `backend/proxy.go:17-35` | `sessionTokenFromRequest` strictly inspects `r.CookiesNamed("telos_session")` and enforces hex structure. |
| Chat WebSocket upgrade | `backend/main.go:1277` | Calls `upgrader.Upgrade(w, r, nil)` for `/api/v1/chat/ws`. |
| Events WebSocket upgrade | `backend/event_handlers.go:64` | Calls `upgrader.Upgrade(w, r, nil)` for `/api/v1/events/ws`. |
| Highest database migration | `backend/db/migrations/0022_catalog_identity_and_progress.sql` | Currently highest applied migration file; next is `0023_device_tokens.sql`. |
| Settings revocation UI | `frontend/src/components/settings/SecuritySection.tsx`, `frontend/src/components/settings/AdminUsersSection.tsx` | Hosts user session management and admin user management interfaces. |

## 3. Architecture

### 3.1 Boundaries

S2 modifies the backend authentication layer, security policy, database schema, and Settings management components. It must comply with the following boundaries:
- **May touch:** `backend/db/migrations/0023_device_tokens.sql`, `backend/auth.go` (new auth handlers), `backend/main.go` (`getAuthenticatedUser` and route table), `backend/security.go` (`devCORS` and `securityHeaders`), `backend/proxy.go` (token validation), `backend/realtime.go` (device socket tracking), `frontend/src/components/settings/SecuritySection.tsx`, `frontend/src/components/settings/AdminUsersSection.tsx`.
- **May not touch:** Existing migration files `0001` through `0022` (immutable), `SameSite=Strict` cookie settings for web browser logins, or handler authorization logic inside `withAuth`.

### 3.2 Token Model and Lifecycle

Device authentication operates via dual tokens alongside traditional cookies:

```
[ Native Client ]  --( POST /api/v1/auth/devices/register )--> [ Gateway ]
                   <--( Refresh Token + Access Token )-------

[ Native Client ]  --( Bearer <Access Token> in Header )-----> [ Gateway (withAuth) ]

[ Native Client ]  --( POST /api/v1/auth/ws-ticket )---------> [ Gateway ]
                   <--( Single-Use Ticket (30s) )-------------

[ Native Client ]  --( WS: Sec-WebSocket-Protocol: telos-ticket.<ticket> )--> [ Gateway ]
```

1. **Registration:** `POST /api/v1/auth/devices/register` accepts user credentials (or existing session), `device_name`, `platform`, and `client_version`. On success, it issues a long-lived `refresh_token` (valid 90 days) and a short-lived `access_token` (valid 15 minutes).
2. **Storage:** Device identity and refresh credentials live in **separate tables**. `devices` holds stable identity — one row per client install, unchanged across rotations. `device_refresh_tokens` holds the rotating credential as a SHA-256 hash. Plaintext refresh tokens are never persisted. The split is load-bearing: with the token hash on the device row, every rotation would either mutate identity or add a duplicate device to the member's list, and revoking "a device" would only kill one token generation. See §4.3.
3. **Rotation:** `POST /api/v1/auth/devices/refresh` validates the presented hash against an unconsumed, unexpired `device_refresh_tokens` row, stamps its `consumed_at`, inserts a successor row, sets the old row's `replaced_by` to the successor, and returns a fresh `access_token` / `refresh_token` pair. The `devices` row is untouched apart from `last_seen_at`.
4. **Bearer Authentication:** `getAuthenticatedUser` (`backend/main.go:680-706`) checks for `telos_session` cookie first. If absent, it reads the `Authorization: Bearer <token>` header. Access tokens are cryptographically validated or matched against an active in-memory cache/database token table.
5. **WebSocket Authentication:** Browser clients continue sending cookies on WebSocket upgrade. Native clients call `POST /api/v1/auth/ws-ticket` using their access token to get a 30-second single-use ticket string. During WebSocket upgrade, the client sends `Sec-WebSocket-Protocol: telos-ticket.<ticket_id>`. The upgrader validates the ticket, consumes it immediately, strips the subprotocol header from the response, and binds the socket to the associated `user_id` and `device_id`. This prevents token leakage into proxy logs or upstream service headers (`backend/proxy.go:17-35`).

### 3.3 Revocation & Realtime Enforcement

Device revocation is supported for both end-users and administrators:
- **User Revocation:** Surfaced in `frontend/src/components/settings/SecuritySection.tsx` under a "Registered Devices" list alongside active browser sessions.
- **Admin Revocation:** Surfaced in `frontend/src/components/settings/AdminUsersSection.tsx` allowing operators to invalidate all devices for any user account.
- **Live Disconnection:** When a device is revoked via `DELETE /api/v1/users/me/devices/{id}`, its database record sets `revoked_at = NOW()`. The handler immediately signals `realtime.go` (via `revokeDevice(deviceID)`), which identifies active WebSocket connection instances associated with that `device_id` and closes them with close code `4001 (Device Revoked)`.

### 3.4 Configurable CORS and CSP Policy

Production deployments promote CORS from dev-only to configurable opt-in:
- **Environment Variable:** `TELOS_ALLOWED_ORIGINS` (comma-separated list of origins, e.g., `https://client.example.com,tauri://localhost`).
- **CORS Enforcement:** `security.go:240-258` (`backend/security.go:240-258`) is updated to inspect `TELOS_ALLOWED_ORIGINS` regardless of `TELOS_ENV`. If the incoming `Origin` header matches an allowed origin, it emits `Access-Control-Allow-Origin: <origin>`, `Access-Control-Allow-Credentials: true`, and handles preflight `OPTIONS` requests.
- **CSP Adjustment:** `securityHeaders` (`backend/security.go:212-236`) parses `TELOS_ALLOWED_ORIGINS` and appends all explicitly permitted web socket / HTTP origins to `connect-src`.

## 4. Contract changes

### 4.1 New API Routes

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/v1/auth/devices/register` | Session **or** username/password | Registers a new native client device, issuing refresh/access tokens. Never unauthenticated — an open registration endpoint would mint credentials for anyone. |
| `POST` | `/api/v1/auth/devices/refresh` | Refresh token in body | Swaps a valid refresh token for a new token pair (rotates refresh token) |
| `POST` | `/api/v1/auth/devices/revoke` | Bearer / Cookie | Revokes current device refresh token |
| `GET` | `/api/v1/users/me/devices` | Bearer / Cookie | Lists all active registered devices for current user |
| `DELETE` | `/api/v1/users/me/devices/{id}` | Bearer / Cookie | Revokes a specific registered device by ID |
| `POST` | `/api/v1/auth/ws-ticket` | Bearer / Cookie | Generates a 30-second single-use ticket for WebSocket upgrade |

### 4.2 Environment Variables

- `TELOS_ALLOWED_ORIGINS`: Comma-separated allowed origin URLs for CORS and CSP `connect-src` in non-development environments. Default: empty (CORS disabled).

### 4.3 Database Schema (`backend/db/migrations/0023_device_tokens.sql`)

```sql
-- Stable device identity. Exactly one row per registered client install,
-- unchanged across token rotations.
CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name VARCHAR(128) NOT NULL,
    platform VARCHAR(64) NOT NULL,
    client_version VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

-- Rotating refresh credentials. Many rows per device over its lifetime.
-- replaced_by threads each rotation to its successor, which is what makes
-- reuse detection possible: presenting a token that already has a successor
-- means the credential leaked.
CREATE TABLE IF NOT EXISTS device_refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    replaced_by UUID REFERENCES device_refresh_tokens(id)
);

CREATE INDEX IF NOT EXISTS idx_devices_user_id ON devices(user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_device_refresh_tokens_device ON device_refresh_tokens(device_id);
```

## 5. Implementation surface

| File Path | Change | Rationale |
|---|---|---|
| `backend/db/migrations/0023_device_tokens.sql` | Create migration | Store registered native devices and hashed refresh tokens idempotently. |
| `backend/main.go` | Update `getAuthenticatedUser` (`backend/main.go:680-706`), rescope comment (`backend/main.go:685`), register routes | Accept Bearer access tokens, update cookie-only invariant comment, route new device endpoints. |
| `backend/auth.go` | Add device token handlers & helpers | Implement registration, rotation, revocation, and single-use WS ticket generation. |
| `backend/security.go` | Update `devCORS` (`backend/security.go:240-258`), `securityHeaders` (`backend/security.go:212-236`) | Support `TELOS_ALLOWED_ORIGINS` for production CORS and CSP `connect-src` widening. |
| `backend/event_handlers.go` | Update `handleEventsWS` (`backend/event_handlers.go:64`) | Validate single-use WebSocket ticket from `Sec-WebSocket-Protocol`. |
| `backend/realtime.go` | Add `revokeDevice(deviceID)` helper | Support immediate WebSocket termination upon device revocation. |
| `frontend/src/components/settings/SecuritySection.tsx` | Add Registered Devices UI section | Allow members to view and revoke registered devices. |
| `frontend/src/components/settings/AdminUsersSection.tsx` | Add Admin Device Revocation | Allow admins to inspect and clear device tokens for managed users. |

## 6. Failure handling and observability

- **Expired / Invalid Bearer Token:** Returns `401 Unauthorized` with JSON `{ "code": "token_expired", "message": "Access token expired" }`.
- **Revoked Refresh Token:** Returns `401 Unauthorized`. Presenting a token whose row already carries a `replaced_by` means the credential was captured and replayed — revoke the entire `devices` row, cascade-invalidate every `device_refresh_tokens` row under it, terminate its live sockets, and emit a `device_token_reuse_detected` audit event. Fail closed: a legitimate client that lost a rotation response re-registers rather than being silently re-admitted.
- **WS Ticket Reuse / Expiry:** If a ticket is expired (>30s) or already consumed, WebSocket upgrade returns `401 Unauthorized` and handshake fails.
- **Audit Logging:** Every device registration, token refresh failure, and device revocation emits structured audit events (`device_registered`, `device_refreshed`, `device_revoked`) via standard logging.
- **Rate Limiting:** Registration endpoint (`/api/v1/auth/devices/register`) is rate-limited per IP (10 requests per minute) to prevent brute-force token generation.

## 7. Security and privacy

- **No Plaintext Refresh Tokens:** Refresh tokens are generated with 256 bits of cryptographically secure entropy (`crypto/rand`) and stored strictly as SHA-256 hashes.
- **Token Rotation:** Refresh tokens are single-use; each refresh exchange invalidates the old token hash and issues a new pair.
- **Proxy Leakage Protection:** Single-use WS tickets passed via `Sec-WebSocket-Protocol` prevent auth tokens from appearing in query parameters (which log to server access logs) or HTTP request headers forwarded upstream by `proxy.go` (`backend/proxy.go:17-35`).
- **CORS Containment:** Production CORS remains off by default and requires explicit origin allowlisting via `TELOS_ALLOWED_ORIGINS`.

## 8. Verification

Run the unit and integration verification suites matching touched components:

```bash
# Backend unit & vet suite
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...

# Backend integration suite focusing on auth and security
bash scripts/test-backend.sh auth
bash scripts/test-backend.sh security

# Frontend verification
cd frontend && npm run lint && npx tsc --noEmit && npm run test:unit
```

## 9. Documentation changes required with implementation

- Update `documentation/architecture/03-gateway-and-api.md` to document device token authentication endpoints, bearer token usage, single-use WebSocket ticket protocol, and `TELOS_ALLOWED_ORIGINS`.
- Update `CLAUDE.md` to list migration `0023_device_tokens.sql` under database schema history.

## 10. Deferred / out of scope

- OAuth2 / OIDC server capabilities (Telos issues its own device tokens directly; third-party identity provider federation is deferred).
- Granular per-device permission scopes (device tokens inherit full user permissions).
