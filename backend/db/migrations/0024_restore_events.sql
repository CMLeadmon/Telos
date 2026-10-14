-- Migration 0024: an audit trail for restores.
--
-- A node restored from a backup comes back with every credential that existed when the
-- backup was taken: browser sessions, device refresh tokens and access tokens, and unused
-- invites. `telos-core sanitize-restore` revokes all of them before any traffic is served
-- and records what it did here. The runtime role may read this history but never write
-- it; only the schema owner (the sanitizer) does.
CREATE TABLE IF NOT EXISTS restore_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restored_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source_snapshot TEXT NOT NULL,
    sessions_revoked INTEGER NOT NULL CHECK (sessions_revoked >= 0),
    devices_revoked INTEGER NOT NULL CHECK (devices_revoked >= 0),
    refresh_tokens_revoked INTEGER NOT NULL CHECK (refresh_tokens_revoked >= 0),
    invites_invalidated INTEGER NOT NULL CHECK (invites_invalidated >= 0),
    CONSTRAINT chk_restore_events_snapshot CHECK (source_snapshot = 'unknown' OR source_snapshot ~ '^[0-9a-f]{8,64}$')
);

CREATE INDEX IF NOT EXISTS idx_restore_events_restored_at ON restore_events(restored_at DESC);

DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telos_runtime') THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON restore_events FROM telos_runtime;
    END IF;
END $$;
