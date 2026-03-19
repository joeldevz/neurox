package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RemoteConfig configures an OpenAI-compatible embedding API.
type RemoteConfig struct {
	URL        string // e.g. "https://api.openai.com/v1"
	APIKey     string
	Model      string // e.g. "text-embedding-3-small"
	Dimensions int    // vector dimensions
}

type Remote struct {
	url        string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

func NewRemote(cfg RemoteConfig) *Remote {
	dims := cfg.Dimensions
	if dims == 0 {
		dims = 1536
	}
	return &Remote{
		url:        cfg.URL,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		dimensions: dims,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Remote) Embed(ctx context.Context, text string) ([]float32, error) {
	batch, err := r.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, fmt.Errorf("remote returned empty embeddings")
	}
	return batch[0], nil
}

func (r *Remote) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{
		"model": r.model,
		"input": texts,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url+"/embeddings", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode remote response: %w", err)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		embeddings[i] = d.Embedding
	}
	return embeddings, nil
}

func (r *Remote) Dimensions() int { return r.dimensions }
func (r *Remote) Name() string    { return "remote/" + r.model }
