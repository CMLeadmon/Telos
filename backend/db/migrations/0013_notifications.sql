-- Migration 0013: durable in-app notifications + recipient-filtered user-event
-- log. Upgrades the existing notifications table (0006) in place and adds a
-- strictly-increasing per-recipient event stream for socket catch-up.

-- ── Notifications: generic resource reference, payload, idempotency, event link ──
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS resource_type   TEXT   NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS resource_id     TEXT   NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS payload         JSONB  NOT NULL DEFAULT '{}';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_sequence  BIGINT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_notifications_kind') THEN
    ALTER TABLE notifications ADD CONSTRAINT chk_notifications_kind CHECK (
      kind IN ('mention','thread_reply','annotation_reply','watch_party_invite','account_security'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_notifications_payload') THEN
    ALTER TABLE notifications ADD CONSTRAINT chk_notifications_payload CHECK (jsonb_typeof(payload) = 'object');
  END IF;
END $$;

-- A set idempotency key is globally unique so replay cannot duplicate a row.
-- NULLs are distinct in a unique index, so legacy keyless rows are unaffected
-- and ON CONFLICT (idempotency_key) can infer this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_idem
  ON notifications (idempotency_key);
-- Stable descending inbox seek and a partial unread index.
CREATE INDEX IF NOT EXISTS idx_notifications_inbox  ON notifications (user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_unread ON notifications (user_id) WHERE read_at IS NULL;

-- ── User events: durable, recipient-scoped, strictly increasing sequence ──────
CREATE TABLE IF NOT EXISTS user_events (
    sequence      BIGSERIAL PRIMARY KEY,
    recipient_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id   TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_user_events_kind CHECK (
      kind IN ('mention','thread_reply','annotation_reply','watch_party_invite','account_security')),
    CONSTRAINT chk_user_events_payload CHECK (jsonb_typeof(payload) = 'object')
);
-- Recipient-scoped ascending catch-up seek.
CREATE INDEX IF NOT EXISTS idx_user_events_recipient ON user_events (recipient_id, sequence);

-- Link a notification to its event once the event exists.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_notifications_event') THEN
    ALTER TABLE notifications
      ADD CONSTRAINT fk_notifications_event
      FOREIGN KEY (event_sequence) REFERENCES user_events(sequence) ON DELETE SET NULL;
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_notifications_event ON notifications (event_sequence);
