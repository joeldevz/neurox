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

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name           string
		status         StrikeStatus
		rejectedEpoch  int
		currentEpoch   int
		want           bool
	}{
		{"no strike → always retry", StrikeNone, 0, 0, true},
		{"strike1 too soon", StrikeOne, 10, 20, false},
		{"strike1 ready", StrikeOne, 10, 60, true},
		{"strike1 exact threshold", StrikeOne, 10, 58, true},
		{"strike2 too soon", StrikeTwo, 10, 100, false},
		{"strike2 ready", StrikeTwo, 10, 160, true},
		{"final → never", StrikeFinal, 0, 10000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldRetry(tt.status, tt.rejectedEpoch, tt.currentEpoch)
			if got != tt.want {
				t.Errorf("ShouldRetry(%q, %d, %d) = %v, want %v",
					tt.status, tt.rejectedEpoch, tt.currentEpoch, got, tt.want)
			}
		})
	}
}

func TestIsFinal(t *testing.T) {
	if IsFinal(StrikeNone) {
		t.Error("StrikeNone should not be final")
	}
	if IsFinal(StrikeOne) {
		t.Error("StrikeOne should not be final")
	}
	if !IsFinal(StrikeFinal) {
		t.Error("StrikeFinal should be final")
	}
}
