# Gateway & API Integration

> Spec for AI coding agents and human developers. Statements are normative unless marked *(informative)*.

This document specifies how Telos routes traffic to its headless services, the Traefik configuration that makes routing possible, and the request-translation matrix `telos-core` implements over the headless Jellyfin and Grimmory APIs. For the service inventory and topology, see [`01-system-overview.md`](01-system-overview.md). For production deployment, see [`02-deployment.md`](02-deployment.md).

## 1. Routing model

Telos presents a single origin to every client: `https://${TELOS_DOMAIN}`. There is no second domain, no subdomain-per-service, and no split between an "app" origin and an "API" origin. Because every request — chat, voice signaling, media streaming, book catalog — is same-origin, no cross-origin response headers are required anywhere in the stack; the browser never needs to be told which origins may read a response, because there is only ever one.

Routing is declared in `config/dynamic/routes.yaml` and loaded through Traefik's file provider. Traefik does not use the Docker provider and never mounts the container-runtime socket. The edge routes only `telos-core` and LiveKit signaling; Jellyfin and Grimmory are reachable solely by `telos-core` over `telos-backend`.

Traefik v3 does not buffer response bodies by default. This matters for two traffic shapes in Telos:

- **HLS segment delivery** from `jellyfin` streams through Traefik untouched, segment by segment, rather than being accumulated in memory before forwarding.
- **HTTP Range requests** (used for seeking in audio/video playback and for partial book/asset downloads) pass through unmodified, preserving `206 Partial Content` semantics end to end.

The only buffering middleware is `upload-limits` on `telos-core`, where request bodies are capped at 100 MB. No response buffering is enabled.

## 2. Static Traefik configuration

The complete `config/traefik.yaml`, mounted read-only into the `traefik` container (see [`02-deployment.md`](02-deployment.md), Section 3):

```yaml
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: ":443"

providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true

certificatesResolvers:
  letsencrypt:
    acme:
      storage: /letsencrypt/acme.json
      httpChallenge:
        entryPoint: web
```

`entryPoints.web` redirects ordinary traffic to TLS and also answers ACME HTTP-01 challenges. The resolver's email is supplied through `TRAEFIK_CERTIFICATESRESOLVERS_LETSENCRYPT_ACME_EMAIL`; no certificate or private key is committed to the repository.

## 3. Headless API integration matrix

`telos-core` is the only service a client ever calls directly for these features. Internally, it translates each public `/api/v1/*` call into one or more requests against the headless Jellyfin or Grimmory APIs, both reachable only over `telos-backend` (never exposed to the client directly).

