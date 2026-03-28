package consolidate

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/joeldevz/neurox/internal/contradiction"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/filelink"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/temporal"
)

const (
	defaultInterval = 30 * time.Minute
	bufferCap       = 200

	// Legacy promotion thresholds (kept for backward compatibility, may be deprecated)
	bufferToWorkingScore    = 0.3 // importance threshold for Buffer→Working
	workingToCoreAccessMin  = 5   // min access_count for Working→Core
	workingToCoreAgeMinDays = 7   // min age in days for Working→Core

	// New multi-factor promotion thresholds
	// Buffer→Working: requires composite score based on durable value + activation
	bufferToWorkingMinActivation       = 0.25 // minimum activation level
	bufferToWorkingMinConsolidation    = 0.15 // minimum consolidation strength OR importance
	bufferToWorkingImportanceWeight    = 0.4  // weight for importance in composite score
	bufferToWorkingActivationWeight    = 0.35 // weight for activation level
	bufferToWorkingConsolidationWeight = 0.25 // weight for consolidation strength
	bufferToWorkingCompositeThreshold  = 0.35 // minimum composite score

	// Working→Core: requires semantic stability beyond just age/access
	workingToCoreMinActivation     = 0.30 // minimum activation level
	workingToCoreMinConsolidation  = 0.40 // minimum consolidation strength
	workingToCoreMinCompositeScore = 0.50 // minimum composite score
	workingToCoreRecencyDays       = 14   // must have been accessed within this many days

	// Core recalibration: values assigned when promoting to Core
	coreRecalibrationBaseImportance = 0.60 // base importance for durable observations entering Core
	coreRecalibrationMinImportance  = 0.45 // minimum importance after recalibration
	coreRecalibrationTypeBonus      = 0.10 // bonus for high-value types (decision, bugfix)
)

type Config struct {
	Interval         time.Duration
	DedupThreshold   float64
	ContradictionMin float64
	ContradictionMax float64
	RelatedMin       float64
	RelatedMax       float64

	// Promotion thresholds (optional overrides)
	BufferToWorkingThreshold   float64 // Composite score threshold for Buffer→Working
	WorkingToCoreThreshold     float64 // Composite score threshold for Working→Core
	CoreRecalibrationBase      float64 // Base importance when recalibrating for Core
	CoreRecalibrationTypeBonus float64 // Bonus for high-value types in Core
}

type Pipeline struct {
	db                    *sql.DB
	decay                 *decay.Engine
	embedder              embed.Provider
	embedQueue            *embed.Queue
	gate                  *llm.Gate
	contradictionDetector *contradiction.Detector
	reflectEngine         *reflectpkg.Engine
	idGen                 filelink.IDGenerator
	cfg                   Config
	stop                  chan struct{}
	wg                    sync.WaitGroup
}

func NewPipeline(db *sql.DB, decayEngine *decay.Engine, embedder embed.Provider, embedQueue *embed.Queue, gate *llm.Gate, linkStore *links.Store, llmProvider llm.Provider, curatorProvider llm.Provider, idGen filelink.IDGenerator, cfg Config) *Pipeline {
	if cfg.Interval == 0 {
		cfg.Interval = defaultInterval
	}

	// Use curatorProvider for reflections when available; fall back to llmProvider.
	reflectProvider := llmProvider
	if curatorProvider != nil {
		if _, ok := curatorProvider.(llm.Disabled); !ok {
			reflectProvider = curatorProvider
		}
	}

	return &Pipeline{
		db:         db,
		decay:      decayEngine,
		embedder:   embedder,
		embedQueue: embedQueue,
		gate:       gate,
		contradictionDetector: contradiction.NewDetector(db, embedder, llmProvider, linkStore,
			cfg.ContradictionMin, cfg.ContradictionMax),
		reflectEngine: reflectpkg.NewEngine(db, reflectProvider, linkStore, idGen),
		idGen:         idGen,
		cfg:           cfg,
		stop:          make(chan struct{}),
	}
}

// Start begins the periodic consolidation loop.
func (p *Pipeline) Start(ctx context.Context) {
	p.wg.Add(1)
	go p.loop(ctx)
}

// Stop gracefully shuts down the pipeline.
func (p *Pipeline) Stop() {
	close(p.stop)
	p.wg.Wait()
}

func (p *Pipeline) loop(ctx context.Context) {
	defer p.wg.Done()

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			if err := p.Run(ctx); err != nil {
				log.Printf("consolidation run failed: %v", err)
			}
		}
	}
}

// RunResult holds stats from a consolidation run.
type RunResult struct {
	Epoch               int
	Decayed             int64
	PromotedToWorking   int64
	PromotedToCore      int64
	Deduped             int64
	ContradictionsFound int
	ContradictionsFixed int
	ReflectionsCreated  int
	RelatesToLinks      int64
	Evicted             int64
	GarbageCollected    int64
	SessionsCleaned     int64
}

