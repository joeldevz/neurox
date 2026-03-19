package facts

import "time"

// Fact represents a knowledge triple (subject-predicate-object) with temporal validity.
type Fact struct {
	ID            string
	Subject       string
	Predicate     string
	Object        string
	ObservationID string
	Namespace     string
	ValidFrom     time.Time
	ValidUntil    *time.Time
	SupersededBy  string
	CreatedAt     time.Time
}

// TraversalResult holds a fact found during graph traversal.
type TraversalResult struct {
	Fact  Fact
	Depth int
}
