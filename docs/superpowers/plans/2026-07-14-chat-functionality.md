# Chat Functionality — Full Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the Chat module up to the design-system mockups — reactions, message edit/delete/pin, presence, a right-hand channel panel, a rich composer (emoji + markdown + @mentions), Library/Stream media-share cards, and unread badges + mention notifications — everything in `mockups/chat-synthwave.html` and `system/chat-kit.html` **except** the AI Oracle summarizer (deferred to its own spec).

**Architecture:** Adopt **"WS becomes receive-only; all mutations become REST"** (Approach A from brainstorming). The chat WebSocket (`GET /api/v1/chat/ws`) stops accepting inbound messages and becomes a pure Redis→client subscriber. Every mutation — send, edit, delete, react, pin, mark-read — is a REST endpoint that runs through the existing `withAuth`/`hasPermission`/`csrfMiddleware` stack, writes Postgres, then publishes a typed event to the channel's Redis pub/sub topic (`telos:chat:<channelID>`), which fans back out over every subscriber's WS. A second, per-user events WebSocket (`GET /api/v1/events/ws`) carries cross-channel signals (unread bumps, mention notifications). Presence is tracked in Redis with per-user refcounts (multi-tab safe) and a heartbeat TTL.

**Tech Stack:** Go 1.22 (stdlib `net/http` + `pgx` + `go-redis` + `gorilla/websocket`, all already present), Postgres migration `0006`, Next.js 16 static export, Zustand, `react-markdown` + `rehype-sanitize` (new), a small emoji picker (`emoji-mart` or hand-rolled), Playwright e2e.

## Global Constraints

- **Go is NOT installed on the host.** Run every Go command in a container, from the repo root:
  `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 go test ./...`
  (`go vet ./...` likewise). A local `go build` fails unless `backend/out/` exists.
- **The Go test container has no Postgres/Redis.** Only pure functions are unit-testable in Go here (see `main_test.go`, which tests `GenerateLiveKitToken` in isolation). DB/Redis-integration behavior is verified with **Playwright e2e** (`frontend/`, needs `npm run dev` + a running stack) and **manual smoke** (`curl` + `podman logs telos-core`, watching for `(Mock)` fallback warnings). Each task states which gate applies — do not invent DB-backed Go tests.
- **Schema changes** go in `backend/db/migrations/` as one new numbered file using `IF NOT EXISTS` / `ON CONFLICT` patterns. Migrations are applied at startup, tracked in `schema_migrations` (see `main.go:288`). Never edit an already-applied migration; add `0006_chat_enhancements.sql`.
- **Secrets** come only from `.env` interpolation — never hardcode.
- **Copyleft boundary:** integrate with Jellyfin/Grimmory over HTTP only; never link their code into the gateway.
- **CSRF is automatic:** `csrfMiddleware` (`main.go:534`) validates `Origin`/`Referer` on all state-changing requests. New `POST`/`PATCH`/`DELETE` endpoints inherit this. The frontend `api()` helper (`frontend/src/lib/api.ts`) already sends `credentials: "include"` and same-origin/allowed-origin requests, so dev (`:3000` → `:8080`) already passes.
- **Frontend is a static export** (`output: "export"`): no server components at runtime, no API routes. Dev on `:3000` points API/WS at `:8080` via `apiBase()`/`wsBase()`.
- **The gateway marshals empty Go slices as JSON `null`.** Any new list endpoint must either return `[]T{}` initialized (not `var x []T`) or the frontend must normalize. Prefer initializing `x := []T{}` server-side, matching `handleListChannels`.
- **Lint rejects setState-in-effect;** use promise-chain loaders as existing stores/pages do.
- **Next.js 16 is newer than training data** — consult `node_modules/next/dist/docs/` before nontrivial Next.js work (per `frontend/AGENTS.md`).
- **Commit after every task** with a conventional-commit message; end each message with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` (executors on a different model should substitute their own name).
- **Flat design, two themes** via `data-theme` on `<html>` (`synthwave` default, `ink`): token swap only, no shadows. New CSS goes in `frontend/src/styles/chat.css` using existing tokens (`--rose`, `--cyan`, `--violet`, `--line`, `--surface`, `--r`, `--f-mono`, …). Class names must match the mockups (`.share`, `.reacts`, `.react`, `.oracle`, `.aside`, `.pin`, `.memrow`, …) so the lifted CSS applies.

## Verified Codebase Facts (probed live 2026-07-14 — do NOT re-derive)

- **Backend is one file:** `backend/main.go` (2391 lines). Chat lives at:
  - `handleWebSocket` (`main.go:1173`) — upgrades, sends 50-message history, subscribes to `telos:chat:<cid>`, and **currently also runs an inbound read loop** that persists+publishes. This inbound loop is what Phase 0 removes.
  - `handleListChannels` (`main.go:1339`) — `GET /api/v1/channels` → `[{id,name,type}]`.
  - `getMessagesForChannel` (`main.go:1363`) — history query, newest-50, joins `users`.
  - Routes registered `main.go:176-247`. Chat routes at `:213-217`.
- **WS contract today:** server→client JSON is `WSNotification{type,messages?,message?}` (`main.go:85`); `WSMessage` has `id,sender,senderId,displayName,avatar,avatarUrl,role,content,timestamp` (`main.go:73`). Client→server today is `{content}` (removed in Phase 0). Frontend consumer: `useChatSessionStore.ts` `ws.onmessage` switch on `history`/`message`.
- **Permissions** are DB-driven: `permissions`, `role_permissions`, `channel_role_overrides` (bitmask `view_channel`=1, `send_messages`=2, `join_voice`=4), seeded in `0001_auth_tables.sql`; checked by `hasPermission(ctx,user,perm,channelID)` (`main.go:643`). `Administrator` role bypasses channel overrides. Global perms (no channel bit) are checked against `role_permissions` — that is how we add `manage_messages`.
- **`messages`** = `id,channel_id,user_id,content,created_at` (`0002`). **`users`** has `username`, `display_name` (nullable), `avatar_file_id` (nullable → avatar URL `/api/v1/users/<id>/avatar`). **`user_roles(user_id,role_id)`**; first role is the display role, else `"Member"`.
- **Redis** client is global `redisClient` (`main.go:46`); chat topic `telos:chat:<cid>`; caching keys `telos:jellyfin:*`. Pub/sub pattern: `redisClient.Subscribe(ctx, chan)` / `redisClient.Publish(ctx, chan, payload)`.
- **Right `.aside` panel is styled but never rendered.** `foundations/shell.css` defines `.aside` (284px, `border-left`); `AppShell.tsx` renders only `rail` + `arena` inside `.shellbody`. The aside must be added as a third `.shellbody` child, conditional on `/chat`.
- **Library/Stream expose LIST endpoints only** — there is no single-item metadata route. Embeds need `GET /api/v1/library/books/{id}` and `GET /api/v1/media/items/{id}` (Phase 4, Task 13).
- **Frontend helpers** (`lib/api.ts`): `api<T>(path, init?)` (throws `ApiError`), `apiBase()`, `wsBase()`, `avatarUrl(id)`, `libraryCoverUrl(id)`. Stores use `zustand create` (some with `persist`).
- **Playwright** e2e in `frontend/`; run `npx playwright test` after `npm run dev`. Existing spec references "Voice signaling"; there is chat coverage to keep green.

---

## File Structure

**Backend (`backend/`)** — stays single-file per repo convention, but grows in clearly-commented sections:
- `db/migrations/0006_chat_enhancements.sql` — **create**: reactions, pins, reads, notifications tables; `messages` columns; `manage_messages` permission.
- `main.go` — **modify**: new event structs; enrich `getMessagesForChannel`; convert `handleWebSocket` to receive-only + presence lifecycle; new handlers (`handleSendMessage`, `handleEditMessage`, `handleDeleteMessage`, `handleAddReaction`, `handleRemoveReaction`, `handlePinMessage`, `handleUnpinMessage`, `handleListPins`, `handleChannelMembers`, `handleMarkRead`, `handleUnreadCounts`, `handleUserSearch`, `handleLibraryBookByID`, `handleMediaItemByID`, `handleListNotifications`, `handleMarkNotificationsRead`, `handleEventsWebSocket`); a `publishChatEvent` helper; a `presence` helper block. New routes at `main.go:~217`.
- `main_test.go` — **modify**: pure-function unit tests (mention parsing, emoji validation, event envelope marshaling).

**Frontend (`frontend/src/`)**:
- `stores/useChatSessionStore.ts` — **modify**: new state (reactions/edits/deletes/pins/presence/embeds), REST mutators, WS reducer for new event types.
- `stores/usePresenceStore.ts` — **create** (or fold into chat store): online rosters + realm count.
- `stores/useNotificationsStore.ts` — **create**: unread counts, notifications feed, per-user events WS.
- `components/chat/ChatMessage.tsx` — **create**: one message (avatar/head/body/embed/reactions/hover actions). Split out of `chat/page.tsx`.
- `components/chat/MessageBody.tsx` — **create**: sanitized markdown renderer.
- `components/chat/Reactions.tsx` — **create**: reaction pills + add-reaction popover.
- `components/chat/Composer.tsx` — **create**: input + emoji picker + `@`-autocomplete + `+` share menu. Split out of `chat/page.tsx`.
- `components/chat/EmojiPicker.tsx` — **create**.
- `components/chat/MentionAutocomplete.tsx` — **create**.
- `components/chat/ShareCard.tsx` — **create**: embed card (book/film/file variants + actions).
- `components/chat/SharePicker.tsx` — **create**: search Library/Stream to attach an embed.
- `components/chat/ChatAside.tsx` — **create**: Pinned + channel stats + Online roster.
- `components/AppShell.tsx` — **modify**: render `<ChatAside/>` on `/chat`; unread badges on rail channels; wire `<NotificationsBell/>`; topbar "N online".
- `components/NotificationsBell.tsx` — **create**: bell dropdown.
- `app/(shell)/chat/page.tsx` — **modify**: slim down to layout + `map(ChatMessage)` + `<Composer/>`.
- `styles/chat.css` — **modify**: add `.share`, `.reacts/.react`, hover-actions, `.aside`, `.pin`, `.memrow`, `.specrow`, mention/emoji popovers, unread badge, bell — lifted from the mockups.
- `lib/api.ts` — **modify**: add `mediaItemUrl`, embed/share helper URLs as needed.
- `e2e/chat.spec.ts` (or existing chat spec) — **modify**: extend coverage per phase.

**Phases are independently shippable** — each ends green and demoable, and can be reviewed/merged as its own PR.

---

# Phase 0 — Foundation (schema + mutation transport + event protocol)

### Task 1: Migration 0006 — chat schema

**Files:**
- Create: `backend/db/migrations/0006_chat_enhancements.sql`

**Interfaces:**
- Produces (SQL objects later tasks depend on): `messages.edited_at`, `messages.deleted_at`, `messages.embed_kind`, `messages.embed_ref`, `messages.embed_snapshot`; tables `message_reactions(message_id,user_id,emoji)`, `channel_pins(channel_id,message_id,pinned_by,pinned_at)`, `channel_reads(user_id,channel_id,last_read_message_id,last_read_at)`, `notifications(id,user_id,kind,channel_id,message_id,actor_id,read_at,created_at)`; permission `manage_messages`.

- [ ] **Step 1: Write the migration**

```sql
-- Migration 0006: Chat enhancements — reactions, edits/deletes, pins, embeds, reads, notifications

ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at      TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS deleted_at     TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_kind     TEXT;   -- 'library_book' | 'stream_film' | 'file'
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_ref      TEXT;   -- referenced item id
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_snapshot JSONB;  -- denormalized {title,subtitle,kicker,cover,duration}

