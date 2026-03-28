package benchmark

import (
	"context"
	"testing"

	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
)

func TestBenchEnvPersistPendingEmbeddings(t *testing.T) {
	ctx := context.Background()
	env, err := NewBenchEnv(ctx, NewScaleConfig("small"))
	if err != nil {
		t.Fatalf("NewBenchEnv() error = %v", err)
	}
	defer env.Close()

	_, err = env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Redis caching decision",
		Content:         "DECISION: use Redis for caching hot project data.",
		ObservationType: observation.ObservationTypeDecision,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-test",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}

	if err := env.PersistPendingEmbeddings(ctx); err != nil {
		t.Fatalf("PersistPendingEmbeddings() error = %v", err)
	}

	var embeddedCount int
	err = env.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM observations
		WHERE deleted_at IS NULL AND embedding IS NOT NULL
	`).Scan(&embeddedCount)
	if err != nil {
		t.Fatalf("count embedded observations: %v", err)
	}
	if embeddedCount != 1 {
		t.Fatalf("embeddedCount = %d, want 1", embeddedCount)
	}
}

func TestBenchEnvRecallEngineUsesFactStore(t *testing.T) {
	ctx := context.Background()
	env, err := NewBenchEnv(ctx, NewScaleConfig("small"))
	if err != nil {
		t.Fatalf("NewBenchEnv() error = %v", err)
	}
	defer env.Close()

	saved, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Opaque architecture note",
		Content:         "Stored for fact-backed recall validation.",
		ObservationType: observation.ObservationTypeDecision,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-facts",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
	})
	if err != nil {
		t.Fatalf("save observation: %v", err)
	}

	_, err = env.FactStore.Save(ctx, facts.Fact{
		Subject:       "project",
		Predicate:     "uses_database",
		Object:        "postgresql",
		ObservationID: saved.ID,
		Namespace:     "bench-facts",
	})
	if err != nil {
		t.Fatalf("save fact: %v", err)
	}

	results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
		Query:     "postgresql",
		Namespace: "bench-facts",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search() returned no results, want fact-backed observation")
	}
	if results[0].ID != saved.ID {
		t.Fatalf("top result id = %s, want %s", results[0].ID, saved.ID)
	}
	if results[0].Title != "[fact] project | uses_database | postgresql" {
		t.Fatalf("top result title = %q", results[0].Title)
	}
}
