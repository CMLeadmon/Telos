CREATE TABLE IF NOT EXISTS catalog_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    surface TEXT NOT NULL,
    kind TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_catalog_surface CHECK (surface IN ('library','stream')),
    CONSTRAINT chk_catalog_kind CHECK (kind IN ('epub','pdf','audiobook','video','audio','folder'))
);

CREATE TABLE IF NOT EXISTS catalog_sources (
    catalog_item_id UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_id TEXT NOT NULL,
    upstream_library_id TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT true,
    available BOOLEAN NOT NULL DEFAULT true,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    missing_since TIMESTAMPTZ,
    CONSTRAINT chk_catalog_provider CHECK (provider IN ('grimmory','jellyfin')),
    CONSTRAINT uq_catalog_provider_upstream UNIQUE (provider, upstream_id),
    CONSTRAINT uq_catalog_item_source UNIQUE (catalog_item_id, provider, upstream_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_sources_active
    ON catalog_sources (catalog_item_id) WHERE active;
CREATE INDEX IF NOT EXISTS idx_catalog_sources_item
    ON catalog_sources (catalog_item_id);
CREATE INDEX IF NOT EXISTS idx_catalog_sources_library
    ON catalog_sources (provider, upstream_library_id, active);

CREATE TABLE IF NOT EXISTS member_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    catalog_item_id UUID NOT NULL REFERENCES catalog_items(id) ON DELETE CASCADE,
    locator JSONB NOT NULL DEFAULT '{}',
    position_ms BIGINT NOT NULL DEFAULT 0,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    percent REAL NOT NULL DEFAULT 0,
    completed BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, catalog_item_id),
    CONSTRAINT chk_member_progress_locator CHECK (jsonb_typeof(locator) = 'object'),
    CONSTRAINT chk_member_progress_position CHECK (position_ms >= 0),
    CONSTRAINT chk_member_progress_duration CHECK (duration_ms >= 0),
    CONSTRAINT chk_member_progress_percent CHECK (percent >= 0 AND percent <= 1)
);

CREATE INDEX IF NOT EXISTS idx_member_progress_item
    ON member_progress (catalog_item_id);
CREATE INDEX IF NOT EXISTS idx_member_progress_continue
    ON member_progress (user_id, updated_at DESC, catalog_item_id)
    WHERE completed = false;
