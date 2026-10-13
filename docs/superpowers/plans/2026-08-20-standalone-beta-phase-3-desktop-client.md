# Standalone Beta Phase 3: Desktop Client Implementation Plan

> **Remediated 2026-10-13:** Initial native delivery is Linux x86-64 on an Ubuntu reference desktop. Windows/macOS native packages are deferred and create no initial build or evidence prerequisites. Read the [cold handoff](2026-10-13-standalone-beta-cold-handoff.md) and [shared contracts](2026-10-13-standalone-beta-handoff-contracts.md) first; this is an active execution plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the unsigned Linux x86-64 Tauri client on an Ubuntu reference desktop connect over platform-trusted HTTPS, authenticate with revocable device credentials stored by the operating system, and complete the essential Telos media/member journeys.

**Architecture:** Stabilize the server's public device-registration and exact-origin CORS boundary first. Branch frontend authentication explicitly between browser cookie mode and native token mode. Persist only non-secret connection metadata in WebView local storage; place refresh tokens in the OS credential service through a narrow Rust bridge and keep access tokens in process memory. Remove the independent permissive TLS probe. Replace header-dependent media-element URLs with authenticated mint endpoints and self-authorizing, spend-time-revoked media locators. Build the unsigned AppImage on Ubuntu and exercise the real WebKitGTK package.

**Tech Stack:** Go 1.26.5; PostgreSQL 16.14; Redis 7; Next.js 16.2.10; React 19.2.7; TypeScript 5.9.3; Vitest 4.1.10; Tauri CLI 2.11.4 / Rust crate 2.11.5; Rust stable 1.88+; `keyring` 4.1.6; native GitHub Actions runners.

**Spec:** `docs/superpowers/specs/2026-08-20-standalone-beta-readiness-design.md`

## Global Constraints

- Execute after Phase 2 local implementation freezes the public origin, node configuration, route, and server installation contracts. Source-bound native rehearsals are not final candidate evidence. Unavailable external infrastructure blocks certification, not independent implementation; Phase 5 reruns the journey against frozen artifacts.
- Browser mode remains same-origin cookie authentication. Desktop mode never falls back from token authentication to cookies.
- `POST /api/v1/auth/devices/register` is public only because the handler verifies password credentials itself. Preserve Redis/IP/username rate limiting, constant-work credential rejection, account-active checks, request-size bounds, validation, and audit logging.
- Production CORS is deny-by-default. Supported native origins are exact `tauri://localhost` and `http://tauri.localhost`; no wildcard, suffix, reflection, `null`, or arbitrary localhost origin is accepted.
- Refresh tokens go only to Linux Secret Service for this delivery; portable interfaces leave future OS adapters possible. Access tokens remain memory-only. Native keychain errors are visible and fail closed; never silently downgrade a native client to memory persistence.
- Non-secret connection metadata may use WebView local storage and contains only normalized node origin, user ID, username, and device ID. It never contains a token, password, invite, cookie, certificate fingerprint, or authorization header.
- Only platform-trusted HTTPS is supported in release builds. The native client has no accept-any verifier, certificate fingerprint command, first-use dialog, pin override, or HTTP production connection path.
- A self-authorizing media locator is still a credential. Keep it out of logs, analytics, error bodies, referrers, and evidence; return `Cache-Control: no-store` and a restrictive referrer policy.
- Media spend-time authorization checks account/device or browser-session state and current permissions on every range, segment, key, map, and nested-manifest request.
- Exactly two physical signing files exist: cursor keyring and HLS root. HLS and direct-stream/audiobook ticket MAC keys are domain-separated; preserve the existing audiobook derivation label `telos-stream-ticket-v1`, use `telos-hls-locator-v1` for HLS and `telos-jellyfin-audio-ticket-v1` for direct audio. Ticket types are not cross-decodable. There is no third audiobook secret file.
- Do not add a schema migration unless tests prove the existing device/session tables cannot represent the authority check. If one is required, add `0024_*.sql`; never edit `0023_device_tokens.sql`.
- Linux unit tests use an injected credential-store fake. Actual OS-store persistence is an external `desktop-linux` native smoke gate on the Ubuntu reference desktop.

---

## Execution order and review focus

Run each command block in a fresh shell from the repository root unless it explicitly changes directory. Preserve task IDs, but implement in order **1 → 3 → 4 → 2 → 5 → 6 → 7**: Task 2 needs the structured credential bridge and trusted-origin normalization to type-check and run. Task 3 removes the JSON store only; Task 4 owns TLS removal so intermediate commits compile. Within Task 5 review backend authority, mint/spend routes, then frontend integration separately.

- A refresh response followed by a failed credential write must not replay the consumed token (Tasks 2/3).
- Logout or node switching during an in-flight refresh must not resurrect auth or send credentials to another node (Task 2).
- Headerless locators are bearer capabilities; issuer revocation is enforceable, viewer identity without separate auth is not (Task 5).
- Headless CI does not prove a working Secret Service, codec stack, or WebKitGTK media element (Tasks 3/6).
- Existing plaintext JSON secrets need a one-time retirement path, and native protected images need authenticated fetches (Tasks 3/5).

## Current-State Baseline

The device-registration handler already supports password verification and reserves the login limiter (`backend/auth.go:504-555`), but production wires it behind middleware that requires an existing authenticated session first (`backend/main.go:457`, `backend/main.go:881-887`). CORS omits `Authorization` from preflight response headers (`backend/security.go:277-291`), the frontend login path assumes a cookie (`frontend/src/stores/useAuthStore.ts:63-70`), and Tauri persists secrets in its JSON store (`frontend/src-tauri/src/lib.rs:59-86`). Connection setup performs a separate certificate-pin probe (`frontend/src/app/connect/page.tsx:82-96`), while native media elements cannot attach bearer headers (`frontend/src/components/stream/HlsPlayer.tsx:44-56`) and the HLS locator route itself is authentication-wrapped (`backend/main.go:526`). These are the specific seams this phase replaces.

---

## File Structure

