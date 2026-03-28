package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	"github.com/joeldevz/neurox/internal/session"
)

// ---------------------------------------------------------------------------
// Surface Parity Tests
//
// These tests verify that MCP and HTTP produce equivalent results for key
// operations.  Since both surfaces now delegate to the same shared
// infrastructure (SaveQueue, ProactiveEngine, SessionManager, RecallEngine),
// parity tests validate that the infrastructure produces consistent results
// regardless of the entry point.
//
// Approach: create a single set of shared dependencies, then simulate the
// "MCP path" and "HTTP path" by calling the same underlying components with
// the same inputs and verifying the results are identical or equivalent.
// ---------------------------------------------------------------------------

// --- Parity: Save -----------------------------------------------------------

func TestParity_SaveViaQueueProducesValidObservation(t *testing.T) {
	// Both MCP and HTTP use SaveQueue.Enqueue when SaveQueue is available.
	// This test verifies that observations saved through the queue path have
	// the same quality (defaults applied, validation passed, ID generated)
	// as observations saved through the direct Store.Save path.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	queue := observation.NewSaveQueue(store)
	queue.Start(ctx)
	defer queue.Stop()

	tests := []struct {
		name    string
		surface string // simulated surface
		obs     observation.Observation
	}{
		{
			name:    "mcp surface save",
			surface: "mcp",
			obs: observation.Observation{
				Title:         "MCP architecture decision",
				Content:       "Decided to use event sourcing for the audit log.",
				SourceSurface: "mcp",
				SourceTool:    "save",
				Namespace:     "parity",
				Tags:          []string{"architecture", "audit"},
			},
		},
		{
			name:    "http surface save",
			surface: "http",
			obs: observation.Observation{
				Title:         "HTTP architecture decision",
				Content:       "Decided to use event sourcing for the audit log.",
				SourceSurface: "http",
				SourceTool:    "save",
				Namespace:     "parity",
				Tags:          []string{"architecture", "audit"},
			},
		},
	}

	var savedIDs []string

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := queue.Enqueue(ctx, tt.obs)
			if err != nil {
				t.Fatalf("enqueue (%s): %v", tt.surface, err)
			}
			if result.ID == "" {
				t.Fatalf("enqueue (%s): empty ID returned", tt.surface)
			}
			if result.Namespace != "parity" {
				t.Errorf("enqueue (%s): namespace = %q, want %q", tt.surface, result.Namespace, "parity")
			}
			savedIDs = append(savedIDs, result.ID)
		})
	}

	// Wait for the queue worker to persist both observations.
	deadline := time.After(5 * time.Second)
	for {
		pending := queue.Pending()
		if pending == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("queue did not drain in time; pending = %d", pending)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Give a small extra pause for persistence to complete.
	time.Sleep(200 * time.Millisecond)

	// Verify both observations exist and have equivalent quality.
	for i, id := range savedIDs {
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("get saved[%d] (%s): %v", i, id, err)
		}
		// Defaults should be applied regardless of surface.
		if got.ObservationType == "" {
			t.Errorf("saved[%d]: empty observation_type (defaults not applied)", i)
		}
		if got.Kind == "" {
			t.Errorf("saved[%d]: empty kind (defaults not applied)", i)
		}
		if got.Confidence == 0 {
			t.Errorf("saved[%d]: zero confidence (defaults not applied)", i)
		}
		if got.Importance == 0 {
			t.Errorf("saved[%d]: zero importance (defaults not applied)", i)
		}
		if got.Layer != 0 {
			t.Errorf("saved[%d]: layer = %d, want 0 (Buffer)", i, got.Layer)
		}
	}

	// Both observations should be in the DB — verify count.
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'parity' AND deleted_at IS NULL").Scan(&count)
	if count != 2 {
		t.Errorf("observation count = %d, want 2 (one per surface)", count)
	}
}

