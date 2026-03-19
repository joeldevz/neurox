package llm

import "errors"

var (
	ErrLLMDisabled = errors.New("llm provider is disabled")
	ErrEmptyPrompt = errors.New("empty prompt")
)
