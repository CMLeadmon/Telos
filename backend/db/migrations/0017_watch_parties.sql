-- Migration 0017: host-authoritative Watch Parties — durable identity,
-- invitations, members, and host-successor consent. Playback state and the host
-- lease live in Redis (bounded TTL); only durable identity is in Postgres.

CREATE TABLE IF NOT EXISTS watch_parties (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_item_id    TEXT NOT NULL,
    text_channel_id  UUID REFERENCES channels(id) ON DELETE SET NULL,
    voice_channel_id UUID REFERENCES channels(id) ON DELETE SET NULL,
    host_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    host_generation  BIGINT NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_watch_parties_host ON watch_parties (host_id);
CREATE INDEX IF NOT EXISTS idx_watch_parties_text_channel ON watch_parties (text_channel_id);
CREATE INDEX IF NOT EXISTS idx_watch_parties_voice_channel ON watch_parties (voice_channel_id);

CREATE TABLE IF NOT EXISTS watch_party_members (
    party_id  UUID NOT NULL REFERENCES watch_parties(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state     TEXT NOT NULL DEFAULT 'joined',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (party_id, user_id),
    CONSTRAINT chk_wp_member_state CHECK (state IN ('joined','detached','left'))
);
CREATE INDEX IF NOT EXISTS idx_wp_members_user ON watch_party_members (user_id);

CREATE TABLE IF NOT EXISTS watch_party_invitations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    party_id   UUID NOT NULL REFERENCES watch_parties(id) ON DELETE CASCADE,
    invitee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_wp_invite UNIQUE (party_id, invitee_id),
    CONSTRAINT chk_wp_invite_status CHECK (status IN ('pending','accepted','declined'))
);
CREATE INDEX IF NOT EXISTS idx_wp_invites_invitee ON watch_party_invitations (invitee_id);

-- Durable host-successor consent, bound to a host generation.
CREATE TABLE IF NOT EXISTS watch_party_host_offers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    party_id        UUID NOT NULL REFERENCES watch_parties(id) ON DELETE CASCADE,
    host_generation BIGINT NOT NULL,
    successor_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'offered',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_wp_offer_status CHECK (status IN ('offered','accepted','cancelled'))
);
CREATE INDEX IF NOT EXISTS idx_wp_offers_party ON watch_party_host_offers (party_id, host_generation);
CREATE INDEX IF NOT EXISTS idx_wp_offers_successor ON watch_party_host_offers (successor_id);
