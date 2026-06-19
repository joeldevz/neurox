package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/joeldevz/neurox/internal/consolidate"
	curatepkg "github.com/joeldevz/neurox/internal/curate"
	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/health"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/savepipeline"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/telemetry"
	"github.com/joeldevz/neurox/internal/updatecheck"
)

type Deps struct {
	ObservationStore *observation.Store
	SaveQueue        *observation.SaveQueue
	RecallEngine     *recall.Engine
	LinkStore        *links.Store
	FactStore        *facts.Store
	FactExtractor    *facts.Extractor
	ReflectEngine    *reflectpkg.Engine
	SessionManager   *session.Manager
	ProactiveEngine  *proactive.Engine
	Pipeline         *consolidate.Pipeline
	CurateEngine     *curatepkg.Engine
	DB               *sql.DB
	LLMProvider      llm.Provider
	LLMGate          *llm.Gate
	EmbedQueue       *embed.Queue
	Embedder         embed.Provider
	Tracker          *telemetry.Tracker
	Version          string
	ConfigDir        string
}

func (d *Deps) handleSave(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "save",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "title", "content", "observation_type", "kind", "confidence", "topic_key", "tags", "files", "namespace"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	obs := observation.Observation{
		Title:           req.GetString("title", ""),
		Content:         req.GetString("content", ""),
		ObservationType: observation.ObservationType(req.GetString("observation_type", "")),
		Kind:            observation.Kind(req.GetString("kind", "")),
		TopicKey:        req.GetString("topic_key", ""),
		Namespace:       req.GetString("namespace", ""),
	}

	if args := req.GetArguments(); args != nil {
		if v, ok := args["confidence"]; ok {
			if f, ok := v.(float64); ok {
				obs.Confidence = f
			}
		}
	}

	if tags := req.GetString("tags", ""); tags != "" {
		obs.Tags = splitCSV(tags)
	}
	if files := req.GetString("files", ""); files != "" {
		obs.Files = splitCSV(files)
	}

	// Explicit retention overrides auto-classification inside the pipeline.
	if ret := req.GetString("retention", ""); ret != "" {
		obs.Retention = observation.Retention(ret)
	}

	// Delegate to the shared save pipeline (defaults + retention + session
	// attachment + SaveQueue fast path or sync fallback + post-save hooks).
	pr, pErr := savepipeline.Run(ctx, savepipeline.Deps{
		Store:         d.ObservationStore,
		SaveQueue:     d.SaveQueue,
		LLMGate:       d.LLMGate,
		FactExtractor: d.FactExtractor,
		EmbedQueue:    d.EmbedQueue,
		DB:            d.DB,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "mcp",
		Tool:    "save",
	})
	if pErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save failed: %v", pErr)), nil
	}

	if pr.Rejected {
		return toolResultJSON(map[string]string{
			"message": pr.Message,
			"title":   pr.Title,
		})
	}

	return toolResultJSON(saveResponse{
		ID:        pr.ID,
		Title:     pr.Title,
		Layer:     pr.Layer,
		Namespace: pr.Namespace,
		TopicKey:  pr.TopicKey,
		Message:   pr.Message,
	})
}

