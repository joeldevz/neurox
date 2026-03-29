package llm

import (
	"context"
	"log"
	"strings"
	"time"
)

// AutoDetect tries LLM providers in priority order: Ollama → Remote → Disabled.
// If provider is "none" or "disabled", returns Disabled immediately without attempting any connections.
func AutoDetect(ctx context.Context, provider string, ollamaCfg OllamaConfig, remoteCfg RemoteConfig) Provider {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "none", "disabled":
		log.Printf("llm provider explicitly disabled")
		return Disabled{}
	case "remote":
		if remoteCfg.URL != "" && remoteCfg.Model != "" {
			remote := NewRemote(remoteCfg)
			log.Printf("using llm provider: %s", remote.Name())
			return remote
		}
		log.Printf("llm remote provider configured but url/model missing, falling back to disabled")
		return Disabled{}
	case "ollama":
		ollama := NewOllama(ollamaCfg)
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := ollama.Ping(pingCtx); err == nil {
			log.Printf("using llm provider: %s", ollama.Name())
			return ollama
		}
		log.Printf("ollama llm not available: configured but unreachable")
		return Disabled{}
	}

	// Auto-detect mode: try Ollama first
	if ollamaCfg.URL != "" || ollamaCfg.Model != "" || remoteCfg.URL == "" {
		ollama := NewOllama(ollamaCfg)
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if err := ollama.Ping(pingCtx); err == nil {
			log.Printf("using llm provider: %s", ollama.Name())
			return ollama
		} else {
			log.Printf("ollama llm not available: %v", err)
		}
	}

	// Try Remote if configured
	if remoteCfg.URL != "" && remoteCfg.Model != "" {
		remote := NewRemote(remoteCfg)
		log.Printf("using llm provider: %s", remote.Name())
		return remote
	}

	log.Printf("no llm provider available, quality gate disabled")
	return Disabled{}
}
