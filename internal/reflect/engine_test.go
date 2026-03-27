package reflect

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
)

type mockLLM struct {
	response      string
	err           error
	capturePrompt *string // if set, captures the prompt sent to Complete
}

func (m *mockLLM) Complete(_ context.Context, prompt string) (string, error) {
	if m.capturePrompt != nil {
		*m.capturePrompt = prompt
	}
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

func TestForceReflectCooldown(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert enough observations for ForceReflect
	for i := 0; i < 5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'cooldownns')`, fmt.Sprintf("COOL%04d", i))
	}

	// First call: should create a reflection
	result1, err := e.ForceReflect(ctx, "cooldownns")
	if err != nil {
		t.Fatalf("first force reflect: %v", err)
	}
	if result1.ReflectionsCreated != 1 {
		t.Fatalf("first call: expected 1 reflection, got %d", result1.ReflectionsCreated)
	}

	// Second call immediately: should return 0 due to cooldown
	result2, err := e.ForceReflect(ctx, "cooldownns")
	if err != nil {
		t.Fatalf("second force reflect: %v", err)
	}
	if result2.ReflectionsCreated != 0 {
		t.Errorf("second call (cooldown): expected 0 reflections, got %d", result2.ReflectionsCreated)
	}

	// Verify only 1 reflection exists in DB
	var count int
	tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reflections WHERE namespace = 'cooldownns'").Scan(&count)
	if count != 1 {
		t.Errorf("reflections in DB = %d, want 1", count)
	}
}

func idFromInt(i int) string {
	return fmt.Sprintf("OBS%04d", i)
}

func TestExtractTitleWithPrefix(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Mock LLM that includes TITLE: prefix
	mockResponse := `TITLE: Pattern: Temporal Context Improves Memory Recall

**Insight 1:** First insight about patterns.
**Insight 2:** Second insight about architecture.
**Insight 3:** Third insight about testing.`
	mock := &mockLLM{response: mockResponse}
	engine := NewEngine(database, mock, linkStore, idGen)

	ctx := context.Background()

	// Insert enough observations for ForceReflect
	for i := 0; i < 5; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'titletest')`, fmt.Sprintf("TTL%04d", i))
	}

	result, err := engine.ForceReflect(ctx, "titletest")
	if err != nil {
		t.Fatalf("force reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Fatalf("expected 1 reflection, got %d", result.ReflectionsCreated)
	}

	// Verify the extracted title was saved
	var title string
	err = database.QueryRowContext(ctx, `SELECT title FROM observations WHERE source = 'reflection' AND namespace = 'titletest' AND deleted_at IS NULL`).Scan(&title)
	if err != nil {
		t.Fatalf("query title: %v", err)
	}
	wantTitle := "Pattern: Temporal Context Improves Memory Recall"
	if title != wantTitle {
		t.Errorf("title = %q, want %q", title, wantTitle)
	}

	// Verify the body does NOT contain the TITLE: line
	var content string
	err = database.QueryRowContext(ctx, `SELECT content FROM observations WHERE source = 'reflection' AND namespace = 'titletest' AND deleted_at IS NULL`).Scan(&content)
	if err != nil {
		t.Fatalf("query content: %v", err)
	}
	if strings.Contains(content, "TITLE:") {
		t.Errorf("content should not contain TITLE: prefix, got: %s", content)
	}
	if !strings.HasPrefix(content, "**Insight 1:**") {
		t.Errorf("content should start with **Insight 1:**, got: %s", content)
	}
}

