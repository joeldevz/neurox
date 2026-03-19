package contradiction

import (
	"context"
	"path/filepath"
	"testing"

	"neurox/internal/db"
	"neurox/internal/embed"
	"neurox/internal/links"
	"neurox/internal/llm"
	"neurox/internal/observation"
)

type mockLLM struct {
	response string
	err      error
}

func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

func (m *mockLLM) Name() string { return "mock" }

func setupTest(t *testing.T) (*Detector, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	detector := NewDetector(database, embed.Disabled{}, llm.Disabled{}, linkStore)
	return detector, &db.TestDB{DB: database}
}

func TestRunWithoutEmbeddings(t *testing.T) {
	d, _ := setupTest(t)
	ctx := context.Background()

	// Without embeddings, should return zero results
	result, err := d.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Candidates != 0 {
		t.Errorf("candidates = %d, want 0", result.Candidates)
	}
}

func TestIsYes(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"YES", true},
		{"yes", true},
		{"Yes", true},
		{"YES - they contradict", true},
		{"NO", false},
		{"no", false},
		{"MAYBE", false},
		{"", false},
		{"Y", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isYes(tt.input)
			if got != tt.want {
				t.Errorf("isYes(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMarkSuperseded(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	// Insert two observations
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('NEW1', 'DB is Postgres 16', 'Migrated to Postgres 16', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OLD1', 'DB is Postgres 14', 'Using Postgres 14', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)

	err := d.markSuperseded(ctx, "NEW1", "OLD1")
	if err != nil {
		t.Fatalf("markSuperseded: %v", err)
	}

	// Verify old observation is expired
	var staleness string
	var validUntil, invalidatedBy *string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness, valid_until, invalidated_by FROM observations WHERE id = 'OLD1'").
		Scan(&staleness, &validUntil, &invalidatedBy)

	if staleness != "expired" {
		t.Errorf("staleness = %q, want 'expired'", staleness)
	}
	if validUntil == nil {
		t.Error("valid_until should be set")
	}
	if invalidatedBy == nil || *invalidatedBy != "NEW1" {
		t.Error("invalidated_by should be NEW1")
	}

	// Verify supersedes link exists
	linkList, err := d.linkStore.GetBySource(ctx, "NEW1", links.RelationSupersedes)
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(linkList) != 1 {
		t.Fatalf("expected 1 supersedes link, got %d", len(linkList))
	}
	if linkList[0].TargetID != "OLD1" {
		t.Errorf("link target = %q, want 'OLD1'", linkList[0].TargetID)
	}
}

func TestCreateQuestion(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	// Insert two observations
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('A1', 'Auth uses JWT', 'We use JWT for auth', 'decision', 1, 0.9, 0.8, 'semantic', 'myproject')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('B1', 'Auth uses sessions', 'We use session cookies', 'decision', 1, 0.9, 0.8, 'semantic', 'myproject')`)

	c := candidate{
		newID:      "A1",
		newTitle:   "Auth uses JWT",
		newContent: "We use JWT for auth",
		oldID:      "B1",
		oldTitle:   "Auth uses sessions",
		oldContent: "We use session cookies",
		similarity: 0.65,
	}

	err := d.createQuestion(ctx, c)
	if err != nil {
		t.Fatalf("createQuestion: %v", err)
	}

	// Verify question observation was created
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE observation_type = 'question' AND namespace = 'myproject' AND deleted_at IS NULL").Scan(&count)
	if count != 1 {
		t.Errorf("question count = %d, want 1", count)
	}

	// Verify contradicts link exists
	linkList, err := d.linkStore.GetBySource(ctx, "A1", links.RelationContradicts)
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(linkList) != 1 {
		t.Fatalf("expected 1 contradicts link, got %d", len(linkList))
	}
}

func TestResolveWithLLMYes(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	// Override LLM to return YES
	d.llm = &mockLLM{response: "YES"}

	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('NEW2', 'Postgres 16', 'Migrated', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OLD2', 'Postgres 14', 'Using 14', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)

	resolved, err := d.resolveWithLLM(ctx, candidate{
		newID: "NEW2", newTitle: "Postgres 16", newContent: "Migrated",
		oldID: "OLD2", oldTitle: "Postgres 14", oldContent: "Using 14",
	})
	if err != nil {
		t.Fatalf("resolveWithLLM: %v", err)
	}
	if !resolved {
		t.Error("expected resolved = true")
	}

	// Verify old is expired
	var staleness string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness FROM observations WHERE id = 'OLD2'").Scan(&staleness)
	if staleness != "expired" {
		t.Errorf("staleness = %q, want 'expired'", staleness)
	}
}

func TestResolveWithLLMNo(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	d.llm = &mockLLM{response: "NO"}

	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('A2', 'Feature A', 'Content A', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('B2', 'Feature B', 'Content B', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)

	resolved, err := d.resolveWithLLM(ctx, candidate{
		newID: "A2", newTitle: "Feature A", newContent: "Content A",
		oldID: "B2", oldTitle: "Feature B", oldContent: "Content B",
	})
	if err != nil {
		t.Fatalf("resolveWithLLM: %v", err)
	}
	if resolved {
		t.Error("expected resolved = false for NO response")
	}

	// Verify old is still fresh
	var staleness string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness FROM observations WHERE id = 'B2'").Scan(&staleness)
	if staleness != "fresh" {
		t.Errorf("staleness = %q, want 'fresh'", staleness)
	}
}
