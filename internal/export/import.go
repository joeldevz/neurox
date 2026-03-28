package export

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"
)

// ImportMarkdown reads .md files from sourceDir and upserts observations into the DB.
// Returns the count of observations imported.
func ImportMarkdown(ctx context.Context, db *sql.DB, sourceDir string) (int, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return 0, fmt.Errorf("read source dir: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			continue
		}
		obs, err := parseMarkdown(string(data))
		if err != nil || obs == nil {
			continue
		}
		if err := upsertObservation(ctx, db, obs); err != nil {
			return count, fmt.Errorf("upsert %s: %w", entry.Name(), err)
		}
		count++
	}
	return count, nil
}

type parsedObs struct {
	id         string
	title      string
	content    string
	obsType    string
	kind       string
	layer      int
	importance float64
	confidence float64
	tags       string
	namespace  string
	staleness  string
	retention  string
	createdAt  string
	validFrom  string
	validUntil string
}

func parseMarkdown(raw string) (*parsedObs, error) {
	if !strings.HasPrefix(raw, "---\n") {
		return nil, fmt.Errorf("no frontmatter")
	}
	end := strings.Index(raw[4:], "\n---\n")
	if end < 0 {
		return nil, fmt.Errorf("unclosed frontmatter")
	}
	frontmatter := raw[4 : end+4]
	body := raw[end+9:] // skip "\n---\n"

	obs := &parsedObs{importance: 0.5, confidence: 0.7, staleness: "fresh", retention: "durable"}
	for _, line := range strings.Split(frontmatter, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "id":
			obs.id = v
		case "type":
			obs.obsType = v
		case "kind":
			obs.kind = v
		case "layer":
			switch v {
			case "working":
				obs.layer = 1
			case "core":
				obs.layer = 2
			default:
				obs.layer = 0
			}
		case "importance":
			fmt.Sscanf(v, "%f", &obs.importance)
		case "confidence":
			fmt.Sscanf(v, "%f", &obs.confidence)
		case "tags":
			obs.tags = strings.Trim(v, "[]")
		case "namespace":
			obs.namespace = v
		case "staleness":
			obs.staleness = v
		case "retention":
			obs.retention = v
		case "created_at":
			obs.createdAt = v
		case "valid_from":
			obs.validFrom = v
		case "valid_until":
			obs.validUntil = v
		}
	}

	// Extract title from body "# Title"
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			obs.title = strings.TrimPrefix(line, "# ")
			break
		}
	}

	// Content = body minus the title heading and links section
	lines := strings.Split(body, "\n")
	var contentLines []string
	inLinks := false
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			continue // skip title heading
		}
		if line == "## Links" {
			inLinks = true
			continue
		}
		if inLinks && strings.HasPrefix(line, "## ") {
			inLinks = false
		}
		if inLinks {
			continue
		}
		contentLines = append(contentLines, line)
	}
	obs.content = strings.TrimSpace(strings.Join(contentLines, "\n"))

	if obs.id == "" {
		obs.id = ulid.Make().String()
	}
	if obs.title == "" {
		return nil, fmt.Errorf("no title found")
	}
	return obs, nil
}

func upsertObservation(ctx context.Context, db *sql.DB, obs *parsedObs) error {
	// Use INSERT ... ON CONFLICT to preserve scoring metadata for existing observations.
	// New observations: insert with all available fields from the Markdown parse.
	// Existing observations: update content-related fields but preserve accumulated
	// scoring metadata (activation_level, consolidation_strength, access_count,
	// decay_rate, has_embedding, source_surface, source_session_id, source_tool,
	// layer, staleness, consolidation_status).
	// For importance: only update if imported value is higher than existing.
	_, err := db.ExecContext(ctx, `
        INSERT INTO observations
            (id, title, content, observation_type, kind, layer, importance, confidence,
             tags, namespace, staleness, created_at, valid_from, valid_until,
             updated_at, deleted_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(?, datetime('now')), COALESCE(?, datetime('now')), ?, datetime('now'), NULL)
        ON CONFLICT(id) DO UPDATE SET
            title = excluded.title,
            content = excluded.content,
            observation_type = excluded.observation_type,
            kind = excluded.kind,
            tags = excluded.tags,
            namespace = excluded.namespace,
            confidence = excluded.confidence,
            importance = MAX(observations.importance, excluded.importance),
            updated_at = datetime('now')`,
		obs.id, obs.title, obs.content, obs.obsType, obs.kind, obs.layer,
		obs.importance, obs.confidence, obs.tags, obs.namespace,
		obs.staleness,
		nullStr(obs.createdAt), nullStr(obs.validFrom), nullStr(obs.validUntil),
	)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
