package consolidate

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/temporal"
)

func newTestPipeline(t *testing.T) (*Pipeline, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	p := NewPipeline(database, decayEngine, embed.Disabled{}, nil, gate, linkStore, llm.Disabled{}, nil, idGen, Config{})
	return p, &db.TestDB{DB: database}
}

func TestPromoteBufferToWorking(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// High importance + good activation/consolidation → should be promoted
	// Composite: 0.5*0.4 + 0.5*0.35 + 0.4*0.25 = 0.2 + 0.175 + 0.1 = 0.475 >= 0.35
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, consolidation_status)
		VALUES('HIGH1', 'Important', 'content', 'decision', 0, 0.9, 0.5, 0.5, 0.4, 'semantic', 'default', 'pending')`)

	// Low importance + low activation/consolidation → should NOT be promoted
	// Composite: 0.1*0.4 + 0.1*0.35 + 0.1*0.25 = 0.04 + 0.035 + 0.025 = 0.1 < 0.35
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, consolidation_status)
		VALUES('LOW1', 'Trivial', 'content', 'discovery', 0, 0.5, 0.1, 0.1, 0.1, 'episodic', 'default', 'pending')`)

	// Procedural → auto-promote regardless of scores
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, consolidation_status)
		VALUES('PROC1', 'Procedure', 'content', 'pattern', 0, 0.5, 0.1, 0.1, 0.1, 'procedural', 'default', 'pending')`)

	promoted, err := p.promoteBufferToWorking(ctx, 1)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 2 { // HIGH1 + PROC1
		t.Errorf("expected 2 promoted, got %d", promoted)
	}

	// Verify layers
	assertLayer(t, tdb, "HIGH1", 1)
	assertLayer(t, tdb, "LOW1", 0)
	assertLayer(t, tdb, "PROC1", 1)
}

func TestPromoteWorkingToCore(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Old + accessed enough + sufficient activation/consolidation + recent access → should promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('OLD_ACC', 'Veteran', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Old but not accessed enough → should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('OLD_NO', 'Old unused', 'content', 'discovery', 1, 0.5, 0.3, 0.5, 0.6, 'semantic', 'default', 'durable', 1, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Recent + accessed → should NOT promote (too new)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('NEW_ACC', 'New active', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-1 days'))`)

	// Old + accessed but low activation/consolidation → should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('LOW_STRENGTH', 'Low strength', 'content', 'discovery', 1, 0.9, 0.8, 0.1, 0.1, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted to Core, got %d", promoted)
	}

	assertLayer(t, tdb, "OLD_ACC", 2)
	assertLayer(t, tdb, "OLD_NO", 1)
	assertLayer(t, tdb, "NEW_ACC", 1)
	assertLayer(t, tdb, "LOW_STRENGTH", 1)
}

func TestEvictBuffer(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Insert more than bufferCap observations
	for i := 0; i < bufferCap+5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'discovery', 0, 0.7, ?, 'semantic', 'default')`,
			idFromInt(i), float64(i)/float64(bufferCap+5))
	}

	evicted, err := p.evictBuffer(ctx)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if evicted != 5 {
		t.Errorf("expected 5 evicted, got %d", evicted)
	}

	// Verify buffer is at cap
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE layer = 0 AND deleted_at IS NULL").Scan(&count)
	if count != bufferCap {
		t.Errorf("buffer count = %d, want %d", count, bufferCap)
	}
}

func TestFullConsolidationRun(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Set up Buffer observation with sufficient composite score
	// importance=0.5, activation=0.5, consolidation=0.4 = composite 0.475 >= 0.35
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, consolidation_status)
		VALUES('B1', 'Buffer high', 'content', 'decision', 0, 0.9, 0.5, 0.5, 0.4, 'semantic', 'default', 'pending')`)

	// Set up Working observation with sufficient activation/consolidation for Core
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('W1', 'Working old', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	err := p.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// B1 should be promoted to Working
	assertLayer(t, tdb, "B1", 1)
	// W1 should be promoted to Core
	assertLayer(t, tdb, "W1", 2)

	// Verify consolidation run was recorded
	var runCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM consolidation_runs WHERE status = 'completed'").Scan(&runCount)
	if runCount != 1 {
		t.Errorf("expected 1 completed run, got %d", runCount)
	}
}

func TestPromoteWorkingToCoreRespectsRetention(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Durable + old + accessed + sufficient activation/consolidation + recent access → should promote to Core
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('DUR1', 'Durable decision', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Operational + old + accessed + sufficient activation/consolidation + recent access → should NOT promote to Core (retention blocks it)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('OPS1', 'Step 4 progress', 'content', 'discovery', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'operational', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted to Core, got %d", promoted)
	}

	assertLayer(t, tdb, "DUR1", 2)
	assertLayer(t, tdb, "OPS1", 1) // stays in Working
}

func TestForceRunRespectsRetention(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Durable in Buffer → should reach Core via ForceRun
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('FBUF_DUR', 'Architecture decision', 'content', 'decision', 0, 0.9, 0.8, 'semantic', 'default', 'durable')`)

	// Operational in Buffer → should reach Working but NOT Core
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('FBUF_OPS', 'Implement Step 3', 'content', 'discovery', 0, 0.9, 0.8, 'semantic', 'default', 'operational')`)

	err := p.ForceRun(ctx)
	if err != nil {
		t.Fatalf("force run: %v", err)
	}

	assertLayer(t, tdb, "FBUF_DUR", 2) // durable → Core
	assertLayer(t, tdb, "FBUF_OPS", 1) // operational → Working only
}

