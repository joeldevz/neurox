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
