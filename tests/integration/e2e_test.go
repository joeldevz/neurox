package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/consolidate"
	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/session"
)

// mockLLM returns canned responses for integration tests.
type mockLLM struct {
	response string
}

func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.response, nil
}
func (m *mockLLM) Name() string { return "mock" }

func openTestDB(t *testing.T) *db.TestDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &db.TestDB{DB: database}
}

// --- E2E: Save + Consolidation + Recall ---

func TestE2E_SaveConsolidateRecall(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()

	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)
	linkStore := links.NewStore(tdb.DB, idGen)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	decayEngine := decay.NewEngine(tdb.DB)
	pipeline := consolidate.NewPipeline(tdb.DB, decayEngine, embed.Disabled{}, nil, gate, linkStore, llm.Disabled{}, llm.Disabled{}, idGen, consolidate.Config{})

	// Save 100 observations with varying importance
	for i := 0; i < 100; i++ {
		importance := 0.1 + float64(i%10)*0.09
		obs := observation.Observation{
			Title:           fmt.Sprintf("Observation %d", i),
			Content:         fmt.Sprintf("Content for observation %d about topic %d", i, i%10),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Confidence:      0.7,
			Namespace:       "e2e",
		}
		if importance >= 0.5 {
			obs.ObservationType = observation.ObservationTypeDecision
		}
		saved, err := store.Save(ctx, obs)
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}

		// Manually set importance (store defaults to 0.5)
		tdb.DB.ExecContext(ctx, "UPDATE observations SET importance = ? WHERE id = ?", importance, saved.ID)
	}

	// Verify all in Buffer
	var bufferCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE layer = 0 AND deleted_at IS NULL").Scan(&bufferCount)
	if bufferCount != 100 {
		t.Fatalf("buffer count = %d, want 100", bufferCount)
	}

	// Run consolidation
	if err := pipeline.Run(ctx); err != nil {
		t.Fatalf("consolidation: %v", err)
	}

	// Verify promotions happened
	var workingCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE layer = 1 AND deleted_at IS NULL").Scan(&workingCount)
	if workingCount == 0 {
		t.Error("expected some observations promoted to Working")
	}

	// Verify recall returns ranked results
	results, err := recallEngine.Search(ctx, recall.SearchOptions{
		Query:     "observation topic",
		Namespace: "e2e",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) == 0 {
		t.Error("recall returned no results")
	}

	// Verify consolidation run was recorded
	var runCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM consolidation_runs WHERE status = 'completed'").Scan(&runCount)
	if runCount == 0 {
		t.Error("expected completed consolidation run")
	}
}

// --- E2E: Topic Key Upsert ---

func TestE2E_TopicKeyUpsert(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()

	store := observation.NewStore(tdb.DB, nil)

	// Save with topic_key
	first, err := store.Save(ctx, observation.Observation{
		Title:     "Go version",
		Content:   "Using Go 1.21",
		TopicKey:  "go-version",
		Namespace: "proj",
	})
	if err != nil {
		t.Fatalf("save first: %v", err)
	}

	// Save again with same topic_key → should update, not duplicate
	second, err := store.Save(ctx, observation.Observation{
		Title:     "Go version",
		Content:   "Upgraded to Go 1.22",
		TopicKey:  "go-version",
		Namespace: "proj",
	})
	if err != nil {
		t.Fatalf("save second: %v", err)
	}

	// Should be same ID
	if second.ID != first.ID {
		t.Errorf("expected same ID (upsert), got first=%s second=%s", first.ID, second.ID)
	}

	// Content should be updated
	got, err := store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "Upgraded to Go 1.22" {
		t.Errorf("content = %q, want updated content", got.Content)
	}

	// Should be exactly 1 observation
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'proj' AND deleted_at IS NULL").Scan(&count)
	if count != 1 {
		t.Errorf("count = %d, want 1 (no duplicates)", count)
	}
}

// --- E2E: Invalidate with Replacement ---