// ForceRun executes consolidation ignoring all thresholds — promotes everything
// from Buffer→Working→Core, runs dedup, contradictions, reflection, and GC.
func (p *Pipeline) ForceRun(ctx context.Context) error {
	start := time.Now()

	epoch, err := p.nextEpoch(ctx)
	if err != nil {
		return fmt.Errorf("get epoch: %w", err)
	}

	runID, err := p.startRun(ctx, epoch)
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}

	var result RunResult
	result.Epoch = epoch

	// 0. Cleanup stale sessions
	result.SessionsCleaned, err = p.cleanupStaleSessions(ctx)
	if err != nil {
		log.Printf("cleanup stale sessions warning: %v", err)
	} else if result.SessionsCleaned > 0 {
		log.Printf("cleaned up %d stale sessions", result.SessionsCleaned)
	}

	// 1. Decay
	result.Decayed, err = p.decay.ApplyDecay(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("decay: %w", err)
	}

	// 1.5 Backfill missing embeddings
	if p.embedQueue != nil {
		p.embedQueue.BackfillPending(ctx)
	}

	// 2. Force promote ALL Buffer → Working
	res, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
		WHERE deleted_at IS NULL AND layer = 0
	`)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("force promote buffer→working: %w", err)
	}
	result.PromotedToWorking, _ = res.RowsAffected()

	// 3. Force promote durable Working → Core (operational stays in Working)
	res, err = p.db.ExecContext(ctx, `
		UPDATE observations
		SET layer = 2, consolidation_status = 'promoted', updated_at = datetime('now')
		WHERE deleted_at IS NULL AND layer = 1 AND retention = 'durable'
	`)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("force promote working→core: %w", err)
	}
	result.PromotedToCore, _ = res.RowsAffected()

	// 4. Dedup
	if embed.IsAvailable(p.embedder) {
		result.Deduped, err = p.dedup(ctx)
		if err != nil {
			log.Printf("dedup warning: %v", err)
		}
	}

	// 5. Contradictions
	if embed.IsAvailable(p.embedder) {
		cResult, cErr := p.contradictionDetector.Run(ctx)
		if cErr != nil {
			log.Printf("contradiction detection warning: %v", cErr)
		} else {
			result.ContradictionsFound = cResult.Candidates
			result.ContradictionsFixed = cResult.Resolved + cResult.Questions
		}
	}

	// 6. Reflection
	if llm.IsAvailable(p.reflectEngine.LLM()) {
		namespaces, nsErr := p.activeNamespaces(ctx)
		if nsErr == nil {
			for _, ns := range namespaces {
				rResult, rErr := p.reflectEngine.ForceReflect(ctx, ns)
				if rErr != nil {
					log.Printf("reflection warning (%s): %v", ns, rErr)
				} else {
					result.ReflectionsCreated += rResult.ReflectionsCreated
				}
			}
		}
	}

	// 7. Cross-namespace relates_to links (spreading activation)
	if embed.IsAvailable(p.embedder) {
		relatesToCount, rtErr := p.createCrossNamespaceRelatesTo(ctx)
		if rtErr != nil {
			log.Printf("cross-namespace relates_to warning: %v", rtErr)
		} else {
			result.RelatesToLinks = relatesToCount
			if relatesToCount > 0 {
				log.Printf("created %d cross-namespace relates_to links", relatesToCount)
			}
		}
	}

	// 8. GC
	result.GarbageCollected, err = p.decay.GarbageCollect(ctx)
	if err != nil {
		log.Printf("gc warning: %v", err)
	}

	p.completeRun(ctx, runID, result)
	duration := time.Since(start)
	log.Printf("forced consolidation epoch %d: decayed=%d promoted_working=%d promoted_core=%d deduped=%d contradictions=%d/%d reflections=%d relates_to=%d gc=%d sessions_cleaned=%d (%v)",
		epoch, result.Decayed, result.PromotedToWorking, result.PromotedToCore,
		result.Deduped, result.ContradictionsFixed, result.ContradictionsFound,
		result.ReflectionsCreated, result.RelatesToLinks, result.GarbageCollected, result.SessionsCleaned, duration)

	return nil
}

// Run executes a single consolidation pass: epoch → decay → retry strikes → promote → dedup → evict → GC.
func (p *Pipeline) Run(ctx context.Context) error {
	start := time.Now()

	// Get next epoch
	epoch, err := p.nextEpoch(ctx)
	if err != nil {
		return fmt.Errorf("get epoch: %w", err)
	}

	// Record the run
	runID, err := p.startRun(ctx, epoch)
	if err != nil {
		return fmt.Errorf("start run: %w", err)
	}

	var result RunResult
	result.Epoch = epoch

	// 0. Cleanup stale sessions
	result.SessionsCleaned, err = p.cleanupStaleSessions(ctx)
	if err != nil {
		log.Printf("cleanup stale sessions warning: %v", err)
	} else if result.SessionsCleaned > 0 {
		log.Printf("cleaned up %d stale sessions", result.SessionsCleaned)
	}

	// 1. Decay
	result.Decayed, err = p.decay.ApplyDecay(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("decay: %w", err)
	}

	// 1.5 Backfill missing embeddings
	if p.embedQueue != nil {
		p.embedQueue.BackfillPending(ctx)
	}

	// 2. Retry previously rejected observations (3-strike system)
	retried, retryErr := p.retryRejected(ctx, epoch)
	if retryErr != nil {
		log.Printf("retry rejected warning: %v", retryErr)
	} else if retried > 0 {
		log.Printf("retried %d previously rejected observations", retried)
	}

	// 3. Promote Buffer→Working (with gate)
	result.PromotedToWorking, err = p.promoteBufferToWorking(ctx, epoch)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("promote buffer→working: %w", err)
	}

	// 4. Promote Working→Core
	result.PromotedToCore, err = p.promoteWorkingToCore(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("promote working→core: %w", err)
	}

	// 5. Dedup (only if embeddings available)
	if embed.IsAvailable(p.embedder) {
		result.Deduped, err = p.dedup(ctx)
		if err != nil {
			log.Printf("dedup warning: %v", err)
		}
	}

	// 6. Detect contradictions
	if embed.IsAvailable(p.embedder) {
		cResult, cErr := p.contradictionDetector.Run(ctx)
		if cErr != nil {
			log.Printf("contradiction detection warning: %v", cErr)
		} else {
			result.ContradictionsFound = cResult.Candidates
			result.ContradictionsFixed = cResult.Resolved + cResult.Questions
		}
	}

	// 7. Reflection (per active namespace)
	if llm.IsAvailable(p.reflectEngine.LLM()) {
		namespaces, nsErr := p.activeNamespaces(ctx)
		if nsErr != nil {
			log.Printf("get namespaces warning: %v", nsErr)
		}
		for _, ns := range namespaces {
			rResult, rErr := p.reflectEngine.Run(ctx, ns)
			if rErr != nil {
				log.Printf("reflection warning (%s): %v", ns, rErr)
			} else {
				result.ReflectionsCreated += rResult.ReflectionsCreated
			}
		}
	}

	// 8. Cross-namespace relates_to links (spreading activation)
	if embed.IsAvailable(p.embedder) {
		relatesToCount, rtErr := p.createCrossNamespaceRelatesTo(ctx)
		if rtErr != nil {
			log.Printf("cross-namespace relates_to warning: %v", rtErr)
		} else {
			result.RelatesToLinks = relatesToCount
			if relatesToCount > 0 {
				log.Printf("created %d cross-namespace relates_to links", relatesToCount)
			}
		}
	}

	// 9. Evict buffer overflow
	result.Evicted, err = p.evictBuffer(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("evict buffer: %w", err)
	}

	// 9. Garbage collect
	result.GarbageCollected, err = p.decay.GarbageCollect(ctx)
	if err != nil {
		log.Printf("gc warning: %v", err)
	}

	// Complete run
	p.completeRun(ctx, runID, result)
	duration := time.Since(start)
	log.Printf("consolidation epoch %d: decayed=%d promoted_working=%d promoted_core=%d deduped=%d contradictions=%d/%d reflections=%d relates_to=%d evicted=%d gc=%d sessions_cleaned=%d gate=%s (%v)",
		epoch, result.Decayed, result.PromotedToWorking, result.PromotedToCore,
		result.Deduped, result.ContradictionsFixed, result.ContradictionsFound,
		result.ReflectionsCreated, result.RelatesToLinks, result.Evicted, result.GarbageCollected, result.SessionsCleaned, p.gate.Mode(), duration)

	return nil
}

func (p *Pipeline) nextEpoch(ctx context.Context) (int, error) {
	var epoch int
	err := p.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(epoch), 0) + 1 FROM consolidation_runs").Scan(&epoch)
	if err != nil {
		return 0, err
	}
	return epoch, nil
}

func (p *Pipeline) startRun(ctx context.Context, epoch int) (string, error) {
	id := fmt.Sprintf("RUN%d", epoch)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO consolidation_runs(id, status, epoch) VALUES(?, 'running', ?)
	`, id, epoch)
	return id, err
}

