package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"time"

	"neurox/internal/graph"
	"neurox/internal/observation"
	"neurox/internal/recall"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := s.deps.DB

	var total, buffer, working, core, stale, expired, linkCount, sessions, factCount, temporalCount int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 0").Scan(&buffer)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 1").Scan(&working)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 2").Scan(&core)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'stale'").Scan(&stale)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'expired'").Scan(&expired)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links").Scan(&linkCount)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE status = 'active'").Scan(&sessions)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM facts WHERE valid_until IS NULL").Scan(&factCount)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM temporal_mentions").Scan(&temporalCount)

	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "buffer": buffer, "working": working, "core": core,
		"stale": stale, "expired": expired, "links": linkCount, "active_sessions": sessions,
		"facts": factCount, "temporal_mentions": temporalCount,
		"llm_provider": s.deps.LLMProvider, "embedding_provider": s.deps.EmbedProvider,
		"gate_mode": s.deps.GateMode,
	})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title           string   `json:"title"`
		Content         string   `json:"content"`
		ObservationType string   `json:"observation_type"`
		Kind            string   `json:"kind"`
		Confidence      float64  `json:"confidence"`
		TopicKey        string   `json:"topic_key"`
		Tags            []string `json:"tags"`
		Files           []string `json:"files"`
		Namespace       string   `json:"namespace"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	obs := observation.Observation{
		Title:           req.Title,
		Content:         req.Content,
		ObservationType: observation.ObservationType(req.ObservationType),
		Kind:            observation.Kind(req.Kind),
		Confidence:      req.Confidence,
		TopicKey:        req.TopicKey,
		Tags:            req.Tags,
		Files:           req.Files,
		Namespace:       req.Namespace,
	}

	saved, err := s.deps.ObservationStore.Save(r.Context(), obs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, observationToJSON(saved))
}

func (s *Server) handleRecall(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := q.Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	opts := recall.SearchOptions{
		Query:           query,
		ObservationType: observation.ObservationType(q.Get("type")),
		Kind:            observation.Kind(q.Get("kind")),
		Namespace:       q.Get("namespace"),
		Staleness:       q.Get("staleness"),
		IncludeStale:    q.Get("include_stale") == "true",
	}

	if files := q.Get("files"); files != "" {
		opts.Files = splitCSV(files)
	}
	if limit := q.Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil {
			opts.Limit = n
		}
	}

	results, err := s.deps.RecallEngine.Search(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]map[string]any, 0, len(results))
	for _, res := range results {
		items = append(items, map[string]any{
			"id": res.ID, "title": res.Title, "content": res.Content,
			"score": res.Score, "layer": res.Layer,
			"observation_type": res.ObservationType, "kind": res.Kind,
			"confidence": res.Confidence, "tags": res.Tags,
			"staleness": res.Staleness, "linked_files": res.LinkedFiles,
		})
	}

	resp := map[string]any{
		"query": query, "count": len(items), "results": items,
	}
	intent := recall.DetectTemporalIntent(query, time.Now().UTC())
	if intent.Kind != recall.IntentNone {
		resp["temporal_intent"] = string(intent.Kind)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	namespace := q.Get("namespace")
	if namespace == "" {
		namespace = "default"
	}

	limit := 20
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	query := "SELECT id, title, content, observation_type, layer, confidence, importance, kind, staleness, created_at FROM observations WHERE deleted_at IS NULL AND namespace = ?"
	args := []any{namespace}

	if files := q.Get("files"); files != "" {
		fileList := splitCSV(files)
		placeholders := make([]string, len(fileList))
		for i, f := range fileList {
			placeholders[i] = "?"
			args = append(args, f)
		}
		query += fmt.Sprintf(` AND (id IN (SELECT observation_id FROM file_observations WHERE file_path IN (%s) AND valid_until IS NULL) OR layer >= 1)`, strings.Join(placeholders, ","))
	}

	query += " ORDER BY layer DESC, importance DESC, created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.deps.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id, title, content, obsType, kind, staleness, createdAt string
		var layer int
		var confidence, importance float64
		if err := rows.Scan(&id, &title, &content, &obsType, &layer, &confidence, &importance, &kind, &staleness, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, map[string]any{
			"id": id, "title": title, "content": content,
			"observation_type": obsType, "layer": layer,
			"confidence": confidence, "importance": importance,
			"kind": kind, "staleness": staleness, "created_at": createdAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": namespace, "count": len(items), "items": items,
	})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	obs, err := s.deps.ObservationStore.Get(r.Context(), id)
	if err != nil {
		if err.Error() == "get observation: sql: no rows in result set" {
			writeError(w, http.StatusNotFound, "observation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, observationToJSON(obs))
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Title           string   `json:"title"`
		Content         string   `json:"content"`
		ObservationType string   `json:"observation_type"`
		Kind            string   `json:"kind"`
		Confidence      float64  `json:"confidence"`
		Tags            []string `json:"tags"`
		Files           []string `json:"files"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	obs := observation.Observation{
		ID:              id,
		Title:           req.Title,
		Content:         req.Content,
		ObservationType: observation.ObservationType(req.ObservationType),
		Kind:            observation.Kind(req.Kind),
		Confidence:      req.Confidence,
		Tags:            req.Tags,
		Files:           req.Files,
	}

	updated, err := s.deps.ObservationStore.Update(r.Context(), obs)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "observation not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, observationToJSON(updated))
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.ObservationStore.SoftDelete(r.Context(), id); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "observation not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "message": "observation forgotten"})
}

