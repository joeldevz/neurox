package llm

import (
	"context"
	"fmt"
	"strings"
)

// GateMode controls how the quality gate operates.
type GateMode string

const (
	GateModeAuto GateMode = "auto" // heuristic pre-filter + LLM for uncertain
	GateModeFull GateMode = "full" // everything through LLM
	GateModeOff  GateMode = "off"  // heuristics only, no LLM
)

// GateDecision is the LLM's verdict on an observation.
type GateDecision string

const (
	GateAdd    GateDecision = "ADD"
	GateUpdate GateDecision = "UPDATE"
	GateSkip   GateDecision = "SKIP"
)

// WriteGateInput holds the context for a write gate decision.
type WriteGateInput struct {
	NewTitle     string
	NewContent   string
	SimilarTitle string
	SimilarContent string
	Similarity   float64
}

// PromotionInput holds the context for a promotion decision.
type PromotionInput struct {
	Title           string
	Content         string
	ObservationType string
	Importance      float64
	AccessCount     int
	Layer           int
}

// SaveInput holds the context for a save gate decision.
type SaveInput struct {
	Title           string
	Content         string
	ObservationType string
}

// SaveDecision is the gate's verdict on whether to save.
type SaveDecision string

const (
	SaveAccept SaveDecision = "ACCEPT"
	SaveReject SaveDecision = "REJECT"
)

// PromotionDecision is the gate's verdict on promotion.
type PromotionDecision string

const (
	PromotionPromote PromotionDecision = "PROMOTE"
	PromotionReject  PromotionDecision = "REJECT"
	PromotionDefer   PromotionDecision = "DEFER"
)

// Gate uses an LLM to make quality decisions about observations.
type Gate struct {
	provider Provider
	mode     GateMode
}

// NewGate creates a quality gate. If provider is disabled and mode is not "off",
// it degrades to "off" mode automatically.
func NewGate(provider Provider, mode GateMode) *Gate {
	if mode == "" {
		mode = GateModeAuto
	}
	if !IsAvailable(provider) && mode != GateModeOff {
		mode = GateModeOff
	}
	return &Gate{
		provider: provider,
		mode:     mode,
	}
}

// Mode returns the current gate mode.
func (g *Gate) Mode() GateMode { return g.mode }

// SaveGateDecide evaluates whether an observation is worth saving at all.
// Returns ACCEPT or REJECT. Only called when gate mode is auto or full.
func (g *Gate) SaveGateDecide(ctx context.Context, input SaveInput) (SaveDecision, error) {
	if g.mode == GateModeOff {
		return SaveAccept, nil
	}

	prompt := fmt.Sprintf(`You are a memory quality filter for an AI coding agent. Decide if this observation is worth saving to persistent memory.

Title: %s
Content: %s
Type: %s

ACCEPT if it's a:
- Reusable technical insight, gotcha, or pattern
- User preference or workflow choice
- Architectural decision with rationale
- Bug root cause that could recur
- Configuration that's hard to rediscover

REJECT if it's:
- Temporary project state ("step 3 done", "working on X")
- A TODO or plan that will change
- Information already in code/git/docs
- Too vague to be useful later
- A single-use task description

Reply with exactly one word: ACCEPT or REJECT.`,
		input.Title, input.Content, input.ObservationType,
	)

	resp, err := g.provider.Complete(ctx, prompt)
	if err != nil {
		// On LLM failure, accept (don't lose data)
		return SaveAccept, nil
	}

	upper := strings.ToUpper(strings.TrimSpace(resp))
	if idx := strings.IndexAny(upper, " \n\t.,;"); idx > 0 {
		upper = upper[:idx]
	}
	if upper == "REJECT" {
		return SaveReject, nil
	}
	return SaveAccept, nil
}