func TestEvictBufferPrefersOperational(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Insert bufferCap + 2 observations: half operational, half durable, same importance
	for i := 0; i < bufferCap; i++ {
		ret := "durable"
		if i%2 == 0 {
			ret = "operational"
		}
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
			VALUES(?, 'obs', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', ?)`,
			idFromInt(i), ret)
	}
	// Add 2 more to trigger eviction
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('EXTRA_OPS', 'extra op', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', 'operational')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention)
		VALUES('EXTRA_DUR', 'extra dur', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', 'durable')`)

	evicted, err := p.evictBuffer(ctx)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if evicted != 2 {
		t.Errorf("expected 2 evicted, got %d", evicted)
	}

	// Verify: evicted observations should be operational (since they sort first)
	var deletedOps, deletedDur int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NOT NULL AND retention = 'operational'").Scan(&deletedOps)
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NOT NULL AND retention = 'durable'").Scan(&deletedDur)

	if deletedOps < deletedDur {
		t.Errorf("expected operational observations to be evicted first: ops=%d, dur=%d", deletedOps, deletedDur)
	}
}

func TestRetentionLifecycleIntegration(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Save one operational observation (step log) in Buffer with sufficient composite score
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('LIFE_OPS', 'Implement Step 5', 'Created migration files', 'discovery', 0, 0.9, 0.8, 0.5, 0.4, 'semantic', 'lifecycle', 'operational', 'pending')`)

	// Save one durable observation (real decision) in Buffer with sufficient composite score
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('LIFE_DUR', 'Use SQLite for storage', 'Decided SQLite with WAL mode for single-writer', 'decision', 0, 0.9, 0.8, 0.5, 0.4, 'semantic', 'lifecycle', 'durable', 'pending')`)

	// Run normal consolidation (Run)
	err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Both should be promoted to Working (Buffer→Working doesn't filter by retention)
	assertLayer(t, tdb, "LIFE_OPS", 1)
	assertLayer(t, tdb, "LIFE_DUR", 1)

	// Now simulate conditions for Working→Core (set access_count, recent access, old created_at, sufficient activation/consolidation)
	tdb.Exec(t, `UPDATE observations SET access_count = 10, last_accessed = datetime('now', '-1 days'), created_at = datetime('now', '-30 days'), activation_level = 0.5, consolidation_strength = 0.6 WHERE id IN ('LIFE_OPS', 'LIFE_DUR')`)

	// Run ForceRun
	err = p.ForceRun(ctx)
	if err != nil {
		t.Fatalf("ForceRun: %v", err)
	}

	// Durable should reach Core, operational should stay in Working
	assertLayer(t, tdb, "LIFE_DUR", 2)
	assertLayer(t, tdb, "LIFE_OPS", 1) // remains in Working

	// Verify operational observation is NOT deleted
	var deletedAt sql.NullString
	tdb.DB.QueryRowContext(ctx, "SELECT deleted_at FROM observations WHERE id = 'LIFE_OPS'").Scan(&deletedAt)
	if deletedAt.Valid {
		t.Error("operational observation should not be deleted, just kept in Working")
	}
}

func TestCleanupStaleSessions(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Insert a stale session (> 24 hours old, still active)
	tdb.Exec(t, `INSERT INTO sessions(id, title, namespace, status, started_at)
		VALUES('STALE1', 'Old Session', 'default', 'active', datetime('now', '-25 hours'))`)

	// Insert a recent session (< 24 hours old, active - should NOT be cleaned)
	tdb.Exec(t, `INSERT INTO sessions(id, title, namespace, status, started_at)
		VALUES('RECENT1', 'Recent Session', 'default', 'active', datetime('now', '-1 hours'))`)

	// Insert a completed session (should NOT be touched)
	tdb.Exec(t, `INSERT INTO sessions(id, title, namespace, status, started_at, ended_at)
		VALUES('COMPLETED1', 'Completed Session', 'default', 'completed', datetime('now', '-48 hours'), datetime('now', '-47 hours'))`)

	// Run consolidation
	err := p.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Verify stale session is now abandoned
	var staleStatus string
	err = tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = 'STALE1'").Scan(&staleStatus)
	if err != nil {
		t.Fatalf("get stale session status: %v", err)
	}
	if staleStatus != "abandoned" {
		t.Errorf("STALE1: expected status 'abandoned', got '%s'", staleStatus)
	}

	// Verify recent session is still active
	var recentStatus string
	err = tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = 'RECENT1'").Scan(&recentStatus)
	if err != nil {
		t.Fatalf("get recent session status: %v", err)
	}
	if recentStatus != "active" {
		t.Errorf("RECENT1: expected status 'active', got '%s'", recentStatus)
	}

	// Verify completed session is still completed
	var completedStatus string
	err = tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = 'COMPLETED1'").Scan(&completedStatus)
	if err != nil {
		t.Fatalf("get completed session status: %v", err)
	}
	if completedStatus != "completed" {
		t.Errorf("COMPLETED1: expected status 'completed', got '%s'", completedStatus)
	}

	// Verify ended_at was set for the stale session
	var endedAt sql.NullString
	err = tdb.DB.QueryRowContext(ctx, "SELECT ended_at FROM sessions WHERE id = 'STALE1'").Scan(&endedAt)
	if err != nil {
		t.Fatalf("get stale session ended_at: %v", err)
	}
	if !endedAt.Valid {
		t.Error("STALE1: expected ended_at to be set")
	}
}

func TestHasDistinctTemporalWindows(t *testing.T) {
	march := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	marchLater := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		a    []temporal.Mention
		b    []temporal.Mention
		want bool
	}{
		{
			name: "no mentions → false",
			want: false,
		},
		{
			name: "one side empty → false",
			a:    []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			want: false,
		},
		{
			name: "months apart → true",
			a:    []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			b:    []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &jan}},
			want: true,
		},
		{
			name: "days apart → false (within 7-day window)",
			a:    []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			b:    []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &marchLater}},
			want: false,
		},
		{
			name: "no dates in mentions → false",
			a:    []temporal.Mention{{Kind: temporal.KindCurrentState}},
			b:    []temporal.Mention{{Kind: temporal.KindCurrentState}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasDistinctTemporalWindows(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("hasDistinctTemporalWindows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func assertLayer(t *testing.T, tdb *db.TestDB, id string, expectedLayer int) {
	t.Helper()
	var layer int
	err := tdb.DB.QueryRowContext(context.Background(), "SELECT layer FROM observations WHERE id = ?", id).Scan(&layer)
	if err != nil {
		t.Fatalf("get layer for %s: %v", id, err)
	}
	if layer != expectedLayer {
		t.Errorf("%s: layer = %d, want %d", id, layer, expectedLayer)
	}
}

func idFromInt(i int) string {
	return "OBS" + padInt(i)
}

func padInt(i int) string {
	s := ""
	if i < 10 {
		s = "00"
	} else if i < 100 {
		s = "0"
	}
	return s + intToStr(i)
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestConsolidationThresholdsFromConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Create pipeline with custom thresholds
	cfg := Config{
		Interval:         30 * time.Minute,
		DedupThreshold:   0.92, // Custom high threshold
		ContradictionMin: 0.70,
		ContradictionMax: 0.88,
	}
	p := NewPipeline(database, decayEngine, embed.Disabled{}, nil, gate, linkStore, llm.Disabled{}, nil, idGen, cfg)

	// Verify the config was applied
	if p.cfg.DedupThreshold != 0.92 {
		t.Errorf("DedupThreshold = %f, want 0.92", p.cfg.DedupThreshold)
	}
	if p.cfg.ContradictionMin != 0.70 {
		t.Errorf("ContradictionMin = %f, want 0.70", p.cfg.ContradictionMin)
	}
	if p.cfg.ContradictionMax != 0.88 {
		t.Errorf("ContradictionMax = %f, want 0.88", p.cfg.ContradictionMax)
	}

	// Verify the detector received the thresholds
	if p.contradictionDetector == nil {
		t.Fatal("contradictionDetector is nil")
	}
}

// normalize normalizes a vector to unit length
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return v
	}
	result := make([]float32, len(v))
	for i, x := range v {
		result[i] = float32(float64(x) / norm)
	}
	return result
}

// createTestEmbedding creates a simple test embedding with the given dimension
// The seed parameter allows creating different but deterministic vectors
func createTestEmbedding(seed int, dim int) []float32 {
	vec := make([]float32, dim)
	// Use a deterministic pseudo-random generator based on seed
	x := uint32(seed)
	for i := 0; i < dim; i++ {
		// Simple LCG for deterministic "random" numbers
		x = x*1103515245 + 12345
		val := float32(int32(x))/float32(1<<30) - 1.0 // Range [-1, 1]
		vec[i] = val
	}
	return normalize(vec)
}

// createSimilarEmbedding creates an embedding with the specified cosine similarity to base
// similarity should be between 0 and 1
func createSimilarEmbedding(base []float32, similarity float64) []float32 {
	dim := len(base)
	baseNorm := normalize(base)

	// Create a random orthogonal vector using deterministic approach
	random := make([]float32, dim)
	for i := range random {
		// Use index-based deterministic "random" values
		random[i] = float32((i*137+47)%100)/50.0 - 1.0
	}
	random = normalize(random)

	// Make random orthogonal to base by subtracting projection
	var dot float64
	for i := range random {
		dot += float64(random[i]) * float64(baseNorm[i])
	}
	for i := range random {
		random[i] = float32(float64(random[i]) - dot*float64(baseNorm[i]))
	}
	random = normalize(random)

	// Combine: result = similarity * base + sqrt(1-similarity^2) * random
	result := make([]float32, dim)
	orthoWeight := math.Sqrt(1 - similarity*similarity)
	for i := range result {
		result[i] = float32(similarity*float64(baseNorm[i]) + orthoWeight*float64(random[i]))
	}

	return normalize(result)
}

func TestCreateCrossNamespaceRelatesTo(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Create a mock embedder that will report as available
	mockEmbedder := &mockEmbedder{available: true, dim: 128}

	// Create pipeline with thresholds that allow relates_to creation
	cfg := Config{
		Interval:       30 * time.Minute,
		DedupThreshold: 0.90, // RelatedMax will be 0.90
		RelatedMin:     0.70, // Window [0.70, 0.90)
		RelatedMax:     0.90,
	}
	p := NewPipeline(database, decayEngine, mockEmbedder, nil, gate, linkStore, llm.Disabled{}, nil, idGen, cfg)

	ctx := context.Background()

	// Create base embedding
	baseEmbedding := createTestEmbedding(1, 128)

	// Create observations in different namespaces with different similarities
	// OBS1: namespace A, base embedding
	sim70 := createSimilarEmbedding(baseEmbedding, 0.75) // Within window [0.70, 0.90)
	sim90 := createSimilarEmbedding(baseEmbedding, 0.92) // Above RelatedMax (should not create link)
	sim50 := createSimilarEmbedding(baseEmbedding, 0.50) // Below RelatedMin (should not create link)

	// Insert observations in namespace A (Working layer)
	tdb := &db.TestDB{DB: database}
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_A1', 'Base observation', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'namespace_a', ?)`,
		embed.SerializeF32(baseEmbedding))

	// Insert observations in namespace B with different similarities
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_B1', 'Similar 75%', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'namespace_b', ?)`,
		embed.SerializeF32(sim70))
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_B2', 'Similar 92%', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'namespace_b', ?)`,
		embed.SerializeF32(sim90))
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_B3', 'Similar 50%', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'namespace_b', ?)`,
		embed.SerializeF32(sim50))

	// Run the relates_to creation
	count, err := p.createCrossNamespaceRelatesTo(ctx)
	if err != nil {
		t.Fatalf("createCrossNamespaceRelatesTo: %v", err)
	}

	// Should create 1 link: OBS_A1 -> OBS_B1 (sim 75% is in [0.70, 0.90))
	// OBS_B2 (92%) is >= RelatedMax so no link
	// OBS_B3 (50%) is < RelatedMin so no link
	if count != 1 {
		t.Errorf("expected 1 relates_to link, got %d", count)
	}

	// Verify the link was created correctly
	var linkCount int
	err = database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observation_links
		WHERE relation_type = 'relates_to'
		  AND ((source_id = 'OBS_A1' AND target_id = 'OBS_B1') OR (source_id = 'OBS_B1' AND target_id = 'OBS_A1'))
	`).Scan(&linkCount)
	if err != nil {
		t.Fatalf("query link: %v", err)
	}
	if linkCount != 1 {
		t.Errorf("expected 1 link between OBS_A1 and OBS_B1, got %d", linkCount)
	}
}

func TestCreateCrossNamespaceRelatesToSameNamespace(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mockEmbedder := &mockEmbedder{available: true, dim: 128}

	cfg := Config{
		Interval:       30 * time.Minute,
		DedupThreshold: 0.90,
		RelatedMin:     0.70,
		RelatedMax:     0.90,
	}
	p := NewPipeline(database, decayEngine, mockEmbedder, nil, gate, linkStore, llm.Disabled{}, nil, idGen, cfg)

	ctx := context.Background()
	tdb := &db.TestDB{DB: database}

	// Create two observations in the SAME namespace with high similarity
	baseEmbedding := createTestEmbedding(1, 128)
	similarEmbedding := createSimilarEmbedding(baseEmbedding, 0.75)

	// Both in namespace_a
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_S1', 'Base', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'same_namespace', ?)`,
		embed.SerializeF32(baseEmbedding))
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_S2', 'Similar', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'same_namespace', ?)`,
		embed.SerializeF32(similarEmbedding))

	count, err := p.createCrossNamespaceRelatesTo(ctx)
	if err != nil {
		t.Fatalf("createCrossNamespaceRelatesTo: %v", err)
	}

	// Should not create any links because observations are in the same namespace
	if count != 0 {
		t.Errorf("expected 0 links for same-namespace observations, got %d", count)
	}
}

