package classify

import (
	"regexp"
	"strings"

	"github.com/joeldevz/neurox/internal/observation"
)

// stepPattern matches common step/plan execution titles.
var stepPattern = regexp.MustCompile(`(?i)^(implement\s+step|step\s+\d|plan\s+completed|plan\s+status|build\s+flags|file\s+observations|embeddings?\s|TestToolsList|tracker\s+wiring|fixing\s+schema|renamed\s+local|named\s+return|queue\s+imported|embed\s+mocking|update\s+newpipeline|llm\s+availability)`)

// InferRetention classifies an observation as operational or durable based
// on heuristics applied to its metadata. Callers should only use this when
// the user/agent has not explicitly provided a retention value.
func InferRetention(title string, obsType observation.ObservationType, source string) observation.Retention {
	// 1. Consolidator output is always operational.
	if source == "consolidator" {
		return observation.RetentionOperational
	}

	// 2. Reflections are durable (subject to quality check elsewhere).
	if source == "reflection" {
		return observation.RetentionDurable
	}

	// 3. Title matches step/plan execution patterns.
	if stepPattern.MatchString(strings.TrimSpace(title)) {
		return observation.RetentionOperational
	}

	// 4. High-signal observation types are durable.
	switch obsType {
	case observation.ObservationTypeDecision,
		observation.ObservationTypeGotcha,
		observation.ObservationTypePattern,
		observation.ObservationTypePreference:
		return observation.RetentionDurable
	}

	// 5. Other types default to durable.
	return observation.RetentionDurable
}
