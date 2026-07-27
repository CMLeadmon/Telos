# Telos Less-Is-More — Phase 2 (Removal) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove voice rooms, Watch Party, the notification inbox, and My List from Telos entirely — code, schema, containers, dependencies, documentation, and tests — while preserving WebSocket catch-up correctness and every live user's effective capabilities.

**Architecture:** Subtract before redesigning. All Go and TypeScript references are removed first so the application runs correctly against the old (wider) schema; only then does the destructive migration drop the now-unreferenced objects. The one exception is the `user_events` idempotency guard, which must be relocated by an additive migration *before* the code that depends on it changes.

**Tech Stack:** Go 1.26.5 (gateway, single `main` package under `backend/`), PostgreSQL 16.14, Redis 7, Next.js 16.2.10 static export + React 19 + Zustand, Vitest, Playwright, podman + podman-compose.

## Global Constraints

- **Never edit an applied migration file.** `backend/migrations.go` checksums every applied migration and halts startup with `errMigrationChanged` ("applied migration history changed"). Schema changes are **additive-only**: create new files, never modify `0001`–`0018`.
- Migration filenames must match `^([0-9]{4})_([a-z0-9_]+)\.sql$`, be contiguous from `0001`, and have no duplicate versions. Migrations are embedded by `//go:embed db/migrations/*.sql` at `backend/main.go:81` — new files are picked up automatically, no registration needed.
- **Go is not installed on the host.** Run all Go commands in a container: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 <cmd>`.
- **The host uses podman, not docker.**
- **A local `go build` fails unless `backend/out/` exists** (it is `//go:embed all:out`). Use `go test ./...` and `go vet ./...`, which do not require it, or let the Dockerfile populate it.
- **Copyleft boundary:** never link or compile Jellyfin or Grimmory code into the Go gateway. HTTP APIs across container boundaries only.
- All credentials come from `.env` interpolation. Never hardcode secrets.
- Upstream failures return 502/503 and surface as `degraded` in `/api/v1/health`. Handlers must never fabricate catalog or media records.
- Telos is licensed Apache-2.0. No other license claim may appear.
- Do not run `podman-compose up` against the live stack as part of this phase. The live stack is not redeployed here.
- Frontend: this Next.js version is newer than training data — consult `node_modules/next/dist/docs/` before nontrivial Next.js work.

---

## File Structure

**New files:**
- `backend/db/migrations/0019_user_event_idempotency.sql` — additive: moves the idempotency guard onto `user_events`.
- `backend/db/migrations/0020_remove_realtime_extras.sql` — destructive: every drop plus the RBAC collapse.
- `backend/user_events.go` — the reduced successor to `notifications.go`. Owns `RecordUserEvent`: idempotent claim, durable event append, outbox delivery hint.
- `backend/user_events_test.go` — successor to `notifications_test.go`.

**Deleted outright:**
- Backend: `voice.go`, `voice_test.go`, `watchparty.go`, `watchparty_handlers.go`, `watchparty_integration_test.go`, `watchparty_test.go`, `notification_handlers.go`, `mylist.go`, `mylist_test.go`, `notifications.go`, `notifications_test.go`.
- Frontend components: `VoiceDock.tsx`, `settings/VoiceAudioSection.tsx`, `stream/WatchPartyPanel.tsx`, `stream/CreateWatchPartyDialog.tsx`, `stream/MediaPlayer.tsx`, `stream/MyListShelf.tsx`, `notifications/NotificationInbox.tsx`, `notifications/NotificationInbox.test.tsx`, `notifications/NotificationItem.tsx`.
- Frontend logic: `hooks/useMicTest.ts`, `lib/voiceAudio.ts`, `lib/voiceAudio.test.mjs`, `lib/playbackSync.ts`, `lib/playbackSync.test.ts`, `stores/useVoiceSessionStore.ts`, `stores/useWatchPartyStore.ts`, `stores/useWatchPartyStore.test.ts`, `stores/useNotificationStore.ts`, `stores/useNotificationStore.test.ts`, `stores/useMyListStore.ts`, `stores/useMyListStore.test.ts`.
- Styles: `styles/voice.css`, `styles/notifications.css`.
- E2E: `voice.spec.ts`, `voice-secure-context.spec.ts`, `watch-party.spec.ts`, `notifications.spec.ts`, `my-list.spec.ts`.
- Infra/docs: `config/livekit.yaml`, `docs/voice-turn-ports.md`, `docs/voice-turn-setup-guide.md`, `tests/load/livekit-rooms.yaml`.

**Modified:** `backend/main.go`, `main_test.go`, `chat.go`, `chat_test.go`, `chat_integration_test.go`, `chat_threads.go`, `channel_admin.go`, `channel_admin_test.go`, `account_lifecycle.go`, `annotations.go`, `annotations_integration_test.go`, `outbox.go`, `pagination.go`, `security.go`, `server.go`, `settings.go`, `settings_test.go`, `auth.go`; `frontend/src/app/layout.tsx`, `app/page.tsx`, `app/(shell)/settings/page.tsx`, `app/(shell)/stream/page.tsx`, `components/AppShell.tsx`, `components/MobileNavigation.tsx`, `components/MobileNavigation.test.tsx`, `components/settings/AdminChannelsSection.tsx`, `components/settings/AdminChannelsSection.test.tsx`, `components/settings/CreditsSection.tsx`, `lib/api.ts`, `stores/useChatSessionStore.ts`, `stores/usePreferencesStore.ts`, `styles/stream.css`, `styles/styles.css`; `frontend/e2e/capabilities.spec.ts`, `product-truth.spec.ts`; `docker-compose.yml`, `frontend/package.json`, `NOTICE`, `scripts/check-product-truth.sh`.

