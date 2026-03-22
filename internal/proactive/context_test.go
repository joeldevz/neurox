package proactive

import (
	"context"
	"path/filepath"
	"testing"

	"neurox/internal/db"
	"neurox/internal/embed"
)

func setupTest(t *testing.T) (*Engine, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	engine := NewEngine(database, embed.Disabled{})
	return engine, &db.TestDB{DB: database}
}

func TestGetContextEmpty(t *testing.T) {
	e, _ := setupTest(t)
	ctx := context.Background()

	result, err := e.GetContext(ctx, "empty_ns", nil, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	if result.Count != 0 {
		t.Errorf("count = %d, want 0", result.Count)
	}
}

func TestGetContextWithObservations(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert observations at different layers
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('CORE1', 'Core decision', 'content', 'decision', 2, 0.9, 0.9, 'semantic', 'app')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('WORK1', 'Working obs', 'content', 'discovery', 1, 0.7, 0.5, 'semantic', 'app')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('BUF1', 'Buffer obs', 'content', 'discovery', 0, 0.5, 0.3, 'semantic', 'app')`)

	result, err := e.GetContext(ctx, "app", nil, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	if result.Count == 0 {
		t.Error("expected observations in context")
	}
	// Core should come first
	if result.Items[0].ID != "CORE1" {
		t.Errorf("first item = %s, want CORE1 (highest layer)", result.Items[0].ID)
	}
}

func TestGetContextWithFiles(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert observation linked to a file
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('LINKED1', 'File-linked obs', 'content about auth.go', 'discovery', 1, 0.7, 0.6, 'semantic', 'app')`)
	tdb.Exec(t, `INSERT INTO file_observations(id, observation_id, file_path)
		VALUES('FL1', 'LINKED1', 'src/auth.go')`)

	// Insert unlinked observation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('UNLINKED1', 'Unlinked obs', 'content', 'discovery', 1, 0.7, 0.5, 'semantic', 'app')`)

	result, err := e.GetContext(ctx, "app", []string{"src/auth.go"}, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	// File-linked should be first
	if len(result.Items) == 0 {
		t.Fatal("expected items")
	}
	if result.Items[0].ID != "LINKED1" {
		t.Errorf("first item = %s, want LINKED1 (file-linked)", result.Items[0].ID)
	}
}

func TestGetContextIncludesReflections(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert a reflection (content must be > 50 chars to pass quality filter)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
		VALUES('REFL1', 'Reflection: app', 'High-level insight synthesized from multiple observations about architecture and patterns in the codebase.', 'pattern', 2, 0.9, 0.9, 'semantic', 'app', 'reflection')`)

	result, err := e.GetContext(ctx, "app", nil, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	if len(result.Reflections) != 1 {
		t.Errorf("reflections = %d, want 1", len(result.Reflections))
	}
}

func TestGetContextDurableRanksAboveOperational(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert two observations at the same layer and importance, but different retention.
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('DUR1', 'Durable decision', 'content', 'decision', 1, 0.7, 0.6, 'semantic', 'app', 'durable')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('OPS1', 'Step 4 tracking', 'content', 'discovery', 1, 0.7, 0.6, 'semantic', 'app', 'operational')`)

	result, err := e.GetContext(ctx, "app", nil, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}
	if len(result.Items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(result.Items))
	}
	// Durable should come before operational at the same layer/importance.
	if result.Items[0].ID != "DUR1" {
		t.Errorf("first item = %s, want DUR1 (durable ranks above operational)", result.Items[0].ID)
	}
	if result.Items[1].ID != "OPS1" {
		t.Errorf("second item = %s, want OPS1", result.Items[1].ID)
	}
	// Verify retention field is populated.
	if result.Items[0].Retention != "durable" {
		t.Errorf("DUR1 retention = %q, want 'durable'", result.Items[0].Retention)
	}
	if result.Items[1].Retention != "operational" {
		t.Errorf("OPS1 retention = %q, want 'operational'", result.Items[1].Retention)
	}
}

func TestGetContextExcludesOldOperational(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert an old operational observation (>7 days ago) and a durable one.
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, created_at)
		VALUES('OLD_OPS', 'Step 1 old', 'content', 'discovery', 1, 0.7, 0.6, 'semantic', 'app', 'operational', datetime('now', '-10 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, created_at)
		VALUES('OLD_DUR', 'Old but durable', 'content', 'decision', 1, 0.7, 0.6, 'semantic', 'app', 'durable', datetime('now', '-10 days'))`)

	result, err := e.GetContext(ctx, "app", nil, 10)
	if err != nil {
		t.Fatalf("get context: %v", err)
	}

	foundOps := false
	foundDur := false
	for _, item := range result.Items {
		if item.ID == "OLD_OPS" {
			foundOps = true
		}
		if item.ID == "OLD_DUR" {
			foundDur = true
		}
	}
	if foundOps {
		t.Error("old operational observation should be excluded from context")
	}
	if !foundDur {
		t.Error("old durable observation should still appear in context")
	}
}
