package export

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// FullExport represents the complete database state for JSON export/import.
type FullExport struct {
	Version           string                `json:"version"`
	ExportedAt        string                `json:"exported_at"`
	Namespace         string                `json:"namespace,omitempty"`
	Observations      []ObservationRow      `json:"observations"`
	ObservationLinks  []ObservationLinkRow  `json:"observation_links"`
	FileObservations  []FileObservationRow  `json:"file_observations"`
	Facts             []FactRow             `json:"facts"`
	TemporalMentions  []TemporalMentionRow  `json:"temporal_mentions"`
	Sessions          []SessionRow          `json:"sessions"`
	Reflections       []ReflectionRow       `json:"reflections"`
	ConsolidationRuns []ConsolidationRunRow `json:"consolidation_runs"`
}

// ObservationRow mirrors all columns from the observations table.
type ObservationRow struct {
	ID                    string  `json:"id"`
	Title                 string  `json:"title"`
	Content               string  `json:"content"`
	ObservationType       string  `json:"observation_type"`
	Layer                 int     `json:"layer"`
	Confidence            float64 `json:"confidence"`
	Importance            float64 `json:"importance"`
	AccessCount           int     `json:"access_count"`
	LastAccessed          *string `json:"last_accessed"`
	RepetitionCount       int     `json:"repetition_count"`
	DecayRate             float64 `json:"decay_rate"`
	Kind                  string  `json:"kind"`
	Tags                  *string `json:"tags"`
	Namespace             string  `json:"namespace"`
	Source                *string `json:"source"`
	TopicKey              *string `json:"topic_key"`
	ValidFrom             string  `json:"valid_from"`
	ValidUntil            *string `json:"valid_until"`
	InvalidatedBy         *string `json:"invalidated_by"`
	Staleness             string  `json:"staleness"`
	ConsolidationStatus   string  `json:"consolidation_status"`
	RejectionEpoch        *int    `json:"rejection_epoch"`
	Embedding             *string `json:"embedding,omitempty"` // base64-encoded
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
	DeletedAt             *string `json:"deleted_at"`
	ModifiedEpoch         int     `json:"modified_epoch"`
	ActivationLevel       float64 `json:"activation_level"`
	ConsolidationStrength float64 `json:"consolidation_strength"`
	SourceSurface         *string `json:"source_surface"`
	SourceSessionID       *string `json:"source_session_id"`
	SourceTool            *string `json:"source_tool"`
}

// ObservationLinkRow mirrors all columns from the observation_links table.
type ObservationLinkRow struct {
	ID           string  `json:"id"`
	SourceID     string  `json:"source_id"`
	TargetID     string  `json:"target_id"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	CreatedBy    string  `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
}

// FileObservationRow mirrors all columns from the file_observations table.
type FileObservationRow struct {
	ID             string  `json:"id"`
	ObservationID  string  `json:"observation_id"`
	FilePath       string  `json:"file_path"`
	CommitSHAFrom  *string `json:"commit_sha_from"`
	CommitSHAUntil *string `json:"commit_sha_until"`
	ValidFrom      string  `json:"valid_from"`
	ValidUntil     *string `json:"valid_until"`
	CreatedAt      string  `json:"created_at"`
}

// FactRow mirrors all columns from the facts table.
type FactRow struct {
	ID            string  `json:"id"`
	Subject       string  `json:"subject"`
	Predicate     string  `json:"predicate"`
	Object        string  `json:"object"`
	ObservationID *string `json:"observation_id"`
	Namespace     string  `json:"namespace"`
	ValidFrom     string  `json:"valid_from"`
	ValidUntil    *string `json:"valid_until"`
	SupersededBy  *string `json:"superseded_by"`
	CreatedAt     string  `json:"created_at"`
}

// TemporalMentionRow mirrors all columns from the temporal_mentions table.
type TemporalMentionRow struct {
	ID              string  `json:"id"`
	ObservationID   string  `json:"observation_id"`
	RawText         string  `json:"raw_text"`
	MentionKind     string  `json:"mention_kind"`
	NormalizedStart *string `json:"normalized_start"`
	NormalizedEnd   *string `json:"normalized_end"`
	AnchorTime      string  `json:"anchor_time"`
	Confidence      float64 `json:"confidence"`
	CreatedAt       string  `json:"created_at"`
}

