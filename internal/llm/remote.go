package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RemoteConfig configures an OpenAI-compatible chat completion API.
type RemoteConfig struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// RemoteProvider calls an OpenAI-compatible /chat/completions endpoint.
type RemoteProvider struct {
	url    string
	apiKey string
	model  string
	client *http.Client
}

func NewRemote(cfg RemoteConfig) *RemoteProvider {
	return &RemoteProvider{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewRemoteWithTimeout creates a RemoteProvider with a custom HTTP timeout.
// Use this for long-running operations like curation where the default 60s
// is insufficient for large prompts.
func NewRemoteWithTimeout(cfg RemoteConfig, timeout time.Duration) *RemoteProvider {
	return &RemoteProvider{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		client: &http.Client{Timeout: timeout},
	}
}

func (r *RemoteProvider) Complete(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", ErrEmptyPrompt
	}

	body := map[string]any{
		"model": r.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal llm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create llm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote llm request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode remote response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("remote returned no choices")
	}

	return stripThinkTags(result.Choices[0].Message.Content), nil
}

func (r *RemoteProvider) Name() string { return "remote/" + r.model }
