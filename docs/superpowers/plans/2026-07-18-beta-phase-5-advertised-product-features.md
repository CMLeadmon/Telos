# Telos Beta Phase 5: Advertised Product Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship every approved, visible non-AI Telos product capability with durable state, current authorization, real-time delivery, failure recovery, and focused browser evidence.

**Architecture:** Build features on the Phase 3 cursor/outbox/account-lifecycle contracts and Phase 4 media/library authorization boundaries. Keep PostgreSQL authoritative, use user-scoped real-time events only as delivery hints, and make each Zustand store reconcile through REST after reconnect.

**Tech Stack:** Go 1.26.5, PostgreSQL 16.14, Redis 7, Next.js 16.2.10, React 19, Zustand 5, PDF.js `pdfjs-dist` 6.1.200, EPUB.js 0.4.2, HLS.js, LiveKit, Vitest, Testing Library, Playwright

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its focused commit.
- Begin only after the Phase 4 exit gate is accepted.
- Reserve migrations `0013` through `0018` exactly as assigned in this plan; never edit earlier applied migrations.
- Use Phase 3 `PageCursor`, `Page[T]`, `EnqueueOutbox`, and account-lifecycle interfaces without parallel alternatives.
- Use Phase 4 `JellyfinAuthorizer`, `GrimmoryAuthorizer.AuthorizeBook(..., BookRead|BookManage)`, safe proxy, and audit interfaces for every affected request. Reader, progress, annotation, reply, and notification-resource resolution always call `AuthorizeBook(..., BookRead)`.
- All product mutations are durable before an event is published. Redis loss may delay UI delivery but cannot lose state.
- Every chat create/reply POST requires a stable client-generated mutation ID; retrying the same mutation returns the original durable message and never creates a second row.
- Notification and channel sockets are delivery hints. Their durable catch-up endpoints page forward by increasing sequence to a server-sampled high-water mark.
- Every potentially unbounded Phase 5 list uses the Phase 3 scoped cursor contract with default 50 and maximum 100; finite enumerations must register and test a hard cap rather than returning an unregistered array.
- Update the account-deletion retention matrix and integration test whenever this phase adds user-owned data.
- No Oracle, AI summary, AI API, AI runtime dependency, or future-AI schema is permitted.
- Add the failing backend, store/component, or Playwright test before implementation; no persistence integration test may pass by skipping.

## Entry Gate

- [ ] Phase 4 evidence identifies the exact starting commit and migration checksum set through `0012`.
- [ ] `scripts/test-backend.sh all`, frontend lint/unit/build, and the Phase 4 media/storage suite pass.
- [ ] Jellyfin item authorization and Grimmory range-safe content work against disposable fixtures.
- [ ] The product-truth baseline identifies all current inert/AI surfaces that this phase must remove or implement.

## File Responsibility Map

| Area | Files |
|---|---|
| Product truth | `scripts/check-product-truth.sh`, landing/shell/chat UI, README, normative architecture |
| Notifications/events | `backend/db/migrations/0013_notifications.sql`, `backend/notifications.go`, `backend/events.go`, notification store/components |
| Threads | `backend/db/migrations/0014_threads.sql`, `backend/chat.go`, channel change log, thread panel/composer |
| Readers/annotations | `backend/db/migrations/0015_annotations.sql`, `backend/annotations.go`, PDF/EPUB reader and annotation modules |
| My List | `backend/db/migrations/0016_my_list.sql`, `backend/mylist.go`, My List store/shelf |
| Watch Parties | `backend/db/migrations/0017_watch_parties.sql`, `backend/watchparty.go`, Watch Party store/player/panels |
| Channel administration | `backend/db/migrations/0018_channel_permission_overrides.sql`, `backend/chat.go`, channel authorization tests, working Settings forms |

---

## P5-T1: Remove AI and Oracle from the Shipped Product

**Closes:** P01, R02

**Files:**