// SessionRow mirrors all columns from the sessions table.
type SessionRow struct {
	ID        string  `json:"id"`
	Title     *string `json:"title"`
	Directory *string `json:"directory"`
	Branch    *string `json:"branch"`
	Namespace string  `json:"namespace"`
	Status    string  `json:"status"`
	Summary   *string `json:"summary"`
	StartedAt string  `json:"started_at"`
	EndedAt   *string `json:"ended_at"`
}

// ReflectionRow mirrors all columns from the reflections table.
type ReflectionRow struct {
	ID                   string `json:"id"`
	Content              string `json:"content"`
	SourceObservationIDs string `json:"source_observation_ids"`
	Namespace            string `json:"namespace"`
	Layer                int    `json:"layer"`
	CreatedAt            string `json:"created_at"`
}

// ConsolidationRunRow mirrors all columns from the consolidation_runs table.
type ConsolidationRunRow struct {
	ID                    string  `json:"id"`
	Status                string  `json:"status"`
	Epoch                 int     `json:"epoch"`
	ObservationsProcessed int     `json:"observations_processed"`
	ObservationsPromoted  int     `json:"observations_promoted"`
	ObservationsDeduped   int     `json:"observations_deduped"`
	ContradictionsFound   int     `json:"contradictions_found"`
	ReflectionsCreated    int     `json:"reflections_created"`
	StartedAt             string  `json:"started_at"`
	CompletedAt           *string `json:"completed_at"`
	ErrorMessage          *string `json:"error_message"`
	LLMTokensUsed         int     `json:"llm_tokens_used"`
}

// ExportJSONStats returns counts of all exported entities for display.
type ExportJSONStats struct {
	Observations      int
	Links             int
	FileObservations  int
	Facts             int
	TemporalMentions  int
	Sessions          int
	Reflections       int
	ConsolidationRuns int
}

// ExportJSONWithStats writes the entire database state and returns detailed stats.
func ExportJSONWithStats(ctx context.Context, db *sql.DB, namespace, outputPath string) (ExportJSONStats, error) {
	export := FullExport{
		Version:    "1.0",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Namespace:  namespace,
	}

	var err error

	export.Observations, err = loadObservationsJSON(ctx, db, namespace)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load observations: %w", err)
	}

	export.ObservationLinks, err = loadObservationLinksJSON(ctx, db)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load observation_links: %w", err)
	}

	export.FileObservations, err = loadFileObservationsJSON(ctx, db)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load file_observations: %w", err)
	}

	export.Facts, err = loadFactsJSON(ctx, db, namespace)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load facts: %w", err)
	}

	export.TemporalMentions, err = loadTemporalMentionsJSON(ctx, db)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load temporal_mentions: %w", err)
	}

	export.Sessions, err = loadSessionsJSON(ctx, db, namespace)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load sessions: %w", err)
	}

	export.Reflections, err = loadReflectionsJSON(ctx, db, namespace)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load reflections: %w", err)
	}

	export.ConsolidationRuns, err = loadConsolidationRunsJSON(ctx, db)
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("load consolidation_runs: %w", err)
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return ExportJSONStats{}, fmt.Errorf("marshal JSON: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return ExportJSONStats{}, fmt.Errorf("write file: %w", err)
	}

	return ExportJSONStats{
		Observations:      len(export.Observations),
		Links:             len(export.ObservationLinks),
		FileObservations:  len(export.FileObservations),
		Facts:             len(export.Facts),
		TemporalMentions:  len(export.TemporalMentions),
		Sessions:          len(export.Sessions),
		Reflections:       len(export.Reflections),
		ConsolidationRuns: len(export.ConsolidationRuns),
	}, nil
}

// --- Table loaders ---

