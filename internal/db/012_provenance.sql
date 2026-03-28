-- Migration 012: Add provenance fields to observations
-- Tracks where each observation originated: which surface, session, and tool created it.
-- Note: SQLite doesn't support IF NOT EXISTS for ALTER TABLE ADD COLUMN.
-- The application migration runner handles duplicate column errors gracefully.

ALTER TABLE observations ADD COLUMN source_surface TEXT;

ALTER TABLE observations ADD COLUMN source_session_id TEXT;

ALTER TABLE observations ADD COLUMN source_tool TEXT;
