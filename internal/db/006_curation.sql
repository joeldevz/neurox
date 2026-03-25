CREATE TABLE curation_runs (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    observations_before INTEGER DEFAULT 0,
    observations_deleted INTEGER DEFAULT 0,
    observations_recalibrated INTEGER DEFAULT 0,
    observations_protected INTEGER DEFAULT 0,
    dry_run INTEGER NOT NULL DEFAULT 0,
    curator_model TEXT,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT,
    error_message TEXT
);

CREATE TABLE curation_decisions (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES curation_runs(id) ON DELETE CASCADE,
    observation_id TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('DELETE', 'KEEP')),
    old_importance REAL,
    new_importance REAL,
    reason TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_curation_decisions_run ON curation_decisions(run_id);
CREATE INDEX idx_curation_runs_namespace ON curation_runs(namespace);
