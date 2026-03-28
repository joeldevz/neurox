package observation

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
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
	if saved.Importance != DefaultImportance {
		t.Fatalf("Importance = %v, want %v", saved.Importance, DefaultImportance)
	}
	if saved.ActivationLevel != DefaultActivationLevel {
		t.Fatalf("ActivationLevel = %v, want %v", saved.ActivationLevel, DefaultActivationLevel)
	}
	if saved.ConsolidationStrength != DefaultConsolidationStrength {
		t.Fatalf("ConsolidationStrength = %v, want %v", saved.ConsolidationStrength, DefaultConsolidationStrength)
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

func TestSaveExtractsTemporalMentions(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	var extractCalls []string
	store.SetTemporalExtractor(&mockTemporalExtractor{calls: &extractCalls})

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Migration note",
		Content: "We migrated to SQLite yesterday",
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if len(extractCalls) != 1 {
		t.Fatalf("extract calls = %d, want 1", len(extractCalls))
	}
	if extractCalls[0] != saved.ID {
		t.Fatalf("extracted observation ID = %q, want %q", extractCalls[0], saved.ID)
	}
}

func TestSaveWithoutExtractorStillWorks(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	_, err := store.Save(ctx, Observation{
		Title:   "No extractor",
		Content: "This should still work yesterday",
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
}

func TestSaveExtractorFailureDoesNotBlockSave(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	store.SetTemporalExtractor(&failingExtractor{})

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Failing extractor",
		Content: "yesterday we did something",
	})
	if err != nil {
		t.Fatalf("Save returned error despite extractor failure: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected observation to be saved")
	}
}

type mockTemporalExtractor struct {
	calls *[]string
}

func (m *mockTemporalExtractor) Extract(_ context.Context, obsID, _ string) (int, error) {
	*m.calls = append(*m.calls, obsID)
	return 1, nil
}

type failingExtractor struct{}

func (f *failingExtractor) Extract(_ context.Context, _, _ string) (int, error) {
	return 0, fmt.Errorf("simulated extraction failure")
}

func TestRetentionRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		input     Retention
		wantSaved Retention
	}{
		{
			name:      "default retention is durable",
			input:     "",
			wantSaved: RetentionDurable,
		},
		{
			name:      "explicit durable",
			input:     RetentionDurable,
			wantSaved: RetentionDurable,
		},
		{
			name:      "explicit operational",
			input:     RetentionOperational,
			wantSaved: RetentionOperational,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, database := newTestStore(t)
			defer database.Close()

			ctx := context.Background()
			saved, err := store.Save(ctx, Observation{
				Title:     "Retention test: " + tc.name,
				Content:   "Testing retention round-trip",
				Retention: tc.input,
			})
			if err != nil {
				t.Fatalf("Save returned error: %v", err)
			}
			if saved.Retention != tc.wantSaved {
				t.Fatalf("saved.Retention = %q, want %q", saved.Retention, tc.wantSaved)
			}

			got, err := store.Get(ctx, saved.ID)
			if err != nil {
				t.Fatalf("Get returned error: %v", err)
			}
			if got.Retention != tc.wantSaved {
				t.Fatalf("Get().Retention = %q, want %q", got.Retention, tc.wantSaved)
			}
		})
	}
}

func TestRetentionUpdateRoundTrip(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:     "Update retention test",
		Content:   "Initially durable",
		Retention: RetentionDurable,
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if saved.Retention != RetentionDurable {
		t.Fatalf("initial Retention = %q, want %q", saved.Retention, RetentionDurable)
	}

	saved.Retention = RetentionOperational
	updated, err := store.Update(ctx, saved)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Retention != RetentionOperational {
		t.Fatalf("updated.Retention = %q, want %q", updated.Retention, RetentionOperational)
	}

	got, err := store.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Retention != RetentionOperational {
		t.Fatalf("Get().Retention = %q, want %q", got.Retention, RetentionOperational)
	}
}

func TestRetentionValidation(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	_, err := store.Save(ctx, Observation{
		Title:     "Invalid retention",
		Content:   "Should fail validation",
		Retention: "invalid_value",
	})
	if err == nil {
		t.Fatal("expected error for invalid retention, got nil")
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

func TestActivationSignalsRoundTrip(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:                 "Test activation signals",
		Content:               "Testing activation_level and consolidation_strength persistence",
		Importance:            0.8,
		ActivationLevel:       0.75,
		ConsolidationStrength: 0.6,
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if saved.Importance != 0.8 {
		t.Fatalf("Importance = %v, want 0.8", saved.Importance)
	}
	if saved.ActivationLevel != 0.75 {
		t.Fatalf("ActivationLevel = %v, want 0.75", saved.ActivationLevel)
	}
	if saved.ConsolidationStrength != 0.6 {
		t.Fatalf("ConsolidationStrength = %v, want 0.6", saved.ConsolidationStrength)
	}

	// Verify via Get
	got, err := store.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Importance != 0.8 {
		t.Fatalf("Get().Importance = %v, want 0.8", got.Importance)
	}
	if got.ActivationLevel != 0.75 {
		t.Fatalf("Get().ActivationLevel = %v, want 0.75", got.ActivationLevel)
	}
	if got.ConsolidationStrength != 0.6 {
		t.Fatalf("Get().ConsolidationStrength = %v, want 0.6", got.ConsolidationStrength)
	}
}

func TestActivationSignalsUpdate(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:                 "Update activation test",
		Content:               "Initial values",
		Importance:            0.5,
		ActivationLevel:       0.5,
		ConsolidationStrength: 0.0,
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	// Update activation signals
	saved.Importance = 0.9
	saved.ActivationLevel = 0.85
	saved.ConsolidationStrength = 0.7

	updated, err := store.Update(ctx, saved)
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	if updated.Importance != 0.9 {
		t.Fatalf("updated.Importance = %v, want 0.9", updated.Importance)
	}
	if updated.ActivationLevel != 0.85 {
		t.Fatalf("updated.ActivationLevel = %v, want 0.85", updated.ActivationLevel)
	}
	if updated.ConsolidationStrength != 0.7 {
		t.Fatalf("updated.ConsolidationStrength = %v, want 0.7", updated.ConsolidationStrength)
	}

	// Verify via Get
	got, err := store.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Importance != 0.9 {
		t.Fatalf("Get().Importance = %v, want 0.9", got.Importance)
	}
	if got.ActivationLevel != 0.85 {
		t.Fatalf("Get().ActivationLevel = %v, want 0.85", got.ActivationLevel)
	}
	if got.ConsolidationStrength != 0.7 {
		t.Fatalf("Get().ConsolidationStrength = %v, want 0.7", got.ConsolidationStrength)
	}
}

func TestActivationSignalsDefaults(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	saved, err := store.Save(ctx, Observation{
		Title:   "Default activation test",
		Content: "Testing default values for activation signals",
		// Not setting Importance, ActivationLevel, or ConsolidationStrength
	})
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	if saved.Importance != DefaultImportance {
		t.Fatalf("Importance = %v, want default %v", saved.Importance, DefaultImportance)
	}
	if saved.ActivationLevel != DefaultActivationLevel {
		t.Fatalf("ActivationLevel = %v, want default %v", saved.ActivationLevel, DefaultActivationLevel)
	}
	if saved.ConsolidationStrength != DefaultConsolidationStrength {
		t.Fatalf("ConsolidationStrength = %v, want default %v", saved.ConsolidationStrength, DefaultConsolidationStrength)
	}
}
