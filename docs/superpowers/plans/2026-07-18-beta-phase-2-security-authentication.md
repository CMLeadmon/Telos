# Telos Beta Phase 2: Security and Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Telos gateway, authentication lifecycle, channel authorization, real-time connections, upstream proxies, and voice grants safe for an invite-only Internet-facing beta.

**Architecture:** Extract narrowly scoped security, authentication, proxy, and real-time boundaries from the current gateway. Authenticate only by secure cookie, centralize public errors and request policy, make security mutations transactional, register every live socket against its session, and require resource-specific authorization before allocating upstream or Redis work.

**Tech Stack:** Go 1.26.5 `net/http`, pgx/v5, go-redis/v9, Gorilla WebSocket, LiveKit protocol, Traefik middleware, PostgreSQL/Redis integration fixtures, govulncheck, npm audit

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its focused commit.
- Begin only after the Phase 1 clean-checkout gate is accepted.
- Keep session credentials cookie-only. Never add bearer/query fallback for browser sessions.
- Production is same-origin. Development origins come from a finite `TELOS_DEV_ORIGINS` allowlist.
- Channel view permission is a prerequisite for send, history, members, pins, reactions, embeds, voice, and subscriptions.
- Edge request/socket limits and route-specific body policy are applied before proxy buffering or upstream work; the core's bounded in-flight semaphore runs at handler entry before authentication, database, Redis, filesystem, or upstream work.
- Public errors contain stable codes and correlation IDs, not raw infrastructure text.
- Add failing tests before each behavior change and preserve the 90% coverage floor for the boundaries changed here.

## Entry Gate

- [ ] Phase 1 exit evidence references the exact starting commit.
- [ ] `bash scripts/verify-clean-checkout.sh --inventory-only` succeeds.
- [ ] Baseline Go tests, frontend production audit, and `govulncheck` outputs are captured so vulnerability and regression closure is measurable.
- [ ] The active `.env` is not read or printed by any test command.

## File Responsibility Map

| Area | Files |
|---|---|
| Dependency remediation | `backend/go.mod`, `backend/go.sum`, `frontend/package.json`, `frontend/package-lock.json`, `backend/Dockerfile` |
| HTTP security | `backend/security.go`, `backend/security_test.go`, `backend/errors.go`, `backend/main.go`, `config/dynamic/routes.yaml` |
| Proxy boundary | `backend/proxy.go`, `backend/proxy_test.go`, `backend/media.go`, `backend/library.go` |
| Authentication lifecycle | `backend/auth.go`, `backend/auth_test.go`, `backend/settings.go`, `backend/testutil/integration.go`, `docker-compose.test.yml`, `scripts/test-backend.sh` |
| Channel/realtime authorization | `backend/chat.go`, `backend/chat_test.go`, `backend/realtime.go`, `backend/realtime_test.go`, `backend/settings.go` |
| Voice/shutdown | `backend/voice.go`, `backend/voice_test.go`, `backend/server.go`, `backend/server_test.go`, `config/livekit.yaml`, `docker-compose.yml` |

---

## P2-T1: Remove Reachable Dependency Vulnerabilities

**Closes:** S06

**Files:**

- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Modify: `backend/Dockerfile`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Create: `documentation/operations/dependency-security-baseline.md`

**Version floor contract:**

| Package | Minimum accepted version |
|---|---|
| `github.com/jackc/pgx/v5` | `v5.9.2` |
| `github.com/redis/go-redis/v9` | `v9.6.3` |
| `github.com/go-jose/go-jose/v3` | `v3.0.5` |
| `golang.org/x/net` | `v0.53.0` |
| `epubjs` | `0.4.2` |
| `postcss` override | `8.5.10` |
| `next` | `16.2.10` |