- Create: `scripts/check-product-truth.sh`
- Create: `frontend/e2e/product-truth.spec.ts`
- Modify: `frontend/src/app/page.tsx`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/chat/ChatAside.tsx`
- Modify: `frontend/src/styles/chat.css`
- Modify: `README.md`
- Modify: `documentation/architecture/01-system-overview.md`
- Modify: `documentation/architecture/03-gateway-and-api.md`
- Modify: `documentation/architecture/04-frontend-architecture.md`
- Modify: `documentation/architecture/05-roadmap-and-licensing.md`
- Modify: `documentation/product/beta-feature-status.md`

**Script contract:** `scripts/check-product-truth.sh` scans `frontend/src/`, `backend/`, `documentation/`, `README.md`, package manifests, and Compose/configuration (excluding historical plans/specs) and exits nonzero for an Oracle/AI control, AI product claim, AI API/runtime dependency, OIDC-as-shipped claim, non-Apache Telos license claim, absolute local-only privacy claim, or listed visible inert control.

- [ ] Add the failing repository scan and browser assertions that name every current Oracle/AI and inert advertised surface.
- [ ] Remove Oracle icons, controls, filtering, styles, API scope, runtime configuration, summaries, documentation, and marketing; do not replace them with a dormant feature flag.
- [ ] Replace product copy only with approved shipped capabilities and the accurate direct-HTTPS/optional-private-network/operator-metadata-egress/encrypted-off-node-backup privacy model.
- [ ] Ensure My List and Watch Party controls remain absent until their implementing tasks add working actions; the notification bell remains absent until P5-T3.
- [ ] Run `scripts/check-product-truth.sh` and `cd frontend && npx playwright test e2e/product-truth.spec.ts --project=chromium`; expected result: both pass and no prohibited/inert surface renders.
- [ ] Commit with message `fix(product): remove AI and Oracle surfaces`.

## P5-T2: Implement the Durable Notification Service and User Event Stream

**Closes:** P03

**Files:**

- Create: `backend/db/migrations/0013_notifications.sql`
- Create: `backend/notifications.go`
- Create: `backend/notifications_test.go`
- Create: `backend/events.go`
- Create: `backend/events_test.go`
- Modify: `backend/main.go`
- Modify: `backend/outbox.go`
- Modify: `backend/auth.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`

**Interfaces:**

```go
type NotificationKind string

type NotificationInput struct {
    RecipientID   string
    ActorID       string
    Kind          NotificationKind
    ResourceType  string
    ResourceID    string
    IdempotencyKey string
    Payload       json.RawMessage
}

func CreateNotification(ctx context.Context, tx pgx.Tx, input NotificationInput) (Notification, error)

type UserEvent struct {
    Sequence     int64
    Kind         string
    ResourceType string
    ResourceID   string
    Payload      json.RawMessage
    CreatedAt    time.Time
}

type UserEventCatchUp struct {
    Items     []UserEvent
    NextAfter ChangeCursor
    HighWater ChangeCursor
    HasMore   bool
}
```

Routes are `GET /api/v1/notifications?cursor=&limit=`, `GET /api/v1/notifications/unread-count`, `PUT /api/v1/notifications/{id}/read`, `PUT /api/v1/notifications/read-all`, recipient-scoped `GET /api/v1/events?afterSequence=&throughSequence=&limit=`, and authenticated `GET /api/v1/events/ws`. A missing `throughSequence` samples the recipient's current maximum sequence once; subsequent pages retain that high-water mark and return only `afterSequence < sequence <= throughSequence` in ascending order. Kinds are `mention`, `thread_reply`, `annotation_reply`, `watch_party_invite`, and `account_security`.

- [ ] Add failing migration, recipient isolation, stable inbox pagination, monotonic sequence/high-water catch-up, unread count, read idempotency, unique idempotency-key, outbox replay, socket-gap, Redis-loss, and deletion tests.
- [ ] Upgrade the existing table with constrained kinds, immutable recipient scope, resource references/payload, unique idempotency key, and `(user_id, created_at DESC, id DESC)` plus unread indexes; add a durable recipient-filtered `user_events` log with a strictly increasing sequence and `(recipient_id, sequence)` index.
- [ ] Create notification, matching durable user-event, and `telos:user:<recipientID>` outbox delivery hint in the caller’s transaction; notification rows reference their event sequence and replay never duplicates either row.
- [ ] Wire `account_security` events for password change, session revocation, role change, and account active-state change; inactive recipients retain the event for their next authorized login and public payloads contain no credential/session token.
- [ ] Implement authenticated recipient-scoped inbox and forward catch-up REST operations plus the bounded/revocable Phase 2 user-event socket; validate nonnegative sequence bounds and never expose another user’s event, payload, or high-water value.
- [ ] Run `scripts/test-backend.sh product -run 'Test(Notification|UserEvent|UserEventCatchUp|OutboxNotification|DeleteAccountNotification|DeleteAccountUserEvent)'`; expected result: all tests pass without skips, catch-up closes an injected socket gap exactly once, and Redis loss preserves inbox/event rows.
- [ ] Commit with message `feat(notifications): add durable in-app inbox`.

## P5-T3: Add the Notification Inbox Client

**Closes:** P03

**Files:**

- Create: `frontend/src/stores/useNotificationStore.ts`
- Create: `frontend/src/stores/useNotificationStore.test.ts`
- Create: `frontend/src/components/notifications/NotificationInbox.tsx`
- Create: `frontend/src/components/notifications/NotificationItem.tsx`
- Create: `frontend/src/components/notifications/NotificationInbox.test.tsx`
- Create: `frontend/src/styles/notifications.css`
- Create: `frontend/e2e/notifications.spec.ts`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/styles/styles.css`

