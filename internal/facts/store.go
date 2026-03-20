package facts

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"neurox/internal/filelink"
)

// Store manages fact persistence in SQLite.
type Store struct {
	db  *sql.DB
	idg filelink.IDGenerator
}

// NewStore creates a new fact store.
func NewStore(db *sql.DB, idGen filelink.IDGenerator) *Store {
	return &Store{db: db, idg: idGen}
}

// Save inserts a new fact. If a fact with the same subject+predicate exists in the
// namespace, the old one is superseded (valid_until set, superseded_by linked).
func (s *Store) Save(ctx context.Context, f Fact) (Fact, error) {
	if strings.TrimSpace(f.Subject) == "" || strings.TrimSpace(f.Predicate) == "" || strings.TrimSpace(f.Object) == "" {
		return Fact{}, fmt.Errorf("subject, predicate, and object are required")
	}
	if f.Namespace == "" {
		f.Namespace = "default"
	}

	f.ID = s.idg.New()

	// Check for existing active fact with same subject+predicate in namespace
	var existingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM facts
		WHERE subject = ? AND predicate = ? AND namespace = ? AND valid_until IS NULL
		LIMIT 1
	`, f.Subject, f.Predicate, f.Namespace).Scan(&existingID)

	// Insert new fact first (before updating old, to satisfy FK constraint)
	_, insertErr := s.db.ExecContext(ctx, `
		INSERT INTO facts(id, subject, predicate, object, observation_id, namespace)
		VALUES(?, ?, ?, ?, ?, ?)
	`, f.ID, f.Subject, f.Predicate, f.Object, nullableString(f.ObservationID), f.Namespace)
	if insertErr != nil {
		return Fact{}, fmt.Errorf("insert fact: %w", insertErr)
	}

	// Supersede old fact now that new one exists (FK constraint satisfied)
	if err == nil && existingID != "" {
		_, superErr := s.db.ExecContext(ctx, `
			UPDATE facts SET valid_until = datetime('now'), superseded_by = ?
			WHERE id = ?
		`, f.ID, existingID)
		if superErr != nil {
			return Fact{}, fmt.Errorf("supersede existing fact: %w", superErr)
		}
	}

	return s.Get(ctx, f.ID)
}

// SaveWithValidFrom inserts a fact with an explicit valid_from timestamp.
// This is used when temporal data is available (e.g. "migration happened_on 2026-03-06").
func (s *Store) SaveWithValidFrom(ctx context.Context, f Fact, validFrom time.Time) (Fact, error) {
	if strings.TrimSpace(f.Subject) == "" || strings.TrimSpace(f.Predicate) == "" || strings.TrimSpace(f.Object) == "" {
		return Fact{}, fmt.Errorf("subject, predicate, and object are required")
	}
	if f.Namespace == "" {
		f.Namespace = "default"
	}

	f.ID = s.idg.New()

	var existingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM facts
		WHERE subject = ? AND predicate = ? AND namespace = ? AND valid_until IS NULL
		LIMIT 1
	`, f.Subject, f.Predicate, f.Namespace).Scan(&existingID)

	_, insertErr := s.db.ExecContext(ctx, `
		INSERT INTO facts(id, subject, predicate, object, observation_id, namespace, valid_from)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, f.ID, f.Subject, f.Predicate, f.Object, nullableString(f.ObservationID), f.Namespace, validFrom.UTC().Format(time.RFC3339))
	if insertErr != nil {
		return Fact{}, fmt.Errorf("insert fact: %w", insertErr)
	}

	if err == nil && existingID != "" {
		_, superErr := s.db.ExecContext(ctx, `
			UPDATE facts SET valid_until = datetime('now'), superseded_by = ?
			WHERE id = ?
		`, f.ID, existingID)
		if superErr != nil {
			return Fact{}, fmt.Errorf("supersede existing fact: %w", superErr)
		}
	}

	return s.Get(ctx, f.ID)
}

// SearchHistory returns superseded (historical) facts for a subject+predicate in a namespace.
func (s *Store) SearchHistory(ctx context.Context, subject, predicate, namespace string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, subject, predicate, object, observation_id, namespace, valid_from, valid_until, superseded_by, created_at
		FROM facts
		WHERE namespace = ? AND subject = ? AND predicate = ?
		ORDER BY valid_from DESC
		LIMIT ?
	`, namespace, subject, predicate, limit)
	if err != nil {
		return nil, fmt.Errorf("search fact history: %w", err)
	}
	defer rows.Close()

	return scanFacts(rows)
}

// Get returns a fact by ID.
func (s *Store) Get(ctx context.Context, id string) (Fact, error) {
	return scanFact(s.db.QueryRowContext(ctx, `
		SELECT id, subject, predicate, object, observation_id, namespace, valid_from, valid_until, superseded_by, created_at
		FROM facts WHERE id = ?
	`, id))
}