- Modify: `backend/main.go` — public registration route and bare locator routes.
- Create: `backend/routes.go` and `backend/routes_test.go` — auditable route registration and production middleware-chain tests.
- Modify: `backend/auth.go`, `backend/devices_integration_test.go` — device registration boundary behavior.
- Modify: `backend/security.go`, `backend/security_test.go` — exact native origins and `Authorization` preflight.
- Modify: `docker-compose.yml`, `.env.example`, `cli/lib/config.sh` — pass/validate supported native origins.
- Modify: `frontend/src/lib/deviceAuth.ts`, `frontend/src/lib/deviceAuth.test.ts` — explicit token lifecycle.
- Modify: `frontend/src/stores/useAuthStore.ts`, `frontend/src/stores/useAuthStore.test.ts` — cookie/token branching and startup/logout semantics.
- Create: `frontend/src/lib/connectionProfile.ts`, `frontend/src/lib/connectionProfile.test.ts` — non-secret native profile persistence.
- Modify: `frontend/src/stores/useConnectionStore.ts`, `frontend/src/stores/useConnectionStore.test.ts`, `frontend/src/app/page.tsx`, `frontend/src/app/login/page.tsx` — hydrate native profile and route native login explicitly.
- Create: `frontend/src-tauri/src/credentials.rs` — OS credential adapter and commands.
- Modify: `frontend/src-tauri/src/lib.rs`, `frontend/src-tauri/Cargo.toml`, `frontend/src-tauri/Cargo.lock`, `frontend/src-tauri/capabilities/default.json` — keyring bridge; remove secret JSON store.
- Modify: `frontend/src/lib/secureStorage.ts`, `frontend/src/lib/secureStorage.test.ts`, `frontend/src/lib/nativeEnv.ts`, `frontend/src/lib/nativeShellContract.test.ts` — structured credential bridge and fail-closed native behavior.
- Delete: `frontend/src-tauri/src/tls.rs`.
- Delete: `frontend/src/lib/certPinning.ts`, `frontend/src/lib/certPinning.test.ts`.
- Modify: `frontend/src/app/connect/page.tsx`, `frontend/src/app/connect/connect_page.test.tsx`, `frontend/src/stores/useConnectionStore.ts`, `frontend/src/stores/useConnectionStore.test.ts`, and `frontend/src-tauri/README.md` — platform-trust-only connection UX.
- Modify: `backend/hls.go`, `backend/hls_test.go`, `backend/media.go`, `backend/media_test.go`, `backend/streamticket.go`, `backend/streamticket_test.go` — revocable self-authorizing media locators.
- Modify: `backend/api/openapi.json` and generated frontend client — new mint endpoints/contracts.
- Modify: `frontend/src/components/stream/HlsPlayer.tsx`, `frontend/src/components/stream/HlsPlayer.test.tsx`, `frontend/src/app/(shell)/stream/page.tsx` — mint URLs before media-element use.
- Create: `.github/workflows/desktop.yml` — Ubuntu format/lint/test/AppImage build job.
- Create: `scripts/verify-desktop-artifact.py`, `tests/operations/desktop_artifact_test.sh` — artifact shape/identity checks.
- Create: `documentation/operations/desktop-beta.md` — unsigned installation and manual smoke procedure.
- Modify: `ci/phase-gates.json`, `.github/workflows/ci.yml` — Phase 3 local/external gates.

---

### Task 1: Make password-backed device registration reachable and CORS-correct

**Files:**
- Create: `backend/routes.go`
- Create: `backend/routes_test.go`
- Modify: `backend/main.go`
- Modify: `backend/auth.go`
- Modify: `backend/devices_integration_test.go`
- Modify: `backend/security.go`
- Modify: `backend/security_test.go`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `cli/lib/config.sh`

**Interfaces:**
- Produces: public `POST /api/v1/auth/devices/register` with handler-owned authentication.
- Produces: exact-origin production CORS allowing `Content-Type` and `Authorization` for configured origins.
- Consumes: `TELOS_ALLOWED_ORIGINS` from persistent `node.env` through Compose.

- [ ] **Step 1: Add a production-chain registration integration test**

Extract route registration and the outer middleware composition into testable functions without moving concern handlers into `main.go`:

```go
func registerRoutes(mux *http.ServeMux)
func gatewayHandler(mux http.Handler, cfg SecurityConfig) http.Handler
```

In `backend/routes_test.go`, build the same handler chain as production with the integration fixture DB/Redis, then send a password registration request with no cookie/bearer token and `Origin: tauri://localhost`. Assert `200`, nonblank device/refresh/access values, a database device row, one successful limiter completion, and audit output with no password/token. Send wrong password/unknown user and assert indistinguishable `401` bodies plus charged failures. Send valid deviceName/platform/clientVersion but no cookie or password and assert `401`; an entirely empty request remains validation `400`. Use `setupIntegrationDB`/the existing integration fixture helpers and skip only when deliberately running the unit-only suite.

Add a static assertion that the registered handler for this exact route is not wrapped in `withAuth`; exercise the handler through `registerRoutes`, not by calling `handleRegisterDevice` directly.

- [ ] **Step 2: Add CORS preflight cases**

Extend `backend/security_test.go` with an OPTIONS table:

| Origin | Requested headers | Expected |
|---|---|---|
| `tauri://localhost` | `authorization, content-type` | `204`, exact ACAO, both headers |
| `http://tauri.localhost` | `Authorization` | `204`, exact ACAO |
| public browser origin | `Content-Type` | same-origin behavior |
| `http://localhost` | either | no CORS approval in production |
| `null` | either | no CORS approval |
| `https://evil.example` | either | no CORS approval |
| approved origin | `X-Injected` | reject requested header |

Also assert `Vary: Origin, Access-Control-Request-Headers, Access-Control-Request-Method`, allowed methods contain only actual API methods, and credentials are allowed only with an exact origin.

- [ ] **Step 3: Run focused tests and observe both defects**

```bash
bash scripts/test-backend.sh all -count=1 -v -run 'TestProductionDeviceRegistration|TestCORSPreflight'
```

Expected before implementation: registration returns `401` before the handler, and preflight omits `Authorization`. Afterward both named test groups execute and pass, with no SKIP lines. The existing `security` selector does not include `TestCORSPreflight`; the explicit `all -run` command is required.

- [ ] **Step 4: Extract route/boundary construction and unwrap only registration**

Move the route table mechanically into `routes.go`; do not change unrelated paths. Register:

```go
mux.HandleFunc("POST /api/v1/auth/devices/register", handleRegisterDevice)
```

Keep refresh public and every device listing/revocation/ticket endpoint behind `withAuth`. Preserve the complete outer boundary by returning it from `gatewayHandler` and using that function in `main` and tests.

- [ ] **Step 5: Permit only required cross-origin request headers**

Rename `devCORS` to `corsMiddleware` if doing so improves truth. On an approved origin and valid preflight, emit:

```http
Access-Control-Allow-Origin: tauri://localhost
Access-Control-Allow-Credentials: true
Access-Control-Allow-Methods: GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Headers: Authorization, Content-Type
```

