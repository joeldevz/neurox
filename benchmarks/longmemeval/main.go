// Command bench-longmemeval runs the LongMemEval benchmark against Neurox's memory engine.
//
// It ingests conversation sessions as observations, runs recall queries, and measures
// retrieval quality (Recall@k, NDCG@k) and optionally QA accuracy.
//
// Usage:
//
//	go run -tags fts5 ./benchmarks/longmemeval/ -data benchmarks/longmemeval/data/longmemeval_oracle.json
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
	"github.com/joeldevz/neurox/internal/temporal"
)

// --- Dataset types ---

type questionRaw struct {
	QuestionID         string          `json:"question_id"`
	QuestionType       string          `json:"question_type"`
	Question           string          `json:"question"`
	Answer             json.RawMessage `json:"answer"`
	QuestionDate       string          `json:"question_date"`
	HaystackDates      []string        `json:"haystack_dates"`
	HaystackSessionIDs []string        `json:"haystack_session_ids"`
	HaystackSessions   [][]Turn        `json:"haystack_sessions"`
	AnswerSessionIDs   []string        `json:"answer_session_ids"`
}

type Question struct {
	QuestionID         string   `json:"question_id"`
	QuestionType       string   `json:"question_type"`
	Question           string   `json:"question"`
	Answer             string   `json:"answer"`
	QuestionDate       string   `json:"question_date"`
	HaystackDates      []string `json:"haystack_dates"`
	HaystackSessionIDs []string `json:"haystack_session_ids"`
	HaystackSessions   [][]Turn `json:"haystack_sessions"`
	AnswerSessionIDs   []string `json:"answer_session_ids"`
}

type Turn struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	HasAnswer bool   `json:"has_answer,omitempty"`
}

// --- Result types ---

type QuestionResult struct {
	QuestionID   string   `json:"question_id"`
	QuestionType string   `json:"question_type"`
	Hypothesis   string   `json:"hypothesis"`
	RetrievedIDs []string `json:"retrieved_session_ids"`
	AnswerIDs    []string `json:"answer_session_ids"`
	RecallAny    float64  `json:"recall_any"`
	RecallAll    float64  `json:"recall_all"`
	NDCGAny      float64  `json:"ndcg_any"`
}

type AggregateMetrics struct {
	Total         int                `json:"total"`
	RecallAnyAt5  float64            `json:"recall_any@5"`
	RecallAnyAt10 float64            `json:"recall_any@10"`
	RecallAllAt5  float64            `json:"recall_all@5"`
	RecallAllAt10 float64            `json:"recall_all@10"`
	NDCGAnyAt5    float64            `json:"ndcg_any@5"`
	NDCGAnyAt10   float64            `json:"ndcg_any@10"`
	ByType        map[string]TypeMetrics `json:"by_type"`
}

type TypeMetrics struct {
	Count         int     `json:"count"`
	RecallAnyAt5  float64 `json:"recall_any@5"`
	RecallAnyAt10 float64 `json:"recall_any@10"`
	RecallAllAt5  float64 `json:"recall_all@5"`
	RecallAllAt10 float64 `json:"recall_all@10"`
	NDCGAnyAt5    float64 `json:"ndcg_any@5"`
	NDCGAnyAt10   float64 `json:"ndcg_any@10"`
}

