# Telos "Less Is More" Redesign — Design

Date: 2026-07-27
Branch: `pre-beta`
Status: Approved for planning

## 1. Purpose

Telos is **a community library for commentary on and storage of media**. Today
the product also ships voice rooms, synchronized Watch Parties, a notification
inbox, and a My List shelf. None of those serve that purpose, and each one
carries schema, containers, documentation, and test surface.

This redesign subtracts what does not serve the purpose, adds the one thing the
purpose demands but the product lacks (commentary on media that is not a book),
and rebuilds the presentation layer mobile-first.

### Success criteria

1. No voice, LiveKit, Watch Party, notification-inbox, or My List code, schema,
   container, dependency, document, or test remains in the repository.
2. Top-level navigation is three modules — Library, Stream, Chat — plus Settings.
3. Any book, media item, or file can carry community commentary.
4. Every authenticated route is usable on a phone in both orientations, verified
   by automated checks, not by inspection.
5. Every normative and public document describes the product that actually ships.

### Non-goals

- No AI features, endpoints, or reserved schema. Unchanged from current policy.
- No PWA, service worker, or offline mode.
- No new visual language. The synthwave and ink themes both survive with their
  palettes intact; only the token and layout system beneath them is rebuilt.
- No changes to Chat's feature set. Threads, reactions, pins, presence,
  mentions, the emoji picker, and share cards all stay.
- No re-architecture of the Jellyfin, Grimmory, ClamAV, or egress-proxy
  integrations.

## 2. Information architecture

| Surface | Owns | Route |
|---|---|---|
| Library | Books (Grimmory catalog, EPUB/PDF reader) and Files (folder tree, upload, download, delete) behind a segmented control | `/library` |
| Stream | Video and audio playback through the Jellyfin HLS proxy | `/stream` |
| Chat | Channel conversation, threads, reactions, pins, presence | `/chat` |
| Settings | Profile, security, appearance, admin | `/settings` |

Commentary is **cross-cutting**, not a tab. It attaches to items inside Library
and Stream.

`/files` becomes a client-side redirect to `/library?view=files` so existing
links, bookmarks, and share cards keep working.

## 3. Phase 2 — Removal

### 3.1 Files deleted outright

**Backend:** `voice.go`, `voice_test.go`, `watchparty.go`,
`watchparty_handlers.go`, `watchparty_integration_test.go`,
`watchparty_test.go`, `notification_handlers.go`, `mylist.go`, `mylist_test.go`.

`notifications.go` and `notifications_test.go` are **not** deleted — they are
renamed and reduced to `user_events.go` / `user_events_test.go` per §3.3.1.

**Frontend components:** `VoiceDock.tsx`, `settings/VoiceAudioSection.tsx`,
`stream/WatchPartyPanel.tsx`, `stream/CreateWatchPartyDialog.tsx`,
`stream/MediaPlayer.tsx`, `stream/MyListShelf.tsx`,
`notifications/NotificationInbox.tsx`, `notifications/NotificationInbox.test.tsx`,
`notifications/NotificationItem.tsx`.

**Frontend logic:** `hooks/useMicTest.ts`, `lib/voiceAudio.ts`,
`lib/voiceAudio.test.mjs`, `lib/playbackSync.ts`, `lib/playbackSync.test.ts`,
`stores/useVoiceSessionStore.ts`, `stores/useWatchPartyStore.ts`,
`stores/useWatchPartyStore.test.ts`, `stores/useNotificationStore.ts`,
`stores/useNotificationStore.test.ts`, `stores/useMyListStore.ts`,
`stores/useMyListStore.test.ts`.

**Styles:** `styles/voice.css`, `styles/notifications.css`.

**E2E:** `voice.spec.ts`, `voice-secure-context.spec.ts`, `watch-party.spec.ts`,
`notifications.spec.ts`, `my-list.spec.ts`.