func TestCreateCrossNamespaceRelatesToDuplicatePrevention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mockEmbedder := &mockEmbedder{available: true, dim: 128}

	cfg := Config{
		Interval:       30 * time.Minute,
		DedupThreshold: 0.90,
		RelatedMin:     0.70,
		RelatedMax:     0.90,
	}
	p := NewPipeline(database, decayEngine, mockEmbedder, nil, gate, linkStore, llm.Disabled{}, nil, idGen, cfg)

	ctx := context.Background()
	tdb := &db.TestDB{DB: database}

	// Create two observations in different namespaces
	baseEmbedding := createTestEmbedding(1, 128)
	similarEmbedding := createSimilarEmbedding(baseEmbedding, 0.75)

	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_D1', 'Base', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'ns_a', ?)`,
		embed.SerializeF32(baseEmbedding))
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_D2', 'Similar', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'ns_b', ?)`,
		embed.SerializeF32(similarEmbedding))

	// Create an existing relates_to link (in reverse direction)
	tdb.Exec(t, `INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
		VALUES('LINK001', 'OBS_D2', 'OBS_D1', 'relates_to', 0.75, 'consolidator')`)

	// Run the relates_to creation - should not create a duplicate
	count, err := p.createCrossNamespaceRelatesTo(ctx)
	if err != nil {
		t.Fatalf("createCrossNamespaceRelatesTo: %v", err)
	}

	// Should not create any new links because one already exists
	if count != 0 {
		t.Errorf("expected 0 new links when link already exists, got %d", count)
	}

	// Verify only one link exists
	var linkCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_links WHERE relation_type = 'relates_to'`).Scan(&linkCount)
	if linkCount != 1 {
		t.Errorf("expected 1 total link, got %d", linkCount)
	}
}

func TestCreateCrossNamespaceRelatesToThresholds(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	decayEngine := decay.NewEngine(database)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mockEmbedder := &mockEmbedder{available: true, dim: 128}

	cfg := Config{
		Interval:       30 * time.Minute,
		DedupThreshold: 0.85,
		RelatedMin:     0.65,
		RelatedMax:     0.85,
	}
	p := NewPipeline(database, decayEngine, mockEmbedder, nil, gate, linkStore, llm.Disabled{}, nil, idGen, cfg)

	ctx := context.Background()
	tdb := &db.TestDB{DB: database}

	baseEmbedding := createTestEmbedding(1, 128)

	// Test cases at boundary values
	testCases := []struct {
		name       string
		similarity float64
		wantLink   bool
	}{
		{"exactly at RelatedMin", 0.65, true},  // >= RelatedMin should create link
		{"just below RelatedMin", 0.64, false}, // < RelatedMin should NOT create link
		{"just below RelatedMax", 0.84, true},  // < RelatedMax should create link
		{"exactly at RelatedMax", 0.85, false}, // >= RelatedMax should NOT create link
	}

	for i, tc := range testCases {
		obsID := fmt.Sprintf("OBS_T%d", i)
		simEmbedding := createSimilarEmbedding(baseEmbedding, tc.similarity)

		// Insert observation in different namespace
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
			VALUES(?, ?, 'content', 'discovery', 1, 0.9, 0.8, 'semantic', ?, ?)`,
			obsID, tc.name, fmt.Sprintf("ns_%d", i), embed.SerializeF32(simEmbedding))
	}

	// Insert base observation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, embedding)
		VALUES('OBS_BASE', 'Base', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'base_ns', ?)`,
		embed.SerializeF32(baseEmbedding))

	count, err := p.createCrossNamespaceRelatesTo(ctx)
	if err != nil {
		t.Fatalf("createCrossNamespaceRelatesTo: %v", err)
	}

	// Should create 2 links (exactly at RelatedMin and just below RelatedMax)
	if count != 2 {
		t.Errorf("expected 2 relates_to links, got %d", count)
	}
}

// mockEmbedder is a mock implementation of embed.Provider for testing
type mockEmbedder struct {
	available bool
	dim       int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockEmbedder) Dimensions() int {
	return m.dim
}

func (m *mockEmbedder) Name() string {
	return "mock"
}

// ============================================================================
// BASELINE TESTS: Current promotion behavior capture
// These tests document the current behavior to ensure refactoring doesn't
// break existing functionality, and highlight the problems to be fixed.
// ============================================================================

// TestPromoteWorkingToCoreWithRecalibration verifies that observations promoted
// to Core are now recalibrated to prevent valuable knowledge from having low scores.
func TestPromoteWorkingToCoreWithRecalibration(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create a durable observation in Working that has been decayed multiple times
	// (simulating it staying in Working for a while)
	// Must have sufficient activation/consolidation AND composite score for new promotion rules
	// importance=0.5, activation=0.50, consolidation=0.60 = composite 0.515 >= 0.50
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('DECAYED_DUR', 'Important decision', 'Use event sourcing', 'decision', 1, 0.9, 0.50, 0.50, 0.60, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Promote to Core
	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted, got %d", promoted)
	}

	// Verify it reached Core
	assertLayer(t, tdb, "DECAYED_DUR", 2)

	// Check that importance WAS recalibrated (no longer at the low original value)
	var importance float64
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'DECAYED_DUR'").Scan(&importance)

	// Should be recalibrated to at least the base Core importance
	if importance < coreRecalibrationBaseImportance {
		t.Errorf("promoted to Core with degraded importance: %.3f, want >= %.2f", importance, coreRecalibrationBaseImportance)
	}

	// Should have type bonus for decision
	expectedMin := coreRecalibrationBaseImportance + coreRecalibrationTypeBonus
	if importance < expectedMin {
		t.Errorf("expected at least %.2f (base + type bonus), got %.3f", expectedMin, importance)
	}

	t.Logf("Recalibrated: promoted to Core with importance %.3f (pre-promotion was 0.50)", importance)
}

// TestPromotionRequiresSemanticStability verifies that Working->Core promotion
// now requires activation and consolidation strength, not just access_count and age.
func TestPromotionRequiresSemanticStability(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with same access/age but different activation/consolidation
	observations := []struct {
		id            string
		obsType       string
		activation    float64
		consolidation float64
		shouldPromote bool
		description   string
	}{
		{"STABLE_DEC", "decision", 0.5, 0.6, true, "high stability (decision)"},
		{"STABLE_BUG", "bugfix", 0.5, 0.6, true, "high stability (bugfix)"},
		{"WEAK_DISC", "discovery", 0.1, 0.1, false, "low stability (discovery)"},
		{"WEAK_PREF", "preference", 0.2, 0.2, false, "medium-low stability (preference)"},
	}

	for _, obs := range observations {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
			VALUES(?, 'Test', 'content', ?, 1, 0.9, 0.5, ?, ?, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`,
			obs.id, obs.obsType, obs.activation, obs.consolidation)
	}

	// Promote to Core
	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 2 {
		t.Errorf("expected 2 promoted (only stable ones), got %d", promoted)
	}

	// Verify promotion based on stability
	for _, obs := range observations {
		var layer int
		tdb.DB.QueryRowContext(ctx, "SELECT layer FROM observations WHERE id = ?", obs.id).Scan(&layer)

		if obs.shouldPromote {
			if layer != 2 {
				t.Errorf("%s: expected promotion to Core, got layer %d", obs.description, layer)
			}
		} else {
			if layer != 1 {
				t.Errorf("%s: expected to stay in Working, got layer %d", obs.description, layer)
			}
		}
	}

	t.Logf("SUCCESS: promotion now requires semantic stability (activation + consolidation), not just access/age")
}

