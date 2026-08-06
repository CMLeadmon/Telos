# Connection Layer — Design

The Connection Layer sub-project introduces server URL entry, transport validation, secure credential storage, and network reconnection management to the Telos Next.js frontend client. It enables users to point native desktop, mobile, or remote web client builds at self-hosted Telos nodes, establish transport security using Trust-On-First-Use (TOFU) certificate pinning, maintain socket connectivity across mobile backgrounding cycles, and disconnect cleanly. This sub-project unblocks S5 (Tauri Shell) by providing the multi-server connection state machine required by non-browser native client packages.

## 1. Purpose

### 1.1 Decisions

| Decision | Specification |
|---|---|
| Entry Route | New route for server-URL entry and validation, placed ahead of `/login`. |
| State Management | New Zustand store for connection state (`useConnectionStore.ts`), alongside existing stores. |
| Credential Storage | Tokens live in the OS keychain via the Tauri plugin. Never `localStorage`. The existing `persist`-middleware store is theme state and is explicitly not the pattern to copy here. |
| Certificate Trust | Certificate trust is trust-on-first-use with pinning. There is no "accept any certificate" toggle — self-hosted servers will have self-signed certs, and a blanket-accept switch destroys the transport security everything else assumes. |
| Error States | Declared error states: unreachable, version skew, untrusted certificate, credential revoked. |
| Version Skew | Version skew is a soft warning banner by default; hard-fail only below a backend-declared minimum client version. |
| Reconnection Logic | Implement the online/visibility listening that `frontend/src/lib/reconnectingSocket.ts` documents in its docblock but does not implement — the current implementation is exponential backoff only. Harmless in a browser tab, load-bearing once backgrounded on a phone. |

### 1.2 Success criteria

1. Client users can input a remote Telos node URL (e.g. `https://telos.community.internal`), validate node health, and proceed to authentication without hardcoded API paths.
2. Device tokens and refresh credentials are saved exclusively into the native OS keychain when executing in native shells, and never written to `localStorage` or `sessionStorage`.
3. Transport connections validate TLS certificates using TOFU pinning; fingerprint mismatches trigger an untrusted certificate error screen and block network transmission.
4. Returning to an active app after mobile backgrounding instantly triggers socket reconnection when `online` or `visibilitychange` events fire, eliminating silent socket drops.
5. Soft version skew displays an informational banner allowing continued operation, while incompatible API versions hard-stop access with upgrade instructions.

## 2. Verified current state

- **Reconnecting Socket Gap:** `frontend/src/lib/reconnectingSocket.ts:1-5` docblock asserts: *"Handles exponential backoff (500ms -> 30s) with ±20% jitter, online/visibility listening, and clean lifecycle management."* However, `frontend/src/lib/reconnectingSocket.ts:17-115` implements only exponential backoff timer scheduling in `scheduleReconnect()` at `frontend/src/lib/reconnectingSocket.ts:100-114` (initial `minDelayMs` 500ms at line 34, `maxDelayMs` 30000ms at line 35). It completely lacks `window.addEventListener('online', ...)` and `document.addEventListener('visibilitychange', ...)` event listeners.
- **Existing Store Inventory:** `frontend/src/stores/` contains 10 non-test Zustand stores: `useAnnotationStore.ts`, `useAuthStore.ts`, `useChatSessionStore.ts`, `useFilesStore.ts`, `useLibraryStore.ts`, `useMediaStore.ts`, `useMobileNavStore.ts`, `usePreferencesStore.ts`, `useSettingsStore.ts`, and `useThemeStore.ts`. Only `useThemeStore.ts:2,13` imports and uses the `persist` middleware from `zustand/middleware` for theme preferences. No store manages active server endpoint metadata.
- **Auth and Route Inventory:** Client entry is handled by `frontend/src/app/page.tsx` which routes directly to `frontend/src/app/login/page.tsx` (`MODE_COPY` at line 23). `(shell)` routes share `AppShell.tsx` across five feature endpoints: `/chat` (`frontend/src/app/(shell)/chat/page.tsx`), `/files` (`frontend/src/app/(shell)/files/page.tsx`), `/library` (`frontend/src/app/(shell)/library/page.tsx`), `/settings` (`frontend/src/app/(shell)/settings/page.tsx`), and `/stream` (`frontend/src/app/(shell)/stream/page.tsx`).

## 3. Architecture

### 3.1 Boundaries

- This sub-project modifies frontend connection state, credential persistence mechanisms, socket lifecycle event listeners, and entry routing.
- Backend handler logic remains untouched; server health and version checks consume existing `/api/v1/health` (`backend/main.go:365-376`).
- Web browser builds retain cookie session capabilities while acquiring the connection selection flow when configured for multi-node connection mode.

### 3.2 Connection Lifecycle & State Management

The connection lifecycle transitions through five formal phases managed by a new Zustand store `frontend/src/stores/useConnectionStore.ts`:

```
[ Unconfigured ] ──(Submit URL)──> [ Validating Node ] ──(Health OK & Certificate Valid)──> [ Configured ]
                                            │                                                    │
                                   (Health/TLS Error)                                     (Token Revoked)
                                            ▼                                                    ▼
                                    [ Error State ] <─────────────────────────────────── [ Authentication ]
```

