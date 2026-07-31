package benchmark

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joeldevz/neurox/internal/consolidate"
	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/filelink"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/temporal"
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

	// --- facts ---
	factStore := facts.NewStore(database, idGen)

	// --- recall engine ---
	// Honor NEUROX_RECALL_DISABLE_BACKFILL (used by the G6 stretch gate to
	// measure cross-session recall without the namespace backfill band-aid).
	disableBackfill := false
	if v := strings.TrimSpace(os.Getenv("NEUROX_RECALL_DISABLE_BACKFILL")); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			disableBackfill = parsed
		}
	}
	recallOpts := []recall.EngineOption{
		recall.WithEmbedder(fakeEmbedder),
		recall.WithFactStore(factStore),
		recall.WithDisableBackfill(disableBackfill),
	}
	// Honor NEUROX_RECALL_RRF_K (k override for the RRF formula).
	if v := strings.TrimSpace(os.Getenv("NEUROX_RECALL_RRF_K")); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 {
			recallOpts = append(recallOpts, recall.WithRRFK(k))
		}
	}
	// Honor NEUROX_RECALL_SEMANTIC_MIN_SCORE (semantic threshold override).
	if v := strings.TrimSpace(os.Getenv("NEUROX_RECALL_SEMANTIC_MIN_SCORE")); v != "" {
		if score, err := strconv.ParseFloat(v, 64); err == nil && score > 0 {
			recallOpts = append(recallOpts, recall.WithSemanticMinScore(score))
		}
	}
	recallEngine := recall.NewEngine(database, recallOpts...)

	// --- links ---
	linkStore := links.NewStore(database, idGen)

	// --- session manager (disabled LLM → no extraction from summaries) ---
	sessionMgr := session.NewManager(database, llm.Disabled{}, idGen)
	sessionMgr.SetTemporalExtractor(temporalExtractor)

	// --- decay ---
	decayEngine := decay.NewEngine(database)

	// --- consolidation pipeline ---
	// Use disabled LLM and gate-off mode for fully deterministic behaviour.
	disabledLLM := llm.Disabled{}
	gate := llm.NewGate(disabledLLM, llm.GateModeOff)

	pipeline := consolidate.NewPipeline(
		database,
		decayEngine,
		fakeEmbedder,
		nil, // benchmark embeddings are persisted synchronously via PersistPendingEmbeddings
		gate,
		linkStore,
		disabledLLM,
		nil, // no curator provider
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

// PersistPendingEmbeddings synchronously embeds all observations that do not yet
// have a persisted embedding. This keeps benchmark execution deterministic and
// avoids relying on the production background queue timing.
func (e *BenchEnv) PersistPendingEmbeddings(ctx context.Context) error {
	if e == nil {
		return fmt.Errorf("bench env is nil")
	}
	if e.DB == nil {
		return fmt.Errorf("bench env database is not initialized")
	}
	if e.Embedder == nil {
		return fmt.Errorf("bench env embedder is not initialized")
	}

	type pendingObservation struct{ id string }

	rows, err := e.DB.QueryContext(ctx, `
		SELECT id, title, content
		FROM observations
		WHERE deleted_at IS NULL AND embedding IS NULL
		ORDER BY importance DESC, created_at ASC, id ASC
	`)
	if err != nil {
		return fmt.Errorf("query pending benchmark embeddings: %w", err)
	}
	defer rows.Close()

	pending := make([]pendingObservation, 0)
	texts := make([]string, 0)
	for rows.Next() {
		var id string
		var title string
		var content string
		if err := rows.Scan(&id, &title, &content); err != nil {
			return fmt.Errorf("scan pending benchmark embedding: %w", err)
		}
		pending = append(pending, pendingObservation{id: id})
		texts = append(texts, title+" "+content)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate pending benchmark embeddings: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	vectors, err := e.Embedder.EmbedBatch(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed pending benchmark observations: %w", err)
	}
	if len(vectors) != len(pending) {
		return fmt.Errorf("embed pending benchmark observations: got %d vectors for %d observations", len(vectors), len(pending))
	}

	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin benchmark embedding tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for i, item := range pending {
		if _, err := tx.ExecContext(ctx,
			"UPDATE observations SET embedding = ? WHERE id = ?",
			embed.SerializeF32(vectors[i]),
			item.id,
		); err != nil {
			return fmt.Errorf("persist benchmark embedding for %s: %w", item.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit benchmark embeddings: %w", err)
	}
	tx = nil

	return nil
}

// Ensure FakeEmbedder satisfies the embed.Provider check.
var _ embed.Provider = (*FakeEmbedder)(nil)

// idGen is used only to satisfy filelink.IDGenerator interface checks.
var _ filelink.IDGenerator = observation.NewULIDGenerator()