Do not reflect unapproved requested methods/headers. Ordinary approved responses get exact ACAO/credentials; unapproved origins get neither.

- [ ] **Step 6: Pass and validate native origins through Compose**

Add `TELOS_ALLOWED_ORIGINS=${TELOS_ALLOWED_ORIGINS:-}` to core. Set `.env.example` to the beta-supported pair:

```dotenv
TELOS_ALLOWED_ORIGINS=tauri://localhost,http://tauri.localhost
```

Use exactly this pair for the beta configuration, canonicalizing order and rejecting duplicates. Do not introduce an unimplemented browser-only mode/flag to waive this check; a future explicit mode can revise the shared contract. Reject `*`, `null`, paths, queries, userinfo, and unrecognized origins.

- [ ] **Step 7: Run backend and Compose checks**

```bash
bash scripts/test-backend.sh auth
bash scripts/test-backend.sh security
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env
```

Expected: all exit `0`; password registration passes only through the public handler-owned exchange.

- [ ] **Step 8: Commit the server desktop boundary**

```bash
git add backend/routes.go backend/routes_test.go backend/main.go backend/auth.go \
  backend/devices_integration_test.go backend/security.go backend/security_test.go \
  docker-compose.yml .env.example cli/lib/config.sh
git commit -m "auth: expose rate-limited desktop device registration"
```

---

### Task 2: Implement explicit browser and desktop authentication state machines

**Files:**
- Modify: `frontend/src/lib/deviceAuth.ts`
- Modify: `frontend/src/lib/deviceAuth.test.ts`
- Modify: `frontend/src/lib/api.ts`, `frontend/src/lib/api.test.ts` — access expiry/retry integration
- Modify: `frontend/src/components/AppShell.tsx` — startup routing owner
- Modify: `frontend/src/stores/useAuthStore.ts`
- Modify: `frontend/src/stores/useAuthStore.test.ts`
- Create: `frontend/src/lib/connectionProfile.ts`
- Create: `frontend/src/lib/connectionProfile.test.ts`
- Modify: `frontend/src/stores/useConnectionStore.ts`
- Modify: `frontend/src/stores/useConnectionStore.test.ts`
- Modify: `frontend/src/app/page.tsx`
- Modify: `frontend/src/app/login/page.tsx` only for distinct native keychain/transport messages.

**Interfaces:**
- Produces: `ConnectionProfile { origin, userId, username, deviceId }` without credentials.
- Produces: `initializeAuth()`, `login()`, and `logout()` with deterministic cookie/token branches.
- Consumes: structured credential functions finalized in Task 3; tests mock the interface until then.

- [ ] **Step 1: Write state-machine tests first**

Add Vitest coverage for this exact transition table:

| Mode/event | Required calls | Result |
|---|---|---|
| cookie login | `/auth/login`, `/auth/me` | authenticated; no keychain call |
| token login | device register, temporary access install, `/auth/me`, keychain set, profile set | authenticated |
| token login keychain failure | revoke new device, clear access/profile | anonymous with credential-store error |
| native startup with profile+refresh | keychain get, one refresh rotation, keychain replace, `/auth/me` | authenticated |
| startup 401/403 | keychain delete, access clear, profile retains origin only | anonymous |
| startup network/5xx | retain keychain/profile | reconnecting/error, retry available |
| cookie logout | `/auth/logout`, user clear | anonymous |
| token logout | `/auth/logout` with bearer, keychain delete, access clear, account/device profile clear | anonymous |
| token logout network failure | local credential still deleted after best-effort revoke | anonymous; remote revocation pending warning |
| keychain write fails after successful refresh | best-effort revoke using returned access token; clear access; delete consumed credential | anonymous with storage error; never retry consumed refresh |
| keychain delete fails at logout | clear access immediately; retain cleanup address and suppress auto-login | anonymous with cleanup-required error and explicit retry |
| logout/node switch races refresh | invalidate auth generation; discard late result and best-effort revoke its device | no auth resurrection/no cross-origin credential send |

Assert concurrent startup/API/socket refresh shares one rotation **per captured origin/account/device**, passwords are absent from state after login settles, and access tokens are absent from local/session storage. Capture an immutable auth generation at operation start and check it before every adoption. Switching nodes clears memory, closes sockets, revokes active blob URLs, and increments generation; a late old-node response cannot overwrite the new profile.

- [ ] **Step 2: Run the tests and confirm cookie-only login fails token cases**

```bash
cd frontend
npx vitest run src/lib/deviceAuth.test.ts src/lib/connectionProfile.test.ts \
  src/stores/useAuthStore.test.ts src/stores/useConnectionStore.test.ts
```

Expected: nonzero for missing profile/startup APIs and unconditional cookie login.

- [ ] **Step 3: Implement non-secret profile persistence**

Use one key, `telos.connection.v1`, in local storage only when `isNativeApp()` is true. Parse with closed field checks; require an HTTPS normalized origin (or explicit debug-only loopback exception shared with Task 4) and UUID user/device IDs before treating it as credential-addressable. Provide:

```typescript
export interface ConnectionProfile {
  origin: string;
  userId: string | null;
  username: string | null;
  deviceId: string | null;
}

loadConnectionProfile(): ConnectionProfile | null
saveConnectionProfile(profile: ConnectionProfile): void
clearConnectionIdentity(): void // retain origin; clear user/device
clearConnectionProfile(): void
```

Reject and delete malformed/credential-bearing records. `useConnectionStore` hydrates only the origin at native startup and configures token mode with a null access token.

- [ ] **Step 4: Separate registration from token adoption**

Change `registerDevice` to return the server response without persisting it. Add `adoptDeviceCredential(profile, tokens)` that writes the refresh token first through the structured key and installs the access token only after persistence succeeds. During login, temporarily install the returned access token, call `/auth/me` to obtain authoritative user ID/username, then adopt/persist the full profile. On adoption failure, best-effort revoke the just-created device and clear memory.

- [ ] **Step 5: Branch `useAuthStore` by server mode**

Cookie behavior remains unchanged. Token login builds a bounded device name/platform/version from native platform data and follows the registration flow. `initializeAuth` rotates only when a valid native profile exists. Refresh `401/403` deletes the matching OS credential; network/5xx retains it. Logout clears access and invalidates the generation immediately, then best-effort revokes using a captured token and deletes the exact OS credential. If deletion fails, keep a non-secret cleanup tombstone outside `ConnectionProfile` (`telos.credential-cleanup.v1`, containing only the structured credential key); it suppresses automatic login until deletion succeeds. Never claim all credentials are gone while deletion failed. Test restart with this tombstone.

