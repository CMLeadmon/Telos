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
| Reading progress (`book_progress`) | Deleted |
| Reactions (`message_reactions`) | Deleted |
| Channel read state (`channel_reads`) | Deleted |
| Notifications (`notifications`) | Deleted |
| User-event stream (`user_events` where `recipient_id`) | Deleted |
| Private annotations (`annotations` where `visibility = 'private'`) | Deleted |
| My List entries + revision (`media_list_entries`, `media_lists`) | Deleted |
| Watch Party membership/invitations/host offers | Deleted; hosted parties are ended |
| Community annotations and replies | Retained, authored by the anonymized user |
| Private uploads and avatars (`files` where `purpose <> 'shared'`) | Row deleted; physical asset queued for removal |
| Shared community files (`files` where `purpose = 'shared'`) | Retained |
| Public chat messages | Retained, authored by the anonymized user |
| User row | Anonymized (`deleted-<id>`, display "Deleted User", disabled, no avatar) |

Public authorship is retained only in anonymized form: the `users` row survives
so message foreign keys render as "Deleted User" rather than breaking history.

## Physical asset deletion

Private avatar and upload assets are removed after the database transaction
records the deletion job (`asset_deletion_jobs`). Jobs run after commit through
an `AssetRemover`; a failed job stays pending for the reconciliation pass so
database and filesystem state converge. The Phase 4 storage layer supplies the
filesystem-backed remover.

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

Later phases extend this matrix (annotations, My List, Watch Party membership)
by extending the same deletion fixture before passing.
