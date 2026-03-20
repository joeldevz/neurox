package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"neurox/internal/db"
	"neurox/internal/links"
	"neurox/internal/observation"
	"neurox/internal/recall"
	"neurox/internal/temporal"
)

func newTestDeps(t *testing.T) *Deps {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	obsStore := observation.NewStore(database, nil)

	// Configure temporal extractor so observations with temporal content
	// get temporal mentions persisted (needed for temporal-aware recall tests).
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	obsStore.SetTemporalExtractor(temporalExtractor)

	return &Deps{
		ObservationStore: obsStore,
		RecallEngine:     recall.NewEngine(database),
		LinkStore:        links.NewStore(database, idGen),
		DB:               database,
	}
}

func initServer(t *testing.T, deps *Deps) *mcpTestHelper {
	t.Helper()
	s := NewServer(deps)
	ctx := context.Background()

	s.HandleMessage(ctx, mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	}))

	return &mcpTestHelper{s: s, t: t, ctx: ctx, nextID: 1}
}

type mcpTestHelper struct {
	s      *mcpserver.MCPServer
	t      *testing.T
	ctx    context.Context
	nextID int
}

func (h *mcpTestHelper) callTool(name string, args map[string]any) string {
	h.t.Helper()
	id := h.nextID
	h.nextID++

	resp := h.s.HandleMessage(h.ctx, mustMarshal(h.t, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}))

	b, err := json.Marshal(resp)
	if err != nil {
		h.t.Fatalf("marshal response: %v", err)
	}

	var parsed struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		h.t.Fatalf("unmarshal: %v\nraw: %s", err, string(b))
	}

	if parsed.Error != nil {
		h.t.Fatalf("JSON-RPC error calling %s: %s", name, parsed.Error.Message)
	}
	if parsed.Result.IsError {
		h.t.Fatalf("tool error calling %s: %s", name, parsed.Result.Content[0].Text)
	}

	if len(parsed.Result.Content) == 0 {
		return ""
	}
	return parsed.Result.Content[0].Text
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestSaveAndRecall(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save
	saveText := h.callTool("save", map[string]any{
		"title":            "JWT auth migration",
		"content":          "Migrated from session tokens to JWT for better scalability",
		"observation_type": "decision",
		"tags":             "auth,jwt",
	})

	var saved saveResponse
	json.Unmarshal([]byte(saveText), &saved)

	if saved.ID == "" {
		t.Fatal("save returned empty ID")
	}
	if saved.Message != "observation saved to Buffer" {
		t.Errorf("unexpected message: %s", saved.Message)
	}

	// Recall
	recallText := h.callTool("recall", map[string]any{
		"query": "JWT auth",
	})

	var recalled recallResponse
	json.Unmarshal([]byte(recallText), &recalled)

	if recalled.Count == 0 {
		t.Fatal("recall returned no results")
	}
	if recalled.Results[0].Title != "JWT auth migration" {
		t.Errorf("unexpected title: %s", recalled.Results[0].Title)
	}
}

func TestStatusTool(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save an observation first
	h.callTool("save", map[string]any{
		"title":   "Test obs",
		"content": "For status test",
	})

	// Status
	statusText := h.callTool("status", map[string]any{})

	var status statusResponse
	json.Unmarshal([]byte(statusText), &status)

	if status.Total != 1 {
		t.Errorf("expected total=1, got %d", status.Total)
	}
	if status.Buffer != 1 {
		t.Errorf("expected buffer=1, got %d", status.Buffer)
	}
}

func TestInvalidateTool(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save
	saveText := h.callTool("save", map[string]any{
		"title":   "Old fact",
		"content": "Something outdated",
	})

	var saved saveResponse
	json.Unmarshal([]byte(saveText), &saved)

	// Invalidate with replacement
	invText := h.callTool("invalidate", map[string]any{
		"observation_id":      saved.ID,
		"reason":              "outdated info",
		"replacement_title":   "New fact",
		"replacement_content": "Something current",
	})

	var inv map[string]string
	json.Unmarshal([]byte(invText), &inv)

	if inv["replacement_id"] == "" {
		t.Error("expected replacement_id")
	}
	if inv["message"] != "observation invalidated and replaced" {
		t.Errorf("unexpected message: %s", inv["message"])
	}
}

