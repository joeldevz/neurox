package temporal

import "time"

// MentionKind classifies the type of temporal expression.
type MentionKind string

const (
	KindAbsolute     MentionKind = "absolute"
	KindRelative     MentionKind = "relative"
	KindCurrentState MentionKind = "current_state"
	KindDuration     MentionKind = "duration"
	KindRecurring    MentionKind = "recurring"
)

// Mention represents a temporal expression extracted from observation content.
type Mention struct {
	ID              string
	ObservationID   string
	RawText         string
	Kind            MentionKind
	NormalizedStart *time.Time
	NormalizedEnd   *time.Time
	AnchorTime      time.Time
	Confidence      float64
	CreatedAt       time.Time
}

// ParseResult is what the parser returns before persistence.
type ParseResult struct {
	RawText         string
	Kind            MentionKind
	NormalizedStart *time.Time
	NormalizedEnd   *time.Time
	Confidence      float64
}

// LatestTime returns the latest NormalizedStart across mentions, or nil if none have dates.
func LatestTime(mentions []Mention) *time.Time {
	var latest *time.Time
	for _, m := range mentions {
		if m.NormalizedStart != nil {
			if latest == nil || m.NormalizedStart.After(*latest) {
				t := *m.NormalizedStart
				latest = &t
			}
		}
	}
	return latest
}

// HasKind returns true if any mention has the given kind.
func HasKind(mentions []Mention, kind MentionKind) bool {
	for _, m := range mentions {
		if m.Kind == kind {
			return true
		}
	}
	return false
}
