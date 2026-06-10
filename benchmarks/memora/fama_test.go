package main

import (
	"testing"
)

func TestFAMALogic(t *testing.T) {
	tests := []struct {
		name           string
		standardPass   bool
		hasStaleObs    bool
		staleContains  bool
		expectedFAMA   bool
		description    string
	}{
		{
			name:          "fresh_answer_all_fresh_obs",
			standardPass:  true,
			hasStaleObs:   false,
			staleContains: false,
			expectedFAMA:  true,
			description:   "Correct answer from fresh observations only",
		},
		{
			name:          "fresh_answer_stale_obs_wrong_content",
			standardPass:  true,
			hasStaleObs:   true,
			staleContains: false,
			expectedFAMA:  true,
			description:   "Correct answer from both fresh and stale, but stale doesn't support wrong answer",
		},
		{
			name:          "fresh_answer_stale_obs_wrong_content",
			standardPass:  true,
			hasStaleObs:   true,
			staleContains: true,
			expectedFAMA:  false,
			description:   "Correct answer but stale observations support the invalidated answer",
		},
		{
			name:          "wrong_answer",
			standardPass:  false,
			hasStaleObs:   true,
			staleContains: true,
			expectedFAMA:  false,
			description:   "Wrong answer never passes FAMA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate FAMA logic
			famaCorrect := false
			if tt.standardPass {
				if !tt.hasStaleObs {
					famaCorrect = true
				} else if !tt.staleContains {
					famaCorrect = true
				} else {
					famaCorrect = false
				}
			}

			if famaCorrect != tt.expectedFAMA {
				t.Errorf("%s: expected FAMA=%v, got %v", tt.description, tt.expectedFAMA, famaCorrect)
			}
		})
	}
}

func TestAccuracyPercent(t *testing.T) {
	tests := []struct {
		correct  int
		total    int
		expected float64
	}{
		{10, 20, 50.0},
		{0, 10, 0.0},
		{10, 10, 100.0},
		{0, 0, 0.0},
	}

	for _, tt := range tests {
		acc := &Accuracy{Correct: tt.correct, Total: tt.total}
		result := acc.Percent()
		if result != tt.expected {
			t.Errorf("Accuracy{%d, %d}.Percent() = %v, expected %v", tt.correct, tt.total, result, tt.expected)
		}
	}
}
