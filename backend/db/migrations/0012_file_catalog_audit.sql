-- Migration 0012: purpose-aware file catalog, logical folders, ingestion
-- leases, book handoff, reservations, and durable audit.

-- File lifecycle state and visibility. purpose already exists (0011).
ALTER TABLE files ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'available';
ALTER TABLE files ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'community';
ALTER TABLE files ADD COLUMN IF NOT EXISTS folder_id UUID;
ALTER TABLE files ADD COLUMN IF NOT EXISTS logical_name TEXT;

ALTER TABLE files ADD CONSTRAINT chk_files_state CHECK (state IN (
  'staged','validating','scanning','promoting','available','handoff_pending',
  'handed_off','quarantined','missing','deleting','deleted','consumed'
));
ALTER TABLE files ADD CONSTRAINT chk_files_visibility CHECK (visibility IN ('private','community'));

-- Physical storage keys are unique and content-addressed.
CREATE UNIQUE INDEX IF NOT EXISTS idx_files_storage_key ON files (storage_key);
CREATE INDEX IF NOT EXISTS idx_files_visible ON files (purpose, state, visibility, created_at DESC);

-- Backfill: clean shared files become available/community; anything else that
-- is not clean is quarantined rather than guessed public.
UPDATE files SET state = 'available' WHERE scan_status = 'clean';
UPDATE files SET state = 'quarantined' WHERE scan_status = 'infected';
UPDATE files SET visibility = 'private' WHERE purpose <> 'shared';

-- Logical folders are database-only nodes (physical storage never mirrors user
-- path names). Sibling names are unique per parent+owner.
CREATE TABLE IF NOT EXISTS logical_folders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id       UUID REFERENCES logical_folders(id) ON DELETE CASCADE,
    owner_id        UUID REFERENCES users(id) ON DELETE SET NULL,
    normalized_name TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    visibility      TEXT NOT NULL DEFAULT 'community',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_folder_visibility CHECK (visibility IN ('private','community')),
    CONSTRAINT chk_folder_no_self_parent CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_folders_sibling
    ON logical_folders (COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'::uuid), owner_id, normalized_name);
CREATE INDEX IF NOT EXISTS idx_folders_parent ON logical_folders (parent_id);
CREATE INDEX IF NOT EXISTS idx_folders_owner ON logical_folders (owner_id);

ALTER TABLE files ADD CONSTRAINT fk_files_folder
    FOREIGN KEY (folder_id) REFERENCES logical_folders(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_files_folder ON files (folder_id);

-- Nonterminal ingestion lease: a random owner, expiry, attempt count.
CREATE TABLE IF NOT EXISTS file_ingestion_leases (
    file_id       UUID PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    lease_owner   TEXT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    attempts      INT NOT NULL DEFAULT 0,
    last_progress TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_leases_expiry ON file_ingestion_leases (expires_at);

-- Book handoff records (Grimmory ingestion).
CREATE TABLE IF NOT EXISTS book_handoffs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id     UUID REFERENCES files(id) ON DELETE SET NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_handoff_status CHECK (status IN ('pending','delivered','failed'))
);
CREATE INDEX IF NOT EXISTS idx_book_handoffs_file ON book_handoffs (file_id);

-- Per-user and node upload reservations (space accounting during ingestion).
CREATE TABLE IF NOT EXISTS upload_reservations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    bytes       BIGINT NOT NULL CHECK (bytes >= 0),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_reservations_user ON upload_reservations (user_id);
CREATE INDEX IF NOT EXISTS idx_reservations_expiry ON upload_reservations (expires_at);

-- Immutable durable audit of file/folder actions.
CREATE TABLE IF NOT EXISTS file_audit (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    file_id     UUID,
    folder_id   UUID,
    action      TEXT NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_audit_action CHECK (action IN (
      'create','promote','rename','move','download','delete','moderate',
      'handoff','quarantine','missing','reconcile','consume'
    )),
    CONSTRAINT chk_audit_detail_object CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX IF NOT EXISTS idx_file_audit_file ON file_audit (file_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_file_audit_actor ON file_audit (actor_id, created_at DESC);
