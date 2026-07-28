-- Migration 0020: remove voice rooms, Watch Party, the notification inbox, and
-- My List, and collapse the role set to four. Destructive and irreversible.
-- Applied only after all code referencing these objects is gone (this repo's
-- backend and frontend no longer reference any of them as of this migration).

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
-- both declare ON DELETE CASCADE, so per-role grants and per-channel voice
-- overrides are removed by this single delete. Do not add explicit deletes.
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
-- cascades on role deletion, so deleting a role first would silently strip its
-- members of every capability instead of moving them.
INSERT INTO user_roles (user_id, role_id)
SELECT user_id, 'Member' FROM user_roles WHERE role_id = 'Contributor'
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (user_id, role_id)
SELECT user_id, 'Moderator' FROM user_roles WHERE role_id = 'Librarian'
ON CONFLICT DO NOTHING;

DELETE FROM roles WHERE id IN ('Contributor', 'Librarian');
