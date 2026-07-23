# Telos Beta-Readiness Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the complete non-AI Telos product as a durable, confidential, invite-only beta that can be installed, operated, upgraded, restored, and exposed to the Internet with reproducible evidence.

**Architecture:** Keep PostgreSQL authoritative for durable state, Redis limited to rebuildable real-time state, and Jellyfin, Grimmory, LiveKit, and ClamAV behind strict container/process boundaries. Execute seven sequential remediation phases; each phase consumes only reviewed interfaces from earlier phases and ends at a blocking evidence gate. Phase 7 finishes and commits every operational release input before freezing, building, and certifying one immutable post-implementation candidate.

**Tech Stack:** Go 1.26.5, Node.js 24.18.0 LTS, Next.js 16.2.10, React 19, PostgreSQL 16.14, Redis 7, Traefik 3, Jellyfin, Grimmory, LiveKit, ClamAV, Podman/Docker Compose, Vitest, Testing Library, Playwright, k6, restic, Trivy, govulncheck, Syft

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run, use `superpowers:executing-plans`, and stop after its focused commit and evidence review. Phase 7's P7-T11 through P7-T17 are source-immutable certification/publishing exceptions: they create no source commit, keep generated state outside Git, and stop at their stated signed-evidence or approval checkpoint.
- The approved design is [2026-07-18-beta-readiness-remediation-design.md](../specs/2026-07-18-beta-readiness-remediation-design.md). If a phase plan conflicts with that design, the design wins and the discrepancy must be resolved before code changes.
- Preserve all pre-existing user changes. Phase 1 classifies and incorporates the dirty working tree; no task may discard, overwrite, or silently reformat unrelated work.
- Keep every secret in `.env` interpolation or a mode-`0600` secret file. Never write secret values to source, tests, logs, command output, release manifests, or screenshots.
- Preserve the copyleft boundary: Jellyfin and Grimmory remain separate processes reached over documented APIs. Never import, link, compile, or copy their source into `telos-core`.
- Ship no Oracle, AI summary, AI runtime, AI API, AI control, or AI marketing claim. Do not reserve a beta endpoint or schema for a future AI module.
- Ship Apache-2.0 `LICENSE`, applicable `NOTICE`, and complete Credits in both source and the application.
- Treat all beta data as durable and confidential. There is no planned reset.
- Pin Go to 1.26.5, Node.js to 24.18.0 LTS, PostgreSQL to the 16.14 patch line, and Next.js to 16.2.10. Use `overrides.postcss = "8.5.10"` and upgrade EPUB.js to 0.4.2 unless a newer reviewed lockfile resolution is required to clear a scanner gate.
- Dependency floors are pgx `v5.9.2`, go-redis `v9.6.3`, go-jose `v3.0.5`, and `x/net v0.53.0`. The committed lockfiles, not floating ranges, are authoritative.
- Authentication JSON bodies are at most 16 KiB, other JSON bodies at most 64 KiB, avatars at most 5 MiB, and file/book uploads at most 100 MiB.
- Support 100 registered users, 25 concurrent authenticated users, 10 chat/annotation events per second, 5 simultaneous Watch Parties, and 25 total voice participants.
- Meet an RPO of at most 24 hours and an RTO of at most 4 hours with encrypted off-node backups retaining 14 daily and 8 weekly generations.
- Support the current stable Chrome, Firefox, Safari, and Edge desktop releases plus current Safari on iOS and Chrome on Android.
- Use test-driven development for every behavior change. A task starts with a failing regression/acceptance test and closes only when the focused test and the phase regression set pass.
- Keep backend statement coverage at or above 70% overall and 90% for authentication, authorization, migrations, proxy boundaries, uploads, deletion, outbox, and Watch Party synchronization.
- No visible control may be inert. A control either performs its stated action, exposes a clear disabled reason, or is absent.
- Do not advance after a failed task check or phase exit gate. Record the command, exit status, and artifact path in the task commit or phase evidence document.

