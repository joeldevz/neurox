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

const (
	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "qwen3:8b"
)

// OllamaConfig configures the Ollama LLM provider.
type OllamaConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

// OllamaProvider calls the Ollama /api/generate endpoint.
type OllamaProvider struct {
	url    string
	model  string
	client *http.Client
}

func NewOllama(cfg OllamaConfig) *OllamaProvider {
	url := cfg.URL
	if url == "" {
		url = defaultOllamaURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultOllamaModel
	}
	return &OllamaProvider{
		url:   url,
		model: model,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (o *OllamaProvider) Complete(ctx context.Context, prompt string) (string, error) {
	if prompt == "" {
		return "", ErrEmptyPrompt
	}

	body := map[string]any{
		"model":  o.model,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal llm request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url+"/api/generate", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create llm request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama generate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	return stripThinkTags(result.Response), nil
}

func (o *OllamaProvider) Name() string { return "ollama/" + o.model }

// Ping checks if Ollama is reachable.
func (o *OllamaProvider) Ping(ctx context.Context) error {
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
