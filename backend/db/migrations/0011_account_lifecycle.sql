-- Migration 0011: account deletion lifecycle and retention scaffolding.

-- Explicit file purpose so deletion can distinguish private assets (avatars,
-- book-ingestion staging) from shared community files that are retained.
ALTER TABLE files ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'shared';
ALTER TABLE files ADD CONSTRAINT chk_files_purpose
  CHECK (purpose IN ('shared', 'avatar', 'book_ingest'));
CREATE INDEX IF NOT EXISTS idx_files_purpose_uploader ON files (uploader_id, purpose);

-- Idempotent deletion receipts: a repeat deletion returns the same receipt.
CREATE TABLE IF NOT EXISTS deletion_requests (
    user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    request_id   UUID NOT NULL DEFAULT gen_random_uuid(),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Physical-asset deletion jobs recorded transactionally, executed after commit
-- with retry/reconciliation visibility.
CREATE TABLE IF NOT EXISTS asset_deletion_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID,
    area        TEXT NOT NULL,               -- 'avatar' | 'upload'
    storage_key TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- 'pending' | 'done'
    attempts    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT chk_asset_job_status CHECK (status IN ('pending', 'done')),
    CONSTRAINT chk_asset_job_area CHECK (area IN ('avatar', 'upload'))
);
CREATE INDEX IF NOT EXISTS idx_asset_jobs_pending ON asset_deletion_jobs (created_at) WHERE status = 'pending';