func loadObservationsJSON(ctx context.Context, db *sql.DB, namespace string) ([]ObservationRow, error) {
	query := `
		SELECT id, title, content, observation_type, layer, confidence, importance,
		       access_count, last_accessed, repetition_count, decay_rate,
		       kind, tags, namespace, source, topic_key,
		       valid_from, valid_until, invalidated_by,
		       staleness, consolidation_status, rejection_epoch,
		       embedding,
		       created_at, updated_at, deleted_at,
		       modified_epoch, activation_level, consolidation_strength,
		       source_surface, source_session_id, source_tool
		FROM observations`
	args := []any{}
	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY created_at ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ObservationRow
	for rows.Next() {
		var o ObservationRow
		var (
			lastAccessed    sql.NullString
			tags            sql.NullString
			source          sql.NullString
			topicKey        sql.NullString
			validUntil      sql.NullString
			invalidatedBy   sql.NullString
			rejectionEpoch  sql.NullInt64
			embedding       []byte
			deletedAt       sql.NullString
			sourceSurface   sql.NullString
			sourceSessionID sql.NullString
			sourceTool      sql.NullString
		)

		if err := rows.Scan(
			&o.ID, &o.Title, &o.Content, &o.ObservationType, &o.Layer,
			&o.Confidence, &o.Importance, &o.AccessCount, &lastAccessed,
			&o.RepetitionCount, &o.DecayRate, &o.Kind, &tags, &o.Namespace,
			&source, &topicKey, &o.ValidFrom, &validUntil, &invalidatedBy,
			&o.Staleness, &o.ConsolidationStatus, &rejectionEpoch,
			&embedding, &o.CreatedAt, &o.UpdatedAt, &deletedAt,
			&o.ModifiedEpoch, &o.ActivationLevel, &o.ConsolidationStrength,
			&sourceSurface, &sourceSessionID, &sourceTool,
		); err != nil {
			return nil, fmt.Errorf("scan observation: %w", err)
		}

		o.LastAccessed = nullStringPtr(lastAccessed)
		o.Tags = nullStringPtr(tags)
		o.Source = nullStringPtr(source)
		o.TopicKey = nullStringPtr(topicKey)
		o.ValidUntil = nullStringPtr(validUntil)
		o.InvalidatedBy = nullStringPtr(invalidatedBy)
		o.DeletedAt = nullStringPtr(deletedAt)
		o.SourceSurface = nullStringPtr(sourceSurface)
		o.SourceSessionID = nullStringPtr(sourceSessionID)
		o.SourceTool = nullStringPtr(sourceTool)

		if rejectionEpoch.Valid {
			v := int(rejectionEpoch.Int64)
			o.RejectionEpoch = &v
		}
		if len(embedding) > 0 {
			enc := base64.StdEncoding.EncodeToString(embedding)
			o.Embedding = &enc
		}

		result = append(result, o)
	}
	return result, rows.Err()
}

