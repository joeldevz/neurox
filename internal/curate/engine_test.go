package curate

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
)

// newTestDB opens a fresh in-memory (temp file) SQLite database with the full
// schema applied and registers a t.Cleanup to close it.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	response string
	err      error
}

func (m mockProvider) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

func (m mockProvider) Name() string { return "mock" }

// insertObs is a test helper that inserts a minimal observation row.
func insertObs(t *testing.T, database *sql.DB, id, namespace string, importance float64) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES(?, ?, ?, 'discovery', 1, 0.7, ?, 'semantic', ?)
	`, id, "title-"+id, "content-"+id, importance, namespace)
	if err != nil {
		t.Fatalf("insert observation %q: %v", id, err)
	}
}

// newTestEngine creates an Engine wired to the given DB and mock provider.
func newTestEngine(database *sql.DB, provider mockProvider) *Engine {
	return NewEngine(database, provider, Priorities{Namespaced: map[string][]string{}}, "mock-model")
}

// ─────────────────────────────────────────────────────────────────────────────
// TestParseDecisions
// ─────────────────────────────────────────────────────────────────────────────

func TestParseDecisions(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantCount  int
		wantAction string // first decision's action (only checked when wantErr=false)
		wantErr    bool
	}{
		{
			name:       "valid JSON array",
			input:      `[{"id":"OBS001","action":"DELETE","new_importance":0,"reason":"junk"}]`,
			wantCount:  1,
			wantAction: "DELETE",
		},
		{
			name: "JSON wrapped in markdown code fences",
			input: "```json\n" +
				`[{"id":"OBS001","action":"KEEP","new_importance":0.8,"reason":"useful"}]` +
				"\n```",
			wantCount:  1,
			wantAction: "KEEP",
		},
		{
			name: "JSON with surrounding explanation text",
			input: "Here are my decisions:\n" +
				`[{"id":"OBS001","action":"KEEP","new_importance":0.5,"reason":"ok"}]` +
				"\nHope that helps!",
			wantCount:  1,
			wantAction: "KEEP",
		},
		{
			name:    "no JSON array in response",
			input:   "sorry I can't do that",
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   "[{bad json}]",
			wantErr: true,
		},
		{
			name:      "empty array returns empty slice no error",
			input:     "[]",
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDecisions(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Errorf("len(decisions) = %d, want %d", len(got), tc.wantCount)
			}
			if tc.wantCount > 0 && got[0].Action != tc.wantAction {
				t.Errorf("decisions[0].Action = %q, want %q", got[0].Action, tc.wantAction)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_DELETE
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_DELETE(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "OBS001", "test", 0.5)
	insertObs(t, database, "OBS002", "test", 0.5)

	resp := `[{"id":"OBS001","action":"DELETE","new_importance":0,"reason":"junk"},{"id":"OBS002","action":"KEEP","new_importance":0.85,"reason":"useful"}]`
	engine := newTestEngine(database, mockProvider{response: resp})

	report, err := engine.CurateNamespace(context.Background(), "test", false)
	if err != nil {
		t.Fatalf("CurateNamespace: %v", err)
	}

	if report.Before != 2 {
		t.Errorf("Before = %d, want 2", report.Before)
	}
	if report.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1", report.Deleted)
	}
	if report.Recalibrated != 1 {
		t.Errorf("Recalibrated = %d, want 1", report.Recalibrated)
	}

	// OBS001 must be soft-deleted (deleted_at IS NOT NULL).
	var deletedAt sql.NullString
	database.QueryRowContext(context.Background(),
		"SELECT deleted_at FROM observations WHERE id = 'OBS001'").Scan(&deletedAt)
	if !deletedAt.Valid {
		t.Error("OBS001: expected deleted_at to be set, got NULL")
	}

	// OBS002 must still be alive.
	database.QueryRowContext(context.Background(),
		"SELECT deleted_at FROM observations WHERE id = 'OBS002'").Scan(&deletedAt)
	if deletedAt.Valid {
		t.Error("OBS002: expected deleted_at to be NULL, got a value")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_KEEP_updates_importance
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_KEEP_updates_importance(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "OBS001", "test", 0.1)

	resp := `[{"id":"OBS001","action":"KEEP","new_importance":0.85,"reason":"useful"}]`
	engine := newTestEngine(database, mockProvider{response: resp})

	_, err := engine.CurateNamespace(context.Background(), "test", false)
	if err != nil {
		t.Fatalf("CurateNamespace: %v", err)
	}

	var importance float64
	database.QueryRowContext(context.Background(),
		"SELECT importance FROM observations WHERE id = 'OBS001'").Scan(&importance)
	if importance != 0.85 {
		t.Errorf("importance = %.3f, want 0.85", importance)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_DryRun
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_DryRun(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "OBS001", "test", 0.5)
	insertObs(t, database, "OBS002", "test", 0.5)

	resp := `[{"id":"OBS001","action":"DELETE","new_importance":0,"reason":"junk"},{"id":"OBS002","action":"DELETE","new_importance":0,"reason":"also junk"}]`
	engine := newTestEngine(database, mockProvider{response: resp})

	report, err := engine.CurateNamespace(context.Background(), "test", true)
	if err != nil {
		t.Fatalf("CurateNamespace dryRun: %v", err)
	}

	// Report still accounts for the decisions.
	if report.Deleted != 2 {
		t.Errorf("report.Deleted = %d, want 2", report.Deleted)
	}

	// No rows should actually be deleted.
	var deletedAt sql.NullString
	for _, id := range []string{"OBS001", "OBS002"} {
		database.QueryRowContext(context.Background(),
			"SELECT deleted_at FROM observations WHERE id = ?", id).Scan(&deletedAt)
		if deletedAt.Valid {
			t.Errorf("%s: deleted_at should be NULL in dry-run, got %q", id, deletedAt.String)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_EmptyNamespace
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_EmptyNamespace(t *testing.T) {
	database := newTestDB(t)
	engine := newTestEngine(database, mockProvider{response: "[]"})

	report, err := engine.CurateNamespace(context.Background(), "empty", false)
	if err != nil {
		t.Fatalf("CurateNamespace: %v", err)
	}
	if report.Before != 0 {
		t.Errorf("Before = %d, want 0", report.Before)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_LLMError
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_LLMError(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "OBS001", "test", 0.5)

	engine := newTestEngine(database, mockProvider{err: errLLM("provider unavailable")})

	_, err := engine.CurateNamespace(context.Background(), "test", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !containsStr(err.Error(), "llm complete") {
		t.Errorf("error %q does not contain %q", err.Error(), "llm complete")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateNamespace_MalformedResponse
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateNamespace_MalformedResponse(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "OBS001", "test", 0.5)

	engine := newTestEngine(database, mockProvider{response: "sorry I can't do that"})

	_, err := engine.CurateNamespace(context.Background(), "test", false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !containsStr(err.Error(), "parse decisions") {
		t.Errorf("error %q does not contain %q", err.Error(), "parse decisions")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCurateAll
// ─────────────────────────────────────────────────────────────────────────────

func TestCurateAll(t *testing.T) {
	database := newTestDB(t)
	insertObs(t, database, "NS1OBS001", "ns1", 0.5)
	insertObs(t, database, "NS2OBS001", "ns2", 0.5)

	// The mock must return valid decisions that reference the actual IDs.
	// CurateAll calls CurateNamespace per namespace, each gets its own Complete call,
	// but we only have a single response. We return a generic valid empty array so
	// both namespaces succeed without errors.
	resp := `[{"id":"NS1OBS001","action":"KEEP","new_importance":0.6,"reason":"ok"},{"id":"NS2OBS001","action":"KEEP","new_importance":0.7,"reason":"ok"}]`
	engine := newTestEngine(database, mockProvider{response: resp})

	full, err := engine.CurateAll(context.Background(), false)
	if err != nil {
		t.Fatalf("CurateAll: %v", err)
	}

	if len(full.Namespaces) != 2 {
		t.Errorf("len(Namespaces) = %d, want 2", len(full.Namespaces))
	}
	if full.TotalBefore != 2 {
		t.Errorf("TotalBefore = %d, want 2", full.TotalBefore)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestClamp
// ─────────────────────────────────────────────────────────────────────────────

func TestClamp(t *testing.T) {
	tests := []struct {
		v, min, max float64
		want        float64
	}{
		{v: -0.5, min: 0, max: 1, want: 0},
		{v: 1.5, min: 0, max: 1, want: 1},
		{v: 0.5, min: 0, max: 1, want: 0.5},
	}
	for _, tc := range tests {
		got := clamp(tc.v, tc.min, tc.max)
		if got != tc.want {
			t.Errorf("clamp(%.2f, %.2f, %.2f) = %.2f, want %.2f", tc.v, tc.min, tc.max, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// errLLM is a simple sentinel error type for the mock LLM.
type errLLM string

func (e errLLM) Error() string { return string(e) }

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