**Store contract:** `loadInitial`, `loadMore`, `markRead`, `markAllRead`, `catchUp`, and `reconcile` manage `items`, `nextCursor`, `unreadCount`, `lastEventSequence`, `catchUpHighWater`, `status`, and `error`.

- [ ] Add failing store/component/browser tests for pagination, badge count, sequence gaps, multi-page high-water catch-up, duplicate socket/catch-up events, deep links, mark-one/all, optimistic rollback, retry, reload, and keyboard-operable dialog behavior.
- [ ] Implement a labeled inbox dialog and server-derived unread badge; each approved notification kind links to its currently authorized source context or explains that a removed/inaccessible source is unavailable, and book-resource resolution rechecks `AuthorizeBook(..., BookRead)`.
- [ ] Merge user events by sequence plus stable notification ID/idempotency key; persist the last applied sequence and page the durable catch-up API through its sampled high-water before accepting later socket hints.
- [ ] Retain previous read state and expose retry if mark-one/all persistence fails; do not silently clear an unread item.
- [ ] Run `cd frontend && npm run test:unit -- src/stores/useNotificationStore.test.ts src/components/notifications/NotificationInbox.test.tsx && npx playwright test e2e/notifications.spec.ts --project=chromium`; expected result: all tests pass and an injected socket gap is recovered exactly once.
- [ ] Commit with message `feat(frontend): add notification inbox`.

## P5-T4: Implement One-Level Thread APIs and Durable Events

**Closes:** P02, P03

**Files:**

- Create: `backend/db/migrations/0014_threads.sql`
- Modify: `backend/chat.go`
- Modify: `backend/main.go`
- Modify: `backend/notifications.go`
- Modify: `backend/chat_test.go`
- Create: `backend/chat_integration_test.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`

**Interfaces:**

```go
type ChatMessage struct {
    ThreadRootID *string
    ClientMutationID string
    ReplyCount   int
    LastReplyAt  *time.Time
    LastReplyBy  *MessageActor
}

type ChannelReadState struct {
    LastReadMessageID string
    LastReadAt        time.Time
    UnreadCount       int
}

type CreateMessageInput struct {
    Body             string
    ClientMutationID string
}

func ListChannelRoots(ctx context.Context, channelID string, cursor *PageCursor, limit int) (Page[ChatMessage], error)
func ListThreadReplies(ctx context.Context, channelID, rootID string, cursor *PageCursor, limit int) (Page[ChatMessage], error)

type ChannelChange struct {
    Sequence  int64
    Kind      string
    MessageID string
    OccurredAt time.Time
}

type ChannelChangeCatchUp struct {
    Items     []ChannelChange
    NextAfter ChangeCursor
    HighWater ChangeCursor
    HasMore   bool
}
```

`POST /api/v1/channels/{id}/messages` and `POST /api/v1/channels/{id}/messages/{rootID}/replies` bodies require `clientMutationId`; a retry by the same author returns the original message. Other routes are channel root history, `GET /api/v1/channels/{id}/messages/{rootID}/replies?cursor=&limit=`, forward `GET /api/v1/channels/{id}/changes?afterSequence=&throughSequence=&limit=`, and monotonic `PUT /api/v1/channels/{id}/read` with a message ID from that channel. A missing `throughSequence` samples the authorized channel high-water once, and each later page returns increasing changes through that same bound. Channel listing includes server-derived unread count/read state and current change high-water.
`channel_changes` stores compact kind/message identifiers for seven days and is pruned in bounded batches. A cursor older than the retained per-channel floor returns `410 change_cursor_expired` plus the authorized current high-water, never a partial page presented as complete; the client then reloads current channel roots/open threads/read state and starts from that high-water.

