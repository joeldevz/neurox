package export

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

// ImportJSONStats holds counts of all imported entities.
type ImportJSONStats struct {
	Observations      int
	Links             int
	FileObservations  int
	Facts             int
	TemporalMentions  int
	Sessions          int
	Reflections       int
	ConsolidationRuns int
}

// ImportJSON reads a JSON export file and restores all data into the database.
// Uses INSERT OR IGNORE to avoid conflicts on re-import, preserving existing data.
// Returns the number of observations imported.
func ImportJSON(ctx context.Context, db *sql.DB, sourcePath string) (int, error) {
	stats, err := ImportJSONWithStats(ctx, db, sourcePath)
	if err != nil {
		return 0, err
	}
	return stats.Observations, nil
}

// ImportJSONWithStats reads a JSON export file and returns detailed import stats.
func ImportJSONWithStats(ctx context.Context, db *sql.DB, sourcePath string) (ImportJSONStats, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("read file: %w", err)
	}

	var export FullExport
	if err := json.Unmarshal(data, &export); err != nil {
		return ImportJSONStats{}, fmt.Errorf("parse JSON: %w", err)
	}

	var stats ImportJSONStats

	// Import in dependency order: observations first, then tables that reference them.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stats.Observations, err = importObservationsJSON(ctx, tx, export.Observations)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import observations: %w", err)
	}

	stats.Sessions, err = importSessionsJSON(ctx, tx, export.Sessions)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import sessions: %w", err)
	}

	stats.Links, err = importObservationLinksJSON(ctx, tx, export.ObservationLinks)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import observation_links: %w", err)
	}

	stats.FileObservations, err = importFileObservationsJSON(ctx, tx, export.FileObservations)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import file_observations: %w", err)
	}

	stats.Facts, err = importFactsJSON(ctx, tx, export.Facts)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import facts: %w", err)
	}

	stats.TemporalMentions, err = importTemporalMentionsJSON(ctx, tx, export.TemporalMentions)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import temporal_mentions: %w", err)
	}

	stats.Reflections, err = importReflectionsJSON(ctx, tx, export.Reflections)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import reflections: %w", err)
	}

	stats.ConsolidationRuns, err = importConsolidationRunsJSON(ctx, tx, export.ConsolidationRuns)
	if err != nil {
		return ImportJSONStats{}, fmt.Errorf("import consolidation_runs: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return ImportJSONStats{}, fmt.Errorf("commit transaction: %w", err)
	}

	return stats, nil
}

// --- Table importers ---

func importObservationsJSON(ctx context.Context, tx *sql.Tx, observations []ObservationRow) (int, error) {
	if len(observations) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO observations (
			id, title, content, observation_type, layer, confidence, importance,
			access_count, last_accessed, repetition_count, decay_rate,
			kind, tags, namespace, source, topic_key,
			valid_from, valid_until, invalidated_by,
			staleness, consolidation_status, rejection_epoch,
			embedding,
			created_at, updated_at, deleted_at,
			modified_epoch, activation_level, consolidation_strength,
			source_surface, source_session_id, source_tool
		) VALUES (
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?
		)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, o := range observations {
		var embeddingBytes []byte
		if o.Embedding != nil {
			embeddingBytes, err = base64.StdEncoding.DecodeString(*o.Embedding)
			if err != nil {
				return count, fmt.Errorf("decode embedding for %s: %w", o.ID, err)
			}
		}

		_, err := stmt.ExecContext(ctx,
			o.ID, o.Title, o.Content, o.ObservationType, o.Layer,
			o.Confidence, o.Importance, o.AccessCount,
			ptrToNullStr(o.LastAccessed), o.RepetitionCount, o.DecayRate,
			o.Kind, ptrToNullStr(o.Tags), o.Namespace,
			ptrToNullStr(o.Source), ptrToNullStr(o.TopicKey),
			o.ValidFrom, ptrToNullStr(o.ValidUntil), ptrToNullStr(o.InvalidatedBy),
			o.Staleness, o.ConsolidationStatus, ptrIntToNull(o.RejectionEpoch),
			nullBlob(embeddingBytes),
			o.CreatedAt, o.UpdatedAt, ptrToNullStr(o.DeletedAt),
			o.ModifiedEpoch, o.ActivationLevel, o.ConsolidationStrength,
			ptrToNullStr(o.SourceSurface), ptrToNullStr(o.SourceSessionID), ptrToNullStr(o.SourceTool),
		)
		if err != nil {
			return count, fmt.Errorf("insert observation %s: %w", o.ID, err)
		}
		count++
	}
	return count, nil
}