func (s *Server) handleInvalidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Reason             string `json:"reason"`
		ReplacementTitle   string `json:"replacement_title"`
		ReplacementContent string `json:"replacement_content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	result, err := s.deps.ObservationStore.Invalidate(r.Context(), s.deps.LinkStore, observation.InvalidateInput{
		ObservationID:      id,
		Reason:             req.Reason,
		ReplacementTitle:   req.ReplacementTitle,
		ReplacementContent: req.ReplacementContent,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "observation not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := map[string]string{"invalidated_id": result.InvalidatedID, "message": "observation invalidated"}
	if result.ReplacementID != "" {
		resp["replacement_id"] = result.ReplacementID
		resp["link_id"] = result.LinkID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Branch    string `json:"branch"`
		Namespace string `json:"namespace"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	namespace := req.Namespace
	if namespace == "" {
		namespace = "default"
	}

	ctx := r.Context()
	s.deps.DB.ExecContext(ctx, `
		UPDATE sessions SET status = 'abandoned', ended_at = datetime('now')
		WHERE status = 'active' AND namespace = ?
	`, namespace)

	id := s.deps.LinkStore.IDGen()
	_, err := s.deps.DB.ExecContext(ctx, `
		INSERT INTO sessions(id, title, directory, branch, namespace, status)
		VALUES(?, ?, ?, ?, ?, 'active')
	`, id, nullStr(req.Title), nullStr(req.Directory), nullStr(req.Branch), namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"session_id": id, "namespace": namespace, "message": "session started",
	})
}

func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Summary string `json:"summary"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Summary) == "" {
		writeError(w, http.StatusBadRequest, "summary is required")
		return
	}

	result, err := s.deps.DB.ExecContext(r.Context(), `
		UPDATE sessions SET status = 'completed', summary = ?, ended_at = datetime('now')
		WHERE id = ? AND status = 'active'
	`, req.Summary, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "session not found or already ended")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"session_id": id, "message": "session completed"})
}

func (s *Server) handleGitHook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChangedFiles []string `json:"changed_files"`
		CommitSha    string   `json:"commit_sha"`
		Branch       string   `json:"branch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	if len(req.ChangedFiles) == 0 {
		writeError(w, http.StatusBadRequest, "changed_files is required")
		return
	}
	if strings.TrimSpace(req.CommitSha) == "" {
		writeError(w, http.StatusBadRequest, "commit_sha is required")
		return
	}

	ctx := r.Context()
	placeholders := make([]string, len(req.ChangedFiles))
	args := make([]any, len(req.ChangedFiles))
	for i, f := range req.ChangedFiles {
		placeholders[i] = "?"
		args[i] = f
	}

	// Expire file links
	fileQuery := fmt.Sprintf(`
		UPDATE file_observations SET valid_until = datetime('now'), commit_sha_until = ?
		WHERE file_path IN (%s) AND valid_until IS NULL
	`, strings.Join(placeholders, ","))
	s.deps.DB.ExecContext(ctx, fileQuery, append([]any{req.CommitSha}, args...)...)

	// Mark observations stale
	staleQuery := fmt.Sprintf(`
		UPDATE observations
		SET staleness = 'stale', confidence = MAX(0.01, confidence * 0.5),
		    updated_at = datetime('now'), modified_epoch = modified_epoch + 1
		WHERE deleted_at IS NULL AND staleness = 'fresh'
		  AND id IN (SELECT DISTINCT observation_id FROM file_observations WHERE file_path IN (%s))
	`, strings.Join(placeholders, ","))

	result, _ := s.deps.DB.ExecContext(ctx, staleQuery, args...)
	affected, _ := result.RowsAffected()

	writeJSON(w, http.StatusOK, map[string]any{
		"commit_sha": req.CommitSha, "files_processed": len(req.ChangedFiles),
		"observations_staled": affected, "message": "git hook processed",
	})
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx := r.Context()

	limit := 50
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	offset := 0
	if o := q.Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	var where []string
	var args []any
	where = append(where, "deleted_at IS NULL")

	if v := q.Get("type"); v != "" {
		where = append(where, "observation_type = ?")
		args = append(args, v)
	}
	if v := q.Get("layer"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			where = append(where, "layer = ?")
			args = append(args, n)
		}
	}
	if v := q.Get("namespace"); v != "" {
		where = append(where, "namespace = ?")
		args = append(args, v)
	}
	if v := q.Get("kind"); v != "" {
		where = append(where, "kind = ?")
		args = append(args, v)
	}
	if v := q.Get("staleness"); v != "" {
		where = append(where, "staleness = ?")
		args = append(args, v)
	}

	whereClause := strings.Join(where, " AND ")

	// Count total.
	var total int
	countQ := "SELECT COUNT(*) FROM observations WHERE " + whereClause
	s.deps.DB.QueryRowContext(ctx, countQ, args...).Scan(&total)

	// Query page.
	selectQ := fmt.Sprintf(`
		SELECT id, title, content, observation_type, layer, confidence, importance, kind,
		       COALESCE(tags, ''), namespace, COALESCE(staleness, 'fresh'), COALESCE(created_at, '')
		FROM observations WHERE %s
		ORDER BY importance DESC, created_at DESC
		LIMIT ? OFFSET ?
	`, whereClause)
	queryArgs := append(args, limit, offset)

	rows, err := s.deps.DB.QueryContext(ctx, selectQ, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id, title, content, obsType, kind, tags, ns, staleness, createdAt string
		var layer int
		var confidence, importance float64
		if err := rows.Scan(&id, &title, &content, &obsType, &layer, &confidence, &importance, &kind, &tags, &ns, &staleness, &createdAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		item := map[string]any{
			"id": id, "title": title, "content": content,
			"observation_type": obsType, "layer": layer,
			"confidence": confidence, "importance": importance,
			"kind": kind, "namespace": ns, "staleness": staleness,
			"created_at": createdAt,
		}
		if tags != "" {
			item["tags"] = splitCSV(tags)
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "limit": limit, "offset": offset,
		"count": len(items), "items": items,
	})
}