**Infrastructure and docs:** the `livekit` service block in
`docker-compose.yml`, its Traefik TURN port mappings, `config/livekit.yaml`,
`docs/voice-turn-ports.md`, `docs/voice-turn-setup-guide.md`,
`tests/load/livekit-rooms.yaml`, the `livekit-client` dependency in
`frontend/package.json`, and the LiveKit entry in `NOTICE` and
`settings/CreditsSection.tsx`.

`stream/MediaPlayer.tsx` is safe to delete: it exists solely to drive
host/participant playback sync. The real HLS player is a separate inline
component inside `app/(shell)/stream/page.tsx`.

### 3.2 Files edited to drop references

**Backend:** `main.go`, `main_test.go`, `auth.go`, `chat.go`, `chat_test.go`,
`chat_integration_test.go`, `chat_threads.go`, `channel_admin.go`,
`channel_admin_test.go`, `account_lifecycle.go`, `annotations.go`,
`annotations_integration_test.go`, `outbox.go`, `pagination.go`, `security.go`,
`server.go`, `settings.go`, `settings_test.go`.

**Frontend:** `app/layout.tsx`, `app/page.tsx`, `app/(shell)/settings/page.tsx`,
`app/(shell)/stream/page.tsx`, `components/AppShell.tsx`,
`components/MobileNavigation.tsx`, `components/MobileNavigation.test.tsx`,
`components/settings/AdminChannelsSection.tsx`,
`components/settings/AdminChannelsSection.test.tsx`,
`components/settings/CreditsSection.tsx`, `lib/api.ts`,
`stores/useChatSessionStore.ts`, `stores/usePreferencesStore.ts`,
`styles/stream.css`, `styles/styles.css`.

**E2E:** `capabilities.spec.ts`, `product-truth.spec.ts`.

### 3.3 Migrations `0019` and `0020`

The schema work splits across two migrations because half of it must land
*before* the Go changes and half *after*:

- **`0019_user_event_idempotency.sql` — additive only.** Adds
  `user_events.idempotency_key` with a unique index and backfills it from
  `notifications`. Safe to apply while the current code is still running, which
  is what makes the guard relocation in §3.3.1 possible without a flag day.
- **`0020_remove_realtime_extras.sql` — destructive.** Every drop below. Applied
  only after all code referencing the dropped objects is gone.

`0020` is applied in this order:

1. Drop `watch_party_host_offers`, `watch_party_invitations`,
   `watch_party_members`, `watch_parties` — reverse foreign-key order.
2. Delete messages belonging to voice channels, then delete
   `channels WHERE type = 'voice'`.
3. Drop the `chk_channels_type` constraint and then the `channels.type` column.
   With one legal value remaining, the column is dead weight in every query,
   admin form, and sidebar grouping.
4. Delete the `join_voice` permission row. `role_permissions.permission_id` and
   `channel_permission_overrides.permission_id` both declare
   `REFERENCES permissions(id) ON DELETE CASCADE`, so per-role grants and any
   per-channel voice override are removed automatically. The cascade is
   intended; the migration must not add redundant explicit deletes, and the
   migration test asserts no orphaned override survives.
5. Drop the `fk_notifications_event` constraint, then `DROP TABLE notifications`.
   The idempotency guard has already moved to `user_events` in `0019` — see
   §3.3.1.
6. Narrow `chk_user_events_kind` to
   `('mention','thread_reply','annotation_reply','account_security')`.
7. Drop `media_list_entries`, then `media_lists`.
8. RBAC collapse — see §3.4.

### 3.3.1 The inbox is not a leaf — `notifications` carries the idempotency guard

`CreateNotification` is not a pure inbox writer. It does three things in one
transaction:

1. Claims an idempotency key by inserting into `notifications`, relying on that
   table's `idx_notifications_idem` unique index and `ON CONFLICT DO NOTHING`.
2. Appends the durable `user_events` row that WebSocket catch-up replays.
3. Enqueues an outbox delivery hint on `telos:user:<recipient>`.

The source comment states the ordering is deliberate: *"Claim the idempotency
key first, without an event, so a concurrent replay that loses the race never
inserts a duplicate user_event."* The notifications table **is** the
deduplication mechanism protecting the event stream.

