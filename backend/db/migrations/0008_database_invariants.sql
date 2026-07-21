-- Migration 0008: relational invariants, leading foreign-key indexes, and
-- corrected permission grants.

-- ── Leading indexes for every foreign-key join/delete path not already covered
-- by the leading column of a primary key or unique index. ─────────────────────
CREATE INDEX IF NOT EXISTS idx_cro_role ON channel_role_overrides (role_id);
CREATE INDEX IF NOT EXISTS idx_messages_user ON messages (user_id);
CREATE INDEX IF NOT EXISTS idx_reactions_user ON message_reactions (user_id);
CREATE INDEX IF NOT EXISTS idx_pins_message ON channel_pins (message_id);
CREATE INDEX IF NOT EXISTS idx_pins_pinned_by ON channel_pins (pinned_by);
CREATE INDEX IF NOT EXISTS idx_reads_channel ON channel_reads (channel_id);
CREATE INDEX IF NOT EXISTS idx_reads_last_message ON channel_reads (last_read_message_id);
CREATE INDEX IF NOT EXISTS idx_notifications_channel ON notifications (channel_id);
CREATE INDEX IF NOT EXISTS idx_notifications_message ON notifications (message_id);
CREATE INDEX IF NOT EXISTS idx_notifications_actor ON notifications (actor_id);
CREATE INDEX IF NOT EXISTS idx_files_uploader ON files (uploader_id);
CREATE INDEX IF NOT EXISTS idx_users_avatar_file ON users (avatar_file_id);
CREATE INDEX IF NOT EXISTS idx_invites_role ON invites (role_id);
CREATE INDEX IF NOT EXISTS idx_invites_creator ON invites (creator_id);
CREATE INDEX IF NOT EXISTS idx_invites_used_by ON invites (used_by);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_role_permissions_permission ON role_permissions (permission_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role ON user_roles (role_id);

-- ── Named CHECK constraints backing security/lifecycle assumptions. On a live
-- volume with legacy data these should be added NOT VALID then validated; on a
-- fresh beta database they validate immediately over empty tables. ────────────
ALTER TABLE users
  ADD CONSTRAINT chk_users_username_canonical
  CHECK (username ~ '^[a-z0-9][a-z0-9_.-]{2,31}$');

ALTER TABLE channels
  ADD CONSTRAINT chk_channels_type CHECK (type IN ('text', 'voice'));

ALTER TABLE channel_role_overrides
  ADD CONSTRAINT chk_cro_masks CHECK (allow_mask >= 0 AND deny_mask >= 0);

ALTER TABLE messages
  ADD CONSTRAINT chk_messages_content_length CHECK (char_length(content) <= 4000);

ALTER TABLE sessions
  ADD CONSTRAINT chk_sessions_dates CHECK (expires_at > created_at);

ALTER TABLE invites
  ADD CONSTRAINT chk_invites_dates CHECK (expires_at > created_at);

ALTER TABLE files
  ADD CONSTRAINT chk_files_size CHECK (size_bytes >= 0);
ALTER TABLE files
  ADD CONSTRAINT chk_files_scan_status
  CHECK (scan_status IN ('pending', 'clean', 'infected', 'failed'));

ALTER TABLE book_progress
  ADD CONSTRAINT chk_book_progress_percent CHECK (percent >= 0 AND percent <= 100);

ALTER TABLE user_preferences
  ADD CONSTRAINT chk_prefs_object CHECK (jsonb_typeof(prefs) = 'object');

ALTER TABLE notifications
  ADD CONSTRAINT chk_notifications_kind CHECK (char_length(kind) BETWEEN 1 AND 40);

-- Defence-in-depth unique index on the canonical (lowercased) username.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower ON users (lower(username));

-- ── Corrected permission grants. `manage_messages` gates pin/moderation routes
-- but was never seeded; add it and grant to Owner/Administrator/Moderator,
-- preserving any custom grants. Correct the manage_members description to the
-- shipped disable/enable behavior (no administrative password replacement). ───
INSERT INTO permissions (id, description) VALUES
  ('manage_messages', 'Pin, unpin, and moderate messages in channels')
ON CONFLICT (id) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r, 'manage_messages'
FROM (VALUES ('Owner'), ('Administrator'), ('Moderator')) AS grants(r)
ON CONFLICT (role_id, permission_id) DO NOTHING;

UPDATE permissions
SET description = 'Disable or re-enable member accounts (no password replacement)'
WHERE id = 'manage_members';