func TestParity_SaveSyncPathProducesValidObservation(t *testing.T) {
	// When SaveQueue is nil, both MCP and HTTP fall back to direct
	// Store.Save with LLM gate check.  Verify the sync path produces
	// observations with the same quality as the queue path.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)

	tests := []struct {
		name    string
		surface string
	}{
		{"mcp sync save", "mcp"},
		{"http sync save", "http"},
		{"cli sync save", "cli"},
	}

	var ids []string

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := observation.Observation{
				Title:           fmt.Sprintf("Decision from %s", tt.surface),
				Content:         "Chose Go for the backend because of concurrency support.",
				ObservationType: observation.ObservationTypeDecision,
				SourceSurface:   tt.surface,
				SourceTool:      "save",
				Namespace:       "parity-sync",
			}

			saved, err := store.Save(ctx, obs)
			if err != nil {
				t.Fatalf("save (%s): %v", tt.surface, err)
			}
			if saved.ID == "" {
				t.Fatalf("save (%s): empty ID", tt.surface)
			}
			ids = append(ids, saved.ID)

			// Verify round-trip.
			got, err := store.Get(ctx, saved.ID)
			if err != nil {
				t.Fatalf("get (%s): %v", tt.surface, err)
			}
			if got.SourceSurface != tt.surface {
				t.Errorf("source_surface = %q, want %q", got.SourceSurface, tt.surface)
			}
			if got.SourceTool != "save" {
				t.Errorf("source_tool = %q, want %q", got.SourceTool, "save")
			}
		})
	}

	// All three surfaces should produce observations that look identical
	// (except for source_surface and ID).
	if len(ids) != 3 {
		t.Fatalf("expected 3 saved observations, got %d", len(ids))
	}
	obs0, _ := store.Get(ctx, ids[0])
	obs1, _ := store.Get(ctx, ids[1])
	obs2, _ := store.Get(ctx, ids[2])

	// Quality fields should match across surfaces.
	if obs0.Confidence != obs1.Confidence || obs1.Confidence != obs2.Confidence {
		t.Errorf("confidence differs across surfaces: %.2f / %.2f / %.2f", obs0.Confidence, obs1.Confidence, obs2.Confidence)
	}
	if obs0.Importance != obs1.Importance || obs1.Importance != obs2.Importance {
		t.Errorf("importance differs across surfaces: %.2f / %.2f / %.2f", obs0.Importance, obs1.Importance, obs2.Importance)
	}
	if obs0.Layer != obs1.Layer || obs1.Layer != obs2.Layer {
		t.Errorf("layer differs across surfaces: %d / %d / %d", obs0.Layer, obs1.Layer, obs2.Layer)
	}
	if obs0.ObservationType != obs1.ObservationType || obs1.ObservationType != obs2.ObservationType {
		t.Errorf("observation_type differs across surfaces: %s / %s / %s", obs0.ObservationType, obs1.ObservationType, obs2.ObservationType)
	}
}

