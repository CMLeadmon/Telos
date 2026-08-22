# API List Inventory

Every JSON array or decoded upstream collection returned by the gateway is
registered here with its authorization predicate, ordering, and either a
cursor policy or a hard cap. The list-policy registry (`backend/pagination.go`)
is the machine-readable source of truth; `TestListPolicyRegistryCompleteness`
fails if a scope below is unregistered, and later phases must extend both.

## Cursor-paginated lists (default 50, max 100)

| Scope | Route | Auth | Order | Seek tuple |
|---|---|---|---|---|
| `chat.history` | `GET /api/v1/channels/{id}/messages` | channel view | `created_at DESC, id DESC` | `(created_at, id)` |
| `admin.users` | `GET /api/v1/admin/users` | manage_members | `created_at DESC, id DESC` | `(created_at, id)` |
| `admin.invites` | `GET /api/v1/admin/invites` | manage_members | `created_at DESC, token_hash DESC` | `(created_at, token_hash)` |
| `admin.sessions` | `GET /api/v1/users/me/sessions` | self | `created_at DESC, token_hash DESC` | `(created_at, token_hash)` |
| `channel.members` | `GET /api/v1/channels/{id}/members` | channel view | presence roster | `(joined, id)` |

Cursors are canonical-JSON/base64url envelopes, HMAC-SHA256 signed with an
active/retiring keyring (`TELOS_CURSOR_KEYS_FILE`, mode 0600, each key ≥32
random bytes), and expire after 24 hours. Decode rejects a wrong
scope/version/sort/key or an expired/tampered token with `400 invalid_cursor`.

## Deliberately finite lists (hard cap)

| Scope | Cap | Notes |
|---|---|---|
| `channels` | 100 | authorized channels for the user |
| `roles` | 100 | |
| `permissions` | 64 | |
| `message.reactions` | 100 | per message |
| `channel.pins` | 100 | per channel |
| `search.users` / `search.channels` / `search.files` / `search.media` / `search.library` | 15 | per scope |

## Reserved for later phases

Future audit, catalog, and reconciliation lists must register before they are
returned. An unregistered array response fails CI.
