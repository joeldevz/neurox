package llm

import (
	"regexp"
	"strings"
)

var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// stripThinkTags removes <think>...</think> blocks from LLM responses.
// Some models (e.g. qwen) include reasoning traces that shouldn't be stored.
func stripThinkTags(s string) string {
	cleaned := thinkTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(cleaned)
}