func main() {
	dataPath := flag.String("data", "benchmarks/longmemeval/data/longmemeval_oracle.json", "Path to LongMemEval JSON")
	outputPath := flag.String("output", "benchmarks/longmemeval/results.jsonl", "Output JSONL path")
	limit := flag.Int("limit", 0, "Limit number of questions (0 = all)")
	topK := flag.Int("k", 10, "Top-K for recall")
	typeFilter := flag.String("type", "", "Filter by question type (e.g. temporal-reasoning)")
	useEmbed := flag.Bool("embed", false, "Enable embedding provider (auto-detects ollama)")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	// Load dataset
	data, err := loadDataset(*dataPath)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	log.Printf("Loaded %d questions from %s", len(data), *dataPath)

	// Apply filters
	if *typeFilter != "" {
		var filtered []Question
		for _, q := range data {
			if q.QuestionType == *typeFilter {
				filtered = append(filtered, q)
			}
		}
		data = filtered
		log.Printf("Filtered to %d questions of type %q", len(data), *typeFilter)
	}
	if *limit > 0 && *limit < len(data) {
		data = data[:*limit]
		log.Printf("Limited to %d questions", *limit)
	}

	// Create temp DB
	tmpDir, err := os.MkdirTemp("", "neurox-bench-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bench.db")
	ctx := context.Background()
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	// Set up stores
	obsStore := observation.NewStore(database, nil)
	temporalStore := temporal.NewStore(database, observation.NewULIDGenerator())
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	obsStore.SetTemporalExtractor(temporalExtractor)

	// Embedding provider (optional)
	var embedder embed.Provider = embed.Disabled{}
	if *useEmbed {
		embedder = embed.AutoDetect(ctx, embed.OllamaConfig{})
		if embed.IsAvailable(embedder) {
			log.Printf("Embeddings enabled: %s", embedder.Name())
		} else {
			log.Println("WARNING: -embed flag set but no provider available")
		}
	}

	engine := recall.NewEngine(database, recall.WithEmbedder(embedder))

	// Process questions
	var results []QuestionResult
	start := time.Now()

	for i, q := range data {
		namespace := fmt.Sprintf("q_%s", q.QuestionID)

		// Ingest sessions
		sessionObsMap := ingestSessions(ctx, obsStore, database, embedder, q, namespace)

		// Run recall
		retrieved := runRecall(ctx, engine, q, namespace, *topK)

		// Map retrieved observation IDs back to session IDs
		retrievedSessionIDs := mapToSessionIDs(retrieved, sessionObsMap)

		// Build hypothesis from top results
		hypothesis := buildHypothesis(retrieved, *topK)

		// Compute retrieval metrics
		answerSet := toSet(q.AnswerSessionIDs)
		isAbstention := strings.HasSuffix(q.QuestionID, "_abs")

		result := QuestionResult{
			QuestionID:   q.QuestionID,
			QuestionType: q.QuestionType,
			Hypothesis:   hypothesis,
			RetrievedIDs: retrievedSessionIDs,
			AnswerIDs:    q.AnswerSessionIDs,
		}

		if !isAbstention && len(answerSet) > 0 {
			result.RecallAny = recallAnyAtK(retrievedSessionIDs, answerSet, *topK)
			result.RecallAll = recallAllAtK(retrievedSessionIDs, answerSet, *topK)
			result.NDCGAny = ndcgAnyAtK(retrievedSessionIDs, answerSet, *topK)
		}

		results = append(results, result)

		if *verbose {
			log.Printf("[%d/%d] %s (%s) recall_any@%d=%.2f recall_all@%d=%.2f",
				i+1, len(data), q.QuestionID, q.QuestionType,
				*topK, result.RecallAny, *topK, result.RecallAll)
		}

		if (i+1)%50 == 0 {
			log.Printf("Progress: %d/%d questions processed", i+1, len(data))
		}
	}

	duration := time.Since(start)

	// Compute aggregate metrics
	metrics := computeAggregateMetrics(results)

	// Print summary
	printSummary(metrics, duration)

	// Write JSONL output
	if err := writeJSONL(*outputPath, results); err != nil {
		log.Fatalf("write output: %v", err)
	}
	log.Printf("Results written to %s", *outputPath)

	// Write metrics JSON
	metricsPath := strings.TrimSuffix(*outputPath, filepath.Ext(*outputPath)) + "_metrics.json"
	if err := writeJSON(metricsPath, metrics); err != nil {
		log.Fatalf("write metrics: %v", err)
	}
	log.Printf("Metrics written to %s", metricsPath)
}

// --- Ingestion ---

