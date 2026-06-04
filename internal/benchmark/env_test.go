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

// TestBenchEnvHonorsDisableBackfillEnv verifies the wiring that makes
// G6 (stretch gate) work end-to-end: the env var NEUROX_RECALL_DISABLE_BACKFILL
// is parsed and propagated to the recall engine via WithDisableBackfill.
func TestBenchEnvHonorsDisableBackfillEnv(t *testing.T) {
	ctx := context.Background()

	t.Run("unset defaults to false", func(t *testing.T) {
		t.Setenv("NEUROX_RECALL_DISABLE_BACKFILL", "")
		env, err := NewBenchEnv(ctx, NewScaleConfig("small"))
		if err != nil {
			t.Fatalf("NewBenchEnv() error = %v", err)
		}
		defer env.Close()
		if env.RecallEngine.DisableBackfill() {
			t.Error("DisableBackfill = true with unset env, want false (default)")
		}
		if env.RecallEngine.RRFK() != 60 {
			t.Errorf("RRFK = %d, want 60 (default)", env.RecallEngine.RRFK())
		}
	})

	t.Run("true propagates to engine", func(t *testing.T) {
		t.Setenv("NEUROX_RECALL_DISABLE_BACKFILL", "true")
		env, err := NewBenchEnv(ctx, NewScaleConfig("small"))
		if err != nil {
			t.Fatalf("NewBenchEnv() error = %v", err)
		}
		defer env.Close()
		if !env.RecallEngine.DisableBackfill() {
			t.Error("DisableBackfill = false with env=true, want true")
		}
	})

	t.Run("RRF k override", func(t *testing.T) {
		t.Setenv("NEUROX_RECALL_DISABLE_BACKFILL", "")
		t.Setenv("NEUROX_RECALL_RRF_K", "30")
		env, err := NewBenchEnv(ctx, NewScaleConfig("small"))
		if err != nil {
			t.Fatalf("NewBenchEnv() error = %v", err)
		}
		defer env.Close()
		if env.RecallEngine.RRFK() != 30 {
			t.Errorf("RRFK = %d, want 30 (env override)", env.RecallEngine.RRFK())
		}
	})
}
