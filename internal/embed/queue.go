package embed

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	defaultBatchSize    = 50
	defaultFlushEvery   = 500 * time.Millisecond
	defaultMaxRetries   = 3
	defaultRetryBackoff = time.Second
)

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
func AutoDetect(ctx context.Context, ollamaCfg OllamaConfig, remoteCfg ...RemoteConfig) Provider {
	// Try Ollama first
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

// PendingCount returns the number of observations without embeddings.
func PendingCount(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE embedding IS NULL AND deleted_at IS NULL").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending embeddings: %w", err)
	}
	return count, nil
}
