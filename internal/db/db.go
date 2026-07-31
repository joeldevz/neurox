package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
//go:embed 002_temporal_mentions.sql
//go:embed 003_tool_calls.sql
//go:embed 004_retention_policy.sql
//go:embed 005_backfill_retention.sql
//go:embed 006_curation.sql
//go:embed 007_rescue_expired.sql
//go:embed 008_db_settings.sql
//go:embed 009_reconcile_active_reflections.sql
//go:embed 010_activation_signals.sql
//go:embed 011_reconcile_scores.sql
//go:embed 012_provenance.sql
//go:embed 013_facts_fts.sql
var schemaFS embed.FS

type migration struct {
	version int
	name    string
	path    string
}

var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		path:    "schema.sql",
	},
	{
		version: 2,
		name:    "temporal_mentions",
		path:    "002_temporal_mentions.sql",
	},
	{
		version: 3,
		name:    "tool_calls",
		path:    "003_tool_calls.sql",
	},
	{
		version: 4,
		name:    "retention_policy",
		path:    "004_retention_policy.sql",
	},
	{
		version: 5,
		name:    "backfill_retention",
		path:    "005_backfill_retention.sql",
	},
	{
		version: 6,
		name:    "curation",
		path:    "006_curation.sql",
	},
	{
		version: 7,
		name:    "rescue_expired",
		path:    "007_rescue_expired.sql",
	},
	{
		version: 8,
		name:    "db_settings",
		path:    "008_db_settings.sql",
	},
	{
		version: 9,
		name:    "reconcile_active_reflections",
		path:    "009_reconcile_active_reflections.sql",
	},
	{
		version: 10,
		name:    "activation_signals",
		path:    "010_activation_signals.sql",
	},
	{
		version: 11,
		name:    "reconcile_scores",
		path:    "011_reconcile_scores.sql",
	},
	{
		version: 12,
		name:    "provenance",
		path:    "012_provenance.sql",
	},
	{
		version: 13,
		name:    "facts_fts",
		path:    "013_facts_fts.sql",
	},
}

func Open(ctx context.Context, databasePath string) (*sql.DB, error) {
	if strings.TrimSpace(databasePath) == "" {
		return nil, fmt.Errorf("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	if err := configure(ctx, database); err != nil {
		_ = database.Close()
		return nil, err
	}

	if err := Migrate(ctx, database); err != nil {
		_ = database.Close()
		return nil, err
	}

	// FTS5 availability check: fail fast if FTS5 isn't compiled in
	var fts5Check int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM pragma_compile_options WHERE compile_options LIKE 'ENABLE_FTS5%'").Scan(&fts5Check); err != nil || fts5Check == 0 {
		_ = database.Close()
		return nil, fmt.Errorf("FTS5 support is not compiled in — rebuild with `CGO_ENABLED=1 go build -tags fts5 ./...`")
	}

	return database, nil
}

func configure(ctx context.Context, database *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 15000;",
		"PRAGMA temp_store = MEMORY;",
	}

	for _, statement := range pragmas {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply pragma %q: %w", statement, err)
		}
	}

	return nil
}

// WALCheckpoint runs a passive WAL checkpoint to keep the WAL file small.
// It uses PASSIVE mode so it never blocks writers. Returns the number of
// WAL frames written back to the database, or an error.
func WALCheckpoint(ctx context.Context, database *sql.DB) (int, error) {
	var busy, log, checkpointed int
	err := database.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE);").Scan(&busy, &log, &checkpointed)
	if err != nil {
		return 0, fmt.Errorf("wal checkpoint: %w", err)
	}
	return checkpointed, nil
}

func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}

	for _, item := range migrations {
		var exists int
		if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = ?", item.version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", item.version, err)
		}
		if exists > 0 {
			continue
		}

		script, err := schemaFS.ReadFile(item.path)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", item.version, err)
		}

		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}

		// Execute migration script - for idempotent migrations, ignore duplicate column errors
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			// Check if this is a "duplicate column" error which is safe to ignore for idempotent migrations
			errStr := err.Error()
			if !strings.Contains(errStr, "duplicate column name") {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d: %w", item.version, err)
			}
			// Duplicate column error - log but continue (idempotent migration)
			// This allows migrations to be re-run safely
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name) VALUES(?, ?)", item.version, item.name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", item.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", item.version, err)
		}
	}

	return nil
}
