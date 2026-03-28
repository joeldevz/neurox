package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBackupCreatesValidCopy(t *testing.T) {
	ctx := context.Background()

	// Create and populate a source database.
	srcPath := filepath.Join(t.TempDir(), "source.db")
	srcDB, err := Open(ctx, srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	// Insert test observations.
	_, err = srcDB.ExecContext(ctx, `
		INSERT INTO observations (id, title, content, namespace, created_at)
		VALUES 
			('obs1', 'First Observation', 'Content one', 'test-ns', datetime('now')),
			('obs2', 'Second Observation', 'Content two', 'test-ns', datetime('now')),
			('obs3', 'Third Observation', 'Content three', 'test-ns', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert test data: %v", err)
	}

	// Also insert a session and a link to verify full DB copy.
	_, err = srcDB.ExecContext(ctx, `
		INSERT INTO sessions (id, title, namespace, status)
		VALUES ('sess1', 'Test Session', 'test-ns', 'active')
	`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// Run backup.
	destPath := filepath.Join(t.TempDir(), "backup.db")
	if err := Backup(ctx, srcDB, destPath); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Open the backup database and verify data.
	backupDB, err := Open(ctx, destPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupDB.Close()

	// Verify observation count.
	var obsCount int
	if err := backupDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations").Scan(&obsCount); err != nil {
		t.Fatalf("count observations in backup: %v", err)
	}
	if obsCount != 3 {
		t.Errorf("observations in backup = %d, want 3", obsCount)
	}

	// Verify specific observation content.
	var title, content string
	if err := backupDB.QueryRowContext(ctx, "SELECT title, content FROM observations WHERE id = 'obs1'").Scan(&title, &content); err != nil {
		t.Fatalf("query obs1 in backup: %v", err)
	}
	if title != "First Observation" {
		t.Errorf("obs1 title = %q, want %q", title, "First Observation")
	}
	if content != "Content one" {
		t.Errorf("obs1 content = %q, want %q", content, "Content one")
	}

	// Verify session was copied.
	var sessionCount int
	if err := backupDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = 'sess1'").Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions in backup: %v", err)
	}
	if sessionCount != 1 {
		t.Errorf("sessions in backup = %d, want 1", sessionCount)
	}
}

func TestBackupWithResult(t *testing.T) {
	ctx := context.Background()

	srcPath := filepath.Join(t.TempDir(), "source.db")
	srcDB, err := Open(ctx, srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	// Insert some data so the backup has non-zero size.
	_, err = srcDB.ExecContext(ctx, `
		INSERT INTO observations (id, title, content, namespace, created_at)
		VALUES ('obs1', 'Test', 'Content', 'ns', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "backup.db")
	result, err := BackupWithResult(ctx, srcDB, destPath)
	if err != nil {
		t.Fatalf("backup with result: %v", err)
	}

	if result.Path != destPath {
		t.Errorf("result.Path = %q, want %q", result.Path, destPath)
	}
	if result.SizeBytes <= 0 {
		t.Errorf("result.SizeBytes = %d, want > 0", result.SizeBytes)
	}
	if result.Message == "" {
		t.Error("result.Message is empty")
	}
}

func TestBackupEmptyDestPath(t *testing.T) {
	ctx := context.Background()

	srcPath := filepath.Join(t.TempDir(), "source.db")
	srcDB, err := Open(ctx, srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	if err := Backup(ctx, srcDB, ""); err == nil {
		t.Fatal("expected error for empty dest path, got nil")
	}
}

func TestBackupCreatesDirectory(t *testing.T) {
	ctx := context.Background()

	srcPath := filepath.Join(t.TempDir(), "source.db")
	srcDB, err := Open(ctx, srcPath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer srcDB.Close()

	// Backup to a nested directory that doesn't exist yet.
	destPath := filepath.Join(t.TempDir(), "nested", "deep", "backup.db")
	if err := Backup(ctx, srcDB, destPath); err != nil {
		t.Fatalf("backup to nested dir: %v", err)
	}

	// Verify the backup file exists and is a valid SQLite database.
	backupDB, err := Open(ctx, destPath)
	if err != nil {
		t.Fatalf("open backup at nested path: %v", err)
	}
	defer backupDB.Close()

	assertTableExists(t, backupDB, "observations")
}

func TestDefaultBackupPath(t *testing.T) {
	got := DefaultBackupPath("/home/user/.config/neurox/neurox.db")
	want := "/home/user/.config/neurox/neurox.db.backup"
	if got != want {
		t.Errorf("DefaultBackupPath = %q, want %q", got, want)
	}
}
