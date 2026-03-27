package consolidate

import (
	"context"
	"database/sql"
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

	// High importance → should be promoted
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, consolidation_status)
		VALUES('HIGH1', 'Important', 'content', 'decision', 0, 0.9, 0.5, 'semantic', 'default', 'pending')`)

	// Low importance → should NOT be promoted
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, consolidation_status)
		VALUES('LOW1', 'Trivial', 'content', 'discovery', 0, 0.5, 0.1, 'episodic', 'default', 'pending')`)

	// Procedural → auto-promote regardless of importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, consolidation_status)
		VALUES('PROC1', 'Procedure', 'content', 'pattern', 0, 0.5, 0.1, 'procedural', 'default', 'pending')`)

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

	// Old + accessed enough → should promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, created_at)
		VALUES('OLD_ACC', 'Veteran', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'default', 10, datetime('now', '-30 days'))`)

	// Old but not accessed → should NOT promote
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, created_at)
		VALUES('OLD_NO', 'Old unused', 'content', 'discovery', 1, 0.5, 0.3, 'semantic', 'default', 1, datetime('now', '-30 days'))`)

	// Recent + accessed → should NOT promote (too new)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count)
		VALUES('NEW_ACC', 'New active', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'default', 10)`)

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

	// Set up some observations
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, consolidation_status)
		VALUES('B1', 'Buffer high', 'content', 'decision', 0, 0.9, 0.5, 'semantic', 'default', 'pending')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, created_at)
		VALUES('W1', 'Working old', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'default', 10, datetime('now', '-30 days'))`)

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

	// Durable + old + accessed → should promote to Core
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, retention, created_at)
		VALUES('DUR1', 'Durable decision', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'default', 10, 'durable', datetime('now', '-30 days'))`)

	// Operational + old + accessed → should NOT promote to Core (retention blocks it)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, retention, created_at)
		VALUES('OPS1', 'Step 4 progress', 'content', 'discovery', 1, 0.9, 0.8, 'semantic', 'default', 10, 'operational', datetime('now', '-30 days'))`)

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

	// Save one operational observation (step log) in Buffer
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, consolidation_status)
		VALUES('LIFE_OPS', 'Implement Step 5', 'Created migration files', 'discovery', 0, 0.9, 0.8, 'semantic', 'lifecycle', 'operational', 'pending')`)

	// Save one durable observation (real decision) in Buffer
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, consolidation_status)
		VALUES('LIFE_DUR', 'Use SQLite for storage', 'Decided SQLite with WAL mode for single-writer', 'decision', 0, 0.9, 0.8, 'semantic', 'lifecycle', 'durable', 'pending')`)

	// Run normal consolidation (Run)
	err := p.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Both should be promoted to Working (Buffer→Working doesn't filter by retention)
	assertLayer(t, tdb, "LIFE_OPS", 1)
	assertLayer(t, tdb, "LIFE_DUR", 1)

	// Now simulate conditions for Working→Core (set access_count and old created_at)
	tdb.Exec(t, `UPDATE observations SET access_count = 10, created_at = datetime('now', '-30 days') WHERE id IN ('LIFE_OPS', 'LIFE_DUR')`)

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