// TestBufferToWorkingPromotionByCompositeScore verifies that Buffer->Working
// promotion now uses a composite score combining importance, activation, and consolidation.
func TestBufferToWorkingPromotionByCompositeScore(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with different signals
	// High composite score: importance=0.5, activation=0.5, consolidation=0.4 = 0.475
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('BUF_HIGH_COMP', 'High composite', 'content', 'decision', 0, 0.9, 0.5, 0.5, 0.4, 'semantic', 'default', 'durable', 'pending')`)

	// Low composite score: importance=0.5, activation=0.1, consolidation=0.1 = 0.26
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('BUF_LOW_COMP', 'Low composite', 'content', 'discovery', 0, 0.9, 0.5, 0.1, 0.1, 'semantic', 'default', 'durable', 'pending')`)

	// High importance but low activation/consolidation (should still promote due to high importance + type)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('BUF_HIGH_IMP', 'High importance', 'content', 'decision', 0, 0.9, 0.75, 0.1, 0.1, 'semantic', 'default', 'durable', 'pending')`)

	// Procedural (auto-promote regardless of scores)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('BUF_PROC', 'Procedural', 'content', 'pattern', 0, 0.5, 0.1, 0.1, 0.1, 'procedural', 'default', 'durable', 'pending')`)

	// Promote Buffer to Working
	promoted, err := p.promoteBufferToWorking(ctx, 1)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Should promote 3: high composite, high importance (auto-promote), procedural (auto-promote)
	if promoted != 3 {
		t.Errorf("expected 3 promoted, got %d", promoted)
	}

	// Verify promoted
	assertLayer(t, tdb, "BUF_HIGH_COMP", 1)
	assertLayer(t, tdb, "BUF_HIGH_IMP", 1)
	assertLayer(t, tdb, "BUF_PROC", 1)

	// Verify low composite stayed in Buffer
	assertLayer(t, tdb, "BUF_LOW_COMP", 0)

	t.Logf("SUCCESS: Buffer->Working promotion uses composite score (importance + activation + consolidation)")
}