func loadObservationLinksJSON(ctx context.Context, db *sql.DB) ([]ObservationLinkRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, source_id, target_id, relation_type, confidence, created_by, created_at
		FROM observation_links
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ObservationLinkRow
	for rows.Next() {
		var l ObservationLinkRow
		if err := rows.Scan(&l.ID, &l.SourceID, &l.TargetID, &l.RelationType,
			&l.Confidence, &l.CreatedBy, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

func loadFileObservationsJSON(ctx context.Context, db *sql.DB) ([]FileObservationRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, observation_id, file_path, commit_sha_from, commit_sha_until,
		       valid_from, valid_until, created_at
		FROM file_observations
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FileObservationRow
	for rows.Next() {
		var f FileObservationRow
		var commitFrom, commitUntil, validUntil sql.NullString
		if err := rows.Scan(&f.ID, &f.ObservationID, &f.FilePath,
			&commitFrom, &commitUntil, &f.ValidFrom, &validUntil, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan file_observation: %w", err)
		}
		f.CommitSHAFrom = nullStringPtr(commitFrom)
		f.CommitSHAUntil = nullStringPtr(commitUntil)
		f.ValidUntil = nullStringPtr(validUntil)
		result = append(result, f)
	}
	return result, rows.Err()
}

func loadFactsJSON(ctx context.Context, db *sql.DB, namespace string) ([]FactRow, error) {
	query := `
		SELECT id, subject, predicate, object, observation_id, namespace,
		       valid_from, valid_until, superseded_by, created_at
		FROM facts`
	args := []any{}
	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY created_at ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []FactRow
	for rows.Next() {
		var f FactRow
		var obsID, validUntil, supersededBy sql.NullString
		if err := rows.Scan(&f.ID, &f.Subject, &f.Predicate, &f.Object,
			&obsID, &f.Namespace, &f.ValidFrom, &validUntil, &supersededBy, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		f.ObservationID = nullStringPtr(obsID)
		f.ValidUntil = nullStringPtr(validUntil)
		f.SupersededBy = nullStringPtr(supersededBy)
		result = append(result, f)
	}
	return result, rows.Err()
}

func loadTemporalMentionsJSON(ctx context.Context, db *sql.DB) ([]TemporalMentionRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, observation_id, raw_text, mention_kind,
		       normalized_start, normalized_end, anchor_time, confidence, created_at
		FROM temporal_mentions
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TemporalMentionRow
	for rows.Next() {
		var t TemporalMentionRow
		var normStart, normEnd sql.NullString
		if err := rows.Scan(&t.ID, &t.ObservationID, &t.RawText, &t.MentionKind,
			&normStart, &normEnd, &t.AnchorTime, &t.Confidence, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan temporal_mention: %w", err)
		}
		t.NormalizedStart = nullStringPtr(normStart)
		t.NormalizedEnd = nullStringPtr(normEnd)
		result = append(result, t)
	}
	return result, rows.Err()
}

func loadSessionsJSON(ctx context.Context, db *sql.DB, namespace string) ([]SessionRow, error) {
	query := `
		SELECT id, title, directory, branch, namespace, status, summary, started_at, ended_at
		FROM sessions`
	args := []any{}
	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY started_at ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var s SessionRow
		var title, dir, branch, summary, endedAt sql.NullString
		if err := rows.Scan(&s.ID, &title, &dir, &branch, &s.Namespace,
			&s.Status, &summary, &s.StartedAt, &endedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		s.Title = nullStringPtr(title)
		s.Directory = nullStringPtr(dir)
		s.Branch = nullStringPtr(branch)
		s.Summary = nullStringPtr(summary)
		s.EndedAt = nullStringPtr(endedAt)
		result = append(result, s)
	}
	return result, rows.Err()
}

func loadReflectionsJSON(ctx context.Context, db *sql.DB, namespace string) ([]ReflectionRow, error) {
	query := `
		SELECT id, content, source_observation_ids, namespace, layer, created_at
		FROM reflections`
	args := []any{}
	if namespace != "" {
		query += " WHERE namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY created_at ASC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ReflectionRow
	for rows.Next() {
		var r ReflectionRow
		if err := rows.Scan(&r.ID, &r.Content, &r.SourceObservationIDs,
			&r.Namespace, &r.Layer, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan reflection: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func loadConsolidationRunsJSON(ctx context.Context, db *sql.DB) ([]ConsolidationRunRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, status, epoch, observations_processed, observations_promoted,
		       observations_deduped, contradictions_found, reflections_created,
		       started_at, completed_at, error_message, llm_tokens_used
		FROM consolidation_runs
		ORDER BY started_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ConsolidationRunRow
	for rows.Next() {
		var c ConsolidationRunRow
		var completedAt, errorMsg sql.NullString
		if err := rows.Scan(&c.ID, &c.Status, &c.Epoch,
			&c.ObservationsProcessed, &c.ObservationsPromoted,
			&c.ObservationsDeduped, &c.ContradictionsFound, &c.ReflectionsCreated,
			&c.StartedAt, &completedAt, &errorMsg, &c.LLMTokensUsed); err != nil {
			return nil, fmt.Errorf("scan consolidation_run: %w", err)
		}
		c.CompletedAt = nullStringPtr(completedAt)
		c.ErrorMessage = nullStringPtr(errorMsg)
		result = append(result, c)
	}
	return result, rows.Err()
}

// nullStringPtr converts sql.NullString to *string.
func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}
