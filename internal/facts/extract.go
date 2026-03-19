package facts

import (
	"context"
	"fmt"
	"strings"

	"neurox/internal/llm"
)

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
		_, err := e.store.Save(ctx, Fact{
			Subject:       t.subject,
			Predicate:     t.predicate,
			Object:        t.object,
			ObservationID: observationID,
			Namespace:     namespace,
		})
		if err != nil {
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

Examples:
- project | uses_framework | react
- auth_module | depends_on | jwt_library
- database | version | postgres_16

Output triples (one per line, pipe-separated):`, title, content)

	resp, err := e.llm.Complete(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return parseTriples(resp), nil
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