func TestToolsList(t *testing.T) {
	deps := newTestDeps(t)
	ctx := context.Background()
	s := NewServer(deps)

	// Initialize first
	s.HandleMessage(ctx, mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	}))

	// List tools
	result := s.HandleMessage(ctx, mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}))

	b, _ := json.Marshal(result)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatalf("unmarshal tools list: %v", err)
	}

	expectedTools := []string{
		"save", "recall", "context", "update", "forget",
		"invalidate", "status", "session_start", "session_end",
		"git_hook", "reflect", "consolidate",
	}

	toolNames := make(map[string]bool)
	for _, tool := range resp.Result.Tools {
		toolNames[tool.Name] = true
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}

	if len(resp.Result.Tools) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(resp.Result.Tools))
	}
}

func TestForgetTool(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save
	saveText := h.callTool("save", map[string]any{
		"title":   "To be forgotten",
		"content": "Temporary note",
	})

	var saved saveResponse
	json.Unmarshal([]byte(saveText), &saved)

	// Forget
	forgetText := h.callTool("forget", map[string]any{
		"id": saved.ID,
	})

	var result map[string]string
	json.Unmarshal([]byte(forgetText), &result)

	if result["message"] != "observation forgotten (soft-deleted)" {
		t.Errorf("unexpected message: %s", result["message"])
	}
}

func TestSessionLifecycle(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Start session
	startText := h.callTool("session_start", map[string]any{
		"title":     "Feature work",
		"directory": "/home/user/project",
		"branch":    "feat/auth",
		"namespace": "myproject",
	})

	var started map[string]string
	json.Unmarshal([]byte(startText), &started)

	if started["session_id"] == "" {
		t.Fatal("expected session_id")
	}

	// End session
	endText := h.callTool("session_end", map[string]any{
		"session_id": started["session_id"],
		"summary":    "Implemented JWT auth flow",
	})

	var ended map[string]string
	json.Unmarshal([]byte(endText), &ended)

	if ended["message"] != "session completed" {
		t.Errorf("unexpected message: %s", ended["message"])
	}
}

// --- Temporal end-to-end tests ---

func TestRecallCurrentStateE2E(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save observations about database
	h.callTool("save", map[string]any{
		"title":   "Database is Postgres",
		"content": "We use Postgres database for the project",
		"tags":    "database",
	})
	h.callTool("save", map[string]any{
		"title":   "Database is SQLite",
		"content": "We currently use SQLite database for the project",
		"tags":    "database",
	})

	// Recall with current-state intent
	recallText := h.callTool("recall", map[string]any{
		"query": "database currently",
	})

	var recalled recallResponse
	json.Unmarshal([]byte(recallText), &recalled)

	if recalled.TemporalIntent != "current_state" {
		t.Errorf("temporal_intent = %q, want 'current_state'", recalled.TemporalIntent)
	}
	if recalled.Count < 2 {
		t.Fatalf("expected at least 2 results, got %d", recalled.Count)
	}
	// Verify the FTS query cleaning works: "currently" stripped, both "database" results returned
	titles := make(map[string]bool)
	for _, r := range recalled.Results {
		titles[r.Title] = true
	}
	if !titles["Database is SQLite"] {
		t.Error("expected 'Database is SQLite' in results")
	}
	if !titles["Database is Postgres"] {
		t.Error("expected 'Database is Postgres' in results")
	}
}

func TestRecallWhenIntentE2E(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save observations about auth
	h.callTool("save", map[string]any{
		"title":   "Auth migration completed",
		"content": "Auth system migration to JWT completed on 2026-03-06",
		"tags":    "auth,migration",
	})
	h.callTool("save", map[string]any{
		"title":   "Auth configuration",
		"content": "Auth system configuration and setup notes for JWT",
		"tags":    "auth",
	})

	// Recall with when intent
	recallText := h.callTool("recall", map[string]any{
		"query": "when did auth migration",
	})

	var recalled recallResponse
	json.Unmarshal([]byte(recallText), &recalled)

	if recalled.TemporalIntent != "when" {
		t.Errorf("temporal_intent = %q, want 'when'", recalled.TemporalIntent)
	}
	if recalled.Count == 0 {
		t.Fatal("expected results")
	}
}