**Task order rationale:** Task 1 (guard relocation) is first because it is the only step where a mistake is silent. Tasks 2–5 remove backend features. Task 6 removes `channels.type`. Tasks 7–9 remove frontend features. Task 10 removes infrastructure. Task 11 applies the destructive migration once nothing references its targets. Task 12 re-arms the product-truth guard.

---

### Task 1: Relocate the idempotency guard onto `user_events`

The riskiest step in the phase. `CreateNotification` currently claims an idempotency key by inserting into `notifications` (`idx_notifications_idem`, `ON CONFLICT DO NOTHING`) *before* appending a `user_events` row. That ordering is what stops a retried mutation from double-appending an event. Dropping `notifications` without moving the guard produces duplicated mentions and thread replies on WebSocket reconnect — with nothing failing loudly.

**Files:**
- Create: `backend/db/migrations/0019_user_event_idempotency.sql`
- Create: `backend/user_events.go`
- Create: `backend/user_events_test.go`
- Delete: `backend/notifications.go`, `backend/notifications_test.go`
- Modify: `backend/chat_threads.go:288-310`, `backend/annotations.go:289-300`, `backend/outbox.go:71-90`

**Interfaces:**
- Produces: `RecordUserEvent(ctx context.Context, tx pgx.Tx, in UserEventInput) (int64, error)` returning the assigned `user_events.sequence`; and `type UserEventInput struct { RecipientID, ActorID, Kind, ResourceType, ResourceID, IdempotencyKey string; Payload json.RawMessage }`. Returns the existing sequence on a duplicate key rather than an error.
- Consumes: `insertUserEvent` and `EnqueueOutbox`, both already present.

- [ ] **Step 1: Write the additive migration**

Create `backend/db/migrations/0019_user_event_idempotency.sql`:

```sql
-- Migration 0019: move the notification idempotency guard onto user_events so
-- the event stream self-deduplicates once the notifications table is dropped.
-- Additive only: safe to apply while the pre-removal code is still running.

ALTER TABLE user_events ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

-- Backfill from the notifications rows that own each event.
UPDATE user_events ue
SET    idempotency_key = n.idempotency_key
FROM   notifications n
WHERE  n.event_sequence = ue.sequence
  AND  ue.idempotency_key IS NULL
  AND  n.idempotency_key IS NOT NULL;

-- NULLs are distinct in a unique index, so pre-existing keyless rows are
-- unaffected and ON CONFLICT (idempotency_key) can infer this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_events_idem
  ON user_events (idempotency_key);
```

- [ ] **Step 2: Write the failing test**

Create `backend/user_events_test.go`. This is an integration test following the existing convention in `backend/*_integration_test.go` — check that file set for the harness helper name and build tag before writing, and match it exactly.

```go
func TestRecordUserEventIsIdempotent(t *testing.T) {
	ctx, pool := testPool(t) // match the existing integration harness helper
	recipient := seedUser(t, ctx, pool)

	var first, second int64
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var e error
		first, e = RecordUserEvent(ctx, tx, UserEventInput{
			RecipientID:    recipient,
			Kind:           "mention",
			ResourceType:   "message",
			ResourceID:     "msg-1",
			IdempotencyKey: "test:dedupe:1",
			Payload:        json.RawMessage(`{}`),
		})
		return e
	})
	if err != nil {
		t.Fatalf("first record: %v", err)
	}

	// A replay with the same key must return the same sequence, not append.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var e error
		second, e = RecordUserEvent(ctx, tx, UserEventInput{
			RecipientID:    recipient,
			Kind:           "mention",
			ResourceType:   "message",
			ResourceID:     "msg-1",
			IdempotencyKey: "test:dedupe:1",
			Payload:        json.RawMessage(`{}`),
		})
		return e
	})
	if err != nil {
		t.Fatalf("replay record: %v", err)
	}

	if first != second {
		t.Fatalf("replay appended a new event: first=%d second=%d", first, second)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_events WHERE idempotency_key = $1`,
		"test:dedupe:1").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 user_event, got %d", n)
	}
}
```

- [ ] **Step 3: Run the test and confirm it fails**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 \
  go test ./... -run TestRecordUserEventIsIdempotent -v
```

Expected: FAIL — `undefined: RecordUserEvent` and `undefined: UserEventInput`.

- [ ] **Step 4: Write `backend/user_events.go`**

Port from `notifications.go`, keeping `looksLikeUUID` and `insertUserEvent`, dropping the notifications-table write and every inbox read path (`ListNotifications`, `UnreadCount`, `MarkRead`, `MarkAllRead`, `loadNotificationByKey`).