- [ ] Add failing tests for root-only history, same-channel roots, reply-to-reply rejection, duplicate root/reply POST retries, cursor stability, forward sequence/high-water catch-up, seven-day bounded prune/expired-cursor full-resync signal, permission revocation, reply metadata, mentions, notification idempotency, monotonic channel reads, and unread counts including replies.
- [ ] Add `messages.thread_root_id` with database enforcement that it references a root in the same channel and index `(thread_root_id, created_at, id)`; require a client mutation ID for new writes with author-scoped uniqueness so the same author/request maps to one durable message.
- [ ] Implement root/reply cursor queries without N+1 metadata lookups; a reply cannot itself receive a reply.
- [ ] Add the durable `channel_changes` sequence log and authorized forward catch-up query; write `message.created`, `message.updated`, `message.deleted`, mention, thread-reply notification, and outbox delivery hints in the same transaction as their source mutation.
- [ ] Run `scripts/test-backend.sh product -run 'Test(Thread|ChannelRootHistory|DuplicateMessageMutation|ChannelChangeCatchUp|ReplyNotification|MessageCursor)'`; expected result: duplicate POSTs return one message, injected socket gaps close exactly once, second-level/cross-channel replies fail, and revoked users see no change history.
- [ ] Commit with message `feat(chat): add one-level threads`.

## P5-T5: Add the Thread Panel and Reply Composer

**Closes:** P02

**Files:**

- Create: `frontend/src/components/chat/ThreadPanel.tsx`
- Create: `frontend/src/components/chat/ThreadComposer.tsx`
- Create: `frontend/src/components/chat/ThreadPanel.test.tsx`
- Create: `frontend/e2e/threads.spec.ts`
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Create: `frontend/src/stores/useChatSessionStore.test.ts`
- Modify: `frontend/src/components/chat/ChatMessage.tsx`
- Modify: `frontend/src/app/(shell)/chat/page.tsx`
- Modify: `frontend/src/styles/chat.css`

**Store contract:** `openThread`, `loadMoreReplies`, `sendReply`, and `closeThread` manage `activeThreadRoot`, `threadReplies`, `threadNextCursor`, `threadStatus`, `threadDraftError`, and a stable `clientMutationId` retained with every unacknowledged draft.

- [ ] Add failing tests for opening/closing, cursor pagination, root metadata updates, duplicate POST acknowledgement/socket delivery, reconnect reconciliation, and retaining failed reply text plus its mutation ID.
- [ ] Implement a single-level panel that keeps root/channel context visible and omits reply controls from replies.
- [ ] Reconcile the durable reply page and channel read state after opening/reconnect, merge events by message ID, mark the latest visible channel message read, and render server-derived unread badges.
- [ ] Generate one client mutation ID when a root/reply draft is first submitted, reuse it for every retry until acknowledgement, clear input only after acknowledgement, and preserve the draft plus public API error on failure.
- [ ] Run `cd frontend && npm run test:unit -- src/stores/useChatSessionStore.test.ts src/components/chat/ThreadPanel.test.tsx && npx playwright test e2e/threads.spec.ts --project=chromium`; expected result: reload and a timed-out accepted POST each render the reply exactly once.
- [ ] Commit with message `feat(chat-ui): add one-level thread panel`.

## P5-T6: Add In-App PDF Reading and Format-Specific Progress

**Closes:** P05

**Files:**

- Create: `frontend/src/components/library/EpubReader.tsx`
- Create: `frontend/src/components/library/PdfReader.tsx`
- Create: `frontend/src/lib/reader.ts`
- Create: `frontend/src/lib/reader.test.ts`
- Create: `frontend/src/components/library/BookReader.test.tsx`
- Create: `frontend/e2e/pdf-reader.spec.ts`
- Modify: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/app/(shell)/library/page.tsx`
- Modify: `frontend/src/styles/library.css`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Modify: `backend/library.go`
- Modify: `backend/library_test.go`

**Interfaces:**

```ts
export type ReaderLocator =
  | { kind: "epub"; cfi: string; fraction: number }
  | { kind: "pdf"; page: number; zoom: number };