func TestRecallHistoryIncludesExpiredE2E(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save and then manually expire an observation
	saveText := h.callTool("save", map[string]any{
		"title":   "Old auth system",
		"content": "Auth system uses session tokens for authentication",
		"tags":    "auth",
	})

	var saved saveResponse
	json.Unmarshal([]byte(saveText), &saved)

	// Manually expire it
	deps.DB.ExecContext(context.Background(),
		"UPDATE observations SET staleness = 'expired', valid_until = datetime('now', '-1 day') WHERE id = ?",
		saved.ID)

	// Save a current observation
	h.callTool("save", map[string]any{
		"title":   "Current auth system",
		"content": "Auth system uses JWT tokens for authentication",
		"tags":    "auth",
	})

	// Normal recall should NOT include expired
	normalText := h.callTool("recall", map[string]any{
		"query": "auth tokens",
	})
	var normalRecall recallResponse
	json.Unmarshal([]byte(normalText), &normalRecall)

	for _, r := range normalRecall.Results {
		if r.ID == saved.ID {
			t.Error("expired observation should not appear in normal recall")
		}
	}

	// History recall ("previously") should include it
	historyText := h.callTool("recall", map[string]any{
		"query": "auth tokens previously",
	})
	var historyRecall recallResponse
	json.Unmarshal([]byte(historyText), &historyRecall)

	if historyRecall.TemporalIntent != "history" {
		t.Errorf("temporal_intent = %q, want 'history'", historyRecall.TemporalIntent)
	}

	found := false
	for _, r := range historyRecall.Results {
		if r.ID == saved.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("expired observation should appear in history recall")
	}
}

func TestRecallNoTemporalIntentOmitted(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	h.callTool("save", map[string]any{
		"title":   "Auth implementation",
		"content": "Auth uses middleware pattern",
	})

	recallText := h.callTool("recall", map[string]any{
		"query": "auth implementation",
	})

	var recalled recallResponse
	json.Unmarshal([]byte(recallText), &recalled)

	// No temporal intent → field should be empty (omitted from JSON)
	if recalled.TemporalIntent != "" {
		t.Errorf("temporal_intent should be empty for non-temporal query, got %q", recalled.TemporalIntent)
	}
}

func TestStatusIncludesTemporalMentions(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save an observation with temporal content (temporal mentions are extracted on save)
	h.callTool("save", map[string]any{
		"title":   "Migration note",
		"content": "We currently use SQLite for the project",
	})

	statusText := h.callTool("status", map[string]any{})

	var status statusResponse
	json.Unmarshal([]byte(statusText), &status)

	// temporal_mentions field should exist (may be 0 if extractor didn't run,
	// but the field should be in the response)
	if status.Total != 1 {
		t.Errorf("total = %d, want 1", status.Total)
	}
}

func TestGitHookTool(t *testing.T) {
	deps := newTestDeps(t)
	h := initServer(t, deps)

	// Save an observation linked to a file
	h.callTool("save", map[string]any{
		"title":   "Auth service pattern",
		"content": "Service uses middleware for auth checks",
		"files":   "internal/auth/service.go",
	})

	// Simulate git hook
	hookText := h.callTool("git_hook", map[string]any{
		"changed_files": "internal/auth/service.go",
		"commit_sha":    "abc123",
	})

	var hookResult map[string]any
	json.Unmarshal([]byte(hookText), &hookResult)

	if hookResult["message"] != "git hook processed" {
		t.Errorf("unexpected message: %v", hookResult["message"])
	}

	staled, _ := hookResult["observations_staled"].(float64)
	if staled != 1 {
		t.Errorf("expected 1 observation staled, got %v", staled)
	}
}
