package contradiction

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/links"
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

	detector := NewDetector(database, embed.Disabled{}, llm.Disabled{}, linkStore, 0, 0)
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
	}, nil, nil)
	if err != nil {
		t.Fatalf("resolveWithLLM: %v", err)
	}
	if !resolved {
		t.Error("expected resolved = true")
	}

	// Verify old is expired (no temporal context → hard supersession)
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
	}, nil, nil)
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

// --- Temporal-aware contradiction tests ---

func TestTemporalSupersessionPreservesAsStale(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('CUR1', 'DB is SQLite', 'Currently using SQLite', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OLD3', 'DB is Postgres', 'Using Postgres', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)

	err := d.markTemporalSupersession(ctx, "CUR1", "OLD3")
	if err != nil {
		t.Fatalf("markTemporalSupersession: %v", err)
	}

	// Verify old observation is stale (NOT expired)
	var staleness string
	var validUntil, invalidatedBy *string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness, valid_until, invalidated_by FROM observations WHERE id = 'OLD3'").
		Scan(&staleness, &validUntil, &invalidatedBy)

	if staleness != "stale" {
		t.Errorf("staleness = %q, want 'stale' (temporal supersession should preserve history)", staleness)
	}
	if validUntil == nil {
		t.Error("valid_until should be set")
	}
	if invalidatedBy == nil || *invalidatedBy != "CUR1" {
		t.Error("invalidated_by should be CUR1")
	}

	// Verify supersedes link exists
	linkList, err := d.linkStore.GetBySource(ctx, "CUR1", links.RelationSupersedes)
	if err != nil {
		t.Fatalf("get links: %v", err)
	}
	if len(linkList) != 1 {
		t.Fatalf("expected 1 supersedes link, got %d", len(linkList))
	}
}

func TestResolveWithLLMUsesTemporalSupersessionWhenContextExists(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	d.llm = &mockLLM{response: "YES"}

	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('NEW3', 'Now using SQLite', 'Migrated to SQLite', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OLD4', 'Using Postgres', 'We use Postgres', 'decision', 1, 0.9, 0.8, 'semantic', 'default')`)

	// Provide temporal mentions → should use temporal supersession
	march := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	newMentions := []temporal.Mention{{Kind: temporal.KindCurrentState}}
	oldMentions := []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}}

	resolved, err := d.resolveWithLLM(ctx, candidate{
		newID: "NEW3", newTitle: "Now using SQLite", newContent: "Migrated to SQLite",
		oldID: "OLD4", oldTitle: "Using Postgres", oldContent: "We use Postgres",
	}, newMentions, oldMentions)
	if err != nil {
		t.Fatalf("resolveWithLLM: %v", err)
	}
	if !resolved {
		t.Error("expected resolved = true")
	}

	// Verify old is stale (not expired) because temporal context triggered soft supersession
	var staleness string
	tdb.DB.QueryRowContext(ctx, "SELECT staleness FROM observations WHERE id = 'OLD4'").Scan(&staleness)
	if staleness != "stale" {
		t.Errorf("staleness = %q, want 'stale' (temporal context should trigger soft supersession)", staleness)
	}
}

