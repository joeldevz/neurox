-- Migration 010: Add activation and consolidation strength signals
-- Separates durable value (importance) from recency/activation signals

-- Add activation_level: tracks recent accessibility (decays with time, bumps with access)
-- Range: 0.0 to 1.0, default 0.5 (neutral activation)
-- Note: SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we catch the error
-- The application migration runner handles duplicate column errors gracefully

ALTER TABLE observations ADD COLUMN activation_level REAL NOT NULL DEFAULT 0.5
    CHECK (activation_level BETWEEN 0.0 AND 1.0);

-- Add consolidation_strength: tracks how well-established a memory is
-- Range: 0.0 to 1.0, default 0.0 (new memories have no consolidation yet)
ALTER TABLE observations ADD COLUMN consolidation_strength REAL NOT NULL DEFAULT 0.0
    CHECK (consolidation_strength BETWEEN 0.0 AND 1.0);

-- Add index for efficient queries by activation level
CREATE INDEX IF NOT EXISTS idx_obs_activation ON observations(activation_level DESC) WHERE deleted_at IS NULL;

-- Backfill existing observations with sensible defaults based on current state:
-- - Core layer (2): high consolidation, activation based on importance
-- - Working layer (1): medium consolidation, activation based on importance  
-- - Buffer layer (0): low consolidation, activation based on importance
UPDATE observations SET
    activation_level = CASE
        WHEN importance >= 0.7 THEN 0.8
        WHEN importance >= 0.4 THEN 0.6
        WHEN importance >= 0.2 THEN 0.4
        ELSE 0.3
    END,
    consolidation_strength = CASE
        WHEN layer = 2 THEN MIN(0.7 + (access_count * 0.02), 0.95)
        WHEN layer = 1 THEN MIN(0.4 + (access_count * 0.015), 0.7)
        ELSE MIN(0.1 + (access_count * 0.01), 0.4)
    END
WHERE deleted_at IS NULL;
