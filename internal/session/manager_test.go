package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/temporal"
)

type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

func (m *mockLLM) Name() string { return "mock" }

func setupTest(t *testing.T) (*Manager, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, llm.Disabled{}, idGen)
	return mgr, &db.TestDB{DB: database}
}

func TestStartSession(t *testing.T) {
	mgr, _ := setupTest(t)
	ctx := context.Background()

	result, err := mgr.Start(ctx, "My Session", "/home/user/project", "main", "myns")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected session ID")
	}
	if result.Namespace != "myns" {
		t.Errorf("namespace = %q, want 'myns'", result.Namespace)
	}
}

func TestStartAbandonsPrevious(t *testing.T) {
	mgr, tdb := setupTest(t)
	ctx := context.Background()

	// Start first session
	first, _ := mgr.Start(ctx, "First", "", "", "ns")

	// Start second — should abandon first
	second, err := mgr.Start(ctx, "Second", "", "", "ns")
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	if second.Abandoned != 1 {
		t.Errorf("abandoned = %d, want 1", second.Abandoned)
	}

	// Verify first is abandoned
	var status string
	tdb.DB.QueryRowContext(ctx, "SELECT status FROM sessions WHERE id = ?", first.SessionID).Scan(&status)
	if status != "abandoned" {
		t.Errorf("first session status = %q, want 'abandoned'", status)
	}
}

func TestEndSession(t *testing.T) {
	mgr, _ := setupTest(t)
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")

	result, err := mgr.End(ctx, start.SessionID, "We implemented feature X. Discovered that Y works better.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if result.SessionID != start.SessionID {
		t.Error("session ID mismatch")
	}
	// No LLM → no extraction
	if result.ObservationsExtracted != 0 {
		t.Errorf("extracted = %d, want 0 (no LLM)", result.ObservationsExtracted)
	}
	// No LLM → warning should be set
	if result.Warning == "" {
		t.Error("expected warning when LLM is disabled")
	}
}

func TestEndSessionWithLLM(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | Chose React over Vue | What: Selected React for the frontend. Why: Better ecosystem for our team. Where: Frontend architecture.
discovery | Redis caching improves latency | What: Adding Redis cache reduced API latency by 40%. Where: API gateway.`}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	result, err := mgr.End(ctx, start.SessionID, "We chose React and added Redis caching.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	// Extraction is async — ObservationsExtracted is -1 to signal background processing
	if result.ObservationsExtracted != -1 {
		t.Errorf("extracted = %d, want -1 (async)", result.ObservationsExtracted)
	}
	// LLM available → no warning
	if result.Warning != "" {
		t.Errorf("expected no warning when LLM is available, got %q", result.Warning)
	}

	// Wait for background extraction to finish before checking DB
	mgr.WaitBackground()

	// Verify observations were created
	var count int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'ns' AND source = 'consolidator' AND deleted_at IS NULL").Scan(&count)
	if count != 2 {
		t.Errorf("observations in DB = %d, want 2", count)
	}
}

func TestEndSessionWithLLMExtractsTemporalMentions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `discovery | SQLite migration | What: We migrated to SQLite yesterday. Currently running in production.`}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)

	// Wire temporal extraction.
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	mgr.SetTemporalExtractor(temporalExtractor)

	ctx := context.Background()
	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	_, err = mgr.End(ctx, start.SessionID, "We migrated to SQLite yesterday and it is currently working.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Wait for background extraction to finish before checking DB
	mgr.WaitBackground()

	// Verify at least one observation was extracted
	var obsCount int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'ns' AND source = 'consolidator' AND deleted_at IS NULL").Scan(&obsCount)
	if obsCount < 1 {
		t.Fatalf("observations in DB = %d, want >= 1", obsCount)
	}

	// Verify temporal mentions were created for the extracted observation.
	var mentionCount int
	database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM temporal_mentions tm
		JOIN observations o ON o.id = tm.observation_id
		WHERE o.namespace = 'ns' AND o.source = 'consolidator'
	`).Scan(&mentionCount)
	if mentionCount < 1 {
		t.Errorf("temporal mentions = %d, want >= 1", mentionCount)
	}
}

