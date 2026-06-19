package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

const defaultOllamaURL = "http://localhost:11434"

type OllamaConfig struct {
	URL   string
	Model string
}

type Ollama struct {
	url    string
	model  string
	client *http.Client
	dims   atomic.Int32 // populated on first successful EmbedBatch
}

func NewOllama(cfg OllamaConfig) *Ollama {
	url := cfg.URL
	if url == "" {
		url = defaultOllamaURL
	}
	return &Ollama{
		url:   url,
		model: cfg.Model,
		client: &http.Client{
			Timeout: embedTimeout(),
		},
	}
}

func (o *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings")
	}
	return batch[0], nil
}

func (o *Ollama) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{
		"model": o.model,
		"input": texts,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/embed", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}

	// Populate dims on first successful batch
	if len(result.Embeddings) > 0 {
		o.dims.CompareAndSwap(0, int32(len(result.Embeddings[0])))
	}

	return result.Embeddings, nil
}

func (o *Ollama) Dimensions() int { return int(o.dims.Load()) }
func (o *Ollama) Name() string    { return "ollama/" + o.model }

// Ping checks if Ollama is reachable and the model is available.
func (o *Ollama) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("create ping request: %w", err)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned %d", resp.StatusCode)
	}
	return nil
}