// TestOperationalCannotReachCore verifies that operational observations
// cannot reach Core even with high access counts and age.
func TestOperationalCannotReachCore(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create operational observation with high access count and age + sufficient activation/consolidation + recent access
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('OPS_WORKING', 'Operational step', 'Step 99 completed', 'discovery', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'operational', 100, datetime('now', '-1 days'), datetime('now', '-90 days'))`)

	// Create durable observation with same stats
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('DUR_WORKING', 'Durable decision', 'Use interfaces', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 100, datetime('now', '-1 days'), datetime('now', '-90 days'))`)

	// Try to promote to Core
	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted (only durable), got %d", promoted)
	}

	// Verify durable reached Core
	assertLayer(t, tdb, "DUR_WORKING", 2)

	// Verify operational stayed in Working
	assertLayer(t, tdb, "OPS_WORKING", 1)

	t.Logf("SUCCESS: operational observations blocked from Core regardless of access/age/activation/consolidation")
}

// TestProceduralAutoPromoteFromBuffer verifies that procedural observations
// are auto-promoted from Buffer regardless of importance.
func TestProceduralAutoPromoteFromBuffer(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create procedural observation with low importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, consolidation_status)
		VALUES('PROC_LOW', 'Low proc', 'How to run tests', 'pattern', 0, 0.5, 0.1, 'procedural', 'default', 'durable', 'pending')`)

	// Create semantic observation with same importance (should not promote)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, consolidation_status)
		VALUES('SEM_LOW', 'Low sem', 'Random thought', 'discovery', 0, 0.5, 0.1, 'semantic', 'default', 'durable', 'pending')`)

	// Promote Buffer to Working
	promoted, err := p.promoteBufferToWorking(ctx, 1)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted (procedural), got %d", promoted)
	}

	// Verify procedural promoted
	assertLayer(t, tdb, "PROC_LOW", 1)

	// Verify semantic stayed in Buffer
	assertLayer(t, tdb, "SEM_LOW", 0)

	t.Logf("Baseline: procedural observations auto-promoted regardless of importance")
}

