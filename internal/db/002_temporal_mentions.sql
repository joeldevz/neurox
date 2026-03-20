CREATE TABLE temporal_mentions (
    id TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    raw_text TEXT NOT NULL,
    mention_kind TEXT NOT NULL
        CHECK (mention_kind IN ('absolute', 'relative', 'current_state', 'duration', 'recurring')),
    normalized_start TEXT,
    normalized_end TEXT,
    anchor_time TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 0.8 CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_temporal_obs ON temporal_mentions(observation_id);
CREATE INDEX idx_temporal_kind ON temporal_mentions(mention_kind);
CREATE INDEX idx_temporal_start ON temporal_mentions(normalized_start) WHERE normalized_start IS NOT NULL;
CREATE INDEX idx_temporal_end ON temporal_mentions(normalized_end) WHERE normalized_end IS NOT NULL;
