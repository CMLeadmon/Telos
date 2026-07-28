-- Migration 0021: generalize annotations from a book-only anchor to any media
-- target, so commentary works on books, streamed media, and files. Free now
-- (the table is empty on the beta node); expensive once real annotations exist.

ALTER TABLE annotations ADD COLUMN IF NOT EXISTS target_type TEXT NOT NULL DEFAULT 'book';
ALTER TABLE annotations ADD COLUMN IF NOT EXISTS target_id   TEXT NOT NULL DEFAULT '';

-- Existing rows are book annotations; carry their book id into the generic
-- target and stamp the type. (No-op on an empty table.)
UPDATE annotations SET target_id = book_id::text WHERE target_type = 'book' AND target_id = '';

ALTER TABLE annotations DROP CONSTRAINT IF EXISTS chk_annotation_target;
ALTER TABLE annotations ADD CONSTRAINT chk_annotation_target
  CHECK (target_type IN ('book','media','file'));

-- Rebuild the seek indexes on the generic target. Drop the book-specific ones
-- and the now-unused book_id column.
DROP INDEX IF EXISTS idx_annotations_book_visible;
DROP INDEX IF EXISTS idx_annotations_owner;
ALTER TABLE annotations DROP COLUMN IF EXISTS book_id;

CREATE INDEX IF NOT EXISTS idx_annotations_target_visible
  ON annotations (target_type, target_id, visibility, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_annotations_owner_target
  ON annotations (user_id, target_type, target_id, created_at DESC, id DESC);
