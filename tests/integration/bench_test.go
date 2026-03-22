package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	bench "neurox/internal/benchmark"
	"neurox/internal/consolidate"
	"neurox/internal/db"
	"neurox/internal/decay"
	"neurox/internal/embed"
	"neurox/internal/facts"
	"neurox/internal/links"
	"neurox/internal/llm"
	"neurox/internal/observation"
	"neurox/internal/recall"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func openBenchDB(b *testing.B) *db.TestDB {
	b.Helper()
	dbPath := filepath.Join(b.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	b.Cleanup(func() { database.Close() })
	return &db.TestDB{DB: database}
}

// benchTopic returns a rotating technical topic label used to vary content.
func benchTopic(i int) string {
	topics := []string{
		"authentication", "database", "caching", "deployment", "monitoring",
		"security", "performance", "testing", "API design", "TypeScript",
	}
	return topics[i%len(topics)]
}

// ─── Standard Go Benchmarks ──────────────────────────────────────────────────

// BenchmarkSave measures raw write throughput for a single observation.
func BenchmarkSave(b *testing.B) {
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)
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

// BenchmarkSave_10K measures write throughput when saving 10 000 observations
// in a single outer iteration. This gives a realistic picture of sustained
// write performance under FTS5 index pressure.
func BenchmarkSave_10K(b *testing.B) {
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10_000; j++ {
			_, err := store.Save(ctx, observation.Observation{
				Title:           fmt.Sprintf("Batch obs %d-%d: %s", i, j, benchTopic(j)),
				Content:         fmt.Sprintf("Detailed content for obs %d in batch %d about %s", j, i, benchTopic(j)),
				ObservationType: observation.ObservationTypeDecision,
				Kind:            observation.KindSemantic,
				Namespace:       "bench-10k",
				Retention:       observation.RetentionDurable,
			})
			if err != nil {
				b.Fatalf("save %d: %v", j, err)
			}
		}
	}
}

// BenchmarkRecallFTS measures FTS5 query throughput with 1 000 pre-seeded
// observations.
func BenchmarkRecallFTS(b *testing.B) {
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)
	ctx := context.Background()

	// Seed with 1000 observations.
	for i := 0; i < 1000; i++ {
		_, _ = store.Save(ctx, observation.Observation{
			Title:     fmt.Sprintf("Observation about topic %d category %d", i%50, i%10),
			Content:   fmt.Sprintf("Detailed content about topic %d in category %d with keywords alpha beta gamma", i%50, i%10),
			Namespace: "bench",
		})
	}

	engine := recall.NewEngine(tdb.DB)

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

// BenchmarkRecallFTS_50K measures FTS5 recall performance under a large dataset
// (50 000 seeded observations). This exercises FTS5 index lookup at realistic
// production scale.
func BenchmarkRecallFTS_50K(b *testing.B) {
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)
	ctx := context.Background()

	b.Log("seeding 50 000 observations (this takes a moment)...")
	for i := 0; i < 50_000; i++ {
		_, _ = store.Save(ctx, observation.Observation{
			Title:     fmt.Sprintf("Scale obs %d: %s", i, benchTopic(i)),
			Content:   fmt.Sprintf("Scale content %d about %s with keywords alpha beta gamma delta epsilon", i, benchTopic(i)),
			Namespace: "bench-50k",
		})
	}

	engine := recall.NewEngine(tdb.DB)
	queries := bench.RecallBenchQueries()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := queries[i%len(queries)]
		_, err := engine.Search(ctx, recall.SearchOptions{
			Query:     q,
			Namespace: "bench-50k",
			Limit:     10,
		})
		if err != nil {
			b.Fatalf("recall: %v", err)
		}
	}
}

