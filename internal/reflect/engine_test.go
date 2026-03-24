package reflect

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
)

type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

func (m *mockLLM) Name() string { return "mock" }

func setupTest(t *testing.T) (*Engine, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mock := &mockLLM{response: "1. Insight one: patterns emerge from codebase analysis.\n2. Insight two: architecture follows clean layered design.\n3. Insight three: testing covers all critical paths."}
	engine := NewEngine(database, mock, linkStore, idGen)
	return engine, &db.TestDB{DB: database}
}

func TestRunBelowThreshold(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert only 5 Working observations (below default threshold of 20)
	for i := 0; i < 5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'discovery', 1, 0.7, 0.5, 'semantic', 'myns')`, idFromInt(i))
	}

	result, err := e.Run(ctx, "myns")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ReflectionsCreated != 0 {
		t.Errorf("reflections = %d, want 0 (below threshold)", result.ReflectionsCreated)
	}
}

func TestRunAboveThreshold(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert 25 Working observations (above threshold)
	for i := 0; i < 25; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'discovery', 1, 0.7, 0.5, 'semantic', 'myns')`, idFromInt(i))
	}

	result, err := e.Run(ctx, "myns")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Errorf("reflections = %d, want 1", result.ReflectionsCreated)
	}

	// Verify reflection was saved
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reflections WHERE namespace = 'myns'").Scan(&count)
	if count != 1 {
		t.Errorf("reflections in DB = %d, want 1", count)
	}

	// Verify Core observation was created
	var coreCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE source = 'reflection' AND layer = 2 AND namespace = 'myns' AND deleted_at IS NULL").Scan(&coreCount)
	if coreCount != 1 {
		t.Errorf("core observations = %d, want 1", coreCount)
	}

	// Verify derived_from links were created
	var linkCount int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links WHERE relation_type = 'derived_from'").Scan(&linkCount)
	if linkCount == 0 {
		t.Error("expected derived_from links to be created")
	}
}

func TestForceReflect(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert just 5 observations — below auto threshold but ForceReflect should work
	for i := 0; i < 5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'proj')`, idFromInt(100+i))
	}

	result, err := e.ForceReflect(ctx, "proj")
	if err != nil {
		t.Fatalf("force reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Errorf("reflections = %d, want 1", result.ReflectionsCreated)
	}
}

func TestForceReflectTooFewObservations(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Only 2 observations
	for i := 0; i < 2; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'proj')`, idFromInt(200+i))
	}

	_, err := e.ForceReflect(ctx, "proj")
	if err == nil {
		t.Error("expected error for too few observations")
	}
}

func TestRunDisabledLLM(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	engine := NewEngine(database, llm.Disabled{}, linkStore, idGen)

	result, err := engine.Run(context.Background(), "ns")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ReflectionsCreated != 0 {
		t.Errorf("should return 0 reflections with disabled LLM")
	}
}

func TestRunIdempotent(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert 25 observations
	for i := 0; i < 25; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'discovery', 1, 0.7, 0.5, 'semantic', 'ns')`, idFromInt(300+i))
	}

	// First run: should create reflection
	result1, err := e.Run(ctx, "ns")
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if result1.ReflectionsCreated != 1 {
		t.Fatalf("run 1: reflections = %d, want 1", result1.ReflectionsCreated)
	}

	// Second run: sources are now linked via derived_from, so should be < threshold
	result2, err := e.Run(ctx, "ns")
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if result2.ReflectionsCreated != 0 {
		t.Errorf("run 2: reflections = %d, want 0 (already reflected)", result2.ReflectionsCreated)
	}
}

func TestEmptyReflectionRejection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	tests := []struct {
		name     string
		response string
		wantSave bool
	}{
		{
			name:     "empty response is rejected",
			response: "",
			wantSave: false,
		},
		{
			name:     "short response is rejected",
			response: "Too short",
			wantSave: false,
		},
		{
			name:     "whitespace-only is rejected",
			response: "   \n  \t  ",
			wantSave: false,
		},
		{
			name:     "valid response is saved",
			response: "1. Insight one: patterns emerge from codebase analysis across multiple packages.\n2. Insight two: architecture follows clean design.",
			wantSave: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mock := &mockLLM{response: tc.response}
			engine := NewEngine(database, mock, linkStore, idGen)

			// Insert enough observations for ForceReflect
			for i := 0; i < 5; i++ {
				database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
					VALUES(?, 'obs', 'content for test', 'decision', 1, 0.9, 0.8, 'semantic', ?)`,
					fmt.Sprintf("EMPTY_%s_%d", tc.name[:4], i), "empty_test_"+tc.name[:4])
			}

			result, err := engine.ForceReflect(ctx, "empty_test_"+tc.name[:4])
			if err != nil {
				t.Fatalf("force reflect: %v", err)
			}

			if tc.wantSave && result.ReflectionsCreated != 1 {
				t.Errorf("expected 1 reflection, got %d", result.ReflectionsCreated)
			}
			if !tc.wantSave && result.ReflectionsCreated != 0 {
				t.Errorf("expected 0 reflections (rejected), got %d", result.ReflectionsCreated)
			}
		})
	}
}

func TestReflectionSavedWithDurableRetention(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert enough observations for ForceReflect
	for i := 0; i < 5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'rettest')`, fmt.Sprintf("RET%04d", i))
	}

	result, err := e.ForceReflect(ctx, "rettest")
	if err != nil {
		t.Fatalf("force reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Fatalf("expected 1 reflection, got %d", result.ReflectionsCreated)
	}

	// Verify the reflection observation has retention = 'durable'
	var retention string
	err = tdb.DB.QueryRowContext(ctx, `SELECT retention FROM observations WHERE source = 'reflection' AND namespace = 'rettest' AND deleted_at IS NULL`).Scan(&retention)
	if err != nil {
		t.Fatalf("query retention: %v", err)
	}
	if retention != "durable" {
		t.Errorf("retention = %q, want 'durable'", retention)
	}
}

func idFromInt(i int) string {
	return fmt.Sprintf("OBS%04d", i)
}