The version baseline was verified at plan time against the official [Go release history](https://go.dev/doc/devel/release), [Node.js release announcements](https://nodejs.org/en/blog), [PostgreSQL versioning policy](https://www.postgresql.org/support/versioning/), and the published [Next.js](https://www.npmjs.com/package/next) and [PDF.js distribution](https://www.npmjs.com/package/pdfjs-dist) package records. P7-T4 locks reviewed build inputs by digest; P7-T11 freezes and signs the final source/artifact set so later registry changes cannot alter the candidate.

## Program Dependency Flow

```text
P1 Release Baseline
  -> P2 Security & Authentication
    -> P3 Database & Data Lifecycle
      -> P4 Storage & Media Reliability
        -> P5 Advertised Product Features
          -> P6 Frontend Quality
            -> P7 Operations & Beta Certification
```

Phase order is strict. Phase 5 relies on the Phase 3 outbox and pagination contracts and the Phase 4 authorization/proxy boundaries. The accepted Phase 6 commit becomes Phase 7's immutable predecessor fixture. Phase 7 implements operations through P7-T10, freezes the resulting clean commit at P7-T11, and then runs source-immutable certification, approval, and publication tasks against that exact candidate.

## Phase Index

| Phase | Plan | Primary result |
|---|---|---|
| 1 | [Release Baseline](2026-07-18-beta-phase-1-release-baseline.md) | A clean checkout contains every release input, has truthful legal/product surfaces, and builds and boots reproducibly. |
| 2 | [Security and Authentication](2026-07-18-beta-phase-2-security-authentication.md) | Credentials stay at the gateway boundary, authorization remains live for sockets and channels, races are closed, and scanner gates are clean. |
| 3 | [Database and Data Lifecycle](2026-07-18-beta-phase-3-database-data-lifecycle.md) | Runtime privileges are minimal, migrations are immutable, durable events reconcile, deletion is complete, and backup mechanics are safe. |
| 4 | [Storage and Media Reliability](2026-07-18-beta-phase-4-storage-media-reliability.md) | Uploads are atomic and scanned, paths are confined, media access is authorized, streaming honors ranges, and service health is honest. |
| 5 | [Advertised Product Features](2026-07-18-beta-phase-5-advertised-product-features.md) | Threads, notifications, annotations, PDF reading, My List, channel management, and Watch Parties are fully implemented; AI is absent. |
| 6 | [Frontend Quality](2026-07-18-beta-phase-6-frontend-quality.md) | Supported layouts, recovery paths, accessibility, permissions, and bundle budgets are enforced. |
| 7 | [Operations and Beta Certification](2026-07-18-beta-phase-7-operations-beta-certification.md) | Operational inputs are committed before one signed candidate passes CI, browser, recovery, lifecycle, capacity, exposure, and final beta gates. |

## Finding Catalog

These IDs are stable across all phase plans and the final certification record.

### Release and Product Truth

| ID | Finding |
|---|---|
| R01 | Runtime-critical migrations, routes, development overlay, operations files, scripts, and generated artifacts are mixed into a dirty or untracked checkout. |
| R02 | Licensing, Credits, repository links, privacy claims, advertised features, OIDC status, themes, permissions, and service inventory disagree across code and normative documentation. |
| R03 | Builds use floating inputs or non-reproducible installation and lack a clean-checkout proof, release manifest, and baseline evidence. |

### Security and Authentication

| ID | Finding |
|---|---|
| S01 | The generic reverse proxy can forward `telos_session`, accept query-string sessions, and relay upstream `Set-Cookie` or unsafe headers. |
| S02 | WebSocket authorization is checked only at upgrade, so revoked, expired, disabled, or permission-reduced sessions retain live access. |
| S03 | Arbitrary channel IDs allocate subscriptions; default-channel and send-only checks bypass view denies; listings and embeds can cross authorization boundaries. |
| S04 | Invite acceptance, first-owner bootstrap, and last-Owner mutations have replay or concurrency races. |
| S05 | Body sizes, client-address trust, login limits, socket counts, write queues, deadlines, and degraded throttling are unsafe or unbounded. |
| S06 | Reachable Go and production npm vulnerabilities remain, including pgx, go-jose, go-redis, `x/net`, EPUB.js/xmldom, and PostCSS findings. |
| S07 | CORS, response headers, public errors, secret validation, random failures, username canonicalization, SSRF ranges, and log redaction do not meet an Internet-facing boundary. |
| S08 | LiveKit grants over-authorize publication and HTTP/WebSocket/outbox/upstream shutdown is not bounded or graceful. |

### Database and Data Lifecycle

| ID | Finding |
|---|---|
| D01 | `telos-core` uses the PostgreSQL schema-owner/superuser account instead of a least-privilege runtime role. |
| D02 | Migration execution has no advisory lock, checksum, contiguous-version validation, future-version rejection, or adequate startup context. |
| D03 | Security constraints, leading foreign-key indexes, database timeouts, corrected role grants, and safe query statistics are missing. |
| D04 | Authentication and history paths fan out queries; lists and searches lack stable cursors or bounded indexed access. |
| D05 | Durable mutations and Redis delivery are not joined by a transactional outbox with idempotent replay. |
| D06 | Account deletion leaves durable rows, session metadata, files, and public authorship without an explicit retention/anonymization contract. |
| D07 | Backup and restore are not atomic, encrypted off-node, retention-aware, session-invalidating, checksum/version-gated, or proven against recovery objectives. |

### Storage and Media

| ID | Finding |
|---|---|
| M01 | Upload naming and copying race, filesystem/database outcomes diverge, and valid EPUB ZIP containers are rejected or insufficiently bounded. |
| M02 | ClamAV is stopped or omitted from health, scanner I/O lacks deadlines, and uploads have no per-user quota or node free-space reserve. |
| M03 | File purpose/status filtering, symlink-safe confinement, pagination arithmetic, reconciliation, and durable audit records are incomplete. |
| M04 | Jellyfin item/library authorization, admin-token isolation, upstream header policy, and range/conditional behavior are incomplete. |
| M05 | Grimmory binary responses drop ranges, accept only `200`, and inherit timeouts that terminate legitimate long reads. |
| M06 | Internal networks block required signature/metadata updates, outbound policy is uncontrolled when opened, and readiness reports false-green service/storage state. |

### Advertised Product

| ID | Finding |
|---|---|
| P01 | Oracle/AI controls, summaries, claims, or runtime scope remain even though AI is removed from beta. |
| P02 | One-level chat threads and their channel-aware history/reply flows are absent. |
| P03 | The durable in-app inbox and all approved notification sources/read operations are absent or inert. |
| P04 | Private-by-default EPUB/PDF annotations, community sharing, replies, ownership, and moderation are absent. |
| P05 | PDF content opens outside Telos instead of using an in-app reader with durable progress. |
| P06 | My List is advertised but does not persist and revalidate authorized Jellyfin items. |
| P07 | Watch Party controls are inert and host-controlled synchronization linked to existing chat/voice is absent. |
| P08 | `manage_channels` is advertised without channel CRUD/role overrides, and custom permission grants do not consistently expose administration. |

### Frontend Quality

| ID | Finding |
|---|---|
| F01 | CSS ordering hides both mobile navigation paths and clips/overlaps the top bar, landing header, Stream, and empty states. |
| F02 | Auth transport errors silently appear anonymous; preference/message failures lose state; WebSockets do not reconnect and reconcile. |
| F03 | Clickable non-controls, dialogs, search, reader focus, keyboard behavior, reduced motion, zoom, and accessible status/error announcements are incomplete. |
| F04 | Authenticated routes ship oversized initial JavaScript and eagerly load LiveKit/media/reader code; broad Zustand subscriptions amplify renders. |
| F05 | Desktop/mobile browser coverage and responsive/visual regression evidence are incomplete. |

### Operations and Certification

| ID | Finding |
|---|---|
| O01 | Containers, secrets, networks, resource limits, root filesystems, capabilities, and logs are not hardened to an operator-ready baseline. |
| O02 | CI lacks vulnerability, secret, dependency, license, container, SBOM, reproducibility, and release-evidence gates. |
| O03 | Metrics, alerts, correlation IDs, backup/restore age, dependency/backlog monitoring, and incident runbooks are incomplete. |
| O04 | Only a partial Chromium suite runs; stateful and supported-browser/device scenarios are missing or skipped. |
| O05 | Clean installation, schema/data upgrade, rollback, and restored-node operation are not rehearsed on the candidate artifacts. |
| O06 | Approved capacity targets and host firewall/unrelated-port exposure have not been measured or certified. |

## Audit-to-Task Coverage Matrix

| Finding | Closing task(s) | Final evidence |
|---|---|---|
| R01 | P1-T1, P1-T2, P1-T5 | Clean-tree inventory and clean-checkout boot artifact |
| R02 | P1-T3, P5-T1, P7-T10, P7-T16, P7-T17 | Legal/product truth scan, Credits UI test, and signed final reconciliation |
| R03 | P1-T4, P1-T5, P7-T1, P7-T4, P7-T11, P7-T16, P7-T17 | Reproducible build, immutable staging/candidate lock, and matching tag/provenance |
| S01 | P2-T3 | Proxy boundary unit/integration suite |
| S02 | P2-T4, P2-T5 | Revocation/expiry/role-change socket tests |
| S03 | P2-T5, P5-T12 | Channel authorization matrix, override invalidation, and CRUD E2E |
| S04 | P2-T4 | Concurrent bootstrap/invite/last-Owner database tests |
| S05 | P2-T2, P2-T4, P2-T5, P2-T6, P7-T2 | Atomic limits, trusted-proxy, body deadline, queue, admission, and runtime cap tests |
| S06 | P2-T1, P7-T1 | Zero reachable Go and zero moderate/high/critical production npm findings |
| S07 | P2-T2, P2-T3, P2-T4 | Header/origin/error/SSRF/random/username/log regression suite |
| S08 | P2-T5, P2-T6, P3-T7, P7-T2 | LiveKit seat/grant, outbox drain, and graceful-stop tests |
| D01 | P3-T2 | Runtime-role privilege integration test |
| D02 | P3-T1, P7-T6, P7-T12 | Empty/concurrent/gap/checksum/future migration suite plus `0007`→`0018` lifecycle proof |
| D03 | P3-T3, P3-T4 | Schema invariant and timeout evidence |
| D04 | P3-T4, P3-T5, P3-T6 | Query-count, cursor, index-plan, and search tests |
| D05 | P3-T7 | Redis-loss/replay/idempotency outbox tests |
| D06 | P3-T8 | Retention matrix and deletion reconciliation tests |
| D07 | P3-T9, P3-T10, P7-T7, P7-T13 | Encrypted retention mechanics and signed clean-node restore drill |
| M01 | P4-T2, P4-T3, P4-T6 | Collision/interruption/EPUB/expansion regression suite |
| M02 | P4-T4, P4-T5, P4-T6, P4-T11 | Scanner deadline, quota, reserve, and readiness tests |
| M03 | P4-T1, P4-T2, P4-T6, P4-T7 | Symlink/purpose/audit/reconciliation tests |
| M04 | P2-T3, P4-T8, P4-T9 | Authorized-library proxy matrix |
| M05 | P4-T9 | `200`/`206`/conditional/long-stream tests |
| M06 | P4-T10, P4-T11, P7-T3 | Egress allowlist and full readiness/alert evidence |
| P01 | P1-T3, P5-T1 | Source/UI/API/docs absence scan |
| P02 | P5-T4, P5-T5 | Thread API, outbox, browser, and authorization tests |
| P03 | P5-T2, P5-T3, P5-T4, P5-T7, P5-T10 | Inbox plus every approved source/dedupe/read/reconnect test |
| P04 | P5-T7, P5-T8 | Locator/privacy/share/reply/moderation tests |
| P05 | P5-T6 | In-app PDF range/progress/reader tests |
| P06 | P5-T9 | My List authorization and missing-upstream tests |
| P07 | P5-T10, P5-T11 | Host authority, drift, detach/rejoin, chat/voice E2E |
| P08 | P5-T12, P6-T1 | Capability-driven channel/admin test matrix |
| F01 | P6-T2 | Mobile viewport and screenshot diffs |
| F02 | P6-T1, P6-T3, P6-T4 | Stale-capability, offline/retry/draft, and reconnect reconciliation tests |
| F03 | P6-T5, P7-T8, P7-T14 | Automated plus signed manual keyboard, focus, zoom, motion, and reader evidence |
| F04 | P6-T6 | Bundle manifest budget and render-selector tests |
| F05 | P6-T7, P7-T8, P7-T14 | Automated matrix plus signed physical-device checklist |
| O01 | P7-T2, P7-T4 | Runtime policy checks, live inspection, and digest-pinned release inputs |
| O02 | P7-T1, P7-T4, P7-T10, P7-T16, P7-T17 | Green CI security/license/SBOM and signed provenance reports |
| O03 | P4-T11, P7-T3, P7-T10, P7-T16, P7-T17 | Metrics/alert rules, delivery, incident, and final evidence |
| O04 | P6-T7, P7-T8, P7-T10, P7-T14, P7-T16, P7-T17 | No-skips stateful browser and signed physical-device reports |
| O05 | P1-T5, P7-T5, P7-T6, P7-T7, P7-T10, P7-T12, P7-T13, P7-T16, P7-T17 | Clean install, defined predecessor upgrade/rollback, and recovery records |
| O06 | P7-T9, P7-T10, P7-T15, P7-T16, P7-T17 | Capacity thresholds and external exposure report |

## Approved Product and Service Requirements

| Requirement | Owning tasks |
|---|---|
| Apache-2.0 source and release licensing | P1-T3, P7-T1, P7-T4 |
| No AI or Oracle feature surface | P1-T3, P5-T1, P7-T10 |
| One-level threads | P5-T4, P5-T5 |
| Durable in-app notifications for mentions, thread replies, annotation replies, Watch invitations, and security/admin events | P3-T7, P5-T2, P5-T3, P5-T4, P5-T7, P5-T10 |
| Private-by-default, explicitly community-shareable EPUB/PDF annotations with replies and moderation | P5-T6, P5-T7, P5-T8 |
| In-app PDF and EPUB readers with durable progress | P4-T9, P5-T6 |
| Durable authorized Jellyfin My List | P4-T8, P5-T9 |
| Host-controlled Watch Parties linked to existing chat and voice | P5-T10, P5-T11 |
| Working channel CRUD and role overrides | P5-T12, P6-T1 |
| In-app Credits for all isolated services and infrastructure dependencies | P1-T3 |
| Encrypted off-node backups, 14 daily/8 weekly, RPO ≤24h, RTO ≤4h | P3-T9, P3-T10, P7-T7, P7-T13 |
| 100 registered, 25 concurrent authenticated, 10 durable events/s, 5 Watch Parties, 25 voice participants | P7-T9, P7-T15 |
| Current desktop Chrome/Firefox/Safari/Edge and mobile iOS Safari/Android Chrome | P6-T7, P7-T8, P7-T14 |

## Shared Interface Contracts

Later phases must use these names unless a reviewed earlier task records and updates every dependent plan reference.

### Backend

```go
type CursorValue struct {
    Kind  string
    Value string
}

type PageCursor struct {
    Version    uint8
    KeyID      string
    Scope      string
    Sort       string
    FilterHash string
    Values     []CursorValue
    ID         string
    IssuedAt   time.Time
}

type CursorCodec interface {
    Encode(scope string, cursor PageCursor) (string, error)
    Decode(scope string, token string) (PageCursor, error)
}

type ChangeCursor struct {
    Sequence int64
}

type PageRequest struct {
    Scope string
    Sort  string
    Limit int
    After string
}

type Page[T any] struct {
    Items      []T
    NextCursor string
}

type SessionRegistry interface {
    Register(sessionHash string, userID string, conn RevocableConnection) func()
    RevokeSession(sessionHash string)
    RevokeUser(userID string)
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

type OutboxDrainer interface {
    Drain(ctx context.Context) error
}

type VoiceSeatLease interface {
    Commit(ctx context.Context, participantSID string) error
    Release(ctx context.Context) error
}

type VoiceSeatStore interface {
    Reserve(ctx context.Context, userID, roomID string) (VoiceSeatLease, error)
    ApplyWebhook(ctx context.Context, event livekit.WebhookEvent) error
    Reconcile(ctx context.Context, active []LiveKitParticipant) error
}

type OutboxEvent struct {
    Topic          string
    EventType      string
    AggregateType  string
    AggregateID    string
    IdempotencyKey string
    Payload        json.RawMessage
}

func EnqueueOutbox(ctx context.Context, tx pgx.Tx, event OutboxEvent) (string, error)

type CapacityLease interface {
    Renew(ctx context.Context) error
    Release(ctx context.Context) error
}

func (l *CapacityLimiter) Acquire(ctx context.Context, class, userID string) (CapacityLease, error)

type BookAction string

const (
    BookRead   BookAction = "read"
    BookManage BookAction = "manage"
)

func (a *JellyfinAuthorizer) AuthorizeItem(ctx context.Context, itemID string) (AuthorizedMediaItem, error)
func (a *JellyfinAuthorizer) AuthorizeItems(ctx context.Context, itemIDs []string) (map[string]AuthorizedMediaItem, error)
func (a *GrimmoryAuthorizer) AuthorizeBook(ctx context.Context, user *UserContext, bookID string, action BookAction) (AuthorizedBook, error)

type WatchPartyPlaybackState struct {
    PartyID           string
    MediaItemID       string
    Playing           bool
    PositionSeconds   float64
    PlaybackRate      float64
    Version           int64
    ServerTime        time.Time
    HostLeaseExpiresAt time.Time
}
```

- `CursorCodec` signs the canonical envelope through an injected active/retiring keyring. Decode rejects a wrong scope/version/sort/filter/value type, malformed value, expired cursor, unknown/retired key, or invalid constant-time HMAC. Timestamp-ordered lists use `(created_at,id)` values; My List uses `manual=(list_revision,position,id)` and returns `409 list_changed` after a reorder.
- Descending `cursor` pages retrieve older list records. Durable real-time domains expose a separate ascending `afterSequence` change feed with a sampled server high-water mark; an expired high-water returns an explicit resync response rather than silently skipping state.
- `sessionTokenFromRequest(*http.Request)` accepts only the secure `telos_session` cookie.
- `decodeJSON(http.ResponseWriter, *http.Request, any, int64)` applies the size limit before decoding and rejects trailing documents.
- `clientIP(*http.Request, []netip.Prefix)` trusts forwarding headers only when the direct peer is Traefik.
- Every potentially unbounded list endpoint accepts `cursor` and `limit` with default 50/maximum 100. Deliberately finite enumerations must appear in the Phase 3 list-policy registry with a tested hard cap; an unregistered array response fails CI.
- All API errors use `{ "error": { "code": string, "message": string, "requestId": string } }` without raw infrastructure details.

The Phase 2 proxy boundary is extended, not replaced, by later media work:

```go
type ProxyPolicy struct {
    RequestHeaders        map[string]struct{}
    ResponseHeaders       map[string]struct{}
    BuildQuery            func(url.Values) (url.Values, error)
    AllowRanges           bool
    MaxResponseBytes      int64
    ResponseHeaderTimeout time.Duration
    UpstreamReadIdle      time.Duration
    DownstreamWriteIdle   time.Duration
}
```

`BuildQuery` constructs the complete per-route upstream query from validated fields and never forwards the raw browser query. The media response allowlist is `Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges`, `ETag`, `Last-Modified`, `Cache-Control`, and `Content-Disposition`; upstream CORS, redirect, cookie, authentication, server, and hop-by-hop headers never pass.

Required security/runtime configuration is explicit and Compose-wired: exact `TELOS_PUBLIC_ORIGIN`; mode-`0600` `TELOS_CURSOR_KEYS_FILE`, `DATABASE_OBSERVER_URL_FILE`, and `TELOS_HLS_SIGNING_KEY_FILE`; stable `JELLYFIN_LIBRARY_IDS` and `GRIMMORY_LIBRARY_IDS`; and the host-wide `/run/telos/maintenance.lock` shared by backup, prune, install, upgrade, rollback, restore, and drills. Every cursor/HLS signing key decodes to at least 32 random bytes, all active/retiring keys are bytewise distinct from one another and from session secrets, and preflight rejects duplicates, examples, short values, or loose files without printing values.

### Frontend

```ts
export type ApiError = {
  kind: "http" | "network" | "timeout";
  code: string;
  message: string;
  requestId?: string;
  status?: number;
};

export type CurrentUser = {
  id: string;
  username: string;
  displayName: string;
  roles: string[];
  permissions: string[];
};
```

- `useNotificationStore`, `useAnnotationStore`, `useMyListStore`, and `useWatchPartyStore` own their named durable feature state.
- Stores expose narrow selectors; components do not subscribe to an entire store object.
- WebSocket reconnect uses bounded exponential backoff with jitter, then REST reconciliation from the last applied durable change sequence/high-water or Watch state version. Ordinary descending list cursors are never reused as forward catch-up cursors.
- Reader dependencies and LiveKit/HLS dependencies are loaded with dynamic imports only after the user enters the corresponding feature.

## Common Verification Commands

Run focused commands inside each task first. Each phase exit gate then runs:

```bash
set -euo pipefail
! grep -rn "ci""te:" documentation/ AGENTS.md
! grep -rnE "TO""DO|TB""D" documentation/ AGENTS.md
GO_TEST_IMAGE=docker.io/library/golang:1.26.5
if test -f release/images.lock; then
  GO_TEST_IMAGE="$(awk '$1 == "golang-test" { print $2 }' release/images.lock)"
  test -n "$GO_TEST_IMAGE"
fi
podman run --rm -v ./backend:/app:z -w /app "$GO_TEST_IMAGE" go test ./...
podman run --rm -v ./backend:/app:z -w /app "$GO_TEST_IMAGE" go test -race ./...
podman run --rm -v ./backend:/app:z -w /app "$GO_TEST_IMAGE" go vet ./...
(
  cd frontend
  npm ci
  npm run lint
  npm run test:unit
  npm run build
  npx playwright test
)
git diff --check
```

The negated documentation scans succeed only when they find no match, and `set -euo pipefail` prevents later commands from masking a failure. Phase 2 onward also runs `podman run --rm -v ./backend:/app:z -w /app "$GO_TEST_IMAGE" go run golang.org/x/vuln/cmd/govulncheck@v1.1.2 ./...` and `(cd frontend && npm audit --omit=dev --audit-level=moderate)`. Once P7-T4 creates `release/images.lock`, the common gate requires its reviewed `golang-test` digest rather than the pre-freeze patch tag. Phase 7 also runs its complete scanner, browser, recovery, and capacity gates.

## Program Completion Gate

- [ ] Each phase exit gate is green on its recorded accepted commit, and the complete accumulated suite is green again on the one P7-T11 frozen candidate.
- [ ] Every finding in the coverage matrix has linked machine-readable or human-readable evidence.
- [ ] A clean checkout can build, install, boot, and reach readiness without untracked input.
- [ ] Every visible non-AI product capability in the approved design passes stateful end-to-end coverage.
- [ ] Source, UI, API, runtime, marketing, and normative documentation contain no Oracle/AI feature.
- [ ] Go, npm production, secret, dependency, license, container, and SBOM gates pass.
- [ ] The supported automated browser matrix contains no unexplained skips; current iOS Safari and Android Chrome manual checks are signed.
- [ ] The clean-node encrypted restore meets RPO ≤24h and RTO ≤4h.
- [ ] Upgrade and rollback preserve the live schema and durable community data.
- [ ] Capacity and host-exposure certification meet the approved beta envelope.
- [ ] The final Apache-2.0 release manifest, Credits, SBOM, checksums, signatures/provenance, runbooks, and beta checklist all identify the P7-T11 candidate.