| Component | Telos endpoint | Internal headless request | Aggregation logic |
| --- | --- | --- | --- |
| Media Stream | `GET /api/v1/media` | Jellyfin `GET /Users/{userId}/Views` | Lists the user's available media libraries (views) as Telos media sections. |
| Media Catalog | `GET /api/v1/media/items` | Jellyfin `GET /Users/{userId}/Items` | Lists playable items within a library for the Telos media browser. |
| Media Item | `GET /api/v1/media/items/{id}` | Jellyfin `GET /Users/{userId}/Items/{id}` | Returns one real upstream item or an explicit 404/502/503; it never fabricates content. |
| Media Refresh | `POST /api/v1/media/refresh`, `GET /api/v1/media/refresh/status` (`manage_files`) | Jellyfin `POST /Library/Refresh`, `GET /ScheduledTasks[/{taskId}]` | Starts or joins one manual administrative scan, reports its progress, and clears gateway media caches only after Jellyfin reports successful completion; ordinary viewers cannot invoke it. |
| Audio Stream | `GET /api/v1/stream/audio/{id}` | Jellyfin `GET /Audio/{itemId}/stream` | Proxies a direct audio stream for the requested item. |
| Video Playback | `GET /api/v1/stream/video/{id}` | Jellyfin `GET /Videos/{itemId}/main.m3u8?PlaySessionId={sessionId}` | Proxies the HLS playlist and segment stream for adaptive video playback. |
| Digital Library | `GET /api/v1/library/books` | Grimmory `GET /api/v1/books` | Lists the book catalog for the Telos library view. |
| Library Facets | `GET /api/v1/library/facets` | *(derived, no upstream call)* | The deployed Grimmory build has no `/api/v1/books/facets` endpoint (500s) — `telos-core` derives author/category/language/format facets from the cached book list instead. |
| Book Cover | `GET /api/v1/library/books/{id}/cover` | Grimmory `GET /api/v1/media/book/{id}/thumbnail` | Proxies the cover image; the upstream response is mislabeled `application/json`, so the gateway forces `image/jpeg`. |
| Book Content | `GET /api/v1/library/books/{id}/content` | Grimmory `GET /api/v1/books/{id}/content` | Streams the raw EPUB/PDF bytes for the in-app reader or a PDF download. |
| Read Progress | `GET`/`PUT /api/v1/library/books/{id}/progress` | *(none — stored in `telos-core` Postgres)* | Grimmory is reached over a single shared admin-credential JWT (see below), so per-user reading progress cannot live upstream; it's stored locally against the Telos user id and Grimmory's numeric book id. |
| Edit Book Metadata | `PUT /api/v1/library/books/{id}/metadata` (`manage_library`) | Grimmory `PUT /api/v1/books/{id}/metadata` | Forwards only the Telos metadata whitelist with `REPLACE_WHEN_PROVIDED`, preserving every unlisted upstream field, then invalidates the Grimmory catalog cache. |
| Fetch Book Metadata | `POST /api/v1/library/books/{id}/metadata/fetch` (`manage_library`) | Grimmory `POST /api/v1/books/{id}/metadata/prospective` | Converts Grimmory's provider SSE stream into a normalized, read-only candidate list; applying values remains a separate client-side review step. |
| Replace Book Cover | `PUT /api/v1/library/books/{id}/cover` (`manage_library`) | Grimmory `POST /api/v1/books/{id}/metadata/cover/upload` | Accepts a JPEG/PNG upload or downloads a reviewed candidate cover through a 5 MB, public-address-only SSRF boundary, then sends a whitelisted multipart request upstream. |
| Delete Book | `DELETE /api/v1/library/books/{id}` (`manage_library`) | Grimmory `DELETE /api/v1/books?ids={id}` | Removes the shared book and file only after upstream success, then deletes all Telos reading-progress rows for the book and invalidates catalog caches. |

There are deliberately no `/jellyfin/*` or `/grimmory/*` catch-all proxy routes.
The gateway holds administrative upstream credentials, so forwarding arbitrary
client paths or methods would turn ordinary `view_media`/`view_library`
permission into upstream administrator access. New integrations must add a
narrow method-and-path Telos handler instead.

Book management is a shared-catalog operation, not a per-user ownership
operation. Every management route requires `manage_library`; the Librarian
role and roles holding `manage_community` receive that permission. A successful
delete affects every member and removes both the Grimmory record and its file.

Grimmory (a BookLore-derived image) does not accept a static bearer token — every request needs a JWT minted via `POST /api/v1/auth/login`, which `telos-core` performs on startup and on token expiry using `GRIMMORY_ADMIN_USER`/`GRIMMORY_ADMIN_PASSWORD` credentials, caching the resulting ~2-hour token in memory. This supersedes the OIDC federation model described for Grimmory below: Grimmory is addressed as a single admin account, and Telos-side identity/permissions (the `view_library` permission) gate access instead.

Audiobooks are not served by Grimmory. They're ordinary Jellyfin audio libraries (see the Media Stream/Catalog rows above); the Library module's audiobook shelf filters Jellyfin's reported libraries down to ones whose name contains "audiobook", since Jellyfin (verified on 10.11.11) does not reliably surface a distinct audiobook `CollectionType` through `/Users/{id}/Views`.

### 3.1 Manual media reconciliation

Media reconciliation is completion-aware and manual-only. An authorized user
starts it from the Stream page; clients must not periodically start scans or
poll scan status when no user-initiated scan is in progress. `telos-core`
identifies Jellyfin's `RefreshLibrary` scheduled task, starts the library
refresh, and monitors that task for up to five minutes. Concurrent manual
requests join the in-flight scan rather than queueing duplicate scans.