func (d *Deps) handleRecall(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "recall",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "query", "observation_type", "kind", "namespace", "files", "include_stale", "limit", "debug"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	query := req.GetString("query", "")

	opts := recall.SearchOptions{
		Query:           query,
		ObservationType: observation.ObservationType(req.GetString("observation_type", "")),
		Kind:            observation.Kind(req.GetString("kind", "")),
		Namespace:       req.GetString("namespace", ""),
	}

	if files := req.GetString("files", ""); files != "" {
		opts.Files = splitCSV(files)
	}

	if args := req.GetArguments(); args != nil {
		if v, ok := args["include_stale"]; ok {
			if b, ok := v.(bool); ok {
				opts.IncludeStale = b
			}
		}
		if v, ok := args["limit"]; ok {
			if f, ok := v.(float64); ok {
				opts.Limit = int(f)
			}
		}
		if v, ok := args["debug"]; ok {
			if b, ok := v.(bool); ok {
				opts.Debug = b
			}
		}
	}

	results, err := d.RecallEngine.Search(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("recall failed: %v", err)), nil
	}

	items := make([]recallResponseItem, 0, len(results))
	for _, r := range results {
		item := recallResponseItem{
			ID:              r.ID,
			Title:           r.Title,
			Content:         r.Content,
			Score:           r.Score,
			Layer:           r.Layer,
			ObservationType: string(r.ObservationType),
			Kind:            string(r.Kind),
			Confidence:      r.Confidence,
			Tags:            r.Tags,
			Staleness:       r.Staleness,
			Retention:       r.Retention,
			LinkedFiles:     r.LinkedFiles,
			SourceSurface:   r.SourceSurface,
			SourceSessionID: r.SourceSessionID,
			SourceTool:      r.SourceTool,
			CreatedAt:       r.CreatedAt,
		}
		if r.Breakdown != nil {
			item.ScoreBreakdown = r.Breakdown
		}
		items = append(items, item)
	}

	resp := recallResponse{
		Query:   query,
		Count:   len(items),
		Results: items,
	}

	// Include detected temporal intent for debugging/advanced clients
	intent := recall.DetectTemporalIntent(query, time.Now().UTC())
	if intent.Kind != recall.IntentNone {
		resp.TemporalIntent = string(intent.Kind)
	}

	return toolResultJSON(resp)
}

func (d *Deps) handleContext(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "context",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "namespace", "files", "limit"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	namespace := req.GetString("namespace", "default")
	limit := 20
	if args := req.GetArguments(); args != nil {
		if v, ok := args["limit"]; ok {
			if f, ok := v.(float64); ok && f > 0 {
				limit = int(f)
			}
		}
	}

	var files []string
	if f := req.GetString("files", ""); f != "" {
		files = splitCSV(f)
	}

	if d.ProactiveEngine != nil {
		result, err := d.ProactiveEngine.GetContext(ctx, namespace, files, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("context failed: %v", err)), nil
		}
		return toolResultJSON(result)
	}

	// Fallback: simple query if proactive engine not available
	query := "SELECT id, title, content, observation_type, layer, confidence, importance, kind, tags, staleness, source_surface, source_session_id, source_tool, created_at FROM observations WHERE deleted_at IS NULL AND namespace = ? AND valid_until IS NULL ORDER BY layer DESC, importance DESC, created_at DESC LIMIT ?"
	args := []any{namespace, limit}

	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("context query failed: %v", err)), nil
	}
	defer rows.Close()

	var items []contextResponseItem
	for rows.Next() {
		var item contextResponseItem
		var tags, sourceSurface, sourceSessionID, sourceTool sql.NullString
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Title, &item.Content, &item.ObservationType, &item.Layer, &item.Confidence, &item.Importance, &item.Kind, &tags, &item.Staleness, &sourceSurface, &sourceSessionID, &sourceTool, &createdAt); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("scan context row: %v", err)), nil
		}
		if tags.Valid {
			item.Tags = observation.ParseTags(tags.String)
		}
		item.SourceSurface = sourceSurface.String
		item.SourceSessionID = sourceSessionID.String
		item.SourceTool = sourceTool.String
		item.CreatedAt = createdAt
		items = append(items, item)
	}

	return toolResultJSON(contextResponse{
		Namespace: namespace,
		Count:     len(items),
		Items:     items,
	})
}

