-- Migration 0010: transactional outbox for durable real-time delivery.
--
-- Any durable mutation that also needs real-time delivery writes an outbox row
-- in the same transaction. A bounded dispatcher claims rows with FOR UPDATE
-- SKIP LOCKED, publishes them through Redis, and marks them published. Unique
-- idempotency keys make replay safe.

CREATE TABLE IF NOT EXISTS outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic           TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    aggregate_type  TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending', -- 'pending' | 'published'
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    CONSTRAINT chk_outbox_status CHECK (status IN ('pending', 'published')),
    CONSTRAINT chk_outbox_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

-- Claim index: pending rows due for delivery, oldest first.
CREATE INDEX IF NOT EXISTS idx_outbox_claim
    ON outbox_events (next_attempt_at)
    WHERE status = 'pending';

-- Cleanup index: published rows by age.
CREATE INDEX IF NOT EXISTS idx_outbox_published
    ON outbox_events (published_at)
    WHERE status = 'published';
