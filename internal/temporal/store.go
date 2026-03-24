package temporal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/filelink"
)

// Store manages temporal mention persistence in SQLite.
type Store struct {
	db  *sql.DB
	idg filelink.IDGenerator
}

// NewStore creates a new temporal mention store.
func NewStore(db *sql.DB, idGen filelink.IDGenerator) *Store {
	return &Store{db: db, idg: idGen}
}

// Save inserts a temporal mention linked to an observation.
func (s *Store) Save(ctx context.Context, m Mention) (Mention, error) {
	if m.ObservationID == "" {
		return Mention{}, fmt.Errorf("observation_id is required")
	}
	if m.RawText == "" {
		return Mention{}, fmt.Errorf("raw_text is required")
	}
	if err := m.Kind.Validate(); err != nil {
		return Mention{}, err
	}

	m.ID = s.idg.New()
	if m.Confidence <= 0 {
		m.Confidence = 0.8
	}
	if m.AnchorTime.IsZero() {
		m.AnchorTime = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO temporal_mentions(id, observation_id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.ObservationID, m.RawText, m.Kind, nullableTime(m.NormalizedStart), nullableTime(m.NormalizedEnd), m.AnchorTime.UTC().Format(time.RFC3339), m.Confidence)
	if err != nil {
		return Mention{}, fmt.Errorf("insert temporal mention: %w", err)
	}

	return s.Get(ctx, m.ID)
}

// SaveAll inserts multiple temporal mentions for an observation.
func (s *Store) SaveAll(ctx context.Context, observationID string, results []ParseResult, anchor time.Time) ([]Mention, error) {
	if len(results) == 0 {
		return nil, nil
	}

	var mentions []Mention
	for _, r := range results {
		m := Mention{
			ObservationID:   observationID,
			RawText:         r.RawText,
			Kind:            r.Kind,
			NormalizedStart: r.NormalizedStart,
			NormalizedEnd:   r.NormalizedEnd,
			AnchorTime:      anchor,
			Confidence:      r.Confidence,
		}
		saved, err := s.Save(ctx, m)
		if err != nil {
			return mentions, fmt.Errorf("save temporal mention %q: %w", r.RawText, err)
		}
		mentions = append(mentions, saved)
	}
	return mentions, nil
}

// Get returns a temporal mention by ID.
func (s *Store) Get(ctx context.Context, id string) (Mention, error) {
	var m Mention
	var normalizedStart, normalizedEnd sql.NullString
	var anchorTime, createdAt string

	err := s.db.QueryRowContext(ctx, `
		SELECT id, observation_id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence, created_at
		FROM temporal_mentions WHERE id = ?
	`, id).Scan(&m.ID, &m.ObservationID, &m.RawText, &m.Kind, &normalizedStart, &normalizedEnd, &anchorTime, &m.Confidence, &createdAt)
	if err != nil {
		return Mention{}, fmt.Errorf("get temporal mention: %w", err)
	}

	if normalizedStart.Valid {
		if parsed, err := parseSQLiteTime(normalizedStart.String); err == nil {
			m.NormalizedStart = &parsed
		}
	}
	if normalizedEnd.Valid {
		if parsed, err := parseSQLiteTime(normalizedEnd.String); err == nil {
			m.NormalizedEnd = &parsed
		}
	}
	if parsed, err := parseSQLiteTime(anchorTime); err == nil {
		m.AnchorTime = parsed
	}
	if parsed, err := parseSQLiteTime(createdAt); err == nil {
		m.CreatedAt = parsed
	}

	return m, nil
}

// ByObservation returns all temporal mentions linked to an observation.
func (s *Store) ByObservation(ctx context.Context, observationID string) ([]Mention, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, observation_id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence, created_at
		FROM temporal_mentions WHERE observation_id = ?
		ORDER BY created_at ASC
	`, observationID)
	if err != nil {
		return nil, fmt.Errorf("list temporal mentions: %w", err)
	}
	defer rows.Close()

	return scanMentions(rows)
}

// LoadByObservations returns temporal mentions grouped by observation ID.
// This is a standalone function that takes a *sql.DB directly, so callers
// don't need a fully initialized Store.
func LoadByObservations(ctx context.Context, database *sql.DB, observationIDs []string) (map[string][]Mention, error) {
	if len(observationIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(observationIDs))
	args := make([]any, len(observationIDs))
	for i, id := range observationIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := database.QueryContext(ctx, `
		SELECT id, observation_id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence, created_at
		FROM temporal_mentions
		WHERE observation_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY created_at ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("load temporal mentions batch: %w", err)
	}
	defer rows.Close()

	mentions, err := scanMentions(rows)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]Mention)
	for _, m := range mentions {
		result[m.ObservationID] = append(result[m.ObservationID], m)
	}
	return result, nil
}

func scanMentions(rows *sql.Rows) ([]Mention, error) {
	var mentions []Mention
	for rows.Next() {
		var m Mention
		var normalizedStart, normalizedEnd sql.NullString
		var anchorTime, createdAt string

		err := rows.Scan(&m.ID, &m.ObservationID, &m.RawText, &m.Kind, &normalizedStart, &normalizedEnd, &anchorTime, &m.Confidence, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan temporal mention: %w", err)
		}

		if normalizedStart.Valid {
			if parsed, err := parseSQLiteTime(normalizedStart.String); err == nil {
				m.NormalizedStart = &parsed
			}
		}
		if normalizedEnd.Valid {
			if parsed, err := parseSQLiteTime(normalizedEnd.String); err == nil {
				m.NormalizedEnd = &parsed
			}
		}
		if parsed, err := parseSQLiteTime(anchorTime); err == nil {
			m.AnchorTime = parsed
		}
		if parsed, err := parseSQLiteTime(createdAt); err == nil {
			m.CreatedAt = parsed
		}

		mentions = append(mentions, m)
	}
	return mentions, rows.Err()
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func parseSQLiteTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported sqlite time %q", value)
}

// Validate checks that a MentionKind is valid.
func (k MentionKind) Validate() error {
	switch k {
	case KindAbsolute, KindRelative, KindCurrentState, KindDuration, KindRecurring:
		return nil
	default:
		return fmt.Errorf("invalid mention_kind %q", k)
	}
}