CREATE TABLE IF NOT EXISTS message_reactions (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    emoji      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS idx_reactions_message ON message_reactions (message_id);

CREATE TABLE IF NOT EXISTS channel_pins (
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    pinned_by  UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    pinned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, message_id)
);

CREATE TABLE IF NOT EXISTS channel_reads (
    user_id              UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    channel_id           UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    last_read_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    last_read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE IF NOT EXISTS notifications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,                 -- 'mention'
    channel_id UUID REFERENCES channels(id)   ON DELETE CASCADE,
    message_id UUID REFERENCES messages(id)   ON DELETE CASCADE,
    actor_id   UUID REFERENCES users(id)      ON DELETE CASCADE,
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, created_at DESC);

INSERT INTO permissions (id, description) VALUES
    ('manage_messages', 'Edit or delete any message and pin/unpin messages')
ON CONFLICT (id) DO NOTHING;

-- Grant to privileged roles that exist (defensive: only rows for existing roles are inserted)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, 'manage_messages' FROM roles r WHERE r.id IN ('Administrator','Host','Curator')
ON CONFLICT DO NOTHING;
```

- [ ] **Step 2: Apply + verify against a running stack**

Bring the stack up (or restart core so migrations run): `podman-compose up -d --build telos-core`. Then verify:

Run: `podman exec -i telos-postgres psql -U telos -d telos -c "\d message_reactions" -c "\d channel_pins" -c "\d channel_reads" -c "\d notifications" -c "SELECT id FROM permissions WHERE id='manage_messages';"`
Expected: all four tables described; one `manage_messages` row. Also confirm `podman logs telos-core | grep "migration version 6"` shows it applied (idempotent on re-run).

> Note: verify actual DB user/name against `.env` (`POSTGRES_USER`/`POSTGRES_DB`); the example assumes `telos`/`telos`.

- [ ] **Step 3: Commit**

```bash
git add backend/db/migrations/0006_chat_enhancements.sql
git commit -m "feat(chat): migration 0006 — reactions, pins, reads, notifications, embeds

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: WS event protocol + enriched history

**Files:**
- Modify: `backend/main.go` (structs near `:73-90`; `getMessagesForChannel` `:1363`)
- Modify: `backend/main_test.go`

**Interfaces:**
- Produces (Go types consumed by every later backend task): the server→client event envelope and enriched message shape.

```go
// Server→client event envelope (superset of the old WSNotification; old
// "history"/"message" fields preserved for compatibility during migration).
type WSEvent struct {
    Type      string          `json:"type"` // history|message|message.update|message.delete|reaction|pin|presence
    Messages  []WSMessage     `json:"messages,omitempty"`  // history
    Message   *WSMessage      `json:"message,omitempty"`   // message | message.update
    MessageID string          `json:"messageId,omitempty"` // message.delete | reaction | pin
    ChannelID string          `json:"channelId,omitempty"`
    Reaction  *WSReaction     `json:"reaction,omitempty"`  // reaction
    Pin       *WSPin          `json:"pin,omitempty"`       // pin
    Presence  *WSPresence     `json:"presence,omitempty"`  // presence
}

type WSReaction struct {
    MessageID string `json:"messageId"`
    Emoji     string `json:"emoji"`
    UserID    string `json:"userId"`
    Op        string `json:"op"`    // "add" | "remove"
    Count     int    `json:"count"` // new total for this emoji on this message
    Mine      bool   `json:"-"`     // per-recipient; computed client-side, never serialized
}

type WSPin struct {
    MessageID string `json:"messageId"`
    Op        string `json:"op"` // "add" | "remove"
}

type WSPresence struct {
    ChannelID string          `json:"channelId,omitempty"`
    Online    []PresenceUser  `json:"online"` // roster for this channel
    Count     int             `json:"count"`  // realm-wide online count
}
type PresenceUser struct {
    UserID      string `json:"userId"`
    Username    string `json:"username"`
    DisplayName string `json:"displayName"`
    Role        string `json:"role"`
    Avatar      string `json:"avatar"`
    AvatarUrl   string `json:"avatarUrl"`
}
```