```go
package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// UserEventInput describes one durable, recipient-scoped event.
type UserEventInput struct {
	RecipientID    string
	ActorID        string
	Kind           string
	ResourceType   string
	ResourceID     string
	IdempotencyKey string
	Payload        json.RawMessage
}

var errUserEventInput = errors.New("invalid user event input")

// RecordUserEvent appends a durable event for the recipient and publishes a
// delivery hint, exactly once per idempotency key. A replay returns the
// existing sequence rather than appending a duplicate — WebSocket catch-up
// replays this stream, so a duplicate is a user-visible double delivery.
func RecordUserEvent(ctx context.Context, tx pgx.Tx, in UserEventInput) (int64, error) {
	if in.RecipientID == "" || in.Kind == "" || in.IdempotencyKey == "" {
		return 0, errUserEventInput
	}
	payload := in.Payload
	if len(payload) == 0 || string(payload) == "null" {
		payload = json.RawMessage("{}")
	}
	// A non-UUID actor (e.g. a system actor) is stored as NULL rather than
	// failing the whole operation on a cast error.
	actorID := in.ActorID
	if !looksLikeUUID(actorID) {
		actorID = ""
	}

	var seq int64
	err := tx.QueryRow(ctx, `
		INSERT INTO user_events
		  (recipient_id, actor_id, kind, resource_type, resource_id, payload, idempotency_key)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING sequence
	`, in.RecipientID, actorID, in.Kind, in.ResourceType, in.ResourceID, payload, in.IdempotencyKey).Scan(&seq)

	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race: an event with this key already exists. Return it
		// unchanged and publish no second hint.
		if err := tx.QueryRow(ctx,
			`SELECT sequence FROM user_events WHERE idempotency_key = $1`,
			in.IdempotencyKey).Scan(&seq); err != nil {
			return 0, err
		}
		return seq, nil
	}
	if err != nil {
		return 0, err
	}

	// Delivery hint on the recipient's channel (deduped by the outbox itself).
	hint, _ := json.Marshal(map[string]any{"sequence": seq, "kind": in.Kind})
	if _, err := EnqueueOutbox(ctx, tx, OutboxEvent{
		Topic:          "telos:user:" + in.RecipientID,
		EventType:      "user_event",
		AggregateType:  "user",
		AggregateID:    in.RecipientID,
		IdempotencyKey: "evt:" + in.IdempotencyKey,
		Payload:        hint,
	}); err != nil {
		return 0, err
	}
	return seq, nil
}
```

Copy `looksLikeUUID` verbatim from `notifications.go:138` into this file before deleting the original. If `insertUserEvent` lives in `notifications.go`, it is now superseded by the inline INSERT above and is not carried over.

- [ ] **Step 5: Delete the superseded files**

```bash
git rm backend/notifications.go backend/notifications_test.go
```

- [ ] **Step 6: Switch the three callers**

In `backend/chat_threads.go`, `notifyForMessageTx` (line 291) currently calls `CreateNotification` with `NotifyThreadReply` and mention kinds. Replace each call with `RecordUserEvent`, mapping `NotificationInput` → `UserEventInput` field-for-field and the `NotifyX` constants to their string literals (`"thread_reply"`, `"mention"`). Rename the function to `recordMessageEventsTx` and update its call site at `chat_threads.go:273`. Discard the returned sequence with `_`.

In `backend/annotations.go`, `CreateReply` (line 293) calls `CreateNotification` with `NotifyAnnotationReply`. Replace with `RecordUserEvent` and kind `"annotation_reply"`.

In `backend/outbox.go:75-90`, the security-intent block calls `CreateNotification` with `NotifyAccountSecurity`. Replace with `RecordUserEvent` and kind `"account_security"`, keeping the `accountSecurityNotifyKinds` gate and the `"notif:" + key` idempotency key exactly as-is so previously-recorded keys still deduplicate.

- [ ] **Step 7: Run the full backend suite**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS, including `TestRecordUserEventIsIdempotent`.

- [ ] **Step 8: Commit**

```bash
git add backend/db/migrations/0019_user_event_idempotency.sql backend/user_events.go backend/user_events_test.go backend/chat_threads.go backend/annotations.go backend/outbox.go
git commit -m "refactor: move the event idempotency guard onto user_events

The notifications table's unique index was the deduplication guard for the
user_events stream that WebSocket catch-up replays. Relocate it before the
table is dropped so a retried mutation cannot double-deliver a mention or
thread reply on reconnect.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Remove the notification inbox HTTP surface

**Files:**
- Delete: `backend/notification_handlers.go`
- Modify: `backend/main.go:478-482`, `backend/pagination.go:225`, `backend/account_lifecycle.go:95`

- [ ] **Step 1: Delete the handlers file**

```bash
git rm backend/notification_handlers.go
```

- [ ] **Step 2: Remove the four route registrations**

Delete these lines from `backend/main.go` (currently 478–482, including the `// In-app notifications + recipient-scoped user-event stream.` comment):

```go
mux.Handle("GET /api/v1/notifications", withAuth(http.HandlerFunc(handleListNotifications), ""))
mux.Handle("GET /api/v1/notifications/unread-count", withAuth(http.HandlerFunc(handleUnreadCount), ""))
mux.Handle("PUT /api/v1/notifications/{id}/read", withAuth(http.HandlerFunc(handleMarkNotificationRead), ""))
mux.Handle("PUT /api/v1/notifications/read-all", withAuth(http.HandlerFunc(handleMarkAllNotificationsRead), ""))
```

- [ ] **Step 3: Remove the pagination registration**

