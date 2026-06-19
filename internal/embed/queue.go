package embed

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultBatchSize    = 50
	defaultFlushEvery   = 500 * time.Millisecond
	defaultMaxRetries   = 3
	defaultRetryBackoff = time.Second
)

// Preference ranking for embedding models (highest priority first)
var embedModelRanking = []string{
	"qwen3-embedding",
	"mxbai-embed-large",
	"bge-m3",
	"bge-large",
	"nomic-embed-text",
	"snowflake-arctic-embed",
	"all-minilm",
}

// Queue processes embedding requests asynchronously in batches.
type Queue struct {
	provider Provider
	db       *sql.DB
	ch       chan string
	wg       sync.WaitGroup
	stop     chan struct{}
}

// NewQueue creates a background embedding queue. Call Start() to begin processing.
func NewQueue(provider Provider, db *sql.DB) *Queue {
	return &Queue{
		provider: provider,
		db:       db,
		ch:       make(chan string, 1000),
		stop:     make(chan struct{}),
	}
}

// Enqueue adds an observation ID to the embedding queue.
func (q *Queue) Enqueue(id string) {
	select {
	case q.ch <- id:
	default:
		log.Printf("embed queue full, dropping %s", id)
	}
}

// Start begins the background worker that processes the queue.
func (q *Queue) Start(ctx context.Context) {
	q.wg.Add(1)
	go q.worker(ctx)
}

// Stop gracefully shuts down the queue, processing remaining items.
func (q *Queue) Stop() {
	close(q.stop)
	q.wg.Wait()
}

