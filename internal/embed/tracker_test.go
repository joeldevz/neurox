package embed

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
)

// namedMockProvider is a mock provider with a configurable name for testing
type namedMockProvider struct {
	dims int
	name string
}

func (m *namedMockProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec, nil
}

func (m *namedMockProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		vec := make([]float32, m.dims)
		for j := range vec {
			vec[j] = 0.1
		}
		result[i] = vec
	}
	return result, nil
}

func (m *namedMockProvider) Dimensions() int { return m.dims }
func (m *namedMockProvider) Name() string    { return m.name }

func TestModelTrackerFirstRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	provider := &namedMockProvider{dims: 384, name: "test/model1"}
	queue := NewQueue(provider, database)
	ctx := context.Background()
	queue.Start(ctx)
	defer queue.Stop()

	tracker := NewModelTracker(database, queue)

	// First run: should store model without re-embedding
	err = tracker.CheckAndMigrate(ctx, provider)
	if err != nil {
		t.Fatalf("CheckAndMigrate first run: %v", err)
	}

	// Verify model was stored
	var storedModel string
	err = database.QueryRowContext(ctx, "SELECT value FROM db_settings WHERE key = 'embed_model'").Scan(&storedModel)
	if err != nil {
		t.Fatalf("query stored model: %v", err)
	}
	if storedModel != provider.Name() {
		t.Errorf("stored model = %q, want %q", storedModel, provider.Name())
	}

	// Verify dimensions were stored
	var storedDims string
	err = database.QueryRowContext(ctx, "SELECT value FROM db_settings WHERE key = 'embed_dims'").Scan(&storedDims)
	if err != nil {
		t.Fatalf("query stored dims: %v", err)
	}
	if storedDims != "384" {
		t.Errorf("stored dims = %q, want 384", storedDims)
	}
}

func TestModelTrackerNoChange(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	provider := &namedMockProvider{dims: 384, name: "test/model1"}
	queue := NewQueue(provider, database)
	ctx := context.Background()
	queue.Start(ctx)
	defer queue.Stop()

	tracker := NewModelTracker(database, queue)

	// First run
	if err := tracker.CheckAndMigrate(ctx, provider); err != nil {
		t.Fatalf("CheckAndMigrate first run: %v", err)
	}

	// Insert an observation
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS1', 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)

	// Set an embedding for the observation
	database.ExecContext(ctx, `
		UPDATE observations SET embedding = X'00000000' WHERE id = 'OBS1'
	`)

	// Second run with same model: should not trigger re-embed
	if err := tracker.CheckAndMigrate(ctx, provider); err != nil {
		t.Fatalf("CheckAndMigrate second run: %v", err)
	}

	// Verify embedding was NOT cleared (no re-embed happened)
	var embedding []byte
	err = database.QueryRowContext(ctx, "SELECT embedding FROM observations WHERE id = 'OBS1'").Scan(&embedding)
	if err != nil {
		t.Fatalf("get embedding: %v", err)
	}
	if embedding == nil {
		t.Error("embedding was cleared, but no re-embed should have happened")
	}
}

func TestModelTrackerChanged(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	provider1 := &namedMockProvider{dims: 384, name: "test/model1"}
	provider2 := &namedMockProvider{dims: 768, name: "test/model2"}

	queue := NewQueue(provider1, database)
	ctx := context.Background()
	queue.Start(ctx)
	defer queue.Stop()

	tracker := NewModelTracker(database, queue)

	// First run with provider1
	if err := tracker.CheckAndMigrate(ctx, provider1); err != nil {
		t.Fatalf("CheckAndMigrate first run: %v", err)
	}

	// Insert an observation with embedding
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS1', 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`)
	database.ExecContext(ctx, `
		UPDATE observations SET embedding = X'00000000' WHERE id = 'OBS1'
	`)

	// Change model: should trigger background re-embed
	if err := tracker.CheckAndMigrate(ctx, provider2); err != nil {
		t.Fatalf("CheckAndMigrate with changed model: %v", err)
	}

	// Wait for background goroutine to complete
	time.Sleep(500 * time.Millisecond)

	// Wait for re-embed to complete (embedding should be NULL then re-set)
	time.Sleep(1 * time.Second)

	// Verify db_settings was updated with new model
	var storedModel string
	err = database.QueryRowContext(ctx, "SELECT value FROM db_settings WHERE key = 'embed_model'").Scan(&storedModel)
	if err != nil {
		t.Fatalf("query stored model: %v", err)
	}
	if storedModel != provider2.Name() {
		t.Errorf("stored model = %q, want %q", storedModel, provider2.Name())
	}
}
