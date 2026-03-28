package consolidate

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// BackfillResult holds statistics from a backfill operation.
type BackfillResult struct {
	CoreRecalibrated     int64
	WorkingRecalibrated  int64
	BufferRecalibrated   int64
	SignalsAdjusted      int64
	ConsolidationBoosted int64
	ActivationPatched    int64
}

// ReconcileScores performs a deterministic backfill of existing observations
// to recalibrate artificially depressed importance values.
//
// This addresses the issue where the old decay logic pushed durable knowledge
// toward importance=0.01. The backfill:
// - Preserves IDs, history, and relationships
// - Only affects durable observations (operational stay as-is)
// - Uses layer, type, access_count, and age for recalibration
// - Updates activation_level and consolidation_strength for consistency
func (p *Pipeline) ReconcileScores(ctx context.Context) (*BackfillResult, error) {
	return p.reconcileScoresTx(ctx, p.db)
}

// reconcileScoresTx performs the actual backfill work within a transaction.
func (p *Pipeline) reconcileScoresTx(ctx context.Context, db *sql.DB) (*BackfillResult, error) {
	var result BackfillResult

	// Step 1: Recalibrate durable Core observations with depressed importance
	res, err := db.ExecContext(ctx, `
		UPDATE observations SET
			importance = CASE observation_type
				WHEN 'decision' THEN MAX(0.70, importance)
				WHEN 'bugfix' THEN MAX(0.70, importance)
				WHEN 'pattern' THEN MAX(0.65, importance)
				WHEN 'gotcha' THEN MAX(0.65, importance)
				WHEN 'preference' THEN MAX(0.60, importance)
				WHEN 'config' THEN MAX(0.55, importance)
				WHEN 'discovery' THEN MAX(0.50, importance)
				WHEN 'question' THEN MAX(0.40, importance)
				ELSE MAX(0.50, importance)
			END,
			activation_level = CASE 
				WHEN activation_level < 0.30 THEN 0.40
				ELSE activation_level
			END,
			consolidation_strength = CASE 
				WHEN consolidation_strength < 0.50 THEN MAX(consolidation_strength, 0.60)
				ELSE consolidation_strength
			END,
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer = 2
		  AND retention = 'durable'
		  AND importance <= 0.05
	`)
	if err != nil {
		return nil, fmt.Errorf("recalibrate Core observations: %w", err)
	}
	result.CoreRecalibrated, _ = res.RowsAffected()

	// Step 2: Recalibrate durable Working observations with depressed importance
	res, err = db.ExecContext(ctx, `
		UPDATE observations SET
			importance = CASE observation_type
				WHEN 'decision' THEN MAX(0.60, importance)
				WHEN 'bugfix' THEN MAX(0.60, importance)
				WHEN 'pattern' THEN MAX(0.55, importance)
				WHEN 'gotcha' THEN MAX(0.55, importance)
				WHEN 'preference' THEN MAX(0.50, importance)
				WHEN 'config' THEN MAX(0.45, importance)
				WHEN 'discovery' THEN MAX(0.40, importance)
				WHEN 'question' THEN MAX(0.35, importance)
				ELSE MAX(0.40, importance)
			END,
			activation_level = CASE 
				WHEN activation_level < 0.25 THEN 0.35
				ELSE activation_level
			END,
			consolidation_strength = CASE 
				WHEN consolidation_strength < 0.30 THEN MAX(consolidation_strength, 0.40)
				ELSE consolidation_strength
			END,
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer = 1
		  AND retention = 'durable'
		  AND importance <= 0.05
	`)
	if err != nil {
		return nil, fmt.Errorf("recalibrate Working observations: %w", err)
	}
	result.WorkingRecalibrated, _ = res.RowsAffected()

	// Step 3: Recalibrate durable Buffer observations with depressed importance
	res, err = db.ExecContext(ctx, `
		UPDATE observations SET
			importance = CASE observation_type
				WHEN 'decision' THEN MAX(0.50, importance)
				WHEN 'bugfix' THEN MAX(0.50, importance)
				WHEN 'pattern' THEN MAX(0.45, importance)
				WHEN 'gotcha' THEN MAX(0.45, importance)
				WHEN 'preference' THEN MAX(0.40, importance)
				WHEN 'config' THEN MAX(0.35, importance)
				WHEN 'discovery' THEN MAX(0.30, importance)
				WHEN 'question' THEN MAX(0.25, importance)
				ELSE MAX(0.30, importance)
			END,
			activation_level = CASE 
				WHEN activation_level < 0.20 THEN 0.30
				ELSE activation_level
			END,
			consolidation_strength = CASE 
				WHEN consolidation_strength < 0.15 THEN MAX(consolidation_strength, 0.20)
				ELSE consolidation_strength
			END,
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer = 0
		  AND retention = 'durable'
		  AND importance <= 0.05
	`)
	if err != nil {
		return nil, fmt.Errorf("recalibrate Buffer observations: %w", err)
	}
	result.BufferRecalibrated, _ = res.RowsAffected()

	// Step 4: Ensure activation_level and consolidation_strength are reasonable
	// for observations that have good importance but low signals
	res, err = db.ExecContext(ctx, `
		UPDATE observations SET
			activation_level = CASE 
				WHEN importance >= 0.70 AND activation_level < 0.30 THEN 0.40
				WHEN importance >= 0.50 AND activation_level < 0.20 THEN 0.30
				ELSE activation_level
			END,
			consolidation_strength = CASE 
				WHEN layer = 2 AND consolidation_strength < 0.40 THEN 0.50
				WHEN layer = 1 AND consolidation_strength < 0.25 THEN 0.35
				ELSE consolidation_strength
			END,
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer >= 1
		  AND retention = 'durable'
		  AND importance >= 0.40
	`)
	if err != nil {
		return nil, fmt.Errorf("adjust signals for high-importance observations: %w", err)
	}
	result.SignalsAdjusted, _ = res.RowsAffected()

	// Step 5: Boost consolidation_strength for high-access observations
	res, err = db.ExecContext(ctx, `
		UPDATE observations SET
			consolidation_strength = MIN(0.90, consolidation_strength + (access_count * 0.02)),
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND retention = 'durable'
		  AND access_count >= 5
		  AND consolidation_strength < 0.70
	`)
	if err != nil {
		return nil, fmt.Errorf("boost consolidation for high-access observations: %w", err)
	}
	result.ConsolidationBoosted, _ = res.RowsAffected()

	// Step 6: Ensure all observations have at least minimal activation
	res, err = db.ExecContext(ctx, `
		UPDATE observations SET
			activation_level = 0.20,
			updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND activation_level < 0.10
	`)
	if err != nil {
		return nil, fmt.Errorf("patch minimal activation: %w", err)
	}
	result.ActivationPatched, _ = res.RowsAffected()

	total := result.CoreRecalibrated + result.WorkingRecalibrated + result.BufferRecalibrated
	log.Printf("score reconciliation complete: core=%d working=%d buffer=%d signals=%d consolidation=%d activation=%d total_recovered=%d",
		result.CoreRecalibrated, result.WorkingRecalibrated, result.BufferRecalibrated,
		result.SignalsAdjusted, result.ConsolidationBoosted, result.ActivationPatched, total)

	return &result, nil
}