Deleting it naively would therefore let a retried mutation append duplicate
`user_events`, double-delivering mentions and thread replies on reconnect —
a silent correctness regression in the exact subsystem this redesign claims to
preserve.

Phase 2 therefore **relocates the guard rather than deleting it**:

- `user_events` gains `idempotency_key TEXT` and a unique index, so it
  self-deduplicates.
- `notifications.go` is **renamed and reduced** to `user_events.go`, exporting
  `RecordUserEvent(ctx, tx, UserEventInput) (int64, error)` — the idempotent
  claim, the event append, and the outbox hint, with the notifications-table
  write removed.
- The inbox read surface — `ListNotifications`, `UnreadCount`, `MarkRead`,
  `MarkAllRead`, `loadNotificationByKey`, and their four HTTP handlers — is
  deleted outright. That is the part that was genuinely inbox-only.
- Callers in `chat_threads.go` (`notifyForMessageTx`), `annotations.go`
  (`CreateReply`), and `outbox.go` (security intents) switch to
  `RecordUserEvent`. Their transactional semantics are unchanged.

A migration test asserts that recording the same idempotency key twice yields
exactly one `user_events` row.

**What survives, deliberately:**

- `user_events` — this is the durable, recipient-scoped, strictly-increasing
  sequence backing **WebSocket catch-up on reconnect**, not the inbox. It is
  exercised by `e2e/ws-reconnection.spec.ts` and is reused in §5 to power unread
  badges.
- `outbox_events` — general transactional outbox with health checks
  (`outboxWarnPending`, `outboxFailAge`) and a 15-second shutdown drain. It
  carries the `telos:events:security` audit trail.

Dropping `notifications` therefore destroys **no audit data**. Its only cost is
that a member no longer sees an in-app notice when their roles change or their
password is reset; the security event is still durably recorded in
`outbox_events`.

**Data at risk (verified against the live stack, 2026-07-27):** 4 watch parties,
4 watch-party members, 0 invitations, 0 watch-party notifications, 2 voice
channels containing 0 messages, 0 My List entries, 0 annotations. Nothing of
value is lost.

### 3.4 RBAC collapse to four roles

Current permission sets:

```
Member      = send_messages, upload_files, view_channel, view_files,
              view_library, view_media  (+ join_voice)
Contributor = Member + upload_books
Librarian   = Contributor + manage_files + manage_library
```

A naive union of all three into Member would grant **every member
`manage_files`**, the right to delete and audit any other member's files. That
is an unacceptable privilege escalation.

The migration therefore:

1. Grants `upload_books` to `Member` — Member absorbs Contributor. Uploading a
   book is the correct default capability for a member of a community library.
2. Reassigns any user holding `Contributor` or `Librarian` to `Moderator`, which
   already carries `moderate_chat` and `moderate_annotations` and gains
   `manage_files` and `manage_library`. One live user (a Librarian) is affected
   and loses nothing.
3. Deletes the `Contributor` and `Librarian` roles. `role_permissions.role_id`,
   `channel_permission_overrides.role_id`, and the user-role assignment table
   all cascade on role deletion.

**Ordering is load-bearing.** Step 2 must complete before step 3. Deleting a
role first would cascade its user assignments away silently, stripping the
affected member of every capability instead of moving them.

Final roles: **Owner, Administrator, Moderator, Member.**

## 4. Phase 3 — Design foundation

The visual problem is structural, not aesthetic: breakpoints, spacing, and type
sizes are hardcoded ad-hoc across eight CSS files with no shared source. Eight
distinct `max-width` values appear across `app.css`, `landing.css`,
`library.css`, `settings.css`, and `stream.css` with no relationship to one
another.

Consolidate into `styles/tokens/`:

- **Breakpoints** as named custom media values, referenced everywhere. No raw
  pixel breakpoints outside the token file.
- **Fluid type scale** using `clamp()` so headings and body text scale
  continuously instead of stepping at arbitrary widths.