In `backend/pagination.go:225`, delete the `{"notifications", "created_desc"}` entry from the surface list.

- [ ] **Step 4: Remove the account-deletion cleanup**

In `backend/account_lifecycle.go:95`, delete the `` `DELETE FROM notifications WHERE user_id = $1` `` statement from the cleanup list. Leave the `media_list_entries` and `media_lists` statements for now — Task 5 removes those.

- [ ] **Step 5: Verify**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS. If a test in `main_test.go` asserts a notifications route exists, delete that test case.

- [ ] **Step 6: Commit**

```bash
git add -A backend/
git commit -m "feat: remove the notification inbox HTTP surface

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Remove Watch Party from the backend

**Files:**
- Delete: `backend/watchparty.go`, `backend/watchparty_handlers.go`, `backend/watchparty_integration_test.go`, `backend/watchparty_test.go`
- Modify: `backend/main.go:502-517`, `backend/channel_admin.go:23,94-97,257`

- [ ] **Step 1: Delete the four files**

```bash
git rm backend/watchparty.go backend/watchparty_handlers.go backend/watchparty_integration_test.go backend/watchparty_test.go
```

- [ ] **Step 2: Remove the sixteen route registrations**

Delete `backend/main.go` lines 502–517 in full, including the Watch Party section comment. Every route in that block (`POST /api/v1/watch-parties` through `PUT /api/v1/watch-parties/{id}/state`) goes.

- [ ] **Step 3: Remove the channel-deletion Watch Party guard**

In `backend/channel_admin.go`:
- Delete the `errChannelInUseByParty` declaration (line 23).
- Delete the `linked` existence query at line 97 and its surrounding guard — the block that runs `SELECT EXISTS(SELECT 1 FROM watch_parties WHERE (text_channel_id=$1 OR voice_channel_id=$1) AND ended_at IS NULL)` and returns `errChannelInUseByParty`.
- Delete the `channel_in_use` / "This channel is linked to an active Watch Party." error mapping at line 257.
- Update the `// Watch Party (409).` comment at line 94 to describe what the function actually does now.

- [ ] **Step 4: Verify**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS. Delete any `channel_admin_test.go` case asserting the 409-on-linked-party behaviour.

- [ ] **Step 5: Commit**

```bash
git add -A backend/
git commit -m "feat: remove Watch Party from the backend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Remove voice from the backend

Voice is not confined to `voice.go` — `handleVoiceToken` is inline in `main.go` around lines 3205–3270.

**Files:**
- Delete: `backend/voice.go`, `backend/voice_test.go`
- Modify: `backend/main.go:274-275,462-466,3205-3270`, `backend/chat.go:40,86-87`, `backend/security.go:179`, `backend/settings.go:66-83`, `backend/server.go:35`

- [ ] **Step 1: Delete the voice files**

```bash
git rm backend/voice.go backend/voice_test.go
```

- [ ] **Step 2: Remove voice from `main.go`**

- Delete the `voiceSeats = newVoiceSeatStore(redisClient)` initialisation and its `// Atomic voice seat store enforcing the 25-participant beta ceiling.` comment (lines 274–275).
- Delete both route registrations: `POST /api/v1/voice/channels/{id}/token` (line 463) and `POST /api/v1/voice/webhook` (line 466), plus the `// Voice Token (Requires join_voice)` comment.
- Delete the entire `// LiveKit Voice Token (Hardened SDK)` section starting at line 3205: `handleVoiceToken`, its `GenerateLiveKitToken` wrapper at line 3226, and any helper used only by them.

**Note:** `GenerateLiveKitToken` has a dedicated test in `main_test.go` (referenced in `CLAUDE.md`). Delete that test with the function.

- [ ] **Step 3: Remove the voice channel action**

In `backend/chat.go`, delete `ChannelVoice ChannelAction = "voice"` (line 40) and its `case ChannelVoice: actionPerm = "join_voice"` branch (lines 86–87).

- [ ] **Step 4: Remove the webhook CSRF exemption**

In `backend/security.go:179`, delete the `if r.URL.Path == "/api/v1/voice/webhook"` exemption branch. The endpoint no longer exists, so the exemption is now a hole rather than an allowance.

- [ ] **Step 5: Remove voice preference validation**

In `backend/settings.go:66-83`, delete the validation cases for `voiceNoiseSuppression`, `voiceEchoCancellation`, `voiceAutoGainControl`, `voiceInputDeviceId`, `voiceOutputDeviceId`, `voiceInputGain`, and `voiceOutputVolume`. Delete the matching assertions in `settings_test.go`.

- [ ] **Step 6: Update the stale shutdown comment**

`backend/server.go:35` reads `// upgrades/uploads/voice reservations with 503 shutting_down.` — drop the voice clause. Do **not** touch `signal.Notify` on line 54; that is Go's os/signal package, unrelated to notifications.

- [ ] **Step 7: Verify**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add -A backend/
git commit -m "feat: remove voice rooms and LiveKit token minting from the backend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Remove My List from the backend

**Files:**
- Delete: `backend/mylist.go`, `backend/mylist_test.go`
- Modify: `backend/main.go:496-499`, `backend/account_lifecycle.go:98-99`

- [ ] **Step 1: Delete the files**

```bash
git rm backend/mylist.go backend/mylist_test.go
```

- [ ] **Step 2: Remove the four route registrations**