func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := s.deps.DB

	// By type.
	byType := map[string]int{}
	rows, _ := db.QueryContext(ctx, "SELECT observation_type, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY observation_type ORDER BY COUNT(*) DESC")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			var c int
			rows.Scan(&t, &c)
			byType[t] = c
		}
	}

	// By layer.
	byLayer := map[string]int{}
	rows2, _ := db.QueryContext(ctx, "SELECT layer, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY layer ORDER BY layer")
	if rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var l, c int
			rows2.Scan(&l, &c)
			name := "buffer"
			if l == 1 {
				name = "working"
			} else if l == 2 {
				name = "core"
			}
			byLayer[name] = c
		}
	}

	// By namespace.
	byNamespace := map[string]int{}
	rows3, _ := db.QueryContext(ctx, "SELECT namespace, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY namespace ORDER BY COUNT(*) DESC")
	if rows3 != nil {
		defer rows3.Close()
		for rows3.Next() {
			var ns string
			var c int
			rows3.Scan(&ns, &c)
			byNamespace[ns] = c
		}
	}

	// By kind.
	byKind := map[string]int{}
	rows4, _ := db.QueryContext(ctx, "SELECT kind, COUNT(*) FROM observations WHERE deleted_at IS NULL GROUP BY kind ORDER BY COUNT(*) DESC")
	if rows4 != nil {
		defer rows4.Close()
		for rows4.Next() {
			var k string
			var c int
			rows4.Scan(&k, &c)
			byKind[k] = c
		}
	}

	// Edge breakdown.
	byRelation := map[string]int{}
	rows5, _ := db.QueryContext(ctx, "SELECT relation_type, COUNT(*) FROM observation_links GROUP BY relation_type ORDER BY COUNT(*) DESC")
	if rows5 != nil {
		defer rows5.Close()
		for rows5.Next() {
			var r string
			var c int
			rows5.Scan(&r, &c)
			byRelation[r] = c
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"by_type":      byType,
		"by_layer":     byLayer,
		"by_namespace":  byNamespace,
		"by_kind":      byKind,
		"by_relation":  byRelation,
	})
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 200
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	minImp := 0.0
	if mi := q.Get("min_importance"); mi != "" {
		if v, err := strconv.ParseFloat(mi, 64); err == nil {
			minImp = v
		}
	}

	opts := graph.Options{
		Namespace:       q.Get("namespace"),
		ObservationType: q.Get("type"),
		Tags:            q.Get("tags"),
		MinImportance:   minImp,
		Limit:           limit,
		LinkedOnly:      q.Get("linked_only") == "true",
	}

	data, err := graph.Query(r.Context(), s.deps.DB, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// If ?format=json, return raw data. Otherwise serve HTML.
	if q.Get("format") == "json" {
		writeJSON(w, http.StatusOK, data)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := graph.RenderHTML(w, data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) handleReflect(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "reflection requires LLM provider (not yet implemented)",
	})
}

// helpers

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	return true
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

func nullStr(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func observationToJSON(obs observation.Observation) map[string]any {
	m := map[string]any{
		"id": obs.ID, "title": obs.Title, "content": obs.Content,
		"observation_type": obs.ObservationType, "layer": obs.Layer,
		"confidence": obs.Confidence, "importance": obs.Importance,
		"kind": obs.Kind, "namespace": obs.Namespace,
		"created_at": obs.CreatedAt, "updated_at": obs.UpdatedAt,
	}
	if len(obs.Tags) > 0 {
		m["tags"] = obs.Tags
	}
	if len(obs.Files) > 0 {
		m["files"] = obs.Files
	}
	if obs.TopicKey != "" {
		m["topic_key"] = obs.TopicKey
	}
	if obs.Source != "" {
		m["source"] = obs.Source
	}
	return m
}