`POST /api/v1/media/refresh` returns `202 Accepted` once the scan has started or
the caller has joined an existing scan. The client then polls
`GET /api/v1/media/refresh/status`, whose `status` is one of `starting`,
`scanning`, `refreshing`, `complete`, `failed`, or `timeout`; a numeric
`progress` may be present while Jellyfin is scanning. A success indicator must
be shown only for `complete`, never merely because the initial `POST` was
accepted. Failure and timeout leave the currently displayed catalog intact and
surface an actionable error.

Jellyfin is authoritative for movies, television, audio, and audiobook catalog
membership. After a successful scan, `telos-core` deletes every
`telos:jellyfin:*` cache entry and the Stream client discards and rebuilds its
library, item, and nested-folder caches. Consequently, media deleted from the
shared storage disappears when Jellyfin's completed scan stops returning it.
If a removed folder was open, the client returns to Stream home; if a removed
item was playing, playback stops with a removal notice. Direct links to an item
that Jellyfin now reports as missing show a clear unavailable state rather than
fabricating or retaining media metadata.

## 4. Upstream credential boundary

`telos-core` uses one configured Jellyfin administrator token and one short-lived
Grimmory administrator JWT strictly for server-to-server translation. These
credentials are never returned to a browser. Telos authentication and
permissions are authoritative for client access.

OIDC federation and automatic per-service user provisioning are outside beta
scope and are not implemented in the current gateway; OIDC is not presented
anywhere as an available integration. Any future implementation must add purpose-built setup
or identity endpoints; it must not restore a public administrative catch-all
proxy.

## 5. Internet-facing HTTP security boundary

The boundary is centralized in `backend/security.go` and configured once from
the environment (`loadSecurityConfig`). All state-changing requests and
WebSocket upgrades are validated against **one exact origin**,
`TELOS_PUBLIC_ORIGIN` (scheme + IDNA-normalized host + effective port); a
default-port alias matches, but sibling subdomains, scheme downgrades, and
wrong ports do not. Production emits no CORS headers; development honors a
finite `TELOS_DEV_ORIGINS` allowlist only.

- **Request admission.** A bounded semaphore (128 in-flight) rejects excess
  requests with `503 server_busy` before any authentication, database, Redis,
  filesystem, or upstream work. Traefik applies a matching `inFlightReq`
  ceiling at the edge.
- **Body policy (`decodeJSON`).** The size limit is applied with
  `http.MaxBytesReader` *before* decoding; auth JSON is 16 KiB and other JSON
  64 KiB. Exactly one JSON document is accepted (trailing data is rejected),
  under a 15-second absolute read deadline. Streaming uploads use
  `withRenewedBodyReadDeadline` so an actively transferring client is not cut
  off by a fixed total-body deadline while a stalled one still times out.
  Traefik route classes mirror this: a 16 KiB auth-JSON class, a 64 KiB
  other-JSON class, and streaming upload/media classes that never spool a
  body at the edge (the route-wide 100 MiB buffering middleware is removed).
- **Client address trust (`clientIP`).** `X-Forwarded-For` is honored only
  when the direct peer is inside `TELOS_TRUSTED_PROXY_CIDRS` (the Traefik
  ingress subnet); otherwise the direct peer address is authoritative.
- **Response headers.** Production responses carry a strict CSP whose
  `connect-src` names exactly the public origin's `wss://host[:port]` (never
  bare `wss:`, wildcards, or a reflected origin), plus HSTS
  (`max-age=31536000; includeSubDomains`), `X-Frame-Options: DENY`,
  `nosniff`, `strict-origin-when-cross-origin`, and a restrictive
  Permissions-Policy.
- **SSRF-safe egress.** Outbound metadata/cover fetches resolve the host
  once, reject the entire answer set if any address is non-public
  (loopback/private/link-local/CGNAT/multicast, IPv4 or IPv6, including
  IPv4-mapped forms), dial exactly one validated address while preserving the
  original SNI so the transport cannot re-resolve, cap redirects at three, and
  reject scheme downgrades.
- **Error and log hygiene.** Public errors use the stable shape
  `{ "error": { "code", "message", "requestId" } }` with no raw
  database/Redis/filesystem/upstream text; health failures report only a
  status word. Secret validation prints only the offending variable name.

