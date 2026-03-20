package temporal

import (
	"context"
	"time"
)

// Extractor implements observation.TemporalExtractor by combining the Parser and Store.
type Extractor struct {
	parser *Parser
	store  *Store
}

// NewExtractor creates a temporal extractor that parses and persists mentions.
func NewExtractor(parser *Parser, store *Store) *Extractor {
	return &Extractor{parser: parser, store: store}
}

// Extract parses temporal expressions from content and persists them linked to the observation.
func (e *Extractor) Extract(ctx context.Context, observationID, content string) (int, error) {
	anchor := time.Now().UTC()
	results := e.parser.Parse(content, anchor)
	if len(results) == 0 {
		return 0, nil
	}

	mentions, err := e.store.SaveAll(ctx, observationID, results, anchor)
	if err != nil {
		return len(mentions), err
	}
	return len(mentions), nil
}