func TestExtractTitleFallback(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Mock LLM that omits TITLE: prefix (old format)
	mockResponse := `1. Insight one: patterns emerge from codebase analysis.
2. Insight two: architecture follows clean design.
3. Insight three: testing covers all critical paths.`
	mock := &mockLLM{response: mockResponse}
	engine := NewEngine(database, mock, linkStore, idGen)

	ctx := context.Background()

	// Insert enough observations for ForceReflect
	for i := 0; i < 5; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'fallbackns')`, fmt.Sprintf("FBL%04d", i))
	}

	result, err := engine.ForceReflect(ctx, "fallbackns")
	if err != nil {
		t.Fatalf("force reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Fatalf("expected 1 reflection, got %d", result.ReflectionsCreated)
	}

	// Verify the fallback title was saved
	var title string
	err = database.QueryRowContext(ctx, `SELECT title FROM observations WHERE source = 'reflection' AND namespace = 'fallbackns' AND deleted_at IS NULL`).Scan(&title)
	if err != nil {
		t.Fatalf("query title: %v", err)
	}
	wantTitle := "Synthesis: fallbackns"
	if title != wantTitle {
		t.Errorf("title = %q, want %q", title, wantTitle)
	}

	// Verify the full content was preserved as body
	var content string
	err = database.QueryRowContext(ctx, `SELECT content FROM observations WHERE source = 'reflection' AND namespace = 'fallbackns' AND deleted_at IS NULL`).Scan(&content)
	if err != nil {
		t.Fatalf("query content: %v", err)
	}
	if content != mockResponse {
		t.Errorf("content should be full original content when no TITLE: prefix\ngot: %s\nwant: %s", content, mockResponse)
	}
}

func TestGetActiveReflectionNoExisting(t *testing.T) {
	e, _ := setupTest(t)
	ctx := context.Background()

	// No reflection exists yet
	active, err := e.getActiveReflection(ctx, "nonexistentns")
	if err != nil {
		t.Fatalf("getActiveReflection: %v", err)
	}
	if active != nil {
		t.Errorf("expected nil when no active reflection, got %+v", active)
	}
}

func TestGetActiveReflectionReturnsMostRecent(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert first reflection observation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, retention, created_at)
		VALUES('REF001', 'First Reflection', 'First content', 'pattern', 2, 0.9, 0.9, 'semantic', 'testns', 'reflection', 'durable', datetime('now', '-2 hours'))`)

	// Insert second (more recent) reflection observation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, retention, created_at)
		VALUES('REF002', 'Second Reflection', 'Second content', 'pattern', 2, 0.9, 0.9, 'semantic', 'testns', 'reflection', 'durable', datetime('now'))`)

	active, err := e.getActiveReflection(ctx, "testns")
	if err != nil {
		t.Fatalf("getActiveReflection: %v", err)
	}
	if active == nil {
		t.Fatal("expected active reflection, got nil")
	}
	if active.id != "REF002" {
		t.Errorf("expected most recent reflection REF002, got %s", active.id)
	}
	if active.title != "Second Reflection" {
		t.Errorf("expected title 'Second Reflection', got %s", active.title)
	}
	if active.content != "Second content" {
		t.Errorf("expected content 'Second content', got %s", active.content)
	}
}

func TestGetActiveReflectionIgnoresSoftDeleted(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert a soft-deleted reflection
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, retention, deleted_at)
		VALUES('REF001', 'Deleted Reflection', 'Deleted content', 'pattern', 2, 0.9, 0.9, 'semantic', 'testns', 'reflection', 'durable', datetime('now'))`)

	// Insert an active reflection
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, retention)
		VALUES('REF002', 'Active Reflection', 'Active content', 'pattern', 2, 0.9, 0.9, 'semantic', 'testns', 'reflection', 'durable')`)

	active, err := e.getActiveReflection(ctx, "testns")
	if err != nil {
		t.Fatalf("getActiveReflection: %v", err)
	}
	if active == nil {
		t.Fatal("expected active reflection, got nil")
	}
	if active.id != "REF002" {
		t.Errorf("expected active reflection REF002, got %s", active.id)
	}
}

func TestSynthesizeWithoutPreviousReflection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Track the prompt sent to LLM
	var capturedPrompt string
	mock := &mockLLM{
		response:      "TITLE: Test Synthesis\n\n**Insight 1:** First insight.",
		capturePrompt: &capturedPrompt,
	}

	engine := NewEngine(database, mock, linkStore, idGen)
	ctx := context.Background()

	sources := []sourceObs{
		{id: "OBS001", title: "Test Obs", content: "Test content", obsType: "discovery"},
	}

	_, err = engine.synthesize(ctx, sources, "testns", nil)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	// Verify the prompt does NOT contain previous reflection context
	if strings.Contains(capturedPrompt, "Previous Active Reflection") {
		t.Error("prompt should NOT contain 'Previous Active Reflection' when no previous reflection exists")
	}
	// Verify the prompt contains the expected base structure
	if !strings.Contains(capturedPrompt, "memory synthesis engine") {
		t.Error("prompt should contain base synthesis instructions")
	}
}

func TestSynthesizeWithPreviousReflection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)

	// Track the prompt sent to LLM
	var capturedPrompt string
	mock := &mockLLM{
		response:      "TITLE: Enriched Synthesis\n\n**Insight 1:** Enhanced insight.",
		capturePrompt: &capturedPrompt,
	}

	engine := NewEngine(database, mock, linkStore, idGen)
	ctx := context.Background()

	sources := []sourceObs{
		{id: "OBS001", title: "Test Obs", content: "Test content", obsType: "discovery"},
	}

	prev := &activeReflection{
		id:      "PREV001",
		title:   "Previous Reflection Title",
		content: "Previous reflection content for context",
	}

	_, err = engine.synthesize(ctx, sources, "testns", prev)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	// Verify the prompt contains previous reflection context
	if !strings.Contains(capturedPrompt, "Previous Active Reflection") {
		t.Error("prompt should contain 'Previous Active Reflection' when previous reflection exists")
	}
	if !strings.Contains(capturedPrompt, "Previous Reflection Title") {
		t.Error("prompt should contain the previous reflection title")
	}
	if !strings.Contains(capturedPrompt, "Previous reflection content for context") {
		t.Error("prompt should contain the previous reflection content")
	}
	// Verify reconsolidation instruction is present
	if !strings.Contains(capturedPrompt, "Build upon and enrich the previous reflection") {
		t.Error("prompt should contain instruction to build upon previous reflection")
	}
}

// TestFirstReflectionNoSupersedes verifies that the first reflection in a namespace
// does not create a supersedes link.
func TestFirstReflectionNoSupersedes(t *testing.T) {
	e, tdb := setupTest(t)
	ctx := context.Background()

	// Insert enough observations for ForceReflect
	for i := 0; i < 5; i++ {
		tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'firstns')`, fmt.Sprintf("FST%04d", i))
	}

	result, err := e.ForceReflect(ctx, "firstns")
	if err != nil {
		t.Fatalf("force reflect: %v", err)
	}
	if result.ReflectionsCreated != 1 {
		t.Fatalf("expected 1 reflection, got %d", result.ReflectionsCreated)
	}

	// Verify exactly 1 active reflection observation exists
	var activeCount int
	tdb.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE namespace='firstns' AND source='reflection' AND deleted_at IS NULL`).Scan(&activeCount)
	if activeCount != 1 {
		t.Errorf("active reflections = %d, want 1", activeCount)
	}

	// Verify no supersedes links exist
	var supersedesCount int
	tdb.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_links WHERE relation_type='supersedes'`).Scan(&supersedesCount)
	if supersedesCount != 0 {
		t.Errorf("supersedes links = %d, want 0", supersedesCount)
	}

	// Verify 1 row in reflections table (append-only history)
	var reflectionCount int
	tdb.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM reflections WHERE namespace='firstns'`).Scan(&reflectionCount)
	if reflectionCount != 1 {
		t.Errorf("reflections rows = %d, want 1", reflectionCount)
	}
}

// TestSecondReflectionCreatesSupersedes verifies that a second reflection
// creates a supersedes link and soft-deletes the previous observation.
func TestSecondReflectionCreatesSupersedes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mock := &mockLLM{response: "TITLE: Test Reflection\n\n**Insight 1:** First insight about patterns.\n**Insight 2:** Second insight about architecture.\n**Insight 3:** Third insight about testing."}

	// Create engine with modified cooldown for testing
	engine := NewEngine(database, mock, linkStore, idGen)

	ctx := context.Background()

	// Insert enough observations for first reflection
	for i := 0; i < 5; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'secondns')`, fmt.Sprintf("SND%04d", i))
	}

	// First reflection
	result1, err := engine.ForceReflect(ctx, "secondns")
	if err != nil {
		t.Fatalf("first force reflect: %v", err)
	}
	if result1.ReflectionsCreated != 1 {
		t.Fatalf("first: expected 1 reflection, got %d", result1.ReflectionsCreated)
	}

	// Get the ID of the first reflection observation
	var firstObsID string
	database.QueryRowContext(ctx, `SELECT id FROM observations WHERE namespace='secondns' AND source='reflection' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`).Scan(&firstObsID)
	if firstObsID == "" {
		t.Fatal("could not get first reflection observation ID")
	}

	// Add more observations for second reflection
	for i := 5; i < 10; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'secondns')`, fmt.Sprintf("SND%04d", i))
	}

	// Update the first reflection's created_at to be older than cooldown
	database.ExecContext(ctx, `UPDATE reflections SET created_at = datetime('now', '-3 hours') WHERE namespace='secondns'`)

	// Now force reflect should work (cooldown check passes due to old reflection)
	result2, err := engine.ForceReflect(ctx, "secondns")
	if err != nil {
		t.Fatalf("second force reflect: %v", err)
	}
	if result2.ReflectionsCreated != 1 {
		t.Fatalf("second: expected 1 reflection, got %d", result2.ReflectionsCreated)
	}

	// Verify exactly 1 active reflection observation exists
	var activeCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE namespace='secondns' AND source='reflection' AND deleted_at IS NULL`).Scan(&activeCount)
	if activeCount != 1 {
		t.Errorf("active reflections = %d, want 1", activeCount)
	}

	// Verify the first observation is now soft-deleted
	var firstDeleted bool
	database.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM observations WHERE id = ?`, firstObsID).Scan(&firstDeleted)
	if !firstDeleted {
		t.Error("first reflection observation should be soft-deleted")
	}

	// Verify exactly 1 supersedes link exists
	var supersedesCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_links WHERE relation_type='supersedes'`).Scan(&supersedesCount)
	if supersedesCount != 1 {
		t.Errorf("supersedes links = %d, want 1", supersedesCount)
	}

	// Verify the supersedes link points from new to old
	var sourceID, targetID string
	database.QueryRowContext(ctx, `SELECT source_id, target_id FROM observation_links WHERE relation_type='supersedes' LIMIT 1`).Scan(&sourceID, &targetID)
	if targetID != firstObsID {
		t.Errorf("supersedes target = %s, want %s (the first reflection)", targetID, firstObsID)
	}

	// Verify 2 rows in reflections table (append-only)
	var reflectionCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM reflections WHERE namespace='secondns'`).Scan(&reflectionCount)
	if reflectionCount != 2 {
		t.Errorf("reflections rows = %d, want 2", reflectionCount)
	}
}

// TestMultipleReconsolidationsUniqueness verifies that after multiple reconsolidations,
// there is always exactly 1 active reflection per namespace.
func TestMultipleReconsolidationsUniqueness(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mock := &mockLLM{response: "TITLE: Test Reflection\n\n**Insight 1:** First insight about patterns.\n**Insight 2:** Second insight about architecture.\n**Insight 3:** Third insight about testing."}

	engine := NewEngine(database, mock, linkStore, idGen)
	ctx := context.Background()

	// Insert initial observations
	for i := 0; i < 5; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'multi-ns')`, fmt.Sprintf("MLT%04d", i))
	}

	// Create 3 reflections
	for round := 0; round < 3; round++ {
		// Add more observations for each round
		for i := 0; i < 3; i++ {
			database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
				VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'multi-ns')`, fmt.Sprintf("MLT%04d%d%d", round, i, round))
		}

		// Update all previous reflections to be older than cooldown
		database.ExecContext(ctx, `UPDATE reflections SET created_at = datetime('now', '-3 hours') WHERE namespace='multi-ns'`)

		result, err := engine.ForceReflect(ctx, "multi-ns")
		if err != nil {
			t.Fatalf("round %d force reflect: %v", round, err)
		}
		if result.ReflectionsCreated != 1 {
			t.Fatalf("round %d: expected 1 reflection, got %d", round, result.ReflectionsCreated)
		}
	}

	// Verify exactly 1 active reflection observation exists
	var activeCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE namespace='multi-ns' AND source='reflection' AND deleted_at IS NULL`).Scan(&activeCount)
	if activeCount != 1 {
		t.Errorf("active reflections = %d, want 1", activeCount)
	}

	// Verify exactly 2 supersedes links exist (3 reflections = 2 transitions)
	var supersedesCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_links WHERE relation_type='supersedes'`).Scan(&supersedesCount)
	if supersedesCount != 2 {
		t.Errorf("supersedes links = %d, want 2", supersedesCount)
	}

	// Verify reflections table has all 3 rows
	var reflectionCount int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM reflections WHERE namespace='multi-ns'`).Scan(&reflectionCount)
	if reflectionCount != 3 {
		t.Errorf("reflections rows = %d, want 3", reflectionCount)
	}
}

