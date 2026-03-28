package observation

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	defaultQueueSize  = 500
	defaultDrainPause = 50 * time.Millisecond
)

// SaveResult is the immediate response returned to the MCP client before
// the observation is persisted.  ID is pre-generated so the caller can
// reference it right away.
type SaveResult struct {
	ID        string
	Title     string
	Namespace string
	TopicKey  string
}

// PreSaveFilter runs in the background worker before persisting.
// Return false to silently skip the observation (e.g. quality gate reject).
type PreSaveFilter func(ctx context.Context, obs Observation) bool

// PostSaveHook is called by the worker after an observation is successfully
// persisted.  Implementations must not block indefinitely.
type PostSaveHook func(ctx context.Context, saved Observation)

// SaveQueue decouples MCP handlers from the SQLite write path.
//
// Enqueue validates the observation, pre-generates a ULID, and pushes it
// into a buffered channel.  A single background goroutine drains the
// channel and writes to the Store sequentially—no external lock contention.
//
// If the queue is full, Enqueue falls back to a synchronous write so the
// observation is never silently dropped.
type SaveQueue struct {
	store   *Store
	ch      chan Observation
	filters []PreSaveFilter
	hooks   []PostSaveHook
	wg      sync.WaitGroup
	stop    chan struct{}
}

// NewSaveQueue creates a queue backed by the given Store.
// Call Start() to launch the background worker.
func NewSaveQueue(store *Store) *SaveQueue {
	return &SaveQueue{
		store: store,
		ch:    make(chan Observation, defaultQueueSize),
		stop:  make(chan struct{}),
	}
}

// OnPreSave registers a filter that runs before each write.
// If any filter returns false, the observation is silently dropped.
func (q *SaveQueue) OnPreSave(filter PreSaveFilter) {
	q.filters = append(q.filters, filter)
}

// OnPostSave registers a hook that runs after each successful write.
func (q *SaveQueue) OnPostSave(hook PostSaveHook) {
	q.hooks = append(q.hooks, hook)
}

// Enqueue validates the observation synchronously (so callers get immediate
// errors for bad input), pre-generates an ID, and pushes to the background
// worker.  If the queue is full, it falls back to a blocking Store.Save.
func (q *SaveQueue) Enqueue(ctx context.Context, obs Observation) (SaveResult, error) {
	obs.ApplyDefaults()
	if err := obs.Validate(); err != nil {
		return SaveResult{}, err
	}

	// Pre-generate ID so we can return it immediately.
	obs.ID = ulid.Make().String()

	select {
	case q.ch <- obs:
		// Queued successfully — return immediately.
		return SaveResult{
			ID:        obs.ID,
			Title:     obs.Title,
			Namespace: obs.Namespace,
			TopicKey:  obs.TopicKey,
		}, nil
	default:
		// Queue full — fall back to synchronous write.
		log.Printf("save queue full (%d), falling back to sync write", defaultQueueSize)
		saved, err := q.store.Save(ctx, obs)
		if err != nil {
			return SaveResult{}, err
		}
		q.runHooks(saved)
		return SaveResult{
			ID:        saved.ID,
			Title:     saved.Title,
			Namespace: saved.Namespace,
			TopicKey:  saved.TopicKey,
		}, nil
	}
}

// Start launches the single background worker.
func (q *SaveQueue) Start(ctx context.Context) {
	q.wg.Add(1)
	go q.worker(ctx)
}

// Stop drains remaining items and waits for the worker to finish.
func (q *SaveQueue) Stop() {
	close(q.stop)
	q.wg.Wait()
}

// Pending returns the number of items waiting in the queue.
func (q *SaveQueue) Pending() int {
	return len(q.ch)
}

func (q *SaveQueue) worker(ctx context.Context) {
	defer q.wg.Done()

	for {
		select {
		case <-ctx.Done():
			q.drain()
			return
		case <-q.stop:
			q.drain()
			return
		case obs := <-q.ch:
			q.persist(obs)
		}
	}
}

// drain processes all remaining items in the channel.
func (q *SaveQueue) drain() {
	for {
		select {
		case obs := <-q.ch:
			q.persist(obs)
		default:
			return
		}
	}
}

// persist runs pre-save filters, writes a single observation, and fires hooks.
func (q *SaveQueue) persist(obs Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, filter := range q.filters {
		if !filter(ctx, obs) {
			log.Printf("save queue: filtered out %q (id=%s)", obs.Title, obs.ID)
			return
		}
	}

	saved, err := q.store.Save(ctx, obs)
	if err != nil {
		log.Printf("save queue: persist failed for %q (id=%s): %v", obs.Title, obs.ID, err)
		return
	}
	q.runHooks(saved)
}

func (q *SaveQueue) runHooks(saved Observation) {
	ctx := context.Background()
	for _, hook := range q.hooks {
		hook(ctx, saved)
	}
}