```

`validateBookProgress(format, raw)` server-validates the corresponding locator and percentage. PDF.js is pinned to `6.1.200` and dynamically loaded only for PDFs.

- [ ] Add failing backend/unit/browser tests for `AuthorizeBook(..., BookRead)` on reader/progress lookups, in-app PDF open, canvas/text rendering, page navigation, range use, validated locator bounds, progress restore, and existing EPUB behavior.
- [ ] Extract current EPUB behavior into `EpubReader`, add `PdfReader` with canvas and text layer, and dispatch by server-verified book format rather than opening another tab.
- [ ] Persist PDF page/zoom and EPUB CFI/fraction through the existing progress route; reject mixed, nonfinite, negative, or out-of-range values.
- [ ] Dynamically import PDF.js/EPUB.js only after the matching reader opens and terminate workers/listeners on close.
- [ ] Run `scripts/test-backend.sh product -run 'Test(BookProgress|PDFProgress|EPUBProgress)' && cd frontend && npm run test:unit -- src/lib/reader.test.ts src/components/library/BookReader.test.tsx && npx playwright test e2e/pdf-reader.spec.ts --project=chromium`; expected result: PDF and EPUB both read in-app, reject invalid locators, and restore progress.
- [ ] Commit with message `feat(library): add in-app PDF reader`.

## P5-T7: Implement Private and Community Annotations with Replies

**Closes:** P04, P03

**Files:**

- Create: `backend/db/migrations/0015_annotations.sql`
- Create: `backend/annotations.go`
- Create: `backend/annotations_test.go`
- Create: `backend/annotations_integration_test.go`
- Modify: `backend/main.go`
- Modify: `backend/notifications.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`

**Interfaces:**

```go
type AnnotationLocator struct {
    Kind  string
    CFI   string
    Page  int
    Rects []PDFRect
}
```

Visibility is `private` or `community`. Routes list/create by book, patch/delete by annotation, list/create replies, and moderator deletion requiring `moderate_annotations`.

- [ ] Add failing tests for private default, owner mutation, book access, EPUB CFI/PDF rectangle validation, community enumeration, reply restrictions, moderation, reply notifications, and account lifecycle.
- [ ] Add constrained annotation/reply tables and cursor indexes; grant `moderate_annotations` to Owner, Administrator, and Moderator through the migration.
- [ ] Limit selected text to 2,000 Unicode code points, notes to 10,000, and replies to 4,000; validate PDF page/normalized rectangles and bounded EPUB CFI syntax.
- [ ] Implement owner edit/delete/visibility transition, community-only replies, moderator removal with audit event, and idempotent reply notification in caller transactions; every annotation/reply/book lookup first calls `AuthorizeBook(..., BookRead)`, and notification/event payloads carry IDs and actor metadata, never selected text or note/reply bodies.
- [ ] Run `scripts/test-backend.sh product -run 'Test(Annotation|AnnotationReply|ModerateAnnotation|DeleteAccountAnnotations)'`; expected result: no cross-user private read and all lifecycle behavior passes.
- [ ] Commit with message `feat(library): add private and community annotations`.

## P5-T8: Add Annotation and Community Reply Reader UI

**Closes:** P04

**Files:**

- Create: `frontend/src/stores/useAnnotationStore.ts`
- Create: `frontend/src/stores/useAnnotationStore.test.ts`
- Create: `frontend/src/components/library/AnnotationPanel.tsx`
- Create: `frontend/src/components/library/AnnotationEditor.tsx`
- Create: `frontend/src/components/library/AnnotationPanel.test.tsx`
- Create: `frontend/src/lib/pdfSelection.ts`
- Create: `frontend/src/lib/pdfSelection.test.ts`
- Create: `frontend/e2e/annotations.spec.ts`
- Modify: `frontend/src/components/library/EpubReader.tsx`
- Modify: `frontend/src/components/library/PdfReader.tsx`
- Modify: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/styles/library.css`

**Store contract:** `create`, `update`, `remove`, and `reply` use normalized EPUB/PDF locators and preserve rollback state on failure.

- [ ] Add failing tests for private-by-default creation, explicit sharing, owner edit/delete, moderator removal, community reply, deep-link focus, and persistence rollback.
- [ ] Capture EPUB CFI selections and normalized per-page PDF rectangles; never derive authorization or visibility from client fields.
- [ ] Implement mine/community filters, explicit visibility control defaulting to private, ownership actions, reply UI, and capability-driven moderation.
- [ ] Reconcile annotations and replies through REST after notification deep links/reconnect; keep failed note/reply bodies available for retry.
- [ ] Run `cd frontend && npm run test:unit -- src/stores/useAnnotationStore.test.ts src/components/library/AnnotationPanel.test.tsx src/lib/pdfSelection.test.ts && npx playwright test e2e/annotations.spec.ts --project=chromium`; expected result: two-user coverage proves private notes never appear to the second user and replies require explicit sharing.
- [ ] Commit with message `feat(reader): add annotations and replies`.

