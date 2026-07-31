package recall

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	accessQueueSize  = 1024
	accessFlushEvery = 2 * time.Second
	accessFlushBatch = 100
)

// accessQueue coalesces recall access-bump events (access_count++,
// last_accessed=now) into periodic batch UPDATEs so Search never blocks
// on or fails because of a write. Events are counted per ID so repeated
// recalls in the same flush window increment access_count correctly.
type accessQueue struct {
	db       *sql.DB
	ch       chan string // observation ID
	stop     chan struct{}
	flushSig chan struct{} // test-only: signal worker to flush immediately
	wg       sync.WaitGroup
}

func newAccessQueue(db *sql.DB) *accessQueue {
	return &accessQueue{
		db:       db,
		ch:       make(chan string, accessQueueSize),
		stop:     make(chan struct{}),
		flushSig: make(chan struct{}, 1),
	}
}

func (q *accessQueue) start(ctx context.Context) {
	q.wg.Add(1)
	go q.worker(ctx)
}

func (q *accessQueue) stopAndWait() {
	close(q.stop)
	q.wg.Wait()
}

// flushNow is a test-only method that signals the worker to immediately flush pending counts.
// Used by tests to ensure access bumps are persisted before assertions.
func (q *accessQueue) flushNow() error {
	// Give the worker time to process any pending channel items first.
	time.Sleep(50 * time.Millisecond)

	// Signal the worker to flush.
	select {
	case q.flushSig <- struct{}{}:
	default:
	}

	// Give the worker a moment to process the flush signal and execute the UPDATE.
	time.Sleep(200 * time.Millisecond)
	return nil
}

// enqueue submits IDs for a coalesced access bump. Never blocks: if the
// channel is full, the event is dropped (telemetry, not correctness —
// access_count/last_accessed are best-effort signals, not required for
// read correctness) and logged at debug level.
func (q *accessQueue) enqueue(ids []string) {
	for _, id := range ids {
		select {
		case q.ch <- id:
		default:
			log.Printf("DEBUG: access queue full (%d), dropping bump for %s", accessQueueSize, id)
		}
	}
}

func (q *accessQueue) worker(ctx context.Context) {
	defer q.wg.Done()
	counts := make(map[string]int)
	ticker := time.NewTicker(accessFlushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(counts) == 0 {
			return
		}
		if err := q.flush(counts); err != nil {
			log.Printf("DEBUG: access queue flush failed: %v", err)
		}
		counts = make(map[string]int)
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-q.stop:
			flush()
			return
		case id := <-q.ch:
			counts[id]++
			if len(counts) >= accessFlushBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-q.flushSig:
			flush()
		}
	}
}

func (q *accessQueue) flush(counts map[string]int) error {
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for id, n := range counts {
		activationInc := 0.08 * float64(n)
		consolidationInc := 0.02 * float64(n)
		if _, err := q.db.ExecContext(fctx, `
			UPDATE observations
			SET access_count = access_count + ?,
				last_accessed = datetime('now'),
				activation_level = MIN(1.0, activation_level + ?),
				consolidation_strength = MIN(1.0, consolidation_strength + ?)
			WHERE deleted_at IS NULL AND id = ?
		`, n, activationInc, consolidationInc, id); err != nil {
			return fmt.Errorf("bump access for %s: %w", id, err)
		}
	}
	return nil
}
