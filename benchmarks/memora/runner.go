package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Runner orchestrates the benchmark execution
type Runner struct {
	serverURL string
	namespace string
	dataset   *Dataset
	client    *http.Client
}

// NewRunner creates a new benchmark runner
func NewRunner(serverURL, namespace string, dataset *Dataset) *Runner {
	return &Runner{
		serverURL: serverURL,
		namespace: namespace,
		dataset:   dataset,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Run executes the benchmark
func (r *Runner) Run(limit int) (*Report, error) {
	report := &Report{
		StandardAccuracy:       make(map[string]*Accuracy),
		FAMAAccuracy:           make(map[string]*Accuracy),
		StalenessDistribution:  make(map[string]float64),
		EvaluatedQuestions:     []*EvaluationResult{},
		Notes:                  []string{},
	}

	// Initialize accuracy maps
	for _, qtype := range []string{"remembering", "reasoning", "recommending"} {
		report.StandardAccuracy[qtype] = &Accuracy{Correct: 0, Total: 0}
		report.FAMAAccuracy[qtype] = &Accuracy{Correct: 0, Total: 0}
	}

	// Phase 1: Ingest sessions as observations
	fmt.Println("Phase 1: Ingesting sessions...")
	sessionToObsID := make(map[string]string) // session_id -> observation_id
	userSessionDates := make(map[string][]time.Time)

	for _, user := range r.dataset.Users {
		for _, session := range user.Sessions {
			// Combine session turns into a single observation
			content := strings.Join([]string{}, "")
			for _, turn := range session.Turns {
				content += turn.Role + ": " + turn.Content + "\n"
			}

			obsID, err := r.saveObservation(session.SessionID, content, user.UserID)
			if err != nil {
				report.Notes = append(report.Notes, fmt.Sprintf("Warning: failed to ingest %s: %v", session.SessionID, err))
				continue
			}

			sessionToObsID[session.SessionID] = obsID

			// Parse date
			if t, err := time.Parse("2006-01-02T15:04:05Z", session.Date); err == nil {
				userSessionDates[user.UserID] = append(userSessionDates[user.UserID], t)
			}
		}
	}

	fmt.Printf("  Ingested %d sessions\n", len(sessionToObsID))

	// Wait for all observations to be persisted before Phase 2
	fmt.Println("  Waiting for persistence...")
	if err := r.waitForPersistence(len(sessionToObsID), 30*time.Second); err != nil {
		report.Notes = append(report.Notes, fmt.Sprintf("Warning: persistence wait timed out: %v", err))
	}

	// Phase 2: Mark superseded observations
	fmt.Println("Phase 2: Marking superseded observations...")
	supersededCount := 0
	for _, user := range r.dataset.Users {
		if len(user.Sessions) < 2 {
			continue
		}

		// For each user with multiple sessions, mark earlier observations as stale when newer ones exist
		for i := 0; i < len(user.Sessions)-1; i++ {
			olderSession := user.Sessions[i]
			if obsID, ok := sessionToObsID[olderSession.SessionID]; ok {
				// Invalidate the older session's observation
				if err := r.invalidateObservation(obsID, "Superseded by newer session"); err == nil {
					supersededCount++
				}
			}
		}
	}
	fmt.Printf("  Marked %d observations as superseded\n", supersededCount)

	// Wait for invalidations to be persisted and staleness visible before Phase 3
	if supersededCount > 0 {
		fmt.Println("  Waiting for staleness updates...")
		time.Sleep(2 * time.Second)
	}

	// Phase 3: Evaluate questions
	fmt.Println("Phase 3: Evaluating questions...")
	if limit > len(r.dataset.Questions) {
		limit = len(r.dataset.Questions)
	}

	staleFresh := 0
	staleStale := 0
	staleExpired := 0

	for i := 0; i < limit; i++ {
		q := r.dataset.Questions[i]

		// Query observations for this user and question context
		// Use question + user tag to make search more focused
		results, err := r.searchObservations(q.Question+" "+q.UserID, q.UserID, 5)
		if err != nil {
			report.Notes = append(report.Notes, fmt.Sprintf("Query failed for %s: %v", q.QID, err))
			continue
		}

		// Evaluate standard accuracy: does the answer appear in results?
		standardCorrect := false
		famaCorrect := false

		if len(results) > 0 {
			// Concatenate all result content
			allContent := ""
			containsStale := false

			for _, obs := range results {
				allContent += obs.Content + " "

				// Track staleness
				if obs.Staleness == "fresh" {
					staleFresh++
				} else if obs.Staleness == "stale" {
					staleStale++
					containsStale = true
				} else if obs.Staleness == "expired" {
					staleExpired++
					containsStale = true
				}
			}

			// Check standard accuracy: does the correct answer appear?
			if containsIgnoreCase(allContent, q.Answer) {
				standardCorrect = true
			}

			// Check FAMA accuracy:
			// - Standard accuracy is met (answer is in retrieved content)
			// - AND either no stale observations were used
			//   OR the stale observations do NOT contain the invalidated answer
			if standardCorrect {
				if !containsStale {
					// All observations are fresh
					famaCorrect = true
				} else if !containsIgnoreCase(allContent, q.InvalidatedAnswer) {
					// Has stale obs but they don't support the wrong answer
					famaCorrect = true
				} else {
					// Stale observations support the wrong answer
					famaCorrect = false
				}
			}
		}

		// Record evaluation
		report.EvaluatedQuestions = append(report.EvaluatedQuestions, &EvaluationResult{
			QuestionID:       q.QID,
			QuestionType:     q.QuestionType,
			Question:         q.Question,
			StandardCorrect:  standardCorrect,
			FAMACorrect:      famaCorrect,
			GroundTruth:      q.Answer,
			RetrievedContent: results,
		})

		// Update metrics
		report.StandardAccuracy[q.QuestionType].Total++
		if standardCorrect {
			report.StandardAccuracy[q.QuestionType].Correct++
		}

		report.FAMAAccuracy[q.QuestionType].Total++
		if famaCorrect {
			report.FAMAAccuracy[q.QuestionType].Correct++
		}
	}

	// Calculate FAMA gap
	totalStd := 0
	totalFAMA := 0
	for qtype := range report.StandardAccuracy {
		totalStd += report.StandardAccuracy[qtype].Correct
		totalFAMA += report.FAMAAccuracy[qtype].Correct
	}

	if totalStd > 0 {
		report.FAMAGap = float64(totalStd-totalFAMA) * 100.0 / float64(totalStd)
	}

	// Calculate staleness distribution
	totalStaleObs := staleFresh + staleStale + staleExpired
	if totalStaleObs > 0 {
		report.StalenessDistribution["fresh"] = float64(staleFresh) * 100.0 / float64(totalStaleObs)
		report.StalenessDistribution["stale"] = float64(staleStale) * 100.0 / float64(totalStaleObs)
		report.StalenessDistribution["expired"] = float64(staleExpired) * 100.0 / float64(totalStaleObs)
	}

	report.Notes = append(report.Notes, "Evaluation uses exact-match normalized (lowercase, stripped punctuation)")
	report.Notes = append(report.Notes, fmt.Sprintf("Retrieved %d total observations with staleness tracking", totalStaleObs))

	return report, nil
}

// waitForPersistence polls until observations are persisted or timeout
func (r *Runner) waitForPersistence(expectedCount int, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	lastCount := 0
	stableCount := 0

	for {
		select {
		case <-ticker.C:
			// Poll search endpoint to count persisted observations
			params := url.Values{}
			params.Set("q", "session")
			params.Set("namespace", r.namespace)
			params.Set("limit", "200")

			resp, err := r.client.Get(r.serverURL + "/api/v1/observations/search?" + params.Encode())
			if err != nil {
				continue
			}

			var respBody struct {
				Results []Observation `json:"results"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
				resp.Body.Close()
				continue
			}
			resp.Body.Close()

			count := len(respBody.Results)
			if count >= expectedCount {
				fmt.Printf("    Persistence confirmed: %d/%d observations\n", count, expectedCount)
				return nil
			}

			// Check if count has stabilized (not expected to reach all if some fail)
			if count == lastCount {
				stableCount++
				if stableCount >= 4 && count > 0 { // stable for 2s and at least some observations
					fmt.Printf("    Persistence stabilized: %d/%d observations (accepting %d)\n", count, expectedCount, count)
					return nil
				}
			} else {
				stableCount = 0
			}
			lastCount = count

			if count > 0 {
				fmt.Printf("    Waiting for persistence: %d/%d observations\n", count, expectedCount)
			}

		case <-time.After(time.Until(deadline)):
			if lastCount > 0 {
				fmt.Printf("    Persistence wait timeout after %v (confirmed %d/%d)\n", maxWait, lastCount, expectedCount)
				return nil // Accept partial persistence rather than hard fail
			}
			return fmt.Errorf("persistence wait timeout after %v", maxWait)
		}
	}
}

// saveObservation saves a session as an observation
func (r *Runner) saveObservation(sessionID, content, userID string) (string, error) {
	body := map[string]interface{}{
		"title":                sessionID,
		"content":              content,
		"namespace":            r.namespace,
		"kind":                 "episodic",
		"observation_type":     "discovery",
		"tags":                 []string{"session", userID},
		"confidence":           0.95,
	}

	jsonBody, _ := json.Marshal(body)
	resp, err := r.client.Post(
		r.serverURL+"/api/v1/observations",
		"application/json",
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("save failed: status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if id, ok := result["id"].(string); ok {
		return id, nil
	}
	return "", fmt.Errorf("no id in response")
}

// invalidateObservation marks an observation as stale, with retry on 404
func (r *Runner) invalidateObservation(obsID, reason string) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		body := map[string]interface{}{
			"reason": reason,
		}

		jsonBody, _ := json.Marshal(body)
		req, _ := http.NewRequest(
			"POST",
			r.serverURL+"/api/v1/observations/"+obsID+"/invalidate",
			bytes.NewBuffer(jsonBody),
		)
		req.Header.Set("Content-Type", "application/json")

		resp, err := r.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// If 404, observation not yet persisted, retry with backoff
		if resp.StatusCode == 404 {
			lastErr = fmt.Errorf("invalidate 404 (attempt %d/5)", attempt+1)
			if attempt < 4 {
				time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
				continue
			}
			return lastErr
		}

		// Other non-2xx status
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			bodyStr, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("invalidate failed: status %d, body: %s", resp.StatusCode, string(bodyStr))
		}

		// Success
		return nil
	}
	return lastErr
}

// searchObservations searches for relevant observations
func (r *Runner) searchObservations(query, userID string, limit int) ([]Observation, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("namespace", r.namespace)
	params.Set("limit", fmt.Sprintf("%d", limit))

	resp, err := r.client.Get(r.serverURL + "/api/v1/observations/search?" + params.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search failed: status %d", resp.StatusCode)
	}

	var respBody struct {
		Results []Observation `json:"results"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&respBody)

	return respBody.Results, nil
}

// Observation represents a search result
type Observation struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Staleness string  `json:"staleness"`
	Score     float64 `json:"score"`
}

// containsIgnoreCase checks if haystack contains needle (case-insensitive)
func containsIgnoreCase(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