// Search finds active facts matching a subject or object query within a namespace.
func (s *Store) Search(ctx context.Context, query string, namespace string, limit int) ([]Fact, error) {
	if limit <= 0 {
		limit = 20
	}
	q := "%" + query + "%"

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, subject, predicate, object, observation_id, namespace, valid_from, valid_until, superseded_by, created_at
		FROM facts
		WHERE valid_until IS NULL
		  AND namespace = ?
		  AND (subject LIKE ? OR object LIKE ? OR predicate LIKE ?)
		ORDER BY created_at DESC
		LIMIT ?
	`, namespace, q, q, q, limit)
	if err != nil {
		return nil, fmt.Errorf("search facts: %w", err)
	}
	defer rows.Close()

	return scanFacts(rows)
}

// ByObservation returns all active facts linked to an observation.
func (s *Store) ByObservation(ctx context.Context, observationID string) ([]Fact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, subject, predicate, object, observation_id, namespace, valid_from, valid_until, superseded_by, created_at
		FROM facts
		WHERE observation_id = ? AND valid_until IS NULL
		ORDER BY created_at ASC
	`, observationID)
	if err != nil {
		return nil, fmt.Errorf("facts by observation: %w", err)
	}
	defer rows.Close()

	return scanFacts(rows)
}

// Traverse walks the fact graph starting from a subject or object, following
// related facts up to the given depth (max 2).
func (s *Store) Traverse(ctx context.Context, entity string, namespace string, maxDepth int) ([]TraversalResult, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 2 {
		maxDepth = 2
	}

	visited := make(map[string]bool)
	var results []TraversalResult

	type node struct {
		entity string
		depth  int
	}
	queue := []node{{entity: entity, depth: 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		rows, err := s.db.QueryContext(ctx, `
			SELECT id, subject, predicate, object, observation_id, namespace, valid_from, valid_until, superseded_by, created_at
			FROM facts
			WHERE valid_until IS NULL AND namespace = ? AND (subject = ? OR object = ?)
			ORDER BY created_at DESC
		`, namespace, current.entity, current.entity)
		if err != nil {
			return nil, fmt.Errorf("traverse facts: %w", err)
		}

		facts, err := scanFacts(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}

		for _, f := range facts {
			if visited[f.ID] {
				continue
			}
			visited[f.ID] = true

			nextDepth := current.depth + 1
			results = append(results, TraversalResult{Fact: f, Depth: nextDepth})

			// Follow the other end of the triple
			nextEntity := f.Object
			if f.Object == current.entity {
				nextEntity = f.Subject
			}
			if nextDepth < maxDepth {
				queue = append(queue, node{entity: nextEntity, depth: nextDepth})
			}
		}
	}

	return results, nil
}

// Count returns the number of active facts in a namespace.
func (s *Store) Count(ctx context.Context, namespace string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM facts WHERE valid_until IS NULL AND namespace = ?
	`, namespace).Scan(&count)
	return count, err
}

func scanFact(row *sql.Row) (Fact, error) {
	var f Fact
	var obsID, validFrom, validUntil, supersededBy sql.NullString
	var createdAt string

	err := row.Scan(&f.ID, &f.Subject, &f.Predicate, &f.Object, &obsID, &f.Namespace, &validFrom, &validUntil, &supersededBy, &createdAt)
	if err != nil {
		return Fact{}, fmt.Errorf("scan fact: %w", err)
	}

	f.ObservationID = obsID.String
	f.SupersededBy = supersededBy.String

	if validFrom.Valid {
		parsed, err := parseSQLiteTime(validFrom.String)
		if err == nil {
			f.ValidFrom = parsed
		}
	}
	if validUntil.Valid {
		parsed, err := parseSQLiteTime(validUntil.String)
		if err == nil {
			f.ValidUntil = &parsed
		}
	}
	parsed, err := parseSQLiteTime(createdAt)
	if err == nil {
		f.CreatedAt = parsed
	}

	return f, nil
}

func scanFacts(rows *sql.Rows) ([]Fact, error) {
	var facts []Fact
	for rows.Next() {
		var f Fact
		var obsID, validFrom, validUntil, supersededBy sql.NullString
		var createdAt string

		err := rows.Scan(&f.ID, &f.Subject, &f.Predicate, &f.Object, &obsID, &f.Namespace, &validFrom, &validUntil, &supersededBy, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan fact row: %w", err)
		}

		f.ObservationID = obsID.String
		f.SupersededBy = supersededBy.String

		if validFrom.Valid {
			parsed, err := parseSQLiteTime(validFrom.String)
			if err == nil {
				f.ValidFrom = parsed
			}
		}
		if validUntil.Valid {
			parsed, err := parseSQLiteTime(validUntil.String)
			if err == nil {
				f.ValidUntil = &parsed
			}
		}
		parsed, err := parseSQLiteTime(createdAt)
		if err == nil {
			f.CreatedAt = parsed
		}

		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
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
