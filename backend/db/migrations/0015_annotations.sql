-- Migration 0015: private/community book annotations with community replies.

-- Moderator capability to remove others' community annotations/replies.
INSERT INTO permissions (id, description) VALUES
  ('moderate_annotations', 'Remove other users'' community annotations and replies')
ON CONFLICT (id) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r, 'moderate_annotations'
FROM (VALUES ('Owner'), ('Administrator'), ('Moderator')) AS grants(r)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS annotations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    book_id       BIGINT NOT NULL,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    visibility    TEXT NOT NULL DEFAULT 'private',
    locator       JSONB NOT NULL DEFAULT '{}',
    selected_text TEXT NOT NULL DEFAULT '',
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_annotation_visibility CHECK (visibility IN ('private','community')),
    CONSTRAINT chk_annotation_locator CHECK (jsonb_typeof(locator) = 'object')
);
-- Community enumeration and owner-private seek.
CREATE INDEX IF NOT EXISTS idx_annotations_book_visible ON annotations (book_id, visibility, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_annotations_owner ON annotations (user_id, book_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS annotation_replies (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    annotation_id UUID NOT NULL REFERENCES annotations(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_annotation_replies_seek ON annotation_replies (annotation_id, created_at, id);
-- Leading index for the user_id foreign key (audited by the invariants test).
CREATE INDEX IF NOT EXISTS idx_annotation_replies_user ON annotation_replies (user_id);
