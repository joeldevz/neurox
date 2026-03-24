package observation

import (
	"context"
	"database/sql"
	"testing"

	"github.com/joeldevz/neurox/internal/links"
)

func TestInvalidateWithoutReplacement(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	linkStore := links.NewStore(database, newULIDGenerator())

	saved, err := store.Save(ctx, Observation{
		Title:   "Old auth approach",
		Content: "Use session tokens",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	result, err := store.Invalidate(ctx, linkStore, InvalidateInput{
		ObservationID: saved.ID,
		Reason:        "Migrated to JWT",
	})
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	if result.InvalidatedID != saved.ID {
		t.Errorf("invalidated_id = %q, want %q", result.InvalidatedID, saved.ID)
	}
	if result.ReplacementID != "" {
		t.Errorf("replacement_id should be empty, got %q", result.ReplacementID)
	}

	// Verify staleness and confidence
	updated, err := store.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var staleness string
	database.QueryRowContext(ctx, "SELECT staleness FROM observations WHERE id = ?", saved.ID).Scan(&staleness)
	if staleness != "stale" {
		t.Errorf("staleness = %q, want stale", staleness)
	}

	expectedConfidence := DefaultConfidence * 0.5
	if updated.Confidence < expectedConfidence-0.01 || updated.Confidence > expectedConfidence+0.01 {
		t.Errorf("confidence = %f, want ~%f", updated.Confidence, expectedConfidence)
	}
}

func TestInvalidateWithReplacement(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	linkStore := links.NewStore(database, newULIDGenerator())

	original, err := store.Save(ctx, Observation{
		Title:           "DB is Postgres 14",
		Content:         "Production database runs Postgres 14",
		ObservationType: ObservationTypeDecision,
		Kind:            KindSemantic,
		Namespace:       "myproject",
	})
	if err != nil {
		t.Fatalf("save original: %v", err)
	}

	result, err := store.Invalidate(ctx, linkStore, InvalidateInput{
		ObservationID:      original.ID,
		Reason:             "Upgraded database",
		ReplacementTitle:   "DB is Postgres 16",
		ReplacementContent: "Production database upgraded to Postgres 16",
	})
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	if result.ReplacementID == "" {
		t.Fatal("expected replacement_id to be set")
	}
	if result.LinkID == "" {
		t.Fatal("expected link_id to be set")
	}

	// Verify replacement was created with inherited properties
	replacement, err := store.Get(ctx, result.ReplacementID)
	if err != nil {
		t.Fatalf("get replacement: %v", err)
	}
	if replacement.Title != "DB is Postgres 16" {
		t.Errorf("replacement title = %q", replacement.Title)
	}
	if replacement.ObservationType != ObservationTypeDecision {
		t.Errorf("replacement type = %q, want decision", replacement.ObservationType)
	}
	if replacement.Namespace != "myproject" {
		t.Errorf("replacement namespace = %q, want myproject", replacement.Namespace)
	}

	// Verify supersedes link exists
	supersedesLinks, err := linkStore.GetBySource(ctx, result.ReplacementID, links.RelationSupersedes)
	if err != nil {
		t.Fatalf("get supersedes links: %v", err)
	}
	if len(supersedesLinks) != 1 {
		t.Fatalf("expected 1 supersedes link, got %d", len(supersedesLinks))
	}
	if supersedesLinks[0].TargetID != original.ID {
		t.Errorf("supersedes target = %q, want %q", supersedesLinks[0].TargetID, original.ID)
	}

	// Verify original has valid_until and invalidated_by set
	var validUntil sql.NullString
	var invalidatedBy sql.NullString
	database.QueryRowContext(ctx, "SELECT valid_until, invalidated_by FROM observations WHERE id = ?", original.ID).Scan(&validUntil, &invalidatedBy)
	if !validUntil.Valid {
		t.Error("expected valid_until to be set")
	}
	if !invalidatedBy.Valid || invalidatedBy.String != result.ReplacementID {
		t.Errorf("invalidated_by = %q, want %q", invalidatedBy.String, result.ReplacementID)
	}
}

func TestInvalidateNonExistentObservation(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	linkStore := links.NewStore(database, newULIDGenerator())

	_, err := store.Invalidate(ctx, linkStore, InvalidateInput{
		ObservationID: "NONEXISTENT",
		Reason:        "test",
	})
	if err == nil {
		t.Fatal("expected error for non-existent observation")
	}
}

func TestInvalidateValidation(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	linkStore := links.NewStore(database, newULIDGenerator())

	tests := []struct {
		name  string
		input InvalidateInput
	}{
		{"empty observation_id", InvalidateInput{Reason: "test"}},
		{"empty reason", InvalidateInput{ObservationID: "OBS001"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Invalidate(ctx, linkStore, tt.input)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