func TestE2E_InvalidateWithReplacement(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()

	idGen := observation.NewULIDGenerator()
	store := observation.NewStore(tdb.DB, nil)
	linkStore := links.NewStore(tdb.DB, idGen)
	recallEngine := recall.NewEngine(tdb.DB)

	// Save original
	original, err := store.Save(ctx, observation.Observation{
		Title:     "DB is Postgres 14",
		Content:   "We use Postgres 14",
		Namespace: "proj",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Invalidate with replacement
	result, err := store.Invalidate(ctx, linkStore, observation.InvalidateInput{
		ObservationID:      original.ID,
		Reason:             "Migrated to Postgres 16",
		ReplacementTitle:   "DB is Postgres 16",
		ReplacementContent: "Migrated to Postgres 16",
	})
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}

	if result.ReplacementID == "" {
		t.Fatal("expected replacement ID")
	}

	// Verify original is stale
	got, _ := store.Get(ctx, original.ID)
	if got.Confidence >= 0.7 {
		t.Errorf("original confidence = %.2f, should be reduced", got.Confidence)
	}

	// Verify supersedes link
	linkList, err := linkStore.GetBySource(ctx, result.ReplacementID, links.RelationSupersedes)
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(linkList) == 0 {
		t.Error("expected supersedes link")
	}

	// Verify recall excludes the stale original
	results, err := recallEngine.Search(ctx, recall.SearchOptions{
		Query:     "Postgres",
		Namespace: "proj",
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	for _, r := range results {
		if r.ID == original.ID && r.Staleness == "expired" {
			t.Error("recall should exclude expired observations by default")
		}
	}
}

// --- E2E: File-linked staleness via git hook ---

func TestE2E_GitHookStaleness(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()

	store := observation.NewStore(tdb.DB, nil)

	// Save observation linked to files
	saved, err := store.Save(ctx, observation.Observation{
		Title:     "Auth middleware pattern",
		Content:   "Using JWT middleware in auth.go",
		Namespace: "proj",
		Files:     []string{"src/auth.go", "src/middleware.go"},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file links created
	var fileCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM file_observations WHERE observation_id = ?", saved.ID).Scan(&fileCount)
	if fileCount != 2 {
		t.Errorf("file links = %d, want 2", fileCount)
	}

	// Simulate git hook: mark files as changed
	commitSha := "abc123"
	tdb.DB.ExecContext(ctx, `
		UPDATE file_observations SET valid_until = datetime('now'), commit_sha_until = ?
		WHERE file_path IN ('src/auth.go') AND valid_until IS NULL
	`, commitSha)

	tdb.DB.ExecContext(ctx, `
		UPDATE observations
		SET staleness = 'stale', confidence = MAX(0.01, confidence * 0.5), updated_at = datetime('now')
		WHERE deleted_at IS NULL AND staleness = 'fresh'
		  AND id IN (SELECT DISTINCT observation_id FROM file_observations WHERE file_path = 'src/auth.go')
	`)

	// Verify staleness
	var staleness string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness FROM observations WHERE id = ?", saved.ID).Scan(&staleness)
	if staleness != "stale" {
		t.Errorf("staleness = %q, want 'stale'", staleness)
	}
}

// --- E2E: Reflection ---

func TestE2E_Reflection(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(tdb.DB, idGen)

	mock := &mockLLM{response: "1. The project consistently uses JWT for authentication.\n2. Redis is the preferred caching layer.\n3. Database migrations follow a forward-only pattern."}
	engine := reflectpkg.NewEngine(tdb.DB, mock, linkStore, idGen)

	// Insert 25 Working observations
	for i := 0; i < 25; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, ?, ?, 'discovery', 1, 0.7, 0.5, 'semantic', 'proj')`,
			fmt.Sprintf("REFL%04d", i), fmt.Sprintf("Obs %d", i), fmt.Sprintf("Content about topic %d", i%5))
	}

	result, err := engine.Run(ctx, "proj")
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Errorf("reflections = %d, want 1", result.ReflectionsCreated)
	}

	// Verify reflection observation in Core
	var coreCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE source = 'reflection' AND layer = 2 AND namespace = 'proj'").Scan(&coreCount)
	if coreCount != 1 {
		t.Errorf("core reflections = %d, want 1", coreCount)
	}

	// Verify derived_from links
	var linkCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links WHERE relation_type = 'derived_from'").Scan(&linkCount)
	if linkCount == 0 {
		t.Error("expected derived_from links")
	}

	// Second run should not create duplicates (sources already linked)
	result2, err := engine.Run(ctx, "proj")
	if err != nil {
		t.Fatalf("reflect 2: %v", err)
	}
	if result2.ReflectionsCreated != 0 {
		t.Errorf("second run reflections = %d, want 0 (idempotent)", result2.ReflectionsCreated)
	}
}

// --- E2E: Session lifecycle with extraction ---

func TestE2E_SessionLifecycle(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()

	mock := &mockLLM{response: `decision | Chose React for frontend | What: Selected React. Why: Team expertise. Where: Frontend.
discovery | API latency improved with caching | What: Redis cache reduced latency 40%. Where: API gateway.
pattern | Always use transactions for multi-table updates | What: Wrap related updates in tx. Why: Data consistency.`}

	mgr := session.NewManager(tdb.DB, mock, idGen)
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})

	// Insert some pre-existing observations for context
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('EXIST1', 'Existing Core obs', 'Important decision', 'decision', 2, 0.9, 0.9, 'semantic', 'myapp')`)

	// Start session
	startResult, err := mgr.Start(ctx, "Implement auth", "/home/user/myapp", "feature/auth", "myapp")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if startResult.SessionID == "" {
		t.Fatal("expected session ID")
	}

	// Get context (should include existing observations)
	ctxResult, err := proactiveEng.GetContext(ctx, "myapp", nil, 10)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if ctxResult.Count == 0 {
		t.Error("expected context with existing observations")
	}

	// End session with summary
	endResult, err := mgr.End(ctx, startResult.SessionID, "Implemented JWT auth. Chose React for frontend. Added Redis caching.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	// Extraction is async — ObservationsExtracted is -1 to signal background processing
	if endResult.ObservationsExtracted != -1 {
		t.Errorf("extracted = %d, want -1 (async)", endResult.ObservationsExtracted)
	}

	// Wait for background extraction to finish before checking DB
	mgr.WaitBackground()

	// Verify extracted observations exist
	var extractedCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'myapp' AND source = 'consolidator' AND deleted_at IS NULL").Scan(&extractedCount)
	if extractedCount != 3 {
		t.Errorf("extracted in DB = %d, want 3", extractedCount)
	}

	// Verify session is completed
	var status string
	tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", startResult.SessionID).Scan(&status)
	if status != "completed" {
		t.Errorf("session status = %q, want 'completed'", status)
	}
}

// --- E2E: Fact graph ---

func TestE2E_FactGraph(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()

	factStore := facts.NewStore(tdb.DB, idGen)

	// Build a knowledge graph
	factStore.Save(ctx, facts.Fact{Subject: "project", Predicate: "uses_framework", Object: "react", Namespace: "app"})
	factStore.Save(ctx, facts.Fact{Subject: "project", Predicate: "uses_database", Object: "postgres", Namespace: "app"})
	factStore.Save(ctx, facts.Fact{Subject: "postgres", Predicate: "version", Object: "16", Namespace: "app"})
	factStore.Save(ctx, facts.Fact{Subject: "react", Predicate: "version", Object: "18", Namespace: "app"})

	// Search
	results, err := factStore.Search(ctx, "project", "app", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) < 2 {
		t.Errorf("search results = %d, want >= 2", len(results))
	}

	// Traverse from project (depth 2 should reach version facts)
	traversal, err := factStore.Traverse(ctx, "project", "app", 2)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(traversal) < 4 {
		t.Errorf("traversal results = %d, want >= 4", len(traversal))
	}

	// Supersede: update postgres version
	factStore.Save(ctx, facts.Fact{Subject: "postgres", Predicate: "version", Object: "17", Namespace: "app"})

	// Only active version should be visible
	count, _ := factStore.Count(ctx, "app")
	if count != 4 { // react, postgres, react version, pg 17 (pg 16 superseded)
		t.Errorf("active facts = %d, want 4", count)
	}
}

// --- E2E: Degraded mode (no Ollama, no LLM) ---

func TestE2E_DegradedMode(t *testing.T) {
	tdb := openTestDB(t)
	ctx := context.Background()
	idGen := observation.NewULIDGenerator()

	// Everything with Disabled providers
	store := observation.NewStore(tdb.DB, nil)
	recallEngine := recall.NewEngine(tdb.DB)
	linkStore := links.NewStore(tdb.DB, idGen)
	gate := llm.NewGate(llm.Disabled{}, llm.GateModeOff)
	decayEngine := decay.NewEngine(tdb.DB)
	pipeline := consolidate.NewPipeline(tdb.DB, decayEngine, embed.Disabled{}, nil, gate, linkStore, llm.Disabled{}, llm.Disabled{}, idGen, consolidate.Config{})
	proactiveEng := proactive.NewEngine(tdb.DB, embed.Disabled{})
	sessionMgr := session.NewManager(tdb.DB, llm.Disabled{}, idGen)

	// Save observations
	for i := 0; i < 10; i++ {
		_, err := store.Save(ctx, observation.Observation{
			Title:     fmt.Sprintf("Degraded obs %d", i),
			Content:   fmt.Sprintf("Content %d about testing", i),
			Namespace: "degraded",
		})
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	// Consolidation should work (heuristic-only)
	if err := pipeline.Run(ctx); err != nil {
		t.Fatalf("consolidation: %v", err)
	}

	// Recall should work (FTS-only)
	results, err := recallEngine.Search(ctx, recall.SearchOptions{
		Query:     "testing",
		Namespace: "degraded",
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(results) == 0 {
		t.Error("FTS recall should work without embeddings")
	}

	// Context should work
	ctxResult, err := proactiveEng.GetContext(ctx, "degraded", nil, 10)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if ctxResult.Count == 0 {
		t.Error("context should return observations without embeddings")
	}

	// Session should work (no extraction without LLM)
	startResult, _ := sessionMgr.Start(ctx, "Test", "", "", "degraded")
	endResult, err := sessionMgr.End(ctx, startResult.SessionID, "Did some testing", "test")
	if err != nil {
		t.Fatalf("session end: %v", err)
	}
	if endResult.ObservationsExtracted != 0 {
		t.Errorf("should not extract without LLM, got %d", endResult.ObservationsExtracted)
	}

	// Gate should be in off mode
	if gate.Mode() != llm.GateModeOff {
		t.Errorf("gate mode = %q, want 'off'", gate.Mode())
	}

	_ = time.Now() // keep time import used
}