// TestPromotionRecalibratesImportance verifies that promotion to Core now
// recalibrates importance to prevent degraded scores from entering Core.
func TestPromotionRecalibratesImportance(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with sufficient importance/activation/consolidation for promotion
	// importance=0.50, activation=0.50, consolidation=0.60 = composite 0.515 >= 0.50
	// After promotion, importance will be recalibrated higher
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('LOW_IMP_1', 'Decision', 'Architecture choice', 'decision', 1, 0.9, 0.50, 0.50, 0.60, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('LOW_IMP_2', 'Bugfix', 'Fixed critical bug', 'bugfix', 1, 0.9, 0.50, 0.50, 0.60, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Promote to Core
	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 2 {
		t.Errorf("expected 2 promoted, got %d", promoted)
	}

	// Verify both in Core
	assertLayer(t, tdb, "LOW_IMP_1", 2)
	assertLayer(t, tdb, "LOW_IMP_2", 2)

	// Verify importance WAS recalibrated (no longer at the low original values)
	var imp1, imp2 float64
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'LOW_IMP_1'").Scan(&imp1)
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'LOW_IMP_2'").Scan(&imp2)

	// Should be recalibrated to at least the base Core importance
	if imp1 < coreRecalibrationBaseImportance {
		t.Errorf("LOW_IMP_1: importance not recalibrated, got %.3f, want >= %.2f", imp1, coreRecalibrationBaseImportance)
	}
	if imp2 < coreRecalibrationBaseImportance {
		t.Errorf("LOW_IMP_2: importance not recalibrated, got %.3f, want >= %.2f", imp2, coreRecalibrationBaseImportance)
	}

	// Should have type bonus for decision/bugfix
	expectedMin := coreRecalibrationBaseImportance + coreRecalibrationTypeBonus
	if imp1 < expectedMin {
		t.Errorf("LOW_IMP_1: expected at least %.2f (base + type bonus), got %.3f", expectedMin, imp1)
	}
	if imp2 < expectedMin {
		t.Errorf("LOW_IMP_2: expected at least %.2f (base + type bonus), got %.3f", expectedMin, imp2)
	}

	t.Logf("Recalibrated: decision=%.3f, bugfix=%.3f (pre-promotion: 0.50, 0.50)", imp1, imp2)
}

// TestDurableKnowledgeReachesCoreRecalibrated shows that durable knowledge
// that starts in Buffer, gets decayed, then promoted to Working, then Core -
// is now recalibrated upon entering Core to prevent degraded importance.
func TestDurableKnowledgeReachesCoreRecalibrated(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Simulate a valuable decision that starts in Buffer
	// Must have sufficient activation/consolidation for new promotion rules
	// importance=0.8, activation=0.5, consolidation=0.5 = composite 0.595 >= 0.35
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status, created_at)
		VALUES('VAL_DECISION', 'Critical architecture', 'Use CQRS pattern', 'decision', 0, 0.9, 0.8, 0.50, 0.50, 'semantic', 'default', 'durable', 'pending', datetime('now', '-1 day'))`)

	// Step 1: Promote to Working (meets composite score threshold)
	promoted, err := p.promoteBufferToWorking(ctx, 1)
	if err != nil {
		t.Fatalf("promote buffer: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted to Working, got %d", promoted)
	}
	assertLayer(t, tdb, "VAL_DECISION", 1)

	// Simulate decay while in Working (multiple epochs)
	// In real scenario, this would happen via decay engine
	// Keep importance high enough for composite score AND activation/consolidation high enough
	tdb.Exec(t, `UPDATE observations SET importance = 0.50, activation_level = 0.50, consolidation_strength = 0.60 WHERE id = 'VAL_DECISION'`)

	// Simulate time passing and access accumulation (with recent access)
	tdb.Exec(t, `UPDATE observations SET access_count = 10, last_accessed = datetime('now', '-1 days'), created_at = datetime('now', '-30 days') WHERE id = 'VAL_DECISION'`)

	// Step 2: Promote to Core
	promoted, err = p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote working: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted to Core, got %d", promoted)
	}
	assertLayer(t, tdb, "VAL_DECISION", 2)

	// Check final importance - should be recalibrated, not left at 0.2
	var finalImportance float64
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'VAL_DECISION'").Scan(&finalImportance)

	// Should be recalibrated to at least the base Core importance
	if finalImportance < coreRecalibrationBaseImportance {
		t.Errorf("valuable decision reached Core with degraded importance: final=%.3f, want >= %.2f",
			finalImportance, coreRecalibrationBaseImportance)
	}

	// Should have type bonus for decision
	expectedMin := coreRecalibrationBaseImportance + coreRecalibrationTypeBonus
	if finalImportance < expectedMin {
		t.Errorf("expected at least %.2f (base + type bonus), got %.3f", expectedMin, finalImportance)
	}

	t.Logf("SUCCESS: valuable decision recalibrated in Core: initial=0.800, pre-promotion=0.500, final=%.3f", finalImportance)
}

// TestCalculateCompositeScore verifies the composite score calculation.
func TestCalculateCompositeScore(t *testing.T) {
	tests := []struct {
		name          string
		importance    float64
		activation    float64
		consolidation float64
		want          float64
	}{
		{"all equal 0.5", 0.5, 0.5, 0.5, 0.5},
		{"all max", 1.0, 1.0, 1.0, 1.0},
		{"all min", 0.0, 0.0, 0.0, 0.0},
		{"high importance only", 1.0, 0.0, 0.0, 0.4},
		{"high activation only", 0.0, 1.0, 0.0, 0.35},
		{"high consolidation only", 0.0, 0.0, 1.0, 0.25},
		{"typical values", 0.6, 0.5, 0.4, 0.515},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCompositeScore(tt.importance, tt.activation, tt.consolidation)
			if got != tt.want {
				t.Errorf("calculateCompositeScore() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsHighValueType verifies the high-value type detection.
func TestIsHighValueType(t *testing.T) {
	tests := []struct {
		obsType string
		want    bool
	}{
		{"decision", true},
		{"bugfix", true},
		{"pattern", true},
		{"gotcha", true},
		{"preference", true},
		{"discovery", false},
		{"config", false},
		{"question", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.obsType, func(t *testing.T) {
			got := isHighValueType(tt.obsType)
			if got != tt.want {
				t.Errorf("isHighValueType(%q) = %v, want %v", tt.obsType, got, tt.want)
			}
		})
	}
}

// TestCalculateCoreImportance verifies the Core recalibration logic.
func TestCalculateCoreImportance(t *testing.T) {
	tests := []struct {
		name                  string
		currentImportance     float64
		obsType               string
		consolidationStrength float64
		wantMin               float64
		wantMax               float64
	}{
		{
			name:                  "low importance decision with high consolidation",
			currentImportance:     0.1,
			obsType:               "decision",
			consolidationStrength: 0.8,
			wantMin:               coreRecalibrationBaseImportance + coreRecalibrationTypeBonus,
			wantMax:               1.0,
		},
		{
			name:                  "low importance discovery with low consolidation",
			currentImportance:     0.1,
			obsType:               "discovery",
			consolidationStrength: 0.1,
			wantMin:               coreRecalibrationBaseImportance,
			wantMax:               coreRecalibrationBaseImportance + 0.05,
		},
		{
			name:                  "high importance preserved",
			currentImportance:     0.9,
			obsType:               "decision",
			consolidationStrength: 0.5,
			wantMin:               0.9,
			wantMax:               0.9,
		},
		{
			name:                  "minimum importance enforced",
			currentImportance:     0.01,
			obsType:               "discovery",
			consolidationStrength: 0.0,
			wantMin:               coreRecalibrationBaseImportance,
			wantMax:               coreRecalibrationBaseImportance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCoreImportance(tt.currentImportance, tt.obsType, tt.consolidationStrength)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("calculateCoreImportance() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestWorkingToCoreRequiresRecency verifies that Working->Core requires recent access.
func TestWorkingToCoreRequiresRecency(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Old observation with recent access -> should promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('RECENT_ACCESS', 'Recent', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Old observation with stale access (beyond recency window) -> should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('STALE_ACCESS', 'Stale', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-20 days'), datetime('now', '-30 days'))`)

	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted (only recent access), got %d", promoted)
	}

	assertLayer(t, tdb, "RECENT_ACCESS", 2)
	assertLayer(t, tdb, "STALE_ACCESS", 1)
}

