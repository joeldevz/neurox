package contradiction

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"neurox/internal/embed"
	"neurox/internal/links"
	"neurox/internal/llm"
)

const (
	// Similarity range for potential contradictions: close enough to be about
	// the same topic, but not so close that they're duplicates.
	minContradictionSimilarity = 0.50
	maxContradictionSimilarity = 0.85

	// Max candidates to evaluate per run
	maxCandidatesPerRun = 20
)

// Detector finds and resolves contradicting observations.
type Detector struct {
	db        *sql.DB
	embedder  embed.Provider
	llm       llm.Provider
	linkStore *links.Store
}

// NewDetector creates a contradiction detector.
func NewDetector(db *sql.DB, embedder embed.Provider, llmProvider llm.Provider, linkStore *links.Store) *Detector {
	return &Detector{
		db:        db,
		embedder:  embedder,
		llm:       llmProvider,
		linkStore: linkStore,
	}
}

// DetectResult holds stats from a contradiction detection run.
type DetectResult struct {
	Candidates int
	Resolved   int
	Questions  int
}

// candidate holds a pair of potentially contradicting observations.
type candidate struct {
	newID      string
	newTitle   string
	newContent string
	oldID      string
	oldTitle   string
	oldContent string
	similarity float64
}

// Run scans recent observations for contradictions and resolves them.
// It focuses on Working-layer observations that haven't been checked yet.
func (d *Detector) Run(ctx context.Context) (DetectResult, error) {
	if !embed.IsAvailable(d.embedder) {
		return DetectResult{}, nil
	}

	candidates, err := d.findCandidates(ctx)
	if err != nil {
		return DetectResult{}, fmt.Errorf("find contradiction candidates: %w", err)
	}

	var result DetectResult
	result.Candidates = len(candidates)

	for _, c := range candidates {
		if llm.IsAvailable(d.llm) {
			resolved, err := d.resolveWithLLM(ctx, c)
			if err != nil {
				log.Printf("contradiction resolution failed for %s vs %s: %v", c.newID, c.oldID, err)
				continue
			}
			if resolved {
				result.Resolved++
			}
		} else {
			// Without LLM: create a question observation for human review
			if err := d.createQuestion(ctx, c); err != nil {
				log.Printf("create contradiction question failed: %v", err)
				continue
			}
			result.Questions++
		}
	}

	return result, nil
}

// findCandidates finds pairs of observations that might contradict each other.
// It looks at Working-layer observations and compares them against others in
// the same namespace using cosine similarity.
func (d *Detector) findCandidates(ctx context.Context) ([]candidate, error) {
	// Get Working/Core observations with embeddings, ordered by recency
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, title, content, namespace, embedding
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer >= 1
		  AND embedding IS NOT NULL
		  AND valid_until IS NULL
		ORDER BY created_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()

	type obs struct {
		id        string
		title     string
		content   string
		namespace string
		embedding []float32
	}

	var observations []obs
	for rows.Next() {
		var o obs
		var blob []byte
		if err := rows.Scan(&o.id, &o.title, &o.content, &o.namespace, &blob); err != nil {
			continue
		}
		o.embedding = embed.DeserializeF32(blob)
		if o.embedding != nil {
			observations = append(observations, o)
		}
	}

	if len(observations) < 2 {
		return nil, nil
	}

	// Find pairs in the contradiction similarity range
	var candidates []candidate
	checked := make(map[string]bool)

	for i := 0; i < len(observations) && len(candidates) < maxCandidatesPerRun; i++ {
		for j := i + 1; j < len(observations); j++ {
			if observations[i].namespace != observations[j].namespace {
				continue
			}

			pairKey := observations[i].id + ":" + observations[j].id
			if checked[pairKey] {
				continue
			}
			checked[pairKey] = true

			sim := embed.CosineSimilarity(observations[i].embedding, observations[j].embedding)
			if sim >= minContradictionSimilarity && sim <= maxContradictionSimilarity {
				// Check if there's already a link between them
				existing, _ := d.linkStore.GetBySource(ctx, observations[i].id)
				alreadyLinked := false
				for _, link := range existing {
					if link.TargetID == observations[j].id {
						alreadyLinked = true
						break
					}
				}
				if alreadyLinked {
					continue
				}

				// Newer observation is "new", older is "old"
				candidates = append(candidates, candidate{
					newID:      observations[i].id,
					newTitle:   observations[i].title,
					newContent: observations[i].content,
					oldID:      observations[j].id,
					oldTitle:   observations[j].title,
					oldContent: observations[j].content,
					similarity: sim,
				})
			}

			if len(candidates) >= maxCandidatesPerRun {
				break
			}
		}
	}

	return candidates, nil
}

