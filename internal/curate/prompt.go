package curate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// promptObservation is the subset of fields sent to the LLM in the curation prompt.
type promptObservation struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Content     string  `json:"content"`
	Type        string  `json:"type"`
	Importance  float64 `json:"importance"`
	AccessCount int     `json:"access_count"`
	Tags        string  `json:"tags"`
}

// buildPrompt constructs the curation prompt from the given observations and priorities.
func buildPrompt(observations []observation, priorities []string) string {
	// Build priority lines.
	var priorityLines strings.Builder
	if len(priorities) == 0 {
		priorityLines.WriteString("- (none specified)\n")
	} else {
		for _, p := range priorities {
			fmt.Fprintf(&priorityLines, "- %s\n", p)
		}
	}

	// Serialize observations as a JSON array with only the fields the LLM needs.
	promptObs := make([]promptObservation, len(observations))
	for i, o := range observations {
		promptObs[i] = promptObservation{
			ID:          o.ID,
			Title:       o.Title,
			Content:     o.Content,
			Type:        o.ObservationType,
			Importance:  o.Importance,
			AccessCount: o.AccessCount,
			Tags:        o.Tags,
		}
	}

	obsJSON, err := json.Marshal(promptObs)
	if err != nil {
		// Fallback: use an empty array — this should never happen in practice.
		obsJSON = []byte("[]")
	}

	var sb strings.Builder
	sb.WriteString("You are a memory curator for an AI coding agent. Review ALL observations below and decide for each one: DELETE (noise/junk/duplicate) or KEEP (with recalibrated importance 0.0-1.0).\n\n")
	sb.WriteString("USER PRIORITIES (protect and boost observations matching these):\n")
	sb.WriteString(priorityLines.String())
	sb.WriteString("\nRULES:\n")
	sb.WriteString("- DELETE: step logs, plan status, \"successful build\", micro-implementation details, near-duplicates (keep the better one), empty/vague content, temporary state\n")
	sb.WriteString("- KEEP with HIGH importance (0.7-1.0): architectural decisions, user preferences, recurring patterns, reusable gotchas, cross-project insights\n")
	sb.WriteString("- KEEP with MEDIUM importance (0.4-0.7): useful technical details, specific bugfixes with root cause, configuration that's hard to rediscover\n")
	sb.WriteString("- KEEP with LOW importance (0.1-0.3): minor details that might be useful someday\n")
	sb.WriteString("- Observations matching user priorities should get importance >= 0.7\n")
	sb.WriteString("- Between near-duplicates, keep the one with better content and DELETE the other\n")
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "OBSERVATIONS (%d total):\n", len(observations))
	sb.Write(obsJSON)
	sb.WriteString("\n\nRespond with ONLY a JSON array. One entry per observation, no exceptions, no extra text:\n")
	sb.WriteString(`[{"id":"...","action":"DELETE"|"KEEP","new_importance":0.0-1.0,"reason":"brief reason"}]`)
	sb.WriteString("\n")

	return sb.String()
}