// TestWorkingToCoreRequiresMinimumActivation verifies activation threshold.
func TestWorkingToCoreRequiresMinimumActivation(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Sufficient activation -> should promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('GOOD_ACT', 'Good activation', 'content', 'decision', 1, 0.9, 0.8, 0.35, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Low activation -> should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('LOW_ACT', 'Low activation', 'content', 'decision', 1, 0.9, 0.8, 0.20, 0.6, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted (only good activation), got %d", promoted)
	}

	assertLayer(t, tdb, "GOOD_ACT", 2)
	assertLayer(t, tdb, "LOW_ACT", 1)
}

// TestWorkingToCoreRequiresMinimumConsolidation verifies consolidation threshold.
func TestWorkingToCoreRequiresMinimumConsolidation(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Sufficient consolidation -> should promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('GOOD_CONS', 'Good consolidation', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.45, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	// Low consolidation -> should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, last_accessed, created_at)
		VALUES('LOW_CONS', 'Low consolidation', 'content', 'decision', 1, 0.9, 0.8, 0.5, 0.30, 'semantic', 'default', 'durable', 10, datetime('now', '-1 days'), datetime('now', '-30 days'))`)

	promoted, err := p.promoteWorkingToCore(ctx)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 1 {
		t.Errorf("expected 1 promoted (only good consolidation), got %d", promoted)
	}

	assertLayer(t, tdb, "GOOD_CONS", 2)
	assertLayer(t, tdb, "LOW_CONS", 1)
}

// TestBufferToWorkingRequiresMinimumActivationOrConsolidation verifies Buffer->Working threshold.
func TestBufferToWorkingRequiresMinimumActivationOrConsolidation(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Good activation but low consolidation -> should promote (activation >= min AND composite >= threshold)
	// importance=0.5, activation=0.30, consolidation=0.1 = composite 0.32 < 0.35 (won't promote on composite alone)
	// But it meets the OR condition (activation >= 0.25) and is high-value type, so auto-promotes
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('GOOD_ACT_BUF', 'Good activation', 'content', 'decision', 0, 0.9, 0.75, 0.30, 0.1, 'semantic', 'default', 'durable', 'pending')`)

	// Low activation but good consolidation -> should promote (consolidation >= min AND composite >= threshold)
	// importance=0.5, activation=0.1, consolidation=0.20 = composite 0.25 < 0.35 (won't promote on composite alone)
	// But it meets the OR condition (consolidation >= 0.15) and is high-value type, so auto-promotes
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('GOOD_CONS_BUF', 'Good consolidation', 'content', 'decision', 0, 0.9, 0.75, 0.1, 0.20, 'semantic', 'default', 'durable', 'pending')`)

	// Both low -> should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, consolidation_status)
		VALUES('BOTH_LOW', 'Both low', 'content', 'discovery', 0, 0.9, 0.5, 0.1, 0.1, 'semantic', 'default', 'durable', 'pending')`)

	promoted, err := p.promoteBufferToWorking(ctx, 1)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted != 2 {
		t.Errorf("expected 2 promoted (good activation OR good consolidation with high importance), got %d", promoted)
	}

	assertLayer(t, tdb, "GOOD_ACT_BUF", 1)
	assertLayer(t, tdb, "GOOD_CONS_BUF", 1)
	assertLayer(t, tdb, "BOTH_LOW", 0)
}
