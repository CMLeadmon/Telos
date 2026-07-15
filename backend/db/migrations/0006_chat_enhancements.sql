-- Migration 0006: Chat enhancements — reactions, edits/deletes, pins, embeds, reads, notifications

ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at      TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS deleted_at     TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_kind     TEXT;   -- 'library_book' | 'stream_film' | 'file'
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_ref      TEXT;   -- referenced item id
ALTER TABLE messages ADD COLUMN IF NOT EXISTS embed_snapshot JSONB;  -- denormalized {title,subtitle,kicker,cover,duration}

CREATE TABLE IF NOT EXISTS message_reactions (
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    emoji      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX IF NOT EXISTS idx_reactions_message ON message_reactions (message_id);

CREATE TABLE IF NOT EXISTS channel_pins (
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    pinned_by  UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    pinned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, message_id)
);

CREATE TABLE IF NOT EXISTS channel_reads (
    user_id              UUID NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
    channel_id           UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    last_read_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    last_read_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE IF NOT EXISTS notifications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,                 -- 'mention'
    channel_id UUID REFERENCES channels(id)   ON DELETE CASCADE,
    message_id UUID REFERENCES messages(id)   ON DELETE CASCADE,
    actor_id   UUID REFERENCES users(id)      ON DELETE CASCADE,
    read_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, created_at DESC);

INSERT INTO permissions (id, description) VALUES
    ('manage_messages', 'Edit or delete any message and pin/unpin messages')
ON CONFLICT (id) DO NOTHING;

-- Grant to privileged roles that exist (defensive: only rows for existing roles are inserted)
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, 'manage_messages' FROM roles r WHERE r.id IN ('Administrator','Host','Curator')
ON CONFLICT DO NOTHING;
