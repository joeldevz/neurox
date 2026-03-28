package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// Backup performs a safe, consistent backup of a SQLite database using
// VACUUM INTO, which creates a clean, compacted copy of the database.
//
// VACUUM INTO is atomic (succeeds completely or not at all), works with
// WAL-mode databases, and requires no driver-specific types — just
// standard database/sql. Available since SQLite 3.27.0.
//
// The destination file is created (or overwritten) at destPath.
// The parent directory of destPath is created if it does not exist.
func Backup(ctx context.Context, srcDB *sql.DB, destPath string) error {
	if destPath == "" {
		return fmt.Errorf("backup destination path is required")
	}

	// Ensure destination directory exists.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	// Remove existing backup file to start fresh.
	// VACUUM INTO fails if the destination file already exists.
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing backup: %w", err)
	}

	// VACUUM INTO creates a clean, compacted copy of the database.
	// The path is passed as a parameter to avoid SQL injection.
	if _, err := srcDB.ExecContext(ctx, "VACUUM INTO ?", destPath); err != nil {
		return fmt.Errorf("backup via VACUUM INTO: %w", err)
	}

	return nil
}

// BackupResult holds the result of a backup operation.
type BackupResult struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Message   string `json:"message"`
}

// BackupWithResult performs a backup and returns metadata about the result.
func BackupWithResult(ctx context.Context, srcDB *sql.DB, destPath string) (BackupResult, error) {
	if err := Backup(ctx, srcDB, destPath); err != nil {
		return BackupResult{}, err
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return BackupResult{}, fmt.Errorf("stat backup file: %w", err)
	}

	return BackupResult{
		Path:      destPath,
		SizeBytes: info.Size(),
		Message:   "backup completed successfully",
	}, nil
}

// DefaultBackupPath returns the default backup destination:
// ~/.config/neurox/neurox.db.backup (or equivalent XDG path).
func DefaultBackupPath(dbPath string) string {
	return dbPath + ".backup"
}