1. **Unconfigured:** Initial state when no active server target is configured. Renders `/connect` entry screen.
2. **Validating Node:** Executes `GET /api/v1/health` against target server URL. Checks min client version and certificate fingerprint.
3. **Configured:** Endpoint validated. Client proceeds to `/login` for user authentication using device tokens.
4. **Authentication:** User exchanges login credentials or refresh token for a bearer token, stored securely.
5. **Error State:** One of `unreachable`, `version_skew_hard`, `untrusted_certificate`, or `credential_revoked`.

### 3.3 Certificate Trust & Pinning Model (TOFU)

Self-hosted Telos instances often run behind custom or self-signed TLS certificates. Rather than disabling SSL/TLS verification:
- On initial connection to a HTTPS endpoint, the client captures the server's TLS leaf certificate SHA-256 fingerprint.
- The user is presented with the certificate subject, issuer, and SHA-256 fingerprint on first connect to confirm trust.
- Upon confirmation, the fingerprint is pinned in secure storage (`tauri-plugin-store` or OS keychain).
- Subsequent TLS connections compare presented certificate fingerprints against the pinned hash. Any mismatch aborts transport setup immediately and enters the `untrusted_certificate` error state.

### 3.4 Socket Lifecycle & Backgrounding Strategy

To resolve mobile socket drops, `ReconnectingSocket` in `frontend/src/lib/reconnectingSocket.ts` is updated with lifecycle listeners:
- `window.addEventListener('online', ...)` immediately triggers `connect()` if disconnected and not explicitly closed.
- `document.addEventListener('visibilitychange', ...)` checks socket readiness when `document.visibilityState === 'visible'`. If the WebSocket is dead or closing, it resets `currentDelay` to `minDelay` and initiates an immediate reconnect attempt.
- When backgrounded (`document.visibilityState === 'hidden'`), background timers are suspended cleanly to avoid battery drain.

## 4. Contract changes

- **New Frontend Routes:**
  - `/connect`: Server URL input, node discovery, certificate verification UI.
- **Client Configuration Surface:**
  - `NEXT_PUBLIC_DEFAULT_SERVER_URL` (optional build-time default server fallback for branded web UI builds).

## 5. Implementation surface

| Path | Change | Rationale |
|---|---|---|
| `frontend/src/stores/useConnectionStore.ts` | Create new Zustand store | Tracks active server URL, connection state, certificate fingerprint, and API compatibility. |
| `frontend/src/lib/secureStorage.ts` | Create secure storage interface | Abstracts Tauri OS keychain plugin in native app mode, with fallback for web browser mode. |
| `frontend/src/lib/reconnectingSocket.ts` | Implement `online` and `visibilitychange` listeners | Fixes gap between docblock claims (`L1-5`) and implementation (`L100-114`). |
| `frontend/src/app/connect/page.tsx` | Create server connection setup route | Renders URL entry, node health verification, TOFU certificate confirmation card. |
| `frontend/src/app/page.tsx` | Add connection state check to root redirect | Redirects unconfigured clients to `/connect` before `/login`. |
| `frontend/src/lib/api.ts` | Bind `apiBase()` and `wsBase()` to `useConnectionStore` | Replaces hardcoded origin with active node URL from connection store. |

## 6. Failure handling and observability

- **Unreachable Node:** Server validation timeout (5000ms) displays inline error with diagnostic suggestions (check IP/DNS, network connection).
- **Version Skew:** Server returns API version header. If `server_min_client_version > current_client_version`, block access with `version_skew_hard`. If `server_version > current_client_version` without breaking minimum requirement, display soft warning banner in shell header.
- **Untrusted Certificate:** Fingerprint mismatch halts connection, displaying red security alert with old vs new fingerprint comparison.
- **Credential Revocation:** Backend returning `401 Unauthorized` on API call clears stored tokens, notifies user, and routes to `/login`.

## 7. Security and privacy

- Access tokens and refresh tokens are stored in the OS Keychain (iOS Keychain, Android Keystore, Windows Credential Manager, macOS Keychain) via Tauri secure storage APIs.
- Tokens are never written to unencrypted `localStorage`, `sessionStorage`, or IndexedDB.
- Certificate pinning prevents Machine-in-the-Middle (MitM) interception on local networks or compromised public Wi-Fi.

## 8. Verification

Verification requires running standard frontend build and unit gates per `AGENTS.md` section 5:

```bash
# Frontend quality and build verification
cd frontend
npm run lint && npx tsc --noEmit && npm run test:unit && npm run build
```

New unit tests to add:
- `frontend/src/stores/useConnectionStore.test.ts`: Verify state transitions from `unconfigured` -> `validating` -> `configured`.
- `frontend/src/lib/reconnectingSocket.test.ts`: Verify `online` and `visibilitychange` events trigger immediate socket reconnection attempts.
- `frontend/src/lib/secureStorage.test.ts`: Verify keychain read/write fallbacks.

## 9. Documentation changes required with implementation

- Update `frontend/AGENTS.md` to document `useConnectionStore` and `/connect` route entry requirements.
- Add operational guide under `documentation/operations/native-client-connection.md` explaining TOFU certificate pinning and server URL setup.

## 10. Deferred / out of scope

- Multi-server switching within a single active user session (users must disconnect/logout to change server targets).
- Automatic mDNS / Bonjour local network server auto-discovery (users manually enter domain or IP address).
