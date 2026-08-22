# Data Retention and Account Deletion

Telos treats community data as durable and confidential. Account deletion is an
idempotent lifecycle (`backend/account_lifecycle.go`, migration 0011): a repeat
request returns the same `DeletionReceipt`, and every session and socket is
invalidated before success is reported.

## Retention matrix

| Data | On account deletion |
|---|---|
| Sessions | Revoked (and live sockets closed) |
| Invites created by the user | Marked used/consumed |
| Preferences (`user_preferences`) | Deleted |
| Member continuity (`member_progress`) | Deleted; rows are member-owned and keyed by stable catalog UUID |
| Legacy reading progress (`book_progress`, compatibility period) | Deleted |
| Reactions (`message_reactions`) | Deleted |
| Channel read state (`channel_reads`) | Deleted |
| User-event stream (`user_events` where `recipient_id`) | Deleted |
| Private annotations (`annotations` where `visibility = 'private'`) | Deleted |
| Channel permission overrides (`channel_permission_overrides`) | Cascade-deleted when the role or channel is removed |
| Community annotations and replies | Retained, authored by the anonymized user |
| Private uploads and avatars (`files` where `purpose <> 'shared'`) | Logical row deleted; the default no-op remover records the job as done without claiming physical removal |
| Shared community files (`files` where `purpose = 'shared'`) | Retained |
| Public chat messages | Retained, authored by the anonymized user |
| User row | Anonymized (`deleted-<id>`, display "Deleted User", disabled, no avatar) |

Public authorship is retained only in anonymized form: the `users` row survives
so message foreign keys render as "Deleted User" rather than breaking history.
Stable shared `catalog_items` and `catalog_sources` survive account deletion;
only the deleted member's `member_progress` rows are removed. A successful
shared Grimmory book deletion instead removes both progress representations for
that resolved catalog item in one transaction. An upstream deletion failure
preserves both.

## Physical asset deletion status

Account deletion records `asset_deletion_jobs` after the transaction. In the
current production wiring, `noopAssetRemover` succeeds without removing the
physical asset, so the asynchronous job is marked `done`; it does not remain
pending and it is not evidence of physical deletion. Physical asset deletion
and reconciliation are blocked until Phase 4 supplies a real remover and its
operational evidence.

## Durable event

Account deletion enqueues an `account_deleted` security event in the same
transaction (transactional outbox), which Phase 5 materializes for
administrators.

## Channel change log

The per-channel `channel_changes` log (migration 0014) records compact
`message.created`/`updated`/`deleted` entries with a strictly-increasing
sequence, used only for socket catch-up. It is **retained for 7 days** and
pruned in bounded batches by the reconciler. A catch-up cursor older than the
retained floor returns `410 change_cursor_expired` with the current high-water
so the client fully resynchronizes rather than receiving a partial page
presented as complete.

## File catalog

Every file row (migration 0012) carries a `purpose` (`shared`/`avatar`/
`book_ingest`), a `visibility` (`private`/`community`), and a lifecycle `state`
(`staged` → `validating` → `scanning` → `promoting` → `available`, plus
`quarantined`, `missing`, `deleting`, `deleted`, `consumed`, and the book
handoff states). General listing, search, and download expose only
`purpose='shared'`, `state='available'`, `scan_status='clean'` rows that are
`community` or owned by the viewer (`FileStore.FindAvailable`/`ListVisible`).
Infected, avatar, book-ingestion, nonterminal, and other users' private rows
never leak. Logical folders are database-only nodes with normalized sibling
uniqueness; physical storage stays content-addressed. Every file/folder action
writes an immutable `file_audit` row.

Any future account-owned data must extend the same deletion fixture before it
is released.