- [ ] Record failing baseline outputs from `go run golang.org/x/vuln/cmd/govulncheck@v1.1.2 ./...` in the Go 1.26.5 container and `cd frontend && npm audit --omit=dev --json`; store only package/advisory/version data.
- [ ] Upgrade the vulnerable Go modules to the table floors or newer compatible patch versions, update affected call sites/tests, run `go mod tidy`, and inspect every direct/indirect version change rather than accepting an unexplained graph-wide update.
- [ ] Lock Next.js 16.2.10, EPUB.js 0.4.2, and the PostCSS 8.5.10 override; regenerate the npm lockfile using Node.js 24.18.0 and verify only one production version of each vulnerable transitive package remains.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 sh -lc 'go test ./... && go run golang.org/x/vuln/cmd/govulncheck@v1.1.2 ./...'`; expected result: all tests pass and there are zero reachable known vulnerabilities in Telos call paths.
- [ ] Run `cd frontend && npm ci && npm audit --omit=dev --audit-level=moderate && npm run lint && npm run test:unit && npm run build`; expected result: zero moderate/high/critical production findings and all frontend gates pass.
- [ ] Record resolved versions and scanner summaries in `dependency-security-baseline.md`, then commit with message `build: remediate beta dependency vulnerabilities`.

## P2-T2: Centralize the Internet-Facing HTTP Security Boundary

**Closes:** S05, S07

**Files:**

- Create: `backend/security.go`
- Create: `backend/security_test.go`
- Create: `backend/errors.go`
- Modify: `backend/main.go`
- Modify: `backend/library.go`
- Modify: `docker-compose.yml`
- Modify: `docker-compose.dev.yml`
- Modify: `config/dynamic/routes.yaml`
- Modify: `.env.example`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces:**

```go
type SecurityConfig struct {
    Environment        string
    PublicOrigin       *url.URL
    AllowedDevOrigins  map[string]struct{}
    TrustedProxyRanges []netip.Prefix
    MaxHeaderBytes     int
    MaxInFlightHTTP    int
    AuthJSONBytes      int64
    JSONBytes          int64
    BodyReadTimeout    time.Duration
    UploadReadIdle     time.Duration
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error
func withRenewedBodyReadDeadline(w http.ResponseWriter, r *http.Request, idle time.Duration) (io.ReadCloser, error)
func clientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, error)
func publicURLIPs(ctx context.Context, rawURL *url.URL) ([]netip.Addr, error)
func dialValidatedPublic(ctx context.Context, network, address, serverName string) (net.Conn, error)
func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string)
func securityHeaders(next http.Handler, cfg SecurityConfig) http.Handler
```

`TELOS_PUBLIC_ORIGIN` is one canonical HTTPS origin with an explicit effective
port. Production Origin checks compare scheme, IDNA-normalized host, and
effective port exactly. The rendered production CSP substitutes that same
origin's exact `wss://host[:port]` value for `<public-wss-origin>`; bare `wss:`,
wildcards, sibling subdomains, and reflected request origins are forbidden:

```text
default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline';
img-src 'self' data: blob:; media-src 'self' blob:;
connect-src 'self' <public-wss-origin>;
font-src 'self'; worker-src 'self' blob:; frame-src 'self' blob:;
object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self';
upgrade-insecure-requests
```