Delete `backend/main.go` lines 496–499 — the `GET`/`POST`/`DELETE`/`PUT` routes under `/api/v1/users/me/media-list`.

- [ ] **Step 3: Remove the account-deletion cleanup**

In `backend/account_lifecycle.go`, delete the `` `DELETE FROM media_list_entries WHERE user_id = $1` `` and `` `DELETE FROM media_lists WHERE user_id = $1` `` statements (lines 98–99).

- [ ] **Step 4: Verify and commit**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
git add -A backend/
git commit -m "feat: remove My List from the backend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Remove `channels.type` from the Go code

With voice gone, every channel is a text channel. The column must stop being read and written *before* migration `0020` drops it.

**Files:**
- Modify: `backend/channel_admin.go:43,72`, `backend/main.go:2271`, plus every query selecting or inserting `type` on `channels`

- [ ] **Step 1: Find every reference**

```bash
grep -rn "channels" backend/*.go | grep -iE "\btype\b" | grep -v _test.go
grep -rn "validChannelType\|ChannelType" backend/*.go
```

Work from this list; it is authoritative at execution time.

- [ ] **Step 2: Remove the validator**

Delete `func validChannelType(t string) bool { return t == "text" || t == "voice" }` (`channel_admin.go:43`) and every call site. Channel creation no longer accepts or validates a type.

- [ ] **Step 3: Remove `type` from the wire struct**

In `backend/main.go:2271`, delete the `Type string \`json:"type"\` // "text" or "voice"` field from the channel response struct, and remove `type` from every `SELECT` and `INSERT` touching `channels`.

- [ ] **Step 4: Update the immutability comment**

`channel_admin.go:72` reads `// UpdateChannel renames a channel (type is immutable to preserve text/voice`. Rewrite it to describe rename semantics without the type clause.

- [ ] **Step 5: Verify**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS. Update `chat_test.go`, `channel_admin_test.go`, and `chat_integration_test.go` fixtures that set or assert a channel type.

- [ ] **Step 6: Commit**

```bash
git add -A backend/
git commit -m "refactor: drop the channels.type distinction from the gateway

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Remove voice from the frontend

**Files:**
- Delete: `frontend/src/components/VoiceDock.tsx`, `components/settings/VoiceAudioSection.tsx`, `hooks/useMicTest.ts`, `lib/voiceAudio.ts`, `lib/voiceAudio.test.mjs`, `stores/useVoiceSessionStore.ts`, `styles/voice.css`
- Modify: `components/AppShell.tsx`, `components/MobileNavigation.tsx:18,37,132-161`, `components/MobileNavigation.test.tsx`, `app/(shell)/settings/page.tsx`, `stores/useChatSessionStore.ts`, `stores/usePreferencesStore.ts`, `lib/api.ts`, `styles/styles.css`, `app/layout.tsx`

- [ ] **Step 1: Delete the files**

```bash
git rm frontend/src/components/VoiceDock.tsx \
       frontend/src/components/settings/VoiceAudioSection.tsx \
       frontend/src/hooks/useMicTest.ts \
       frontend/src/lib/voiceAudio.ts \
       frontend/src/lib/voiceAudio.test.mjs \
       frontend/src/stores/useVoiceSessionStore.ts \
       frontend/src/styles/voice.css
```

- [ ] **Step 2: Remove the voice section from mobile navigation**

In `frontend/src/components/MobileNavigation.tsx`: delete the `useVoiceSessionStore` import (line 18), the `Volume2` icon import (line 13), the `voice` binding (line 34), the `voiceChannels` filter (line 37), and the entire `{voiceChannels.length > 0 && (...)}` block (lines 132–161). With `channels.type` gone, `textChannels` becomes simply `channels` — update line 37 accordingly.

- [ ] **Step 3: Remove remaining references**

- `components/AppShell.tsx`: remove the `VoiceDock` render and import, and the voice-channel grouping in the sidebar.
- `app/(shell)/settings/page.tsx`: remove the `VoiceAudioSection` import and its tab/section entry.
- `stores/useChatSessionStore.ts`: remove `type` from the channel model and any voice filtering.
- `stores/usePreferencesStore.ts`: remove the seven `voice*` preference keys matching those deleted in Task 4 Step 5.
- `lib/api.ts`: remove voice token and notification client functions.
- `styles/styles.css`: remove the `voice.css` import.
- `app/layout.tsx`: remove any voice-related provider or import.

- [ ] **Step 4: Verify**

```bash
cd frontend && npm run lint && npx vitest run
```

Expected: PASS with no unresolved imports. Update `MobileNavigation.test.tsx` cases that assert a voice section.

- [ ] **Step 5: Commit**

```bash
git add -A frontend/
git commit -m "feat: remove voice channels from the frontend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: Remove Watch Party and My List from the frontend

**Files:**
- Delete: `components/stream/WatchPartyPanel.tsx`, `components/stream/CreateWatchPartyDialog.tsx`, `components/stream/MediaPlayer.tsx`, `components/stream/MyListShelf.tsx`, `stores/useWatchPartyStore.ts`, `stores/useWatchPartyStore.test.ts`, `stores/useMyListStore.ts`, `stores/useMyListStore.test.ts`, `lib/playbackSync.ts`, `lib/playbackSync.test.ts`
- Modify: `app/(shell)/stream/page.tsx`, `styles/stream.css`

