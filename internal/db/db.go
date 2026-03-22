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

	return database, nil
}

func configure(ctx context.Context, database *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA temp_store = MEMORY;",
	}

	for _, statement := range pragmas {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply pragma %q: %w", statement, err)
		}
	}

	return nil
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

		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", item.version, err)
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