## P5-T9: Implement Durable Authorized My List

**Closes:** P06

**Files:**

- Create: `backend/db/migrations/0016_my_list.sql`
- Create: `backend/mylist.go`
- Create: `backend/mylist_test.go`
- Create: `frontend/src/stores/useMyListStore.ts`
- Create: `frontend/src/stores/useMyListStore.test.ts`
- Create: `frontend/src/components/stream/MyListShelf.tsx`
- Create: `frontend/e2e/my-list.spec.ts`
- Modify: `backend/main.go`
- Modify: `backend/media.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/styles/stream.css`

**Interfaces:**

```go
type MediaListPage struct {
    Page[MediaListEntry]
    ListRevision int64
}

type ReorderMediaListInput struct {
    ItemID           string
    BeforeItemID     *string
    ExpectedRevision int64
}
```

Routes are cursor-paged `GET /api/v1/users/me/media-list?cursor=&limit=`, `POST /api/v1/users/me/media-list`, `DELETE /api/v1/users/me/media-list/{itemID}`, and `PUT /api/v1/users/me/media-list/order`. Every add/remove/reorder atomically increments the user's list revision. A page cursor encodes `(listRevision, position, id)`; continuing after a revision change returns `409 list_changed`, and reorder locks the list, places `itemID` immediately before `beforeItemID` (or at the end when null), maintains unique explicit integer positions, and rejects stale `expectedRevision`.

- [ ] Add failing authorization, duplicate-add, cursor stability, stale-cursor/revision, explicit reorder, concurrent reorder, missing-upstream, playback revalidation, account deletion, and optimistic rollback tests.
- [ ] Add a per-user revision row and entries with unique explicit positions plus non-authoritative title/type snapshots; add/remove/reorder under one lock and increment revision, use Phase 4's bounded `AuthorizeItems` once per page, and use `AuthorizeItem` again on insertion/playback.
- [ ] Return missing upstream entries with `available=false` while retaining their identity and individual removal; never fail the whole page.
- [ ] Implement cursor paging and explicit move-before controls in `useMyListStore`/`MyListShelf`; on `409 list_changed`, discard the stale cursor, reload from page one, retain the requested move, and retry only after user confirmation.
- [ ] Purge the user's list entries and revision row through the Phase 3 account lifecycle transaction, update the retention matrix/complete integration fixture, and prove delete/recreate cannot recover the former user's list.
- [ ] Run `scripts/test-backend.sh product -run 'Test(MyList|MyListCursor|MyListRevision|DeleteAccountMyList)' && cd frontend && npm run test:unit -- src/stores/useMyListStore.test.ts && npx playwright test e2e/my-list.spec.ts --project=chromium`; expected result: ordering is stable, stale revisions conflict safely, unauthorized items never persist, and deleted-account entries do not return.
- [ ] Commit with message `feat(stream): add durable My List`.

## P5-T10: Implement the Host-Authoritative Watch Party Service

**Closes:** P07, P03

**Files:**

- Create: `backend/db/migrations/0017_watch_parties.sql`
- Create: `backend/watchparty.go`
- Create: `backend/watchparty_test.go`
- Create: `backend/watchparty_integration_test.go`
- Modify: `backend/main.go`
- Modify: `backend/notifications.go`
- Modify: `backend/health.go`
- Modify: `backend/account_lifecycle.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`

**Interfaces:**

```go
type WatchPartyControlInput struct {
    Action          string
    PositionSeconds float64
    PlaybackRate    float64
    MediaItemID     string
    ExpectedVersion int64
}

type WatchPartyHostLease struct {
    HostID              string
    AcceptedSuccessorID *string
    ExpiresAt           time.Time
}
```