**Note:** `components/stream/MediaPlayer.tsx` is the watch-party sync wrapper and is safe to delete. The real HLS player is a *separate inline component of the same name* inside `app/(shell)/stream/page.tsx:84`. Do not delete that one.

- [ ] **Step 1: Delete the files**

```bash
git rm frontend/src/components/stream/WatchPartyPanel.tsx \
       frontend/src/components/stream/CreateWatchPartyDialog.tsx \
       frontend/src/components/stream/MediaPlayer.tsx \
       frontend/src/components/stream/MyListShelf.tsx \
       frontend/src/stores/useWatchPartyStore.ts \
       frontend/src/stores/useWatchPartyStore.test.ts \
       frontend/src/stores/useMyListStore.ts \
       frontend/src/stores/useMyListStore.test.ts \
       frontend/src/lib/playbackSync.ts \
       frontend/src/lib/playbackSync.test.ts
```

- [ ] **Step 2: Revert the hero controls**

`app/(shell)/stream/page.tsx` was wired up in commit `454d541`. Remove: the `CreateWatchPartyDialog`, `WatchPartyPanel`, `MyListShelf`, and `useMyListStore` imports; the `watchPartyMediaId`, `addingToList`, and `addedToList` state; the `handleAddMyList` callback; the `<CreateWatchPartyDialog>` render; and the two hero buttons (`hero-add-my-list`, `hero-start-watch-party`) along with their now-unused `Plus` and `Users` icon imports.

- [ ] **Step 3: Remove the styles**

Delete the `.wp-*` rule blocks and My List shelf rules from `styles/stream.css`.

- [ ] **Step 4: Verify and commit**

```bash
cd frontend && npm run lint && npx vitest run
git add -A frontend/
git commit -m "feat: remove Watch Party and My List from the frontend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: Remove the notification inbox from the frontend

**Files:**
- Delete: `components/notifications/NotificationInbox.tsx`, `NotificationInbox.test.tsx`, `NotificationItem.tsx`, `stores/useNotificationStore.ts`, `stores/useNotificationStore.test.ts`, `styles/notifications.css`
- Modify: `components/AppShell.tsx`, `styles/styles.css`

- [ ] **Step 1: Delete the files**

```bash
git rm -r frontend/src/components/notifications \
       frontend/src/stores/useNotificationStore.ts \
       frontend/src/stores/useNotificationStore.test.ts \
       frontend/src/styles/notifications.css
```

- [ ] **Step 2: Remove the bell**

In `components/AppShell.tsx`, remove the notification bell control, its unread-count badge, the `NotificationInbox` dialog render, and all related imports and state. Remove the `notifications.css` import from `styles/styles.css`.

- [ ] **Step 3: Verify and commit**

```bash
cd frontend && npm run lint && npx vitest run
git add -A frontend/
git commit -m "feat: remove the notification inbox from the frontend

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 10: Remove LiveKit infrastructure, dependencies, and documentation

**Files:**
- Delete: `config/livekit.yaml`, `docs/voice-turn-ports.md`, `docs/voice-turn-setup-guide.md`, `tests/load/livekit-rooms.yaml`
- Modify: `docker-compose.yml`, `frontend/package.json`, `frontend/package-lock.json`, `NOTICE`, `frontend/src/components/settings/CreditsSection.tsx`

- [ ] **Step 1: Delete the files**

```bash
git rm config/livekit.yaml docs/voice-turn-ports.md docs/voice-turn-setup-guide.md tests/load/livekit-rooms.yaml
```

- [ ] **Step 2: Remove the compose service**

In `docker-compose.yml`, delete the entire `livekit:` service block (including the `LIVEKIT_KEYS`, `LIVEKIT_WEBHOOK_API_KEY`, `REDIS_PASSWORD`, and `LIVEKIT_TURN_DOMAIN` environment entries added in `454d541`), its published TURN ports, its network attachments, and any Traefik router or entrypoint definition for TURN. Remove `LIVEKIT_*` variables from `.env.example`.

- [ ] **Step 3: Remove the npm dependency**

```bash
cd frontend && npm uninstall livekit-client
```

This updates both `package.json` and `package-lock.json`.

- [ ] **Step 4: Remove the attribution entries**

Delete the LiveKit entry from `NOTICE` and from `frontend/src/components/settings/CreditsSection.tsx`. Both must stay accurate — an attribution for a dependency that is no longer bundled is a false claim.

- [ ] **Step 5: Validate compose and the frontend build**

```bash
./scripts/validate-compose.sh
cd frontend && npm run lint && npm run build
```

Expected: compose validates; the static export builds. Do **not** bring the live stack up.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: remove LiveKit container, config, docs, and dependency

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 11: Apply the destructive migration

Nothing in the codebase references these objects now. This is where they leave the schema.

**Files:**
- Create: `backend/db/migrations/0020_remove_realtime_extras.sql`
- Create/Modify: `backend/migrations_integration_test.go`

- [ ] **Step 1: Take a safety dump first**

```bash
podman exec telos-postgres pg_dump -U "$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2-)" \
  -d "$(grep -E '^POSTGRES_DB=' .env | cut -d= -f2-)" \
  > /tmp/claude-1000/-var-home-cleadmon-Projects-Telos/ea536c6d-15f7-4be7-879d-26ce1ddbbacc/scratchpad/telos-pre-0020.sql
```