func (p *Pipeline) failRun(ctx context.Context, runID string, runErr error) {
	p.db.ExecContext(ctx, `
		UPDATE consolidation_runs SET status = 'failed', error_message = ?, completed_at = datetime('now')
		WHERE id = ?
	`, runErr.Error(), runID)
}

func (p *Pipeline) completeRun(ctx context.Context, runID string, result RunResult) {
	p.db.ExecContext(ctx, `
		UPDATE consolidation_runs
		SET status = 'completed',
		    observations_processed = ?,
		    observations_promoted = ?,
		    observations_deduped = ?,
		    contradictions_found = ?,
		    reflections_created = ?,
		    completed_at = datetime('now')
		WHERE id = ?
	`, result.Decayed, result.PromotedToWorking+result.PromotedToCore, result.Deduped, result.ContradictionsFound, result.ReflectionsCreated, runID)
}

// calculateCompositeScore computes a weighted score combining importance, activation, and consolidation.
// This represents the overall "memory strength" for promotion decisions.
func calculateCompositeScore(importance, activation, consolidation float64) float64 {
	return importance*bufferToWorkingImportanceWeight +
		activation*bufferToWorkingActivationWeight +
		consolidation*bufferToWorkingConsolidationWeight
}

// isHighValueType returns true for observation types that represent durable knowledge.
func isHighValueType(obsType string) bool {
	switch obsType {
	case "decision", "bugfix", "pattern", "gotcha", "preference":
		return true
	default:
		return false
	}
}