Lifecycle routes create/get/invite/accept/decline/join/leave/detach/rejoin/end plus host-transfer offer/accept/cancel/complete and expired-lease claim. `PUT /api/v1/watch-parties/{id}/host-lease` renews only the current host's lease. State routes are `GET/PUT /api/v1/watch-parties/{id}/state` and bounded `GET /api/v1/watch-parties/{id}/ws`; playback state expires 24 hours after last party activity. The client sends a host heartbeat every 5 seconds, and the server lease expires 15 seconds after the last accepted heartbeat.

- [ ] Add failing fake-clock/integration tests for media/channel authorization, durable identity, invitations, monotonic/stale versions, 5-second heartbeats, 15-second expiry, host-only controls, detach/rejoin, explicit successor offer/accept/cancel, unauthorized claim, Redis loss, and the complete account-lifecycle fixture/retention matrix for every new Watch Party user reference.
- [ ] Add party/invitation/member and durable host-successor offer tables linked to one authorized Jellyfin item, text channel, and voice channel; bind each offer/acceptance to the current host generation and invalidate it on cancel, either member leaving, any completed transfer, or party end; write invitation notifications through the outbox and purge/anonymize all user references through account lifecycle.
- [ ] Store playback state with bounded TTL and the renewable 15-second host lease in Redis; on expiry atomically pause/increment state, reject further controls from the old lease, and recover durable party/invite/successor identity after Redis loss.
- [ ] Permit voluntary transfer only from the current host to the current member who explicitly accepted the still-valid offer for that host generation; after host lease expiry permit claim only by that same accepted successor. Never elect, randomly choose, or allow an arbitrary member/administrator to claim.
- [ ] Reject participant control, heartbeats more frequent than the server bound, nonfinite/negative positions, playback rates outside `0.5–2.0`, unauthorized media changes, and stale versions; include server time, increasing version, and lease expiry in responses/events.
- [ ] Run `scripts/test-backend.sh product -run 'Test(WatchParty|WatchPartyHeartbeat|WatchPartyHostLease|WatchPartySuccessor|WatchPartyRedisRecovery|DeleteAccountWatchParties)'`; expected result: five concurrent parties work, an arbitrary member can never claim, expiry pauses playback, and Redis loss preserves durable identity/invites/successor consent.
- [ ] Commit with message `feat(stream): add host-controlled Watch Parties`.

## P5-T11: Add the Synchronized Player, Party UI, and Chat/Voice Links

**Closes:** P07

**Files:**

- Create: `frontend/src/stores/useWatchPartyStore.ts`
- Create: `frontend/src/stores/useWatchPartyStore.test.ts`
- Create: `frontend/src/components/stream/MediaPlayer.tsx`
- Create: `frontend/src/components/stream/CreateWatchPartyDialog.tsx`
- Create: `frontend/src/components/stream/WatchPartyPanel.tsx`
- Create: `frontend/src/lib/playbackSync.ts`
- Create: `frontend/src/lib/playbackSync.test.ts`
- Create: `frontend/e2e/watch-party.spec.ts`
- Modify: `frontend/src/stores/useMediaStore.ts`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/styles/stream.css`

**Interfaces:** `PlaybackAdapter` exposes `snapshot`, `apply`, and `setControlsEnabled`; `projectPosition(state, nowMs)` derives the host position from server time. The store exposes create/join/detach/rejoin/leave, `offerSuccessor`, `acceptSuccessorOffer`, `cancelSuccessorOffer`, `completeHostTransfer`, `claimExpiredHostLease`, `startHostHeartbeat`, `stopHostHeartbeat`, `openLinkedChat`, and `joinLinkedVoice`.

- [ ] Add failing fake-timer unit and two-browser tests for play/pause/seek/rate, clock skew, drift correction, stale events, detach/rejoin, heartbeat cadence, lease expiry, explicit successor offer/accept, arbitrary-member claim rejection, invitation, and linked chat/voice.
- [ ] Extract media playback behind `PlaybackAdapter`; only host actions publish expected-version controls and synchronized participant controls remain disabled with a clear label.
- [ ] Add working creation/invitation/party panels; while hosting send one lease heartbeat every 5 seconds, stop on transfer/end/logout, display the 15-second lease state, resynchronize drift over two seconds, ignore stale versions, and allow explicit independent playback while detached.
- [ ] Add host-only successor offer/cancel/complete controls and target-only accept/decline/expired-lease claim controls; never render a general “claim host” action, and preserve paused state when no accepted successor exists.
- [ ] Reuse `useChatSessionStore` and `useVoiceSessionStore` for linked communication; do not create another chat, voice, presence, or membership stack.
- [ ] Run `cd frontend && npm run test:unit -- src/stores/useWatchPartyStore.test.ts src/lib/playbackSync.test.ts && npx playwright test e2e/watch-party.spec.ts --project=chromium`; expected result: synchronized state converges, heartbeat/expiry behavior is deterministic, only the accepted successor can take over, detach/rejoin works, and chat/voice links work.
- [ ] Commit with message `feat(stream-ui): synchronize Watch Parties`.

## P5-T12: Implement Channel CRUD, Explicit Role Overrides, and Working Forms

**Closes:** P08, S03

**Files:**

- Create: `backend/db/migrations/0018_channel_permission_overrides.sql`
- Create: `backend/channel_admin_test.go`
- Modify: `backend/chat.go`
- Modify: `backend/auth.go`
- Modify: `backend/main.go`
- Create: `backend/authz_integration_test.go`
- Modify: `backend/watchparty_integration_test.go`
- Modify: `backend/account_lifecycle_test.go`
- Modify: `backend/account_lifecycle_integration_test.go`
- Modify: `documentation/operations/data-retention.md`
- Create: `frontend/src/components/settings/AdminChannelsSection.tsx`
- Create: `frontend/src/components/settings/AdminChannelsSection.test.tsx`
- Create: `frontend/e2e/channel-admin.spec.ts`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`
- Modify: `frontend/src/styles/settings.css`