func (d *Deps) handleUpdate(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "update",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "id", "title", "content", "observation_type", "kind", "confidence", "tags", "files"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	id := req.GetString("id", "")
	title := req.GetString("title", "")
	content := req.GetString("content", "")

	obs := observation.Observation{
		ID:              id,
		Title:           title,
		Content:         content,
		ObservationType: observation.ObservationType(req.GetString("observation_type", "")),
		Kind:            observation.Kind(req.GetString("kind", "")),
	}

	if args := req.GetArguments(); args != nil {
		if v, ok := args["confidence"]; ok {
			if f, ok := v.(float64); ok {
				obs.Confidence = f
			}
		}
	}

	if tags := req.GetString("tags", ""); tags != "" {
		obs.Tags = splitCSV(tags)
	}
	if files := req.GetString("files", ""); files != "" {
		obs.Files = splitCSV(files)
	}

	// Preserve retention: use explicit value, or fetch existing from DB.
	if ret := req.GetString("retention", ""); ret != "" {
		obs.Retention = observation.Retention(ret)
	} else {
		existing, getErr := d.ObservationStore.Get(ctx, id)
		if getErr == nil {
			obs.Retention = existing.Retention
		}
	}

	updated, err := d.ObservationStore.Update(ctx, obs)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("update failed: %v", err)), nil
	}

	// Post-update hooks: re-extract facts + re-enqueue embedding.
	// Temporal re-extraction is already handled inside Store.Update().
	savepipeline.AfterUpdate(ctx, savepipeline.Deps{
		FactExtractor: d.FactExtractor,
		EmbedQueue:    d.EmbedQueue,
	}, updated)

	return toolResultJSON(saveResponse{
		ID:        updated.ID,
		Title:     updated.Title,
		Layer:     updated.Layer,
		Namespace: updated.Namespace,
		Message:   "observation updated",
	})
}

func (d *Deps) handleForget(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "forget",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req, "id"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	id := req.GetString("id", "")
	if strings.TrimSpace(id) == "" {
		return mcp.NewToolResultError("id is required"), nil
	}

	if err := d.ObservationStore.SoftDelete(ctx, id); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("forget failed: %v", err)), nil
	}

	return toolResultJSON(map[string]string{
		"id":      id,
		"message": "observation forgotten (soft-deleted)",
	})
}

func (d *Deps) handleInvalidate(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "invalidate",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req, "observation_id", "reason", "replacement_title", "replacement_content"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	input := observation.InvalidateInput{
		ObservationID:      req.GetString("observation_id", ""),
		Reason:             req.GetString("reason", ""),
		ReplacementTitle:   req.GetString("replacement_title", ""),
		ReplacementContent: req.GetString("replacement_content", ""),
		SourceSurface:      "mcp",
		SourceTool:         "invalidate",
	}

	invResult, invErr := d.ObservationStore.Invalidate(ctx, d.LinkStore, input)
	if invErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalidate failed: %v", invErr)), nil
	}

	resp := map[string]string{
		"invalidated_id": invResult.InvalidatedID,
		"message":        "observation marked as stale",
	}
	if invResult.ReplacementID != "" {
		resp["replacement_id"] = invResult.ReplacementID
		resp["link_id"] = invResult.LinkID
		resp["message"] = "observation invalidated and replaced"
	}

	return toolResultJSON(resp)
}

