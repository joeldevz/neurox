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

func TestMigration010ActivationSignals(t *testing.T) {
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

	// Verify migration 010 is recorded
	var migration010Exists int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = 10").Scan(&migration010Exists); err != nil {
		t.Fatalf("migration 010 query failed: %v", err)
	}
	if migration010Exists != 1 {
		t.Fatalf("migration 010 not recorded, got count = %d", migration010Exists)
	}

	// Insert test observations - they should get default values from schema
	_, err = database.ExecContext(ctx, `
		INSERT INTO observations (id, title, content, layer, importance, access_count, created_at)
		VALUES 
			('test1', 'Test 1', 'Content', 2, 0.8, 10, datetime('now')),
			('test2', 'Test 2', 'Content', 1, 0.5, 5, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert test observations failed: %v", err)
	}

	// Verify columns exist and have default values
	testCases := []struct {
		id string
		// New observations get schema defaults: activation_level=0.5, consolidation_strength=0.0
		wantActivation    float64
		wantConsolidation float64
	}{
		{"test1", 0.5, 0.0},
		{"test2", 0.5, 0.0},
	}

	for _, tc := range testCases {
		var activation, consolidation float64
		err := database.QueryRowContext(ctx, `
			SELECT activation_level, consolidation_strength 
			FROM observations 
			WHERE id = ?
		`, tc.id).Scan(&activation, &consolidation)
		if err != nil {
			t.Fatalf("query %s failed: %v", tc.id, err)
		}

		if activation != tc.wantActivation {
			t.Errorf("%s: activation_level = %v, want %v",
				tc.id, activation, tc.wantActivation)
		}
		if consolidation != tc.wantConsolidation {
			t.Errorf("%s: consolidation_strength = %v, want %v",
				tc.id, consolidation, tc.wantConsolidation)
		}
	}

	// Verify we can update the values
	_, err = database.ExecContext(ctx, `
		UPDATE observations 
		SET activation_level = 0.85, consolidation_strength = 0.75
		WHERE id = 'test1'
	`)
	if err != nil {
		t.Fatalf("update activation signals failed: %v", err)
	}

	var updatedActivation, updatedConsolidation float64
	err = database.QueryRowContext(ctx, `
		SELECT activation_level, consolidation_strength 
		FROM observations 
		WHERE id = 'test1'
	`).Scan(&updatedActivation, &updatedConsolidation)
	if err != nil {
		t.Fatalf("query updated observation failed: %v", err)
	}

	if updatedActivation != 0.85 {
		t.Errorf("updated activation_level = %v, want 0.85", updatedActivation)
	}
	if updatedConsolidation != 0.75 {
		t.Errorf("updated consolidation_strength = %v, want 0.75", updatedConsolidation)
	}

	// Verify index exists
	var indexCount int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM sqlite_master 
		WHERE type = 'index' AND name = 'idx_obs_activation'
	`).Scan(&indexCount); err != nil {
		t.Fatalf("index query failed: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_obs_activation index count = %d, want 1", indexCount)
	}

	// Verify CHECK constraints work (values must be between 0 and 1)
	_, err = database.ExecContext(ctx, `
		UPDATE observations SET activation_level = 1.5 WHERE id = 'test2'
	`)
	if err == nil {
		t.Error("expected error for activation_level > 1, got nil")
	}

	_, err = database.ExecContext(ctx, `
		UPDATE observations SET consolidation_strength = -0.5 WHERE id = 'test2'
	`)
	if err == nil {
		t.Error("expected error for consolidation_strength < 0, got nil")
	}
}

