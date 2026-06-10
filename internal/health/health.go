package health

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/telemetry"
)

// Dimension represents one scored aspect of brain health.
type Dimension struct {
	Name           string  `json:"name"`
	Category       string  `json:"category"`
	Score          float64 `json:"score"`
	Max            float64 `json:"max"`
	Status         string  `json:"status"`
	Detail         string  `json:"detail"`
	Recommendation string  `json:"recommendation,omitempty"`
}

// Report is the full health-check output.
type Report struct {
	Score           int                    `json:"score"`
	Grade           string                 `json:"grade"`
	StaticScore     int                    `json:"static_score"`
	DynamicScore    int                    `json:"dynamic_score"`
	Dimensions      []Dimension            `json:"dimensions"`
	ToolUsage       *telemetry.UsageReport `json:"tool_usage,omitempty"`
	Summary         string                 `json:"summary"`
	TopActions      []string               `json:"top_actions"`
	UpdateAvailable string                 `json:"update_available,omitempty"`
}

// Deps holds dependencies for a health check.
type Deps struct {
	DB              *sql.DB
	Embedder        embed.Provider
	LLMProvider     llm.Provider
	EmbedderName    string
	LLMProviderName string
	Tracker         *telemetry.Tracker
	UsageDays       int
}

// Check computes the brain power score.
func Check(ctx context.Context, deps Deps) Report {
	var dims []Dimension

	// --- STATIC DIMENSIONS (60 pts) ---
	dims = append(dims, checkEmbeddingsCoverage(ctx, deps.DB))
	dims = append(dims, checkLLMProvider(deps))
	dims = append(dims, checkEmbedProvider(deps))
	dims = append(dims, checkTagsCoverage(ctx, deps.DB))
	dims = append(dims, checkFileLinkCoverage(ctx, deps.DB))
	dims = append(dims, checkKindDiversity(ctx, deps.DB))
	dims = append(dims, checkTypeDiversity(ctx, deps.DB))
	dims = append(dims, checkLinkRichness(ctx, deps.DB))
	dims = append(dims, checkConsolidationHealth(ctx, deps.DB))

	// --- DYNAMIC DIMENSIONS (40 pts) ---
	var usageReport *telemetry.UsageReport
	usageDays := deps.UsageDays
	if usageDays <= 0 {
		usageDays = 7
	}
	if deps.Tracker != nil {
		ur, err := deps.Tracker.GetUsageReport(ctx, usageDays)
		if err == nil && ur.TotalCalls > 0 {
			usageReport = &ur
			dims = append(dims, checkSaveQuality(ur))
			dims = append(dims, checkRecallDepth(ur))
			dims = append(dims, checkSessionDiscipline(ctx, deps.DB))
			dims = append(dims, checkToolBreadth(ur))
			dims = append(dims, checkReflectUsage(ur))
			dims = append(dims, checkGitHookActivity(ur))
		} else {
			dims = append(dims, Dimension{
				Name: "Usage tracking", Category: "dynamic",
				Score: 0, Max: 40, Status: "no_data",
				Detail:         "No tool call data yet. Telemetry starts collecting after this update.",
				Recommendation: "Use Neurox normally — data will accumulate automatically.",
			})
		}
	} else {
		dims = append(dims, Dimension{
			Name: "Usage tracking", Category: "dynamic",
			Score: 0, Max: 40, Status: "no_data",
			Detail:         "Telemetry tracker not configured.",
			Recommendation: "Use Neurox normally — data will accumulate automatically.",
		})
	}

	// Compute totals
	var staticTotal, dynamicTotal float64
	for _, d := range dims {
		if d.Category == "static" {
			staticTotal += d.Score
		} else {
			dynamicTotal += d.Score
		}
	}
	total := int(staticTotal + dynamicTotal)

	// Top actions: sorted by gap descending
	type gap struct {
		dim Dimension
		gap float64
	}
	var gaps []gap
	for _, d := range dims {
		if d.Score < d.Max && d.Status != "no_data" && d.Recommendation != "" {
			gaps = append(gaps, gap{dim: d, gap: d.Max - d.Score})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].gap > gaps[j].gap })

	var topActions []string
	for i, g := range gaps {
		if i >= 5 {
			break
		}
		topActions = append(topActions, fmt.Sprintf("%s (+%.0f pts)", g.dim.Recommendation, g.gap))
	}

	return Report{
		Score:        total,
		Grade:        gradeFromScore(total),
		StaticScore:  int(staticTotal),
		DynamicScore: int(dynamicTotal),
		Dimensions:   dims,
		ToolUsage:    usageReport,
		Summary:      buildSummary(total, dims),
		TopActions:   topActions,
	}
}

func gradeFromScore(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}

