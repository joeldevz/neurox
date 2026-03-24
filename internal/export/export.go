package export

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExportMarkdown writes one .md file per observation to outputDir.
// Each file has YAML frontmatter + content body + WikiLinks for observation_links.
// Compatible with Obsidian vault structure.
func ExportMarkdown(ctx context.Context, db *sql.DB, namespace, outputDir string) (int, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return 0, fmt.Errorf("create output dir: %w", err)
	}

	// Query observations
	query := `
        SELECT id, title, content, observation_type, kind, layer, importance, confidence,
               tags, namespace, staleness, retention,
               created_at, updated_at, valid_from, valid_until
        FROM observations
        WHERE deleted_at IS NULL`
	args := []any{}
	if namespace != "" {
		query += " AND namespace = ?"
		args = append(args, namespace)
	}
	query += " ORDER BY layer DESC, importance DESC, created_at ASC"

	// Load links and titles before opening the observation rows cursor,
	// so all three queries can share a single SQLite connection when
	// MaxOpenConns=1 (which is required for in-memory / WAL-mode SQLite).
	links, err := loadLinks(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("load links: %w", err)
	}
	titles, err := loadTitles(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("load titles: %w", err)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var (
			id, title, content, obsType, kind, staleness, retention string
			layer                                                   int
			importance, confidence                                  float64
			tags, ns                                                sql.NullString
			createdAt, updatedAt                                    string
			validFrom, validUntil                                   sql.NullString
		)
		if err := rows.Scan(&id, &title, &content, &obsType, &kind, &layer, &importance, &confidence,
			&tags, &ns, &staleness, &retention, &createdAt, &updatedAt, &validFrom, &validUntil); err != nil {
			continue
		}

		md := renderMarkdown(id, title, content, obsType, kind, layer, importance, confidence,
			tags.String, ns.String, staleness, retention, createdAt, validFrom.String, validUntil.String,
			links[id], titles)

		filename := sanitizeFilename(title) + ".md"
		if err := os.WriteFile(filepath.Join(outputDir, filename), []byte(md), 0o644); err != nil {
			return count, fmt.Errorf("write %s: %w", filename, err)
		}
		count++
	}
	return count, rows.Err()
}

func renderMarkdown(id, title, content, obsType, kind string, layer int, importance, confidence float64,
	tags, ns, staleness, retention, createdAt, validFrom, validUntil string,
	obsLinks []link, titles map[string]string) string {

	layerNames := []string{"buffer", "working", "core"}
	layerIdx := layer
	if layerIdx < 0 {
		layerIdx = 0
	}
	if layerIdx >= len(layerNames) {
		layerIdx = len(layerNames) - 1
	}
	layerName := layerNames[layerIdx]

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("id: %s\n", id))
	sb.WriteString(fmt.Sprintf("type: %s\n", obsType))
	sb.WriteString(fmt.Sprintf("kind: %s\n", kind))
	sb.WriteString(fmt.Sprintf("layer: %s\n", layerName))
	sb.WriteString(fmt.Sprintf("importance: %.2f\n", importance))
	sb.WriteString(fmt.Sprintf("confidence: %.2f\n", confidence))
	if tags != "" {
		sb.WriteString(fmt.Sprintf("tags: [%s]\n", tags))
	}
	if ns != "" {
		sb.WriteString(fmt.Sprintf("namespace: %s\n", ns))
	}
	sb.WriteString(fmt.Sprintf("staleness: %s\n", staleness))
	sb.WriteString(fmt.Sprintf("retention: %s\n", retention))
	sb.WriteString(fmt.Sprintf("created_at: %s\n", createdAt))
	if validFrom != "" {
		sb.WriteString(fmt.Sprintf("valid_from: %s\n", validFrom))
	}
	if validUntil != "" {
		sb.WriteString(fmt.Sprintf("valid_until: %s\n", validUntil))
	}
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	sb.WriteString(content)
	sb.WriteString("\n")

	// WikiLinks for observation_links
	if len(obsLinks) > 0 {
		sb.WriteString("\n## Links\n\n")
		for _, l := range obsLinks {
			targetTitle := titles[l.targetID]
			if targetTitle == "" {
				targetTitle = l.targetID
			}
			sb.WriteString(fmt.Sprintf("- %s [[%s]]\n", l.relationType, targetTitle))
		}
	}

	return sb.String()
}

type link struct {
	targetID     string
	relationType string
}

func loadLinks(ctx context.Context, db *sql.DB) (map[string][]link, error) {
	rows, err := db.QueryContext(ctx, "SELECT source_id, target_id, relation_type FROM observation_links")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]link{}
	for rows.Next() {
		var src, tgt, rel string
		if err := rows.Scan(&src, &tgt, &rel); err != nil {
			continue
		}
		out[src] = append(out[src], link{targetID: tgt, relationType: rel})
	}
	return out, rows.Err()
}

func loadTitles(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, title FROM observations WHERE deleted_at IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			continue
		}
		out[id] = title
	}
	return out, rows.Err()
}

func sanitizeFilename(title string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", "\"", "", "<", "", ">", "", "|", "",
	)
	s := replacer.Replace(title)
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = s[:80]
	}
	if s == "" {
		s = fmt.Sprintf("observation-%d", time.Now().UnixNano())
	}
	return s
}