func TestMigration011ReconcileScores(t *testing.T) {
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

	// Verify migration 011 is recorded
	var migration011Exists int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = 11").Scan(&migration011Exists); err != nil {
		t.Fatalf("migration 011 query failed: %v", err)
	}
	if migration011Exists != 1 {
		t.Fatalf("migration 011 not recorded, got count = %d", migration011Exists)
	}

	// Insert test data BEFORE running migration (simulate pre-existing data)
	// We need to insert with the depressed importance values
	_, err = database.ExecContext(ctx, `
		INSERT INTO observations (id, title, content, observation_type, layer, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES 
			('core_dec', 'Core Decision', 'Content', 'decision', 2, 0.01, 0.1, 0.2, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days')),
			('core_ops', 'Core Operational', 'Content', 'discovery', 2, 0.01, 0.1, 0.2, 'semantic', 'default', 'operational', 10, datetime('now', '-30 days')),
			('working_bug', 'Working Bugfix', 'Content', 'bugfix', 1, 0.01, 0.1, 0.15, 'semantic', 'default', 'durable', 5, datetime('now', '-30 days')),
			('buf_disc', 'Buffer Discovery', 'Content', 'discovery', 0, 0.01, 0.1, 0.1, 'semantic', 'default', 'durable', 3, datetime('now', '-30 days'))
	`)
	if err != nil {
		t.Fatalf("insert test observations failed: %v", err)
	}

	// Run migration 011 manually to test the backfill
	migrationScript, err := schemaFS.ReadFile("011_reconcile_scores.sql")
	if err != nil {
		t.Fatalf("read migration 011 failed: %v", err)
	}

	_, err = database.ExecContext(ctx, string(migrationScript))
	if err != nil {
		t.Fatalf("apply migration 011 failed: %v", err)
	}

	// Verify durable Core observation was recalibrated
	var coreDecImportance float64
	err = database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'core_dec'").Scan(&coreDecImportance)
	if err != nil {
		t.Fatalf("query core_dec failed: %v", err)
	}
	if coreDecImportance < 0.70 {
		t.Errorf("core_dec: importance = %.3f, want >= 0.70 (decision in Core)", coreDecImportance)
	}

	// Verify operational Core observation was NOT recalibrated
	var coreOpsImportance float64
	err = database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'core_ops'").Scan(&coreOpsImportance)
	if err != nil {
		t.Fatalf("query core_ops failed: %v", err)
	}
	if coreOpsImportance != 0.01 {
		t.Errorf("core_ops: importance = %.3f, want = 0.01 (operational should not be recalibrated)", coreOpsImportance)
	}

	// Verify Working observation was recalibrated
	var workingBugImportance float64
	err = database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'working_bug'").Scan(&workingBugImportance)
	if err != nil {
		t.Fatalf("query working_bug failed: %v", err)
	}
	if workingBugImportance < 0.60 {
		t.Errorf("working_bug: importance = %.3f, want >= 0.60 (bugfix in Working)", workingBugImportance)
	}

	// Verify Buffer observation was recalibrated
	var bufDiscImportance float64
	err = database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'buf_disc'").Scan(&bufDiscImportance)
	if err != nil {
		t.Fatalf("query buf_disc failed: %v", err)
	}
	if bufDiscImportance < 0.30 {
		t.Errorf("buf_disc: importance = %.3f, want >= 0.30 (discovery in Buffer)", bufDiscImportance)
	}

	// Verify index was created
	var indexCount int
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM sqlite_master 
		WHERE type = 'index' AND name = 'idx_obs_importance_layer'
	`).Scan(&indexCount); err != nil {
		t.Fatalf("index query failed: %v", err)
	}
	if indexCount != 1 {
		t.Errorf("idx_obs_importance_layer index count = %d, want 1", indexCount)
	}
}

// TestOpenVerifiesFTS5Available tests that Open() succeeds when FTS5 is available.
// This test passes under the fts5 build tag. The negative path (missing FTS5) cannot
// be tested under the fts5 tag, as the build itself includes FTS5 support.
// To verify the error path, manually run: CGO_ENABLED=1 go build ./... (no -tags)
// and verify the binary fails with "FTS5 support is not compiled in" on startup.
func TestOpenVerifiesFTS5Available(t *testing.T) {
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

	// Verify that FTS5 support is available by checking pragma_compile_options
	var fts5Count int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM pragma_compile_options WHERE compile_options LIKE 'ENABLE_FTS5%'").Scan(&fts5Count); err != nil {
		t.Fatalf("pragma_compile_options query failed: %v", err)
	}
	if fts5Count == 0 {
		t.Fatalf("FTS5 not found in compile options, but Open() succeeded unexpectedly")
	}

	// Verify the FTS5 virtual table exists and is queryable
	var tableType string
	if err := database.QueryRowContext(ctx, "SELECT type FROM sqlite_master WHERE name = 'observations_fts' AND type = 'table'").Scan(&tableType); err != nil {
		t.Fatalf("observations_fts lookup failed: %v", err)
	}
	if tableType != "table" {
		t.Fatalf("observations_fts exists but is not a table (type=%s)", tableType)
	}
}
