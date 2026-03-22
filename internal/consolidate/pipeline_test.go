package consolidate

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"neurox/internal/db"
	"neurox/internal/decay"
	"neurox/internal/embed"
	"neurox/internal/links"
	"neurox/internal/llm"
	"neurox/internal/observation"
	"neurox/internal/temporal"
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
	p := NewPipeline(database, decayEngine, embed.Disabled{}, nil, gate, linkStore, llm.Disabled{}, idGen, Config{})
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