Confirm the file is non-empty before continuing.

- [ ] **Step 2: Write the migration**

Create `backend/db/migrations/0020_remove_realtime_extras.sql`:

```sql
-- Migration 0020: remove voice rooms, Watch Party, the notification inbox, and
-- My List, and collapse the role set to four. Destructive and irreversible.
-- Applied only after all code referencing these objects is gone.

-- ── Watch Party: reverse foreign-key order ───────────────────────────────────
DROP TABLE IF EXISTS watch_party_host_offers;
DROP TABLE IF EXISTS watch_party_invitations;
DROP TABLE IF EXISTS watch_party_members;
DROP TABLE IF EXISTS watch_parties;

-- ── Voice channels ───────────────────────────────────────────────────────────
DELETE FROM messages WHERE channel_id IN (SELECT id FROM channels WHERE type = 'voice');
DELETE FROM channels WHERE type = 'voice';

-- Every remaining channel is a text channel, so the discriminator is dead
-- weight in every query, admin form, and sidebar grouping.
ALTER TABLE channels DROP CONSTRAINT IF EXISTS chk_channels_type;
ALTER TABLE channels DROP COLUMN IF EXISTS type;

-- role_permissions.permission_id and channel_permission_overrides.permission_id
-- both declare ON DELETE CASCADE, so per-role grants and per-channel overrides
-- are removed by this single delete. Do not add explicit deletes.
DELETE FROM permissions WHERE id = 'join_voice';

-- ── Notification inbox (the guard already moved to user_events in 0019) ──────
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_event;
DROP TABLE IF EXISTS notifications;

ALTER TABLE user_events DROP CONSTRAINT IF EXISTS chk_user_events_kind;
ALTER TABLE user_events ADD CONSTRAINT chk_user_events_kind CHECK (
  kind IN ('mention','thread_reply','annotation_reply','account_security'));

-- ── My List ──────────────────────────────────────────────────────────────────
DROP TABLE IF EXISTS media_list_entries;
DROP TABLE IF EXISTS media_lists;

-- ── RBAC collapse to Owner / Administrator / Moderator / Member ──────────────
-- Member absorbs Contributor. Uploading a book is the correct default for a
-- member of a community library.
INSERT INTO role_permissions (role_id, permission_id)
VALUES ('Member', 'upload_books')
ON CONFLICT DO NOTHING;

-- Moderator absorbs Librarian's management rights.
INSERT INTO role_permissions (role_id, permission_id)
VALUES ('Moderator', 'manage_files'), ('Moderator', 'manage_library')
ON CONFLICT DO NOTHING;

-- ORDERING IS LOAD-BEARING: reassign before deleting. user_roles.role_id
-- cascades on role deletion, so deleting first would silently strip these
-- users of every capability instead of moving them.
INSERT INTO user_roles (user_id, role_id)
SELECT user_id, 'Member' FROM user_roles WHERE role_id = 'Contributor'
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (user_id, role_id)
SELECT user_id, 'Moderator' FROM user_roles WHERE role_id = 'Librarian'
ON CONFLICT DO NOTHING;

DELETE FROM roles WHERE id IN ('Contributor', 'Librarian');
```

- [ ] **Step 3: Write the migration test**

Add to `backend/migrations_integration_test.go`, matching the file's existing harness and build tag:

```go
func TestMigration0020RemovesRealtimeExtras(t *testing.T) {
	ctx, pool := testPool(t)

	for _, table := range []string{
		"watch_parties", "watch_party_members", "watch_party_invitations",
		"watch_party_host_offers", "notifications", "media_lists", "media_list_entries",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			   WHERE table_schema='public' AND table_name=$1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Errorf("table %s should have been dropped", table)
		}
	}

	var typeExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		   WHERE table_name='channels' AND column_name='type')`).Scan(&typeExists); err != nil {
		t.Fatalf("check channels.type: %v", err)
	}
	if typeExists {
		t.Error("channels.type should have been dropped")
	}

	var permCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM permissions WHERE id='join_voice'`).Scan(&permCount); err != nil {
		t.Fatalf("check join_voice: %v", err)
	}
	if permCount != 0 {
		t.Error("join_voice permission should have been dropped")
	}

	// The cascade must leave no orphaned per-channel override.
	var orphans int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM channel_permission_overrides WHERE permission_id='join_voice'`).Scan(&orphans); err != nil {
		t.Fatalf("check overrides: %v", err)
	}
	if orphans != 0 {
		t.Errorf("found %d orphaned join_voice overrides", orphans)
	}

	var roleCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM roles WHERE id IN ('Contributor','Librarian')`).Scan(&roleCount); err != nil {
		t.Fatalf("check roles: %v", err)
	}
	if roleCount != 0 {
		t.Error("Contributor and Librarian should have been deleted")
	}
}

func TestMigration0020PreservesEveryUsersCapabilities(t *testing.T) {
	ctx, pool := testPool(t)

	// No user may be left with zero roles by the collapse.
	var orphanedUsers int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id)
		  AND u.id IN (SELECT user_id FROM user_roles)
	`).Scan(&orphanedUsers); err != nil {
		t.Fatalf("check orphaned users: %v", err)
	}
	if orphanedUsers != 0 {
		t.Errorf("%d users lost every role in the collapse", orphanedUsers)
	}

	// No Member may hold a destructive capability.
	var escalated int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM role_permissions
		WHERE role_id='Member' AND permission_id IN ('manage_files','manage_library')
	`).Scan(&escalated); err != nil {
		t.Fatalf("check escalation: %v", err)
	}
	if escalated != 0 {
		t.Error("Member must not gain manage_files or manage_library")
	}
}
```