func (d *Deps) handleStatus(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "status",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	var total, buffer, working, core int
	var staleCount, expiredCount int

	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 0").Scan(&buffer)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 1").Scan(&working)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 2").Scan(&core)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'stale'").Scan(&staleCount)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'expired'").Scan(&expiredCount)

	var linkCount int
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links").Scan(&linkCount)

	var sessionCount int
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE status = 'active'").Scan(&sessionCount)

	var factCount int
	if d.FactStore != nil {
		factCount, _ = d.FactStore.Count(ctx, "")
		// Count across all namespaces
		d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM facts WHERE valid_until IS NULL").Scan(&factCount)
	}

	var temporalMentionCount int
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM temporal_mentions").Scan(&temporalMentionCount)

	var embeddingsTotal, embeddingsPending int
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL").Scan(&embeddingsTotal)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NULL").Scan(&embeddingsPending)

	embedProvider := "disabled"
	if d.Embedder != nil {
		embedProvider = d.Embedder.Name()
	}

	llmName := "disabled"
	gateMode := "off"
	if d.LLMProvider != nil {
		llmName = d.LLMProvider.Name()
	}
	if d.LLMGate != nil {
		gateMode = string(d.LLMGate.Mode())
	}

	resp := statusResponse{
		Total:             total,
		Buffer:            buffer,
		Working:           working,
		Core:              core,
		Stale:             staleCount,
		Expired:           expiredCount,
		Links:             linkCount,
		Facts:             factCount,
		TemporalMentions:  temporalMentionCount,
		ActiveSessions:    sessionCount,
		LLMProvider:       llmName,
		GateMode:          gateMode,
		EmbeddingsTotal:   embeddingsTotal,
		EmbeddingsPending: embeddingsPending,
		EmbedProvider:     embedProvider,
	}
	if updateAvailable := d.checkUpdateAvailable(ctx); updateAvailable != "" {
		resp.UpdateAvailable = updateAvailable
	}

	return toolResultJSON(resp)
}

func (d *Deps) handleSessionStart(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "session_start",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "title", "directory", "branch", "namespace"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	title := req.GetString("title", "")
	directory := req.GetString("directory", "")
	branch := req.GetString("branch", "")
	namespace := req.GetString("namespace", "default")

	if d.SessionManager != nil {
		startResult, err := d.SessionManager.Start(ctx, title, directory, branch, namespace)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("start session failed: %v", err)), nil
		}

		// Get proactive context for this session
		var contextResult *proactive.ContextResult
		if d.ProactiveEngine != nil {
			cr, err := d.ProactiveEngine.GetSessionContext(ctx, namespace, title, directory, branch, 15)
			if err == nil {
				contextResult = &cr
			}
		}

		resp := map[string]any{
			"session_id": startResult.SessionID,
			"namespace":  startResult.Namespace,
			"abandoned":  startResult.Abandoned,
			"message":    "session started",
		}
		if contextResult != nil && contextResult.Count > 0 {
			resp["context"] = contextResult.Items
			resp["context_count"] = contextResult.Count
			if len(contextResult.Reflections) > 0 {
				resp["reflections"] = contextResult.Reflections
			}
		}

		return toolResultJSON(resp)
	}

	// Fallback without session manager
	d.DB.ExecContext(ctx, `
		UPDATE sessions SET status = 'abandoned', ended_at = datetime('now')
		WHERE status = 'active' AND namespace = ?
	`, namespace)

	id := d.LinkStore.IDGen()
	var execErr error
	_, execErr = d.DB.ExecContext(ctx, `
		INSERT INTO sessions(id, title, directory, branch, namespace, status)
		VALUES(?, ?, ?, ?, ?, 'active')
	`, id, nullableString(title), nullableString(directory), nullableString(branch), namespace)
	if execErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("start session failed: %v", execErr)), nil
	}

	return toolResultJSON(map[string]string{
		"session_id": id,
		"namespace":  namespace,
		"message":    "session started",
	})
}

