-- Migration 0003: Shared Files Library

CREATE TABLE IF NOT EXISTS files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filename VARCHAR(255) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    uploader_id UUID REFERENCES users(id) ON DELETE SET NULL,
    scan_status VARCHAR(20) NOT NULL DEFAULT 'pending', -- 'pending', 'clean', 'infected', 'failed'
    storage_key VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL,
    mime_type VARCHAR(100) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
