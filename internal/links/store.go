package links

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"neurox/internal/filelink"
)

type Store struct {
	db          *sql.DB
	idGenerator filelink.IDGenerator
}

func NewStore(db *sql.DB, idGenerator filelink.IDGenerator) *Store {
	return &Store{db: db, idGenerator: idGenerator}
}

// IDGen returns a new unique ID using the store's ID generator.
func (s *Store) IDGen() string {
	return s.idGenerator.New()
}

func (s *Store) Create(ctx context.Context, input CreateLinkInput) (ObservationLink, error) {
	if s == nil || s.db == nil {
		return ObservationLink{}, fmt.Errorf("store is not initialized")
	}

	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return ObservationLink{}, err
	}

	id := s.idGenerator.New()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
		VALUES(?, ?, ?, ?, ?, ?)
	`, id, input.SourceID, input.TargetID, input.RelationType, input.Confidence, input.CreatedBy)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ObservationLink{}, fmt.Errorf("link already exists between %s and %s with type %s", input.SourceID, input.TargetID, input.RelationType)
		}
		return ObservationLink{}, fmt.Errorf("insert observation link: %w", err)
	}

	return s.Get(ctx, id)
}

func (s *Store) CreateTx(ctx context.Context, tx *sql.Tx, input CreateLinkInput) (ObservationLink, error) {
	if tx == nil {
		return ObservationLink{}, fmt.Errorf("transaction is required")
	}

	input.ApplyDefaults()
	if err := input.Validate(); err != nil {
		return ObservationLink{}, err
	}

	id := s.idGenerator.New()
	_, err := tx.ExecContext(ctx, `
		INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
		VALUES(?, ?, ?, ?, ?, ?)
	`, id, input.SourceID, input.TargetID, input.RelationType, input.Confidence, input.CreatedBy)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ObservationLink{}, fmt.Errorf("link already exists between %s and %s with type %s", input.SourceID, input.TargetID, input.RelationType)
		}
		return ObservationLink{}, fmt.Errorf("insert observation link: %w", err)
	}

	return s.getTx(ctx, tx, id)
}

func (s *Store) Get(ctx context.Context, id string) (ObservationLink, error) {
	if s == nil || s.db == nil {
		return ObservationLink{}, fmt.Errorf("store is not initialized")
	}
	return s.getTx(ctx, s.db, id)
}

func (s *Store) GetBySource(ctx context.Context, sourceID string, relationType ...RelationType) ([]ObservationLink, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	return s.getByColumn(ctx, "source_id", sourceID, relationType...)
}

func (s *Store) GetByTarget(ctx context.Context, targetID string, relationType ...RelationType) ([]ObservationLink, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	return s.getByColumn(ctx, "target_id", targetID, relationType...)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store is not initialized")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM observation_links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete observation link: %w", err)
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

// Traverse walks the observation graph starting from originID, following links
// in both directions up to the given depth (max MaxTraverseDepth).
func (s *Store) Traverse(ctx context.Context, originID string, depth int) ([]TraversalResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store is not initialized")
	}
	if strings.TrimSpace(originID) == "" {
		return nil, fmt.Errorf("origin observation id is required")
	}
	if depth <= 0 {
		depth = 1
	}
	if depth > MaxTraverseDepth {
		depth = MaxTraverseDepth
	}

	visited := map[string]struct{}{originID: {}}
	var results []TraversalResult
	queue := []traversalNode{{id: originID, depth: 0, path: nil}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= depth {
			continue
		}

		neighbors, err := s.getNeighbors(ctx, current.id)
		if err != nil {
			return nil, fmt.Errorf("traverse at depth %d: %w", current.depth, err)
		}

		for _, neighbor := range neighbors {
			if _, seen := visited[neighbor.observationID]; seen {
				continue
			}
			visited[neighbor.observationID] = struct{}{}

			newPath := make([]PathEntry, len(current.path)+1)
			copy(newPath, current.path)
			newPath[len(current.path)] = neighbor.entry

			nextDepth := current.depth + 1
			results = append(results, TraversalResult{
				ObservationID: neighbor.observationID,
				Depth:         nextDepth,
				Path:          newPath,
			})

			if nextDepth < depth {
				queue = append(queue, traversalNode{
					id:    neighbor.observationID,
					depth: nextDepth,
					path:  newPath,
				})
			}
		}
	}

	return results, nil
}

type traversalNode struct {
	id    string
	depth int
	path  []PathEntry
}

type neighborResult struct {
	observationID string
	entry         PathEntry
}

func (s *Store) getNeighbors(ctx context.Context, observationID string) ([]neighborResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_id, target_id, relation_type
		FROM observation_links
		WHERE source_id = ? OR target_id = ?
	`, observationID, observationID)
	if err != nil {
		return nil, fmt.Errorf("query neighbors: %w", err)
	}
	defer rows.Close()

	var neighbors []neighborResult
	for rows.Next() {
		var linkID, sourceID, targetID string
		var relType RelationType
		if err := rows.Scan(&linkID, &sourceID, &targetID, &relType); err != nil {
			return nil, fmt.Errorf("scan neighbor: %w", err)
		}

		otherID := targetID
		if targetID == observationID {
			otherID = sourceID
		}

		neighbors = append(neighbors, neighborResult{
			observationID: otherID,
			entry: PathEntry{
				LinkID:       linkID,
				RelationType: relType,
				FromID:       sourceID,
				ToID:         targetID,
			},
		})
	}
	return neighbors, rows.Err()
}

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) getTx(ctx context.Context, querier rowQueryer, id string) (ObservationLink, error) {
	var link ObservationLink
	var createdAt string

	err := querier.QueryRowContext(ctx, `
		SELECT id, source_id, target_id, relation_type, confidence, created_by, created_at
		FROM observation_links
		WHERE id = ?
	`, id).Scan(
		&link.ID,
		&link.SourceID,
		&link.TargetID,
		&link.RelationType,
		&link.Confidence,
		&link.CreatedBy,
		&createdAt,
	)
	if err != nil {
		return ObservationLink{}, fmt.Errorf("get observation link: %w", err)
	}

	parsed, err := parseSQLiteTime(createdAt)
	if err != nil {
		return ObservationLink{}, fmt.Errorf("parse link created_at: %w", err)
	}
	link.CreatedAt = parsed
	return link, nil
}

func (s *Store) getByColumn(ctx context.Context, column string, value string, relationType ...RelationType) ([]ObservationLink, error) {
	query := fmt.Sprintf(`
		SELECT id, source_id, target_id, relation_type, confidence, created_by, created_at
		FROM observation_links
		WHERE %s = ?`, column)
	args := []any{value}

	if len(relationType) > 0 {
		placeholders := make([]string, len(relationType))
		for i, rt := range relationType {
			placeholders[i] = "?"
			args = append(args, string(rt))
		}
		query += fmt.Sprintf(" AND relation_type IN (%s)", strings.Join(placeholders, ","))
	}

	query += " ORDER BY created_at ASC, id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list observation links by %s: %w", column, err)
	}
	defer rows.Close()

	var results []ObservationLink
	for rows.Next() {
		var link ObservationLink
		var createdAt string
		if err := rows.Scan(&link.ID, &link.SourceID, &link.TargetID, &link.RelationType, &link.Confidence, &link.CreatedBy, &createdAt); err != nil {
			return nil, fmt.Errorf("scan observation link: %w", err)
		}
		parsed, err := parseSQLiteTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse link created_at: %w", err)
		}
		link.CreatedAt = parsed
		results = append(results, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation links: %w", err)
	}

	return results, nil
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