- [ ] Write table-driven failing tests for 16 KiB auth/64 KiB JSON/16 KiB header rejection, stalled/trickled bodies, 128-request admission saturation, trailing JSON, exact-origin HTTP/WebSocket enforcement (including sibling hosts, default-port aliases, and a malicious `wss://` endpoint), spoofed forwarding headers, DNS rebinding/mixed answers/mapped IPv6, CGNAT `100.64.0.0/10`, loopback/private/link-local/multicast IPv4 and IPv6, security headers, and sanitized public errors.
- [ ] Implement `decodeJSON` with `http.MaxBytesReader` before decoding and `http.ResponseController.SetReadDeadline` for a 15-second absolute body-read deadline, one-document enforcement, stable `body_too_large`/`request_timeout`/`invalid_json` codes, and endpoint-specific limits; expose the renewable 15-second idle-read wrapper for Phase 4 multipart uploads so an active large upload is not subject to a fixed total-body deadline, and reject work above a 128-request core semaphore before costly authentication/dependency calls.
- [ ] Accept forwarding headers only when `RemoteAddr` is inside the configured Traefik CIDR. For every external URL and redirect, resolve once, reject the entire mixed set if any answer is disallowed, dial one validated address directly while preserving the validated Host/SNI, verify the connected peer, and never let the transport re-resolve; otherwise trust the direct peer, cap redirects at three, and reject unsafe scheme changes.
- [ ] Parse and Compose-wire `TELOS_PUBLIC_ORIGIN` once, keep `TELOS_DEV_ORIGINS` only in the loopback development overlay, enforce exact normalized origins for state-changing HTTP requests and WebSocket upgrades, and render the CSP with only the exact public WSS origin; add HSTS `max-age=31536000; includeSubDomains`, frame `DENY`, `nosniff`, `strict-origin-when-cross-origin`, and the documented Permissions Policy without reflective credential CORS. Split Traefik routers into 16 KiB auth JSON, 64 KiB other JSON, streaming upload, streaming media, and no-body classes; remove the route-wide 100 MiB buffering middleware, cap edge in-flight requests at 128, and ensure upload/media routes never spool a request body before core authorization/admission.
- [ ] Replace raw database/Redis/filesystem/upstream health and API errors with stable public codes; redact cookies, authorization, invite tokens, credentials, and query strings in request logs; secret validation may print only a variable name.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race -count=1 -run 'Test(Security|DecodeJSON|HeaderLimit|HTTPAdmission|BodyReadDeadline|Origin|CSP|ClientIP|PublicURL|PinnedDial|APIError)' ./...`; expected result: stalled bodies time out, active idle-renewed reads continue, rebinding and overload fail closed, exact same-origin policy holds, and all focused tests pass without races.
- [ ] Commit with message `security: enforce the public HTTP boundary`.

## P2-T3: Isolate Gateway Sessions from Upstream Proxies

**Closes:** S01, S07, M04

**Files:**

- Create: `backend/proxy.go`
- Create: `backend/proxy_test.go`
- Create: `backend/media.go`
- Modify: `backend/main.go`
- Modify: `backend/library.go`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces:**

```go
type ProxyPolicy struct {
    RequestHeaders  map[string]struct{}
    ResponseHeaders map[string]struct{}
    BuildQuery      func(url.Values) (url.Values, error)
    AllowRanges     bool
}

func sessionTokenFromRequest(r *http.Request) (string, error)
func proxyUpstream(w http.ResponseWriter, r *http.Request, target *url.URL, policy ProxyPolicy, authorize func(context.Context) error)
```

The media response allowlist is `Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges`, `ETag`, `Last-Modified`, `Cache-Control`, and `Content-Disposition`. Hop-by-hop headers, upstream CORS, redirects, cookies, and authentication challenges are never relayed.

- [ ] Write failing tests that send `telos_session`, unrelated cookies, `Authorization`, forwarding headers, known/unknown query token parameters, duplicate query keys, and an upstream `Set-Cookie`; assert none crosses the gateway boundary and query-only sessions receive `401`.
- [ ] Make `sessionTokenFromRequest` accept exactly the secure `telos_session` cookie and reject empty, duplicate, query, fragment, header, or malformed token sources.
- [ ] Build new upstream requests from the approved method/path/body, explicit header allowlist, and a per-route `BuildQuery` that constructs only named validated upstream parameters; never forward `r.URL.RawQuery` or unknown/duplicate browser keys, and inject Jellyfin/Grimmory credentials only from server configuration.
- [ ] Strip every hop-by-hop, CORS, cookie, redirect, authentication challenge, and server-identification response header; close every upstream body on all success/error/cancel paths.
- [ ] Permit `Range`, `If-Range`, `If-None-Match`, and `If-Modified-Since` only for policies whose handler explicitly enables them; reject upstream redirects rather than following a browser-controlled location.
- [ ] Run `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test -race -count=10 -run 'Test(SessionToken|ProxyPolicy|ProxyQuery|ProxyHeaders|ProxyCancellation)' ./...`; expected result: no credential, raw/unknown query, body, or response-header leak and all upstream bodies close.
- [ ] Commit with message `security: isolate sessions from upstream proxies`.

## P2-T4: Make Authentication and Privileged Mutations Transactional

**Closes:** S02, S04, S05, S07

**Files:**

- Create: `backend/auth.go`
- Create: `backend/auth_test.go`
- Create: `backend/testutil/integration.go`
- Create: `docker-compose.test.yml`
- Create: `scripts/test-backend.sh`
- Modify: `backend/main.go`
- Modify: `backend/settings.go`
- Modify: `config/dynamic/routes.yaml`
- Modify: `.env.example`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces and policies:**

```go
type RandomSource interface {
    Read([]byte) (int, error)
}

type LoginAttempt interface {
    Complete(ctx context.Context, outcome LoginOutcome) error
}

type LoginLimiter interface {
    Reserve(ctx context.Context, username string, ip netip.Addr) (LoginAttempt, error)
}