- [ ] **Step 1: Extend `WSMessage`** with the fields the UI needs (append; keep existing fields/JSON tags unchanged):

```go
// added to WSMessage:
    Reactions []ReactionSummary `json:"reactions,omitempty"`
    EditedAt  string            `json:"editedAt,omitempty"`  // formatted, empty if never edited
    Deleted   bool              `json:"deleted,omitempty"`   // soft-deleted tombstone
    Pinned    bool              `json:"pinned,omitempty"`
    Embed     *MessageEmbed     `json:"embed,omitempty"`

// new types:
type ReactionSummary struct {
    Emoji string   `json:"emoji"`
    Count int      `json:"count"`
    Users []string `json:"users"` // userIds who reacted (drives "mine" highlight)
}
type MessageEmbed struct {
    Kind     string          `json:"kind"`     // library_book|stream_film|file
    Ref      string          `json:"ref"`
    Snapshot json.RawMessage `json:"snapshot"` // {title,subtitle,kicker,cover,duration}
}
```

- [ ] **Step 2: Rewrite `getMessagesForChannel`** to select edit/delete/embed columns, LEFT JOIN pins, and aggregate reactions. Deleted messages return with `Deleted:true` and blanked `Content`/`Embed` (tombstone). Sketch:

```go
rows, err := dbPool.Query(ctx, `
  SELECT m.id::text, u.id::text, u.username, COALESCE(u.display_name,''),
         u.avatar_file_id IS NOT NULL,
         CASE WHEN m.deleted_at IS NULL THEN m.content ELSE '' END,
         m.created_at, m.edited_at, m.deleted_at IS NOT NULL,
         (cp.message_id IS NOT NULL) AS pinned,
         m.embed_kind, m.embed_ref, m.embed_snapshot
  FROM messages m
  JOIN users u ON m.user_id = u.id
  LEFT JOIN channel_pins cp ON cp.message_id = m.id AND cp.channel_id = m.channel_id
  WHERE m.channel_id = $1
  ORDER BY m.created_at DESC
  LIMIT 50`, channelID)
// ... scan; if deleted -> msg.Deleted=true, skip embed; else attach embed if embed_kind non-null.
// Then bulk-load reactions for the page's message ids into ReactionSummary slices.
```

Reactions bulk load (one query for the page):

```go
// ids := collected message ids
rrows, _ := dbPool.Query(ctx, `
  SELECT message_id::text, emoji, COUNT(*)::int, array_agg(user_id::text)
  FROM message_reactions WHERE message_id = ANY($1)
  GROUP BY message_id, emoji ORDER BY MIN(created_at)`, ids)
// group into map[messageID][]ReactionSummary, assign to each msg.
```

Initialize `msgs := []WSMessage{}` (never `var`), reverse to chronological before returning (history renders oldest→newest; current code returns newest-first and the client appends — keep whatever order the client expects; verify against `useChatSessionStore` which sets `messages` directly, so return **chronological ascending**: reverse the DESC result).

> ⚠ Behavior note: the current query returns DESC and the client renders as-is; the mockup shows oldest-at-top. Return **ascending** here and adjust the client in Task 3 if needed. Pick one order and keep it consistent across history + live append.

- [ ] **Step 3: Unit-test the pure event marshaling** (no DB). In `main_test.go`:

```go
func TestWSEventDeleteMarshal(t *testing.T) {
    b, _ := json.Marshal(WSEvent{Type: "message.delete", MessageID: "abc", ChannelID: "c1"})
    got := string(b)
    if !strings.Contains(got, `"type":"message.delete"`) || !strings.Contains(got, `"messageId":"abc"`) {
        t.Fatalf("unexpected: %s", got)
    }
    if strings.Contains(got, `"reaction"`) || strings.Contains(got, `"messages"`) {
        t.Fatalf("omitempty leaked empty fields: %s", got)
    }
}
```

- [ ] **Step 4: Run Go tests + vet**

Run: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go vet ./... && go test ./..."`
Expected: PASS (existing `GenerateLiveKitToken` test + new marshal test).

- [ ] **Step 5: Commit**

