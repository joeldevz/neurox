package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/consolidate"
	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	deps := &Deps{
		ObservationStore: observation.NewStore(database, nil),
		RecallEngine:     recall.NewEngine(database),
		LinkStore:        links.NewStore(database, idGen),
		DB:               database,
	}

	return NewServer(Config{Port: 0}, deps)
}

func doJSON(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)
	return w
}

func decodeResp(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(t)
	w := doJSON(t, s, "GET", "/health", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSaveAndGetObservation(t *testing.T) {
	s := newTestServer(t)

	// Save
	w := doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title":            "JWT migration",
		"content":          "Migrated to JWT tokens",
		"observation_type": "decision",
		"tags":             []string{"auth", "jwt"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("save: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var saved map[string]any
	decodeResp(t, w, &saved)
	id := saved["id"].(string)
	if id == "" {
		t.Fatal("expected id")
	}

	// Get
	w = doJSON(t, s, "GET", "/api/v1/observations/"+id, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var got map[string]any
	decodeResp(t, w, &got)
	if got["title"] != "JWT migration" {
		t.Errorf("title = %v", got["title"])
	}
}

func TestRecallEndpoint(t *testing.T) {
	s := newTestServer(t)

	// Save observations
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Auth middleware", "content": "Uses JWT for auth",
	})
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Database setup", "content": "PostgreSQL with pgx driver",
	})

	// Recall
	w := doJSON(t, s, "GET", "/api/v1/observations/search?q=JWT+auth", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("recall: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	decodeResp(t, w, &result)
	count := int(result["count"].(float64))
	if count == 0 {
		t.Fatal("expected recall results")
	}
}

func TestForgetEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "To forget", "content": "Temp",
	})
	var saved map[string]any
	decodeResp(t, w, &saved)

	w = doJSON(t, s, "DELETE", "/api/v1/observations/"+saved["id"].(string), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("forget: expected 200, got %d", w.Code)
	}
}

