-- Users table to cache identity and roles
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(50) PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    avatar VARCHAR(10) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'Member',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Text and voice channels
CREATE TABLE IF NOT EXISTS channels (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(50) NOT NULL UNIQUE,
    type VARCHAR(10) NOT NULL DEFAULT 'text', -- 'text' or 'voice'
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Chat messages history
CREATE TABLE IF NOT EXISTS messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id VARCHAR(50) REFERENCES channels(id) ON DELETE CASCADE,
    user_id VARCHAR(50) REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Seed initial default users
INSERT INTO users (id, username, avatar, role) VALUES 
('cleadmon', 'cleadmon', 'CL', 'Host'),
('oracle', 'oracle', 'AI', 'Admin'),
('telos-bot', 'telos-bot', 'TB', 'Admin')
ON CONFLICT (id) DO UPDATE SET 
  avatar = EXCLUDED.avatar,
  role = EXCLUDED.role;

-- Seed initial channels
INSERT INTO channels (id, name, type) VALUES 
('general', 'general', 'text'),
('dev-chat', 'dev-chat', 'text'),
('catalog-updates', 'catalog-updates', 'text'),
('voice-lounge', 'voice-lounge', 'voice'),
('gaming-lounge', 'gaming-lounge', 'voice')
ON CONFLICT (id) DO NOTHING;