- **Spacing rhythm** on a consistent scale, replacing the current mix of
  `6px`/`8px`/`10px`/`12px`/`14px`/`16px`/`18px`/`22px`/`26px`/`48px`.
- **Touch-target primitive** guaranteeing a `44px` minimum hit area for
  interactive elements, applied to `.iconbtn`, `.chan`, `.mobile-nav-btn`,
  reaction chips, and reader controls.
- **Safe-area handling** via `env(safe-area-inset-*)` for the notch and the iOS
  home indicator.
- **`100dvh` replacing `100vh`** throughout. `100vh` is measured against the
  largest viewport on mobile browsers and is cut off by browser chrome.

Both themes keep their palettes; only the layout and scale layer beneath them
changes.

## 5. Phase 4 — Restructure

### 5.1 Library absorbs Files

`/library` gains a segmented control, `Books | Files`, persisted in the
`?view=` query parameter. Each half keeps its native browsing model — the book
catalog keeps covers, authors, and facet filters; Files keeps its folder tree,
upload, download, and delete. `view_files`, `upload_files`, and `manage_files`
capabilities are unchanged; the segment is hidden when the member lacks
`view_files`.

`/files` renders a redirect to `/library?view=files`.

Navigation drops to three modules plus Settings in both `AppShell.tsx` and
`MobileNavigation.tsx`.

### 5.2 Commentary generalized — migration `0021`

```
annotations.book_id BIGINT  →  target_type TEXT + target_id TEXT
CHECK (target_type IN ('book','media','file'))
```

Existing rows migrate as `target_type = 'book'`, `target_id = book_id::text`.
There are **zero rows**, so this is free today and expensive after the beta
opens.

Indexes are rebuilt on `(target_type, target_id, visibility, created_at DESC,
id DESC)` for community enumeration and `(user_id, target_type, target_id, ...)`
for the owner-private seek, preserving the existing keyset-pagination shape.

`visibility` (`private` | `community`), replies, author edit/delete, and
`moderate_annotations` semantics are unchanged.

A single `CommentaryPanel` component serves all three contexts: the EPUB/PDF
reader (where an annotation also carries a `locator` and `selected_text`), a
stream item, and a file. For media and files the locator is empty and the panel
behaves as a comment thread.

### 5.3 Stream page decomposition

`app/(shell)/stream/page.tsx` is over 500 lines carrying three
responsibilities. Its inline HLS player — `hls.js` with a native
`application/vnd.apple.mpegurl` fallback — is extracted to
`components/stream/HlsPlayer.tsx`, which is also where the Phase 5 mobile
controls land.

## 6. Phase 5 — Mobile

- **Bottom navigation:** three modules plus Settings. The channel drawer loses
  its voice-channel section entirely.
- **Reader:** tap zones for page turns, font size and family controls,
  commentary as a bottom sheet rather than a side rail (the current
  `.reader-side` is simply `display: none` below 820px, so mobile readers have
  no annotation access at all today), safe-area-aware chrome.
- **Player:** large touch targets, a real scrub bar with a time preview,
  Picture-in-Picture, and playback-rate control. **No gesture layer.** Screen
  brightness is not controllable from JavaScript in any mobile browser, and
  `video.volume` is read-only on iOS Safari, so a gesture layer would silently
  fail on half the target devices.
- **Composer:** stays above the on-screen keyboard using the `visualViewport`
  API rather than relying on viewport resize.
- **Modals become bottom sheets** below the small breakpoint.

## 7. Phase 6 — Narrative

Reposition rather than redact. The landing page currently sells "Threads, voice
lounges, and a durable inbox"; the README calls Telos "Discord-style
chat/voice". Both describe a product that will not exist.

Updated: landing hero and tagline (`app/page.tsx`), `README.md`, `CLAUDE.md`,
`AGENTS.md`, `documentation/product/beta-feature-status.md`,
`documentation/architecture/01`–`05`, `scripts/check-product-truth.sh`, and
`frontend/e2e/product-truth.spec.ts`.

