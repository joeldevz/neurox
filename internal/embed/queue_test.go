package embed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"neurox/internal/db"
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

func idFromInt(i int) string {
	return "OBS" + string(rune('A'+i)) + "001"
}

func titleFromInt(i int) string {
	return "Test observation " + string(rune('A'+i))
}
