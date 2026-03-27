package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndWALMode(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "neurox.db")

	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("Close returned error: %v", closeErr)
		}
	}()

	assertTableExists(t, database, "observations")
	assertTableExists(t, database, "observations_fts")
	assertTableExists(t, database, "observation_links")
	assertTableExists(t, database, "file_observations")
	assertTableExists(t, database, "sessions")
	assertTableExists(t, database, "facts")
	assertTableExists(t, database, "consolidation_runs")
	assertTableExists(t, database, "reflections")
	assertTableExists(t, database, "temporal_mentions")

	var mode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("journal_mode query failed: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	var migrationCount int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = 1").Scan(&migrationCount); err != nil {
		t.Fatalf("migration query failed: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migrationCount = %d, want 1", migrationCount)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "neurox.db")

	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("Close returned error: %v", closeErr)
		}
	}()

	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("second Migrate returned error: %v", err)
	}

	var migrationCount int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("migration query failed: %v", err)
	}
	if migrationCount != len(migrations) {
		t.Fatalf("migrationCount = %d, want %d", migrationCount, len(migrations))
	}
}

func assertTableExists(t *testing.T, database *sql.DB, name string) {
	t.Helper()

	var count int
	if err := database.QueryRow("SELECT COUNT(1) FROM sqlite_master WHERE name = ?", name).Scan(&count); err != nil {
		t.Fatalf("table lookup for %s failed: %v", name, err)
	}
	if count != 1 {
		t.Fatalf("table %s not found", name)
	}
}

func TestMigration009ReconcileActiveReflections(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "neurox.db")

	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Fatalf("Close returned error: %v", closeErr)
		}
	}()

	// Verify migration 009 is recorded
	var migration009Exists int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = 9").Scan(&migration009Exists); err != nil {
		t.Fatalf("migration 009 query failed: %v", err)
	}
	if migration009Exists != 1 {
		t.Fatalf("migration 009 not recorded, got count = %d", migration009Exists)
	}

	// Insert test data: multiple reflections in same namespace
	_, err = database.ExecContext(ctx, `
		INSERT INTO observations (id, title, content, namespace, source, created_at)
		VALUES 
			('ref1', 'Reflection 1', 'Content 1', 'ns1', 'reflection', datetime('now', '-2 days')),
			('ref2', 'Reflection 2', 'Content 2', 'ns1', 'reflection', datetime('now', '-1 day')),
			('ref3', 'Reflection 3', 'Content 3', 'ns1', 'reflection', datetime('now')),
			('ref4', 'Reflection 4', 'Content 4', 'ns2', 'reflection', datetime('now', '-1 day')),
			('ref5', 'Reflection 5', 'Content 5', 'ns2', 'reflection', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert test reflections failed: %v", err)
	}

	// Run migration 009 again manually to test the reconciliation logic
	migrationScript, err := schemaFS.ReadFile("009_reconcile_active_reflections.sql")
	if err != nil {
		t.Fatalf("read migration 009 failed: %v", err)
	}

	_, err = database.ExecContext(ctx, string(migrationScript))
	if err != nil {
		t.Fatalf("apply migration 009 failed: %v", err)
	}

	// Verify only one active reflection per namespace remains
	var ns1Count, ns2Count int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations 
		WHERE namespace = 'ns1' AND source = 'reflection' AND deleted_at IS NULL
	`).Scan(&ns1Count); err != nil {
		t.Fatalf("query ns1 count failed: %v", err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations 
		WHERE namespace = 'ns2' AND source = 'reflection' AND deleted_at IS NULL
	`).Scan(&ns2Count); err != nil {
		t.Fatalf("query ns2 count failed: %v", err)
	}

	if ns1Count != 1 {
		t.Errorf("ns1 active reflections = %d, want 1", ns1Count)
	}
	if ns2Count != 1 {
		t.Errorf("ns2 active reflections = %d, want 1", ns2Count)
	}

	// Verify the most recent reflection is kept (ref3 for ns1, ref5 for ns2)
	var activeID1, activeID2 string
	if err := database.QueryRowContext(ctx, `
		SELECT id FROM observations 
		WHERE namespace = 'ns1' AND source = 'reflection' AND deleted_at IS NULL
	`).Scan(&activeID1); err != nil {
		t.Fatalf("query ns1 active reflection failed: %v", err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT id FROM observations 
		WHERE namespace = 'ns2' AND source = 'reflection' AND deleted_at IS NULL
	`).Scan(&activeID2); err != nil {
		t.Fatalf("query ns2 active reflection failed: %v", err)
	}

	if activeID1 != "ref3" {
		t.Errorf("ns1 active reflection = %s, want ref3", activeID1)
	}
	if activeID2 != "ref5" {
		t.Errorf("ns2 active reflection = %s, want ref5", activeID2)
	}

	// Verify soft-deleted count
	var deletedCount int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations 
		WHERE source = 'reflection' AND deleted_at IS NOT NULL
	`).Scan(&deletedCount); err != nil {
		t.Fatalf("query deleted count failed: %v", err)
	}
	if deletedCount != 3 {
		t.Errorf("deleted reflections = %d, want 3", deletedCount)
	}
}