// promoteBufferToWorking promotes Buffer observations based on a composite score
// combining durable value (importance), activation level, and consolidation strength.
// Procedural observations are auto-promoted. Operational observations can reach Working
// but will be blocked from Core by retention policy.
func (p *Pipeline) promoteBufferToWorking(ctx context.Context, epoch int) (int64, error) {
	// Use configured threshold or default
	threshold := p.cfg.BufferToWorkingThreshold
	if threshold == 0 {
		threshold = bufferToWorkingCompositeThreshold
	}

	if p.gate.Mode() == llm.GateModeOff {
		// Multi-factor heuristic promotion
		// Fetch candidates with all relevant signals
		rows, err := p.db.QueryContext(ctx, `
			SELECT id, observation_type, kind, importance, activation_level, consolidation_strength
			FROM observations
			WHERE deleted_at IS NULL
			  AND layer = 0
			  AND consolidation_status = 'pending'
		`)
		if err != nil {
			return 0, fmt.Errorf("query buffer candidates: %w", err)
		}
		defer rows.Close()

		type candidate struct {
			id             string
			obsType        string
			kind           string
			importance     float64
			activation     float64
			consolidation  float64
			compositeScore float64
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.obsType, &c.kind, &c.importance, &c.activation, &c.consolidation); err != nil {
				continue
			}
			c.compositeScore = calculateCompositeScore(c.importance, c.activation, c.consolidation)
			candidates = append(candidates, c)
		}

		var promoted int64
		for _, c := range candidates {
			// Auto-promote procedural (procedural knowledge is always useful)
			if c.kind == "procedural" {
				if _, err := p.db.ExecContext(ctx, `
					UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
					WHERE id = ?
				`, c.id); err == nil {
					promoted++
				}
				continue
			}

			// Auto-promote high-importance durable observations
			if c.importance >= 0.7 && isHighValueType(c.obsType) {
				if _, err := p.db.ExecContext(ctx, `
					UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
					WHERE id = ?
				`, c.id); err == nil {
					promoted++
				}
				continue
			}

			// Check minimum thresholds for activation/consolidation
			if c.activation < bufferToWorkingMinActivation && c.consolidation < bufferToWorkingMinConsolidation {
				continue // Too weak on both activation and consolidation
			}

			// Promote based on composite score
			if c.compositeScore >= threshold {
				if _, err := p.db.ExecContext(ctx, `
					UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
					WHERE id = ?
				`, c.id); err == nil {
					promoted++
				}
			}
		}

		return promoted, nil
	}

	// Gate-assisted promotion: fetch candidates and evaluate with richer signals
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, title, content, observation_type, kind, importance, activation_level, consolidation_strength, access_count
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer = 0
		  AND consolidation_status = 'pending'
		ORDER BY importance DESC
		LIMIT 50
	`)
	if err != nil {
		return 0, fmt.Errorf("query promotion candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id              string
		title           string
		content         string
		observationType string
		kind            string
		importance      float64
		activation      float64
		consolidation   float64
		accessCount     int
		compositeScore  float64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.title, &c.content, &c.observationType, &c.kind, &c.importance, &c.activation, &c.consolidation, &c.accessCount); err != nil {
			continue
		}
		c.compositeScore = calculateCompositeScore(c.importance, c.activation, c.consolidation)
		candidates = append(candidates, c)
	}

	var promoted int64
	for _, c := range candidates {
		// Auto-promote procedural or high-importance durable
		if c.kind == "procedural" || (c.importance >= 0.7 && isHighValueType(c.observationType)) {
			if _, err := p.db.ExecContext(ctx, `
				UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
				WHERE id = ?
			`, c.id); err == nil {
				promoted++
			}
			continue
		}

		// Below minimum thresholds → skip
		if c.activation < bufferToWorkingMinActivation && c.consolidation < bufferToWorkingMinConsolidation {
			continue
		}

		// Below composite threshold → skip
		if c.compositeScore < threshold {
			continue
		}

		// In the gray zone → ask the gate
		decision, err := p.gate.PromotionDecide(ctx, llm.PromotionInput{
			Title:           c.title,
			Content:         c.content,
			ObservationType: c.observationType,
			Importance:      c.importance,
			AccessCount:     c.accessCount,
			Layer:           0,
		})
		if err != nil {
			// On error, use heuristic (promote if composite score is good)
			decision = llm.PromotionPromote
		}

		switch decision {
		case llm.PromotionPromote:
			if _, err := p.db.ExecContext(ctx, `
				UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
				WHERE id = ?
			`, c.id); err == nil {
				promoted++
			}
		case llm.PromotionReject:
			p.handleRejection(ctx, c.id, epoch)
		case llm.PromotionDefer:
			// Leave as pending, will be re-evaluated next epoch
		}
	}

	return promoted, nil
}

// calculateCoreImportance recalibrates importance when promoting to Core.
// It ensures durable knowledge doesn't enter Core with degraded scores.
func calculateCoreImportance(currentImportance float64, obsType string, consolidationStrength float64) float64 {
	// Base recalibration value
	base := coreRecalibrationBaseImportance

	// Bonus for high-value observation types
	typeBonus := 0.0
	if isHighValueType(obsType) {
		typeBonus = coreRecalibrationTypeBonus
	}

	// Consolidation strength can add up to 0.15 bonus
	consolidationBonus := consolidationStrength * 0.15

	// Calculate new importance
	newImportance := base + typeBonus + consolidationBonus

	// Don't reduce importance if current is already higher
	if currentImportance > newImportance {
		return currentImportance
	}

	// Ensure minimum importance
	if newImportance < coreRecalibrationMinImportance {
		return coreRecalibrationMinImportance
	}

	// Cap at 1.0
	if newImportance > 1.0 {
		return 1.0
	}

	return newImportance
}

// promoteWorkingToCore promotes Working observations with semantic stability to Core.
// Requires: durable retention, sufficient activation, consolidation strength, and recency.
// Recalibrates importance to prevent degraded scores from entering Core.
// Operational observations are explicitly blocked from Core regardless of other signals.
func (p *Pipeline) promoteWorkingToCore(ctx context.Context) (int64, error) {
	// Use configured threshold or default
	threshold := p.cfg.WorkingToCoreThreshold
	if threshold == 0 {
		threshold = workingToCoreMinCompositeScore
	}

	// Fetch candidates with all relevant signals
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, observation_type, importance, activation_level, consolidation_strength,
		       access_count, julianday('now') - julianday(COALESCE(last_accessed, created_at)) as days_since_access
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer = 1
		  AND retention = 'durable'
		  AND access_count >= ?
		  AND julianday('now') - julianday(created_at) >= ?
	`, workingToCoreAccessMin, workingToCoreAgeMinDays)
	if err != nil {
		return 0, fmt.Errorf("query working candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id              string
		obsType         string
		importance      float64
		activation      float64
		consolidation   float64
		accessCount     int
		daysSinceAccess float64
		compositeScore  float64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.obsType, &c.importance, &c.activation, &c.consolidation, &c.accessCount, &c.daysSinceAccess); err != nil {
			continue
		}
		c.compositeScore = calculateCompositeScore(c.importance, c.activation, c.consolidation)
		candidates = append(candidates, c)
	}

	var promoted int64
	for _, c := range candidates {
		// Check recency: must have been accessed within recent window
		if c.daysSinceAccess > workingToCoreRecencyDays {
			continue // Too stale, even if it meets other criteria
		}

		// Check minimum thresholds for activation and consolidation
		if c.activation < workingToCoreMinActivation {
			continue
		}
		if c.consolidation < workingToCoreMinConsolidation {
			continue
		}

		// Check composite score
		if c.compositeScore < threshold {
			continue
		}

		// Calculate recalibrated importance for Core
		newImportance := calculateCoreImportance(c.importance, c.obsType, c.consolidation)

		// Promote to Core with recalibrated importance
		if _, err := p.db.ExecContext(ctx, `
			UPDATE observations
			SET layer = 2,
			    consolidation_status = 'promoted',
			    importance = ?,
			    updated_at = datetime('now')
			WHERE id = ?
		`, newImportance, c.id); err == nil {
			promoted++
		}
	}

	return promoted, nil
}

