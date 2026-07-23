-- Migration 0018: explicit per-(channel, role, permission) overrides replacing
-- the positional allow/deny bit-mask, with deny precedence for every non-Owner
-- role. A missing row means "inherit".

CREATE TABLE IF NOT EXISTS channel_permission_overrides (
    channel_id    UUID        NOT NULL REFERENCES channels(id)    ON DELETE CASCADE,
    role_id       VARCHAR(50) NOT NULL REFERENCES roles(id)       ON DELETE CASCADE,
    permission_id TEXT        NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    decision      TEXT        NOT NULL,
    PRIMARY KEY (channel_id, role_id, permission_id),
    CONSTRAINT chk_cpo_decision CHECK (decision IN ('allow','deny'))
);
CREATE INDEX IF NOT EXISTS idx_cpo_role ON channel_permission_overrides (role_id);
CREATE INDEX IF NOT EXISTS idx_cpo_perm ON channel_permission_overrides (permission_id);

-- Migrate the bit-mask meanings (view=1, send=2, voice=4) to explicit rows
-- WITHOUT widening access: deny wins, so an allowed bit becomes a row only when
-- the same bit is not denied.
INSERT INTO channel_permission_overrides (channel_id, role_id, permission_id, decision)
SELECT o.channel_id, o.role_id, m.perm, 'deny'
FROM channel_role_overrides o
CROSS JOIN (VALUES (1,'view_channel'),(2,'send_messages'),(4,'join_voice')) AS m(bit, perm)
WHERE (o.deny_mask & m.bit) <> 0
ON CONFLICT DO NOTHING;

INSERT INTO channel_permission_overrides (channel_id, role_id, permission_id, decision)
SELECT o.channel_id, o.role_id, m.perm, 'allow'
FROM channel_role_overrides o
CROSS JOIN (VALUES (1,'view_channel'),(2,'send_messages'),(4,'join_voice')) AS m(bit, perm)
WHERE (o.allow_mask & m.bit) <> 0 AND (o.deny_mask & m.bit) = 0
ON CONFLICT DO NOTHING;

-- Ensure channel management is grantable to Owner and Administrator.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r, 'manage_channels' FROM (VALUES ('Owner'), ('Administrator')) AS g(r)
ON CONFLICT DO NOTHING;

-- Durable audit of channel administration actions.
CREATE TABLE IF NOT EXISTS channel_audit (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    channel_id UUID,
    action     TEXT NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_channel_audit_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX IF NOT EXISTS idx_channel_audit_actor ON channel_audit (actor_id, created_at DESC);
