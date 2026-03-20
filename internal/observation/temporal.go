package observation

import "context"

// TemporalExtractor extracts and persists temporal mentions from observation content.
// Implementations should be safe to call asynchronously and must not block observation saving on failure.
type TemporalExtractor interface {
	// Extract parses temporal expressions from the observation content and persists them.
	// Returns the number of mentions extracted, or an error.
	Extract(ctx context.Context, observationID, content string) (int, error)
}
