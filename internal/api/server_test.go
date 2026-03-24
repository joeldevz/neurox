package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/links"
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