func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()

	batch := make([]string, 0, defaultBatchSize)
	ticker := time.NewTicker(defaultFlushEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.flush(context.Background(), batch)
			return
		case <-q.stop:
			// Drain remaining
			for {
				select {
				case id := <-q.ch:
					batch = append(batch, id)
					if len(batch) >= defaultBatchSize {
						q.flush(context.Background(), batch)
						batch = batch[:0]
					}
				default:
					q.flush(context.Background(), batch)
					return
				}
			}
		case id := <-q.ch:
			batch = append(batch, id)
			if len(batch) >= defaultBatchSize {
				q.flush(ctx, batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				q.flush(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

func (q *Queue) flush(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}

	// Load texts for these IDs
	texts := make([]string, 0, len(ids))
	validIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		var title, content string
		err := q.db.QueryRowContext(ctx, "SELECT title, content FROM observations WHERE id = ? AND deleted_at IS NULL", id).Scan(&title, &content)
		if err != nil {
			continue
		}
		validIDs = append(validIDs, id)
		texts = append(texts, title+" "+content)
	}

	if len(texts) == 0 {
		return
	}

	var embeddings [][]float32
	var err error

	for attempt := 0; attempt < defaultMaxRetries; attempt++ {
		embeddings, err = q.provider.EmbedBatch(ctx, texts)
		if err == nil {
			break
		}
		if attempt < defaultMaxRetries-1 {
			backoff := defaultRetryBackoff * time.Duration(1<<attempt)
			log.Printf("embed batch retry %d/%d after %v: %v", attempt+1, defaultMaxRetries, backoff, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}

	if err != nil {
		log.Printf("embed batch failed after %d retries: %v", defaultMaxRetries, err)
		return
	}

	// Store embeddings
	for i, id := range validIDs {
		if i >= len(embeddings) {
			break
		}
		blob := SerializeF32(embeddings[i])
		if _, err := q.db.ExecContext(ctx, "UPDATE observations SET embedding = ? WHERE id = ?", blob, id); err != nil {
			log.Printf("store embedding for %s: %v", id, err)
		}
	}

	log.Printf("embedded %d observations", len(validIDs))
}

// AutoDetect tries Ollama first, then Remote, then returns Disabled.
// If provider is "none" or "disabled", returns Disabled immediately without attempting any connections.
func AutoDetect(ctx context.Context, provider string, ollamaCfg OllamaConfig, remoteCfg ...RemoteConfig) Provider {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "none", "disabled":
		log.Printf("embedding provider explicitly disabled")
		return Disabled{}
	case "remote":
		if len(remoteCfg) > 0 && remoteCfg[0].URL != "" && remoteCfg[0].Model != "" {
			remote := NewRemote(remoteCfg[0])
			testCtx, testCancel := context.WithTimeout(ctx, 10*time.Second)
			defer testCancel()
			if _, testErr := remote.Embed(testCtx, "test"); testErr == nil {
				log.Printf("using embedding provider: %s", remote.Name())
				return remote
			}
			log.Printf("remote embedding provider configured but test failed, falling back to disabled")
		} else {
			log.Printf("remote embedding provider configured but url/model missing, falling back to disabled")
		}
		return Disabled{}
	case "ollama":
		if ollamaCfg.Model == "" {
			detected := pickBestEmbedModel(ctx, ollamaCfg.URL)
			if detected == "" {
				log.Printf("no embedding model found; run: ollama pull qwen3-embedding:0.6b")
				return Disabled{}
			}
			ollamaCfg.Model = detected
		}
		ollama := NewOllama(ollamaCfg)
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := ollama.Ping(pingCtx); err == nil {
			testCtx, testCancel := context.WithTimeout(ctx, 10*time.Second)
			defer testCancel()
			if _, testErr := ollama.Embed(testCtx, "test"); testErr == nil {
				log.Printf("using embedding provider: %s", ollama.Name())
				return ollama
			} else {
				log.Printf("ollama embed test failed: %v", testErr)
			}
		} else {
			log.Printf("ollama not available for embeddings: configured but unreachable")
		}
		return Disabled{}
	}

	// Auto-detect mode: try Ollama first
	// If no model is configured, auto-detect the best available embedding model
	if ollamaCfg.Model == "" {
		detected := pickBestEmbedModel(ctx, ollamaCfg.URL)
		if detected == "" {
			log.Printf("no embedding model found; run: ollama pull qwen3-embedding:0.6b")
			return Disabled{}
		}
		ollamaCfg.Model = detected
	}

	ollama := NewOllama(ollamaCfg)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := ollama.Ping(pingCtx); err == nil {
		testCtx, testCancel := context.WithTimeout(ctx, 10*time.Second)
		defer testCancel()

		if _, testErr := ollama.Embed(testCtx, "test"); testErr == nil {
			log.Printf("using embedding provider: %s", ollama.Name())
			return ollama
		} else {
			log.Printf("ollama embed test failed: %v", testErr)
		}
	} else {
		log.Printf("ollama not available for embeddings: %v", err)
	}

	// Try Remote if configured
	if len(remoteCfg) > 0 && remoteCfg[0].URL != "" && remoteCfg[0].Model != "" {
		remote := NewRemote(remoteCfg[0])
		testCtx, testCancel := context.WithTimeout(ctx, 10*time.Second)
		defer testCancel()

		if _, testErr := remote.Embed(testCtx, "test"); testErr == nil {
			log.Printf("using embedding provider: %s", remote.Name())
			return remote
		} else {
			log.Printf("remote embed test failed: %v", testErr)
		}
	}

	log.Printf("no embedding provider available, embeddings disabled")
	return Disabled{}
}

// IsAvailable returns true if the provider is not Disabled.
func IsAvailable(p Provider) bool {
	_, disabled := p.(Disabled)
	return !disabled
}

// ReembedAll sets all embeddings to NULL and triggers a backfill.
// This is used when the embedding model changes.
func (q *Queue) ReembedAll(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE observations SET embedding = NULL WHERE deleted_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("clear embeddings: %w", err)
	}
	q.BackfillPending(ctx)
	return nil
}

// BackfillPending queries all observations without embeddings and enqueues them.
// Best-effort: logs errors but never returns them.
func (q *Queue) BackfillPending(ctx context.Context) {
	rows, err := q.db.QueryContext(ctx,
		"SELECT id FROM observations WHERE embedding IS NULL AND deleted_at IS NULL ORDER BY importance DESC LIMIT 500")
	if err != nil {
		log.Printf("backfill query: %v", err)
		return
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		q.Enqueue(id)
		count++
	}
	if count > 0 {
		log.Printf("backfill: enqueued %d observations for embedding", count)
	}
}

// pickBestEmbedModel queries Ollama for available models and returns the
// highest-ranked embedding model from embedModelRanking, or "" if none found.
func pickBestEmbedModel(ctx context.Context, baseURL string) string {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return ""
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	// For each ranked model, check if any available model contains it
	for _, ranked := range embedModelRanking {
		for _, m := range result.Models {
			if strings.Contains(m.Name, ranked) {
				return ranked
			}
		}
	}
	return ""
}