```bash
git add backend/main.go backend/main_test.go
git commit -m "feat(chat): WS event envelope + enriched message history (reactions/pins/embeds/edit/delete)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: REST send + WebSocket becomes receive-only

**Files:**
- Modify: `backend/main.go` (`handleWebSocket` `:1173`; add `handleSendMessage`, `publishChatEvent`; route at `:217`)
- Modify: `frontend/src/stores/useChatSessionStore.ts` (`send`, `connect`)
- Modify: `frontend/e2e/*chat*.spec.ts`

**Interfaces:**
- Produces: `POST /api/v1/channels/{id}/messages` body `{content: string, embed?: {kind,ref}}` → `201 {message: WSMessage}`; helper `func publishChatEvent(ctx, channelID string, ev WSEvent)` used by all later mutation handlers.
- Produces: WS is now **receive-only** — client sends nothing.

- [ ] **Step 1: Add `publishChatEvent` helper**

```go
func publishChatEvent(ctx context.Context, channelID string, ev WSEvent) {
    ev.ChannelID = channelID
    payload, err := json.Marshal(ev)
    if err != nil { log.Printf("marshal chat event: %v", err); return }
    if err := redisClient.Publish(ctx, "telos:chat:"+channelID, payload).Err(); err != nil {
        log.Printf("publish chat event: %v", err)
    }
}
```

- [ ] **Step 2: Add `handleSendMessage`** — moves the persist+broadcast logic out of the WS read loop. Requires `send_messages` on the path channel. Reuse the existing sender-enrichment (username/role/avatar). Skeleton:

```go
func handleSendMessage(w http.ResponseWriter, r *http.Request) {
    channelID := r.PathValue("id")
    user := r.Context().Value(userContextKey).(*UserContext)
    var body struct {
        Content string `json:"content"`
        Embed   *struct{ Kind, Ref string } `json:"embed"`
    }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil { http.Error(w, "bad json", 400); return }
    body.Content = strings.TrimSpace(body.Content)
    if body.Content == "" && body.Embed == nil { http.Error(w, "empty message", 400); return }
    if len(body.Content) > 4000 { http.Error(w, "too long", 400); return }

    // (Phase 4 fills embed snapshot; for now embed is nil.)
    var msgID string; var ts time.Time
    err := dbPool.QueryRow(r.Context(), `
        INSERT INTO messages (channel_id, user_id, content) VALUES ($1,$2,$3)
        RETURNING id, created_at`, channelID, user.ID, body.Content).Scan(&msgID, &ts)
    if err != nil { http.Error(w, "persist failed", 500); return }

    msg := buildWSMessage(r.Context(), msgID, user.ID, body.Content, ts) // extract sender-enrichment into a helper
    publishChatEvent(r.Context(), channelID, WSEvent{Type: "message", Message: &msg})
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(201)
    json.NewEncoder(w).Encode(map[string]*WSMessage{"message": &msg})
}
```

Extract the sender lookup currently inline in the WS loop (`main.go:1267-1319`) into `func buildWSMessage(ctx, msgID, userID, content string, ts time.Time) WSMessage` and reuse it. Register route (needs `send_messages`):

```go
mux.Handle("POST /api/v1/channels/{id}/messages", withAuth(http.HandlerFunc(handleSendMessage), "send_messages"))
```

Note: `withAuth` reads the channel id for permission checks from the `?channel=` **query** param today (`main.go:723`). Confirm whether it also honors `PathValue("id")`; if not, either (a) extend the channel-scoped perm extraction in `withAuth` to also read `r.PathValue("id")`, or (b) have handlers pass `?channel=<id>`. **Preferred:** update `withAuth`'s channel extraction to fall back to `r.PathValue("id")` so all new `/channels/{id}/...` routes are permission-scoped correctly. Do this once here.

- [ ] **Step 3: Convert `handleWebSocket` to receive-only.** Delete the inbound `for { conn.ReadMessage() … INSERT … Publish }` block (`main.go:1228-1330`). Keep: upgrade, history send, Redis subscribe→client pump. Replace the read loop with a minimal drain that only detects disconnect (and, in Task 7, refreshes the presence heartbeat):

```go
// Keep the connection open; we no longer accept inbound messages.
for {
    if _, _, err := conn.ReadMessage(); err != nil { break } // client closed
}
```

- [ ] **Step 4: Point the frontend at REST.** In `useChatSessionStore.ts`, change `send` to POST and stop using `socket.send`:

```ts
send: async (content) => {
  const channelId = get().activeChannelId;
  if (!channelId || !content.trim()) return;
  await api(`/api/v1/channels/${channelId}/messages`, {
    method: "POST",
    body: JSON.stringify({ content }),
  });
  // No optimistic append: the message arrives back over the WS "message" event.
},
```

Keep the `ws.onmessage` `history`/`message` cases as-is (they already handle the echoed event). Ensure history order matches Task 2 (ascending).

- [ ] **Step 5: Update the Playwright chat test** to assert send-over-REST still round-trips over WS. Minimal shape:

```ts
test("message send round-trips over WS after REST post", async ({ page }) => {
  await page.goto("/chat");
  await page.getByPlaceholder(/Message #/).fill("hello node");
  await page.keyboard.press("Enter");
  await expect(page.locator(".mbody", { hasText: "hello node" })).toBeVisible();
});
```

- [ ] **Step 6: Verify (manual + e2e)**

Run backend gate: `podman run --rm -v ./backend:/app:z -w /app docker.io/library/golang:1.22 sh -c "go vet ./... && go test ./..."` → PASS.
Restart stack, then: `npm run dev` and `npx playwright test -g "round-trips"` → PASS. Confirm `podman logs telos-core` shows no `(Mock)` fallback and the POST 201s.

- [ ] **Step 7: Commit**

```bash
git add backend/main.go frontend/src/stores/useChatSessionStore.ts frontend/e2e
git commit -m "feat(chat): move send to REST POST; WebSocket is now receive-only fan-out

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

# Phase 1 — Message lifecycle + reactions

### Task 4: Edit own message

**Files:** Modify `backend/main.go` (add `handleEditMessage`, route); `frontend/src/stores/useChatSessionStore.ts`; `frontend/src/components/chat/ChatMessage.tsx` (created here or Task 6 — create now).

**Interfaces:**
- Produces: `PATCH /api/v1/channels/{id}/messages/{mid}` body `{content}` → `200 {message}`; publishes `WSEvent{Type:"message.update", Message}`.
- Store: `editMessage(id, content)`; reducer handles `message.update` by replacing the message in place.

- [ ] **Step 1: Backend handler** — only the author may edit; set `edited_at=now()`; re-broadcast full message.

```go
func handleEditMessage(w http.ResponseWriter, r *http.Request) {
    channelID, mid := r.PathValue("id"), r.PathValue("mid")
    user := r.Context().Value(userContextKey).(*UserContext)
    var body struct{ Content string `json:"content"` }
    _ = json.NewDecoder(r.Body).Decode(&body)
    body.Content = strings.TrimSpace(body.Content)
    if body.Content == "" || len(body.Content) > 4000 { http.Error(w, "invalid content", 400); return }
    var ts time.Time
    err := dbPool.QueryRow(r.Context(), `
        UPDATE messages SET content=$1, edited_at=now()
        WHERE id=$2 AND channel_id=$3 AND user_id=$4 AND deleted_at IS NULL
        RETURNING created_at`, body.Content, mid, channelID, user.ID).Scan(&ts)
    if err != nil { http.Error(w, "not found or not yours", 404); return }
    msg := buildWSMessage(r.Context(), mid, user.ID, body.Content, ts)
    msg.EditedAt = time.Now().Format("03:04 pm")
    publishChatEvent(r.Context(), channelID, WSEvent{Type: "message.update", Message: &msg})
    writeJSON(w, 200, map[string]*WSMessage{"message": &msg})
}
```

Route: `mux.Handle("PATCH /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleEditMessage), "send_messages"))`. (Add a tiny `writeJSON(w, status, v)` helper if one doesn't exist.)

- [ ] **Step 2: Store reducer + mutator**

```ts
// in ws.onmessage switch:
else if (n.type === "message.update" && n.message) {
  set((s) => ({ messages: s.messages.map((m) => (m.id === n.message!.id ? n.message! : m)) }));
}
// mutator:
editMessage: async (id, content) =>
  api(`/api/v1/channels/${get().activeChannelId}/messages/${id}`, {
    method: "PATCH", body: JSON.stringify({ content }),
  }),
```

- [ ] **Step 3: UI** — in `ChatMessage.tsx`, show an inline editor when the row is the user's own message and "edit" is chosen from the hover-actions (Task 5 adds the hover bar; wire this button now). Render `(edited)` when `m.editedAt`:

```tsx
{m.editedAt && <span className="mtime">(edited)</span>}
```

- [ ] **Step 4: Verify** — Go gate PASS; manual: edit your own message in two browser tabs, confirm both update live and `(edited)` shows. Editing someone else's returns 404 (curl check).
- [ ] **Step 5: Commit** `feat(chat): edit own messages with (edited) marker`.

---

### Task 5: Delete message (own + moderator) with hover actions

**Files:** Modify `backend/main.go` (`handleDeleteMessage`, route); `useChatSessionStore.ts`; `components/chat/ChatMessage.tsx`; `styles/chat.css`.

**Interfaces:**
- Produces: `DELETE /api/v1/channels/{id}/messages/{mid}` → `204`; publishes `WSEvent{Type:"message.delete", MessageID}`. Author OR `manage_messages` may delete.
- Store: `deleteMessage(id)`; reducer marks the message `deleted:true` (tombstone, not removal).

- [ ] **Step 1: Backend** — soft delete; authorize author-or-moderator:

```go
func handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
    channelID, mid := r.PathValue("id"), r.PathValue("mid")
    user := r.Context().Value(userContextKey).(*UserContext)
    isMod, _ := hasPermission(r.Context(), user, "manage_messages", nil)
    tag, err := dbPool.Exec(r.Context(), `
        UPDATE messages SET deleted_at=now(), content='', embed_kind=NULL, embed_ref=NULL, embed_snapshot=NULL
        WHERE id=$1 AND channel_id=$2 AND deleted_at IS NULL AND ($3 OR user_id=$4)`,
        mid, channelID, isMod, user.ID)
    if err != nil { http.Error(w, "delete failed", 500); return }
    if tag.RowsAffected() == 0 { http.Error(w, "not found or forbidden", 404); return }
    _, _ = dbPool.Exec(r.Context(), `DELETE FROM channel_pins WHERE message_id=$1`, mid) // unpin if pinned
    publishChatEvent(r.Context(), channelID, WSEvent{Type: "message.delete", MessageID: mid})
    w.WriteHeader(204)
}
```

Route: `withAuth(..., "view_channel")` (fine-grained author/mod check is inside).

- [ ] **Step 2: Store reducer + mutator** — reducer sets `deleted:true`, clears content/embed/reactions:

```ts
else if (n.type === "message.delete" && n.messageId) {
  set((s) => ({ messages: s.messages.map((m) =>
    m.id === n.messageId ? { ...m, deleted: true, content: "", embed: undefined, reactions: [] } : m) }));
}
```

- [ ] **Step 3: Hover-action bar + tombstone UI** in `ChatMessage.tsx` — an absolutely-positioned action row shown on `.msg:hover` with React (add-reaction), Edit (own), Pin (if `manage_messages`), Delete (own or mod). Tombstone renders `<p className="mbody deleted">message deleted</p>`. Add CSS to `chat.css`:

```css
.msg{position:relative}
.msgactions{position:absolute;top:-10px;right:8px;display:none;gap:4px;background:var(--surface-2);
  border:1px solid var(--line);border-radius:var(--r-xs);padding:2px}
.msg:hover .msgactions{display:flex}
.mbody.deleted{font-style:italic;color:var(--faint)}
```

Gate the mod-only actions on a `canModerate` flag from auth (add `permissions` to `useAuthStore` `me` payload if not present, or expose via a `usePermissions()` selector; check what `GET /auth/me` returns and extend if needed).

- [ ] **Step 4: Verify** — Go gate PASS. Manual: author deletes → tombstone in both tabs; a `manage_messages` user deletes another's message (204); a normal user deleting another's returns 404.
- [ ] **Step 5: Commit** `feat(chat): soft-delete messages (author + moderator) with hover actions`.

---

### Task 6: Reactions

**Files:** Modify `backend/main.go` (`handleAddReaction`, `handleRemoveReaction`, routes); `useChatSessionStore.ts`; create `components/chat/Reactions.tsx`; `styles/chat.css`.

**Interfaces:**
- Produces: `POST /api/v1/channels/{id}/messages/{mid}/reactions` body `{emoji}` → `200`; `DELETE …/reactions/{emoji}` → `204`. Both publish `WSEvent{Type:"reaction", Reaction:{messageId,emoji,userId,op,count}}`. `send_messages` required.
- Store: `toggleReaction(messageId, emoji)`; reducer updates that message's `reactions` from the event.

- [ ] **Step 1: Backend — validate emoji, upsert/delete, recompute count.** Add a pure `isEmoji(s string) bool` (length/rune guard: 1–8 runes, no ASCII control/letters — keep permissive but bounded) so it is unit-testable.

```go
func handleAddReaction(w http.ResponseWriter, r *http.Request) {
    channelID, mid := r.PathValue("id"), r.PathValue("mid")
    user := r.Context().Value(userContextKey).(*UserContext)
    var body struct{ Emoji string `json:"emoji"` }
    _ = json.NewDecoder(r.Body).Decode(&body)
    if !isEmoji(body.Emoji) { http.Error(w, "bad emoji", 400); return }
    _, err := dbPool.Exec(r.Context(), `
        INSERT INTO message_reactions (message_id,user_id,emoji) VALUES ($1,$2,$3)
        ON CONFLICT DO NOTHING`, mid, user.ID, body.Emoji)
    if err != nil { http.Error(w, "failed", 500); return }
    count := reactionCount(r.Context(), mid, body.Emoji)
    publishChatEvent(r.Context(), channelID, WSEvent{Type: "reaction",
        Reaction: &WSReaction{MessageID: mid, Emoji: body.Emoji, UserID: user.ID, Op: "add", Count: count}})
    w.WriteHeader(200)
}
// remove: DELETE FROM message_reactions WHERE message_id/user_id/emoji; Op:"remove"; publish new count.
// reactionCount: SELECT COUNT(*) FROM message_reactions WHERE message_id=$1 AND emoji=$2.
```

Routes: both `withAuth(..., "send_messages")`. `{emoji}` path segment is URL-encoded; `r.PathValue("emoji")` then `url.PathUnescape`.

- [ ] **Step 2: Store reducer** — apply add/remove to the target message's `reactions`, tracking the acting `userId` in `reactions[].users` so the client can compute "mine":

```ts
else if (n.type === "reaction" && n.reaction) {
  const { messageId, emoji, userId, op, count } = n.reaction;
  set((s) => ({ messages: s.messages.map((m) => {
    if (m.id !== messageId) return m;
    const reactions = [...(m.reactions ?? [])];
    const i = reactions.findIndex((x) => x.emoji === emoji);
    if (op === "add") {
      if (i === -1) reactions.push({ emoji, count, users: [userId] });
      else reactions[i] = { ...reactions[i], count, users: [...reactions[i].users, userId] };
    } else if (i !== -1) {
      const users = reactions[i].users.filter((u) => u !== userId);
      if (count <= 0) reactions.splice(i, 1); else reactions[i] = { ...reactions[i], count, users };
    }
    return { ...m, reactions };
  }) }));
}
// mutator toggleReaction(messageId, emoji): look at whether current user is in users[]; POST or DELETE.
```

- [ ] **Step 3: `Reactions.tsx`** — render pills (`.react`, `.on` when `users.includes(myId)`); clicking toggles; a "＋" pill opens the `EmojiPicker` (Task 11 — for now a small inline set `["🔥","📖","🌊","✅","👍","😂"]`, replaced by the full picker in Phase 3). CSS `.reacts/.react/.react.on` is already in `chat.css` from the mockup — verify it's present, add if missing.

- [ ] **Step 4: Unit-test `isEmoji`** in `main_test.go` (`"🔥"`→true, `"abc"`→false, `""`→false, an 80-char string→false). Run Go gate → PASS.
- [ ] **Step 5: Verify** — manual: react in tab A, see the pill increment in tab B; toggle off removes it; own-reaction shows `.on`.
- [ ] **Step 6: Commit** `feat(chat): emoji reactions with live counts`.

---

# Phase 2 — Presence + right channel panel

### Task 7: Redis presence lifecycle + members endpoint

**Files:** Modify `backend/main.go` (`handleWebSocket` connect/disconnect; add `presenceJoin`/`presenceLeave`/`channelRoster` helpers; `handleChannelMembers`, route).

**Interfaces:**
- Produces: on WS connect → user added to channel presence (refcounted, multi-tab safe) + realm online set; on disconnect → decremented; each change publishes `WSEvent{Type:"presence", Presence:{channelId,online,count}}` to `telos:chat:<cid>`.
- Produces: `GET /api/v1/channels/{id}/members` → `{online: PresenceUser[], count: int}` (initial paint before the first presence event).

Redis keys: `telos:presence:chan:<cid>` = HASH userId→refcount; `telos:presence:online` = SET of userIds currently connected anywhere. Heartbeat: `telos:presence:hb:<userId>` STRING EX 60, refreshed by a ping ticker; a Redis keyspace miss is not relied upon — cleanup is by explicit decrement on disconnect, heartbeat guards crashed connections during roster reads.

- [ ] **Step 1: Presence helpers**

```go
func presenceJoin(ctx context.Context, cid, uid string) {
    redisClient.HIncrBy(ctx, "telos:presence:chan:"+cid, uid, 1)
    redisClient.SAdd(ctx, "telos:presence:online", uid)
    redisClient.Set(ctx, "telos:presence:hb:"+uid, "1", 60*time.Second)
}
func presenceLeave(ctx context.Context, cid, uid string) {
    n, _ := redisClient.HIncrBy(ctx, "telos:presence:chan:"+cid, uid, -1).Result()
    if n <= 0 { redisClient.HDel(ctx, "telos:presence:chan:"+cid, uid) }
    // realm online: remove only if no channel holds this user
    // (simple approach: recompute membership lazily in channelRoster; or keep a global refcount hash)
}
```

Use a **global refcount hash** `telos:presence:refs` too (HIncrBy on join, HDecrBy on leave; SREM from `:online` when it hits 0) to make realm-count correct across channels/tabs. Keep it simple and correct.

- [ ] **Step 2: Wire into `handleWebSocket`** — after subscribe, `presenceJoin(ctx, channelIDStr, userID)`, publish roster; `defer presenceLeave(...)` + publish. Add a heartbeat ticker refreshing `hb:<uid>` every 25s in the read-drain goroutine.

- [ ] **Step 3: `channelRoster(ctx, cid) ([]PresenceUser, int)`** — read the channel hash fields with refcount>0, filter to those with a live `hb` key, join `users` + first role for display, and `SCARD`/hash-count the realm online. `handleChannelMembers` returns it. Initialize slices to `[]PresenceUser{}`.

Route: `mux.Handle("GET /api/v1/channels/{id}/members", withAuth(http.HandlerFunc(handleChannelMembers), "view_channel"))`.

- [ ] **Step 4: Verify** — manual: open `/chat` in two tabs as two users; `curl -s .../api/v1/channels/<id>/members` (with a session cookie) shows both; close one tab → roster drops after disconnect. Confirm the topbar count via the presence event in `podman logs`.
- [ ] **Step 5: Commit** `feat(chat): Redis presence (multi-tab refcount) + channel members endpoint`.

---

### Task 8: Presence in the client + render the right channel panel

**Files:** Create `components/chat/ChatAside.tsx`; modify `components/AppShell.tsx` (render aside on `/chat`; topbar count); modify `useChatSessionStore.ts` (presence state + `members` fetch + reducer); `styles/chat.css` (`.aside`, `.memrow`, `.specrow`, `.pin` — most exist in `shell.css`/mockup, verify).

**Interfaces:**
- Consumes: `GET /channels/{id}/members`; `presence` WS events; `GET /channels/{id}/pins` (Task 9 — render empty for now).
- Produces: store fields `online: PresenceUser[]`, `onlineCount: number`; `ChatAside` component.

- [ ] **Step 1: Store presence state** — on `connect`, after opening the WS, `api('/channels/<id>/members')` to seed `online`/`onlineCount`; add a `presence` case to `ws.onmessage`:

```ts
else if (n.type === "presence" && n.presence) {
  set({ online: n.presence.online, onlineCount: n.presence.count });
}
```

- [ ] **Step 2: `ChatAside.tsx`** — three blocks matching the mockup: **Pinned** (`.pin` items from a `pins` store field, Task 9), **Channel** (`.specrow` members/shelf/created — use `online.length`/static until a stats endpoint exists; keep minimal), **Online — N** (`.memrow` list from `online`, `.dot` colored `--cyan` for the oracle/AI account, default otherwise). Reuse `avatarHue`/avatar rendering from `chat/page.tsx` (extract to `lib/avatar.ts`).

- [ ] **Step 3: Render aside in `AppShell`** — add as a third `.shellbody` child, only on chat:

```tsx
<main className="arena">{children}</main>
{onChat && <ChatAside />}
```

`.aside` collapses below tablet width (mirror the existing `.rail`/`app.css` responsive rule — add `.aside{display:none}` in the same media query).

- [ ] **Step 4: Topbar "N online"** — replace the static chip content with `onlineCount` from the chat store when authenticated (mockup shows "6 online").

- [ ] **Step 5: Verify** — manual: two tabs → aside "Online — 2", topbar "2 online"; both update on join/leave. Responsive: narrow viewport hides the aside. `npx playwright test` (existing specs) → still green.
- [ ] **Step 6: Commit** `feat(chat): render right channel panel + live presence roster`.

---

### Task 9: Pin / unpin

**Files:** Modify `backend/main.go` (`handlePinMessage`, `handleUnpinMessage`, `handleListPins`, routes); `useChatSessionStore.ts`; `ChatAside.tsx`; `ChatMessage.tsx` (pin action).

**Interfaces:**
- Produces: `POST /api/v1/channels/{id}/pins` body `{messageId}` → `201`; `DELETE /api/v1/channels/{id}/pins/{mid}` → `204`; `GET /api/v1/channels/{id}/pins` → `{pins: PinnedMessage[]}`. Pin/unpin require `manage_messages`; list requires `view_channel`. Pin/unpin publish `WSEvent{Type:"pin", Pin:{messageId,op}}`.
- Store: `pins: PinnedMessage[]`; `pinMessage(id)`, `unpinMessage(id)`; reducer updates message `.pinned` + refetches `pins`.

- [ ] **Step 1: Backend** — insert/delete `channel_pins`; `handleListPins` returns pinned messages (title/subtitle snapshot from message content, truncated). Publish pin event carrying `op`.
- [ ] **Step 2: Store** — `pin` reducer toggles `m.pinned` and re-fetches the pins list for the aside; seed `pins` on connect via `GET /channels/{id}/pins`.
- [ ] **Step 3: UI** — pin/unpin in the message hover bar (gated on `manage_messages`); `ChatAside` "Pinned" block renders `pins` as `.pin` rows (mockup markup). Clicking a pin scrolls to the message (best-effort by id).
- [ ] **Step 4: Verify** — manual: moderator pins → appears in aside for all; unpin removes it; non-mod has no pin control and `POST /pins` returns 403.
- [ ] **Step 5: Commit** `feat(chat): pin/unpin messages surfaced in the channel panel`.

---

# Phase 3 — Rich composer (markdown, emoji, mentions)

### Task 10: Safe markdown rendering

**Files:** Create `components/chat/MessageBody.tsx`; modify `ChatMessage.tsx`; `frontend/package.json`; `styles/chat.css`.

**Interfaces:**
- Produces: `<MessageBody content={string} />` — renders a **restricted, sanitized** markdown subset (bold, italic, inline code, code block, links, unordered/ordered lists, blockquote). No raw HTML, no images (embeds are the sanctioned rich content). Matches mockup `.mbody b{...}`.

- [ ] **Step 1: Add deps** — `cd frontend && npm i react-markdown rehype-sanitize remark-gfm`. (Verify these are compatible with the Next 16 static export — they are client-side only; import inside a `"use client"` component.)
- [ ] **Step 2: `MessageBody.tsx`** — restrict the schema and allowed elements:

```tsx
"use client";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";

const schema = { ...defaultSchema, tagNames:
  ["p","strong","em","code","pre","a","ul","ol","li","blockquote","br"],
  attributes: { ...defaultSchema.attributes, a: ["href"] } };

export function MessageBody({ content }: { content: string }) {
  return (
    <div className="mbody">
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[[rehypeSanitize, schema]]}
        components={{ a: (p) => <a {...p} target="_blank" rel="noreferrer noopener" /> }}>
        {content}
      </ReactMarkdown>
    </div>
  );
}
```

Ensure links open safely and `javascript:` URLs are stripped (rehype-sanitize handles protocol filtering by default — verify with the test below).

- [ ] **Step 3: Test (Playwright or a tiny vitest if configured)** — assert `**bold**` renders `<strong>`, and a message containing `<img src=x onerror=alert(1)>` renders as inert text (no `<img>`, no script). If no unit runner exists, add a Playwright assertion posting such a message and checking `page.locator("img")` count is unchanged and the text is escaped.
- [ ] **Step 4: Swap** `chat/page.tsx`/`ChatMessage.tsx` to use `<MessageBody>` instead of `<p className="mbody">{m.content}</p>` (skip for `m.deleted`). Verify `.mbody` spacing still matches (adjust `chat.css` so `.mbody p` inherits the old `.mbody` rules).
- [ ] **Step 5: Verify + Commit** — `npm run lint` clean; XSS assertion green. Commit `feat(chat): sanitized markdown rendering in messages`.

---

### Task 11: Emoji picker (composer + reactions)

**Files:** Create `components/chat/EmojiPicker.tsx`; modify `Composer.tsx`, `Reactions.tsx`; `frontend/package.json` (if using a lib); `styles/chat.css`.

**Interfaces:**
- Produces: `<EmojiPicker onPick={(emoji:string)=>void} onClose={()=>void} />` popover. Used by the composer `smile` button (inserts into draft at cursor) and by the reactions "＋" pill (calls `toggleReaction`).

- [ ] **Step 1: Choose implementation** — prefer `emoji-mart` (`@emoji-mart/react` + `@emoji-mart/data`) for completeness; if bundle size is a concern for the static export, hand-roll a compact categorized grid (the mockup only needs common emoji). Decision: use `@emoji-mart/react` (client-only import). `npm i emoji-mart @emoji-mart/data @emoji-mart/react`.
- [ ] **Step 2: `EmojiPicker.tsx`** — wrap the picker in a positioned popover with an outside-click close; theme it via the `theme` store (`ink`→light, `synthwave`→dark).
- [ ] **Step 3: Wire composer** — the `smile` button toggles the picker; `onPick` inserts the emoji into `draft` at the caret and refocuses the input.
- [ ] **Step 4: Wire reactions** — the "＋" pill opens the same picker; `onPick` → `toggleReaction(messageId, emoji)`; replace the temporary inline emoji set from Task 6.
- [ ] **Step 5: Verify + Commit** — manual: pick an emoji into a message; add an arbitrary emoji reaction. `npm run lint` clean. Commit `feat(chat): emoji picker for composer and reactions`.

---

### Task 12: @mention autocomplete

**Files:** Modify `backend/main.go` (`handleUserSearch`, route); create `components/chat/MentionAutocomplete.tsx`; modify `Composer.tsx`; `styles/chat.css`.

**Interfaces:**
- Produces: `GET /api/v1/users/search?q=<prefix>&limit=8` → `[{id,username,displayName,avatarUrl}]` (case-insensitive prefix on username/display_name; `view_channel` required).
- Produces: composer detects a `@<partial>` token at the caret, queries, shows a dropdown, and on select inserts `@username ` (canonical username, used by Phase 5 mention parsing).

- [ ] **Step 1: Backend search** — parameterized `ILIKE $1||'%'`, `LIMIT`, return `[]` initialized. Do not leak inactive users (filter `is_active` if present).
- [ ] **Step 2: `MentionAutocomplete.tsx`** — controlled by the composer: given the current `@`-token, fetch (debounced ~150ms), render up to 8 rows with avatar + name; keyboard up/down/enter/escape; on pick call back with the username.
- [ ] **Step 3: Composer integration** — track caret, extract the active `@token` via regex `/(^|\s)@(\w{0,20})$/`, show the dropdown anchored above the input, replace the token on pick.
- [ ] **Step 4: Verify + Commit** — manual: typing `@ma` lists matching users; selecting inserts `@mara `. Commit `feat(chat): @mention autocomplete in composer`.

---

# Phase 4 — Media-share embeds

### Task 13: Single-item metadata endpoints

**Files:** Modify `backend/main.go` (`handleLibraryBookByID`, `handleMediaItemByID`, routes).

**Interfaces:**
- Produces: `GET /api/v1/library/books/{id}` → `{id,title,authors,pages,coverUrl}` (from Grimmory list/lookup, Redis-cached like existing library calls); `GET /api/v1/media/items/{id}` → `{id,title,year,director,durationSec,kind,coverUrl}` (from Jellyfin). Both feed embed snapshots. `view_library`/`view_media` respectively.

- [ ] **Step 1: Library book by id** — reuse the existing Grimmory client path (`handleLibraryBooks` internals); if Grimmory lacks a by-id route, fetch the list and select (cache per-id under `telos:library:book:<id>`). Force the correct cover content-type as the existing cover handler does.
- [ ] **Step 2: Media item by id** — call Jellyfin `Users/{uid}/Items/{id}` (admin token) or reuse the `handleMediaItems` path filtered by id; map to the response shape; cache under `telos:jellyfin:item:<id>`.
- [ ] **Step 3: Verify + Commit** — `curl` each with a session cookie returns the metadata (watch logs for `(Mock)` fallback). Commit `feat(chat): single-item Library/Stream metadata endpoints for embeds`.

---

### Task 14: Attach + send an embed

**Files:** Modify `backend/main.go` (`handleSendMessage` embed branch; a `buildEmbedSnapshot` helper); create `components/chat/SharePicker.tsx`; modify `Composer.tsx`; `useChatSessionStore.ts`.

**Interfaces:**
- Consumes: Task 13 metadata endpoints.
- Produces: `POST /channels/{id}/messages` now accepts `embed:{kind,ref}`; server resolves the snapshot server-side (never trust client snapshot) and stores `embed_kind/ref/snapshot`; the broadcast `WSMessage.Embed` is populated. `SharePicker` UI lets the `+` menu attach a book/film/file.

- [ ] **Step 1: Server embed resolution** — in `handleSendMessage`, when `body.Embed != nil`, validate `kind ∈ {library_book,stream_film,file}`, call the matching metadata endpoint/helper to build `snapshot` JSON, and include the columns in the INSERT. Attach `MessageEmbed` to the outgoing `WSMessage`.
- [ ] **Step 2: `+` menu** — the composer `+` (currently decorative) opens a small menu: "Share from Library", "Share from Stream", "Upload file" (Upload reuses `POST /api/v1/files` then attaches a `file` embed). First two open `SharePicker`.
- [ ] **Step 3: `SharePicker.tsx`** — a modal that searches Library (`GET /library/books?q=`) or Stream (`GET /media/items?q=`), shows results, and on pick sets a staged `embed:{kind,ref}` in the composer; sending posts content+embed and clears the staging.
- [ ] **Step 4: Verify + Commit** — manual: share a book from the picker; the message posts with an embed; it round-trips over WS with a populated `embed`. Commit `feat(chat): attach Library/Stream/file embeds to messages`.

---

### Task 15: Render embed cards + "Share to chat" from modules

**Files:** Create `components/chat/ShareCard.tsx`; modify `ChatMessage.tsx`; modify `app/(shell)/library/page.tsx` + `app/(shell)/stream/page.tsx` (a "Share to chat" action); `styles/chat.css` (`.share` block — lift from mockup).

**Interfaces:**
- Consumes: `WSMessage.Embed`.
- Produces: `<ShareCard embed={MessageEmbed} />` with `book`/`film`/`file` variants and actions; a cross-module "Share to chat" affordance that opens a channel picker and stages an embed in the composer.

- [ ] **Step 1: `ShareCard.tsx`** — render per the mockup: `.share` with `.cover` (book gradient / film thumbnail with `.play` + `.dur` / file icon) and `.meta` (`.st` kicker, `.sttl` title, `.ssub` subtitle) and `.sact` action buttons. Actions:
  - book → **Add to shelf** (existing library progress/shelf action or navigate to the reader), **Discuss** (no-op/scroll).
  - film → **Watch party** → navigate to/join the channel's voice room (existing `useVoiceSessionStore.join`), **Queue** → navigate to Stream with the item. *Synced playback is out of scope (deferred); Watch party only opens voice for now — label it accordingly.*
  - file → **Download** (existing `GET /files/{id}/download`).
- [ ] **Step 2: Render in messages** — in `ChatMessage.tsx`, render `<ShareCard>` under the body when `m.embed && !m.deleted`.
- [ ] **Step 3: "Share to chat" from Library/Stream** — add a small action on a book/film card that opens a lightweight channel-picker popover; on pick, route to `/chat`, `connect(channelId)`, and stage `embed:{kind,ref}` in the composer store. Keep it minimal (reuse the chat store).
- [ ] **Step 4: Verify + Commit** — manual: shared book/film render as cards in both themes; "Watch party" opens the voice channel; "Share to chat" from Library stages an embed. `npm run lint` clean. Commit `feat(chat): media-share cards + share-to-chat from Library/Stream`.

---

# Phase 5 — Unread badges + @mention notifications

### Task 16: Read cursors, activity feed, per-user events WS, mention parsing

**Files:** Modify `backend/main.go` (`handleMarkRead`, `handleUnreadCounts`, `handleEventsWebSocket`, `handleListNotifications`, `handleMarkNotificationsRead`, routes; mention parsing in `handleSendMessage`; `parseMentions` pure helper).

**Interfaces:**
- Produces:
  - `POST /api/v1/channels/{id}/read` body `{lastMessageId}` → `204` (upsert `channel_reads`).
  - `GET /api/v1/channels/unread` → `[{channelId, unread:int, mention:bool}]`.
  - `GET /api/v1/notifications` → `[{id,kind,channelId,messageId,actorId,actorName,preview,createdAt,readAt}]`.
  - `POST /api/v1/notifications/read` body `{ids?:string[]}` (empty = all) → `204`.
  - `GET /api/v1/events/ws` — per-user WS subscribing to `telos:user:<uid>` and `telos:activity`; relays `{type:"notification", ...}` and `{type:"activity", channelId, messageId}`.
- Producer behavior: `handleSendMessage` parses `@username` tokens → resolves user ids → inserts `notifications` rows → publishes a `notification` event to each mentioned user's `telos:user:<uid>` channel; and publishes `{type:"activity", channelId, messageId}` to `telos:activity` for unread bumps.

Scale note: `telos:activity` is a single global fan-out (every connected client bumps unread for non-active channels). Acceptable for Telos's single-community, single-node scale; documented here as a deliberate simplification (a per-member fan-out would be the large-scale alternative).

- [ ] **Step 1: `parseMentions(content string) []string`** (pure, unit-tested) — regex `@(\w{1,32})`, dedupe, return usernames. Test: `"hi @mara and @jules @mara"` → `["mara","jules"]`.
- [ ] **Step 2: Mention pipeline in `handleSendMessage`** — after insert+broadcast, resolve usernames→ids (`SELECT id,username FROM users WHERE username = ANY($1)`), insert a `notifications` row per mentioned id (skip self), and `redisClient.Publish("telos:user:"+id, notifJSON)`. Then publish activity to `telos:activity`.
- [ ] **Step 3: Read + unread endpoints** — `handleMarkRead` upserts `channel_reads(user,channel,last_read_message_id,last_read_at=now())`. `handleUnreadCounts` computes, per channel the user can view, `COUNT(messages after last_read)` and whether any of those mention the user (join `notifications` unread). Return `[]` initialized.
- [ ] **Step 4: `handleEventsWebSocket`** — upgrade, subscribe to both `telos:user:<uid>` and `telos:activity`, pump to client; drain read loop for disconnect. Route `GET /api/v1/events/ws` with `withAuth(..., "")`.
- [ ] **Step 5: Notifications list/read endpoints.**
- [ ] **Step 6: Unit tests** (`parseMentions`) + Go gate → PASS.
- [ ] **Step 7: Commit** `feat(chat): read cursors, unread counts, mention notifications, per-user events WS`.

---

### Task 17: Unread badges in the rail + mark-read on view

**Files:** Modify `frontend/src/stores/useNotificationsStore.ts` (create); `components/AppShell.tsx`; `useChatSessionStore.ts`.

**Interfaces:**
- Consumes: `GET /channels/unread`, `/events/ws` activity events, `POST /channels/{id}/read`.
- Produces: `useNotificationsStore` with `unread: Record<channelId, {count,mention}>`, `connectEvents()`, `markRead(channelId, lastMessageId)`.

- [ ] **Step 1: `useNotificationsStore`** — on auth, `connectEvents()` opens `/events/ws`; seed `unread` from `GET /channels/unread`; on `activity` event bump `unread[channelId]` unless it's the active chat channel; on `notification` event set `mention:true` + push to the feed (Task 18).
- [ ] **Step 2: AppShell rail badges** — render a count badge on each rail channel from `unread`; style `.chanbadge` (small pill, `--rose` when `mention`, muted otherwise). Add CSS.
- [ ] **Step 3: Mark-read on view** — when a channel becomes active (or a new message arrives while active), call `markRead(channelId, latestMessageId)` and clear `unread[channelId]` locally.
- [ ] **Step 4: Verify** — manual: user B posts in a channel A isn't viewing → badge appears for A; opening it clears the badge; a message mentioning A shows a rose (mention) badge. `npx playwright test` green.
- [ ] **Step 5: Commit** `feat(chat): per-channel unread badges with mark-read on view`.

---

### Task 18: Notifications bell dropdown

**Files:** Create `components/NotificationsBell.tsx`; modify `AppShell.tsx`; `styles/chat.css` (or a small `notifications.css`).

**Interfaces:**
- Consumes: `GET /notifications`, `POST /notifications/read`, live `notification` events from `useNotificationsStore`.
- Produces: bell button with an unread dot + a dropdown list; clicking an item routes to the channel/message and marks it read.

- [ ] **Step 1: `NotificationsBell.tsx`** — the existing topbar bell becomes a toggle; dropdown lists recent notifications (`actorName mentioned you in #channel: preview`, relative time); an unread dot when any are unread.
- [ ] **Step 2: Wire** — open loads `GET /notifications`; clicking an item routes to `/chat`, `connect(channelId)`, marks that id read; a "mark all read" clears via `POST /notifications/read` (empty body).
- [ ] **Step 3: Live arrival** — new `notification` events (from `/events/ws`) prepend to the list and light the dot without a refetch.
- [ ] **Step 4: Verify + Commit** — manual: get mentioned → bell dot lights, dropdown shows it, clicking navigates + clears. Commit `feat(chat): notifications bell with live @mention feed`.

---

## Self-Review

**Spec coverage vs. the approved design:**
- Reactions → Task 6 ✓ · Edit/Delete/Pin → Tasks 4/5/9 ✓ (all four message actions the user selected) · Presence + right panel → Tasks 7/8 ✓ · Emoji picker → Task 11 ✓ · Markdown → Task 10 ✓ · @mention autocomplete → Task 12 ✓ · Media-share cards (Library/Stream/file + share-to-chat) → Tasks 13/14/15 ✓ · Unread badges + @mention notifications → Tasks 16/17/18 ✓ · Oracle → **explicitly deferred** (stated in Goal + Task 15 watch-party note) ✓.
- Architecture (Approach A: WS receive-only + REST mutations + Redis publish) → Task 3 establishes it; every later mutation follows it ✓.

**Placeholder scan:** No "TBD"/"handle edge cases" left as work items. Two intentional decision-notes are flagged inline (history ordering in Task 2; `telos:activity` global fan-out scale note in Task 16) — both resolved to a concrete choice, not deferred.

**Type consistency:** `WSEvent`/`WSMessage`/`WSReaction`/`WSPin`/`WSPresence`/`PresenceUser`/`ReactionSummary`/`MessageEmbed` are defined once in Task 2 and consumed by name thereafter. Store reducer event-type strings (`message`, `message.update`, `message.delete`, `reaction`, `pin`, `presence`) match the Go `WSEvent.Type` values. Endpoint paths are consistent (`/api/v1/channels/{id}/...`). `manage_messages` (introduced in Task 1) is the permission checked in Tasks 5/9.

**Known cross-cutting item for the executor:** several tasks gate UI on the current user's permissions (`manage_messages`) and id. Confirm `GET /api/v1/auth/me` exposes `permissions` and `id`; if it does not, extend it in Task 5 (first task that needs it) rather than duplicating the check later.

---

## Execution Handoff

Each phase is independently shippable — consider one PR per phase (Phase 0 first; it is a prerequisite for all others).
