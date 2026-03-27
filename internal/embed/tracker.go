package embed

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"
)

// ModelTracker tracks the embedding model and triggers re-embedding when it changes.
type ModelTracker struct {
	db    *sql.DB
	queue *Queue
}

// NewModelTracker creates a new model tracker.
func NewModelTracker(db *sql.DB, queue *Queue) *ModelTracker {
	return &ModelTracker{
		db:    db,
		queue: queue,
	}
}

// CheckAndMigrate checks if the embedding model has changed and triggers re-embedding.
// It stores the model name and dimensions in db_settings.
// If the model changed, it starts a background goroutine to re-embed all observations.
func (t *ModelTracker) CheckAndMigrate(ctx context.Context, provider Provider) error {
	currentModel := provider.Name()
	currentDims := strconv.Itoa(provider.Dimensions())

	// Query stored model
	var storedModel, storedDims string
	err := t.db.QueryRowContext(ctx, `
		SELECT value FROM db_settings WHERE key = 'embed_model'
	`).Scan(&storedModel)

	if err == sql.ErrNoRows {
		// First run: store current model and return
		if err := t.storeModel(ctx, currentModel, currentDims); err != nil {
			return fmt.Errorf("store initial model: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("query stored model: %w", err)
	}

	// Get stored dimensions (optional, for observability)
	_ = t.db.QueryRowContext(ctx, `
		SELECT value FROM db_settings WHERE key = 'embed_dims'
	`).Scan(&storedDims)

	// Check if model changed
	if storedModel == currentModel {
		// No change
		return nil
	}

	// Model changed: log and trigger re-embedding
	log.Printf("embed model changed %s→%s, triggering re-embed", storedModel, currentModel)

	// Start background re-embedding
	go func() {
		// Use a background context with timeout for the re-embedding
		reembedCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		if err := t.queue.ReembedAll(reembedCtx); err != nil {
			log.Printf("re-embed failed: %v", err)
			return
		}

		// Update stored model after successful re-embedding
		if err := t.storeModel(reembedCtx, currentModel, currentDims); err != nil {
			log.Printf("update stored model after re-embed: %v", err)
		}
		log.Printf("re-embed completed, updated model to %s", currentModel)
	}()

	return nil
}

func (t *ModelTracker) storeModel(ctx context.Context, model, dims string) error {
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO db_settings(key, value, updated_at)
		VALUES('embed_model', ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, model)
	if err != nil {
		return fmt.Errorf("store embed_model: %w", err)
	}

	_, err = t.db.ExecContext(ctx, `
		INSERT INTO db_settings(key, value, updated_at)
		VALUES('embed_dims', ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, dims)
	if err != nil {
		return fmt.Errorf("store embed_dims: %w", err)
	}

	return nil
}
