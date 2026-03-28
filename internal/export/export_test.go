package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// SQLite in-memory databases are per-connection; limit to 1 to avoid per-conn isolation.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS observations (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL,
			observation_type TEXT NOT NULL, kind TEXT NOT NULL, layer INTEGER NOT NULL DEFAULT 0,
			importance REAL NOT NULL DEFAULT 0.5, confidence REAL NOT NULL DEFAULT 0.7,
			tags TEXT, namespace TEXT, staleness TEXT NOT NULL DEFAULT 'fresh',
			retention TEXT NOT NULL DEFAULT 'durable',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			valid_from TEXT, valid_until TEXT, deleted_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS observation_links (
			id TEXT PRIMARY KEY, source_id TEXT NOT NULL, target_id TEXT NOT NULL,
			relation_type TEXT NOT NULL DEFAULT 'related'
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup schema: %v\nSQL: %s", err, s)
		}
	}
	return db
}

func TestExportMarkdown_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	n, err := ExportMarkdown(context.Background(), db, "", dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 observations, got %d", n)
	}
}

func TestExportMarkdown_WithObservations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert test observations
	_, err := db.Exec(`INSERT INTO observations (id, title, content, observation_type, kind, layer, importance, confidence, tags, namespace, staleness, retention, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"01TEST1234", "Test Observation One", "This is test content.", "discovery", "semantic",
		0, 0.8, 0.9, "go,test", "testns", "fresh", "durable")
	if err != nil {
		t.Fatalf("insert obs 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO observations (id, title, content, observation_type, kind, layer, importance, confidence, tags, namespace, staleness, retention, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"01TEST5678", "Test Observation Two", "Second test content.", "decision", "episodic",
		1, 0.5, 0.7, "", "testns", "stale", "operational")
	if err != nil {
		t.Fatalf("insert obs 2: %v", err)
	}
	// Add a link
	_, err = db.Exec(`INSERT INTO observation_links (id, source_id, target_id, relation_type) VALUES (?, ?, ?, ?)`,
		"01LINK1234", "01TEST1234", "01TEST5678", "supersedes")
	if err != nil {
		t.Fatalf("insert link: %v", err)
	}

	dir := t.TempDir()
	n, err := ExportMarkdown(context.Background(), db, "testns", dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 observations, got %d", n)
	}

	// Check files exist
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("expected 2 files, got %d", len(entries))
	}

	// Read one file and check frontmatter
	data, err := os.ReadFile(filepath.Join(dir, "Test Observation One.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "id: 01TEST1234") {
		t.Errorf("expected id in frontmatter, got: %s", content[:200])
	}
	if !strings.Contains(content, "# Test Observation One") {
		t.Errorf("expected title heading")
	}
	if !strings.Contains(content, "This is test content.") {
		t.Errorf("expected content body")
	}
	if !strings.Contains(content, "supersedes [[Test Observation Two]]") {
		t.Errorf("expected WikiLink in Links section, got:\n%s", content)
	}
}

func TestImportMarkdown_RoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Insert observations
	_, err := db.Exec(`INSERT INTO observations (id, title, content, observation_type, kind, layer, importance, confidence, tags, namespace, staleness, retention, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		"01ROUND001", "Round Trip Test", "Content for round trip test.", "pattern", "procedural",
		2, 0.9, 0.95, "go,roundtrip", "myns", "fresh", "durable")
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	exportDir := t.TempDir()
	n, err := ExportMarkdown(context.Background(), db, "myns", exportDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 exported, got %d", n)
	}

	// Import into a fresh DB
	db2 := setupTestDB(t)
	defer db2.Close()

	imported, err := ImportMarkdown(context.Background(), db2, exportDir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported != 1 {
		t.Fatalf("expected 1 imported, got %d", imported)
	}

	// Verify
	var title, content, obsType, kind, ns string
	var layer int
	var importance, confidence float64
	err = db2.QueryRow(`SELECT title, content, observation_type, kind, layer, importance, confidence, namespace FROM observations WHERE id = ?`, "01ROUND001").
		Scan(&title, &content, &obsType, &kind, &layer, &importance, &confidence, &ns)
	if err != nil {
		t.Fatalf("query imported: %v", err)
	}
	if title != "Round Trip Test" {
		t.Errorf("title mismatch: %s", title)
	}
	if content != "Content for round trip test." {
		t.Errorf("content mismatch: %s", content)
	}
	if obsType != "pattern" {
		t.Errorf("type mismatch: %s", obsType)
	}
	if layer != 2 {
		t.Errorf("layer mismatch: %d", layer)
	}
	if fmt.Sprintf("%.2f", importance) != "0.90" {
		t.Errorf("importance mismatch: %.2f", importance)
	}
}

// setupFullSchemaDB creates an in-memory database with ALL tables matching the
// production schema. Used by JSON export/import tests that need the complete schema.
func setupFullSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE observations (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL,
			observation_type TEXT NOT NULL DEFAULT 'discovery',
			layer INTEGER NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0.7,
			importance REAL NOT NULL DEFAULT 0.5,
			access_count INTEGER NOT NULL DEFAULT 0,
			last_accessed TEXT,
			repetition_count INTEGER NOT NULL DEFAULT 0,
			decay_rate REAL NOT NULL DEFAULT 1.0,
			kind TEXT NOT NULL DEFAULT 'semantic',
			tags TEXT,
			namespace TEXT NOT NULL DEFAULT 'default',
			source TEXT,
			topic_key TEXT,
			valid_from TEXT NOT NULL DEFAULT (datetime('now')),
			valid_until TEXT,
			invalidated_by TEXT,
			staleness TEXT NOT NULL DEFAULT 'fresh',
			consolidation_status TEXT NOT NULL DEFAULT 'pending',
			rejection_epoch INTEGER,
			embedding BLOB,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			deleted_at TEXT,
			modified_epoch INTEGER NOT NULL DEFAULT 0,
			retention TEXT NOT NULL DEFAULT 'durable',
			activation_level REAL NOT NULL DEFAULT 0.5,
			consolidation_strength REAL NOT NULL DEFAULT 0.0,
			source_surface TEXT,
			source_session_id TEXT,
			source_tool TEXT
		)`,
		`CREATE TABLE observation_links (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
			target_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
			relation_type TEXT NOT NULL,
			confidence REAL DEFAULT 1.0,
			created_by TEXT NOT NULL DEFAULT 'consolidator',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE (source_id, target_id, relation_type)
		)`,
		`CREATE TABLE file_observations (
			id TEXT PRIMARY KEY,
			observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
			file_path TEXT NOT NULL,
			commit_sha_from TEXT,
			commit_sha_until TEXT,
			valid_from TEXT NOT NULL DEFAULT (datetime('now')),
			valid_until TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			title TEXT,
			directory TEXT,
			branch TEXT,
			namespace TEXT NOT NULL DEFAULT 'default',
			status TEXT NOT NULL DEFAULT 'active',
			summary TEXT,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			ended_at TEXT
		)`,
		`CREATE TABLE facts (
			id TEXT PRIMARY KEY,
			subject TEXT NOT NULL,
			predicate TEXT NOT NULL,
			object TEXT NOT NULL,
			observation_id TEXT REFERENCES observations(id) ON DELETE SET NULL,
			namespace TEXT NOT NULL DEFAULT 'default',
			valid_from TEXT NOT NULL DEFAULT (datetime('now')),
			valid_until TEXT,
			superseded_by TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE consolidation_runs (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'running',
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
		)`,
		`CREATE TABLE reflections (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			source_observation_ids TEXT NOT NULL,
			namespace TEXT NOT NULL DEFAULT 'default',
			layer INTEGER NOT NULL DEFAULT 2,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE temporal_mentions (
			id TEXT PRIMARY KEY,
			observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
			raw_text TEXT NOT NULL,
			mention_kind TEXT NOT NULL,
			normalized_start TEXT,
			normalized_end TEXT,
			anchor_time TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 0.8,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup schema: %v\nSQL: %s", err, s)
		}
	}
	return db
}

func TestJSONExportImportRoundTrip(t *testing.T) {
	db := setupFullSchemaDB(t)
	defer db.Close()

	ctx := context.Background()

	// --- Seed data across all tables ---

	// Observations (with all fields including scoring metadata)
	_, err := db.Exec(`INSERT INTO observations (
		id, title, content, observation_type, layer, confidence, importance,
		access_count, last_accessed, repetition_count, decay_rate,
		kind, tags, namespace, source, topic_key,
		valid_from, valid_until, staleness, consolidation_status,
		created_at, updated_at,
		modified_epoch, activation_level, consolidation_strength,
		source_surface, source_session_id, source_tool
	) VALUES (
		'01OBS001', 'Test Obs One', 'Content one.', 'decision', 2, 0.95, 0.88,
		5, '2026-03-28 10:00:00', 3, 0.8,
		'semantic', 'go,test', 'testns', 'user-input', 'my-topic',
		'2026-03-01 00:00:00', NULL, 'fresh', 'promoted',
		'2026-03-01 00:00:00', '2026-03-28 10:00:00',
		2, 0.75, 0.60,
		'mcp', 'session-abc', 'save'
	)`)
	if err != nil {
		t.Fatalf("insert obs 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO observations (
		id, title, content, observation_type, layer, confidence, importance,
		access_count, repetition_count, decay_rate,
		kind, tags, namespace, staleness, consolidation_status,
		created_at, updated_at,
		modified_epoch, activation_level, consolidation_strength
	) VALUES (
		'01OBS002', 'Test Obs Two', 'Content two.', 'bugfix', 1, 0.7, 0.5,
		0, 0, 1.0,
		'episodic', NULL, 'testns', 'stale', 'pending',
		'2026-03-02 00:00:00', '2026-03-02 00:00:00',
		0, 0.5, 0.0
	)`)
	if err != nil {
		t.Fatalf("insert obs 2: %v", err)
	}

	// Observation links
	_, err = db.Exec(`INSERT INTO observation_links (id, source_id, target_id, relation_type, confidence, created_by, created_at)
		VALUES ('01LINK001', '01OBS001', '01OBS002', 'supersedes', 0.9, 'agent', '2026-03-28 10:00:00')`)
	if err != nil {
		t.Fatalf("insert link: %v", err)
	}

	// File observations
	_, err = db.Exec(`INSERT INTO file_observations (id, observation_id, file_path, commit_sha_from, valid_from, created_at)
		VALUES ('01FILE001', '01OBS001', 'main.go', 'abc123', '2026-03-01 00:00:00', '2026-03-01 00:00:00')`)
	if err != nil {
		t.Fatalf("insert file_obs: %v", err)
	}

	// Facts
	_, err = db.Exec(`INSERT INTO facts (id, subject, predicate, object, observation_id, namespace, valid_from, created_at)
		VALUES ('01FACT001', 'neurox', 'uses', 'sqlite', '01OBS001', 'testns', '2026-03-01 00:00:00', '2026-03-01 00:00:00')`)
	if err != nil {
		t.Fatalf("insert fact: %v", err)
	}

	// Temporal mentions
	_, err = db.Exec(`INSERT INTO temporal_mentions (id, observation_id, raw_text, mention_kind, normalized_start, anchor_time, confidence, created_at)
		VALUES ('01TEMP001', '01OBS001', 'yesterday', 'relative', '2026-03-27', '2026-03-28 10:00:00', 0.9, '2026-03-28 10:00:00')`)
	if err != nil {
		t.Fatalf("insert temporal: %v", err)
	}

	// Sessions
	_, err = db.Exec(`INSERT INTO sessions (id, title, directory, branch, namespace, status, summary, started_at, ended_at)
		VALUES ('01SESS001', 'Test Session', '/tmp', 'main', 'testns', 'completed', 'Did stuff', '2026-03-28 09:00:00', '2026-03-28 10:00:00')`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Reflections
	_, err = db.Exec(`INSERT INTO reflections (id, content, source_observation_ids, namespace, layer, created_at)
		VALUES ('01REFL001', 'Synthesized insight', '01OBS001,01OBS002', 'testns', 2, '2026-03-28 10:00:00')`)
	if err != nil {
		t.Fatalf("insert reflection: %v", err)
	}

	// Consolidation runs
	_, err = db.Exec(`INSERT INTO consolidation_runs (id, status, epoch, observations_processed, observations_promoted, started_at, completed_at)
		VALUES ('01CONS001', 'completed', 1, 10, 2, '2026-03-28 09:00:00', '2026-03-28 09:05:00')`)
	if err != nil {
		t.Fatalf("insert consolidation_run: %v", err)
	}

	// --- Export ---
	exportPath := filepath.Join(t.TempDir(), "export.json")
	stats, err := ExportJSONWithStats(ctx, db, "testns", exportPath)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// Verify export counts
	if stats.Observations != 2 {
		t.Errorf("export observations = %d, want 2", stats.Observations)
	}
	if stats.Links != 1 {
		t.Errorf("export links = %d, want 1", stats.Links)
	}
	if stats.FileObservations != 1 {
		t.Errorf("export file_observations = %d, want 1", stats.FileObservations)
	}
	if stats.Facts != 1 {
		t.Errorf("export facts = %d, want 1", stats.Facts)
	}
	if stats.TemporalMentions != 1 {
		t.Errorf("export temporal_mentions = %d, want 1", stats.TemporalMentions)
	}
	if stats.Sessions != 1 {
		t.Errorf("export sessions = %d, want 1", stats.Sessions)
	}
	if stats.Reflections != 1 {
		t.Errorf("export reflections = %d, want 1", stats.Reflections)
	}
	if stats.ConsolidationRuns != 1 {
		t.Errorf("export consolidation_runs = %d, want 1", stats.ConsolidationRuns)
	}

	// Verify the JSON file is valid
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	var parsed FullExport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse exported JSON: %v", err)
	}
	if parsed.Version != "1.0" {
		t.Errorf("version = %q, want 1.0", parsed.Version)
	}
	if parsed.Namespace != "testns" {
		t.Errorf("namespace = %q, want testns", parsed.Namespace)
	}

	// --- Import into fresh DB ---
	db2 := setupFullSchemaDB(t)
	defer db2.Close()

	importStats, err := ImportJSONWithStats(ctx, db2, exportPath)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Verify import counts match export counts
	if importStats.Observations != stats.Observations {
		t.Errorf("import observations = %d, want %d", importStats.Observations, stats.Observations)
	}
	if importStats.Links != stats.Links {
		t.Errorf("import links = %d, want %d", importStats.Links, stats.Links)
	}
	if importStats.FileObservations != stats.FileObservations {
		t.Errorf("import file_observations = %d, want %d", importStats.FileObservations, stats.FileObservations)
	}
	if importStats.Facts != stats.Facts {
		t.Errorf("import facts = %d, want %d", importStats.Facts, stats.Facts)
	}
	if importStats.TemporalMentions != stats.TemporalMentions {
		t.Errorf("import temporal_mentions = %d, want %d", importStats.TemporalMentions, stats.TemporalMentions)
	}
	if importStats.Sessions != stats.Sessions {
		t.Errorf("import sessions = %d, want %d", importStats.Sessions, stats.Sessions)
	}
	if importStats.Reflections != stats.Reflections {
		t.Errorf("import reflections = %d, want %d", importStats.Reflections, stats.Reflections)
	}

	// --- Verify data integrity: observations ---
	var (
		title, content, obsType, kind, staleness, consolidationStatus string
		layer, accessCount, modifiedEpoch                             int
		importance, confidence, activationLevel, consolidationStr     float64
		tags, sourceSurface, sourceTool                               sql.NullString
	)
	err = db2.QueryRow(`SELECT title, content, observation_type, kind, layer,
		importance, confidence, access_count, staleness, consolidation_status,
		modified_epoch, activation_level, consolidation_strength,
		tags, source_surface, source_tool
		FROM observations WHERE id = '01OBS001'`).Scan(
		&title, &content, &obsType, &kind, &layer,
		&importance, &confidence, &accessCount, &staleness, &consolidationStatus,
		&modifiedEpoch, &activationLevel, &consolidationStr,
		&tags, &sourceSurface, &sourceTool,
	)
	if err != nil {
		t.Fatalf("query imported obs: %v", err)
	}
	if title != "Test Obs One" {
		t.Errorf("title = %q, want 'Test Obs One'", title)
	}
	if obsType != "decision" {
		t.Errorf("type = %q, want 'decision'", obsType)
	}
	if layer != 2 {
		t.Errorf("layer = %d, want 2", layer)
	}
	if accessCount != 5 {
		t.Errorf("access_count = %d, want 5", accessCount)
	}
	if fmt.Sprintf("%.2f", activationLevel) != "0.75" {
		t.Errorf("activation_level = %.2f, want 0.75", activationLevel)
	}
	if fmt.Sprintf("%.2f", consolidationStr) != "0.60" {
		t.Errorf("consolidation_strength = %.2f, want 0.60", consolidationStr)
	}
	if !sourceSurface.Valid || sourceSurface.String != "mcp" {
		t.Errorf("source_surface = %v, want 'mcp'", sourceSurface)
	}
	if !sourceTool.Valid || sourceTool.String != "save" {
		t.Errorf("source_tool = %v, want 'save'", sourceTool)
	}
	if staleness != "fresh" {
		t.Errorf("staleness = %q, want 'fresh'", staleness)
	}
	if consolidationStatus != "promoted" {
		t.Errorf("consolidation_status = %q, want 'promoted'", consolidationStatus)
	}

	// --- Verify observation_links ---
	var linkSourceID, linkTargetID, linkRelType, linkCreatedBy string
	var linkConfidence float64
	err = db2.QueryRow(`SELECT source_id, target_id, relation_type, confidence, created_by
		FROM observation_links WHERE id = '01LINK001'`).Scan(
		&linkSourceID, &linkTargetID, &linkRelType, &linkConfidence, &linkCreatedBy)
	if err != nil {
		t.Fatalf("query imported link: %v", err)
	}
	if linkSourceID != "01OBS001" || linkTargetID != "01OBS002" {
		t.Errorf("link source/target = %s/%s, want 01OBS001/01OBS002", linkSourceID, linkTargetID)
	}
	if linkRelType != "supersedes" {
		t.Errorf("link relation_type = %q, want 'supersedes'", linkRelType)
	}
	if linkCreatedBy != "agent" {
		t.Errorf("link created_by = %q, want 'agent'", linkCreatedBy)
	}

	// --- Verify facts ---
	var factSubj, factPred, factObj string
	err = db2.QueryRow(`SELECT subject, predicate, object FROM facts WHERE id = '01FACT001'`).Scan(
		&factSubj, &factPred, &factObj)
	if err != nil {
		t.Fatalf("query imported fact: %v", err)
	}
	if factSubj != "neurox" || factPred != "uses" || factObj != "sqlite" {
		t.Errorf("fact = %s/%s/%s, want neurox/uses/sqlite", factSubj, factPred, factObj)
	}

	// --- Verify temporal_mentions ---
	var tempRawText, tempKind string
	var tempNormStart sql.NullString
	err = db2.QueryRow(`SELECT raw_text, mention_kind, normalized_start FROM temporal_mentions WHERE id = '01TEMP001'`).Scan(
		&tempRawText, &tempKind, &tempNormStart)
	if err != nil {
		t.Fatalf("query imported temporal: %v", err)
	}
	if tempRawText != "yesterday" {
		t.Errorf("temporal raw_text = %q, want 'yesterday'", tempRawText)
	}
	if tempKind != "relative" {
		t.Errorf("temporal kind = %q, want 'relative'", tempKind)
	}

	// --- Verify sessions ---
	var sessTitle sql.NullString
	var sessStatus string
	err = db2.QueryRow(`SELECT title, status FROM sessions WHERE id = '01SESS001'`).Scan(&sessTitle, &sessStatus)
	if err != nil {
		t.Fatalf("query imported session: %v", err)
	}
	if !sessTitle.Valid || sessTitle.String != "Test Session" {
		t.Errorf("session title = %v, want 'Test Session'", sessTitle)
	}
	if sessStatus != "completed" {
		t.Errorf("session status = %q, want 'completed'", sessStatus)
	}

	// --- Verify reflections ---
	var reflContent string
	err = db2.QueryRow(`SELECT content FROM reflections WHERE id = '01REFL001'`).Scan(&reflContent)
	if err != nil {
		t.Fatalf("query imported reflection: %v", err)
	}
	if reflContent != "Synthesized insight" {
		t.Errorf("reflection content = %q, want 'Synthesized insight'", reflContent)
	}

	// --- Verify re-import is idempotent (INSERT OR IGNORE) ---
	importStats2, err := ImportJSONWithStats(ctx, db2, exportPath)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	// Counts should still reflect total rows attempted (INSERT OR IGNORE doesn't error)
	if importStats2.Observations != 2 {
		t.Errorf("re-import observations attempted = %d, want 2", importStats2.Observations)
	}
	// But no duplicate rows should exist
	var obsCount int
	db2.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&obsCount)
	if obsCount != 2 {
		t.Errorf("total observations after re-import = %d, want 2 (no duplicates)", obsCount)
	}
}

func TestJSONExportEmpty(t *testing.T) {
	db := setupFullSchemaDB(t)
	defer db.Close()

	exportPath := filepath.Join(t.TempDir(), "empty.json")
	stats, err := ExportJSONWithStats(context.Background(), db, "", exportPath)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stats.Observations != 0 {
		t.Errorf("expected 0 observations, got %d", stats.Observations)
	}

	// Verify JSON is valid with empty arrays
	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var parsed FullExport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if parsed.Version != "1.0" {
		t.Errorf("version = %q, want 1.0", parsed.Version)
	}
}

// TestImportMarkdown_PreservesScoringMetadata verifies that re-importing an
// observation via Markdown does NOT reset accumulated scoring metadata
// (activation_level, consolidation_strength, access_count, decay_rate, etc.).
func TestImportMarkdown_PreservesScoringMetadata(t *testing.T) {
	// Use full schema so activation_level and consolidation_strength columns exist.
	db := setupFullSchemaDB(t)
	defer db.Close()

	ctx := context.Background()

	// Insert an observation with high scoring metadata (simulating a well-established observation).
	_, err := db.Exec(`INSERT INTO observations (
		id, title, content, observation_type, kind, layer,
		importance, confidence,
		access_count, decay_rate,
		activation_level, consolidation_strength,
		tags, namespace, staleness, retention,
		consolidation_status,
		source_surface, source_session_id, source_tool,
		created_at, updated_at
	) VALUES (
		'01SCORE001', 'Scoring Test', 'Original content with scoring metadata.', 'decision', 'semantic', 2,
		0.88, 0.95,
		42, 0.6,
		0.95, 0.80,
		'go,scoring', 'testns', 'fresh', 'durable',
		'promoted',
		'mcp', 'session-xyz', 'save',
		'2026-03-01 00:00:00', '2026-03-28 10:00:00'
	)`)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	// Export to Markdown.
	exportDir := t.TempDir()
	n, err := ExportMarkdown(ctx, db, "testns", exportDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 exported, got %d", n)
	}

	// Re-import into the SAME database (simulating re-import of an existing observation).
	imported, err := ImportMarkdown(ctx, db, exportDir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported != 1 {
		t.Fatalf("expected 1 imported, got %d", imported)
	}

	// Verify scoring metadata is preserved, NOT reset to defaults.
	var (
		activationLevel, consolidationStrength, importance, decayRate float64
		accessCount                                                   int
		consolidationStatus                                           string
		sourceSurface, sourceSessionID, sourceTool                    sql.NullString
	)
	err = db.QueryRow(`SELECT
		activation_level, consolidation_strength, importance, decay_rate,
		access_count, consolidation_status,
		source_surface, source_session_id, source_tool
		FROM observations WHERE id = '01SCORE001'`).Scan(
		&activationLevel, &consolidationStrength, &importance, &decayRate,
		&accessCount, &consolidationStatus,
		&sourceSurface, &sourceSessionID, &sourceTool,
	)
	if err != nil {
		t.Fatalf("query after re-import: %v", err)
	}

	// activation_level must stay at 0.95, not reset to default 0.5
	if fmt.Sprintf("%.2f", activationLevel) != "0.95" {
		t.Errorf("activation_level = %.2f, want 0.95 (was reset!)", activationLevel)
	}
	// consolidation_strength must stay at 0.80, not reset to default 0.0
	if fmt.Sprintf("%.2f", consolidationStrength) != "0.80" {
		t.Errorf("consolidation_strength = %.2f, want 0.80 (was reset!)", consolidationStrength)
	}
	// importance: Markdown exports importance=0.88; since existing is also 0.88, MAX(0.88, 0.88) = 0.88
	if fmt.Sprintf("%.2f", importance) != "0.88" {
		t.Errorf("importance = %.2f, want 0.88", importance)
	}
	// access_count must be preserved (42), not reset to 0
	if accessCount != 42 {
		t.Errorf("access_count = %d, want 42 (was reset!)", accessCount)
	}
	// decay_rate must be preserved (0.6), not reset to 1.0
	if fmt.Sprintf("%.1f", decayRate) != "0.6" {
		t.Errorf("decay_rate = %.1f, want 0.6 (was reset!)", decayRate)
	}
	// consolidation_status must be preserved ("promoted"), not reset to "pending"
	if consolidationStatus != "promoted" {
		t.Errorf("consolidation_status = %q, want 'promoted' (was reset!)", consolidationStatus)
	}
	// source_surface must be preserved ("mcp"), not set to NULL
	if !sourceSurface.Valid || sourceSurface.String != "mcp" {
		t.Errorf("source_surface = %v, want 'mcp' (was reset!)", sourceSurface)
	}
	// source_session_id must be preserved
	if !sourceSessionID.Valid || sourceSessionID.String != "session-xyz" {
		t.Errorf("source_session_id = %v, want 'session-xyz' (was reset!)", sourceSessionID)
	}
	// source_tool must be preserved
	if !sourceTool.Valid || sourceTool.String != "save" {
		t.Errorf("source_tool = %v, want 'save' (was reset!)", sourceTool)
	}
}

// TestImportMarkdown_NewObservation verifies that importing an observation that
// doesn't exist in the DB inserts it correctly with all available fields.
func TestImportMarkdown_NewObservation(t *testing.T) {
	db := setupFullSchemaDB(t)
	defer db.Close()

	// Create a Markdown file for a brand new observation.
	dir := t.TempDir()
	md := `---
id: 01NEW00001
type: pattern
kind: procedural
layer: working
importance: 0.75
confidence: 0.85
tags: go,new
namespace: newns
staleness: fresh
retention: durable
created_at: 2026-03-15 12:00:00
---

# Brand New Observation

This observation does not exist in the DB yet.
`
	if err := os.WriteFile(filepath.Join(dir, "Brand New Observation.md"), []byte(md), 0644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	imported, err := ImportMarkdown(context.Background(), db, dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported != 1 {
		t.Fatalf("expected 1 imported, got %d", imported)
	}

	// Verify it was inserted with the right values.
	var title, content, obsType, kind, ns string
	var layer int
	var importance, confidence float64
	err = db.QueryRow(`SELECT title, content, observation_type, kind, layer, importance, confidence, namespace
		FROM observations WHERE id = '01NEW00001'`).Scan(
		&title, &content, &obsType, &kind, &layer, &importance, &confidence, &ns,
	)
	if err != nil {
		t.Fatalf("query new obs: %v", err)
	}
	if title != "Brand New Observation" {
		t.Errorf("title = %q, want 'Brand New Observation'", title)
	}
	if obsType != "pattern" {
		t.Errorf("type = %q, want 'pattern'", obsType)
	}
	if kind != "procedural" {
		t.Errorf("kind = %q, want 'procedural'", kind)
	}
	if layer != 1 {
		t.Errorf("layer = %d, want 1 (working)", layer)
	}
	if fmt.Sprintf("%.2f", importance) != "0.75" {
		t.Errorf("importance = %.2f, want 0.75", importance)
	}
	if fmt.Sprintf("%.2f", confidence) != "0.85" {
		t.Errorf("confidence = %.2f, want 0.85", confidence)
	}
	if ns != "newns" {
		t.Errorf("namespace = %q, want 'newns'", ns)
	}
}

// TestImportMarkdown_ImportanceOnlyIncrease verifies that importance is only
// updated when the imported value is higher than the existing value.
func TestImportMarkdown_ImportanceOnlyIncrease(t *testing.T) {
	db := setupFullSchemaDB(t)
	defer db.Close()

	// Insert an observation with high importance.
	_, err := db.Exec(`INSERT INTO observations (
		id, title, content, observation_type, kind, layer,
		importance, confidence,
		tags, namespace, staleness, retention,
		created_at, updated_at
	) VALUES (
		'01IMP00001', 'Importance Test', 'High importance content.', 'decision', 'semantic', 2,
		0.95, 0.9,
		'go', 'testns', 'fresh', 'durable',
		'2026-03-01 00:00:00', '2026-03-01 00:00:00'
	)`)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	// Create a Markdown file with LOWER importance.
	dir := t.TempDir()
	md := `---
id: 01IMP00001
type: decision
kind: semantic
layer: core
importance: 0.50
confidence: 0.9
tags: go
namespace: testns
staleness: fresh
retention: durable
---

# Importance Test

High importance content with lower import value.
`
	if err := os.WriteFile(filepath.Join(dir, "Importance Test.md"), []byte(md), 0644); err != nil {
		t.Fatalf("write md: %v", err)
	}

	_, err = ImportMarkdown(context.Background(), db, dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// importance should stay at 0.95 (existing) because 0.95 > 0.50 (imported)
	var importance float64
	err = db.QueryRow(`SELECT importance FROM observations WHERE id = '01IMP00001'`).Scan(&importance)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if fmt.Sprintf("%.2f", importance) != "0.95" {
		t.Errorf("importance = %.2f, want 0.95 (should not decrease)", importance)
	}

	// Now import with HIGHER importance.
	dir2 := t.TempDir()
	md2 := `---
id: 01IMP00001
type: decision
kind: semantic
layer: core
importance: 0.99
confidence: 0.9
tags: go
namespace: testns
staleness: fresh
retention: durable
---

# Importance Test

Updated with even higher importance.
`
	if err := os.WriteFile(filepath.Join(dir2, "Importance Test.md"), []byte(md2), 0644); err != nil {
		t.Fatalf("write md2: %v", err)
	}

	_, err = ImportMarkdown(context.Background(), db, dir2)
	if err != nil {
		t.Fatalf("import2: %v", err)
	}

	// importance should now be 0.99 because 0.99 > 0.95
	err = db.QueryRow(`SELECT importance FROM observations WHERE id = '01IMP00001'`).Scan(&importance)
	if err != nil {
		t.Fatalf("query2: %v", err)
	}
	if fmt.Sprintf("%.2f", importance) != "0.99" {
		t.Errorf("importance = %.2f, want 0.99 (should increase)", importance)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "Hello World"},
		{"foo/bar", "foo-bar"},
		{"a:b*c?", "a-b-c"},
		{strings.Repeat("x", 100), strings.Repeat("x", 80)},
		{"", "observation-"}, // prefix check only
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.in)
		if tc.in == "" {
			if !strings.HasPrefix(got, "observation-") {
				t.Errorf("empty title: expected observation- prefix, got %s", got)
			}
		} else if got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