**Interfaces:** `OverrideDecision` is `inherit`, `allow`, or `deny`; `ChannelRoleOverride` maps permission IDs to decisions. Admin routes provide channel create/update/delete and role-override get/put.

- [ ] Add failing backend/component/browser tests for CRUD, form draft recovery, slug/type validation, explicit-deny precedence, Owner-only bypass, audit, last-text/last-voice-channel, and active-Watch-Party `409` conflicts.
- [ ] Replace positional bit-mask storage with `(channel_id, role_id, permission_id, decision)` rows and migrate current view/send/voice meanings without widening access.
- [ ] Implement create/update/delete behind `manage_channels`, lowercase channel slugs, text/voice invariants, conflict-safe deletion, and durable audit events; enqueue affected-recipient security/admin notification intents for override changes in the same transaction.
- [ ] Make the Phase 2 authorization resolver consume explicit rows with deny precedence for every non-Owner role, including Administrator/custom roles; close affected sockets after an override change.
- [ ] Ship working Settings forms for create/edit/delete and per-role `inherit`/`allow`/`deny`; initialize them from the API, require explicit destructive confirmation, preserve failed drafts, refresh effective capabilities after success, and show stable public `403/409` errors.
- [ ] Run `scripts/test-backend.sh product -run 'Test(ChannelAdmin|ChannelOverride|ChannelDeleteWatchParty)' && cd frontend && npm run test:unit -- src/components/settings/AdminChannelsSection.test.tsx && npx playwright test e2e/channel-admin.spec.ts --project=chromium`; expected result: every rendered action changes durable state, explicit deny wins, and an active party-linked channel returns a visible `409`.
- [ ] Commit with message `feat(admin): ship channel management`.

## Phase 5 Exit Gate

- [ ] `scripts/check-product-truth.sh` passes and no AI/Oracle feature or visible inert advertised control remains.
- [ ] Migrations `0013` through `0018` pass empty-database, live-upgrade, checksum, and account-lifecycle tests.
- [ ] Notifications, threads, annotations, PDF/EPUB progress, My List, Watch Parties, and channel administration API/form integration suites pass without skips.
- [ ] Redis interruption/replay plus socket-gap catch-up cannot lose or duplicate durable product state; notification and channel sequence tests reach their sampled high-water marks.
- [ ] Private annotations remain private under direct API, pagination, event, search, deletion, and browser tests.
- [ ] Every Jellyfin/My List/Watch operation and every book/annotation operation rechecks current upstream/library authorization.
- [ ] Two-browser Watch Party tests prove 5-second heartbeat/15-second expiry, host authority, monotonic state, detach/rejoin, explicit accepted-successor transfer/claim, arbitrary-claim rejection, and existing chat/voice links.
- [ ] Frontend lint, unit/component tests, build, and Phase 5 Chromium E2E pass; Phase 6 repeats product flows across the full certification matrix.
- [ ] The common verification commands in the master plan pass.
- [ ] Stop for human review; Phase 6 starts only from this accepted commit.