func TestEndSessionWithoutTemporalExtractorStillWorks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | Use Redis | What: Decided to use Redis for caching yesterday.`}
	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)
	// No SetTemporalExtractor call — should still work fine.

	ctx := context.Background()
	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	_, err = mgr.End(ctx, start.SessionID, "We decided on Redis.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Wait for background extraction to finish before checking DB
	mgr.WaitBackground()

	// Verify observation was created
	var count int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'ns' AND source = 'consolidator' AND deleted_at IS NULL").Scan(&count)
	if count != 1 {
		t.Fatalf("observations in DB = %d, want 1", count)
	}
}

func TestEndSessionPostSaveHooksFire(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | Chose React | What: Selected React for the frontend. Why: Better ecosystem.
discovery | Redis caching | What: Adding Redis cache reduced latency. Where: API gateway.`}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)

	// Register post-save hooks to track calls.
	// Use a mutex since hooks run in a background goroutine.
	var mu sync.Mutex
	var hookCalls []string
	mgr.OnPostSave(func(_ context.Context, id, title, _, _ string) {
		mu.Lock()
		defer mu.Unlock()
		hookCalls = append(hookCalls, id+":"+title)
	})

	ctx := context.Background()
	start, _ := mgr.Start(ctx, "Hook test", "", "", "ns")
	_, err = mgr.End(ctx, start.SessionID, "We chose React and added Redis caching.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Wait for background extraction to finish
	mgr.WaitBackground()

	// Post-save hooks should have been called for each extracted observation.
	mu.Lock()
	defer mu.Unlock()
	if len(hookCalls) != 2 {
		t.Fatalf("post-save hook calls = %d, want 2; got: %v", len(hookCalls), hookCalls)
	}
}

func TestEndSessionProvenanceOnExtracted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | Use Go | What: Go is the language. Why: Concurrency.`}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)

	ctx := context.Background()
	start, _ := mgr.Start(ctx, "Provenance test", "", "", "ns")
	_, err = mgr.End(ctx, start.SessionID, "We decided on Go.", "mcp")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Wait for background extraction to finish
	mgr.WaitBackground()

	// Verify provenance on the extracted observation.
	var sourceSurface, sourceSessionID, sourceTool string
	err = database.QueryRowContext(ctx, `
		SELECT COALESCE(source_surface,''), COALESCE(source_session_id,''), COALESCE(source_tool,'')
		FROM observations WHERE namespace = 'ns' AND source = 'consolidator' AND deleted_at IS NULL
		LIMIT 1
	`).Scan(&sourceSurface, &sourceSessionID, &sourceTool)
	if err != nil {
		t.Fatalf("query provenance: %v", err)
	}
	if sourceSurface != "mcp" {
		t.Errorf("source_surface = %q, want 'mcp'", sourceSurface)
	}
	if sourceSessionID != start.SessionID {
		t.Errorf("source_session_id = %q, want %q", sourceSessionID, start.SessionID)
	}
	if sourceTool != "session_end" {
		t.Errorf("source_tool = %q, want 'session_end'", sourceTool)
	}
}

func TestEndSessionNotFound(t *testing.T) {
	mgr, _ := setupTest(t)
	_, err := mgr.End(context.Background(), "nonexistent", "summary", "test")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestParseExtractions(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     int
	}{
		{"valid", "decision | Title | Content\nbugfix | Title2 | Content2", 2},
		{"with numbers", "1. decision | Title | Content\n2. bugfix | Title2 | Content2", 2},
		{"NONE", "NONE", 0},
		{"empty", "", 0},
		{"invalid type → discovery", "invalid_type | Title | Content", 1},
		{"missing parts", "not a valid line", 0},
		{"max 8", "a|b|c\na|b|c\na|b|c\na|b|c\na|b|c\na|b|c\na|b|c\na|b|c\na|b|c", 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseExtractions(tt.response)
			if len(got) != tt.want {
				t.Errorf("parseExtractions() = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseExtractionsDoesNotTreatNonethelessAsNone(t *testing.T) {
	// Regression test: "Nonetheless" contains "NONE" when uppercased.
	// parseExtractions should only treat the literal word "NONE" as "no extractions".
	response := "discovery | Update database | What: Nonetheless the schema is good. Why: Nonetheless we proceeded."
	got := parseExtractions(response)
	if len(got) != 1 {
		t.Errorf("parseExtractions('Nonetheless...') = %d, want 1", len(got))
	}
	if len(got) > 0 && got[0].title != "Update database" {
		t.Errorf("title = %q, want 'Update database'", got[0].title)
	}
}

func TestWaitForExtractionReturnsRealCountAndError(t *testing.T) {
	// Direct test: End() -> WaitForExtraction() -> verify count and error.
	// Does NOT use mgr.WaitBackground as substitute.
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | Choice A | Content A
bugfix | Fix B | Content B
discovery | Find C | Content C`}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	endResult, err := mgr.End(ctx, start.SessionID, "Some summary", "cli")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Before waiting: should be -1 (async signal)
	if endResult.ObservationsExtracted != -1 {
		t.Errorf("before wait: extracted = %d, want -1 (async)", endResult.ObservationsExtracted)
	}

	// Call WaitForExtraction directly (not mgr.WaitBackground).
	// This is what the CLI seam uses.
	count, extractErr := endResult.WaitForExtraction()

	// Should get actual count and nil error
	if count != 3 {
		t.Errorf("WaitForExtraction count = %d, want 3", count)
	}
	if extractErr != nil {
		t.Errorf("WaitForExtraction error = %v, want nil", extractErr)
	}

	// Verify that 3 observations were actually persisted in DB
	var dbCount int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE namespace = 'ns' AND source = 'consolidator' AND deleted_at IS NULL").Scan(&dbCount)
	if dbCount != 3 {
		t.Errorf("observations in DB = %d, want 3", dbCount)
	}
}