func (d *Deps) handleSessionEnd(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "session_end",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req, "session_id", "summary"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	sessionID := req.GetString("session_id", "")
	summary := req.GetString("summary", "")

	if strings.TrimSpace(sessionID) == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}
	if strings.TrimSpace(summary) == "" {
		return mcp.NewToolResultError("summary is required"), nil
	}

	if d.SessionManager != nil {
		endResult, endErr := d.SessionManager.End(ctx, sessionID, summary, "mcp")
		if endErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("end session failed: %v", endErr)), nil
		}

		resp := map[string]any{
			"session_id": endResult.SessionID,
			"message":    "session completed",
		}
		if endResult.ObservationsExtracted >= 0 {
			resp["observations_extracted"] = endResult.ObservationsExtracted
		} else {
			resp["observations_status"] = "extracting in background"
		}
		if endResult.Warning != "" {
			resp["warning"] = endResult.Warning
		}

		return toolResultJSON(resp)
	}

	// Fallback
	sqlResult, sqlErr := d.DB.ExecContext(ctx, `
		UPDATE sessions SET status = 'completed', summary = ?, ended_at = datetime('now')
		WHERE id = ? AND status = 'active'
	`, summary, sessionID)
	if sqlErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("end session failed: %v", sqlErr)), nil
	}
	affected, _ := sqlResult.RowsAffected()
	if affected == 0 {
		return mcp.NewToolResultError("session not found or already ended"), nil
	}

	return toolResultJSON(map[string]string{
		"session_id": sessionID,
		"message":    "session completed",
	})
}

func (d *Deps) handleGitHook(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "git_hook",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req, "changed_files", "commit_sha", "branch"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	changedFilesStr := req.GetString("changed_files", "")
	commitSha := req.GetString("commit_sha", "")

	if strings.TrimSpace(changedFilesStr) == "" {
		return mcp.NewToolResultError("changed_files is required"), nil
	}
	if strings.TrimSpace(commitSha) == "" {
		return mcp.NewToolResultError("commit_sha is required"), nil
	}

	changedFiles := splitCSV(changedFilesStr)
	if len(changedFiles) == 0 {
		return mcp.NewToolResultError("no valid files provided"), nil
	}

	// Find observations linked to changed files and mark them stale
	placeholders := make([]string, len(changedFiles))
	args := make([]any, len(changedFiles))
	for i, f := range changedFiles {
		placeholders[i] = "?"
		args[i] = f
	}

	// Mark file_observations as expired
	updateFileQuery := fmt.Sprintf(`
		UPDATE file_observations
		SET valid_until = datetime('now'), commit_sha_until = ?
		WHERE file_path IN (%s) AND valid_until IS NULL
	`, strings.Join(placeholders, ","))
	fileArgs := append([]any{commitSha}, args...)
	d.DB.ExecContext(ctx, updateFileQuery, fileArgs...)

	// Mark linked observations as stale
	markStaleQuery := fmt.Sprintf(`
		UPDATE observations
		SET staleness = 'stale',
		    confidence = MAX(0.01, confidence * 0.5),
		    updated_at = datetime('now'),
		    modified_epoch = modified_epoch + 1
		WHERE deleted_at IS NULL
		  AND staleness = 'fresh'
		  AND id IN (
		    SELECT DISTINCT observation_id FROM file_observations
		    WHERE file_path IN (%s)
		  )
	`, strings.Join(placeholders, ","))

	sqlRes, sqlErr := d.DB.ExecContext(ctx, markStaleQuery, args...)
	if sqlErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("mark stale failed: %v", sqlErr)), nil
	}
	affected, _ := sqlRes.RowsAffected()

	return toolResultJSON(map[string]any{
		"commit_sha":          commitSha,
		"files_processed":     len(changedFiles),
		"observations_staled": affected,
		"message":             "git hook processed",
	})
}

func (d *Deps) handleReflect(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "reflect",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "namespace"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	namespace := req.GetString("namespace", "default")

	if d.ReflectEngine == nil {
		return mcp.NewToolResultError("reflection engine not configured"), nil
	}

	reflectResult, reflectErr := d.ReflectEngine.ForceReflect(ctx, namespace)
	if reflectErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reflection failed: %v", reflectErr)), nil
	}

	return toolResultJSON(map[string]any{
		"namespace":           namespace,
		"source_observations": reflectResult.SourceCount,
		"reflections_created": reflectResult.ReflectionsCreated,
		"message":             "reflection completed",
	})
}

