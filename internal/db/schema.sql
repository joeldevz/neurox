CREATE TABLE observations (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    observation_type TEXT NOT NULL DEFAULT 'discovery'
        CHECK (observation_type IN (
            'decision', 'bugfix', 'discovery', 'pattern',
            'gotcha', 'config', 'preference', 'question'
        )),
    layer INTEGER NOT NULL DEFAULT 0 CHECK (layer IN (0, 1, 2)),
    confidence REAL NOT NULL DEFAULT 0.7 CHECK (confidence BETWEEN 0.0 AND 1.0),
    importance REAL NOT NULL DEFAULT 0.5 CHECK (importance BETWEEN 0.0 AND 1.0),
    access_count INTEGER NOT NULL DEFAULT 0,
    last_accessed TEXT,
    repetition_count INTEGER NOT NULL DEFAULT 0,
    decay_rate REAL NOT NULL DEFAULT 1.0,
    kind TEXT NOT NULL DEFAULT 'semantic'
        CHECK (kind IN ('episodic', 'semantic', 'procedural')),
    tags TEXT,
    namespace TEXT NOT NULL DEFAULT 'default',
    source TEXT,
    topic_key TEXT,
    valid_from TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until TEXT,
    invalidated_by TEXT REFERENCES observations(id),
    staleness TEXT NOT NULL DEFAULT 'fresh'
        CHECK (staleness IN ('fresh', 'stale', 'revalidated', 'expired')),
    consolidation_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (consolidation_status IN ('pending', 'promoted', 'rejected', 'rejected-2', 'rejected-final')),
    rejection_epoch INTEGER,
    embedding BLOB,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    deleted_at TEXT,
    modified_epoch INTEGER NOT NULL DEFAULT 0,
    activation_level REAL NOT NULL DEFAULT 0.5 CHECK (activation_level BETWEEN 0.0 AND 1.0),
    consolidation_strength REAL NOT NULL DEFAULT 0.0 CHECK (consolidation_strength BETWEEN 0.0 AND 1.0)
);

CREATE UNIQUE INDEX uq_active_topic_key
    ON observations(namespace, topic_key)
    WHERE topic_key IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX idx_obs_layer ON observations(layer) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_namespace ON observations(namespace) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_type ON observations(observation_type) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_kind ON observations(kind) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_staleness ON observations(staleness) WHERE staleness = 'stale' AND deleted_at IS NULL;
CREATE INDEX idx_obs_created ON observations(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_importance ON observations(importance DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_consolidation ON observations(consolidation_status) WHERE consolidation_status = 'pending' AND deleted_at IS NULL;
CREATE INDEX idx_obs_activation ON observations(activation_level DESC) WHERE deleted_at IS NULL;

CREATE VIRTUAL TABLE observations_fts USING fts5(
    id UNINDEXED,
    title,
    content,
    tags,
    content=observations,
    content_rowid=rowid
);

CREATE TRIGGER trg_obs_ai AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, id, title, content, tags)
    VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;

CREATE TRIGGER trg_obs_ad AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, id, title, content, tags)
    VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
END;

CREATE TRIGGER trg_obs_au AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, id, title, content, tags)
    VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
    INSERT INTO observations_fts(rowid, id, title, content, tags)
    VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;

CREATE TABLE observation_links (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL CHECK (relation_type IN (
        'supersedes',
        'contradicts',
        'relates_to',
        'derived_from',
        'validates',
        'refines'
    )),
    confidence REAL DEFAULT 1.0 CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_by TEXT NOT NULL DEFAULT 'consolidator'
        CHECK (created_by IN ('agent', 'consolidator', 'user')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    CHECK (source_id != target_id),
    UNIQUE (source_id, target_id, relation_type)
);

CREATE INDEX idx_links_source ON observation_links(source_id);
CREATE INDEX idx_links_target ON observation_links(target_id);
CREATE INDEX idx_links_type ON observation_links(relation_type);

CREATE TABLE file_observations (
    id TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    commit_sha_from TEXT,
    commit_sha_until TEXT,
    valid_from TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_file_obs_file ON file_observations(file_path);
CREATE INDEX idx_file_obs_observation ON file_observations(observation_id);
CREATE INDEX idx_file_obs_valid ON file_observations(file_path) WHERE valid_until IS NULL;

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    title TEXT,
    directory TEXT,
    branch TEXT,
    namespace TEXT NOT NULL DEFAULT 'default',
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'completed', 'abandoned')),
    summary TEXT,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at TEXT
);

CREATE INDEX idx_sessions_status ON sessions(status) WHERE status = 'active';
CREATE INDEX idx_sessions_namespace ON sessions(namespace);

CREATE TABLE facts (
    id TEXT PRIMARY KEY,
    subject TEXT NOT NULL,
    predicate TEXT NOT NULL,
    object TEXT NOT NULL,
    observation_id TEXT REFERENCES observations(id) ON DELETE SET NULL,
    namespace TEXT NOT NULL DEFAULT 'default',
    valid_from TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until TEXT,
    superseded_by TEXT REFERENCES facts(id),
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_facts_subject ON facts(subject);
CREATE INDEX idx_facts_object ON facts(object);
CREATE INDEX idx_facts_observation ON facts(observation_id);
CREATE INDEX idx_facts_namespace ON facts(namespace) WHERE valid_until IS NULL;

CREATE TABLE consolidation_runs (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    epoch INTEGER NOT NULL,
    observations_processed INTEGER DEFAULT 0,
    observations_promoted INTEGER DEFAULT 0,
    observations_deduped INTEGER DEFAULT 0,
    contradictions_found INTEGER DEFAULT 0,
    reflections_created INTEGER DEFAULT 0,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at TEXT,
    error_message TEXT,
    llm_tokens_used INTEGER DEFAULT 0
);

CREATE TABLE reflections (
    id TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    source_observation_ids TEXT NOT NULL,
    namespace TEXT NOT NULL DEFAULT 'default',
    layer INTEGER NOT NULL DEFAULT 2,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_reflections_namespace ON reflections(namespace);