type SecurityEventIntent struct {
    Kind       string
    ActorID    string
    SubjectID  string
    ResourceID string
}

type SecurityEventSink interface {
    Record(ctx context.Context, tx pgx.Tx, intent SecurityEventIntent) error
}

func canonicalUsername(string) (string, error)
```

- Canonical usernames match `[a-z0-9][a-z0-9_.-]{2,31}` after ASCII lowercase conversion. Any leading, trailing, embedded, or Unicode whitespace is rejected rather than trimmed; display names remain the Unicode-facing field.
- Redis-backed login allowance is 5 failures per canonical username/IP pair, 10 per canonical username, and 20 per client IP per 15 minutes.
- Traefik allows 10 login requests per IP per minute with burst 5.
- Redis loss activates a bounded in-process LRU of 10,000 keys with limits of 2 per pair, 3 per username, and 5 per IP per 15 minutes; exhaustion returns `503 auth_throttle_unavailable`, never an unrestricted attempt.
- A Redis Lua transaction atomically checks all three failure counters, creates one
  expiring in-flight reservation in all three scopes, and returns one opaque
  attempt ID before password work starts. `Complete(failure)` atomically converts
  only that reservation to failures; `Complete(success)` releases it and resets
  prior failures without deleting other in-flight reservations. Completion is
  idempotent and abandoned reservations expire after 30 seconds. The degraded LRU
  implements the identical transition under one mutex.

- [ ] Build the disposable PostgreSQL/Redis test harness, then add barrier-synchronized failing tests: 20 bootstrap requests yield one Owner, 20 accepts of one invite yield one user, concurrent Owner demotion/deletion cannot leave zero Owners, and 100 simultaneous login reservations never admit more than any pair/username/IP capacity in Redis or degraded LRU mode.
- [ ] Put bootstrap/invite acceptance and Owner removal/deactivation/deletion into locked transactions: advisory-lock bootstrap, atomically consume one valid invite, lock role membership, roll back partial work, and return `409 last_owner` rather than permitting zero active Owners.
- [ ] Implement strict no-whitespace username canonicalization, full cryptographic reads/failures, generic `invalid_credentials`, and one dummy password verification for an unknown user; cover malformed identity, partial randomness, and response-time distribution.
- [ ] Implement atomic reserve/complete Lua scripts plus the mutex-protected bounded LRU using the trusted address from P2-T2; reserve before password verification, expire abandoned attempts, make completion idempotent, preserve concurrent reservations on success, and never log the username-plus-IP composite key.
- [ ] Issue bounded `Secure`/`HttpOnly`/`SameSite=Strict`/`Path=/` cookies, expose sorted effective `permissions` in `/api/v1/auth/me`, rotate/revoke affected sessions, and pass typed transaction-local `SecurityEventIntent` values to an injected sink that Phase 3 implements with the outbox and Phase 5 materializes as notifications.
- [ ] Run `bash scripts/test-backend.sh auth -race -count=20 -run 'Test(Bootstrap|InviteAcceptance|LastOwner|LoginLimiterConcurrent|LoginLimiterDegraded|CanonicalUsername|SessionMutation)'`; expected result: the synchronized concurrency, replay, atomic limiter, no-whitespace canonicalization, cookie, and session mutation cases pass on every repetition.
- [ ] Commit with message `security: serialize authentication lifecycle mutations`.

## P2-T5: Enforce Live Channel Authorization and Bounded WebSockets

**Closes:** S02, S03, S05, S08

**Files:**

- Create: `backend/chat.go`
- Create: `backend/chat_test.go`
- Create: `backend/realtime.go`
- Create: `backend/realtime_test.go`
- Modify: `backend/main.go`
- Modify: `backend/settings.go`
- Modify: `backend/library.go`
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `documentation/architecture/03-gateway-and-api.md`

**Interfaces and limits:**

```go
type ChannelAction string

const (
    ChannelView  ChannelAction = "view"
    ChannelSend  ChannelAction = "send"
    ChannelVoice ChannelAction = "voice"
    ChannelAdmin ChannelAction = "admin"
)

func AuthorizeChannel(ctx context.Context, user *UserContext, channelID string, action ChannelAction) error

type RevocableConnection interface {
    CloseWithCode(code int, reason string)
}