// BenchmarkConsolidation_5K measures how long Pipeline.Run() takes over 5 000
// observations at varying retention and kind levels.
func BenchmarkConsolidation_5K(b *testing.B) {
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)
	linkStore := links.NewStore(tdb.DB, idGen)
	decayEngine := decay.NewEngine(tdb.DB)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	embedder := bench.NewFakeEmbedder(0)
	embedQueue := embed.NewQueue(embedder, tdb.DB)

	pipeline := consolidate.NewPipeline(
		tdb.DB, decayEngine, embedder, embedQueue,
		gate, linkStore, llm.Disabled{}, idGen,
		consolidate.Config{},
	)

	retentions := []observation.Retention{
		observation.RetentionDurable,
		observation.RetentionDurable,
		observation.RetentionOperational,
	}
	kinds := []observation.Kind{
		observation.KindSemantic,
		observation.KindProcedural,
		observation.KindEpisodic,
	}

	b.Log("seeding 5 000 observations...")
	for i := 0; i < 5_000; i++ {
		_, _ = store.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Consolidation seed %d: %s", i, benchTopic(i)),
			Content:         fmt.Sprintf("Content %d about %s for consolidation benchmark testing pipeline throughput", i, benchTopic(i)),
			ObservationType: observation.ObservationTypeDecision,
			Kind:            kinds[i%len(kinds)],
			Namespace:       "bench-consol",
			Retention:       retentions[i%len(retentions)],
			Confidence:      0.6 + float64(i%4)*0.1,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := pipeline.Run(ctx); err != nil {
			b.Fatalf("pipeline.Run: %v", err)
		}
	}
}

// BenchmarkConcurrentWrites measures throughput and error rate when 8 goroutines
// write observations simultaneously — the key WAL contention scenario.
func BenchmarkConcurrentWrites(b *testing.B) {
	ctx := context.Background()
	tdb := openBenchDB(b)
	store := observation.NewStore(tdb.DB, nil)

	const goroutines = 8
	const writesPerGoroutine = 100

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		errc := make(chan int, goroutines)

		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(gid int) {
				defer wg.Done()
				errs := 0
				for j := 0; j < writesPerGoroutine; j++ {
					_, err := store.Save(ctx, observation.Observation{
						Title:     fmt.Sprintf("Concurrent g%d-j%d-i%d", gid, j, i),
						Content:   fmt.Sprintf("Concurrent write goroutine %d iteration %d observation %d", gid, i, j),
						Namespace: "bench-concurrent",
					})
					if err != nil {
						errs++
					}
				}
				errc <- errs
			}(g)
		}

		wg.Wait()
		close(errc)

		totalErrors := 0
		for e := range errc {
			totalErrors += e
		}
		if totalErrors > 0 {
			b.Errorf("concurrent writes: %d errors in iteration %d", totalErrors, i)
		}
	}
}

// BenchmarkFactGraph measures fact save throughput against a pre-existing
// observation anchor.
func BenchmarkFactGraph(b *testing.B) {
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()
	tdb := openBenchDB(b)
	factStore := facts.NewStore(tdb.DB, idGen)

	// Create an anchor observation to attach facts to.
	obsStore := observation.NewStore(tdb.DB, nil)
	obs, err := obsStore.Save(ctx, observation.Observation{
		Title:     "Anchor observation for fact graph benchmark",
		Content:   "Anchor content",
		Namespace: "bench-facts",
	})
	if err != nil {
		b.Fatalf("save anchor: %v", err)
	}

	predicates := []string{"uses_framework", "depends_on", "configured_by", "deployed_with", "tested_by"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := factStore.Save(ctx, facts.Fact{
			ObservationID: obs.ID,
			Subject:       fmt.Sprintf("project-%d", i%100),
			Predicate:     predicates[i%len(predicates)],
			Object:        fmt.Sprintf("component-%d", i%10),
			Namespace:     "bench-facts",
		})
		if err != nil {
			b.Fatalf("fact save %d: %v", i, err)
		}
	}
}

// ─── Integration Test: Full Suite at Small Scale ─────────────────────────────

