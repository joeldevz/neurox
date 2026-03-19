package links

import (
	"fmt"
	"time"
)

type RelationType string

const (
	RelationSupersedes  RelationType = "supersedes"
	RelationContradicts RelationType = "contradicts"
	RelationRelatesTo   RelationType = "relates_to"
	RelationDerivedFrom RelationType = "derived_from"
	RelationValidates   RelationType = "validates"
	RelationRefines     RelationType = "refines"
)

func (r RelationType) Validate() error {
	switch r {
	case RelationSupersedes, RelationContradicts, RelationRelatesTo,
		RelationDerivedFrom, RelationValidates, RelationRefines:
		return nil
	default:
		return fmt.Errorf("invalid relation_type %q", r)
	}
}

type CreatedBy string

const (
	CreatedByAgent        CreatedBy = "agent"
	CreatedByConsolidator CreatedBy = "consolidator"
	CreatedByUser         CreatedBy = "user"
)

func (c CreatedBy) Validate() error {
	switch c {
	case CreatedByAgent, CreatedByConsolidator, CreatedByUser:
		return nil
	default:
		return fmt.Errorf("invalid created_by %q", c)
	}
}

const (
	DefaultCreatedBy  = CreatedByAgent
	DefaultConfidence = 1.0
	MaxTraverseDepth  = 5
)

type ObservationLink struct {
	ID           string
	SourceID     string
	TargetID     string
	RelationType RelationType
	Confidence   float64
	CreatedBy    CreatedBy
	CreatedAt    time.Time
}

type CreateLinkInput struct {
	SourceID     string
	TargetID     string
	RelationType RelationType
	Confidence   float64
	CreatedBy    CreatedBy
}

func (input *CreateLinkInput) ApplyDefaults() {
	if input.Confidence == 0 {
		input.Confidence = DefaultConfidence
	}
	if input.CreatedBy == "" {
		input.CreatedBy = DefaultCreatedBy
	}
}

func (input CreateLinkInput) Validate() error {
	if input.SourceID == "" {
		return fmt.Errorf("source_id is required")
	}
	if input.TargetID == "" {
		return fmt.Errorf("target_id is required")
	}
	if input.SourceID == input.TargetID {
		return fmt.Errorf("source_id and target_id must be different")
	}
	if err := input.RelationType.Validate(); err != nil {
		return err
	}
	if err := input.CreatedBy.Validate(); err != nil {
		return err
	}
	if input.Confidence < 0 || input.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	return nil
}

// TraversalResult holds a linked observation with the path that led to it.
type TraversalResult struct {
	ObservationID string
	Depth         int
	Path          []PathEntry
}

type PathEntry struct {
	LinkID       string
	RelationType RelationType
	FromID       string
	ToID         string
}
