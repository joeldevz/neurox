package export

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
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
