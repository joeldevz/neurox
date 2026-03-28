package observation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSaveQueueBasic(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewSaveQueue(store)
	q.Start(ctx)
	defer q.Stop()

	result, err := q.Enqueue(ctx, Observation{
		Title:   "Queued observation",
		Content: "This should be saved asynchronously",
	})
	if err != nil {
		t.Fatalf("Enqueue returned error: %v", err)
	}
	if result.ID == "" {
		t.Fatal("Enqueue returned empty ID")
	}
	if result.Title != "Queued observation" {
		t.Fatalf("Title = %q, want %q", result.Title, "Queued observation")
	}

	// Give the worker time to persist.
	time.Sleep(200 * time.Millisecond)

	// Verify observation was actually persisted.
	got, err := store.Get(ctx, result.ID)
	if err != nil {
		t.Fatalf("Get after queue flush: %v", err)
	}
	if got.Title != "Queued observation" {
		t.Fatalf("persisted Title = %q, want %q", got.Title, "Queued observation")
	}
}

func TestSaveQueueReturnsImmediately(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewSaveQueue(store)
	q.Start(ctx)
	defer q.Stop()

	start := time.Now()
	_, err := q.Enqueue(ctx, Observation{
		Title:   "Fast save",
		Content: "Should return in under 10ms",
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Enqueue error: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Fatalf("Enqueue took %v, expected under 10ms", elapsed)
	}
}

func TestSaveQueueValidationError(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx := context.Background()
	q := NewSaveQueue(store)
	q.Start(ctx)
	defer q.Stop()

	// Empty title should fail validation synchronously.
	_, err := q.Enqueue(ctx, Observation{
		Title:   "",
		Content: "no title",
	})
	if err == nil {
		t.Fatal("expected validation error for empty title")
	}
}

func TestSaveQueuePostSaveHooks(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var hookCalled atomic.Int32

	q := NewSaveQueue(store)
	q.OnPostSave(func(_ context.Context, saved Observation) {
		hookCalled.Add(1)
	})
	q.Start(ctx)

	_, err := q.Enqueue(ctx, Observation{
		Title:   "Hook test",
		Content: "Should trigger post-save hook",
	})
	if err != nil {
		t.Fatalf("Enqueue error: %v", err)
	}

	// Give worker time.
	time.Sleep(200 * time.Millisecond)
	q.Stop()

	if hookCalled.Load() != 1 {
		t.Fatalf("hook called %d times, want 1", hookCalled.Load())
	}
}

func TestSaveQueueDrainsOnStop(t *testing.T) {
	store, database := newTestStore(t)
	defer database.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := NewSaveQueue(store)
	q.Start(ctx)

	// Enqueue several observations.
	ids := make([]string, 5)
	for i := range ids {
		result, err := q.Enqueue(ctx, Observation{
			Title:   "Drain test",
			Content: "batch item",
		})
		if err != nil {
			t.Fatalf("Enqueue %d error: %v", i, err)
		}
		ids[i] = result.ID
	}

	// Stop should drain all pending items.
	q.Stop()

	// All should be persisted now.
	for i, id := range ids {
		_, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("observation %d (id=%s) not found after drain: %v", i, id, err)
		}
	}
}
