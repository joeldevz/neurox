package classify

import (
	"testing"

	"neurox/internal/observation"
)

func TestInferRetention(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		obsType observation.ObservationType
		source  string
		want    observation.Retention
	}{
		{
			name:    "consolidator source is operational",
			title:   "Some title",
			obsType: observation.ObservationTypeDiscovery,
			source:  "consolidator",
			want:    observation.RetentionOperational,
		},
		{
			name:    "reflection source is durable",
			title:   "Reflection: neurox",
			obsType: observation.ObservationTypePattern,
			source:  "reflection",
			want:    observation.RetentionDurable,
		},
		{
			name:    "step title pattern is operational",
			title:   "Implement Step 3",
			obsType: observation.ObservationTypeDiscovery,
			source:  "",
			want:    observation.RetentionOperational,
		},
		{
			name:    "step N title is operational",
			title:   "Step 4: Wire dependencies",
			obsType: observation.ObservationTypeDiscovery,
			source:  "",
			want:    observation.RetentionOperational,
		},
		{
			name:    "plan completed is operational",
			title:   "Plan completed successfully",
			obsType: observation.ObservationTypeDiscovery,
			source:  "",
			want:    observation.RetentionOperational,
		},
		{
			name:    "build flags is operational",
			title:   "Build flags verified",
			obsType: observation.ObservationTypeConfig,
			source:  "consolidator",
			want:    observation.RetentionOperational,
		},
		{
			name:    "decision type is durable",
			title:   "Use SQLite for storage",
			obsType: observation.ObservationTypeDecision,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "gotcha type is durable",
			title:   "FTS5 requires build tag",
			obsType: observation.ObservationTypeGotcha,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "pattern type is durable",
			title:   "Store pattern for domain packages",
			obsType: observation.ObservationTypePattern,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "preference type is durable",
			title:   "User prefers autonomous execution",
			obsType: observation.ObservationTypePreference,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "bugfix type defaults to durable",
			title:   "Fixed nil pointer in recall",
			obsType: observation.ObservationTypeBugfix,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "discovery type defaults to durable",
			title:   "Project uses ULID for IDs",
			obsType: observation.ObservationTypeDiscovery,
			source:  "",
			want:    observation.RetentionDurable,
		},
		{
			name:    "TestToolsList consolidator output is operational",
			title:   "TestToolsList count update",
			obsType: observation.ObservationTypeBugfix,
			source:  "consolidator",
			want:    observation.RetentionOperational,
		},
		{
			name:    "tracker wiring is operational",
			title:   "Tracker Wiring in main.go",
			obsType: observation.ObservationTypePattern,
			source:  "",
			want:    observation.RetentionOperational,
		},
		{
			name:    "empty source with normal title is durable",
			title:   "Architecture overview",
			obsType: observation.ObservationTypeDiscovery,
			source:  "",
			want:    observation.RetentionDurable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := InferRetention(tc.title, tc.obsType, tc.source)
			if got != tc.want {
				t.Errorf("InferRetention(%q, %q, %q) = %q, want %q",
					tc.title, tc.obsType, tc.source, got, tc.want)
			}
		})
	}
}
