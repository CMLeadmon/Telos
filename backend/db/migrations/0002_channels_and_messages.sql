-- Migration 0002: Channels, Overrides, and Messages

CREATE TABLE IF NOT EXISTS channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(50) NOT NULL UNIQUE,
    type VARCHAR(10) NOT NULL DEFAULT 'text', -- 'text' or 'voice'
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS channel_role_overrides (
    channel_id UUID REFERENCES channels(id) ON DELETE CASCADE,
    role_id VARCHAR(50) REFERENCES roles(id) ON DELETE CASCADE,
    allow_mask INT NOT NULL DEFAULT 0,
    deny_mask INT NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, role_id)
);

CREATE TABLE IF NOT EXISTS messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_channel_created ON messages (channel_id, created_at DESC, id DESC);

-- Seed default channels with stable UUIDs
INSERT INTO channels (id, name, type) VALUES 
('00000000-0000-0000-0000-000000000001', 'general', 'text'),
('00000000-0000-0000-0000-000000000002', 'dev-chat', 'text'),
('00000000-0000-0000-0000-000000000003', 'catalog-updates', 'text'),
('00000000-0000-0000-0000-000000000004', 'voice-lounge', 'voice'),
('00000000-0000-0000-0000-000000000005', 'gaming-lounge', 'voice')
ON CONFLICT (id) DO NOTHING;