func importSessionsJSON(ctx context.Context, tx *sql.Tx, sessions []SessionRow) (int, error) {
	if len(sessions) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO sessions (
			id, title, directory, branch, namespace, status, summary, started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, s := range sessions {
		_, err := stmt.ExecContext(ctx,
			s.ID, ptrToNullStr(s.Title), ptrToNullStr(s.Directory),
			ptrToNullStr(s.Branch), s.Namespace, s.Status,
			ptrToNullStr(s.Summary), s.StartedAt, ptrToNullStr(s.EndedAt),
		)
		if err != nil {
			return count, fmt.Errorf("insert session %s: %w", s.ID, err)
		}
		count++
	}
	return count, nil
}

func importObservationLinksJSON(ctx context.Context, tx *sql.Tx, links []ObservationLinkRow) (int, error) {
	if len(links) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO observation_links (
			id, source_id, target_id, relation_type, confidence, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, l := range links {
		_, err := stmt.ExecContext(ctx,
			l.ID, l.SourceID, l.TargetID, l.RelationType,
			l.Confidence, l.CreatedBy, l.CreatedAt,
		)
		if err != nil {
			return count, fmt.Errorf("insert link %s: %w", l.ID, err)
		}
		count++
	}
	return count, nil
}

func importFileObservationsJSON(ctx context.Context, tx *sql.Tx, fileObs []FileObservationRow) (int, error) {
	if len(fileObs) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO file_observations (
			id, observation_id, file_path, commit_sha_from, commit_sha_until,
			valid_from, valid_until, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, f := range fileObs {
		_, err := stmt.ExecContext(ctx,
			f.ID, f.ObservationID, f.FilePath,
			ptrToNullStr(f.CommitSHAFrom), ptrToNullStr(f.CommitSHAUntil),
			f.ValidFrom, ptrToNullStr(f.ValidUntil), f.CreatedAt,
		)
		if err != nil {
			return count, fmt.Errorf("insert file_observation %s: %w", f.ID, err)
		}
		count++
	}
	return count, nil
}

func importFactsJSON(ctx context.Context, tx *sql.Tx, facts []FactRow) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO facts (
			id, subject, predicate, object, observation_id, namespace,
			valid_from, valid_until, superseded_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, f := range facts {
		_, err := stmt.ExecContext(ctx,
			f.ID, f.Subject, f.Predicate, f.Object,
			ptrToNullStr(f.ObservationID), f.Namespace,
			f.ValidFrom, ptrToNullStr(f.ValidUntil),
			ptrToNullStr(f.SupersededBy), f.CreatedAt,
		)
		if err != nil {
			return count, fmt.Errorf("insert fact %s: %w", f.ID, err)
		}
		count++
	}
	return count, nil
}

func importTemporalMentionsJSON(ctx context.Context, tx *sql.Tx, mentions []TemporalMentionRow) (int, error) {
	if len(mentions) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO temporal_mentions (
			id, observation_id, raw_text, mention_kind,
			normalized_start, normalized_end, anchor_time, confidence, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, t := range mentions {
		_, err := stmt.ExecContext(ctx,
			t.ID, t.ObservationID, t.RawText, t.MentionKind,
			ptrToNullStr(t.NormalizedStart), ptrToNullStr(t.NormalizedEnd),
			t.AnchorTime, t.Confidence, t.CreatedAt,
		)
		if err != nil {
			return count, fmt.Errorf("insert temporal_mention %s: %w", t.ID, err)
		}
		count++
	}
	return count, nil
}

func importReflectionsJSON(ctx context.Context, tx *sql.Tx, reflections []ReflectionRow) (int, error) {
	if len(reflections) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO reflections (
			id, content, source_observation_ids, namespace, layer, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, r := range reflections {
		_, err := stmt.ExecContext(ctx,
			r.ID, r.Content, r.SourceObservationIDs,
			r.Namespace, r.Layer, r.CreatedAt,
		)
		if err != nil {
			return count, fmt.Errorf("insert reflection %s: %w", r.ID, err)
		}
		count++
	}
	return count, nil
}

func importConsolidationRunsJSON(ctx context.Context, tx *sql.Tx, runs []ConsolidationRunRow) (int, error) {
	if len(runs) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO consolidation_runs (
			id, status, epoch, observations_processed, observations_promoted,
			observations_deduped, contradictions_found, reflections_created,
			started_at, completed_at, error_message, llm_tokens_used
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, c := range runs {
		_, err := stmt.ExecContext(ctx,
			c.ID, c.Status, c.Epoch,
			c.ObservationsProcessed, c.ObservationsPromoted,
			c.ObservationsDeduped, c.ContradictionsFound, c.ReflectionsCreated,
			c.StartedAt, ptrToNullStr(c.CompletedAt),
			ptrToNullStr(c.ErrorMessage), c.LLMTokensUsed,
		)
		if err != nil {
			return count, fmt.Errorf("insert consolidation_run %s: %w", c.ID, err)
		}
		count++
	}
	return count, nil
}

// --- Helpers ---

// ptrToNullStr converts *string to a value suitable for SQL: nil → NULL, otherwise the string.
func ptrToNullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ptrIntToNull converts *int to a value suitable for SQL: nil → NULL, otherwise the int.
func ptrIntToNull(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

// nullBlob returns nil for empty byte slices (SQL NULL), otherwise the bytes.
func nullBlob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