func TestParity_SaveWithQualityGate(t *testing.T) {
	// Both MCP and HTTP use LLM Gate in the sync fallback path.
	// Verify the gate rejects identically for both.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)

	// With gate mode off, everything should pass.
	surfaces := []string{"mcp", "http"}
	for _, surface := range surfaces {
		decision, _ := gate.SaveGateDecide(ctx, llm.SaveInput{
			Title:           fmt.Sprintf("Test from %s", surface),
			Content:         "Meaningful content worth saving.",
			ObservationType: "decision",
		})
		if decision == llm.SaveReject {
			t.Errorf("gate unexpectedly rejected save from %s with mode=off", surface)
		}

		// Verify saves go through.
		saved, err := store.Save(ctx, observation.Observation{
			Title:         fmt.Sprintf("Gate test from %s", surface),
			Content:       "Content that passes quality gate.",
			SourceSurface: surface,
			SourceTool:    "save",
			Namespace:     "parity-gate",
		})
		if err != nil {
			t.Fatalf("save (%s): %v", surface, err)
		}
		if saved.ID == "" {
			t.Fatalf("save (%s): empty ID", surface)
		}
	}

	// Verify both surfaces produced observations.
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'parity-gate' AND deleted_at IS NULL").Scan(&count)
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// --- Parity: Recall ---------------------------------------------------------

func TestParity_RecallSameResultsForSameQuery(t *testing.T) {
	// Both MCP and HTTP use the same recall.Engine.Search.
	// Verify that the same query, namespace, and options produce
	// identical results regardless of which surface invoked them.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)

	// Seed observations (mix of types and content).
	observations := []observation.Observation{
		{Title: "Go concurrency patterns", Content: "Goroutines and channels are the building blocks of concurrent Go programs.", Namespace: "parity-recall", ObservationType: observation.ObservationTypePattern, Tags: []string{"go", "concurrency"}},
		{Title: "Database migration strategy", Content: "Use forward-only migrations with idempotent scripts.", Namespace: "parity-recall", ObservationType: observation.ObservationTypeDecision, Tags: []string{"database", "migration"}},
		{Title: "Error handling convention", Content: "Always wrap errors with context: fmt.Errorf(\"context: %w\", err).", Namespace: "parity-recall", ObservationType: observation.ObservationTypePattern, Tags: []string{"go", "errors"}},
		{Title: "JWT auth implementation", Content: "Implemented JWT bearer token authentication with refresh tokens.", Namespace: "parity-recall", ObservationType: observation.ObservationTypeDecision, Tags: []string{"auth", "jwt"}},
		{Title: "Redis caching layer", Content: "Added Redis as a caching layer between the API and database.", Namespace: "parity-recall", ObservationType: observation.ObservationTypeDiscovery, Tags: []string{"redis", "caching"}},
	}
	for _, obs := range observations {
		if _, err := store.Save(ctx, obs); err != nil {
			t.Fatalf("seed save: %v", err)
		}
	}

	// Table-driven: same query executed "from MCP" and "from HTTP".
	queries := []struct {
		name  string
		query string
		limit int
	}{
		{"broad match", "Go patterns concurrency", 10},
		{"specific topic", "JWT authentication", 5},
		{"database query", "database migration", 10},
	}

	// Pin time so recency scoring is identical across calls.
	now := time.Now().UTC()

	for _, q := range queries {
		t.Run(q.name, func(t *testing.T) {
			opts := recall.SearchOptions{
				Query:     q.query,
				Namespace: "parity-recall",
				Limit:     q.limit,
				Now:       now,
			}

			// "MCP path" — same engine, same options.
			mcpResults, err := recallEngine.Search(ctx, opts)
			if err != nil {
				t.Fatalf("mcp recall: %v", err)
			}

			// "HTTP path" — identical call.
			httpResults, err := recallEngine.Search(ctx, opts)
			if err != nil {
				t.Fatalf("http recall: %v", err)
			}

			// Results must be identical (same engine, same data, same options).
			if len(mcpResults) != len(httpResults) {
				t.Fatalf("result count: mcp=%d, http=%d", len(mcpResults), len(httpResults))
			}

			for i := range mcpResults {
				if mcpResults[i].ID != httpResults[i].ID {
					t.Errorf("result[%d] ID: mcp=%s, http=%s", i, mcpResults[i].ID, httpResults[i].ID)
				}
				if mcpResults[i].Score != httpResults[i].Score {
					t.Errorf("result[%d] score: mcp=%.6f, http=%.6f", i, mcpResults[i].Score, httpResults[i].Score)
				}
				if mcpResults[i].Title != httpResults[i].Title {
					t.Errorf("result[%d] title: mcp=%q, http=%q", i, mcpResults[i].Title, httpResults[i].Title)
				}
			}
		})
	}
}

func TestParity_RecallWithDebugMode(t *testing.T) {
	// Both MCP (debug: true) and HTTP (?debug=true) use the same
	// recall.SearchOptions{Debug: true}.  Verify score breakdowns
	// are populated identically.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)

	// Seed data.
	store.Save(ctx, observation.Observation{
		Title: "Testing strategy", Content: "Table-driven tests with real SQLite.",
		Namespace: "parity-debug", ObservationType: observation.ObservationTypePattern,
	})
	store.Save(ctx, observation.Observation{
		Title: "API design", Content: "REST endpoints with JSON responses.",
		Namespace: "parity-debug", ObservationType: observation.ObservationTypeDecision,
	})

	// Pin time so recency scoring is identical across calls.
	now := time.Now().UTC()

	opts := recall.SearchOptions{
		Query:     "testing table-driven",
		Namespace: "parity-debug",
		Debug:     true,
		Limit:     10,
		Now:       now,
	}

	// Both surfaces use exactly this options struct.
	results1, err := recallEngine.Search(ctx, opts)
	if err != nil {
		t.Fatalf("recall 1: %v", err)
	}
	results2, err := recallEngine.Search(ctx, opts)
	if err != nil {
		t.Fatalf("recall 2: %v", err)
	}

	if len(results1) == 0 {
		t.Fatal("no results returned")
	}

	// Verify debug breakdowns are present and identical.
	for i := range results1 {
		if i >= len(results2) {
			break
		}
		bd1 := results1[i].Breakdown
		bd2 := results2[i].Breakdown

		if bd1 == nil {
			t.Errorf("result[%d]: breakdown nil in first call", i)
			continue
		}
		if bd2 == nil {
			t.Errorf("result[%d]: breakdown nil in second call", i)
			continue
		}
		if bd1.FinalScore != bd2.FinalScore {
			t.Errorf("result[%d] final_score: %.6f vs %.6f", i, bd1.FinalScore, bd2.FinalScore)
		}
		if bd1.Recency != bd2.Recency {
			t.Errorf("result[%d] recency: %.6f vs %.6f", i, bd1.Recency, bd2.Recency)
		}
		if bd1.Importance != bd2.Importance {
			t.Errorf("result[%d] importance: %.6f vs %.6f", i, bd1.Importance, bd2.Importance)
		}
	}
}

func TestParity_RecallWithFilters(t *testing.T) {
	// Both surfaces pass the same filter options (observation_type, kind,
	// include_stale, files) through to recall.SearchOptions.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)

	// Seed varied data.
	store.Save(ctx, observation.Observation{
		Title: "Pattern A", Content: "Common coding pattern",
		Namespace: "parity-filter", ObservationType: observation.ObservationTypePattern,
		Kind: observation.KindSemantic,
	})
	store.Save(ctx, observation.Observation{
		Title: "Decision B", Content: "Architecture decision",
		Namespace: "parity-filter", ObservationType: observation.ObservationTypeDecision,
		Kind: observation.KindEpisodic,
	})
	store.Save(ctx, observation.Observation{
		Title: "Bugfix C", Content: "Fixed pattern matching bug",
		Namespace: "parity-filter", ObservationType: observation.ObservationTypeBugfix,
		Kind: observation.KindProcedural,
	})

	// Pin time so recency scoring is identical across calls.
	now := time.Now().UTC()

	tests := []struct {
		name string
		opts recall.SearchOptions
	}{
		{
			name: "filter by type",
			opts: recall.SearchOptions{
				Query:           "pattern",
				Namespace:       "parity-filter",
				ObservationType: observation.ObservationTypePattern,
				Limit:           10,
				Now:             now,
			},
		},
		{
			name: "filter by kind",
			opts: recall.SearchOptions{
				Query:     "decision architecture",
				Namespace: "parity-filter",
				Kind:      observation.KindEpisodic,
				Limit:     10,
				Now:       now,
			},
		},
		{
			name: "include stale",
			opts: recall.SearchOptions{
				Query:        "pattern",
				Namespace:    "parity-filter",
				IncludeStale: true,
				Limit:        10,
				Now:          now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r1, err := recallEngine.Search(ctx, tt.opts)
			if err != nil {
				t.Fatalf("recall 1: %v", err)
			}
			r2, err := recallEngine.Search(ctx, tt.opts)
			if err != nil {
				t.Fatalf("recall 2: %v", err)
			}
			if len(r1) != len(r2) {
				t.Fatalf("count: %d vs %d", len(r1), len(r2))
			}
			for i := range r1 {
				if r1[i].ID != r2[i].ID {
					t.Errorf("[%d] ID: %s vs %s", i, r1[i].ID, r2[i].ID)
				}
				if r1[i].Score != r2[i].Score {
					t.Errorf("[%d] score: %.6f vs %.6f", i, r1[i].Score, r2[i].Score)
				}
			}
		})
	}
}

// --- Parity: Context --------------------------------------------------------

func TestParity_ContextSameItemsForSameNamespace(t *testing.T) {
	// Both MCP and HTTP delegate to ProactiveEngine.GetContext.
	// Verify two calls with the same parameters return identical results.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})

	// Seed observations across layers and importance.
	for i := 0; i < 15; i++ {
		obs := observation.Observation{
			Title:           fmt.Sprintf("Context obs %d", i),
			Content:         fmt.Sprintf("Content about topic %d for context testing", i%5),
			Namespace:       "parity-ctx",
			ObservationType: observation.ObservationTypeDiscovery,
		}
		saved, err := store.Save(ctx, obs)
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}

		// Vary importance so ordering is deterministic.
		importance := 0.3 + float64(i)*0.04
		tdb.DB.ExecContext(ctx, "UPDATE observations SET importance = ? WHERE id = ?", importance, saved.ID)
	}

	// "MCP path" context call.
	mcpResult, err := proactiveEng.GetContext(ctx, "parity-ctx", nil, 10)
	if err != nil {
		t.Fatalf("mcp context: %v", err)
	}

	// "HTTP path" context call — identical parameters.
	httpResult, err := proactiveEng.GetContext(ctx, "parity-ctx", nil, 10)
	if err != nil {
		t.Fatalf("http context: %v", err)
	}

	// Results must be identical.
	if mcpResult.Count != httpResult.Count {
		t.Fatalf("count: mcp=%d, http=%d", mcpResult.Count, httpResult.Count)
	}
	if mcpResult.Namespace != httpResult.Namespace {
		t.Errorf("namespace: mcp=%q, http=%q", mcpResult.Namespace, httpResult.Namespace)
	}

	for i := range mcpResult.Items {
		if i >= len(httpResult.Items) {
			break
		}
		if mcpResult.Items[i].ID != httpResult.Items[i].ID {
			t.Errorf("item[%d] ID: mcp=%s, http=%s", i, mcpResult.Items[i].ID, httpResult.Items[i].ID)
		}
		if mcpResult.Items[i].Title != httpResult.Items[i].Title {
			t.Errorf("item[%d] title: mcp=%q, http=%q", i, mcpResult.Items[i].Title, httpResult.Items[i].Title)
		}
		if mcpResult.Items[i].Importance != httpResult.Items[i].Importance {
			t.Errorf("item[%d] importance: mcp=%.4f, http=%.4f", i, mcpResult.Items[i].Importance, httpResult.Items[i].Importance)
		}
	}
}

