-- Migration 0014: one-level message threads, client-mutation idempotency, and a
-- durable per-channel change log for socket catch-up.

-- ── Threading + idempotency columns ─────────────────────────────────────────
ALTER TABLE messages ADD COLUMN IF NOT EXISTS thread_root_id     UUID;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS client_mutation_id TEXT;

-- A reply's root must be a root (thread_root_id IS NULL) in the SAME channel,
-- and a reply can never itself receive a reply. Enforced in the database so no
-- code path can create a second-level or cross-channel thread.
CREATE OR REPLACE FUNCTION enforce_thread_root() RETURNS trigger AS $$
BEGIN
  IF NEW.thread_root_id IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.thread_root_id = NEW.id THEN
    RAISE EXCEPTION 'a message cannot be its own thread root';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM messages
    WHERE id = NEW.thread_root_id
      AND channel_id = NEW.channel_id
      AND thread_root_id IS NULL
  ) THEN
    RAISE EXCEPTION 'thread root must be a root message in the same channel';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_enforce_thread_root ON messages;
CREATE TRIGGER trg_enforce_thread_root
  BEFORE INSERT OR UPDATE OF thread_root_id ON messages
  FOR EACH ROW EXECUTE FUNCTION enforce_thread_root();

-- Author-scoped mutation idempotency: one author + client mutation id maps to at
-- most one durable message. NULLs are distinct, so legacy rows are unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_client_mutation
  ON messages (user_id, client_mutation_id) WHERE client_mutation_id IS NOT NULL;

-- Thread reply seek.
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages (thread_root_id, created_at, id);
-- Root-only channel history seek (roots have thread_root_id IS NULL).
CREATE INDEX IF NOT EXISTS idx_messages_roots ON messages (channel_id, created_at DESC, id DESC) WHERE thread_root_id IS NULL;

-- ── Durable per-channel change log ──────────────────────────────────────────
CREATE TABLE IF NOT EXISTS channel_changes (
    sequence    BIGSERIAL PRIMARY KEY,
    channel_id  UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    message_id  UUID,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_channel_change_kind CHECK (kind IN (
      'message.created','message.updated','message.deleted'))
);
CREATE INDEX IF NOT EXISTS idx_channel_changes_seek ON channel_changes (channel_id, sequence);
-- Prune index by age.
CREATE INDEX IF NOT EXISTS idx_channel_changes_age ON channel_changes (occurred_at);