// ingestSessions saves each session as an observation and returns a map of obsID → sessionID.
func ingestSessions(ctx context.Context, store *observation.Store, database *sql.DB, embedder embed.Provider, q Question, namespace string) map[string]string {
	obsToSession := make(map[string]string)

	for i, session := range q.HaystackSessions {
		sessionID := q.HaystackSessionIDs[i]
		sessionDate := q.HaystackDates[i]

		// Build session content
		var sb strings.Builder
		fmt.Fprintf(&sb, "[Session %s | %s]\n", sessionID, sessionDate)
		for _, turn := range session {
			fmt.Fprintf(&sb, "%s: %s\n", turn.Role, turn.Content)
		}

		// Build a title from first user message
		title := fmt.Sprintf("Session %s", sessionID[:8])
		for _, turn := range session {
			if turn.Role == "user" {
				title = truncate(turn.Content, 80)
				break
			}
		}

		obs := observation.Observation{
			Title:     title,
			Content:   sb.String(),
			Kind:      observation.KindEpisodic,
			Namespace: namespace,
			Tags:      []string{"session", sessionID},
		}

		saved, err := store.Save(ctx, obs)
		if err != nil {
			log.Printf("WARNING: ingest session %s failed: %v", sessionID, err)
			continue
		}

		obsToSession[saved.ID] = sessionID

		// Embed synchronously if provider available
		if embed.IsAvailable(embedder) {
			vec, embErr := embedder.Embed(ctx, saved.Title+" "+saved.Content)
			if embErr == nil {
				blob := embed.SerializeF32(vec)
				database.ExecContext(ctx, "UPDATE observations SET embedding = ? WHERE id = ?", blob, saved.ID)
			}
		}

		// Update created_at to match session date for temporal realism
		if parsedDate, ok := parseLongMemDate(sessionDate); ok {
			database.ExecContext(ctx, `
				UPDATE observations SET created_at = ?, valid_from = ? WHERE id = ?
			`, parsedDate.Format(time.RFC3339), parsedDate.Format(time.RFC3339), saved.ID)
		}
	}

	return obsToSession
}

// --- Recall ---

func runRecall(ctx context.Context, engine *recall.Engine, q Question, namespace string, topK int) []recall.Result {
	// Parse question date as reference time for temporal intent detection
	var now time.Time
	if parsedDate, ok := parseLongMemDate(q.QuestionDate); ok {
		now = parsedDate
	} else {
		now = time.Now().UTC()
	}

	// Extract keywords from the question. The recall engine uses FTS5 with AND
	// between all terms, so full natural-language questions would match nothing.
	// We extract the most salient keywords for better retrieval.
	keywords := extractKeywords(q.Question)

	results, err := engine.Search(ctx, recall.SearchOptions{
		Query:     keywords,
		Namespace: namespace,
		Limit:     topK,
		Now:       now,
	})
	if err != nil {
		log.Printf("WARNING: recall for %s failed (query=%q): %v", q.QuestionID, keywords, err)
		return nil
	}

	return results
}

// extractKeywords removes stop words and punctuation from a question to produce
// a keyword query suitable for FTS5 AND matching.
func extractKeywords(question string) string {
	stopWords := map[string]bool{
		"what": true, "was": true, "the": true, "a": true, "an": true,
		"is": true, "are": true, "were": true, "did": true, "do": true,
		"does": true, "i": true, "my": true, "me": true, "we": true,
		"you": true, "your": true, "our": true, "their": true, "it": true,
		"its": true, "this": true, "that": true, "these": true, "those": true,
		"in": true, "on": true, "at": true, "to": true, "for": true,
		"of": true, "with": true, "by": true, "from": true, "about": true,
		"had": true, "have": true, "has": true, "been": true, "be": true,
		"can": true, "could": true, "would": true, "should": true,
		"after": true, "before": true, "how": true, "when": true,
		"where": true, "who": true, "which": true, "any": true,
		"and": true, "or": true, "not": true, "no": true,
		"there": true, "than": true, "then": true, "also": true,
		"just": true, "but": true, "if": true, "so": true,
		"up": true, "out": true, "some": true, "most": true,
		"much": true, "many": true, "more": true, "very": true,
		"first": true, "last": true, "new": true, "old": true,
		"long": true, "recently": true, "ever": true, "since": true,
	}

	tokens := strings.Fields(strings.ToLower(question))
	var keywords []string
	for _, t := range tokens {
		t = strings.Trim(t, "?!.,;:'\"()[]{}")
		if t != "" && len(t) > 2 && !stopWords[t] {
			keywords = append(keywords, t)
		}
	}

	// With OR matching, more keywords improve recall without hurting precision
	// (BM25 ranks multi-term matches higher). Limit to avoid noise.
	if len(keywords) > 8 {
		keywords = keywords[:8]
	}

	if len(keywords) == 0 {
		// Fallback: use first few non-trivial words
		for _, t := range strings.Fields(strings.ToLower(question)) {
			t = strings.Trim(t, "?!.,;:'\"()[]{}")
			if len(t) > 3 {
				keywords = append(keywords, t)
				if len(keywords) >= 3 {
					break
				}
			}
		}
	}

	return strings.Join(keywords, " ")
}

