package graph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Node represents an observation in the graph.
type Node struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ObservationType string   `json:"observation_type"`
	Layer           int      `json:"layer"`
	Importance      float64  `json:"importance"`
	Confidence      float64  `json:"confidence"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags,omitempty"`
	Namespace       string   `json:"namespace"`
	Staleness       string   `json:"staleness"`
	CreatedAt       string   `json:"created_at"`
}

// Edge represents a link between two observations.
type Edge struct {
	ID           string  `json:"id"`
	SourceID     string  `json:"source"`
	TargetID     string  `json:"target"`
	RelationType string  `json:"relation_type"`
	Confidence   float64 `json:"confidence"`
	CreatedBy    string  `json:"created_by"`
}

// Data holds the complete graph for rendering.
type Data struct {
	Nodes  []Node  `json:"nodes"`
	Edges  []Edge  `json:"edges"`
	Stats  Stats   `json:"stats"`
	Filter Options `json:"filter"`
}

// Stats provides summary information about the graph.
type Stats struct {
	TotalObservations int `json:"total_observations"`
	TotalLinks        int `json:"total_links"`
	ShownNodes        int `json:"shown_nodes"`
	ShownEdges        int `json:"shown_edges"`
}

// Options controls what data is included in the graph.
type Options struct {
	Namespace       string  `json:"namespace,omitempty"`
	ObservationType string  `json:"observation_type,omitempty"`
	Tags            string  `json:"tags,omitempty"`
	MinImportance   float64 `json:"min_importance,omitempty"`
	Limit           int     `json:"limit,omitempty"`
	LinkedOnly      bool    `json:"linked_only,omitempty"`
}

// Query extracts graph data from the database with the given filters.
func Query(ctx context.Context, db *sql.DB, opts Options) (*Data, error) {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}

	// Get total counts for stats.
	var totalObs, totalLinks int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&totalObs)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links").Scan(&totalLinks)

	// Build node query with filters.
	nodes, err := queryNodes(ctx, db, opts)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}

	// Build a set of node IDs for edge filtering.
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.ID] = struct{}{}
	}

	// Get edges between the selected nodes.
	edges, err := queryEdges(ctx, db, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}

	return &Data{
		Nodes: nodes,
		Edges: edges,
		Stats: Stats{
			TotalObservations: totalObs,
			TotalLinks:        totalLinks,
			ShownNodes:        len(nodes),
			ShownEdges:        len(edges),
		},
		Filter: opts,
	}, nil
}

func queryNodes(ctx context.Context, db *sql.DB, opts Options) ([]Node, error) {
	var where []string
	var args []any

	where = append(where, "o.deleted_at IS NULL")

	if opts.Namespace != "" {
		where = append(where, "o.namespace = ?")
		args = append(args, opts.Namespace)
	}
	if opts.ObservationType != "" {
		where = append(where, "o.observation_type = ?")
		args = append(args, opts.ObservationType)
	}
	if opts.Tags != "" {
		for _, tag := range splitCSV(opts.Tags) {
			where = append(where, "o.tags LIKE ?")
			args = append(args, "%"+tag+"%")
		}
	}
	if opts.MinImportance > 0 {
		where = append(where, "o.importance >= ?")
		args = append(args, opts.MinImportance)
	}

	linkedJoin := ""
	if opts.LinkedOnly {
		linkedJoin = `AND o.id IN (
			SELECT source_id FROM observation_links
			UNION
			SELECT target_id FROM observation_links
		)`
	}

	query := fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.observation_type, o.layer, o.importance, o.confidence,
		       o.kind, COALESCE(o.tags, ''), o.namespace, COALESCE(o.staleness, 'fresh'),
		       COALESCE(o.created_at, '')
		FROM observations o
		WHERE %s %s
		ORDER BY o.importance DESC, o.layer DESC, o.created_at DESC
		LIMIT ?
	`, strings.Join(where, " AND "), linkedJoin)
	args = append(args, opts.Limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var n Node
		var tags string
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.ObservationType, &n.Layer, &n.Importance,
			&n.Confidence, &n.Kind, &tags, &n.Namespace, &n.Staleness, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		if tags != "" {
			n.Tags = splitCSV(tags)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func queryEdges(ctx context.Context, db *sql.DB, nodeIDs map[string]struct{}) ([]Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	// Build IN clause for both source and target.
	ids := make([]string, 0, len(nodeIDs))
	for id := range nodeIDs {
		ids = append(ids, id)
	}

	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	inClause := strings.Join(placeholders, ",")

	// Duplicate args for the second IN clause.
	for _, id := range ids {
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT id, source_id, target_id, relation_type, confidence, created_by
		FROM observation_links
		WHERE source_id IN (%s) AND target_id IN (%s)
	`, inClause, inClause)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query links: %w", err)
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.RelationType, &e.Confidence, &e.CreatedBy); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