// TestBenchmarkSuite_Small runs the complete benchmark suite at small scale and
// verifies that the Report is well-formed: all nine dimensions ran, every
// result has a non-zero max score, a valid grade, and the JSON export is
// parseable.
func TestBenchmarkSuite_Small(t *testing.T) {
	cfg := bench.NewScaleConfig("small")
	suite := bench.NewSuite(cfg)

	// Register all dimensions — mirrors RunCLI allDims order.
	suite.Register(
		bench.CogKnowledgeUpdate{},
		bench.CogSignalNoise{},
		bench.CogCrossSession{},
		bench.CogTemporal{},
		bench.CogLifecycle{},
		bench.PerfWrite{},
		bench.PerfRecall{},
		bench.PerfConcurrent{},
		bench.PerfContext{},
	)

	ctx := context.Background()
	report, err := suite.Run(ctx)
	if err != nil {
		t.Fatalf("suite.Run: %v", err)
	}

	// Basic Report completeness checks.
	if report == nil {
		t.Fatal("report is nil")
	}
	const wantDims = 9
	if len(report.Dimensions) != wantDims {
		t.Errorf("expected %d dimensions, got %d", wantDims, len(report.Dimensions))
	}
	if report.Scale != "small" {
		t.Errorf("expected scale=small, got %q", report.Scale)
	}
	if report.Grade == "" {
		t.Error("report.Grade is empty")
	}
	if report.Duration == 0 {
		t.Error("report.Duration is zero")
	}

	seenNames := make(map[string]bool, wantDims)
	seenCategories := make(map[string]bool, 2)

	for i, dim := range report.Dimensions {
		if dim.DimensionName == "" {
			t.Errorf("dimension[%d]: DimensionName is empty", i)
		}
		if dim.Category == "" {
			t.Errorf("dimension[%d] %q: Category is empty", i, dim.DimensionName)
		}
		if dim.Max <= 0 {
			t.Errorf("dimension[%d] %q: Max=%v, want >0", i, dim.DimensionName, dim.Max)
		}
		if dim.Score < 0 || dim.Score > 100 {
			t.Errorf("dimension[%d] %q: Score=%v out of [0,100]", i, dim.DimensionName, dim.Score)
		}
		if dim.Grade == "" {
			t.Errorf("dimension[%d] %q: Grade is empty", i, dim.DimensionName)
		}
		if dim.Duration == 0 {
			t.Errorf("dimension[%d] %q: Duration is zero", i, dim.DimensionName)
		}
		seenNames[dim.DimensionName] = true
		seenCategories[dim.Category] = true
	}

	// Both cognitive and performance dimensions must be present.
	for _, cat := range []string{"cognitive", "performance"} {
		if !seenCategories[cat] {
			t.Errorf("no dimensions in category %q", cat)
		}
	}

	// Verify JSON export produces a parseable, non-empty file.
	outPath := filepath.Join(t.TempDir(), "report.json")
	if err := bench.ExportJSON(report, outPath); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read exported JSON: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("exported JSON is empty")
	}
	// A valid JSON object starts with '{'.
	if data[0] != '{' {
		t.Errorf("exported JSON does not start with '{': %q", string(data[:min(50, len(data))]))
	}
}

// TestBenchmarkSuite_CategoryFilter verifies that the category filter used by
// cli.RunCLI selects the correct number of dimensions.
func TestBenchmarkSuite_CategoryFilter(t *testing.T) {
	allDims := []bench.Dimension{
		bench.CogKnowledgeUpdate{},
		bench.CogSignalNoise{},
		bench.CogCrossSession{},
		bench.CogTemporal{},
		bench.CogLifecycle{},
		bench.PerfWrite{},
		bench.PerfRecall{},
		bench.PerfConcurrent{},
		bench.PerfContext{},
	}

	tests := []struct {
		category  string
		wantCount int
	}{
		{"cognitive", 5},
		{"performance", 4},
		{"all", 9},
	}

	for _, tc := range tests {
		t.Run(tc.category, func(t *testing.T) {
			cfg := bench.NewScaleConfig("small")
			suite := bench.NewSuite(cfg)

			for _, d := range allDims {
				if tc.category == "all" || d.Category() == tc.category {
					suite.Register(d)
				}
			}

			if suite.DimCount() != tc.wantCount {
				t.Errorf("category=%q: got %d dimensions, want %d",
					tc.category, suite.DimCount(), tc.wantCount)
			}
		})
	}
}

// TestBenchmarkSuite_DimensionFilter verifies that an explicit dimension-name
// filter selects exactly the named dimensions.
func TestBenchmarkSuite_DimensionFilter(t *testing.T) {
	cfg := bench.NewScaleConfig("small")
	suite := bench.NewSuite(cfg)

	wantNames := map[string]bool{
		"Write Throughput":   true,
		"Recall Performance": true,
	}

	allDims := []bench.Dimension{
		bench.CogKnowledgeUpdate{},
		bench.CogSignalNoise{},
		bench.CogCrossSession{},
		bench.CogTemporal{},
		bench.CogLifecycle{},
		bench.PerfWrite{},
		bench.PerfRecall{},
		bench.PerfConcurrent{},
		bench.PerfContext{},
	}

	for _, d := range allDims {
		if wantNames[d.Name()] {
			suite.Register(d)
		}
	}

	if suite.DimCount() != len(wantNames) {
		t.Fatalf("expected %d dimensions, got %d", len(wantNames), suite.DimCount())
	}
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
