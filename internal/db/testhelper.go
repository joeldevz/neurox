package db

import (
	"context"
	"database/sql"
	"testing"
)

// TestDB wraps *sql.DB with test helper methods.
type TestDB struct {
	DB *sql.DB
}

// Exec runs an SQL statement and fails the test on error.
func (tdb *TestDB) Exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := tdb.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query[:min(len(query), 60)], err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
