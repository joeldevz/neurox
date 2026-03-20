package temporal

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"neurox/internal/db"
	"neurox/internal/observation"
)

func setupTest(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	return NewStore(database, idGen)
}

func createTestObservation(t *testing.T, s *Store, namespace string) string {
	t.Helper()
	id := s.idg.New()
	_, err := s.db.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, namespace) VALUES(?, ?, ?, ?)
	`, id, "test observation", "some content with temporal data", namespace)
	if err != nil {
		t.Fatalf("create test observation: %v", err)
	}
	return id
}

func TestSaveAndGet(t *testing.T) {
	s := setupTest(t)
	ctx := context.Background()
	obsID := createTestObservation(t, s, "test")

	anchor := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)

	saved, err := s.Save(ctx, Mention{
		ObservationID:   obsID,
		RawText:         "two weeks ago",
		Kind:            KindRelative,
		NormalizedStart: &start,
		AnchorTime:      anchor,
		Confidence:      0.9,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if saved.RawText != "two weeks ago" {
		t.Errorf("raw_text = %q, want 'two weeks ago'", saved.RawText)
	}
	if saved.Kind != KindRelative {
		t.Errorf("kind = %q, want 'relative'", saved.Kind)
	}

	got, err := s.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ObservationID != obsID {
		t.Errorf("observation_id = %q, want %q", got.ObservationID, obsID)
	}
	if got.NormalizedStart == nil {
		t.Fatal("expected normalized_start to be set")
	}
	if got.NormalizedStart.Year() != 2026 || got.NormalizedStart.Month() != 3 || got.NormalizedStart.Day() != 6 {
		t.Errorf("normalized_start = %v, want 2026-03-06", got.NormalizedStart)
	}
}

func TestSaveAllAndByObservation(t *testing.T) {
	s := setupTest(t)
	ctx := context.Background()
	obsID := createTestObservation(t, s, "test")

	anchor := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	start1 := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)

	results := []ParseResult{
		{RawText: "two weeks ago", Kind: KindRelative, NormalizedStart: &start1, Confidence: 0.9},
		{RawText: "currently", Kind: KindCurrentState, Confidence: 0.95},
	}

	mentions, err := s.SaveAll(ctx, obsID, results, anchor)
	if err != nil {
		t.Fatalf("save all: %v", err)
	}
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions, got %d", len(mentions))
	}

	byObs, err := s.ByObservation(ctx, obsID)
	if err != nil {
		t.Fatalf("by observation: %v", err)
	}
	if len(byObs) != 2 {
		t.Errorf("expected 2 mentions by observation, got %d", len(byObs))
	}
}

func TestSaveValidation(t *testing.T) {
	s := setupTest(t)
	ctx := context.Background()

	_, err := s.Save(ctx, Mention{RawText: "yesterday", Kind: KindRelative})
	if err == nil {
		t.Error("expected error for missing observation_id")
	}

	_, err = s.Save(ctx, Mention{ObservationID: "abc", Kind: KindRelative})
	if err == nil {
		t.Error("expected error for missing raw_text")
	}

	_, err = s.Save(ctx, Mention{ObservationID: "abc", RawText: "x", Kind: "invalid"})
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}

func TestMentionKindValidate(t *testing.T) {
	valid := []MentionKind{KindAbsolute, KindRelative, KindCurrentState, KindDuration, KindRecurring}
	for _, k := range valid {
		if err := k.Validate(); err != nil {
			t.Errorf("expected %q to be valid, got: %v", k, err)
		}
	}
	if err := MentionKind("bogus").Validate(); err == nil {
		t.Error("expected 'bogus' to be invalid")
	}
}
