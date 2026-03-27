package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaDynamicDimensions(t *testing.T) {
	// Create a mock Ollama server that returns 4-dimensional embeddings
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
		case "/api/embed":
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Return 4-dimensional embeddings
			embeddings := make([][]float32, len(req.Input))
			for i := range req.Input {
				embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"embeddings": embeddings,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	o := NewOllama(OllamaConfig{URL: server.URL, Model: "test-model"})

	// Before any embedding, Dimensions should return 0
	if dims := o.Dimensions(); dims != 0 {
		t.Errorf("Dimensions before EmbedBatch = %d, want 0", dims)
	}

	ctx := context.Background()
	embeddings, err := o.EmbedBatch(ctx, []string{"test text 1", "test text 2"})
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}

	// Verify we got the expected embeddings
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	if len(embeddings[0]) != 4 {
		t.Errorf("expected 4-dimensional embedding, got %d", len(embeddings[0]))
	}

	// After successful EmbedBatch, Dimensions should return 4
	if dims := o.Dimensions(); dims != 4 {
		t.Errorf("Dimensions after EmbedBatch = %d, want 4", dims)
	}
}

func TestOllamaDimensionsRace(t *testing.T) {
	// Create a mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
		case "/api/embed":
			embeddings := [][]float32{{0.1, 0.2, 0.3, 0.4, 0.5}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"embeddings": embeddings,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	o := NewOllama(OllamaConfig{URL: server.URL, Model: "test-model"})
	ctx := context.Background()

	// Run multiple concurrent EmbedBatch calls to test for data races
	// This test should be run with -race flag
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			_, err := o.EmbedBatch(ctx, []string{"test"})
			if err != nil {
				t.Errorf("EmbedBatch failed: %v", err)
			}
			_ = o.Dimensions()
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final dimensions
	if dims := o.Dimensions(); dims != 5 {
		t.Errorf("Dimensions = %d, want 5", dims)
	}
}
