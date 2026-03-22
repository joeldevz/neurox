package benchmark

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"neurox/internal/consolidate"
	"neurox/internal/db"
	"neurox/internal/decay"
	"neurox/internal/embed"
	"neurox/internal/facts"
	"neurox/internal/filelink"
	"neurox/internal/links"
	"neurox/internal/llm"
	"neurox/internal/observation"
	"neurox/internal/proactive"
	"neurox/internal/recall"
	"neurox/internal/session"
	"neurox/internal/temporal"
)

// BenchEnv is the shared environment for all benchmark dimensions.
// It owns an isolated in-process SQLite database and all domain stores.
// Call Close() when done.
type BenchEnv struct {
	// DB is the raw database connection (also available via individual stores).
	DB *sql.DB

	// Domain stores / engines
	ObsStore          *observation.Store
	RecallEngine      *recall.Engine
	LinkStore         *links.Store
	FactStore         *facts.Store
	SessionMgr        *session.Manager
	DecayEngine       *decay.Engine
	Pipeline          *consolidate.Pipeline
	ProactiveEngine   *proactive.Engine
	TemporalStore     *temporal.Store
	TemporalExtractor *temporal.Extractor

	// Embedder used by all stores that need semantic search.
	Embedder *FakeEmbedder

	// Scale holds the configuration used to create this environment.
	Scale ScaleConfig

	// tempDir is removed in Close().
	tempDir string
}

// NewBenchEnv creates an isolated benchmark environment:
//   - temporary directory with a fresh SQLite database
//   - all domain stores wired together
//   - FakeEmbedder for deterministic semantic search
//   - llm.Disabled for deterministic consolidation (no LLM calls)
func NewBenchEnv(ctx context.Context, scale ScaleConfig) (*BenchEnv, error) {
	// Create temp directory for the database file.
	tmpDir, err := os.MkdirTemp("", "neurox-bench-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	dbPath := filepath.Join(tmpDir, "bench.db")
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("open bench db: %w", err)
	}

	// --- embedder ---
	fakeEmbedder := NewFakeEmbedder(defaultFakeDimensions)

	// --- id generator ---
	idGen := observation.NewULIDGenerator()

	// --- observation store (nil gate = noop write gate) ---
	obsStore := observation.NewStore(database, nil)

	// --- temporal ---
	temporalParser := temporal.NewParser()
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporalParser, temporalStore)
	obsStore.SetTemporalExtractor(temporalExtractor)

	// --- recall engine ---
	recallEngine := recall.NewEngine(database, recall.WithEmbedder(fakeEmbedder))

	// --- links ---
	linkStore := links.NewStore(database, idGen)

	// --- facts ---
	factStore := facts.NewStore(database, idGen)

	// --- session manager (disabled LLM → no extraction from summaries) ---
	sessionMgr := session.NewManager(database, llm.Disabled{}, idGen)
	sessionMgr.SetTemporalExtractor(temporalExtractor)

	// --- decay ---
	decayEngine := decay.NewEngine(database)

	// --- consolidation pipeline ---
	// Use disabled LLM and gate-off mode for fully deterministic behaviour.
	disabledLLM := llm.Disabled{}
	gate := llm.NewGate(disabledLLM, llm.GateModeOff)

	// Embedding queue is required by the pipeline but we pass nil — the
	// pipeline handles a nil queue gracefully (no async embedding).
	embedQueue := embed.NewQueue(fakeEmbedder, database)

	pipeline := consolidate.NewPipeline(
		database,
		decayEngine,
		fakeEmbedder,
		embedQueue,
		gate,
		linkStore,
		disabledLLM,
		idGen,
		consolidate.Config{}, // zero value: pipeline won't auto-start
	)

	// --- proactive context engine ---
	proactiveEngine := proactive.NewEngine(database, fakeEmbedder)

	env := &BenchEnv{
		DB:                database,
		ObsStore:          obsStore,
		RecallEngine:      recallEngine,
		LinkStore:         linkStore,
		FactStore:         factStore,
		SessionMgr:        sessionMgr,
		DecayEngine:       decayEngine,
		Pipeline:          pipeline,
		ProactiveEngine:   proactiveEngine,
		TemporalStore:     temporalStore,
		TemporalExtractor: temporalExtractor,
		Embedder:          fakeEmbedder,
		Scale:             scale,
		tempDir:           tmpDir,
	}

	return env, nil
}

// Close releases database resources and removes the temporary directory.
func (e *BenchEnv) Close() {
	if e.DB != nil {
		_ = e.DB.Close()
	}
	if e.tempDir != "" {
		_ = os.RemoveAll(e.tempDir)
	}
}

// MockLLM is a canned-response LLM provider for dimensions that need
// LLM-dependent behaviour (e.g. reflection, extraction).
// It returns a fixed response string for every prompt.
type MockLLM struct {
	Response string
}

// Complete returns the canned response regardless of the prompt.
func (m MockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.Response, nil
}

// Name identifies the mock provider.
func (m MockLLM) Name() string { return "mock-llm" }

// Ensure MockLLM satisfies the llm.Provider interface at compile time.
var _ llm.Provider = MockLLM{}

// Ensure MockLLM satisfies the embed.Provider check (FakeEmbedder only).
var _ embed.Provider = (*FakeEmbedder)(nil)

// idGen is used only to satisfy filelink.IDGenerator interface checks.
var _ filelink.IDGenerator = observation.NewULIDGenerator()