Add `accessExpiresAt` in memory from `accessExpiresIn`, using a monotonic deadline where possible. The authenticated API/blob/socket layer ensures a single refresh before an expired access credential is used; refresh/register/logout themselves bypass this helper to avoid recursion. Avoid automatically replaying arbitrary POST requests: refresh before dispatch, and at most retry a GET/HEAD once on an authenticated 401. Capture the selected origin for the entire request/rotation. Test ten concurrent API/socket callers, an ordinary authorization 403, refresh 401, network/5xx, and consumed-token write failure. `api.ts` currently performs no automatic refresh, so testing only `initializeAuth()` would leave long-running desktop sessions broken.

Bootstrap/invite remain browser-only for beta. On a native client, direct those modes to a clear instruction to complete account creation in the hosted browser, then sign in on desktop; do not send bootstrap/invite secrets through device registration.

- [ ] **Step 6: Run focused frontend tests**

```bash
cd frontend
npx vitest run src/lib/deviceAuth.test.ts src/lib/connectionProfile.test.ts \
  src/stores/useAuthStore.test.ts src/stores/useConnectionStore.test.ts
npx tsc --noEmit
```

Expected: all exit `0`; storage enumeration test finds no token/password values.

- [ ] **Step 7: Commit the auth state machine**

```bash
git add frontend/src/lib/deviceAuth.ts frontend/src/lib/deviceAuth.test.ts \
  frontend/src/lib/connectionProfile.ts frontend/src/lib/connectionProfile.test.ts \
  frontend/src/stores/useAuthStore.ts frontend/src/stores/useAuthStore.test.ts \
  frontend/src/stores/useConnectionStore.ts frontend/src/stores/useConnectionStore.test.ts \
  frontend/src/app/page.tsx frontend/src/app/login/page.tsx
git commit -m "frontend: use device authentication in native mode"
```

---

### Task 3: Replace JSON secrets with operating-system credential storage

**Files:**
- Create: `frontend/src-tauri/src/credentials.rs`
- Modify: `frontend/src-tauri/src/lib.rs`
- Modify: `frontend/src-tauri/Cargo.toml`
- Modify: `frontend/src-tauri/Cargo.lock`
- Modify: `frontend/src-tauri/capabilities/default.json`
- Modify: `frontend/src/lib/secureStorage.ts`
- Modify: `frontend/src/lib/secureStorage.test.ts`
- Modify: `frontend/src/lib/nativeEnv.ts`
- Modify: `frontend/src/lib/nativeShellContract.test.ts`

**Interfaces:**
- Produces Tauri commands: `credential_get`, `credential_set`, `credential_delete`.
- Consumes structured key `{ origin, accountId, deviceId }` and never a caller-selected raw storage key.
- Produces bridge: `window.__TELOS_NATIVE_CREDENTIALS__`.

- [ ] **Step 1: Write Rust adapter tests with an injected fake**

Define in `credentials.rs`:

```rust
#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CredentialKey {
    pub origin: String,
    pub account_id: String,
    pub device_id: String,
}

trait CredentialBackend {
    fn get(&self, service: &str, account: &str) -> Result<Option<String>, CredentialError>;
    fn set(&self, service: &str, account: &str, secret: &str) -> Result<(), CredentialError>;
    fn delete(&self, service: &str, account: &str) -> Result<(), CredentialError>;
}
```

Test HTTPS normalization, default-port folding, rejection of paths/userinfo/non-HTTPS, stable service/account derivation, distinct origins/accounts/devices, get/set/delete forwarding, missing-entry-as-`None`, backend error propagation without secret text, and debug/error formatting that never includes the secret.

- [ ] **Step 2: Update cross-language tests before production code**

Change TypeScript tests to require structured keys and `__TELOS_NATIVE_CREDENTIALS__`. Assert native bridge errors reject rather than fall back to memory; web mode remains process-memory only and reports non-persistent. Assert production Rust source contains no `tauri_plugin_store`, `telos_secret_`, or `__TELOS_NATIVE_STORE__`; the legacy JSON filename is allowed only in the retirement helper described below, never in read/write storage code.

- [ ] **Step 3: Run Rust/TypeScript tests and observe current JSON-store failures**

```bash
cd frontend
npx vitest run src/lib/secureStorage.test.ts src/lib/nativeShellContract.test.ts
cd src-tauri
cargo test
```

Expected: nonzero until the credential adapter/bridge replaces the plugin store.

- [ ] **Step 4: Pin the credential dependency and Rust floor**

In `Cargo.toml`, remove `tauri-plugin-store`; retain `rustls`/`x509-parser` until Task 4 deletes the TLS module; keep `sha2 = "0.10"` for non-secret key derivation and add:

```toml
keyring = { version = "=4.1.6", default-features = false, features = ["v1"] }
url = "=2.5.8"
```

