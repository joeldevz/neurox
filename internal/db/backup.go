package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// Backup performs a safe, consistent backup of a SQLite database using
// the online backup API. This is the only reliable way to copy a
// WAL-mode database while writers may be active.
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
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing backup: %w", err)
	}

	// Open a dedicated source connection for the backup.
	srcConn, err := srcDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire source connection: %w", err)
	}
	defer srcConn.Close()

	// Open the destination database directly.
	destDB, err := sql.Open("sqlite3", destPath)
	if err != nil {
		return fmt.Errorf("open destination database: %w", err)
	}
	defer destDB.Close()

	destConn, err := destDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire destination connection: %w", err)
	}
	defer destConn.Close()

	// Access raw SQLite connections and perform backup.
	return srcConn.Raw(func(srcRaw interface{}) error {
		srcSQLiteConn, ok := srcRaw.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("source connection is not *sqlite3.SQLiteConn")
		}

		return destConn.Raw(func(destRaw interface{}) error {
			destSQLiteConn, ok := destRaw.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("destination connection is not *sqlite3.SQLiteConn")
			}

			// Backup is called on the destination conn, passing the source conn.
			backup, err := destSQLiteConn.Backup("main", srcSQLiteConn, "main")
			if err != nil {
				return fmt.Errorf("init backup: %w", err)
			}

			// Step(-1) copies all pages in one call.
			done, err := backup.Step(-1)
			if err != nil {
				_ = backup.Finish()
				return fmt.Errorf("backup step: %w", err)
			}
			if !done {
				_ = backup.Finish()
				return fmt.Errorf("backup not complete after step(-1)")
			}

			if err := backup.Finish(); err != nil {
				return fmt.Errorf("finish backup: %w", err)
			}

			return nil
		})
	})
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