### 5.1 Session and upstream-proxy isolation

Session tokens are accepted from **exactly** the secure `telos_session`
cookie (`sessionTokenFromRequest`); a token supplied in a query string,
fragment, header, or as a duplicate/empty/malformed cookie is rejected, so a
credential can never be relayed to an upstream. The old `?token=` WebSocket
fallback is removed — browsers send the cookie on the same-origin upgrade.

Every Jellyfin/Grimmory relay goes through `proxyUpstream` (`backend/proxy.go`)
under an allowlist `ProxyPolicy`: it builds a brand-new upstream request from
the approved method/path, a per-route `BuildQuery` that emits only named,
validated parameters (the raw browser query and unknown/duplicate keys are
never forwarded), and injects Jellyfin/Grimmory credentials solely from server
configuration. Only explicitly allowlisted response headers return to the
browser — the media allowlist is `Content-Type`, `Content-Length`,
`Content-Range`, `Accept-Ranges`, `ETag`, `Last-Modified`, `Cache-Control`,
and `Content-Disposition`. Upstream `Set-Cookie`, CORS, `WWW-Authenticate`,
`Location`, `Server`, and every hop-by-hop header are dropped, and an upstream
redirect is refused rather than followed. `Range`/conditional headers pass
only for routes whose policy enables them.

### 5.2 Transactional authentication lifecycle

Account-lifecycle mutations are serialized so concurrency cannot corrupt them
(`backend/auth.go`):

- **Bootstrap** takes a PostgreSQL advisory lock (`pg_advisory_xact_lock`) and
  re-checks the Owner count inside the transaction, so any number of
  simultaneous bootstrap requests create exactly one Owner.
- **Invite acceptance** consumes the invite with a single conditional
  `UPDATE ... WHERE used_at IS NULL AND expires_at > NOW() RETURNING role_id`;
  only one concurrent accept wins, and the loser gets `400 invalid_invite`.
- **Last-Owner protection** locks Owner membership rows (`FOR UPDATE`) inside
  the mutation's transaction before counting, so demote/disable/delete return
  `409 last_owner` rather than ever leaving zero Owners.
- **Usernames** are canonicalized to `[a-z0-9][a-z0-9_.-]{2,31}` after ASCII
  lowercasing; whitespace (including Unicode), control characters, and
  non-ASCII runes are rejected, not trimmed. Display names remain Unicode.
- **Login throttling** is two-layer. Traefik rate-limits `/api/v1/auth/login`
  at 10/min per IP (burst 5). The gateway then reserves an atomic slot via a
  Redis Lua script that checks all three scopes — 5 per username+IP pair, 10
  per username, 20 per IP per 15 minutes — before any password verification,
  converting the reservation to a failure or releasing it on success
  (idempotently). If Redis is unavailable, a bounded in-process LRU (10k keys;
  2/pair, 3/username, 5/IP) enforces a stricter policy and returns
  `503 auth_throttle_unavailable` on exhaustion — never an unthrottled
  attempt. Unknown or malformed usernames still run one dummy password
  verification to equalize timing. Session tokens and any random material are
  generated from a checked entropy source that fails the operation on a short
  or errored read.

Integration tests run against disposable PostgreSQL and Redis via
`scripts/test-backend.sh` (they self-skip when the fixture is absent).

### 5.3 Live channel authorization and bounded WebSockets

Channel access is authorized on every operation, not just at connect
(`backend/chat.go`, `backend/realtime.go`):

- **`AuthorizeChannel`** requires the channel ID to parse as a UUID, to exist,
  and to pass `view_channel` before any action-specific permission. A
  malformed, nonexistent, or view-denied channel is reported identically as
  `404` so channel existence never leaks. The WebSocket upgrade calls it
  before allocating any subscription or presence state — there is no
  default-channel bypass — and the HTTP send/history/pins/members/reaction
  handlers call it too.
- **Live revocation.** Every socket registers in an in-memory
  `SessionRegistry` keyed by session hash and user ID. Logout, password
  change, session revoke, account disable/delete, and role/override change
  close the affected sockets immediately (WebSocket close 1008). A
  30-second revalidation loop is the fallback: it re-checks session validity,
  account status, and channel view, and closes the socket if any no longer
  holds.
