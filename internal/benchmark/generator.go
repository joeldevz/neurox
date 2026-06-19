package benchmark

import (
	"fmt"

	"github.com/joeldevz/neurox/internal/observation"
)

// GenerateNoise returns n operational noise observations in the given namespace.
// These simulate ephemeral step logs, build output, etc.
func GenerateNoise(n int, namespace string) []observation.Observation {
	ns := namespace
	if ns == "" {
		ns = "benchmark"
	}

	out := make([]observation.Observation, 0, n)
	for i := range n {
		out = append(out, observation.Observation{
			Title: fmt.Sprintf("Build step %d completed", i+1),
			Content: fmt.Sprintf(
				"Step %d: compiled 42 packages in 1.2s. No errors. Output written to dist/.",
				i+1,
			),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindEpisodic,
			Tags:            []string{"build", "ci", "noise"},
			Namespace:       ns,
			Retention:       observation.RetentionOperational,
			Confidence:      0.5,
		})
	}
	return out
}
