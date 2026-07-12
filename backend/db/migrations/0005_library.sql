-- Migration 0005: Library — per-user reading progress for Grimmory books.
-- Progress lives in Telos (not Grimmory) because the gateway talks to
-- Grimmory as a single admin account; book_id is Grimmory's numeric book id.

CREATE TABLE IF NOT EXISTS book_progress (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id BIGINT NOT NULL,
    locator JSONB NOT NULL DEFAULT '{}',
    percent REAL NOT NULL DEFAULT 0,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, book_id)
);
