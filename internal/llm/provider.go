package llm

import "context"

// Provider generates text completions from an LLM.
type Provider interface {
	// Complete sends a prompt and returns the completion text.
	Complete(ctx context.Context, prompt string) (string, error)

	// Name returns the provider name for logging.
	Name() string
}

// Disabled is a no-op provider when no LLM is available.
type Disabled struct{}

func (Disabled) Complete(_ context.Context, _ string) (string, error) {
	return "", ErrLLMDisabled
}

func (Disabled) Name() string { return "disabled" }

// IsAvailable returns true if the provider is not nil and not Disabled.
func IsAvailable(p Provider) bool {
	if p == nil {
		return false
	}
	_, disabled := p.(Disabled)
	return !disabled
}
