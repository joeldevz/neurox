package session

import (
	"context"
	"path/filepath"
	"testing"

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
	if result.ObservationsExtracted != 2 {
		t.Errorf("extracted = %d, want 2", result.ObservationsExtracted)
	}
	// LLM available → no warning
	if result.Warning != "" {
		t.Errorf("expected no warning when LLM is available, got %q", result.Warning)
	}

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
	result, err := mgr.End(ctx, start.SessionID, "We migrated to SQLite yesterday and it is currently working.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if result.ObservationsExtracted != 1 {
		t.Fatalf("extracted = %d, want 1", result.ObservationsExtracted)
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
	result, err := mgr.End(ctx, start.SessionID, "We decided on Redis.", "test")
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if result.ObservationsExtracted != 1 {
		t.Fatalf("extracted = %d, want 1", result.ObservationsExtracted)
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
