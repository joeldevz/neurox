package embed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
)

func TestQueueProcessesBatch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// Insert test observations
	for i := 0; i < 3; i++ {
		database.ExecContext(context.Background(), `
			INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, ?, 'test content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
		`, idFromInt(i), titleFromInt(i))
	}

	provider := &MockProvider{dims: 384}
	q := NewQueue(provider, database)

	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx)

	// Enqueue observations
	for i := 0; i < 3; i++ {
		q.Enqueue(idFromInt(i))
	}

	// Wait for processing
	time.Sleep(800 * time.Millisecond)
	cancel()
	q.Stop()

	// Verify embeddings were stored
	for i := 0; i < 3; i++ {
		var embedding []byte
		err := database.QueryRowContext(context.Background(),
			"SELECT embedding FROM observations WHERE id = ?", idFromInt(i)).Scan(&embedding)
		if err != nil {
			t.Fatalf("get embedding %d: %v", i, err)
		}
		if embedding == nil {
			t.Errorf("observation %d has no embedding", i)
			continue
		}
		vec := DeserializeF32(embedding)
		if len(vec) != 384 {
			t.Errorf("observation %d embedding dims = %d, want 384", i, len(vec))
		}
	}
}

func TestQueueGracefulStop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS001', 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)

	provider := &MockProvider{dims: 384}
	q := NewQueue(provider, database)

	ctx := context.Background()
	q.Start(ctx)
	q.Enqueue("OBS001")

	// Stop should flush remaining
	time.Sleep(100 * time.Millisecond)
	q.Stop()

	var embedding []byte
	database.QueryRowContext(context.Background(),
		"SELECT embedding FROM observations WHERE id = 'OBS001'").Scan(&embedding)
	if embedding == nil {
		t.Error("expected embedding after graceful stop")
	}
}

func TestBackfillPendingEnqueuesObservationsWithoutEmbedding(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// Insert 3 observations: 1 with embedding, 2 without
	database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_WITH', 'has embed', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)
	database.ExecContext(context.Background(), `
		UPDATE observations SET embedding = X'00000000' WHERE id = 'OBS_WITH'
	`)
	database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_NO1', 'no embed 1', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)
	database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_NO2', 'no embed 2', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)

	provider := &MockProvider{dims: 384}
	q := NewQueue(provider, database)
	ctx := context.Background()
	q.Start(ctx)

	q.BackfillPending(ctx)

	time.Sleep(800 * time.Millisecond)
	q.Stop()

	// Verify: OBS_NO1 and OBS_NO2 should now have embeddings
	for _, id := range []string{"OBS_NO1", "OBS_NO2"} {
		var embedding []byte
		database.QueryRowContext(ctx, "SELECT embedding FROM observations WHERE id = ?", id).Scan(&embedding)
		if embedding == nil {
			t.Errorf("%s should have embedding after backfill", id)
		}
	}

	// Verify: OBS_WITH embedding should remain (not re-queued by backfill)
	var withEmbedding []byte
	database.QueryRowContext(ctx, "SELECT embedding FROM observations WHERE id = 'OBS_WITH'").Scan(&withEmbedding)
	if withEmbedding == nil {
		t.Error("OBS_WITH should still have its embedding")
	}
}

func idFromInt(i int) string {
	return "OBS" + string(rune('A'+i)) + "001"
}

func titleFromInt(i int) string {
	return "Test observation " + string(rune('A'+i))
}