// retryRejected resets previously rejected observations to 'pending' if enough epochs
// have passed, implementing the 3-strike system.
func (p *Pipeline) retryRejected(ctx context.Context, currentEpoch int) (int64, error) {
	// Strike 1 → retry after 48 epochs
	res1, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET consolidation_status = 'pending', updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND consolidation_status = 'rejected'
		  AND rejection_epoch IS NOT NULL
		  AND ? - rejection_epoch >= ?
	`, currentEpoch, llm.RetryAfterStrike1)
	if err != nil {
		return 0, fmt.Errorf("retry strike-1: %w", err)
	}
	count1, _ := res1.RowsAffected()

	// Strike 2 → retry after 144 epochs
	res2, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET consolidation_status = 'pending', updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND consolidation_status = 'rejected-2'
		  AND rejection_epoch IS NOT NULL
		  AND ? - rejection_epoch >= ?
	`, currentEpoch, llm.RetryAfterStrike2)
	if err != nil {
		return count1, fmt.Errorf("retry strike-2: %w", err)
	}
	count2, _ := res2.RowsAffected()

	return count1 + count2, nil
}

// handleRejection advances the strike status for a rejected observation.
func (p *Pipeline) handleRejection(ctx context.Context, id string, epoch int) {
	var currentStatus string
	err := p.db.QueryRowContext(ctx, `
		SELECT consolidation_status FROM observations WHERE id = ?
	`, id).Scan(&currentStatus)
	if err != nil {
		return
	}

	nextStatus := llm.NextStrike(llm.StrikeStatus(currentStatus))
	p.db.ExecContext(ctx, `
		UPDATE observations SET consolidation_status = ?, rejection_epoch = ?, updated_at = datetime('now')
		WHERE id = ?
	`, string(nextStatus), epoch, id)
}