func TestIsTemporalSequence(t *testing.T) {
	march := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		newMentions []temporal.Mention
		oldMentions []temporal.Mention
		want        bool
	}{
		{
			name: "no mentions → false",
			want: false,
		},
		{
			name:        "only new mentions → false",
			newMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			want:        false,
		},
		{
			name:        "only old mentions → false",
			oldMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &jan}},
			want:        false,
		},
		{
			name:        "new after old → true",
			newMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			oldMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &jan}},
			want:        true,
		},
		{
			name:        "old after new → false",
			newMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &jan}},
			oldMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}},
			want:        false,
		},
		{
			name:        "new is current_state + old has date → true",
			newMentions: []temporal.Mention{{Kind: temporal.KindCurrentState}},
			oldMentions: []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &jan}},
			want:        true,
		},
		{
			name:        "both current_state no dates → false",
			newMentions: []temporal.Mention{{Kind: temporal.KindCurrentState}},
			oldMentions: []temporal.Mention{{Kind: temporal.KindCurrentState}},
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTemporalSequence(tt.newMentions, tt.oldMentions)
			if got != tt.want {
				t.Errorf("isTemporalSequence() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildContradictionPromptWithTemporalContext(t *testing.T) {
	c := candidate{
		newTitle:   "DB is SQLite",
		newContent: "Using SQLite now",
		oldTitle:   "DB is Postgres",
		oldContent: "Using Postgres",
	}

	march := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	d, _ := setupTest(t)

	// Without temporal context
	prompt1 := d.buildContradictionPrompt(c, nil, nil)
	if !containsStr(prompt1, "contradict each other") {
		t.Error("prompt without temporal context should ask about contradiction")
	}
	if containsStr(prompt1, "Temporal context") {
		t.Error("prompt without temporal context should not include temporal section")
	}

	// With temporal context
	newM := []temporal.Mention{{Kind: temporal.KindCurrentState}}
	oldM := []temporal.Mention{{Kind: temporal.KindAbsolute, NormalizedStart: &march}}
	prompt2 := d.buildContradictionPrompt(c, newM, oldM)
	if !containsStr(prompt2, "Temporal context") {
		t.Error("prompt with temporal context should include temporal section")
	}
	if !containsStr(prompt2, "supersedes") {
		t.Error("prompt with temporal context should ask about supersession")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestFindCandidates verifies that findCandidates correctly filters observation pairs
// based on cosine similarity thresholds.
// - Pairs with similarity in [0.65, 0.85) SHOULD be returned as candidates
// - Pairs with similarity in [0.50, 0.65) should NOT be returned
func TestFindCandidates(t *testing.T) {
	d, tdb := setupTest(t)
	ctx := context.Background()

	// Create normalized embedding vectors with specific cosine similarities
	// For simplicity, we use 2D vectors where we can control the angle between them
	// Vector A: [1, 0] (normalized)
	// Vector B: angle ~49° → cos(49°) ≈ 0.656 → in [0.65, 0.85) range → SHOULD be candidate
	// Vector C: angle ~60° → cos(60°) = 0.50 → in [0.50, 0.65) range → should NOT be candidate
	// Vector D: angle ~75° → cos(75°) ≈ 0.259 → below 0.50 → should NOT be candidate

	vecA := []float32{1.0, 0.0}
	vecB := []float32{0.656, 0.7547} // cos(49°) ≈ 0.656
	vecC := []float32{0.50, 0.866}   // cos(60°) = 0.50
	vecD := []float32{0.259, 0.966}  // cos(75°) ≈ 0.259

	// Verify our vectors have the expected similarities
	simAB := embed.CosineSimilarity(vecA, vecB)
	simAC := embed.CosineSimilarity(vecA, vecC)
	simAD := embed.CosineSimilarity(vecA, vecD)

	t.Logf("Cosine similarities: A-B=%.4f, A-C=%.4f, A-D=%.4f", simAB, simAC, simAD)

	// Insert observations with embeddings
	// OBS1 has embedding A
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS1', 'Test A', 'Content A', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	tdb.Exec(t, `UPDATE observations SET embedding = ? WHERE id = 'OBS1'`, embed.SerializeF32(vecA))

	// OBS2 has embedding B (similarity ~0.656 with A → in [0.65, 0.85) → SHOULD be candidate)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS2', 'Test B', 'Content B', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	tdb.Exec(t, `UPDATE observations SET embedding = ? WHERE id = 'OBS2'`, embed.SerializeF32(vecB))

	// OBS3 has embedding C (similarity 0.50 with A → in [0.50, 0.65) → should NOT be candidate)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS3', 'Test C', 'Content C', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	tdb.Exec(t, `UPDATE observations SET embedding = ? WHERE id = 'OBS3'`, embed.SerializeF32(vecC))

	// OBS4 has embedding D (similarity ~0.259 with A → below 0.50 → should NOT be candidate)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS4', 'Test D', 'Content D', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	tdb.Exec(t, `UPDATE observations SET embedding = ? WHERE id = 'OBS4'`, embed.SerializeF32(vecD))

	// OBS5 has embedding similar to B (similarity ~0.75 with B → in [0.65, 0.85) → SHOULD be candidate)
	// This creates another candidate pair: OBS2-OBS5
	vecE := []float32{0.707, 0.707} // 45° → cos(45°) ≈ 0.707
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS5', 'Test E', 'Content E', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	tdb.Exec(t, `UPDATE observations SET embedding = ? WHERE id = 'OBS5'`, embed.SerializeF32(vecE))

	simBE := embed.CosineSimilarity(vecB, vecE)
	t.Logf("Cosine similarity B-E: %.4f", simBE)

	// Call findCandidates
	candidates, err := d.findCandidates(ctx)
	if err != nil {
		t.Fatalf("findCandidates: %v", err)
	}

	// Build a set of candidate pairs for easier checking
	candidatePairs := make(map[string]bool)
	for _, c := range candidates {
		// Always store with smaller ID first for consistent comparison
		pair := c.newID + ":" + c.oldID
		candidatePairs[pair] = true
		t.Logf("Found candidate pair: %s (similarity: %.4f)", pair, c.similarity)
	}

	// Verify that OBS1-OBS2 IS a candidate (similarity ~0.656 in [0.65, 0.85))
	// Note: OBS1 is newer (inserted first in the query order by created_at DESC, but actually
	// they have same timestamp - let's check the actual ordering)
	foundOBS1OBS2 := false
	for _, c := range candidates {
		if (c.newID == "OBS1" && c.oldID == "OBS2") || (c.newID == "OBS2" && c.oldID == "OBS1") {
			foundOBS1OBS2 = true
			if c.similarity < 0.65 || c.similarity >= 0.85 {
				t.Errorf("OBS1-OBS2 similarity %.4f outside expected range [0.65, 0.85)", c.similarity)
			}
			break
		}
	}
	if !foundOBS1OBS2 {
		t.Errorf("OBS1-OBS2 should be a candidate (similarity ~%.4f in [0.65, 0.85))", simAB)
	}

	// Verify that OBS1-OBS3 is NOT a candidate (similarity 0.50 in [0.50, 0.65))
	foundOBS1OBS3 := false
	for _, c := range candidates {
		if (c.newID == "OBS1" && c.oldID == "OBS3") || (c.newID == "OBS3" && c.oldID == "OBS1") {
			foundOBS1OBS3 = true
			break
		}
	}
	if foundOBS1OBS3 {
		t.Errorf("OBS1-OBS3 should NOT be a candidate (similarity %.4f in [0.50, 0.65))", simAC)
	}

	// Verify that OBS1-OBS4 is NOT a candidate (similarity ~0.259 below 0.50)
	foundOBS1OBS4 := false
	for _, c := range candidates {
		if (c.newID == "OBS1" && c.oldID == "OBS4") || (c.newID == "OBS4" && c.oldID == "OBS1") {
			foundOBS1OBS4 = true
			break
		}
	}
	if foundOBS1OBS4 {
		t.Errorf("OBS1-OBS4 should NOT be a candidate (similarity %.4f below 0.50)", simAD)
	}

	// We should have at least OBS1-OBS2 as a candidate
	// OBS2-OBS5 should also be a candidate (similarity ~0.75)
	if len(candidates) < 1 {
		t.Errorf("expected at least 1 candidate, got %d", len(candidates))
	}
}

// TestDetectorThresholds verifies that the detector respects custom minSim/maxSim thresholds.
// With minSim=0.70, a pair with sim=0.65 should NOT be a candidate.
func TestDetectorThresholds(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Create detector with custom threshold minSim=0.70
	detector := NewDetector(database, embed.Disabled{}, llm.Disabled{}, linkStore, 0.70, 0.85)

	ctx := context.Background()

	// Create normalized embedding vectors
	// Vector A: [1, 0] (normalized)
	// Vector B: angle ~49° → cos(49°) ≈ 0.656 → below 0.70 → should NOT be candidate
	// Vector C: angle ~45° → cos(45°) ≈ 0.707 → above 0.70 → SHOULD be candidate

	vecA := []float32{1.0, 0.0}
	vecB := []float32{0.656, 0.7547} // cos(49°) ≈ 0.656
	vecC := []float32{0.707, 0.707}  // cos(45°) ≈ 0.707

	// Verify our vectors have the expected similarities
	simAB := embed.CosineSimilarity(vecA, vecB)
	simAC := embed.CosineSimilarity(vecA, vecC)
	t.Logf("Cosine similarities: A-B=%.4f, A-C=%.4f", simAB, simAC)

	// Insert observations with embeddings
	database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_A', 'Test A', 'Content A', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	database.ExecContext(ctx, `UPDATE observations SET embedding = ? WHERE id = 'OBS_A'`, embed.SerializeF32(vecA))

	database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_B', 'Test B', 'Content B', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	database.ExecContext(ctx, `UPDATE observations SET embedding = ? WHERE id = 'OBS_B'`, embed.SerializeF32(vecB))

	database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('OBS_C', 'Test C', 'Content C', 'discovery', 1, 0.9, 0.8, 'semantic', 'testns')`)
	database.ExecContext(ctx, `UPDATE observations SET embedding = ? WHERE id = 'OBS_C'`, embed.SerializeF32(vecC))

	// Call findCandidates
	candidates, err := detector.findCandidates(ctx)
	if err != nil {
		t.Fatalf("findCandidates: %v", err)
	}

	// Check that OBS_A-OBS_B is NOT a candidate (sim ~0.656 < 0.70)
	foundAB := false
	for _, c := range candidates {
		if (c.newID == "OBS_A" && c.oldID == "OBS_B") || (c.newID == "OBS_B" && c.oldID == "OBS_A") {
			foundAB = true
			break
		}
	}
	if foundAB {
		t.Errorf("OBS_A-OBS_B should NOT be a candidate with minSim=0.70 (similarity ~%.4f)", simAB)
	}

	// Check that OBS_A-OBS_C IS a candidate (sim ~0.707 > 0.70)
	foundAC := false
	for _, c := range candidates {
		if (c.newID == "OBS_A" && c.oldID == "OBS_C") || (c.newID == "OBS_C" && c.oldID == "OBS_A") {
			foundAC = true
			if c.similarity < 0.70 || c.similarity > 0.85 {
				t.Errorf("OBS_A-OBS_C similarity %.4f outside expected range [0.70, 0.85]", c.similarity)
			}
			break
		}
	}
	if !foundAC {
		t.Errorf("OBS_A-OBS_C SHOULD be a candidate with minSim=0.70 (similarity ~%.4f)", simAC)
	}
}