func TestInvalidateEndpoint(t *testing.T) {
	s := newTestServer(t)

	w := doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Old fact", "content": "Outdated info",
	})
	var saved map[string]any
	decodeResp(t, w, &saved)

	w = doJSON(t, s, "POST", "/api/v1/observations/"+saved["id"].(string)+"/invalidate", map[string]any{
		"reason":              "outdated",
		"replacement_title":   "New fact",
		"replacement_content": "Current info",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("invalidate: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]string
	decodeResp(t, w, &result)
	if result["replacement_id"] == "" {
		t.Error("expected replacement_id")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestServer(t)

	// Start
	w := doJSON(t, s, "POST", "/api/v1/sessions", map[string]any{
		"title": "Dev session", "namespace": "test",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("start: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var started map[string]string
	decodeResp(t, w, &started)

	// End
	w = doJSON(t, s, "PUT", "/api/v1/sessions/"+started["session_id"]+"/end", map[string]any{
		"summary": "Implemented auth flow",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("end: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGitHookEndpoint(t *testing.T) {
	s := newTestServer(t)

	// Save observation with file link
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Auth service", "content": "Middleware pattern",
		"files": []string{"internal/auth/service.go"},
	})

	// Git hook
	w := doJSON(t, s, "POST", "/api/v1/hooks/git", map[string]any{
		"changed_files": []string{"internal/auth/service.go"},
		"commit_sha":    "abc123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("git hook: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	decodeResp(t, w, &result)
	if result["observations_staled"].(float64) != 1 {
		t.Errorf("expected 1 staled, got %v", result["observations_staled"])
	}
}

func TestStatusEndpoint(t *testing.T) {
	s := newTestServer(t)

	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Test", "content": "For status",
	})

	w := doJSON(t, s, "GET", "/api/v1/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status: expected 200, got %d", w.Code)
	}

	var status map[string]any
	decodeResp(t, w, &status)
	if status["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", status["total"])
	}
}

func TestCORSHeaders(t *testing.T) {
	s := newTestServer(t)
	w := doJSON(t, s, "OPTIONS", "/health", nil)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}
}

func TestActivityEndpoint(t *testing.T) {
	s := newTestServer(t)

	// Test activity endpoint with no data
	w := doJSON(t, s, "GET", "/api/v1/stats/activity", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activity: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	decodeResp(t, w, &result)

	// Verify structure
	if _, ok := result["days"]; !ok {
		t.Error("expected 'days' field")
	}
	if _, ok := result["series"]; !ok {
		t.Error("expected 'series' field")
	}
	if _, ok := result["period_days"]; !ok {
		t.Error("expected 'period_days' field")
	}

	// With no data, days should be empty array (since we fill all days)
	// Actually we fill all days in range, so days should have 30 entries
	days := result["days"].([]any)
	if len(days) != 30 {
		t.Errorf("expected 30 days, got %d", len(days))
	}

	// Test with custom days parameter
	w = doJSON(t, s, "GET", "/api/v1/stats/activity?days=7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activity with days=7: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	decodeResp(t, w, &result)
	days = result["days"].([]any)
	if len(days) != 7 {
		t.Errorf("expected 7 days, got %d", len(days))
	}
	if result["period_days"].(float64) != 7 {
		t.Errorf("expected period_days=7, got %v", result["period_days"])
	}
}

func TestBrowseSortParameter(t *testing.T) {
	s := newTestServer(t)

	// Create observations in sequence with delays to ensure different timestamps
	// Note: API doesn't accept importance, all get default 0.5, so sort falls back to created_at DESC
	// SQLite datetime('now') has second precision, so we need 1+ second delays
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title":   "First observation",
		"content": "Content A",
	})
	time.Sleep(1100 * time.Millisecond)
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title":   "Second observation",
		"content": "Content B",
	})
	time.Sleep(1100 * time.Millisecond)
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title":   "Third observation",
		"content": "Content C",
	})

	// Test default sort (importance DESC, created_at DESC)
	// Since all have same importance (0.5), secondary sort by created_at DESC applies
	w := doJSON(t, s, "GET", "/api/v1/observations/browse", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browse default: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	decodeResp(t, w, &result)
	items := result["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// With default sort and equal importance, most recent should be first
	firstItem := items[0].(map[string]any)
	if firstItem["title"] != "Third observation" {
		t.Errorf("default sort: expected 'Third observation' first (most recent), got %v", firstItem["title"])
	}

	// Test sort=recent (created_at DESC) - should also give most recent first
	w = doJSON(t, s, "GET", "/api/v1/observations/browse?sort=recent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browse recent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	decodeResp(t, w, &result)
	items = result["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// With recent sort, last created should be first
	firstItem = items[0].(map[string]any)
	if firstItem["title"] != "Third observation" {
		t.Errorf("recent sort: expected 'Third observation' first (most recent), got %v", firstItem["title"])
	}

	// Test with invalid sort value (should use default)
	w = doJSON(t, s, "GET", "/api/v1/observations/browse?sort=invalid", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("browse invalid sort: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestReflectEndpoint_NilEngine(t *testing.T) {
	s := newTestServer(t)
	// ReflectEngine is nil in the default test server.
	w := doJSON(t, s, "POST", "/api/v1/reflect", map[string]any{
		"namespace": "test",
	})
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("reflect without engine: expected 501, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeResp(t, w, &resp)
	if resp["error"] == "" {
		t.Error("expected error message")
	}
}

func TestConsolidateEndpoint_NilPipeline(t *testing.T) {
	s := newTestServer(t)
	// Pipeline is nil in the default test server.
	w := doJSON(t, s, "POST", "/api/v1/consolidate", nil)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("consolidate without pipeline: expected 501, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeResp(t, w, &resp)
	if resp["error"] == "" {
		t.Error("expected error message")
	}
}

func TestCurateEndpoint_NilEngine(t *testing.T) {
	s := newTestServer(t)
	// CurateEngine is nil in the default test server.
	w := doJSON(t, s, "POST", "/api/v1/curate", nil)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("curate without engine: expected 501, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	decodeResp(t, w, &resp)
	if resp["error"] == "" {
		t.Error("expected error message")
	}
}

func TestCurateEndpoint_NilEngine_WithParams(t *testing.T) {
	s := newTestServer(t)
	// CurateEngine is nil — should still return 501 even with query params.
	w := doJSON(t, s, "POST", "/api/v1/curate?namespace=test&dry_run=true", nil)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("curate with params without engine: expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConsolidateEndpoint_WithPipeline(t *testing.T) {
	s := newTestServer(t)

	// Create a Pipeline with the test database so ForceRun can execute.
	// We need a decay engine, which Pipeline uses internally.
	decayEngine := decay.NewEngine(s.deps.DB)
	pipeline := consolidate.NewPipeline(
		s.deps.DB, decayEngine, nil, nil, llm.NewGate(nil, llm.GateModeOff),
		s.deps.LinkStore, nil, nil,
		observation.NewULIDGenerator(), consolidate.Config{},
	)
	s.deps.Pipeline = pipeline

	// Seed an observation so consolidation has something to process.
	doJSON(t, s, "POST", "/api/v1/observations", map[string]any{
		"title": "Test obs", "content": "For consolidation",
	})

	w := doJSON(t, s, "POST", "/api/v1/consolidate", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("consolidate: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	decodeResp(t, w, &resp)
	if resp["message"] != "consolidation completed" {
		t.Errorf("unexpected message: %v", resp["message"])
	}
	// After forced consolidation, buffer should be 0 (everything promoted).
	if resp["buffer"].(float64) != 0 {
		t.Errorf("expected buffer=0 after forced consolidation, got %v", resp["buffer"])
	}
}