// evictBuffer enforces the buffer cap by soft-deleting the lowest importance observations.
func (p *Pipeline) evictBuffer(ctx context.Context) (int64, error) {
	var count int
	if err := p.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE layer = 0 AND deleted_at IS NULL").Scan(&count); err != nil {
		return 0, err
	}

	if count <= bufferCap {
		return 0, nil
	}

	excess := count - bufferCap
	// Prefer evicting operational observations first at the same importance level.
	result, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET deleted_at = datetime('now'), updated_at = datetime('now')
		WHERE id IN (
			SELECT id FROM observations
			WHERE layer = 0 AND deleted_at IS NULL
			ORDER BY CASE WHEN retention = 'operational' THEN 0 ELSE 1 END ASC,
			         importance ASC, created_at ASC
			LIMIT ?
		)
	`, excess)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// dedup merges near-duplicate observations using cosine similarity.
// Observations with distinct temporal windows are preserved even if semantically
// similar, since they represent temporal snapshots rather than true duplicates.
func (p *Pipeline) dedup(ctx context.Context) (int64, error) {
	// Get Working layer observations with embeddings
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, embedding FROM observations
		WHERE deleted_at IS NULL AND layer = 1 AND embedding IS NOT NULL
		ORDER BY importance DESC, created_at DESC
	`)
	if err != nil {
		return 0, fmt.Errorf("query for dedup: %w", err)
	}
	defer rows.Close()

	type item struct {
		id        string
		embedding []float32
	}
	var items []item
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec := embed.DeserializeF32(blob)
		if vec != nil {
			items = append(items, item{id: id, embedding: vec})
		}
	}

	if len(items) < 2 {
		return 0, nil
	}

	// Load temporal mentions for all candidates to check for temporal snapshots
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	temporalMap, _ := temporal.LoadByObservations(ctx, p.db, ids)

	// Find duplicates (O(n^2) but Working layer should be small)
	deleted := make(map[string]bool)
	var deduped int64

	for i := 0; i < len(items); i++ {
		if deleted[items[i].id] {
			continue
		}
		for j := i + 1; j < len(items); j++ {
			if deleted[items[j].id] {
				continue
			}
			sim := embed.CosineSimilarity(items[i].embedding, items[j].embedding)
			if sim >= p.cfg.DedupThreshold {
				// Skip dedup if observations have distinct temporal windows
				if hasDistinctTemporalWindows(temporalMap[items[i].id], temporalMap[items[j].id]) {
					continue
				}
				// Keep items[i] (higher importance/newer), soft-delete items[j]
				p.db.ExecContext(ctx, `
					UPDATE observations SET deleted_at = datetime('now'), updated_at = datetime('now')
					WHERE id = ? AND deleted_at IS NULL
				`, items[j].id)
				deleted[items[j].id] = true
				deduped++
			}
		}
	}

	// Second pass: deduplicate reflections (layer = 2, source = 'reflection')
	dedupedReflections, err := p.dedupReflections(ctx)
	if err != nil {
		log.Printf("dedup reflections warning: %v", err)
	}

	return deduped + dedupedReflections, nil
}