func buildSummary(score int, dims []Dimension) string {
	var disabled int
	for _, d := range dims {
		if d.Status == "disabled" {
			disabled++
		}
	}
	switch {
	case score >= 90:
		return "Excellent brain health! All systems operating at full capacity."
	case score >= 75:
		return "Good brain health with room for improvement in a few areas."
	case score >= 60:
		return fmt.Sprintf("Moderate brain health. %d dimensions need attention.", disabled)
	case score >= 40:
		return fmt.Sprintf("Below average brain health. %d critical dimensions are disabled or degraded.", disabled)
	default:
		return fmt.Sprintf("Poor brain health. %d dimensions are disabled. Significant configuration needed.", disabled)
	}
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// --- STATIC CHECKS ---

func checkEmbeddingsCoverage(ctx context.Context, db *sql.DB) Dimension {
	var total, withEmbed int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL").Scan(&withEmbed)

	dim := Dimension{Name: "Embeddings coverage", Category: "static", Max: 15}
	if total == 0 {
		dim.Score = 15
		dim.Status = "healthy"
		dim.Detail = "No observations yet"
		return dim
	}
	ratio := float64(withEmbed) / float64(total)
	dim.Score = ratio * 15
	dim.Detail = fmt.Sprintf("%d/%d observations have embeddings (%.0f%%)", withEmbed, total, ratio*100)
	switch {
	case ratio >= 0.9:
		dim.Status = "healthy"
	case ratio >= 0.5:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Ensure Ollama is running with an embedding model. Run: ollama pull qwen3-embedding:0.6b"
	return dim
}

func checkLLMProvider(deps Deps) Dimension {
	dim := Dimension{Name: "LLM provider", Category: "static", Max: 10}
	if deps.LLMProvider != nil && llm.IsAvailable(deps.LLMProvider) {
		dim.Score = 10
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("Provider: %s", deps.LLMProvider.Name())
	} else if deps.LLMProviderName != "" && deps.LLMProviderName != "disabled" {
		dim.Score = 10
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("Provider: %s", deps.LLMProviderName)
	} else {
		dim.Score = 0
		dim.Status = "disabled"
		dim.Detail = "No LLM provider configured"
		dim.Recommendation = "Configure Ollama or a remote LLM for reflection and contradiction detection."
	}
	return dim
}

func checkEmbedProvider(deps Deps) Dimension {
	dim := Dimension{Name: "Embedding provider", Category: "static", Max: 10}
	if deps.Embedder != nil && embed.IsAvailable(deps.Embedder) {
		dim.Score = 10
		dim.Status = "healthy"
		providerName := deps.Embedder.Name()
		actualDims := deps.Embedder.Dimensions()
		
		// Read stored dimensions from db_settings for comparison
		var storedDims string
		if deps.DB != nil {
			_ = deps.DB.QueryRow(`
				SELECT value FROM db_settings WHERE key = 'embed_dims'
			`).Scan(&storedDims)
		}
		
		// Check for dimension mismatch
		if actualDims > 0 && storedDims != "" && storedDims != fmt.Sprintf("%d", actualDims) {
			dim.Score = 8
			dim.Status = "warning"
			dim.Detail = fmt.Sprintf("Embedding provider active (%s), but dim mismatch: stored=%s actual=%d", providerName, storedDims, actualDims)
			dim.Recommendation = "Run a recall to trigger dim reconciliation, or re-configure embed.dimensions in config.yaml"
		} else {
			dim.Detail = fmt.Sprintf("Provider: %s, dimensions: %d", providerName, actualDims)
		}
	} else if deps.EmbedderName != "" && deps.EmbedderName != "disabled" {
		dim.Score = 10
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("Provider: %s", deps.EmbedderName)
	} else {
		dim.Score = 0
		dim.Status = "disabled"
		dim.Detail = "No embedding provider configured"
		dim.Recommendation = "Ensure Ollama is running with an embedding model for semantic search."
	}
	return dim
}

func checkTagsCoverage(ctx context.Context, db *sql.DB) Dimension {
	var total, withTags int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND tags IS NOT NULL AND tags != ''").Scan(&withTags)

	dim := Dimension{Name: "Tags coverage", Category: "static", Max: 7}
	if total == 0 {
		dim.Score = 7
		dim.Status = "healthy"
		dim.Detail = "No observations yet"
		return dim
	}
	ratio := float64(withTags) / float64(total)
	dim.Score = ratio * 7
	dim.Detail = fmt.Sprintf("%d/%d observations have tags (%.0f%%)", withTags, total, ratio*100)
	switch {
	case ratio >= 0.7:
		dim.Status = "healthy"
	case ratio >= 0.3:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Always include tags when saving observations for better search."
	return dim
}

func checkFileLinkCoverage(ctx context.Context, db *sql.DB) Dimension {
	var total, withFiles int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT observation_id) FROM file_observations").Scan(&withFiles)

	dim := Dimension{Name: "File links coverage", Category: "static", Max: 7}
	if total == 0 {
		dim.Score = 7
		dim.Status = "healthy"
		dim.Detail = "No observations yet"
		return dim
	}
	ratio := float64(withFiles) / float64(total)
	dim.Score = ratio * 7
	dim.Detail = fmt.Sprintf("%d/%d observations linked to files (%.0f%%)", withFiles, total, ratio*100)
	switch {
	case ratio >= 0.5:
		dim.Status = "healthy"
	case ratio >= 0.2:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Include file paths when saving observations for git-linked staleness tracking."
	return dim
}

func checkKindDiversity(ctx context.Context, db *sql.DB) Dimension {
	dim := Dimension{Name: "Kind diversity", Category: "static", Max: 3}
	rows, err := db.QueryContext(ctx, "SELECT kind, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY kind")
	if err != nil {
		dim.Status = "disabled"
		dim.Detail = "Query failed"
		return dim
	}
	defer rows.Close()

	kinds := make(map[string]int)
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err == nil {
			kinds[kind] = count
		}
	}

	if len(kinds) == 0 {
		dim.Score = 3
		dim.Status = "healthy"
		dim.Detail = "No observations yet"
		return dim
	}

	dim.Score = float64(len(kinds))
	if dim.Score > 3 {
		dim.Score = 3
	}
	dim.Detail = fmt.Sprintf("%d/3 kinds present: semantic=%d, episodic=%d, procedural=%d",
		len(kinds), kinds["semantic"], kinds["episodic"], kinds["procedural"])
	if len(kinds) >= 3 {
		dim.Status = "healthy"
	} else {
		dim.Status = "degraded"
		dim.Recommendation = "Use all three memory kinds: semantic (facts), episodic (events), procedural (how-to)."
	}
	return dim
}

func checkTypeDiversity(ctx context.Context, db *sql.DB) Dimension {
	var distinctTypes int
	db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT observation_type) FROM observations WHERE deleted_at IS NULL").Scan(&distinctTypes)

	dim := Dimension{Name: "Type diversity", Category: "static", Max: 3}
	var total int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	if total == 0 {
		dim.Score = 3
		dim.Status = "healthy"
		dim.Detail = "No observations yet"
		return dim
	}
	ratio := float64(distinctTypes) / 8.0
	dim.Score = ratio * 3
	dim.Detail = fmt.Sprintf("%d/8 observation types used", distinctTypes)
	switch {
	case distinctTypes >= 6:
		dim.Status = "healthy"
	case distinctTypes >= 3:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Use varied observation types: decision, bugfix, discovery, pattern, gotcha, config, preference, question."
	return dim
}

func checkLinkRichness(ctx context.Context, db *sql.DB) Dimension {
	var distinctRelTypes int
	db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT relation_type) FROM observation_links").Scan(&distinctRelTypes)

	dim := Dimension{Name: "Link richness", Category: "static", Max: 2}
	ratio := float64(distinctRelTypes) / 6.0
	dim.Score = ratio * 2
	dim.Detail = fmt.Sprintf("%d/6 link relation types used", distinctRelTypes)
	switch {
	case distinctRelTypes >= 4:
		dim.Status = "healthy"
	case distinctRelTypes >= 2:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Use invalidate and reflect to build a richer knowledge graph."
	return dim
}

func checkConsolidationHealth(ctx context.Context, db *sql.DB) Dimension {
	dim := Dimension{Name: "Consolidation health", Category: "static", Max: 3}
	var status string
	var completedAt sql.NullString
	err := db.QueryRowContext(ctx, "SELECT status, completed_at FROM consolidation_runs ORDER BY started_at DESC LIMIT 1").Scan(&status, &completedAt)
	if err != nil {
		dim.Score = 0
		dim.Status = "disabled"
		dim.Detail = "No consolidation runs found"
		dim.Recommendation = "Run consolidation to promote, decay, and deduplicate observations."
		return dim
	}
	if status == "completed" {
		dim.Score = 3
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("Last run: %s (%s)", status, completedAt.String)
	} else {
		dim.Score = 1
		dim.Status = "degraded"
		dim.Detail = fmt.Sprintf("Last run: %s (%s)", status, completedAt.String)
		dim.Recommendation = "Check consolidation pipeline — last run was not successful."
	}
	return dim
}

// --- DYNAMIC CHECKS ---

func checkSaveQuality(ur telemetry.UsageReport) Dimension {
	dim := Dimension{Name: "Save quality", Category: "dynamic", Max: 10}
	stats, ok := ur.ParamUsageByTool["save"]
	if !ok || stats.Total == 0 {
		dim.Status = "no_data"
		dim.Detail = "No save calls recorded yet"
		return dim
	}
	keyParams := []string{"tags", "files", "kind", "topic_key"}
	var avgUsage float64
	for _, p := range keyParams {
		avgUsage += float64(stats.ByParam[p]) / float64(stats.Total)
	}
	avgUsage /= float64(len(keyParams))
	dim.Score = avgUsage * 10
	dim.Detail = fmt.Sprintf("tags: %.0f%%, files: %.0f%%, kind: %.0f%%, topic_key: %.0f%%",
		pct(stats.ByParam["tags"], stats.Total),
		pct(stats.ByParam["files"], stats.Total),
		pct(stats.ByParam["kind"], stats.Total),
		pct(stats.ByParam["topic_key"], stats.Total))
	switch {
	case avgUsage >= 0.6:
		dim.Status = "healthy"
	case avgUsage >= 0.3:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Always include tags, files, and kind when saving observations."
	return dim
}

func checkRecallDepth(ur telemetry.UsageReport) Dimension {
	dim := Dimension{Name: "Recall depth", Category: "dynamic", Max: 10}
	stats, ok := ur.ParamUsageByTool["recall"]
	if !ok || stats.Total == 0 {
		dim.Status = "no_data"
		dim.Detail = "No recall calls recorded yet"
		return dim
	}
	filterParams := []string{"kind", "observation_type", "namespace", "files"}
	var withFilter int
	// Count calls that had at least 1 filter param
	// We approximate: if any filter param was used at least once, it contributed
	maxUsage := 0
	for _, p := range filterParams {
		if stats.ByParam[p] > maxUsage {
			maxUsage = stats.ByParam[p]
		}
	}
	withFilter = maxUsage
	ratio := float64(withFilter) / float64(stats.Total)
	dim.Score = ratio * 10
	dim.Detail = fmt.Sprintf("%.0f%% of recall calls use filters", ratio*100)
	switch {
	case ratio >= 0.5:
		dim.Status = "healthy"
	case ratio >= 0.2:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Use filters (kind, type, namespace, files) in recall for more precise results."
	return dim
}

func checkSessionDiscipline(ctx context.Context, db *sql.DB) Dimension {
	dim := Dimension{Name: "Session discipline", Category: "dynamic", Max: 5}
	var completed, abandoned int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE status = 'completed'").Scan(&completed)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE status = 'abandoned'").Scan(&abandoned)

	total := completed + abandoned
	if total == 0 {
		dim.Status = "no_data"
		dim.Detail = "No sessions recorded"
		return dim
	}
	ratio := float64(completed) / float64(total)
	dim.Score = ratio * 5
	dim.Detail = fmt.Sprintf("%d completed, %d abandoned (%.0f%% completion)", completed, abandoned, ratio*100)
	switch {
	case ratio >= 0.7:
		dim.Status = "healthy"
	case ratio >= 0.4:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Always call session_end with a summary when done working."
	return dim
}

func checkToolBreadth(ur telemetry.UsageReport) Dimension {
	dim := Dimension{Name: "Tool breadth", Category: "dynamic", Max: 5}
	used := len(ur.CallsByTool)
	total := len(telemetry.AllTools)
	ratio := float64(used) / float64(total)
	dim.Score = ratio * 5
	dim.Detail = fmt.Sprintf("%d/%d tools used", used, total)
	switch {
	case ratio >= 0.7:
		dim.Status = "healthy"
	case ratio >= 0.4:
		dim.Status = "degraded"
	default:
		dim.Status = "disabled"
	}
	dim.Recommendation = "Explore more tools: invalidate, reflect, git_hook for richer memory."
	return dim
}

func checkReflectUsage(ur telemetry.UsageReport) Dimension {
	dim := Dimension{Name: "Reflect usage", Category: "dynamic", Max: 5}
	reflectCalls := ur.CallsByTool["reflect"] + ur.CallsByTool["consolidate"]
	if reflectCalls > 0 {
		dim.Score = 5
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("reflect: %d, consolidate: %d calls", ur.CallsByTool["reflect"], ur.CallsByTool["consolidate"])
	} else {
		dim.Score = 0
		dim.Status = "disabled"
		dim.Detail = "Neither reflect nor consolidate have been called"
		dim.Recommendation = "Use reflect to synthesize insights from related observations."
	}
	return dim
}

func checkGitHookActivity(ur telemetry.UsageReport) Dimension {
	dim := Dimension{Name: "Git hook activity", Category: "dynamic", Max: 5}
	if ur.CallsByTool["git_hook"] > 0 {
		dim.Score = 5
		dim.Status = "healthy"
		dim.Detail = fmt.Sprintf("%d git hook calls", ur.CallsByTool["git_hook"])
	} else {
		dim.Score = 0
		dim.Status = "disabled"
		dim.Detail = "No git hook activity detected"
		dim.Recommendation = "Install the post-commit hook with 'neurox install-hook' to track file changes."
	}
	return dim
}
