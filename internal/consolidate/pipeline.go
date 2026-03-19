package consolidate

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"neurox/internal/contradiction"
	"neurox/internal/decay"
	"neurox/internal/embed"
	"neurox/internal/filelink"
	"neurox/internal/links"
	"neurox/internal/llm"
	reflectpkg "neurox/internal/reflect"
)

const (
	defaultInterval = 30 * time.Minute
	bufferCap       = 200

	// Promotion thresholds
	bufferToWorkingScore    = 0.3 // importance threshold for Buffer→Working
	workingToCoreAccessMin  = 5   // min access_count for Working→Core
	workingToCoreAgeMinDays = 7   // min age in days for Working→Core

	// Dedup
	dedupCosineThreshold = 0.85
)

type Config struct {
	Interval time.Duration
}

type Pipeline struct {
	db                    *sql.DB
	decay                 *decay.Engine
	embedder              embed.Provider
	gate                  *llm.Gate
	contradictionDetector *contradiction.Detector
	reflectEngine         *reflectpkg.Engine
	cfg                   Config
	stop                  chan struct{}
	wg                    sync.WaitGroup
}

func NewPipeline(db *sql.DB, decayEngine *decay.Engine, embedder embed.Provider, gate *llm.Gate, linkStore *links.Store, llmProvider llm.Provider, idGen filelink.IDGenerator, cfg Config) *Pipeline {
	if cfg.Interval == 0 {
		cfg.Interval = defaultInterval
	}
	return &Pipeline{
		db:                    db,
		decay:                 decayEngine,
		embedder:              embedder,
		gate:                  gate,
		contradictionDetector: contradiction.NewDetector(db, embedder, llmProvider, linkStore),
		reflectEngine:         reflectpkg.NewEngine(db, llmProvider, linkStore, idGen),
		cfg:                   cfg,
		stop:                  make(chan struct{}),
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
	Epoch                int
	Decayed              int64
	PromotedToWorking    int64
	PromotedToCore       int64
	Deduped              int64
	ContradictionsFound  int
	ContradictionsFixed  int
	ReflectionsCreated   int
	Evicted              int64
	GarbageCollected     int64
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

	// 1. Decay
	result.Decayed, err = p.decay.ApplyDecay(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("decay: %w", err)
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

	// 3. Force promote ALL Working → Core
	res, err = p.db.ExecContext(ctx, `
		UPDATE observations
		SET layer = 2, consolidation_status = 'promoted', updated_at = datetime('now')
		WHERE deleted_at IS NULL AND layer = 1
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

	// 7. GC
	result.GarbageCollected, err = p.decay.GarbageCollect(ctx)
	if err != nil {
		log.Printf("gc warning: %v", err)
	}

	p.completeRun(ctx, runID, result)
	duration := time.Since(start)
	log.Printf("forced consolidation epoch %d: decayed=%d promoted_working=%d promoted_core=%d deduped=%d contradictions=%d/%d reflections=%d gc=%d (%v)",
		epoch, result.Decayed, result.PromotedToWorking, result.PromotedToCore,
		result.Deduped, result.ContradictionsFixed, result.ContradictionsFound,
		result.ReflectionsCreated, result.GarbageCollected, duration)

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

	// 1. Decay
	result.Decayed, err = p.decay.ApplyDecay(ctx)
	if err != nil {
		p.failRun(ctx, runID, err)
		return fmt.Errorf("decay: %w", err)
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

	// 8. Evict buffer overflow
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
	log.Printf("consolidation epoch %d: decayed=%d promoted_working=%d promoted_core=%d deduped=%d contradictions=%d/%d reflections=%d evicted=%d gc=%d gate=%s (%v)",
		epoch, result.Decayed, result.PromotedToWorking, result.PromotedToCore,
		result.Deduped, result.ContradictionsFixed, result.ContradictionsFound,
		result.ReflectionsCreated, result.Evicted, result.GarbageCollected, p.gate.Mode(), duration)

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

// promoteBufferToWorking promotes Buffer observations that meet the importance threshold
// or are procedural (auto-promote). When the gate is in auto/full mode, uncertain
// candidates are evaluated by the LLM.
func (p *Pipeline) promoteBufferToWorking(ctx context.Context, epoch int) (int64, error) {
	if p.gate.Mode() == llm.GateModeOff {
		// Pure heuristic: same as before
		result, err := p.db.ExecContext(ctx, `
			UPDATE observations
			SET layer = 1,
			    consolidation_status = 'promoted',
			    updated_at = datetime('now')
			WHERE deleted_at IS NULL
			  AND layer = 0
			  AND (importance >= ? OR kind = 'procedural')
			  AND consolidation_status = 'pending'
		`, bufferToWorkingScore)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}

	// Gate-assisted promotion: fetch candidates and evaluate
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, title, content, observation_type, importance, access_count
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
		importance      float64
		accessCount     int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.title, &c.content, &c.observationType, &c.importance, &c.accessCount); err != nil {
			continue
		}
		candidates = append(candidates, c)
	}

	var promoted int64
	for _, c := range candidates {
		// Auto-promote procedural or high-importance
		if c.observationType == "procedural" || c.importance >= 0.7 {
			if _, err := p.db.ExecContext(ctx, `
				UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now')
				WHERE id = ?
			`, c.id); err == nil {
				promoted++
			}
			continue
		}

		// Below threshold → skip
		if c.importance < bufferToWorkingScore {
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
			// On error, use heuristic
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

// promoteWorkingToCore promotes Working observations with sustained access and sufficient age.
func (p *Pipeline) promoteWorkingToCore(ctx context.Context) (int64, error) {
	result, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET layer = 2,
		    consolidation_status = 'promoted',
		    updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer = 1
		  AND access_count >= ?
		  AND julianday('now') - julianday(created_at) >= ?
	`, workingToCoreAccessMin, workingToCoreAgeMinDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
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
	result, err := p.db.ExecContext(ctx, `
		UPDATE observations
		SET deleted_at = datetime('now'), updated_at = datetime('now')
		WHERE id IN (
			SELECT id FROM observations
			WHERE layer = 0 AND deleted_at IS NULL
			ORDER BY importance ASC, created_at ASC
			LIMIT ?
		)
	`, excess)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// dedup merges near-duplicate observations using cosine similarity.
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
			if sim >= dedupCosineThreshold {
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

	return deduped, nil
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
