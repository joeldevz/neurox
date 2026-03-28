package consolidate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
)

// newBatchTestDB creates a fresh in-memory SQLite test database with the
// observations schema, suitable for testing batchExecByIDs.
func newBatchTestDB(t *testing.T) *db.TestDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "batch_test.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &db.TestDB{DB: database}
}

// insertTestObservations inserts n observations with IDs "OBS_0000", "OBS_0001", etc.
func insertTestObservations(t *testing.T, tdb *db.TestDB, n int) []string {
	t.Helper()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("OBS_%04d", i)
		ids[i] = id
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')`, id)
	}
	return ids
}

// countDeleted counts observations that have deleted_at set.
func countDeleted(t *testing.T, tdb *db.TestDB) int {
	t.Helper()
	var count int
	err := tdb.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM observations WHERE deleted_at IS NOT NULL").Scan(&count)
	if err != nil {
		t.Fatalf("count deleted: %v", err)
	}
	return count
}

// countByLayer counts observations in the given layer that are not deleted.
func countByLayer(t *testing.T, tdb *db.TestDB, layer int) int {
	t.Helper()
	var count int
	err := tdb.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM observations WHERE layer = ? AND deleted_at IS NULL", layer).Scan(&count)
	if err != nil {
		t.Fatalf("count by layer: %v", err)
	}
	return count
}

func TestBatchExecByIDs(t *testing.T) {
	const softDeleteTemplate = "UPDATE observations SET deleted_at = datetime('now') WHERE id IN (%s)"
	const promoteTemplate = "UPDATE observations SET layer = 1, updated_at = datetime('now') WHERE id IN (%s)"

	tests := []struct {
		name         string
		totalIDs     int
		chunkSize    int
		wantAffected int64
		wantChunks   int // informational — not directly verified, but implied by correctness
		description  string
	}{
		{
			name:         "zero IDs",
			totalIDs:     0,
			chunkSize:    500,
			wantAffected: 0,
			wantChunks:   0,
			description:  "empty ID list returns 0 affected, nil error",
		},
		{
			name:         "single ID",
			totalIDs:     1,
			chunkSize:    500,
			wantAffected: 1,
			wantChunks:   1,
			description:  "one ID works correctly",
		},
		{
			name:         "under chunk boundary (499)",
			totalIDs:     499,
			chunkSize:    500,
			wantAffected: 499,
			wantChunks:   1,
			description:  "499 IDs fit in a single chunk of 500",
		},
		{
			name:         "exact chunk boundary (500)",
			totalIDs:     500,
			chunkSize:    500,
			wantAffected: 500,
			wantChunks:   1,
			description:  "exactly 500 IDs fit in a single chunk",
		},
		{
			name:         "overflow (1001 IDs, chunk 500)",
			totalIDs:     1001,
			chunkSize:    500,
			wantAffected: 1001,
			wantChunks:   3,
			description:  "1001 IDs split into 3 chunks: 500 + 500 + 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := newBatchTestDB(t)
			ids := insertTestObservations(t, tdb, tt.totalIDs)

			affected, err := batchExecByIDs(
				context.Background(),
				tdb.DB,
				softDeleteTemplate,
				ids,
				tt.chunkSize,
			)
			if err != nil {
				t.Fatalf("batchExecByIDs error: %v", err)
			}
			if affected != tt.wantAffected {
				t.Errorf("affected = %d, want %d", affected, tt.wantAffected)
			}

			// Verify actual DB state: all targeted rows should be soft-deleted
			if tt.totalIDs > 0 {
				deleted := countDeleted(t, tdb)
				if deleted != tt.totalIDs {
					t.Errorf("deleted rows = %d, want %d", deleted, tt.totalIDs)
				}
			}
		})
	}

	// Additional test: verify a promotion UPDATE (layer change) works correctly
	t.Run("promote layer update", func(t *testing.T) {
		tdb := newBatchTestDB(t)
		ids := insertTestObservations(t, tdb, 10)

		// Promote only the first 5
		affected, err := batchExecByIDs(
			context.Background(),
			tdb.DB,
			promoteTemplate,
			ids[:5],
			defaultChunkSize,
		)
		if err != nil {
			t.Fatalf("batchExecByIDs error: %v", err)
		}
		if affected != 5 {
			t.Errorf("affected = %d, want 5", affected)
		}

		// Verify: 5 in layer 1, 5 in layer 0
		inLayer0 := countByLayer(t, tdb, 0)
		inLayer1 := countByLayer(t, tdb, 1)
		if inLayer0 != 5 {
			t.Errorf("layer 0 count = %d, want 5", inLayer0)
		}
		if inLayer1 != 5 {
			t.Errorf("layer 1 count = %d, want 5", inLayer1)
		}
	})

	// Test: zero chunk size falls back to default
	t.Run("zero chunk size uses default", func(t *testing.T) {
		tdb := newBatchTestDB(t)
		ids := insertTestObservations(t, tdb, 3)

		affected, err := batchExecByIDs(
			context.Background(),
			tdb.DB,
			softDeleteTemplate,
			ids,
			0, // zero chunk size
		)
		if err != nil {
			t.Fatalf("batchExecByIDs error: %v", err)
		}
		if affected != 3 {
			t.Errorf("affected = %d, want 3", affected)
		}

		deleted := countDeleted(t, tdb)
		if deleted != 3 {
			t.Errorf("deleted rows = %d, want 3", deleted)
		}
	})

	// Test: negative chunk size falls back to default
	t.Run("negative chunk size uses default", func(t *testing.T) {
		tdb := newBatchTestDB(t)
		ids := insertTestObservations(t, tdb, 2)

		affected, err := batchExecByIDs(
			context.Background(),
			tdb.DB,
			softDeleteTemplate,
			ids,
			-1,
		)
		if err != nil {
			t.Fatalf("batchExecByIDs error: %v", err)
		}
		if affected != 2 {
			t.Errorf("affected = %d, want 2", affected)
		}
	})

	// Test: IDs that don't exist in DB → 0 affected, nil error
	t.Run("non-existent IDs", func(t *testing.T) {
		tdb := newBatchTestDB(t)
		// Don't insert anything, just try to delete non-existent IDs
		fakeIDs := []string{"FAKE_001", "FAKE_002", "FAKE_003"}

		affected, err := batchExecByIDs(
			context.Background(),
			tdb.DB,
			softDeleteTemplate,
			fakeIDs,
			defaultChunkSize,
		)
		if err != nil {
			t.Fatalf("batchExecByIDs error: %v", err)
		}
		if affected != 0 {
			t.Errorf("affected = %d, want 0 for non-existent IDs", affected)
		}
	})
}

func TestDefaultChunkSize(t *testing.T) {
	if defaultChunkSize != 500 {
		t.Errorf("defaultChunkSize = %d, want 500", defaultChunkSize)
	}
}
