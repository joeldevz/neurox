package observation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/joeldevz/neurox/internal/filelink"
)

type Store struct {
	db          *sql.DB
	idGenerator filelink.IDGenerator
	fileLinks   *filelink.Store
	writeGate   WriteGate
	temporal    TemporalExtractor
}

func NewStore(database *sql.DB, gate WriteGate) *Store {
	idGenerator := newULIDGenerator()
	if gate == nil {
		gate = NewNoopWriteGate()
	}
	return &Store{
		db:          database,
		idGenerator: idGenerator,
		fileLinks:   filelink.NewStore(idGenerator),
		writeGate:   gate,
	}
}

// SetTemporalExtractor configures temporal extraction for saved observations.
func (s *Store) SetTemporalExtractor(te TemporalExtractor) {
	s.temporal = te
}

func (s *Store) Save(ctx context.Context, input Observation) (Observation, error) {
	if s == nil || s.db == nil {
		return Observation{}, fmt.Errorf("store is not initialized")
	}

	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return Observation{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Observation{}, fmt.Errorf("begin save transaction: %w", err)
	}

	saved, err := s.saveTx(ctx, tx, input)
	if err != nil {
		_ = tx.Rollback()
		return Observation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Observation{}, fmt.Errorf("commit save transaction: %w", err)
	}

	s.extractTemporal(ctx, saved)
	s.writeGate.CheckAsync(saved)
	return saved, nil
}

// extractTemporal runs the temporal extractor if configured. Failures are silently ignored
// to avoid blocking observation persistence.
func (s *Store) extractTemporal(ctx context.Context, obs Observation) {
	if s.temporal == nil {
		return
	}
	// Best-effort: do not propagate errors.
	_, _ = s.temporal.Extract(ctx, obs.ID, obs.Content)
}

