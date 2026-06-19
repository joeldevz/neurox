package llm

import "testing"

func TestNextStrike(t *testing.T) {
	tests := []struct {
		current StrikeStatus
		want    StrikeStatus
	}{
		{StrikeNone, StrikeOne},
		{StrikeOne, StrikeTwo},
		{StrikeTwo, StrikeFinal},
		{StrikeFinal, StrikeFinal},
	}

	for _, tt := range tests {
		got := NextStrike(tt.current)
		if got != tt.want {
			t.Errorf("NextStrike(%q) = %q, want %q", tt.current, got, tt.want)
		}
	}
}