// TestSupersedesChain verifies that the supersedes links form a proper chain.
func TestSupersedesChain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	linkStore := links.NewStore(database, idGen)
	mock := &mockLLM{response: "TITLE: Test Reflection\n\n**Insight 1:** First insight about patterns.\n**Insight 2:** Second insight about architecture.\n**Insight 3:** Third insight about testing."}

	engine := NewEngine(database, mock, linkStore, idGen)
	ctx := context.Background()

	// Insert initial observations
	for i := 0; i < 5; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'chain-ns')`, fmt.Sprintf("CHN%04d", i))
	}

	// First reflection
	result1, err := engine.ForceReflect(ctx, "chain-ns")
	if err != nil {
		t.Fatalf("first force reflect: %v", err)
	}
	if result1.ReflectionsCreated != 1 {
		t.Fatalf("first: expected 1 reflection, got %d", result1.ReflectionsCreated)
	}

	var obsID1 string
	database.QueryRowContext(ctx, `SELECT id FROM observations WHERE namespace='chain-ns' AND source='reflection' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`).Scan(&obsID1)

	// Second reflection
	for i := 5; i < 8; i++ {
		database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
			VALUES(?, 'obs', 'content', 'decision', 1, 0.9, 0.8, 'semantic', 'chain-ns')`, fmt.Sprintf("CHN%04d", i))
	}

	// Update first reflection to be older than cooldown
	database.ExecContext(ctx, `UPDATE reflections SET created_at = datetime('now', '-3 hours') WHERE namespace='chain-ns'`)

	result2, err := engine.ForceReflect(ctx, "chain-ns")
	if err != nil {
		t.Fatalf("second force reflect: %v", err)
	}
	if result2.ReflectionsCreated != 1 {
		t.Fatalf("second: expected 1 reflection, got %d", result2.ReflectionsCreated)
	}

	var obsID2 string
	database.QueryRowContext(ctx, `SELECT id FROM observations WHERE namespace='chain-ns' AND source='reflection' AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`).Scan(&obsID2)

	// Verify obsID2 supersedes obsID1
	var count int
	database.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_links WHERE source_id=? AND target_id=? AND relation_type='supersedes'`, obsID2, obsID1).Scan(&count)
	if count != 1 {
		t.Errorf("expected obsID2 to supersede obsID1")
	}

	// Verify obsID1 is soft-deleted
	var deleted bool
	database.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM observations WHERE id=?`, obsID1).Scan(&deleted)
	if !deleted {
		t.Error("obsID1 should be soft-deleted")
	}

	// Verify obsID2 is NOT soft-deleted
	database.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM observations WHERE id=?`, obsID2).Scan(&deleted)
	if deleted {
		t.Error("obsID2 should NOT be soft-deleted")
	}
}