// dedupReflections finds and removes duplicate reflections in the Core layer.
// Reflections are saved directly to layer = 2 and need separate deduplication.
// Only compares reflections within the same namespace, keeping the older one.
func (p *Pipeline) dedupReflections(ctx context.Context) (int64, error) {
	// Get all namespaces that have reflections
	nsRows, err := p.db.QueryContext(ctx, `
		SELECT DISTINCT namespace FROM observations
		WHERE deleted_at IS NULL AND layer = 2 AND source = 'reflection'
	`)
	if err != nil {
		return 0, fmt.Errorf("query reflection namespaces: %w", err)
	}
	defer nsRows.Close()

	var namespaces []string
	for nsRows.Next() {
		var ns string
		if err := nsRows.Scan(&ns); err != nil {
			continue
		}
		namespaces = append(namespaces, ns)
	}

	if len(namespaces) == 0 {
		return 0, nil
	}

	var totalDeduped int64

	// Process each namespace separately
	for _, ns := range namespaces {
		deduped, err := p.dedupReflectionsForNamespace(ctx, ns)
		if err != nil {
			log.Printf("dedup reflections for namespace %s warning: %v", ns, err)
			continue
		}
		totalDeduped += deduped
	}

	return totalDeduped, nil
}

// dedupReflectionsForNamespace deduplicates reflections within a single namespace.
// Keeps the older reflection (lower ULID = created first) and soft-deletes the newer duplicate.
func (p *Pipeline) dedupReflectionsForNamespace(ctx context.Context, namespace string) (int64, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, embedding FROM observations
		WHERE deleted_at IS NULL AND layer = 2 AND source = 'reflection' AND namespace = ?
		ORDER BY created_at ASC
	`, namespace)
	if err != nil {
		return 0, fmt.Errorf("query reflections: %w", err)
	}
	defer rows.Close()

	type item struct {
		id        string
		embedding []float32
	}
	var items []item
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec := embed.DeserializeF32(blob)
		if vec != nil {
			items = append(items, item{id: id, embedding: vec})
		}
	}

	if len(items) < 2 {
		return 0, nil
	}

	// Find duplicates (O(n^2) but reflection count per namespace should be small)
	deleted := make(map[string]bool)
	var deduped int64

	for i := 0; i < len(items); i++ {
		if deleted[items[i].id] {
			continue
		}
		for j := i + 1; j < len(items); j++ {
			if deleted[items[j].id] {
				continue
			}
			sim := embed.CosineSimilarity(items[i].embedding, items[j].embedding)
			if sim >= p.cfg.DedupThreshold {
				// Keep items[i] (older, since we ordered by created_at ASC),
				// soft-delete items[j] (newer)
				p.db.ExecContext(ctx, `
					UPDATE observations SET deleted_at = datetime('now'), updated_at = datetime('now')
					WHERE id = ? AND deleted_at IS NULL
				`, items[j].id)
				deleted[items[j].id] = true
				deduped++
			}
		}
	}

	return deduped, nil
}

// hasDistinctTemporalWindows returns true if two sets of temporal mentions
// point to different time periods (more than 7 days apart), suggesting the
// observations are temporal snapshots that should not be deduplicated.
func hasDistinctTemporalWindows(a, b []temporal.Mention) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	aLatest := temporal.LatestTime(a)
	bLatest := temporal.LatestTime(b)

	if aLatest == nil || bLatest == nil {
		return false
	}

	diff := aLatest.Sub(*bLatest)
	if diff < 0 {
		diff = -diff
	}
	return diff > 7*24*time.Hour
}

// activeNamespaces returns namespaces that have active observations.
func (p *Pipeline) activeNamespaces(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT DISTINCT namespace FROM observations
		WHERE deleted_at IS NULL AND layer >= 1
		ORDER BY namespace
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			continue
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, rows.Err()
}

// cleanupStaleSessions marks sessions that have been active for more than 24 hours
// as 'abandoned'. This prevents zombie sessions from accumulating across namespaces.
func (p *Pipeline) cleanupStaleSessions(ctx context.Context) (int64, error) {
	res, err := p.db.ExecContext(ctx, `
		UPDATE sessions
		SET status = 'abandoned', ended_at = datetime('now')
		WHERE status = 'active'
		  AND started_at < datetime('now', '-24 hours')
	`)
	if err != nil {
		return 0, fmt.Errorf("cleanup stale sessions: %w", err)
	}
	return res.RowsAffected()
}

// createCrossNamespaceRelatesTo evaluates observations with embeddings across
// different namespaces and creates relates_to links when similarity falls in
// the window [RelatedMin, RelatedMax).
// It limits cardinality per observation (top-N) to control link explosion.
func (p *Pipeline) createCrossNamespaceRelatesTo(ctx context.Context) (int64, error) {
	// Get all active observations with embeddings from Working and Core layers
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, namespace, embedding FROM observations
		WHERE deleted_at IS NULL
		  AND layer >= 1
		  AND embedding IS NOT NULL
		ORDER BY namespace, importance DESC, created_at DESC
	`)
	if err != nil {
		return 0, fmt.Errorf("query observations for relates_to: %w", err)
	}
	defer rows.Close()

	type obsItem struct {
		id        string
		namespace string
		embedding []float32
	}

	var items []obsItem
	for rows.Next() {
		var id, ns string
		var blob []byte
		if err := rows.Scan(&id, &ns, &blob); err != nil {
			continue
		}
		vec := embed.DeserializeF32(blob)
		if vec != nil {
			items = append(items, obsItem{id: id, namespace: ns, embedding: vec})
		}
	}

	if len(items) < 2 {
		return 0, nil
	}

	// Load existing links to avoid duplicates
	existingLinks, err := p.loadExistingRelatesToLinks(ctx)
	if err != nil {
		return 0, fmt.Errorf("load existing links: %w", err)
	}

	// Track created links to avoid duplicates within this run
	createdLinks := make(map[string]bool)
	var createdCount int64

	const maxLinksPerObservation = 5 // top-N limit to control link explosion

	// Compare observations across namespaces
	for i := 0; i < len(items); i++ {
		// Collect top candidates for items[i]
		type candidate struct {
			id         string
			similarity float64
		}
		var candidates []candidate

		for j := 0; j < len(items); j++ {
			if i == j {
				continue
			}
			// Skip same namespace
			if items[i].namespace == items[j].namespace {
				continue
			}

			// Check if link already exists (in either direction)
			pairKey := p.makePairKey(items[i].id, items[j].id)
			if existingLinks[pairKey] || createdLinks[pairKey] {
				continue
			}

			sim := embed.CosineSimilarity(items[i].embedding, items[j].embedding)

			// Check if similarity is in the window [RelatedMin, RelatedMax)
			if sim >= p.cfg.RelatedMin && sim < p.cfg.RelatedMax {
				candidates = append(candidates, candidate{id: items[j].id, similarity: sim})
			}
		}

		// Sort by similarity descending and take top-N
		if len(candidates) > 0 {
			// Simple bubble sort for small candidate lists
			for a := 0; a < len(candidates)-1; a++ {
				for b := a + 1; b < len(candidates); b++ {
					if candidates[b].similarity > candidates[a].similarity {
						candidates[a], candidates[b] = candidates[b], candidates[a]
					}
				}
			}

			// Create links for top candidates
			limit := maxLinksPerObservation
			if len(candidates) < limit {
				limit = len(candidates)
			}

			for k := 0; k < limit; k++ {
				pairKey := p.makePairKey(items[i].id, candidates[k].id)
				if createdLinks[pairKey] {
					continue
				}

				_, err := p.db.ExecContext(ctx, `
					INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
					VALUES(?, ?, ?, ?, ?, ?)
				`, p.idGen.New(), items[i].id, candidates[k].id, links.RelationRelatesTo, candidates[k].similarity, links.CreatedByConsolidator)
				if err != nil {
					log.Printf("failed to create relates_to link %s -> %s: %v", items[i].id, candidates[k].id, err)
					continue
				}

				createdLinks[pairKey] = true
				createdCount++
			}
		}
	}

	return createdCount, nil
}

// loadExistingRelatesToLinks loads all existing relates_to links into a map
// for quick duplicate checking. The map key is a normalized pair key.
func (p *Pipeline) loadExistingRelatesToLinks(ctx context.Context) (map[string]bool, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT source_id, target_id FROM observation_links
		WHERE relation_type = ?
	`, links.RelationRelatesTo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := make(map[string]bool)
	for rows.Next() {
		var sourceID, targetID string
		if err := rows.Scan(&sourceID, &targetID); err != nil {
			continue
		}
		links[p.makePairKey(sourceID, targetID)] = true
	}
	return links, rows.Err()
}

// makePairKey creates a normalized key for an unordered pair of observation IDs.
// This ensures that (A,B) and (B,A) produce the same key.
func (p *Pipeline) makePairKey(id1, id2 string) string {
	if id1 < id2 {
		return id1 + "|" + id2
	}
	return id2 + "|" + id1
}