type SessionRegistry interface {
    Register(sessionHash string, userID string, conn RevocableConnection) func()
    RevokeSession(sessionHash string)
    RevokeUser(userID string)
}
```

- Maximum 3 WebSockets per user, 32 channel subscriptions per socket, and 100 subscribers per channel.
- Maximum inbound WebSocket frame is 16 KiB; outbound queue depth is 128; write deadline is 10 seconds.
- Ping interval is 25 seconds, pong deadline is 60 seconds, and authorization fallback revalidation is at most 30 seconds.

- [ ] Write a failing authorization matrix for invalid/nonexistent UUIDs, explicit role denies, the seeded default channel, hidden channel listings, send-without-view, pin/reaction/member/history operations, cross-channel messages, cross-module embeds, and voice channel access.
- [ ] Resolve/validate a channel and `ChannelView` before creating Redis subscriptions, presence entries, queues, or goroutines; filter channel lists server-side and remove all default-channel bypass behavior.
- [ ] Require view plus action-specific permission for every channel operation; verify referenced messages belong to the path channel and verify embedded file/library/media resources with their own current permission before storing or rendering them.
- [ ] Implement bounded registries, schema-versioned messages, read limits, a single-writer queue, deadlines/heartbeat, unknown/oversized-message rejection, deterministic slow-consumer close, and exactly-once Redis/presence cleanup.
- [ ] Register each socket by session hash and user ID, directly close it on logout, password rotation, session revoke, account disable/delete, and role/override change, and revalidate session expiry/account/channel permission every 30 seconds.
- [ ] Run `bash scripts/test-backend.sh realtime`; expected result: the complete permission, revocation, cap, backpressure, Redis-failure, and cleanup suite passes under `-race` with no goroutine growth after disconnect.
- [ ] Commit with message `security: enforce live channel authorization`.

## P2-T6: Restrict Voice Grants and Drain the Server Safely

**Closes:** S05, S08

**Files:**

- Create: `backend/voice.go`
- Create: `backend/voice_test.go`
- Create: `backend/server.go`
- Create: `backend/server_test.go`
- Create: `scripts/validate-compose.sh`
- Create: `tests/fixtures/compose.env`
- Modify: `backend/main.go`
- Modify: `config/livekit.yaml`
- Modify: `docker-compose.yml`
- Modify: `.env.example`
- Modify: `documentation/architecture/03-gateway-and-api.md`
- Modify: `documentation/operations/repository-baseline.md`

**Interfaces:**

```go
type VoiceSeatLease interface {
    Commit(ctx context.Context, participantSID string) error
    Release(ctx context.Context) error
}

type VoiceSeatStore interface {
    Reserve(ctx context.Context, userID, roomID string) (VoiceSeatLease, error)
    ApplyWebhook(ctx context.Context, event livekit.WebhookEvent) error
    Reconcile(ctx context.Context, active []LiveKitParticipant) error
}