// resolveWithLLM asks the LLM to determine if two observations contradict
// each other. If they do, the older one is marked with valid_until and a
// supersedes link is created.
func (d *Detector) resolveWithLLM(ctx context.Context, c candidate) (bool, error) {
	prompt := fmt.Sprintf(`You are a memory consistency checker. Two observations from the same project might contradict each other. Determine if they do.

OBSERVATION A (newer):
Title: %s
Content: %s

OBSERVATION B (older):
Title: %s
Content: %s

Do these observations contradict each other? Consider:
- Does A make B outdated or incorrect?
- Do they state conflicting facts about the same thing?
- Is A an update/correction of B?

Reply with exactly one word: YES or NO.`,
		c.newTitle, c.newContent,
		c.oldTitle, c.oldContent,
	)

	resp, err := d.llm.Complete(ctx, prompt)
	if err != nil {
		return false, fmt.Errorf("llm contradiction check: %w", err)
	}

	if !isYes(resp) {
		return false, nil
	}

	// Mark the old observation as superseded
	return true, d.markSuperseded(ctx, c.newID, c.oldID)
}

// markSuperseded marks the old observation with valid_until, sets invalidated_by
// to the new observation, and creates a supersedes link.
func (d *Detector) markSuperseded(ctx context.Context, newID, oldID string) error {
	// Mark old observation as superseded
	_, err := d.db.ExecContext(ctx, `
		UPDATE observations
		SET valid_until = datetime('now'),
		    invalidated_by = ?,
		    staleness = 'expired',
		    updated_at = datetime('now'),
		    modified_epoch = modified_epoch + 1
		WHERE id = ? AND deleted_at IS NULL
	`, newID, oldID)
	if err != nil {
		return fmt.Errorf("mark superseded: %w", err)
	}

	// Create supersedes link: new → old
	_, err = d.linkStore.Create(ctx, links.CreateLinkInput{
		SourceID:     newID,
		TargetID:     oldID,
		RelationType: links.RelationSupersedes,
		Confidence:   1.0,
		CreatedBy:    links.CreatedByConsolidator,
	})
	if err != nil {
		return fmt.Errorf("create supersedes link: %w", err)
	}

	log.Printf("contradiction resolved: %s supersedes %s", newID, oldID)
	return nil
}

// createQuestion creates a 'question' observation for human review when LLM
// is not available. Also creates a contradicts link.
func (d *Detector) createQuestion(ctx context.Context, c candidate) error {
	questionContent := fmt.Sprintf(`Potential contradiction detected between observations:

Observation A (newer, %s): %s
  %s

Observation B (older, %s): %s
  %s

Cosine similarity: %.4f

Please review and resolve: use the invalidate tool to mark the outdated observation.`,
		c.newID, c.newTitle, c.newContent,
		c.oldID, c.oldTitle, c.oldContent,
		c.similarity,
	)

	// Get namespace from the newer observation
	var namespace string
	err := d.db.QueryRowContext(ctx, "SELECT namespace FROM observations WHERE id = ?", c.newID).Scan(&namespace)
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}

	// Insert the question observation
	questionID := d.linkStore.IDGen()
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
		VALUES(?, ?, ?, 'question', 0, 0.5, 0.7, 'semantic', ?, 'consolidator')
	`, questionID,
		fmt.Sprintf("Possible contradiction: %s vs %s", c.newTitle, c.oldTitle),
		questionContent,
		namespace,
	)
	if err != nil {
		return fmt.Errorf("insert question: %w", err)
	}

	// Create contradicts link between the two
	_, err = d.linkStore.Create(ctx, links.CreateLinkInput{
		SourceID:     c.newID,
		TargetID:     c.oldID,
		RelationType: links.RelationContradicts,
		Confidence:   0.7,
		CreatedBy:    links.CreatedByConsolidator,
	})
	if err != nil {
		// Non-fatal: link might already exist
		log.Printf("create contradicts link: %v", err)
	}

	return nil
}

// isYes parses LLM response for a yes/no answer.
func isYes(resp string) bool {
	if len(resp) == 0 {
		return false
	}
	// Take first word, uppercase
	word := resp
	for i, c := range resp {
		if c == ' ' || c == '\n' || c == '\t' || c == '.' || c == ',' {
			word = resp[:i]
			break
		}
	}
	switch {
	case len(word) >= 3 && (word[:3] == "YES" || word[:3] == "yes" || word[:3] == "Yes"):
		return true
	default:
		return false
	}
}