func (d *Deps) handleConsolidate(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "consolidate",
				Namespace:  "",
				ParamsUsed: nonEmptyParams(req),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	if d.Pipeline == nil {
		return mcp.NewToolResultError("consolidation pipeline not available"), nil
	}

	if err := d.Pipeline.ForceRun(ctx); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("consolidation failed: %v", err)), nil
	}

	// Run a passive WAL checkpoint after consolidation to keep the WAL file
	// small and prevent write contention when multiple instances share the DB.
	walCheckpointed, walErr := db.WALCheckpoint(ctx, d.DB)
	_ = walCheckpointed // logged below if needed
	_ = walErr          // passive checkpoint is best-effort

	var buffer, working, core int
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 0").Scan(&buffer)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 1").Scan(&working)
	d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 2").Scan(&core)

	return toolResultJSON(map[string]any{
		"message": "consolidation completed",
		"buffer":  buffer,
		"working": working,
		"core":    core,
	})
}

func (d *Deps) handleCurate(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "curate",
				Namespace:  req.GetString("namespace", ""),
				ParamsUsed: nonEmptyParams(req, "namespace", "dry_run"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	if d.CurateEngine == nil {
		return mcp.NewToolResultError("curator not configured. Set curator.provider, curator.remote_url, curator.remote_api_key, and curator.remote_model in config.yaml"), nil
	}

	namespace := req.GetString("namespace", "")
	dryRun := false
	if args := req.GetArguments(); args != nil {
		if v, ok := args["dry_run"]; ok {
			if b, ok := v.(bool); ok {
				dryRun = b
			}
		}
	}

	if namespace != "" {
		report, curateErr := d.CurateEngine.CurateNamespace(ctx, namespace, dryRun)
		if curateErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("curate namespace %q: %v", namespace, curateErr)), nil
		}
		return toolResultJSON(report)
	}

	report, curateErr := d.CurateEngine.CurateAll(ctx, dryRun)
	if curateErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("curate all: %v", curateErr)), nil
	}
	return toolResultJSON(report)
}

func (d *Deps) handleHealthCheck(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	days := 7
	if args := req.GetArguments(); args != nil {
		if v, ok := args["days"]; ok {
			if f, ok := v.(float64); ok && f > 0 {
				days = int(f)
			}
		}
	}
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "health_check",
				ParamsUsed: nonEmptyParams(req, "days"),
				Success:    err == nil,
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	hdeps := health.Deps{
		DB:        d.DB,
		Tracker:   d.Tracker,
		UsageDays: days,
	}
	if d.Embedder != nil {
		hdeps.Embedder = d.Embedder
		hdeps.EmbedderName = d.Embedder.Name()
	}
	if d.LLMProvider != nil {
		hdeps.LLMProvider = d.LLMProvider
		hdeps.LLMProviderName = d.LLMProvider.Name()
	}
	report := health.Check(ctx, hdeps)
	if updateAvailable := d.checkUpdateAvailable(ctx); updateAvailable != "" {
		report.UpdateAvailable = updateAvailable
		report.TopActions = append([]string{fmt.Sprintf("Update Neurox to %s for the latest fixes and features.", updateAvailable)}, report.TopActions...)
	}
	return toolResultJSON(report)
}

func (d *Deps) checkUpdateAvailable(ctx context.Context) string {
	if strings.TrimSpace(d.Version) == "" || strings.TrimSpace(d.ConfigDir) == "" {
		return ""
	}

	latestVersion, isNewer, err := updatecheck.Check(ctx, d.Version, d.ConfigDir)
	if err != nil || !isNewer {
		return ""
	}

	return latestVersion
}

