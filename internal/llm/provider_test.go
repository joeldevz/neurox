package llm

import (
	"context"
	"testing"
)

func TestDisabledProvider(t *testing.T) {
	p := Disabled{}

	if p.Name() != "disabled" {
		t.Errorf("name = %q, want %q", p.Name(), "disabled")
	}

	_, err := p.Complete(context.Background(), "test")
	if err != ErrLLMDisabled {
		t.Errorf("err = %v, want ErrLLMDisabled", err)
	}

	if IsAvailable(p) {
		t.Error("Disabled should not be available")
	}
}

func TestIsAvailable(t *testing.T) {
	if IsAvailable(Disabled{}) {
		t.Error("Disabled should not be available")
	}
	// OllamaProvider is available (even if it can't connect)
	o := NewOllama(OllamaConfig{})
	if !IsAvailable(o) {
		t.Error("OllamaProvider should be available")
	}
}