type OutboxDrainer interface {
    Drain(ctx context.Context) error
}
```

**Policies:**

- LiveKit tokens grant room join, subscribe, and microphone publication only.
- Tokens deny video, screen share, arbitrary data publication, hidden recording, ingress, egress, room administration, and wildcard room access.
- Voice capacity is 25 participants across Telos rooms; the gateway also enforces per-user one active voice room. One Redis Lua operation reserves both the global seat and user-room key before signing a 90-second token. Uncommitted reservations expire after two minutes, always after the token plus clock-skew margin; committed seats expire after 90 seconds unless renewed, and release is idempotent.
- Signed LiveKit `participant_joined`, `participant_left`, and `room_finished` webhooks commit/release leases. Startup and a 30-second loop reconcile Redis against a bounded server-side LiveKit room/participant listing; webhook replay, reordering, loss, and gateway crash may under-admit temporarily but may never exceed 25 active/reserved seats.
- HTTP server limits are `ReadHeaderTimeout=5s`, `MaxHeaderBytes=16 KiB`, `IdleTimeout=120s`, and no global `WriteTimeout`; JSON/control handlers use 15-second request contexts and bounded upstream control calls use 30 seconds. Traefik applies a 128-request in-flight ceiling plus bounded public-entrypoint request-read, response-write, idle, keepalive, and graceful-close settings; route-specific body policy preserves long streaming while bounding slow clients.
- Shutdown order is stop admission, drain normal HTTP for 30 seconds, close WebSockets for 10 seconds, drain outbox work for 15 seconds, close upstream bodies/pools, then exit nonzero if any stage exceeded its deadline.
- Compose sets `stop_grace_period: 75s` for `telos-core`, exceeding the 55-second staged shutdown budget plus cleanup margin.
- `tests/fixtures/compose.env` contains syntactically valid, obviously nonproduction values only. `scripts/validate-compose.sh` always supplies that fixture explicitly, captures rendered configuration in a mode-`0600` temporary file, emits check names/statuses rather than values, and removes the file on every exit; no plan command auto-loads or prints the operator's `.env`.

- [ ] Write failing decoded-JWT, token-expiry/reservation-expiry, and barrier-synchronized seat tests, then generate 90-second tokens only after current channel authorization and an atomic seat reservation using the stable channel UUID; assert a token cannot outlive its reservation, 100 concurrent requests admit at most 25, one user cannot reserve two rooms, and grants remain microphone-only with no data/video/screen/admin/wildcard permission.
- [ ] Configure LiveKit room/participant limits, signed webhooks, bounded participant listing, and internal administrative/HTTP interfaces behind Traefik/internal networks; preserve only the documented TURN/media ports.
- [ ] Construct `http.Server` explicitly with the header-byte/header-time/idle limits, entry semaphore, and per-handler contexts; configure/test matching bounded Traefik entrypoint/in-flight/keepalive controls while stream handlers rely on cancellation/range policy rather than a short global write timeout.
- [ ] Implement idempotent reservation commit/release, signed webhook replay handling, expiry renewal, and startup/30-second authoritative reconciliation; Redis/Lua failure returns `503 voice_capacity_unavailable` before signing rather than issuing an unaccounted token.
- [ ] Implement signal-driven staged shutdown using the realtime registry and injected `OutboxDrainer`; supply a tested no-op adapter until P3-T7 replaces it, reject new upgrades/uploads/voice reservations with `503 shutting_down`, close all bodies/pools, and set the Compose grace period to 75 seconds.
- [ ] Run `bash scripts/test-backend.sh security -race -count=10 -run 'Test(VoiceGrant|VoiceSeat|VoiceWebhook|VoiceReconcile|ServerShutdown|SlowRequest|HTTPAdmission)'` and `bash scripts/validate-compose.sh --env-file tests/fixtures/compose.env --checks core-ingress,stop-grace`; expected result: capacity never exceeds 25, public/core admission and transport bounds hold, webhook loss/replay converges, every shutdown stage drains or fails on deadline, rendered `telos-core.stop_grace_period` is at least 75 seconds, and no secret value is printed.
- [ ] Commit with message `security: restrict voice and drain gateway safely`.

## Phase 2 Exit Gate

- [ ] `govulncheck ./...` reports zero reachable known Go vulnerabilities.
- [ ] `npm audit --omit=dev --audit-level=moderate` reports zero moderate/high/critical production findings.
- [ ] Browser sessions work only through the secure cookie and no upstream request/response can carry Telos cookies or upstream cookies.
- [ ] Oversized headers/bodies, stalled edge/core clients, edge/core admission exhaustion, pre-admission upload spooling, spoofed forwarding headers, non-exact origins/WSS CSP destinations, DNS rebinding/unsafe SSRF destinations, whitespace/malformed usernames, random-source failures, and raw infrastructure errors are covered by passing tests.
- [ ] Concurrent bootstrap/invite/last-Owner tests pass repeatedly against disposable PostgreSQL.
- [ ] Redis and degraded-LRU login reservations remain atomic at pair/username/IP limits under synchronized concurrency.
- [ ] Invalid, nonexistent, denied, or hidden channels allocate no Redis/socket resources; resource-specific embeds are authorized.
- [ ] Session/account/permission changes close affected sockets immediately and fallback revalidation is at most 30 seconds.
- [ ] WebSocket caps, queues, deadlines, and graceful drain pass under `-race`.
- [ ] LiveKit tokens grant microphone audio only; expiring atomic seats, signed webhooks, and authoritative reconciliation enforce the 25-participant beta ceiling.
- [ ] Rendered Compose grants `telos-core` at least 75 seconds to complete its staged shutdown.
- [ ] The common verification commands in the master plan pass.
- [ ] Stop for human review; Phase 3 starts only from this accepted commit.
