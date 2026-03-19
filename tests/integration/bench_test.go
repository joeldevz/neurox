package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"neurox/internal/db"
	"neurox/internal/observation"
	"neurox/internal/recall"
)

func BenchmarkSave(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	defer database.Close()

	store := observation.NewStore(database, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.Save(ctx, observation.Observation{
			Title:     fmt.Sprintf("Bench observation %d", i),
			Content:   fmt.Sprintf("Content for benchmark observation number %d with some text", i),
			Namespace: "bench",
		})
		if err != nil {
			b.Fatalf("save: %v", err)
		}
	}
}

func BenchmarkRecallFTS(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	defer database.Close()

	store := observation.NewStore(database, nil)
	ctx := context.Background()

	// Seed with 1000 observations
	for i := 0; i < 1000; i++ {
		store.Save(ctx, observation.Observation{
			Title:     fmt.Sprintf("Observation about topic %d category %d", i%50, i%10),
			Content:   fmt.Sprintf("Detailed content about topic %d in category %d with keywords alpha beta gamma", i%50, i%10),
			Namespace: "bench",
		})
	}

	engine := recall.NewEngine(database)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Search(ctx, recall.SearchOptions{
			Query:     "topic alpha",
			Namespace: "bench",
			Limit:     10,
		})
		if err != nil {
			b.Fatalf("recall: %v", err)
		}
	}
}
