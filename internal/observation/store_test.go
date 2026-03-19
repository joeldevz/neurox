package observation

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"neurox/internal/db"
)

func TestSaveBasicObservation(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Remember auth migration",
		Content: "**What**: migrated auth table",
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if saved.ID == "" {
		t.Fatal("Save returned empty ID")
	}
	if saved.Namespace != DefaultNamespace {
		t.Fatalf("Namespace = %q, want %q", saved.Namespace, DefaultNamespace)
	}
	if saved.ObservationType != DefaultObservationType {
		t.Fatalf("ObservationType = %q, want %q", saved.ObservationType, DefaultObservationType)
	}
	if saved.Kind != DefaultKind {
		t.Fatalf("Kind = %q, want %q", saved.Kind, DefaultKind)
	}
	if saved.Confidence != DefaultConfidence {
		t.Fatalf("Confidence = %v, want %v", saved.Confidence, DefaultConfidence)
	}
	if saved.Layer != LayerBuffer {
		t.Fatalf("Layer = %d, want %d", saved.Layer, LayerBuffer)
	}

	var ftsCount int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM observations_fts WHERE id = ?", saved.ID).Scan(&ftsCount); err != nil {
		t.Fatalf("FTS query failed: %v", err)
	}
	if ftsCount != 1 {
		t.Fatalf("ftsCount = %d, want 1", ftsCount)
	}
}

func TestSaveTopicKeyUpsert(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	first, err := store.Save(ctx, Observation{
		Title:    "Old title",
		Content:  "old content",
		TopicKey: "architecture/auth-model",
		Tags:     []string{"auth"},
	})
	if err != nil {
		t.Fatalf("first Save returned error: %v", err)
	}

	second, err := store.Save(ctx, Observation{
		Title:    "New title",
		Content:  "new content",
		TopicKey: "architecture/auth-model",
		Tags:     []string{"auth", "jwt"},
	})
	if err != nil {
		t.Fatalf("second Save returned error: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("upsert ID = %q, want %q", second.ID, first.ID)
	}
	if second.Title != "New title" {
		t.Fatalf("Title = %q, want updated title", second.Title)
	}
	if len(second.Tags) != 2 {
		t.Fatalf("Tags length = %d, want 2", len(second.Tags))
	}

	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM observations WHERE topic_key = ? AND deleted_at IS NULL", "architecture/auth-model").Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("active topic_key count = %d, want 1", count)
	}
}

func TestSaveWithFilesCreatesLinks(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Linked files",
		Content: "captures file context",
		Files:   []string{"internal/auth/service.go", "internal/auth/service.go", "internal/auth/dto.go"},
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if len(saved.Files) != 2 {
		t.Fatalf("Files length = %d, want 2", len(saved.Files))
	}

	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(1) FROM file_observations WHERE observation_id = ? AND valid_until IS NULL", saved.ID).Scan(&count); err != nil {
		t.Fatalf("file link count query failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("active file link count = %d, want 2", count)
	}
}

func TestSaveWithTagsPersistsNormalizedTags(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Tagged observation",
		Content: "stores searchable tags",
		Tags:    []string{"auth", " bugfix ", "auth"},
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if got, want := len(saved.Tags), 2; got != want {
		t.Fatalf("len(Tags) = %d, want %d", got, want)
	}

	var tags string
	if err := database.QueryRowContext(ctx, "SELECT tags FROM observations WHERE id = ?", saved.ID).Scan(&tags); err != nil {
		t.Fatalf("tags query failed: %v", err)
	}
	if tags != "auth,bugfix" {
		t.Fatalf("stored tags = %q, want auth,bugfix", tags)
	}
}

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	return NewStore(database, nil), database
}