- **Bounds.** At most 3 sockets per user and 100 subscribers per channel;
  inbound frames are capped at 16 KiB; each socket has a single writer
  draining a 128-deep queue with a 10-second write deadline, and a full queue
  (slow consumer) closes the socket deterministically. Session tokens are
  never read from the WebSocket URL — the cookie carries them.

The client reconnects with bounded exponential backoff plus jitter and
reconciles durable state through the REST history/members/pins fetch; a 1008
close is treated as a revocation (no reconnect; re-authenticate) rather than a
transient drop.

### 5.6 Transactional outbox

Durable mutations that also need real-time delivery write an `outbox_events`
row (migration 0010) in the same transaction as the mutation (`EnqueueOutbox`).
A bounded dispatcher (`backend/outbox.go`) claims batches of 100 with
`FOR UPDATE SKIP LOCKED`, publishes each through Redis, marks it published, and
retries failures with capped exponential backoff (max 1 min); published rows
are pruned after 7 days. Unique idempotency keys make replay safe, and a
rolled-back mutation publishes nothing. The dispatcher replaces the Phase 2
no-op `OutboxDrainer` (drained during graceful shutdown) and
`OutboxSecurityEventSink` replaces the no-op `SecurityEventSink`, so every
current security mutation — owner bootstrap, invite acceptance, account
enable/disable, and role change — records exactly one durable event within its
transaction. Phase 5 materializes these events as notifications.

### 5.5 Database invariants and query statistics

Migration `0008` backs security/lifecycle assumptions with named CHECK
constraints (canonical username, channel type, non-negative override masks,
message length, session/invite date ordering, file size/scan-status, book
progress range, JSON-object preferences, notification kind) and adds a leading
index for every foreign-key join/delete path (an automated audit asserts none
is missing). It also seeds the `manage_messages` permission — which gates
pin/moderation routes — and grants it to Owner/Administrator/Moderator, and
corrects the `manage_members` description to the shipped disable/enable
behavior (no administrative password replacement).

Query statistics use `pg_stat_statements` with `compute_query_id=on`,
`log_statement=none`, and parameter redaction. A NOLOGIN `telos_observer` role
(created by `deploy/postgres/init/002-create-telos-observer.sh` or
`scripts/provision-db-observer.sh`) receives only `pg_read_all_stats` and
read access to the statistics views; that access and the reset function are
revoked from `PUBLIC` and `telos_runtime`. The observer login
(`DATABASE_OBSERVER_URL_FILE`) is an operator credential and is never mounted
into `telos-core`, the frontend, Jellyfin, or Grimmory.

### 5.4 Voice grants and graceful shutdown

Voice tokens (`backend/voice.go`) are minted only after `AuthorizeChannel`
with the voice action and an atomic seat reservation. Each token is valid for
90 seconds and grants room join, subscribe, and **microphone-only**
publication — camera, screen share, arbitrary data, recording, ingress,
room administration, and wildcard rooms are all denied. A single Redis ZSET
(member = user ID) enforces one active seat per user and at most 25 seats
total; `Reserve` fails closed with `503 voice_capacity_unavailable` on
capacity or Redis error rather than issuing an unaccounted token. Signed
LiveKit `participant_joined` / `participant_left` webhooks (verified against
`LIVEKIT_API_SECRET`, exempt from the browser origin policy) commit and
release seats, and a reconciliation pass prunes seats whose user is not in the
authoritative active set. LiveKit itself caps the room at 25 participants.

Shutdown is staged (`backend/server.go`): a SIGINT/SIGTERM flips an admission
gate that rejects new work with `503 shutting_down`, then drains in-flight
HTTP for 30s, closes WebSockets for 10s, drains the transactional outbox for
15s (a no-op adapter until Phase 3), and closes upstream pools — exiting
nonzero if any stage overran. Compose grants `telos-core` a
`stop_grace_period` of 75s, comfortably above the 55s staged budget.
`scripts/validate-compose.sh` asserts the ingress-only ports, the 75s grace,
and the 25-participant LiveKit cap without printing any secret.
