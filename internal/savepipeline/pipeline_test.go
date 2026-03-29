package savepipeline_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/savepipeline"
	"github.com/joeldevz/neurox/internal/session"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "pipeline_test.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestRun_SetsProvenance verifies that the pipeline always overwrites
// SourceSurface and SourceTool regardless of what the caller set.
func TestRun_SetsProvenance(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	tests := []struct {
		surface string
		tool    string
	}{
		{"cli", "save"},
		{"mcp", "save"},
		{"http", "save"},
	}

	for _, tt := range tests {
		t.Run(tt.surface, func(t *testing.T) {
			obs := observation.Observation{
				Title:     "Provenance test from " + tt.surface,
				Content:   "Content for provenance testing.",
				Namespace: "pipeline-prov",
				// Caller intentionally sets wrong surface — pipeline must overwrite.
				SourceSurface: "wrong-surface",
				SourceTool:    "wrong-tool",
			}

			pr, err := savepipeline.Run(ctx, savepipeline.Deps{
				Store: store,
				DB:    database,
			}, savepipeline.Input{
				Obs:     obs,
				Surface: tt.surface,
				Tool:    tt.tool,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if pr.ID == "" {
				t.Fatal("expected non-empty ID")
			}

			got, err := store.Get(ctx, pr.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.SourceSurface != tt.surface {
				t.Errorf("SourceSurface = %q, want %q", got.SourceSurface, tt.surface)
			}
			if got.SourceTool != tt.tool {
				t.Errorf("SourceTool = %q, want %q", got.SourceTool, tt.tool)
			}
		})
	}
}

// TestRun_AttachesActiveSession verifies that the pipeline looks up and
// attaches the active session ID for the namespace.
func TestRun_AttachesActiveSession(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)
	idGen := observation.NewULIDGenerator()
	mgr := session.NewManager(database, llm.Disabled{}, idGen)

	startResult, err := mgr.Start(ctx, "Test session", "", "", "pipeline-sess")
	if err != nil {
		t.Fatalf("Start session: %v", err)
	}

	obs := observation.Observation{
		Title:     "Observation during session",
		Content:   "Should have session attached by pipeline.",
		Namespace: "pipeline-sess",
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store: store,
		DB:    database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "mcp",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := store.Get(ctx, pr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourceSessionID != startResult.SessionID {
		t.Errorf("SourceSessionID = %q, want %q", got.SourceSessionID, startResult.SessionID)
	}
}

// TestRun_SkipsSessionAttachmentWhenNoDB verifies the pipeline does not
// panic when DB is nil (session lookup is best-effort).
func TestRun_SkipsSessionAttachmentWhenNoDB(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	obs := observation.Observation{
		Title:     "No DB test",
		Content:   "Pipeline should work without DB for session lookup.",
		Namespace: "pipeline-nodb",
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store: store,
		DB:    nil, // no DB — session lookup skipped
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "cli",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pr.ID == "" {
		t.Fatal("expected non-empty ID")
	}
}

// TestRun_AutoClassifiesRetention verifies that retention is auto-classified
// when the caller does not provide an explicit value.
func TestRun_AutoClassifiesRetention(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	obs := observation.Observation{
		Title:           "Architecture decision",
		Content:         "We chose Go for the backend due to concurrency support.",
		Namespace:       "pipeline-retention",
		ObservationType: observation.ObservationTypeDecision,
		// No Retention set — should be auto-classified.
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store: store,
		DB:    database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "cli",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := store.Get(ctx, pr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Retention == "" {
		t.Error("Retention should be auto-classified, not empty")
	}
}

// TestRun_RespectsExplicitRetention verifies that an explicitly provided
// retention value is not overwritten by auto-classification.
func TestRun_RespectsExplicitRetention(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	obs := observation.Observation{
		Title:           "Step execution log",
		Content:         "Running step 2 of 7.",
		Namespace:       "pipeline-retention-explicit",
		ObservationType: observation.ObservationTypeDecision,
		Retention:       observation.RetentionOperational, // explicitly operational
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store: store,
		DB:    database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "mcp",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := store.Get(ctx, pr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Retention != observation.RetentionOperational {
		t.Errorf("Retention = %q, want %q (explicit retention must not be overwritten)",
			got.Retention, observation.RetentionOperational)
	}
}

// TestRun_QueueFastPath verifies that the pipeline uses SaveQueue
// when available (message says "queued for persistence").
func TestRun_QueueFastPath(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)
	queue := observation.NewSaveQueue(store)
	queue.Start(ctx)
	defer queue.Stop()

	obs := observation.Observation{
		Title:     "Queue fast path test",
		Content:   "Should be queued, not written synchronously.",
		Namespace: "pipeline-queue",
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store:     store,
		SaveQueue: queue,
		DB:        database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "mcp",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pr.ID == "" {
		t.Fatal("expected non-empty ID from queue path")
	}
	if pr.Message != "observation queued for persistence" {
		t.Errorf("message = %q, want 'observation queued for persistence'", pr.Message)
	}
}

// TestRun_QualityGateReject verifies that the pipeline honors LLM gate
// rejection in the sync fallback path.
func TestRun_QualityGateReject(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	// Use full mode with an always-reject provider.
	rejectProvider := &alwaysRejectLLM{}
	gate := llm.NewGate(rejectProvider, llm.GateModeFull)

	obs := observation.Observation{
		Title:     "Should be rejected",
		Content:   "Low quality content.",
		Namespace: "pipeline-gate",
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store:   store,
		LLMGate: gate,
		DB:      database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "cli",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !pr.Rejected {
		t.Error("expected Rejected=true for quality gate rejection")
	}
	if pr.ID != "" {
		t.Errorf("expected empty ID for rejected observation, got %q", pr.ID)
	}
}

// TestRun_PostSaveHooksFireViaQueue verifies that PostSave hooks fire when
// using the SaveQueue path.
func TestRun_PostSaveHooksFireViaQueue(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)
	queue := observation.NewSaveQueue(store)

	hookFired := make(chan string, 1)
	queue.OnPostSave(func(_ context.Context, saved observation.Observation) {
		select {
		case hookFired <- saved.ID:
		default:
		}
	})

	queue.Start(ctx)
	defer queue.Stop()

	obs := observation.Observation{
		Title:     "Hook test",
		Content:   "Post-save hook should fire via queue.",
		Namespace: "pipeline-hooks",
	}

	pr, err := savepipeline.Run(ctx, savepipeline.Deps{
		Store:     store,
		SaveQueue: queue,
		DB:        database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "mcp",
		Tool:    "save",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Wait for hook to fire (up to 3 seconds).
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case firedID := <-hookFired:
			if firedID != pr.ID {
				t.Errorf("hook fired for %q, want %q", firedID, pr.ID)
			}
		}
	}()
	<-done
}

// TestRun_NoStoreOrQueue returns an error when neither Store nor SaveQueue is set.
func TestRun_NoStoreOrQueue(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()

	obs := observation.Observation{
		Title:     "No store test",
		Content:   "Should fail without Store or SaveQueue.",
		Namespace: "pipeline-nostore",
	}

	_, err := savepipeline.Run(ctx, savepipeline.Deps{
		// Store: nil, SaveQueue: nil
		DB: database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "cli",
		Tool:    "save",
	})
	if err == nil {
		t.Fatal("expected error when no Store or SaveQueue configured")
	}
}

// --- Update pipeline tests ---

// TestAfterUpdate_NilDepsDoesNotPanic verifies that AfterUpdate is safe to
// call with nil FactExtractor and EmbedQueue.
func TestAfterUpdate_NilDepsDoesNotPanic(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	saved, err := store.Save(ctx, observation.Observation{
		Title:   "Nil deps update test",
		Content: "Initial content",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	saved.Content = "Updated content"
	updated, err := store.Update(ctx, saved)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// AfterUpdate with nil deps should not panic.
	savepipeline.AfterUpdate(ctx, savepipeline.Deps{}, updated)
}

// TestAfterUpdate_TemporalReExtractionInStore verifies that Store.Update()
// calls temporal extraction (parity with Store.Save). This is a design
// decision: temporal re-extraction happens inside the Store, not in
// AfterUpdate, so that all update paths get temporal mentions refreshed.
func TestAfterUpdate_TemporalReExtractionInStore(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	store := observation.NewStore(database, nil)

	var extractCalls []string
	store.SetTemporalExtractor(&testTemporalExtractor{calls: &extractCalls})

	saved, err := store.Save(ctx, observation.Observation{
		Title:   "Temporal re-extraction test",
		Content: "We migrated yesterday",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	// 1 call from Save.
	if len(extractCalls) != 1 {
		t.Fatalf("after save: temporal extract calls = %d, want 1", len(extractCalls))
	}

	// Update with new content.
	saved.Content = "We currently use the new system"
	_, err = store.Update(ctx, saved)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// 2 calls: 1 from Save + 1 from Update.
	if len(extractCalls) != 2 {
		t.Fatalf("after update: temporal extract calls = %d, want 2", len(extractCalls))
	}
	if extractCalls[1] != saved.ID {
		t.Errorf("update temporal extraction ID = %q, want %q", extractCalls[1], saved.ID)
	}
}

// TestBuildPostSaveHooks_NilDeps verifies that BuildPostSaveHooks returns
// no hooks when all deps are nil.
func TestBuildPostSaveHooks_NilDeps(t *testing.T) {
	hooks := savepipeline.BuildPostSaveHooks(savepipeline.Deps{})
	if len(hooks) != 0 {
		t.Errorf("expected 0 hooks with nil deps, got %d", len(hooks))
	}
}

// testTemporalExtractor implements observation.TemporalExtractor for testing.
type testTemporalExtractor struct {
	calls *[]string
}

func (e *testTemporalExtractor) Extract(_ context.Context, obsID, _ string) (int, error) {
	*e.calls = append(*e.calls, obsID)
	return 1, nil
}

// alwaysRejectLLM is an LLM provider that always returns "REJECT".
type alwaysRejectLLM struct{}

func (a *alwaysRejectLLM) Complete(_ context.Context, _ string) (string, error) {
	return "REJECT", nil
}

func (a *alwaysRejectLLM) Name() string { return "always-reject" }

var _ llm.Provider = (*alwaysRejectLLM)(nil)