`beta-feature-status.md` is machine-enforced product truth. Its capability
matrix must lose the voice, Watch Party, notification-inbox, and My List rows;
gain a commentary row covering books, media, and files; and its support-target
line must drop "5 concurrent Watch Parties, 25 voice participants".

`check-product-truth.sh` gains guards that fail if a voice, LiveKit, Watch
Party, notification-inbox, or My List surface reappears in shipped code.

## 8. Error handling

Unchanged in principle: upstream failures return 502/503 and surface as
`degraded` in `/api/v1/health`; handlers never fabricate catalog or media
records. Two specific cases:

- The health aggregate needs **no change**. Verified at `main.go:331-338`: it
  registers `postgres`, `redis`, `outbox`, a `storage` mount check, and upstream
  probes for `jellyfin` and `grimmory` only. There is no voice or LiveKit
  checker to remove. The `outbox` checker stays.
- Commentary on a media item whose Jellyfin record has since disappeared renders
  the comment with a non-authoritative title snapshot and a clear
  "item unavailable" state — it must not fabricate the item.

## 9. Testing

**Backend:** `go test ./...` and `go vet ./...` in the
`docker.io/library/golang:1.26.5` container. Deleted packages' tests go with
them; `chat_test.go`, `channel_admin_test.go`, and `settings_test.go` are
edited for the dropped `channels.type` column and `join_voice` permission.
Migration tests assert `0019` and `0020` are idempotent and that the RBAC
collapse preserves every live user's effective capability set.

**Frontend unit:** existing Vitest suites, minus the deleted stores. New tests
for the Library segmented control and the generalized annotation targeting.

**Mobile E2E — the new bar.** Every authenticated route (`/chat`, `/stream`,
`/library`, `/library?view=files`, `/settings`, reader, player) × `mobile-chrome`
(Pixel 7) and `mobile-safari` (iPhone 13) × portrait and landscape, asserting:

1. No horizontal overflow (`documentElement.scrollWidth <= clientWidth`).
2. Every interactive element has a ≥44px hit box.
3. Bottom navigation is reachable and not occluded.
4. The chat composer stays visible with the keyboard open.
5. Modals and bottom sheets fit within the viewport.

This replaces `mobile-layout.spec.ts`, which today checks only horizontal
overflow on the **public landing page** and covers no authenticated route.

**Guards:** `scripts/check-product-truth.sh` and `npm run lint` green before
every phase commit.

## 10. Delivery

Six phases on `pre-beta`, one commit each, reported and checkpointed
individually. Tests green before each commit. Push at the end. The live stack is
**not** redeployed as part of this work.

| Phase | Content | State |
|---|---|---|
| 1 | Checkpoint push | Done — `454d541` |
| 2 | Removal (§3) | |
| 3 | Design foundation (§4) | |
| 4 | Restructure (§5) | |
| 5 | Mobile (§6) | |
| 6 | Narrative (§7) | |

## 11. Risks

- **Migration `0019` is destructive and irreversible.** Mitigated by verifying
  live row counts first: nothing of value is present. A pre-migration
  `pg_dump` is taken regardless.
- **The RBAC collapse changes real users' roles.** Mitigated by promoting rather
  than demoting, and by a migration test asserting no live user loses an
  effective capability.
- **Dropping `channels.type` touches chat, channel admin, the sidebar, the
  mobile drawer, and admin settings simultaneously.** Contained to Phase 2 so it
  lands and is verified before any redesign work begins.
- **`user_events` must not be dropped with the inbox.** Explicitly called out
  because migration 0013 created both, making them look coupled when the former
  is WebSocket reconnection infrastructure.
- **Deleting `notifications` naively silently breaks event deduplication.** The
  table's unique index is the idempotency guard for `user_events` (§3.3.1). The
  guard must be relocated onto `user_events` in the same migration, before the
  drop. This is the single highest-risk step in Phase 2 because nothing fails
  loudly if it is missed — the regression only appears as duplicated mentions
  and thread replies after a client reconnects.