// --- Hypothesis building ---

func buildHypothesis(results []recall.Result, topK int) string {
	if len(results) == 0 {
		return "I don't have enough information to answer this question."
	}

	limit := topK
	if limit > len(results) {
		limit = len(results)
	}

	var sb strings.Builder
	sb.WriteString("Based on our conversation history:\n\n")
	for i := 0; i < limit; i++ {
		sb.WriteString(results[i].Content)
		sb.WriteString("\n---\n")
	}

	return sb.String()
}

// --- Session ID mapping ---

func mapToSessionIDs(results []recall.Result, obsToSession map[string]string) []string {
	var sessionIDs []string
	seen := make(map[string]bool)
	for _, r := range results {
		if sid, ok := obsToSession[r.ID]; ok && !seen[sid] {
			sessionIDs = append(sessionIDs, sid)
			seen[sid] = true
		}
	}
	return sessionIDs
}

// --- Retrieval metrics ---

func recallAnyAtK(retrieved []string, relevant map[string]bool, k int) float64 {
	limit := k
	if limit > len(retrieved) {
		limit = len(retrieved)
	}
	for i := 0; i < limit; i++ {
		if relevant[retrieved[i]] {
			return 1.0
		}
	}
	return 0.0
}

func recallAllAtK(retrieved []string, relevant map[string]bool, k int) float64 {
	if len(relevant) == 0 {
		return 0.0
	}
	limit := k
	if limit > len(retrieved) {
		limit = len(retrieved)
	}
	found := 0
	for i := 0; i < limit; i++ {
		if relevant[retrieved[i]] {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

func ndcgAnyAtK(retrieved []string, relevant map[string]bool, k int) float64 {
	limit := k
	if limit > len(retrieved) {
		limit = len(retrieved)
	}

	// DCG
	dcg := 0.0
	for i := 0; i < limit; i++ {
		if relevant[retrieved[i]] {
			dcg += 1.0 / math.Log2(float64(i+2)) // i+2 because log2(1) = 0
		}
	}

	// Ideal DCG: all relevant docs at the top
	idealK := len(relevant)
	if idealK > limit {
		idealK = limit
	}
	idcg := 0.0
	for i := 0; i < idealK; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 0.0
	}
	return dcg / idcg
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

// --- Aggregate metrics ---

func computeAggregateMetrics(results []QuestionResult) AggregateMetrics {
	byType := make(map[string]*struct {
		count                                         int
		recallAny5, recallAny10                       float64
		recallAll5, recallAll10                        float64
		ndcgAny5, ndcgAny10                           float64
	})

	var totalRecallAny5, totalRecallAny10 float64
	var totalRecallAll5, totalRecallAll10 float64
	var totalNDCGAny5, totalNDCGAny10 float64
	nonAbstention := 0

	for _, r := range results {
		isAbstention := strings.HasSuffix(r.QuestionID, "_abs")
		if isAbstention {
			continue
		}
		nonAbstention++

		answerSet := toSet(r.AnswerIDs)

		ra5 := recallAnyAtK(r.RetrievedIDs, answerSet, 5)
		ra10 := r.RecallAny
		rl5 := recallAllAtK(r.RetrievedIDs, answerSet, 5)
		rl10 := r.RecallAll
		n5 := ndcgAnyAtK(r.RetrievedIDs, answerSet, 5)
		n10 := r.NDCGAny

		totalRecallAny5 += ra5
		totalRecallAny10 += ra10
		totalRecallAll5 += rl5
		totalRecallAll10 += rl10
		totalNDCGAny5 += n5
		totalNDCGAny10 += n10

		if _, ok := byType[r.QuestionType]; !ok {
			byType[r.QuestionType] = &struct {
				count                                         int
				recallAny5, recallAny10                       float64
				recallAll5, recallAll10                        float64
				ndcgAny5, ndcgAny10                           float64
			}{}
		}
		bt := byType[r.QuestionType]
		bt.count++
		bt.recallAny5 += ra5
		bt.recallAny10 += ra10
		bt.recallAll5 += rl5
		bt.recallAll10 += rl10
		bt.ndcgAny5 += n5
		bt.ndcgAny10 += n10
	}

	metrics := AggregateMetrics{
		Total:  nonAbstention,
		ByType: make(map[string]TypeMetrics),
	}

	if nonAbstention > 0 {
		n := float64(nonAbstention)
		metrics.RecallAnyAt5 = totalRecallAny5 / n
		metrics.RecallAnyAt10 = totalRecallAny10 / n
		metrics.RecallAllAt5 = totalRecallAll5 / n
		metrics.RecallAllAt10 = totalRecallAll10 / n
		metrics.NDCGAnyAt5 = totalNDCGAny5 / n
		metrics.NDCGAnyAt10 = totalNDCGAny10 / n
	}

	for typeName, bt := range byType {
		if bt.count > 0 {
			n := float64(bt.count)
			metrics.ByType[typeName] = TypeMetrics{
				Count:         bt.count,
				RecallAnyAt5:  bt.recallAny5 / n,
				RecallAnyAt10: bt.recallAny10 / n,
				RecallAllAt5:  bt.recallAll5 / n,
				RecallAllAt10: bt.recallAll10 / n,
				NDCGAnyAt5:    bt.ndcgAny5 / n,
				NDCGAnyAt10:   bt.ndcgAny10 / n,
			}
		}
	}

	return metrics
}

// --- Output ---

func printSummary(metrics AggregateMetrics, duration time.Duration) {
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("  LongMemEval Retrieval Results (Neurox)")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  Questions evaluated: %d (excluding abstention)\n", metrics.Total)
	fmt.Printf("  Duration: %v\n\n", duration.Round(time.Millisecond))

	fmt.Println("  Overall Retrieval Metrics:")
	fmt.Printf("    Recall_any@5:  %.4f\n", metrics.RecallAnyAt5)
	fmt.Printf("    Recall_any@10: %.4f\n", metrics.RecallAnyAt10)
	fmt.Printf("    Recall_all@5:  %.4f\n", metrics.RecallAllAt5)
	fmt.Printf("    Recall_all@10: %.4f\n", metrics.RecallAllAt10)
	fmt.Printf("    NDCG_any@5:    %.4f\n", metrics.NDCGAnyAt5)
	fmt.Printf("    NDCG_any@10:   %.4f\n", metrics.NDCGAnyAt10)

	// Sort types for consistent output
	types := make([]string, 0, len(metrics.ByType))
	for t := range metrics.ByType {
		types = append(types, t)
	}
	sort.Strings(types)

	fmt.Println("\n  Per-Type Breakdown:")
	fmt.Printf("  %-28s %5s %10s %10s %10s\n", "Type", "N", "R_any@10", "R_all@10", "NDCG@10")
	fmt.Println("  " + strings.Repeat("-", 65))
	for _, t := range types {
		m := metrics.ByType[t]
		fmt.Printf("  %-28s %5d %10.4f %10.4f %10.4f\n",
			t, m.Count, m.RecallAnyAt10, m.RecallAllAt10, m.NDCGAnyAt10)
	}
	fmt.Println(strings.Repeat("=", 70))
}

func writeJSONL(path string, results []QuestionResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- Helpers ---

func loadDataset(path string) ([]Question, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var raw []questionRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	questions := make([]Question, len(raw))
	for i, r := range raw {
		// Handle answer as string or number
		answer := strings.Trim(string(r.Answer), "\"")
		questions[i] = Question{
			QuestionID:         r.QuestionID,
			QuestionType:       r.QuestionType,
			Question:           r.Question,
			Answer:             answer,
			QuestionDate:       r.QuestionDate,
			HaystackDates:      r.HaystackDates,
			HaystackSessionIDs: r.HaystackSessionIDs,
			HaystackSessions:   r.HaystackSessions,
			AnswerSessionIDs:   r.AnswerSessionIDs,
		}
	}
	return questions, nil
}

// parseLongMemDate parses "2023/04/10 (Mon) 17:50" format.
func parseLongMemDate(s string) (time.Time, bool) {
	// Remove day name in parens: "2023/04/10 (Mon) 17:50" → "2023/04/10 17:50"
	cleaned := s
	if idx := strings.Index(s, "("); idx >= 0 {
		if end := strings.Index(s[idx:], ")"); end >= 0 {
			cleaned = strings.TrimSpace(s[:idx]) + " " + strings.TrimSpace(s[idx+end+1:])
		}
	}
	cleaned = strings.TrimSpace(cleaned)

	layouts := []string{
		"2006/01/02 15:04",
		"2006/01/02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, cleaned); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
