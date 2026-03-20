package facts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"neurox/internal/llm"
)

// temporalPredicates are predicates where the object is expected to be a date.
var temporalPredicates = map[string]bool{
	"happened_on": true,
	"started_on":  true,
	"ended_on":    true,
	"changed_on":  true,
}

// Extractor extracts knowledge triples from observation text using an LLM.
type Extractor struct {
	llm   llm.Provider
	store *Store
}

// NewExtractor creates a fact extractor.
func NewExtractor(llmProvider llm.Provider, store *Store) *Extractor {
	return &Extractor{llm: llmProvider, store: store}
}

// ExtractAndSave extracts facts from an observation and saves them.
// Returns the number of facts extracted. If LLM is not available, returns 0.
func (e *Extractor) ExtractAndSave(ctx context.Context, observationID, title, content, namespace string) (int, error) {
	if !llm.IsAvailable(e.llm) {
		return 0, nil
	}

	triples, err := e.extract(ctx, title, content)
	if err != nil {
		return 0, fmt.Errorf("extract facts: %w", err)
	}

	saved := 0
	for _, t := range triples {
		f := Fact{
			Subject:       t.subject,
			Predicate:     t.predicate,
			Object:        t.object,
			ObservationID: observationID,
			Namespace:     namespace,
		}

		var saveErr error
		if temporalPredicates[t.predicate] {
			if parsed, ok := parseTemporalObject(t.object); ok {
				_, saveErr = e.store.SaveWithValidFrom(ctx, f, parsed)
			} else {
				_, saveErr = e.store.Save(ctx, f)
			}
		} else {
			_, saveErr = e.store.Save(ctx, f)
		}
		if saveErr != nil {
			continue
		}
		saved++
	}

	return saved, nil
}

type triple struct {
	subject   string
	predicate string
	object    string
}

func (e *Extractor) extract(ctx context.Context, title, content string) ([]triple, error) {
	prompt := fmt.Sprintf(`Extract knowledge facts from this observation as subject-predicate-object triples.

Title: %s
Content: %s

Rules:
- Extract only clear, factual statements
- Use simple, normalized terms (lowercase, no articles)
- Each line: subject | predicate | object
- Max 5 triples
- If no clear facts, output NONE
- For temporal facts use these predicates:
  - happened_on: for events with a date (e.g. migration | happened_on | 2026-03-06)
  - started_on: when something began (e.g. project | started_on | 2026-01)
  - current: for present-state facts (e.g. database | current | sqlite)
  - changed_to: when something was replaced (e.g. auth | changed_to | jwt)
- Dates in object should use ISO format (YYYY-MM-DD or YYYY-MM) when possible

Examples:
- project | uses_framework | react
- auth_module | depends_on | jwt_library
- database | current | sqlite
- migration | happened_on | 2026-03-06
- auth_system | changed_to | oauth2

Output triples (one per line, pipe-separated):`, title, content)

	resp, err := e.llm.Complete(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return parseTriples(resp), nil
}

// parseTemporalObject tries to parse an ISO date from a triple's object value.
func parseTemporalObject(object string) (time.Time, bool) {
	layouts := []string{"2006-01-02", "2006-01"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, strings.TrimSpace(object)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseTriples(response string) []triple {
	if strings.TrimSpace(response) == "" || strings.Contains(strings.ToUpper(response), "NONE") {
		return nil
	}

	var triples []triple
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Remove leading bullet/dash
		line = strings.TrimLeft(line, "- •*")
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}

		subject := strings.TrimSpace(parts[0])
		predicate := strings.TrimSpace(parts[1])
		object := strings.TrimSpace(parts[2])

		if subject == "" || predicate == "" || object == "" {
			continue
		}

		triples = append(triples, triple{
			subject:   subject,
			predicate: predicate,
			object:    object,
		})

		if len(triples) >= 5 {
			break
		}
	}

	return triples
}
