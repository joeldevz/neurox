package filelink

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type IDGenerator interface {
	New() string
}

type FileObservation struct {
	ID            string
	ObservationID string
	FilePath      string
	CreatedAt     time.Time
	ValidUntil    *time.Time
}

type Store struct {
	idGenerator IDGenerator
}

func NewStore(idGenerator IDGenerator) *Store {
	return &Store{idGenerator: idGenerator}
}

func (s *Store) ReplaceLinks(ctx context.Context, tx *sql.Tx, observationID string, files []string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	if strings.TrimSpace(observationID) == "" {
		return fmt.Errorf("observation id is required")
	}

	normalized := normalizeFiles(files)
	if _, err := tx.ExecContext(ctx, `
		UPDATE file_observations
		SET valid_until = datetime('now')
		WHERE observation_id = ? AND valid_until IS NULL
	`, observationID); err != nil {
		return fmt.Errorf("expire existing file links: %w", err)
	}

	if len(normalized) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO file_observations(id, observation_id, file_path)
		VALUES(?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare file link insert: %w", err)
	}
	defer stmt.Close()

	for _, file := range normalized {
		if _, err := stmt.ExecContext(ctx, s.idGenerator.New(), observationID, file); err != nil {
			return fmt.Errorf("insert file link %q: %w", file, err)
		}
	}

	return nil
}

func (s *Store) ListByObservation(ctx context.Context, querier queryer, observationID string) ([]FileObservation, error) {
	rows, err := querier.QueryContext(ctx, `
		SELECT id, observation_id, file_path, created_at, valid_until
		FROM file_observations
		WHERE observation_id = ?
		ORDER BY created_at ASC, id ASC
	`, observationID)
	if err != nil {
		return nil, fmt.Errorf("list file links: %w", err)
	}
	defer rows.Close()

	links := make([]FileObservation, 0)
	for rows.Next() {
		var link FileObservation
		var createdAt string
		var validUntil sql.NullString
		if err := rows.Scan(&link.ID, &link.ObservationID, &link.FilePath, &createdAt, &validUntil); err != nil {
			return nil, fmt.Errorf("scan file link: %w", err)
		}
		parsedCreatedAt, err := parseSQLiteTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse file link created_at: %w", err)
		}
		link.CreatedAt = parsedCreatedAt
		if validUntil.Valid {
			parsedValidUntil, err := parseSQLiteTime(validUntil.String)
			if err != nil {
				return nil, fmt.Errorf("parse file link valid_until: %w", err)
			}
			link.ValidUntil = &parsedValidUntil
		}
		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file links: %w", err)
	}

	return links, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func normalizeFiles(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		trimmed := strings.TrimSpace(file)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
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
