package llm

import (
	"context"
	"testing"
)

// mockProvider is a test LLM that returns canned responses.
type mockProvider struct {
	response string
	err      error
}

func (m *mockProvider) Complete(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

func (m *mockProvider) Name() string { return "mock" }

func TestGateDisabledMode(t *testing.T) {
	gate := NewGate(Disabled{}, GateModeAuto)
	// Should degrade to off
	if gate.Mode() != GateModeOff {
		t.Errorf("mode = %q, want %q", gate.Mode(), GateModeOff)
	}
}

func TestGateWriteDecisionOff(t *testing.T) {
	gate := NewGate(Disabled{}, GateModeOff)
	ctx := context.Background()

	tests := []struct {
		name       string
		similarity float64
		want       GateDecision
	}{
		{"high similarity → UPDATE", 0.90, GateUpdate},
		{"low similarity → ADD", 0.78, GateAdd},
		{"borderline → ADD", 0.84, GateAdd},
		{"exact threshold → UPDATE", 0.85, GateUpdate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := gate.WriteGateDecide(ctx, WriteGateInput{
				NewTitle:       "new",
				NewContent:     "content",
				SimilarTitle:   "existing",
				SimilarContent: "content",
				Similarity:     tt.similarity,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if decision != tt.want {
				t.Errorf("decision = %q, want %q", decision, tt.want)
			}
		})
	}
}

func TestGateWriteDecisionWithLLM(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     GateDecision
	}{
		{"ADD response", "ADD", GateAdd},
		{"UPDATE response", "UPDATE", GateUpdate},
		{"SKIP response", "SKIP", GateSkip},
		{"lowercase add", "add", GateAdd},
		{"with explanation", "UPDATE - they are similar", GateUpdate},
		{"unknown → ADD default", "MAYBE", GateAdd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockProvider{response: tt.response}
			gate := NewGate(mock, GateModeAuto)
			decision, err := gate.WriteGateDecide(context.Background(), WriteGateInput{
				NewTitle:       "new",
				NewContent:     "content",
				SimilarTitle:   "existing",
				SimilarContent: "content",
				Similarity:     0.80,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if decision != tt.want {
				t.Errorf("decision = %q, want %q", decision, tt.want)
			}
		})
	}
}

func TestGatePromotionDecisionAutoPrefilter(t *testing.T) {
	mock := &mockProvider{response: "PROMOTE"}
	gate := NewGate(mock, GateModeAuto)
	ctx := context.Background()

	// High importance + accessed → auto-promote without LLM
	decision, err := gate.PromotionDecide(ctx, PromotionInput{
		Title:       "Important decision",
		Content:     "We chose X over Y",
		Importance:  0.8,
		AccessCount: 5,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if decision != PromotionPromote {
		t.Errorf("decision = %q, want PROMOTE", decision)
	}

	// Very low importance → auto-reject without LLM
	decision, err = gate.PromotionDecide(ctx, PromotionInput{
		Title:       "Typo fix",
		Content:     "Fixed a typo",
		Importance:  0.1,
		AccessCount: 0,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if decision != PromotionReject {
		t.Errorf("decision = %q, want REJECT", decision)
	}
}

func TestGatePromotionDecisionWithLLM(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     PromotionDecision
	}{
		{"PROMOTE", "PROMOTE", PromotionPromote},
		{"REJECT", "REJECT", PromotionReject},
		{"DEFER", "DEFER", PromotionDefer},
		{"unknown → PROMOTE default", "MAYBE", PromotionPromote},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockProvider{response: tt.response}
			gate := NewGate(mock, GateModeFull)
			decision, err := gate.PromotionDecide(context.Background(), PromotionInput{
				Title:       "Some observation",
				Content:     "Content here",
				Importance:  0.5,
				AccessCount: 2,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if decision != tt.want {
				t.Errorf("decision = %q, want %q", decision, tt.want)
			}
		})
	}
}