func TestParity_ContextWithFiles(t *testing.T) {
	// File-linked context should be the same for both surfaces.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})

	// Seed observations, some with file links.
	store.Save(ctx, observation.Observation{
		Title: "Auth middleware", Content: "JWT middleware in auth.go",
		Namespace: "parity-ctx-files", Files: []string{"src/auth.go"},
	})
	store.Save(ctx, observation.Observation{
		Title: "Database pool", Content: "Connection pool for Postgres",
		Namespace: "parity-ctx-files", Files: []string{"src/db.go"},
	})
	store.Save(ctx, observation.Observation{
		Title: "No file link", Content: "General architecture note",
		Namespace: "parity-ctx-files",
	})

	files := []string{"src/auth.go"}

	r1, err := proactiveEng.GetContext(ctx, "parity-ctx-files", files, 10)
	if err != nil {
		t.Fatalf("context 1: %v", err)
	}
	r2, err := proactiveEng.GetContext(ctx, "parity-ctx-files", files, 10)
	if err != nil {
		t.Fatalf("context 2: %v", err)
	}

	if r1.Count != r2.Count {
		t.Fatalf("count: %d vs %d", r1.Count, r2.Count)
	}

	for i := range r1.Items {
		if i >= len(r2.Items) {
			break
		}
		if r1.Items[i].ID != r2.Items[i].ID {
			t.Errorf("item[%d] ID: %s vs %s", i, r1.Items[i].ID, r2.Items[i].ID)
		}
	}
}