func (s *Store) Update(ctx context.Context, input Observation) (Observation, error) {
	if s == nil || s.db == nil {
		return Observation{}, fmt.Errorf("store is not initialized")
	}
	input.ApplyDefaults()
	if strings.TrimSpace(input.ID) == "" {
		return Observation{}, fmt.Errorf("id is required")
	}
	if err := input.Validate(); err != nil {
		return Observation{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Observation{}, fmt.Errorf("begin update transaction: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE observations
		SET title = ?,
		    content = ?,
		    observation_type = ?,
		    kind = ?,
		    confidence = ?,
		    tags = ?,
		    namespace = ?,
		    topic_key = ?,
		    layer = ?,
		    retention = ?,
		    importance = ?,
		    activation_level = ?,
		    consolidation_strength = ?,
		    source_surface = ?,
		    source_session_id = ?,
		    source_tool = ?,
		    updated_at = datetime('now'),
		    modified_epoch = modified_epoch + 1
		WHERE id = ? AND deleted_at IS NULL
	`, input.Title, input.Content, input.ObservationType, input.Kind, input.Confidence, TagsValue(input.Tags), input.Namespace, nullableString(input.TopicKey), input.Layer, input.Retention, input.Importance, input.ActivationLevel, input.ConsolidationStrength, nullableString(input.SourceSurface), nullableString(input.SourceSessionID), nullableString(input.SourceTool), input.ID)
	if err != nil {
		_ = tx.Rollback()
		return Observation{}, fmt.Errorf("update observation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return Observation{}, fmt.Errorf("read update result: %w", err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return Observation{}, sql.ErrNoRows
	}

	if len(input.Files) > 0 {
		if err := s.fileLinks.ReplaceLinks(ctx, tx, input.ID, input.Files); err != nil {
			_ = tx.Rollback()
			return Observation{}, err
		}
	}

	updated, err := s.getTx(ctx, tx, input.ID)
	if err != nil {
		_ = tx.Rollback()
		return Observation{}, err
	}

	if err := tx.Commit(); err != nil {
		return Observation{}, fmt.Errorf("commit update transaction: %w", err)
	}

	s.writeGate.CheckAsync(updated)
	return updated, nil
}

func (s *Store) Get(ctx context.Context, id string) (Observation, error) {
	if s == nil || s.db == nil {
		return Observation{}, fmt.Errorf("store is not initialized")
	}
	return s.getTx(ctx, s.db, id)
}

func (s *Store) SoftDelete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store is not initialized")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE observations
		SET deleted_at = datetime('now'), updated_at = datetime('now'), modified_epoch = modified_epoch + 1
		WHERE id = ? AND deleted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("soft delete observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delete result: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) saveTx(ctx context.Context, tx *sql.Tx, input Observation) (Observation, error) {
	existingID, err := s.findActiveByTopicKey(ctx, tx, input.Namespace, input.TopicKey)
	if err != nil {
		return Observation{}, err
	}
	if existingID != "" {
		input.ID = existingID
		return s.updateTx(ctx, tx, input)
	}

	if input.ID == "" {
		input.ID = s.idGenerator.New()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO observations(
			id, title, content, observation_type, layer, confidence, kind, tags, namespace, topic_key, retention,
			importance, activation_level, consolidation_strength,
			source_surface, source_session_id, source_tool
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, input.Title, input.Content, input.ObservationType, input.Layer, input.Confidence, input.Kind, TagsValue(input.Tags), input.Namespace, nullableString(input.TopicKey), input.Retention,
		input.Importance, input.ActivationLevel, input.ConsolidationStrength,
		nullableString(input.SourceSurface), nullableString(input.SourceSessionID), nullableString(input.SourceTool)); err != nil {
		return Observation{}, fmt.Errorf("insert observation: %w", err)
	}

	if len(input.Files) > 0 {
		if err := s.fileLinks.ReplaceLinks(ctx, tx, input.ID, input.Files); err != nil {
			return Observation{}, err
		}
	}

	return s.getTx(ctx, tx, input.ID)
}

func (s *Store) updateTx(ctx context.Context, tx *sql.Tx, input Observation) (Observation, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE observations
		SET title = ?,
		    content = ?,
		    observation_type = ?,
		    kind = ?,
		    confidence = ?,
		    tags = ?,
		    namespace = ?,
		    topic_key = ?,
		    layer = ?,
		    retention = ?,
		    importance = ?,
		    activation_level = ?,
		    consolidation_strength = ?,
		    source_surface = ?,
		    source_session_id = ?,
		    source_tool = ?,
		    updated_at = datetime('now'),
		    modified_epoch = modified_epoch + 1
		WHERE id = ? AND deleted_at IS NULL
	`, input.Title, input.Content, input.ObservationType, input.Kind, input.Confidence, TagsValue(input.Tags), input.Namespace, nullableString(input.TopicKey), LayerBuffer, input.Retention, input.Importance, input.ActivationLevel, input.ConsolidationStrength, nullableString(input.SourceSurface), nullableString(input.SourceSessionID), nullableString(input.SourceTool), input.ID)
	if err != nil {
		return Observation{}, fmt.Errorf("upsert observation by topic_key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Observation{}, fmt.Errorf("read upsert result: %w", err)
	}
	if affected == 0 {
		return Observation{}, sql.ErrNoRows
	}

	if len(input.Files) > 0 {
		if err := s.fileLinks.ReplaceLinks(ctx, tx, input.ID, input.Files); err != nil {
			return Observation{}, err
		}
	}

	return s.getTx(ctx, tx, input.ID)
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) getTx(ctx context.Context, querier rowQueryer, id string) (Observation, error) {
	var observation Observation
	var tags sql.NullString
	var source sql.NullString
	var topicKey sql.NullString
	var sourceSurface sql.NullString
	var sourceSessionID sql.NullString
	var sourceTool sql.NullString
	var deletedAt sql.NullString
	var createdAt string
	var updatedAt string

	err := querier.QueryRowContext(ctx, `
		SELECT id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, tags, namespace, source, topic_key, retention, source_surface, source_session_id, source_tool, created_at, updated_at, deleted_at
		FROM observations
		WHERE id = ?
	`, id).Scan(
		&observation.ID,
		&observation.Title,
		&observation.Content,
		&observation.ObservationType,
		&observation.Layer,
		&observation.Confidence,
		&observation.Importance,
		&observation.ActivationLevel,
		&observation.ConsolidationStrength,
		&observation.Kind,
		&tags,
		&observation.Namespace,
		&source,
		&topicKey,
		&observation.Retention,
		&sourceSurface,
		&sourceSessionID,
		&sourceTool,
		&createdAt,
		&updatedAt,
		&deletedAt,
	)
	if err != nil {
		return Observation{}, fmt.Errorf("get observation: %w", err)
	}

	observation.Tags = ParseTags(tags.String)
	observation.Source = source.String
	observation.TopicKey = topicKey.String
	observation.SourceSurface = sourceSurface.String
	observation.SourceSessionID = sourceSessionID.String
	observation.SourceTool = sourceTool.String
	parsedCreatedAt, err := parseSQLiteTime(createdAt)
	if err != nil {
		return Observation{}, fmt.Errorf("parse observation created_at: %w", err)
	}
	parsedUpdatedAt, err := parseSQLiteTime(updatedAt)
	if err != nil {
		return Observation{}, fmt.Errorf("parse observation updated_at: %w", err)
	}
	observation.CreatedAt = parsedCreatedAt
	observation.UpdatedAt = parsedUpdatedAt
	if deletedAt.Valid {
		parsedDeletedAt, err := parseSQLiteTime(deletedAt.String)
		if err != nil {
			return Observation{}, fmt.Errorf("parse observation deleted_at: %w", err)
		}
		observation.DeletedAt = &parsedDeletedAt
	}

	links, err := s.fileLinks.ListByObservation(ctx, querier, observation.ID)
	if err != nil {
		return Observation{}, err
	}
	observation.Files = make([]string, 0, len(links))
	for _, link := range links {
		if link.ValidUntil == nil {
			observation.Files = append(observation.Files, link.FilePath)
		}
	}

	return observation, nil
}

func (s *Store) findActiveByTopicKey(ctx context.Context, tx *sql.Tx, namespace string, topicKey string) (string, error) {
	if strings.TrimSpace(topicKey) == "" {
		return "", nil
	}

	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM observations
		WHERE namespace = ? AND topic_key = ? AND deleted_at IS NULL
		LIMIT 1
	`, namespace, topicKey).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find observation by topic_key: %w", err)
	}
	return id, nil
}

func nullableString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

type ulidGenerator struct{}

func newULIDGenerator() *ulidGenerator {
	return &ulidGenerator{}
}

// NewULIDGenerator returns an IDGenerator that produces ULIDs.
func NewULIDGenerator() *ulidGenerator {
	return &ulidGenerator{}
}

func (g *ulidGenerator) New() string {
	return ulid.Make().String()
}

func parseSQLiteTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported sqlite time %q", value)
}
