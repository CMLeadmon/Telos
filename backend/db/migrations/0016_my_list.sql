-- Migration 0016: durable per-user "My List" of media items with an explicit
-- order and an optimistic-concurrency revision.

CREATE TABLE IF NOT EXISTS media_lists (
    user_id  UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS media_list_entries (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL,
    position   BIGINT NOT NULL,
    -- Non-authoritative snapshots for display when upstream is unavailable.
    title      TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    added_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, item_id),
    CONSTRAINT uq_media_list_position UNIQUE (user_id, position)
);
CREATE INDEX IF NOT EXISTS idx_media_list_order ON media_list_entries (user_id, position);