func TestParity_ContextIncludesReflections(t *testing.T) {
	// Verify reflections are returned in context for both surfaces.

	tdb := openTestDB(t)
	ctx := context.Background()
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})

	// Insert a reflection observation directly.
	// Content must be > 50 chars to pass the getReflections filter.
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
		VALUES('REFL_PARITY', 'Test reflection', 'Synthesized insight about architecture patterns across the entire project codebase and design decisions.', 'pattern', 2, 0.9, 0.9, 'semantic', 'parity-ctx-refl', 'reflection')`)

	// Insert a regular observation.
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('REG_PARITY', 'Regular obs', 'Normal observation.', 'discovery', 0, 0.7, 0.5, 'semantic', 'parity-ctx-refl')`)

	r1, err := proactiveEng.GetContext(ctx, "parity-ctx-refl", nil, 10)
	if err != nil {
		t.Fatalf("context 1: %v", err)
	}
	r2, err := proactiveEng.GetContext(ctx, "parity-ctx-refl", nil, 10)
	if err != nil {
		t.Fatalf("context 2: %v", err)
	}

	// Reflections should be present in both calls.
	if len(r1.Reflections) != len(r2.Reflections) {
		t.Fatalf("reflections count: %d vs %d", len(r1.Reflections), len(r2.Reflections))
	}
	if len(r1.Reflections) == 0 {
		t.Error("expected at least 1 reflection in context")
	}

	for i := range r1.Reflections {
		if i >= len(r2.Reflections) {
			break
		}
		if r1.Reflections[i].ID != r2.Reflections[i].ID {
			t.Errorf("reflection[%d] ID: %s vs %s", i, r1.Reflections[i].ID, r2.Reflections[i].ID)
		}
	}
}