The [4.1.6 manifest](https://raw.githubusercontent.com/open-source-cooperative/keyring-rs/v4.1.6/Cargo.toml) confirms Rust 1.88 and the `v1` feature (also its default); on Linux it selects the zbus Secret Service backend. The [Entry API](https://docs.rs/keyring/4.1.6/keyring/v1/struct.Entry.html) provides the methods below. Keep `cli` disabled. Regenerate `Cargo.lock` with Rust stable 1.88 or newer, commit it, then run subsequent checks with `--locked`. Review the resulting dependency diff and update license inputs in Phase 4; do not hand-edit the lock.

- [ ] **Step 5: Implement the OS backend**

Use `keyring::Entry::new(service, account)`, `get_password`, `set_password`, and `delete_credential`. Derive:

```text
service = com.telos.app/{sha256(normalized-origin)}
account = {account-id}/{device-id}
```

Validate IDs as canonical UUIDs. Dispatch blocking credential calls with `tauri::async_runtime::spawn_blocking`, not on the UI thread. A real Linux session must expose a D-Bus session bus and an unlocked Secret Service collection (e.g. GNOME Keyring); missing/locked/denied service is a visible error, never an in-memory fallback. Translate only not-found to `None`; distinguish unavailable/locked/denied/backend errors with non-secret messages. Never log the service call's secret or accept arbitrary service/account strings from JavaScript.

- [ ] **Step 6: Replace the bridge and remove store permissions**

Publish:

```javascript
window.__TELOS_NATIVE_CREDENTIALS__ = {
  get: (key) => invoke("credential_get", { key }),
  set: (key, secret) => invoke("credential_set", { key, secret }),
  delete: (key) => invoke("credential_delete", { key })
};
```

Register these credential commands alongside existing clipboard/open-external commands. Remove only old secret-store commands, plugin/setup calls, and `store:default` capability here; Task 4 removes TLS commands. The credential commands are app-owned Tauri commands and need no plugin capability entry.

- [ ] **Step 6a: Retire existing plaintext stores without importing secrets**

In `credentials.rs`, add a one-time startup helper that resolves Tauri’s app-data directory, detects the exact regular file `telos-secrets.json`, and removes it without parsing, logging, or importing its contents. Refuse symlinks/unexpected types and surface removal failures. Clear legacy frontend profile state and require re-login with a notice to revoke previous devices in the hosted browser. Deletion does not promise secure erasure or remote revocation. Tests use a temporary app-data directory and prove unrelated files remain untouched. The legacy filename is permitted only in this helper and its tests; artifact verification rejects an actual bundled file of that name, not a binary containing the retirement string.

- [ ] **Step 7: Make TypeScript native storage fail closed**

Export structured `getCredential`, `setCredential`, `deleteCredential`. In native mode, absence or rejection of the bridge throws `CredentialStoreError`; it never writes the memory map. In browser mode, the map remains available for isolated unit/dev behavior but token-mode auth must not call it.

- [ ] **Step 8: Run credential checks**

```bash
cd frontend/src-tauri
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
cd ..
npx vitest run src/lib/secureStorage.test.ts src/lib/nativeShellContract.test.ts \
  src/lib/deviceAuth.test.ts src/stores/useAuthStore.test.ts
npx tsc --noEmit
```

Expected: all exit `0`; repository scan finds no legacy store bridge/plugin (the retirement helper intentionally names the legacy file):

```bash
! rg -n 'tauri_plugin_store|telos_secret_|__TELOS_NATIVE_STORE__' \
  frontend/src frontend/src-tauri/src frontend/src-tauri/Cargo.toml frontend/src-tauri/capabilities
```

- [ ] **Step 9: Commit OS credential storage**

```bash
git add frontend/src-tauri/src/credentials.rs frontend/src-tauri/src/lib.rs \
  frontend/src-tauri/Cargo.toml frontend/src-tauri/Cargo.lock \
  frontend/src-tauri/capabilities/default.json frontend/src/lib/secureStorage.ts \
  frontend/src/lib/secureStorage.test.ts frontend/src/lib/nativeEnv.ts \
  frontend/src/lib/nativeShellContract.test.ts
git commit -m "desktop: store refresh tokens in the OS credential service"
```

---

### Task 4: Remove TLS pinning and enforce platform trust

**Files:**
- Delete: `frontend/src-tauri/src/tls.rs`
- Delete: `frontend/src/lib/certPinning.ts`
- Delete: `frontend/src/lib/certPinning.test.ts`
- Modify: `frontend/src-tauri/src/lib.rs`
- Modify: `frontend/src-tauri/Cargo.toml`
- Modify: `frontend/src/app/connect/page.tsx`
- Modify: `frontend/src/app/connect/connect_page.test.tsx`
- Modify: `frontend/src/stores/useConnectionStore.ts`
- Modify: `frontend/src/stores/useConnectionStore.test.ts`
- Modify: `frontend/src/lib/serverConfig.ts`
- Modify: `frontend/src/lib/serverConfig.test.ts`
- Modify: `frontend/src/lib/nativeEnv.ts`
- Modify: `frontend/src/lib/nativeShellContract.test.ts`
- Modify: `frontend/src-tauri/README.md`
- Modify: `documentation/operations/building-native-clients.md`

**Interfaces:**
- Produces: one connection validation path: ordinary WebView `fetch` to HTTPS `/api/v1/health`.
- Removes: fingerprint, first-use, mismatch, pin storage, accept-any verifier, and HTTP release path.

- [ ] **Step 1: Rewrite connection tests to platform-trust behavior**

Test that:

- a bare host normalizes to `https://host`;
- a production native connection explicitly using `http://` is rejected before fetch;
- an explicitly enabled debug build may use HTTP only for `localhost`, `127.0.0.1`, or `[::1]`; JavaScript and Rust credential normalization use the same debug flag, while release code cannot enable it;
- a successful ordinary health fetch saves only the normalized origin and routes to login;
- fetch certificate failure shows platform trust guidance and offers no continue/override action;
- no fingerprint confirmation screen or pin read/write occurs;
- version hard/soft skew behavior remains.

- [ ] **Step 2: Run tests and observe the first-use UI failure**

```bash
cd frontend
npx vitest run src/app/connect/connect_page.test.tsx \
  src/stores/useConnectionStore.test.ts src/lib/nativeShellContract.test.ts
```

Expected: nonzero because current code invokes an independent certificate probe and exposes trust confirmation.

- [ ] **Step 3: Delete the non-enforcing native TLS path**

Remove `mod tls`, `telos_leaf_certificate`, `__TELOS_NATIVE_TLS__`, rustls/x509 dependencies, pinning TypeScript/tests, and fingerprint state fields. The connection page calls only `probeNode(serverUrl)` and treats its network/TLS failure as final.

- [ ] **Step 4: Enforce trusted HTTPS for release clients**

Add `normalizeTrustedNodeUrl(raw, allowLoopbackHTTP)` in `serverConfig.ts`. Require `https:` except the explicit loopback development case. Never accept self-signed certificates in code; operator-installed private CAs work because the platform trust store accepts them.

Use distinct invalid URL, HTTPS required, non-Telos response, and version incompatibility errors. WebView fetch normally hides whether a network rejection is TLS or transport; use one honest combined connection/trust message with platform-CA guidance for that case, rather than asserting a certificate diagnosis the API cannot provide. Reject userinfo, query, fragment, and non-root paths; this client stores an origin, not a reverse-proxy path prefix.

- [ ] **Step 5: Remove active pinning/self-signed claims**

Scan current source, README, Tauri README, product docs, and operations docs, excluding `docs/superpowers/specs/` and `docs/superpowers/plans/` as historical records. Replace claims with platform-trusted HTTPS instructions. Add this scan to product truth:

```bash
rg -n 'TOFU|trust on first use|certificate pin|self-signed|leaf fingerprint|accept.?any' \
  frontend/src frontend/src-tauri/src frontend/src-tauri/README.md README.md documentation
```

Expected after cleanup: only explicit statements that the beta does not support those paths.

- [ ] **Step 6: Run frontend/Rust checks**

```bash
cd frontend
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
cd src-tauri
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
```

Expected: all exit `0`; no TLS-probe command or accept-any verifier remains.

- [ ] **Step 7: Commit platform-trust-only behavior**

```bash
git rm frontend/src-tauri/src/tls.rs frontend/src/lib/certPinning.ts frontend/src/lib/certPinning.test.ts
git add frontend/src-tauri/src/lib.rs frontend/src-tauri/Cargo.toml frontend/src-tauri/Cargo.lock \
  frontend/src/app/connect frontend/src/stores/useConnectionStore.ts \
  frontend/src/stores/useConnectionStore.test.ts frontend/src/lib/serverConfig.ts \
  frontend/src/lib/nativeEnv.ts frontend/src/lib/nativeShellContract.test.ts \
  frontend/src-tauri/README.md README.md documentation scripts/check-product-truth.sh
git commit -m "desktop: rely exclusively on platform-trusted HTTPS"
```

---

### Task 5: Make video and audio URLs self-authorizing and revocable

**Files:**
- Modify: `backend/hls.go`
- Modify: `backend/hls_test.go`
- Modify: `backend/media.go`
- Modify: `backend/media_test.go`
- Modify: `backend/streamticket.go`
- Modify: `backend/streamticket_test.go`
- Modify: `backend/routes.go`
- Modify: `backend/devices_integration_test.go`
- Modify: `backend/api/openapi.json`
- Modify: `frontend/src/lib/generated/api-client.ts`
- Create: `frontend/src/lib/mediaLocator.ts`
- Create: `frontend/src/lib/mediaLocator.test.ts`
- Modify: `frontend/src/components/stream/HlsPlayer.tsx`
- Modify: `frontend/src/components/stream/HlsPlayer.test.tsx`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/components/library/AudiobookPlayer.tsx`, its tests, and `frontend/src/lib/api.ts`
- Create: `frontend/src/components/AuthenticatedImage.tsx` and tests; migrate protected cover/avatar callers in AppShell, Stream, Library and reader components.

**Interfaces:**
- Produces authenticated mint endpoints:
  - `POST /api/v1/stream/video/{id}/playback-locator`
  - `POST /api/v1/stream/audio/{id}/stream-ticket`
- Produces bare spend endpoints:
  - `GET /api/v1/hls/{locator}`
  - `GET /api/v1/stream/audio-ticket/{ticket}`
- Preserves `POST /api/v1/library/audiobooks/{id}/stream-ticket` and `GET /api/v1/library/audiobook-stream/{ticket}` with their existing request/response shapes.
- Preserves audiobook mint/spend endpoints while migrating them to the same revocable authority model.

- [ ] **Step 1: Write locator authority tests first**

Add unit and integration cases for both cookie sessions and bearer devices:

- mint requires current authentication and `view_media`/`view_library`;
- token includes item, resource/type, user, authority kind (`session` or `device`), stable authority ID, and expiry but no raw credential;
- HLS TTL and direct-stream ticket TTL are 12 hours;
- spend succeeds with no cookie/header and supports Range/HEAD where the resource does;
- tamper, expiry, wrong item/resource/type, user/authority ownership mismatch in signed test claims, malformed token, and type-confusion fail. A copied valid URL is a bearer capability and remains usable until issuer revocation/expiry; without a cookie/header the server cannot identify a different viewer. Explicitly test this boundary instead of promising cross-user/device copy detection;
- logout/revoked browser session, revoked device, expired device, disabled user, role removal, item deactivation, library allowlist removal, and source unavailability take effect at the next spend;
- nested `.m3u8` rewrites preserve the original authority rather than trying to read auth from the bare request;
- every segment/key/map locator retains that authority and expiry no later than its parent locator and current authority expiry; rewriting cannot extend a 12-hour grant indefinitely;
- logs/errors never contain the locator or upstream admin token.

Inject `now` into authority/token functions for unit tests; integration expiry uses explicit past SQL timestamps and does not sleep. Run DB/Redis cases through `bash scripts/test-backend.sh all -count=1 -v -run 'TestHLS|TestStreamTicket|TestMediaLocator'`; the existing product selector matches commentary, not these tests. Require named cases to execute with no skips.

- [ ] **Step 2: Write frontend media-element tests**

Mock `HTMLMediaElement.canPlayType`, hls.js, and the mint API. Assert:

- native-HLS video mints then assigns the returned absolute trusted URL to `video.src` with no header requirement;
- hls.js loads the minted locator and does not attach an access token to bare locator requests;
- audio mints a direct ticket before assigning `src`;
- mint rejection displays a bounded error and never assigns the protected original route;
- changing item/unmount revokes object/component state and ignores a late mint response;
- locator URLs are never logged.

- [ ] **Step 3: Run focused tests and capture current failures**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./... -run 'TestHLS|TestStreamTicket|TestMediaLocator'
cd frontend
npx vitest run src/lib/mediaLocator.test.ts src/components/stream/HlsPlayer.test.tsx
```

Expected: the bare HLS route currently returns `401`; no initial locator/audio mint exists; native media still receives a protected URL.

- [ ] **Step 4: Introduce a stable authority claim**

Replace raw-token binding with:

```go
type mediaAuthority struct {
    Kind string `json:"k"` // session|device
    ID   string `json:"a"` // session hash or device ID
}

func authorityFromUser(*UserContext) (mediaAuthority, error)
func loadAuthorizedMediaUser(ctx context.Context, authority mediaAuthority, userID, permission string, now time.Time) (*UserContext, time.Time, error)
```

Return the authorized user and the authority expiry. For bearer users, bind to `DeviceID`, require `devices.user_id` equals the signed user, `revoked_at IS NULL`, active user/current effective permissions, and at least one unconsumed/unreplaced refresh-token row with expiry after `now`; derive the horizon from that row. Devices have no expiry column (`backend/db/migrations/0023_device_tokens.sql:4-25`), so “expired device” means no current live refresh credential. Do not bind to the 15-minute access session: playback must survive access-token rotation. For cookie users, bind to `SessionHash`, require an ordinary browser session (`device_id IS NULL`), matching user, non-revoked/unexpired session, and current permissions; use the same role-loading SQL as `LoadAuthenticatedUser(ctx, dbPool, hash)` (`backend/database.go:115`), extended to return the expiry needed by minting. No schema change is needed. Return indistinguishable invalid-capability responses for nonexistent/wrong principals. Clamp mint expiry to `min(now+12h, authorityExpiry)`, report the actual seconds remaining, and preserve that bound in nested locators.

- [ ] **Step 5: Refactor HLS mint/spend**

Set the maximum `hlsLocatorTTL = 12 * time.Hour` (clamped to authority expiry). Include authority kind/ID and validate all required claims after the MAC. The authenticated video mint performs item resolution and Jellyfin playback-info negotiation, then returns:

```json
{"url":"/api/v1/hls/eyJpIjoiaXRlbS0xIn0.c2lnbmF0dXJl","expiresIn":43200}
```

where the first locator names the negotiated allowlisted manifest resource; do not assume all Jellyfin PlaybackInfo responses use a literal `main.m3u8` if the existing negotiation returns another supported path. `expiresIn` is the clamped lifetime. Register the GET locator route bare. On each spend, validate authority/account/permission/item before upstream work. Pass the verified claims into nested manifest rewriting so it does not inspect the bare request for auth.

- [ ] **Step 6: Generalize direct-stream tickets without weakening audiobooks**

Add a required type claim (`jellyfin-audio` or `grimmory-audiobook`) and stable authority. Use the exact domain separation labels in Global Constraints; preserve the current audiobook label and existing route shapes. The audio mint resolves/authorizes its Jellyfin item and returns the bare ticket URL. The spend route rechecks `view_media`, item/library/source, and stream capacity before proxying. Update audiobook tickets to recheck a stable device authority after access-token rotation while retaining `view_library` and catalog checks.

- [ ] **Step 7: Update OpenAPI and frontend playback**

Document response/error shapes, regenerate the TypeScript client, and add `mediaLocator.ts` that makes the authenticated mint call and resolves the returned relative URL against `apiBase()`. Change `HlsPlayer` props to an item ID and media kind rather than an already-protected URL. Assign only minted URLs to native media elements; hls.js uses the same URL without bearer injection.

- [ ] **Step 7a: Close remaining native protected-resource paths**

Audit direct `src`, CSS image URLs, reader document loaders, and navigations using `avatarUrl`, `libraryCoverUrl`, `libraryContentUrl`, or `assetUrl`; native media fixes alone leave authenticated cover/avatar requests bare (`frontend/src/components/AppShell.tsx:404`, `frontend/src/components/library/AudiobookPlayer.tsx:395`). Use a reusable `AuthenticatedImage` backed by `apiBlob` and object URLs for bounded images; revoke object URLs on replacement/unmount/auth-generation change. Reject off-node URLs before attaching Authorization. Reader document fetches and downloads must use the authenticated API/blob path, never a direct protected native navigation. Test image fetch headers, object URL cleanup, item changes, and rejection of an off-origin URL. Preserve browser cookie behavior. Add live cover rendering and ebook reading to native smoke.

- [ ] **Step 8: Run backend/frontend regression**

```bash
bash scripts/test-backend.sh auth
bash scripts/test-backend.sh product
bash scripts/test-backend.sh all -count=1 -v -run 'TestHLS|TestStreamTicket|TestMediaLocator'
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend
npm run generate:client
git diff --exit-code src/lib/generated/api-client.ts
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
```

Expected: all exit `0`; OpenAPI generation is clean; bare media spends pass only with valid locators.

- [ ] **Step 9: Commit token-safe media**

```bash
git add backend/hls.go backend/hls_test.go backend/media.go backend/media_test.go \
  backend/streamticket.go backend/streamticket_test.go backend/main.go backend/routes.go \
  backend/devices_integration_test.go backend/api/openapi.json \
  frontend/src/lib/generated/api-client.ts frontend/src/lib/mediaLocator.ts \
  frontend/src/lib/mediaLocator.test.ts frontend/src/components/stream/HlsPlayer.tsx \
  frontend/src/components/stream/HlsPlayer.test.tsx frontend/src/app/'(shell)'/stream/page.tsx
git commit -m "media: issue revocable self-authorizing playback locators"
```

---

### Task 6: Build and rehearse the unsigned Linux AppImage

**Files:**
- Create: `.github/workflows/desktop.yml`
- Create: `scripts/verify-desktop-artifact.py`
- Create: `tests/operations/desktop_artifact_test.sh`
- Modify: `frontend/src-tauri/tauri.conf.json`
- Modify: `frontend/src-tauri/README.md`
- Create: `documentation/operations/desktop-beta.md`
- Modify: `ci/phase-gates.json`

**Interfaces:**
- Produces: Linux x86-64 AppImage as the initial unsigned beta artifact. Windows/macOS packaging is deferred, with no required jobs or smoke records here.
- Produces external gate ID: `desktop-linux`.
- Consumes later: candidate lock and release workflow from Phase 5.

- [ ] **Step 1: Add artifact-verifier fixture tests**

Create fake bundle directories and assert the verifier rejects wrong extension, duplicate platform artifact, zero-length file, embedded `telos-secrets.json`, unexpected updater/signing metadata, version mismatch, missing candidate/source identity sidecar, and checksum mismatch. It must accept exactly one Linux AppImage artifact with a matching sidecar.

- [ ] **Step 2: Run verifier tests and observe the missing command**

```bash
bash tests/operations/desktop_artifact_test.sh
```

Expected: nonzero because `verify-desktop-artifact.py` does not exist.

- [ ] **Step 3: Implement platform-specific artifact verification**

Expose:

```text
verify-desktop-artifact.py --platform linux \
  --artifact PATH --identity PATH [--candidate-lock PATH]
```

The only accepted suffix is `.AppImage`. Hash raw bytes and validate the shared artifact-identity sidecar (source commit, version, platform/architecture, size, SHA-256 and build run). Use a pinned `unsquashfs` inspection command to list/extract the SquashFS payload in a temporary directory; do not execute the AppImage to inspect untrusted input. Reject actual bundled credential files and external updater configuration. Fail if the inspection tool/format is unavailable; suffix checking alone is insufficient. Before Phase 5 the identity is source-bound. With `--candidate-lock`, verify the artifact digest against the lock; do not rewrite the binary or sidecar to embed the lock digest and create a hash cycle.

- [ ] **Step 4: Tighten Tauri bundle configuration**

Keep identifier `com.telos.app`, set product/version from a single release input verified against `package.json`/Cargo/manifest, disable updater/signing configuration, and narrow CSP from global `https: http: ws: wss:` to required trusted node connections plus Tauri IPC without enabling arbitrary navigation. The connection page still lets a member choose an HTTPS node; document why runtime `connect-src https:` is necessary while navigation/open-external remains filtered.

- [ ] **Step 5: Add native CI jobs**

Create one Ubuntu job with least privileges:

| Runner | Rust checks | Bundle command | Uploaded artifact |
|---|---|---|---|
| `ubuntu-24.04` | fmt, clippy, test | `npm run tauri -- build --bundles appimage` | `.AppImage` + identity |

Use the official [Tauri Linux prerequisites](https://v2.tauri.app/start/prerequisites/#linux): Ubuntu WebKitGTK 4.1 development libraries, build-essential, curl, wget, file, libxdo-dev, libssl-dev, librsvg2-dev, and libayatana-appindicator3-dev; add squashfs-tools for artifact inspection. Record exact package versions from the actual runner. On the reference desktop also verify an unlocked Secret Service, working session D-Bus, AppImage/FUSE support (or documented extract-and-run installation), and GStreamer codecs capable of playing the chosen H.264/AAC and audio fixtures. Install only these declared prerequisites, run `npm ci` from the committed lockfile followed by the bundle command, use committed Cargo/npm locks, verify artifacts, and upload without renaming. CI success proves buildability, not runtime/keychain journeys.

- [ ] **Step 6: Write unsigned installation and smoke instructions**

For the Ubuntu reference desktop, document checksum verification, the exact warning/bypass invited testers will encounter, uninstall, app data location, keychain entry identity, and revocation. The smoke checklist is:

1. install the exact artifact;
2. connect to trusted HTTPS node;
3. login/register device;
4. navigate Chat, Stream, Library, Files; render protected covers/avatars and read an ebook;
5. play/seek HLS video, direct audio, and a multi-track audiobook using actual WebKitGTK; record its version and codec prerequisites, and exercise native-HLS or hls.js according to actual engine support;
6. create/read commentary;
7. quit fully and restart without reentering password;
8. rotate refresh under concurrent API/socket activity;
9. revoke device from browser and confirm next request/media segment/socket fails;
10. log in again, log out, restart, and confirm no credential remains.

Record OS build, package digest, node/candidate identity, start/end, each assertion, and any warning interaction. A skipped assertion makes the platform `not_run`.

- [ ] **Step 7: Run local desktop checks**

```bash
bash tests/operations/desktop_artifact_test.sh
cd frontend
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
cd src-tauri
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
```

Expected: all exit `0` on Linux; the native workflow syntax and catalog contract tests pass locally.

- [ ] **Step 8: Run native build workflow and manual smoke checkpoint**

Run the workflow on Ubuntu. Install its exact AppImage and complete the smoke checklist against the Phase 3 server build, recording one source-bound Linux rehearsal result. A runner-only build without the manual keychain/restart/revocation journey is insufficient. Missing desktop infrastructure leaves the external result `not_run` and allows local implementation review; final `desktop-linux` evidence is rerun in Phase 5 with locked server/client artifacts.

- [ ] **Step 9: Commit desktop packaging**

```bash
git add .github/workflows/desktop.yml scripts/verify-desktop-artifact.py \
  tests/operations/desktop_artifact_test.sh frontend/src-tauri/tauri.conf.json \
  frontend/src-tauri/README.md documentation/operations/desktop-beta.md ci/phase-gates.json
git commit -m "ci: build and verify unsigned desktop packages"
```

---

### Task 7: Run the Phase 3 exit gate

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `ci/phase-gates.json`
- Modify: `documentation/product/beta-feature-status.md`
- Modify: `scripts/check-product-truth.sh`

**Interfaces:**
- Produces local gates: `device-registration-boundary`, `native-auth-state`, `os-credential-adapter`, `platform-trusted-tls`, `token-safe-media`, `desktop-builds`.
- Produces external gate: `desktop-linux`. Windows/macOS gate IDs must not be direct or transitive prerequisites of initial certification.

- [ ] **Step 1: Add desktop contract assertions to product truth**

Require no active JSON secret store/TLS pinning source or dependency (allow only Task 3’s explicit legacy-file retirement helper), exact native origins in beta configuration, `keyring` 4.1.6 with `v1` enabled in the resolved dependency graph, the Ubuntu AppImage job, and `desktop-linux` as the sole required native gate. Check no deferred native gate remains a prerequisite.

- [ ] **Step 2: Run the checker and observe missing Phase 3 CI gates**

```bash
bash scripts/check-product-truth.sh
bash tests/operations/ci_gates_test.sh
```

Expected: nonzero until the catalog/workflows/product matrix agree.

- [ ] **Step 3: Wire all local gates as required CI checks**

Add backend auth/security/product integration jobs, frontend unit/build jobs, Rust fmt/clippy/test, and the Ubuntu native build job. Do not make platform build jobs `continue-on-error`; do not classify their build artifacts as runtime smoke evidence.

- [ ] **Step 4: Update product status conservatively**

Mark the desktop client implemented only after all local tests and the Linux native manual rehearsal pass; label it uncertified until final candidate evidence exists. State that packages are unsigned and invite-only and that public signing/notarization is outside beta.

- [ ] **Step 5: Run the complete Phase 3 local suite**

```bash
bash scripts/test-backend.sh auth
bash scripts/test-backend.sh security
bash scripts/test-backend.sh product
bash scripts/test-backend.sh all -count=1 -v -run 'TestHLS|TestStreamTicket|TestMediaLocator'
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
cd frontend
npm run lint
npx tsc --noEmit
npm run test:unit
npm run build
cd src-tauri
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
cd ../..
bash tests/operations/desktop_artifact_test.sh
bash scripts/check-product-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
```

Expected: all exit `0`.

- [ ] **Step 6: Verify external native evidence**

Confirm the Linux rehearsal names server source, client source/version, exact AppImage digest, Ubuntu/WebKitGTK versions, real Secret Service persistence after restart, and revocation behavior. Missing/incomplete Linux evidence is `not_run` and cannot certify. Deferred Windows/macOS records are not required. Implementation may proceed to Phase 4/5 tooling after local review; candidate certification still requires a new Linux result against the final candidate.

- [ ] **Step 7: Commit phase enforcement**

```bash
git add .github/workflows/ci.yml ci/phase-gates.json \
  documentation/product/beta-feature-status.md scripts/check-product-truth.sh
git commit -m "ci: enforce the desktop beta matrix"
```

- [ ] **Step 8: Record the review checkpoint**

Request code review focused on the public registration boundary, keychain failure handling, authority revocation, locator secrecy, and native-origin policy before Phase 5 consumes desktop artifacts.