// WriteGateDecide evaluates whether a new observation should be added, should
// update the similar one, or should be skipped. Called when cosine similarity
// is in the gray zone (0.75-0.92).
func (g *Gate) WriteGateDecide(ctx context.Context, input WriteGateInput) (GateDecision, error) {
	if g.mode == GateModeOff {
		// Heuristic fallback: similarity >= 0.85 → UPDATE, else ADD
		if input.Similarity >= 0.85 {
			return GateUpdate, nil
		}
		return GateAdd, nil
	}

	prompt := fmt.Sprintf(`You are a memory deduplication judge. Given a NEW observation and an EXISTING similar observation, decide:
- ADD: They are different enough to keep both
- UPDATE: The new one refines or replaces the existing one
- SKIP: The new one adds no value over the existing one

Cosine similarity: %.4f

EXISTING:
Title: %s
Content: %s

NEW:
Title: %s
Content: %s

Reply with exactly one word: ADD, UPDATE, or SKIP.`,
		input.Similarity,
		input.SimilarTitle, input.SimilarContent,
		input.NewTitle, input.NewContent,
	)

	resp, err := g.provider.Complete(ctx, prompt)
	if err != nil {
		// Degrade to heuristic on LLM failure
		if input.Similarity >= 0.85 {
			return GateUpdate, nil
		}
		return GateAdd, nil
	}

	return parseGateDecision(resp), nil
}

// PromotionDecide evaluates whether an observation deserves promotion to a higher layer.
func (g *Gate) PromotionDecide(ctx context.Context, input PromotionInput) (PromotionDecision, error) {
	if g.mode == GateModeOff {
		return PromotionPromote, nil
	}

	// In auto mode, pre-filter obvious cases
	if g.mode == GateModeAuto {
		// High importance + accessed → auto-promote
		if input.Importance >= 0.7 && input.AccessCount >= 3 {
			return PromotionPromote, nil
		}
		// Very low importance → auto-reject
		if input.Importance < 0.2 && input.AccessCount == 0 {
			return PromotionReject, nil
		}
	}

	prompt := fmt.Sprintf(`You are a strict memory consolidation judge for an AI coding agent's long-term memory. Only PROMOTE observations that will be valuable across MULTIPLE future conversations and projects.

Title: %s
Content: %s
Type: %s
Importance: %.2f
Access count: %d
Current layer: %d (0=Buffer, 1=Working, 2=Core)

REJECT if:
- It describes temporary project state (e.g. "step 5 done", "working on feature X")
- It's specific to one task/sprint that will be irrelevant next week
- It's a TODO or plan that will change
- It duplicates information already in code, git history, or docs

DEFER if:
- It might be useful but needs more access/validation first

PROMOTE only if:
- It's a reusable technical insight (gotcha, pattern, convention)
- It's a lasting user preference or workflow pattern
- It's a cross-project architectural decision
- It would save significant time if remembered in a future conversation

Reply with exactly one word: PROMOTE, REJECT, or DEFER.`,
		input.Title, input.Content, input.ObservationType,
		input.Importance, input.AccessCount, input.Layer,
	)

	resp, err := g.provider.Complete(ctx, prompt)
	if err != nil {
		return PromotionPromote, nil
	}

	return parsePromotionDecision(resp), nil
}

func parseGateDecision(resp string) GateDecision {
	upper := strings.ToUpper(strings.TrimSpace(resp))
	// Extract first word
	if idx := strings.IndexAny(upper, " \n\t.,;"); idx > 0 {
		upper = upper[:idx]
	}
	switch upper {
	case "ADD":
		return GateAdd
	case "UPDATE":
		return GateUpdate
	case "SKIP":
		return GateSkip
	default:
		return GateAdd // conservative default
	}
}

func parsePromotionDecision(resp string) PromotionDecision {
	upper := strings.ToUpper(strings.TrimSpace(resp))
	if idx := strings.IndexAny(upper, " \n\t.,;"); idx > 0 {
		upper = upper[:idx]
	}
	switch upper {
	case "PROMOTE":
		return PromotionPromote
	case "REJECT":
		return PromotionReject
	case "DEFER":
		return PromotionDefer
	default:
		return PromotionPromote
	}
}