- [ ] **Step 4: Run the migration and integration tests**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./... -v -run TestMigration0020
```

Expected: both tests PASS. Also confirm idempotency — the suite's existing "apply twice" migration test must still pass, which the `IF EXISTS` / `ON CONFLICT` clauses above guarantee.

- [ ] **Step 5: Run the whole backend suite**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/db/migrations/0020_remove_realtime_extras.sql backend/migrations_integration_test.go
git commit -m "feat: drop voice, Watch Party, inbox, and My List schema

Collapses the role set to Owner/Administrator/Moderator/Member. Reassigns
Contributor to Member and Librarian to Moderator before deleting those roles,
because user_roles cascades on role deletion and would otherwise strip the
affected members of every capability.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 12: Re-arm the product-truth guard and verify the phase

**Files:**
- Delete: `frontend/e2e/voice.spec.ts`, `voice-secure-context.spec.ts`, `watch-party.spec.ts`, `notifications.spec.ts`, `my-list.spec.ts`
- Modify: `frontend/e2e/capabilities.spec.ts`, `frontend/e2e/product-truth.spec.ts`, `scripts/check-product-truth.sh`

- [ ] **Step 1: Delete the obsolete specs**

```bash
git rm frontend/e2e/voice.spec.ts frontend/e2e/voice-secure-context.spec.ts \
       frontend/e2e/watch-party.spec.ts frontend/e2e/notifications.spec.ts \
       frontend/e2e/my-list.spec.ts
```

- [ ] **Step 2: Revert the product-truth assertion**

Commit `454d541` replaced the inert-control assertion with a Watch Party one. Restore it as a removal assertion — replace the `"My List and Watch Party controls are real active controls on stream page"` test with:

```ts
test("no voice, Watch Party, My List, or notification surface renders", async ({ page }) => {
  await login(page);
  await expect(page.locator(".chan", { hasText: "general" })).toBeVisible();
  await expect(page.getByRole("button", { name: /watch party/i })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /my list/i })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /notifications/i })).toHaveCount(0);
  await expect(page.getByTestId("voice-dock")).toHaveCount(0);
});
```

- [ ] **Step 3: Remove `join_voice` from the capabilities spec**

In `frontend/e2e/capabilities.spec.ts`, delete every `join_voice` case and any assertion about voice-gated UI.

- [ ] **Step 4: Add removal guards to the product-truth script**

Append a new numbered check to `scripts/check-product-truth.sh`, following the existing `report`/`hit` idiom:

```bash
# 8. Removed feature surfaces must not reappear in shipped code.
hit=$(grep -rniE 'livekit|watch.?party|voice.?dock|useVoiceSession|useWatchParty|useNotificationStore|useMyListStore|media-list' \
      frontend/src backend --include='*.go' --include='*.ts' --include='*.tsx' 2>/dev/null \
      | grep -vE '(_test\.go|\.spec\.ts|\.test\.tsx?)')
[ -n "$hit" ] && report "removed feature surface reappeared" "$hit"
```

- [ ] **Step 5: Run the guard**

```bash
./scripts/check-product-truth.sh
```

Expected: exit 0, no violations. If it reports a hit, that reference was missed in an earlier task — fix it there rather than loosening the pattern.

- [ ] **Step 6: Run every gate**

```bash
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go vet ./...
podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.26.5 go test ./...
cd frontend && npm run lint && npx vitest run && npm run build
```

Expected: all green. Playwright E2E is deferred to the end of Phase 5, when the mobile matrix lands — the suite needs `npm run dev` running and the UI is still mid-redesign.

- [ ] **Step 7: Confirm nothing survives**

```bash
grep -ril -E 'voice|livekit|watch.?party' --exclude-dir=.git --exclude-dir=node_modules \
  --exclude-dir=out --exclude-dir=.next --exclude-dir=.worktrees \
  --exclude-dir=plans --exclude-dir=specs . | sort
```

Expected: only `documentation/` and root docs remain, all of which Phase 6 rewrites. Any hit under `backend/`, `frontend/src/`, `config/`, or `scripts/` is a miss to fix now.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "test: assert removed surfaces stay removed

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

## Phase 2 Exit Criteria

- `go vet ./...` and `go test ./...` pass in the golang:1.26.5 container.
- `npm run lint`, `npx vitest run`, and `npm run build` pass in `frontend/`.
- `./scripts/check-product-truth.sh` exits 0.
- `./scripts/validate-compose.sh` passes with no `livekit` service.
- Migrations `0019` and `0020` apply cleanly and idempotently; `TestMigration0020PreservesEveryUsersCapabilities` passes.
- No reference to voice, LiveKit, Watch Party, the notification inbox, or My List remains under `backend/`, `frontend/src/`, `frontend/e2e/`, `config/`, or `scripts/`.
- The live stack has **not** been redeployed.

Phase 3 (design foundation) is planned separately, after this phase lands.
