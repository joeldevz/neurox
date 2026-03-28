package consolidate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// defaultChunkSize is the maximum number of IDs per batch SQL call.
// Kept conservative (500) to stay well within SQLite's parameter limit
// (999 in some builds, 32766 in others).
const defaultChunkSize = 500

// batchExecByIDs executes a SQL statement template against groups of IDs in chunks.
// The queryTemplate must contain exactly one %s placeholder that will be replaced
// with the comma-separated ? placeholders for the chunk of IDs.
// Example template: "UPDATE observations SET deleted_at = datetime('now') WHERE id IN (%s)"
// Returns total rows affected across all chunks.
func batchExecByIDs(ctx context.Context, db *sql.DB, queryTemplate string, ids []string, chunkSize int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	var totalAffected int64

	for start := 0; start < len(ids); start += chunkSize {
		end := start + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]

		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = placeholders[:len(placeholders)-1] // trim trailing comma

		query := fmt.Sprintf(queryTemplate, placeholders)

		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}

		result, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalAffected, fmt.Errorf("batch exec chunk [%d:%d]: %w", start, end, err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return totalAffected, fmt.Errorf("rows affected chunk [%d:%d]: %w", start, end, err)
		}
		totalAffected += affected
	}

	return totalAffected, nil
}