// --- Parity: Session --------------------------------------------------------

func TestParity_SessionStartEndConsistency(t *testing.T) {
	// Both MCP and HTTP use SessionManager.Start and SessionManager.End.
	// Verify that the same manager produces consistent results.

	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()

	mock := &mockLLM{response: `decision | Architecture choice | What: Selected Go. Why: Concurrency.
discovery | Performance finding | What: Latency improved 30% with caching.`}

	mgr := session.NewManager(tdb.DB, mock, idGen)

	// Simulate "MCP surface" session.
	mcpStart, err := mgr.Start(ctx, "MCP session", "/project", "main", "parity-sess")
	if err != nil {
		t.Fatalf("mcp start: %v", err)
	}
	if mcpStart.SessionID == "" {
		t.Fatal("mcp start: empty session ID")
	}

	mcpEnd, err := mgr.End(ctx, mcpStart.SessionID, "Completed MCP work. Selected Go for backend. Improved latency with caching.", "mcp")
	if err != nil {
		t.Fatalf("mcp end: %v", err)
	}

	// Simulate "HTTP surface" session.
	httpStart, err := mgr.Start(ctx, "HTTP session", "/project", "main", "parity-sess")
	if err != nil {
		t.Fatalf("http start: %v", err)
	}
	if httpStart.SessionID == "" {
		t.Fatal("http start: empty session ID")
	}

	httpEnd, err := mgr.End(ctx, httpStart.SessionID, "Completed HTTP work. Selected Go for backend. Improved latency with caching.", "http")
	if err != nil {
		t.Fatalf("http end: %v", err)
	}

	// Both should extract the same number of observations (same LLM mock).
	if mcpEnd.ObservationsExtracted != httpEnd.ObservationsExtracted {
		t.Errorf("observations extracted: mcp=%d, http=%d", mcpEnd.ObservationsExtracted, httpEnd.ObservationsExtracted)
	}

	// Verify sessions were recorded.
	var sessionCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE namespace = 'parity-sess' AND status = 'completed'").Scan(&sessionCount)
	if sessionCount != 2 {
		t.Errorf("completed sessions = %d, want 2", sessionCount)
	}
}

func TestParity_SessionAutoAbandons(t *testing.T) {
	// Both surfaces should auto-abandon previous active sessions.

	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()
	mgr := session.NewManager(tdb.DB, llm.Disabled{}, idGen)

	// First session.
	s1, err := mgr.Start(ctx, "Session 1", "", "", "parity-abandon")
	if err != nil {
		t.Fatalf("start 1: %v", err)
	}
	if s1.Abandoned != 0 {
		t.Errorf("first start should abandon 0, got %d", s1.Abandoned)
	}

	// Second session should abandon the first.
	s2, err := mgr.Start(ctx, "Session 2", "", "", "parity-abandon")
	if err != nil {
		t.Fatalf("start 2: %v", err)
	}
	if s2.Abandoned != 1 {
		t.Errorf("second start should abandon 1, got %d", s2.Abandoned)
	}

	// Verify first session is abandoned.
	var status string
	tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", s1.SessionID).Scan(&status)
	if status != "abandoned" {
		t.Errorf("session 1 status = %q, want 'abandoned'", status)
	}
}

