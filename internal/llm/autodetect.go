package llm

import (
	"context"
	"log"
	"time"
)

// AutoDetect tries LLM providers in priority order: Ollama → Remote → Disabled.
func AutoDetect(ctx context.Context, ollamaCfg OllamaConfig, remoteCfg RemoteConfig) Provider {
	// Try Ollama first
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