func (d *Deps) handleBackup(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
	start := time.Now()
	defer func() {
		if d.Tracker != nil {
			d.Tracker.Record(telemetry.CallRecord{
				ToolName:   "backup",
				ParamsUsed: nonEmptyParams(req, "output"),
				Success:    err == nil && (result == nil || !result.IsError),
				DurationMs: time.Since(start).Milliseconds(),
			})
		}
	}()

	output := req.GetString("output", "")
	if output == "" {
		output = db.DefaultBackupPath(d.dbPath())
	}

	backupResult, backupErr := db.BackupWithResult(ctx, d.DB, output)
	if backupErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("backup failed: %v", backupErr)), nil
	}

	return toolResultJSON(backupResult)
}

// dbPath returns the file path of the database by querying PRAGMA database_list.
func (d *Deps) dbPath() string {
	var seq int
	var name, file string
	if err := d.DB.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		return ""
	}
	return file
}

// helpers

// nonEmptyParams returns the names of request arguments that are present and non-empty.
func nonEmptyParams(req mcp.CallToolRequest, names ...string) []string {
	var used []string
	args := req.GetArguments()
	if args == nil {
		return used
	}
	for _, name := range names {
		if v, ok := args[name]; ok && v != nil {
			switch val := v.(type) {
			case string:
				if val != "" {
					used = append(used, name)
				}
			default:
				used = append(used, name)
			}
		}
	}
	return used
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func nullableString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func toolResultJSON(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// response types

type saveResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Layer     int    `json:"layer"`
	Namespace string `json:"namespace"`
	TopicKey  string `json:"topic_key,omitempty"`
	Message   string `json:"message"`
}

type recallResponse struct {
	Query          string               `json:"query"`
	Count          int                  `json:"count"`
	TemporalIntent string               `json:"temporal_intent,omitempty"`
	Results        []recallResponseItem `json:"results"`
}

type recallResponseItem struct {
	ID              string                 `json:"id"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	Score           float64                `json:"score"`
	Layer           int                    `json:"layer"`
	ObservationType string                 `json:"observation_type"`
	Kind            string                 `json:"kind"`
	Confidence      float64                `json:"confidence"`
	Tags            []string               `json:"tags,omitempty"`
	Staleness       string                 `json:"staleness"`
	Retention       string                 `json:"retention"`
	LinkedFiles     []string               `json:"linked_files,omitempty"`
	SourceSurface   string                 `json:"source_surface,omitempty"`
	SourceSessionID string                 `json:"source_session_id,omitempty"`
	SourceTool      string                 `json:"source_tool,omitempty"`
	CreatedAt       string                 `json:"created_at,omitempty"`
	ScoreBreakdown  *recall.ScoreBreakdown `json:"score_breakdown,omitempty"`
}

type contextResponse struct {
	Namespace string                `json:"namespace"`
	Count     int                   `json:"count"`
	Items     []contextResponseItem `json:"items"`
}

type contextResponseItem struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ObservationType string   `json:"observation_type"`
	Layer           int      `json:"layer"`
	Confidence      float64  `json:"confidence"`
	Importance      float64  `json:"importance"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags,omitempty"`
	Staleness       string   `json:"staleness"`
	SourceSurface   string   `json:"source_surface,omitempty"`
	SourceSessionID string   `json:"source_session_id,omitempty"`
	SourceTool      string   `json:"source_tool,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

type statusResponse struct {
	Total             int    `json:"total"`
	Buffer            int    `json:"buffer"`
	Working           int    `json:"working"`
	Core              int    `json:"core"`
	Stale             int    `json:"stale"`
	Expired           int    `json:"expired"`
	Links             int    `json:"links"`
	Facts             int    `json:"facts"`
	TemporalMentions  int    `json:"temporal_mentions"`
	ActiveSessions    int    `json:"active_sessions"`
	LLMProvider       string `json:"llm_provider"`
	GateMode          string `json:"gate_mode"`
	EmbeddingsTotal   int    `json:"embeddings_total"`
	EmbeddingsPending int    `json:"embeddings_pending"`
	EmbedProvider     string `json:"embedding_provider"`
	UpdateAvailable   string `json:"update_available,omitempty"`
}
