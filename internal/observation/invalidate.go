package observation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/joeldevz/neurox/internal/links"
)

type InvalidateInput struct {
	ObservationID      string
	Reason             string
	ReplacementTitle   string
	ReplacementContent string
}

func (input InvalidateInput) Validate() error {
	if strings.TrimSpace(input.ObservationID) == "" {
		return fmt.Errorf("observation_id is required")
	}
	if strings.TrimSpace(input.Reason) == "" {
		return fmt.Errorf("reason is required")
	}
	return nil
}

type InvalidateResult struct {
	InvalidatedID string
	ReplacementID string
	LinkID        string
}

// Invalidate marks an observation as stale and halves its confidence.
// If replacement title+content are provided, creates a new observation that supersedes the old one.
func (s *Store) Invalidate(ctx context.Context, linkStore *links.Store, input InvalidateInput) (InvalidateResult, error) {
	if s == nil || s.db == nil {
		return InvalidateResult{}, fmt.Errorf("store is not initialized")
	}
	if err := input.Validate(); err != nil {
		return InvalidateResult{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InvalidateResult{}, fmt.Errorf("begin invalidate transaction: %w", err)
	}

	// Mark the observation as stale and halve confidence
	result, err := tx.ExecContext(ctx, `
		UPDATE observations
		SET staleness = 'stale',
		    confidence = MAX(0.01, confidence * 0.5),
		    updated_at = datetime('now'),
		    modified_epoch = modified_epoch + 1
		WHERE id = ? AND deleted_at IS NULL
	`, input.ObservationID)
	if err != nil {
		_ = tx.Rollback()
		return InvalidateResult{}, fmt.Errorf("invalidate observation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return InvalidateResult{}, fmt.Errorf("read invalidate result: %w", err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return InvalidateResult{}, sql.ErrNoRows
	}

	res := InvalidateResult{InvalidatedID: input.ObservationID}

	// If replacement provided, create new observation and link
	hasReplacement := strings.TrimSpace(input.ReplacementTitle) != "" && strings.TrimSpace(input.ReplacementContent) != ""
	if hasReplacement {
		// Get original observation to inherit namespace, type, kind
		original, err := s.getTx(ctx, tx, input.ObservationID)
		if err != nil {
			_ = tx.Rollback()
			return InvalidateResult{}, fmt.Errorf("get original for replacement: %w", err)
		}

		replacement := Observation{
			Title:           strings.TrimSpace(input.ReplacementTitle),
			Content:         strings.TrimSpace(input.ReplacementContent),
			ObservationType: original.ObservationType,
			Kind:            original.Kind,
			Namespace:       original.Namespace,
			Source:          "agent",
		}
		replacement.ApplyDefaults()

		saved, err := s.saveTx(ctx, tx, replacement)
		if err != nil {
			_ = tx.Rollback()
			return InvalidateResult{}, fmt.Errorf("save replacement: %w", err)
		}
		res.ReplacementID = saved.ID

		// Mark the original as invalidated by the replacement
		if _, err := tx.ExecContext(ctx, `
			UPDATE observations
			SET valid_until = datetime('now'),
			    invalidated_by = ?
			WHERE id = ?
		`, saved.ID, input.ObservationID); err != nil {
			_ = tx.Rollback()
			return InvalidateResult{}, fmt.Errorf("set invalidated_by: %w", err)
		}

		// Create supersedes link
		link, err := linkStore.CreateTx(ctx, tx, links.CreateLinkInput{
			SourceID:     saved.ID,
			TargetID:     input.ObservationID,
			RelationType: links.RelationSupersedes,
			CreatedBy:    links.CreatedByAgent,
		})
		if err != nil {
			_ = tx.Rollback()
			return InvalidateResult{}, fmt.Errorf("create supersedes link: %w", err)
		}
		res.LinkID = link.ID
	}

	if err := tx.Commit(); err != nil {
		return InvalidateResult{}, fmt.Errorf("commit invalidate transaction: %w", err)
	}

	return res, nil
}
