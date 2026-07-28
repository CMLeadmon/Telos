-- Migration 0019: move the notification idempotency guard onto user_events.
--
-- The notifications table's unique index (idx_notifications_idem) was the
-- deduplication guard for the user_events stream: CreateNotification claimed
-- the key there first, precisely so a concurrent replay that lost the race
-- could never append a duplicate user_event. WebSocket catch-up replays that
-- stream, so a duplicate is a user-visible double delivery.
--
-- The notifications table is dropped in 0020. This migration relocates the
-- guard so user_events self-deduplicates. Additive only, and therefore safe to
-- apply while the pre-removal code is still running.

ALTER TABLE user_events ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

-- Carry each event's owning key across before the owner disappears. A no-op on
-- a deployment with no notifications, which is the case for the beta node.
UPDATE user_events ue
SET    idempotency_key = n.idempotency_key
FROM   notifications n
WHERE  n.event_sequence = ue.sequence
  AND  ue.idempotency_key IS NULL
  AND  n.idempotency_key IS NOT NULL;

-- NULLs are distinct in a unique index, so any legacy keyless row is
-- unaffected and ON CONFLICT (idempotency_key) can infer this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_events_idem
  ON user_events (idempotency_key);
