package main

// FAMA (Forgetting-Aware Memory Accuracy) metrics
//
// FAMA penalizes when a system's response uses memory that has been
// invalidated or superseded. This is distinct from standard accuracy:
//
// - Standard Accuracy: Is the answer factually correct? (0 or 1)
// - FAMA Accuracy: Is the answer correct AND sourced from fresh memory?
//
// Example:
//   Session 1: "My favorite drink is coffee"
//   Session 2: "I stopped drinking coffee, I prefer tea now"
//   Question: "What is the user's favorite drink?"
//
//   System answer: "coffee" (using old observation)
//   Standard accuracy: FAIL (answer is incorrect)
//   FAMA accuracy: FAIL (answer uses stale memory)
//
//   System answer: "tea" (using new observation)
//   Standard accuracy: PASS (answer is correct)
//   FAMA accuracy: PASS (answer uses fresh memory)
//
//   System answer: "coffee" but observation marked as stale
//   Standard accuracy: FAIL (factually wrong)
//   FAMA accuracy: FAIL (uses invalidated memory)
//
// The FAMA gap is the percentage point difference between standard accuracy
// and FAMA accuracy, showing how much the system relies on stale/invalidated memory.

// EvaluationResult represents the evaluation of a single question
type EvaluationResult struct {
	QuestionID       string         `json:"question_id"`
	QuestionType     string         `json:"question_type"`
	Question         string         `json:"question"`
	StandardCorrect  bool           `json:"standard_correct"`
	FAMACorrect      bool           `json:"fama_correct"`
	GroundTruth      string         `json:"ground_truth"`
	RetrievedContent []Observation  `json:"retrieved_content"`
}

// Report summarizes the benchmark results
type Report struct {
	StandardAccuracy      map[string]*Accuracy `json:"standard_accuracy"`
	FAMAAccuracy          map[string]*Accuracy `json:"fama_accuracy"`
	FAMAGap               float64              `json:"fama_gap_percent"`
	StalenessDistribution map[string]float64   `json:"staleness_distribution"`
	EvaluatedQuestions    []*EvaluationResult  `json:"evaluated_questions"`
	Notes                 []string             `json:"notes"`
}
