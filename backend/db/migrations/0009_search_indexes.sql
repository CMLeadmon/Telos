-- Migration 0009: trigram indexes for bounded local search.
--
-- pg_trgm is a trusted extension; the migrating role (telos_owner) is granted
-- CREATE on the database so it can install it. The GIN index expressions match
-- the search query expressions (lower(col)) so ILIKE substring searches use the
-- index instead of a sequential scan.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_users_username_trgm
  ON users USING gin (lower(username) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_display_trgm
  ON users USING gin (lower(coalesce(display_name, '')) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_channels_name_trgm
  ON channels USING gin (lower(name) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_files_filename_trgm
  ON files USING gin (lower(filename) gin_trgm_ops);
