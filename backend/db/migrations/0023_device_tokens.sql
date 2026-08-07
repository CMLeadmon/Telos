-- Stable device identity. Exactly one row per registered client install,
-- unchanged across token rotations.
CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name VARCHAR(128) NOT NULL,
    platform VARCHAR(64) NOT NULL,
    client_version VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ
);

-- Rotating refresh credentials. Many rows per device over its lifetime.
-- replaced_by threads each rotation to its successor, which is what makes
-- reuse detection possible: presenting a token that already has a successor
-- means the credential leaked.
CREATE TABLE IF NOT EXISTS device_refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    replaced_by UUID REFERENCES device_refresh_tokens(id)
);

CREATE INDEX IF NOT EXISTS idx_devices_user_id ON devices(user_id) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_device_refresh_tokens_device ON device_refresh_tokens(device_id);
CREATE INDEX IF NOT EXISTS idx_device_refresh_tokens_replaced_by ON device_refresh_tokens(replaced_by) WHERE replaced_by IS NOT NULL;

-- Device access tokens are stored in sessions so authentication keeps a single
-- lookup path, but they must stay distinguishable from browser sessions:
-- revoking a device has to invalidate its access tokens too, and without this
-- column a revoked device would keep a working bearer token until it expired.
-- A NULL device_id is an ordinary browser session.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS device_id UUID REFERENCES devices(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_sessions_device_id ON sessions(device_id) WHERE device_id IS NOT NULL;