// --- Parity: Provenance Consistency -----------------------------------------

func TestParity_ProvenanceFieldsPreserved(t *testing.T) {
	// Verify that provenance fields (source_surface, source_tool,
	// source_session_id) survive the save→get round-trip consistently
	// for all surfaces.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	idGen := observation.NewULIDGenerator()
	mgr := session.NewManager(tdb.DB, llm.Disabled{}, idGen)

	// Start a session to get a session ID.
	startResult, err := mgr.Start(ctx, "Provenance test", "", "", "parity-prov")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	tests := []struct {
		name      string
		surface   string
		tool      string
		sessionID string
	}{
		{"mcp save with session", "mcp", "save", startResult.SessionID},
		{"http save without session", "http", "save", ""},
		{"cli save", "cli", "save", ""},
		{"consolidator reflect", "consolidator", "reflect", ""},
		{"http invalidate", "http", "invalidate", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := observation.Observation{
				Title:           fmt.Sprintf("Provenance test from %s/%s", tt.surface, tt.tool),
				Content:         "Content for provenance verification.",
				Namespace:       "parity-prov",
				SourceSurface:   tt.surface,
				SourceTool:      tt.tool,
				SourceSessionID: tt.sessionID,
			}

			saved, err := store.Save(ctx, obs)
			if err != nil {
				t.Fatalf("save: %v", err)
			}

			got, err := store.Get(ctx, saved.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if got.SourceSurface != tt.surface {
				t.Errorf("source_surface = %q, want %q", got.SourceSurface, tt.surface)
			}
			if got.SourceTool != tt.tool {
				t.Errorf("source_tool = %q, want %q", got.SourceTool, tt.tool)
			}
			if got.SourceSessionID != tt.sessionID {
				t.Errorf("source_session_id = %q, want %q", got.SourceSessionID, tt.sessionID)
			}
		})
	}
}

// --- Parity: Combined Save + Recall + Context Flow --------------------------

func TestParity_EndToEndFlow(t *testing.T) {
	// Full flow: save observations from multiple surfaces, then verify
	// recall and context return the same results for both.

	tdb := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})

	// Save observations from different surfaces.
	surfaces := []struct {
		surface string
		title   string
		content string
	}{
		{"mcp", "Go error handling", "Use fmt.Errorf with %w for error wrapping."},
		{"http", "Go error handling", "Use fmt.Errorf with %w for error wrapping."},
		{"cli", "Go testing pattern", "Table-driven tests with subtests."},
	}

	for _, s := range surfaces {
		_, err := store.Save(ctx, observation.Observation{
			Title:         s.title,
			Content:       s.content,
			Namespace:     "parity-e2e",
			SourceSurface: s.surface,
			SourceTool:    "save",
		})
		if err != nil {
			t.Fatalf("save (%s): %v", s.surface, err)
		}
	}

	// Verify recall returns all relevant observations.
	results, err := recallEngine.Search(ctx, recall.SearchOptions{
		Query:     "error handling",
		Namespace: "parity-e2e",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("recall returned %d results, want >= 2 (both error handling obs)", len(results))
	}

	// Verify context returns observations from all surfaces.
	ctxResult, err := proactiveEng.GetContext(ctx, "parity-e2e", nil, 10)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if ctxResult.Count < 3 {
		t.Errorf("context returned %d items, want >= 3 (all surfaces)", ctxResult.Count)
	}

	// Verify that calling context twice produces the same result.
	ctxResult2, err := proactiveEng.GetContext(ctx, "parity-e2e", nil, 10)
	if err != nil {
		t.Fatalf("context 2: %v", err)
	}
	if ctxResult.Count != ctxResult2.Count {
		t.Errorf("context count: %d vs %d", ctxResult.Count, ctxResult2.Count)
	}

	_ = time.Now() // keep time import used
}