func TestWaitForExtractionReportsLLMError(t *testing.T) {
	// Test that CLI seam can observe LLM errors through WaitForExtraction.
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Mock LLM that fails
	mock := &mockLLM{err: fmt.Errorf("rate limited")}

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	endResult, err := mgr.End(ctx, start.SessionID, "Some summary", "cli")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// Call WaitForExtraction to wait for and retrieve the error
	count, extractErr := endResult.WaitForExtraction()

	// Should get 0 count and non-nil error
	if count != 0 {
		t.Errorf("WaitForExtraction count = %d, want 0 (LLM failed)", count)
	}
	if extractErr == nil {
		t.Errorf("WaitForExtraction error = nil, want non-nil (should report LLM failure)")
	}
	if extractErr != nil && !strings.Contains(extractErr.Error(), "rate limited") {
		t.Errorf("extractErr = %v, should contain 'rate limited'", extractErr)
	}
}

func TestWaitForExtractionWhenNoLLM(t *testing.T) {
	// When LLM is not available, WaitForExtraction should return synchronously
	// with count=0 and error=nil (no async extraction was launched).
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, llm.Disabled{}, idGen) // No LLM
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	endResult, err := mgr.End(ctx, start.SessionID, "Some summary", "cli")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// ObservationsExtracted should be 0 (sync, no LLM)
	if endResult.ObservationsExtracted != 0 {
		t.Errorf("ObservationsExtracted = %d, want 0 (no LLM)", endResult.ObservationsExtracted)
	}
	if endResult.Warning == "" {
		t.Error("expected warning when LLM is disabled")
	}

	// WaitForExtraction should return immediately with the sync count and nil error
	count, extractErr := endResult.WaitForExtraction()
	if count != 0 {
		t.Errorf("WaitForExtraction count = %d, want 0", count)
	}
	if extractErr != nil {
		t.Errorf("WaitForExtraction error = %v, want nil", extractErr)
	}
}

func TestWaitForExtractionIdempotent(t *testing.T) {
	// Regression test for R4-double-wait: calling WaitForExtraction twice
	// must return the same result without blocking.
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mock := &mockLLM{response: `decision | First call | Content`}
	idGen := observation.NewULIDGenerator()
	mgr := NewManager(database, mock, idGen)
	ctx := context.Background()

	start, _ := mgr.Start(ctx, "Test", "", "", "ns")
	endResult, err := mgr.End(ctx, start.SessionID, "Some summary", "cli")
	if err != nil {
		t.Fatalf("end: %v", err)
	}

	// First call: should block and get result from channel
	count1, err1 := endResult.WaitForExtraction()
	if count1 != 1 {
		t.Errorf("first WaitForExtraction count = %d, want 1", count1)
	}
	if err1 != nil {
		t.Errorf("first WaitForExtraction error = %v, want nil", err1)
	}

	// Second call: must return same result immediately (no block)
	// Use a channel to detect timeout (if it blocks, test will timeout)
	done := make(chan bool, 1)
	go func() {
		count2, err2 := endResult.WaitForExtraction()
		if count2 != count1 {
			t.Errorf("second WaitForExtraction count = %d, want %d", count2, count1)
		}
		if err2 != err1 {
			t.Errorf("second WaitForExtraction error = %v, want %v", err2, err1)
		}
		done <- true
	}()

	select {
	case <-done:
		// Second call completed quickly — idempotence works
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second WaitForExtraction() blocked; not idempotent")
	}
}
