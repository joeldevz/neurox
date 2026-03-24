package recall

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/temporal"
)

// TemporalIntentKind classifies the temporal nature of a recall query.
type TemporalIntentKind string

const (
	IntentNone         TemporalIntentKind = ""
	IntentCurrentState TemporalIntentKind = "current_state"
	IntentHistory      TemporalIntentKind = "history"
	IntentWhen         TemporalIntentKind = "when"
	IntentPointInTime  TemporalIntentKind = "point_in_time"
	IntentTimeRange    TemporalIntentKind = "time_range"
	IntentDuration     TemporalIntentKind = "duration"
)

// TemporalIntent represents detected temporal intent in a recall query.
type TemporalIntent struct {
	Kind     TemporalIntentKind
	From     *time.Time
	Until    *time.Time
	Keywords []string
}

var (
	reCurrentIntent  = regexp.MustCompile(`(?i)\b(current(?:ly)?|right now|at the moment|latest|actual(?:mente)?|ahora(?: mismo)?|en este momento|hoy en día)\b`)
	reHistoryIntent  = regexp.MustCompile(`(?i)\b(before|previous(?:ly)?|used to|formerly|old|prior|earlier|was using|anterior(?:mente)?|antes|previo|antiguo|usábamos)\b`)
	reWhenIntent     = regexp.MustCompile(`(?i)\b(when did|when was|what date|since when|¿?cuándo|en qué fecha|desde cuándo)\b`)
	reDurationIntent = regexp.MustCompile(`(?i)\b(how long|for how|how many (?:days|weeks|months|years)|cuánto tiempo|hace cuánto)\b`)
	reChangedIntent  = regexp.MustCompile(`(?i)\b(what changed|what was updated|what's different|qué cambió|qué se actualizó)\b`)
)

// detectTemporalIntent analyzes a query to determine its temporal intent.
func DetectTemporalIntent(query string, now time.Time) TemporalIntent {
	lower := strings.ToLower(query)

	var intent TemporalIntent

	// Parse temporal expressions from the query itself
	parser := temporal.NewParser()
	mentions := parser.Parse(query, now)

	// Priority: duration > when > changed > point_in_time/range > current > history
	if matches := reDurationIntent.FindAllString(lower, -1); len(matches) > 0 {
		intent.Kind = IntentDuration
		intent.Keywords = matches
		applyParsedWindows(&intent, mentions)
		return intent
	}

	if matches := reWhenIntent.FindAllString(lower, -1); len(matches) > 0 {
		intent.Kind = IntentWhen
		intent.Keywords = matches
		applyParsedWindows(&intent, mentions)
		return intent
	}

	if matches := reChangedIntent.FindAllString(lower, -1); len(matches) > 0 {
		intent.Kind = IntentTimeRange
		intent.Keywords = matches
		applyParsedWindows(&intent, mentions)
		return intent
	}

	// Explicit temporal mentions in query → classify by mention types
	if len(mentions) > 0 {
		hasDuration := false
		allCurrentState := true
		for _, m := range mentions {
			if m.Kind == temporal.KindDuration {
				hasDuration = true
			}
			if m.Kind != temporal.KindCurrentState {
				allCurrentState = false
			}
		}
		// If all mentions are current-state markers, treat as current-state intent
		if allCurrentState {
			intent.Kind = IntentCurrentState
			intent.Keywords = mentionKeywords(mentions)
			return intent
		}
		if hasDuration {
			intent.Kind = IntentTimeRange
		} else {
			intent.Kind = IntentPointInTime
		}
		intent.Keywords = mentionKeywords(mentions)
		applyParsedWindows(&intent, mentions)
		return intent
	}

	if matches := reCurrentIntent.FindAllString(lower, -1); len(matches) > 0 {
		intent.Kind = IntentCurrentState
		intent.Keywords = matches
		return intent
	}

	if matches := reHistoryIntent.FindAllString(lower, -1); len(matches) > 0 {
		intent.Kind = IntentHistory
		intent.Keywords = matches
		return intent
	}

	return intent
}

func applyParsedWindows(intent *TemporalIntent, mentions []temporal.ParseResult) {
	for _, m := range mentions {
		if m.NormalizedStart != nil {
			if intent.From == nil || m.NormalizedStart.Before(*intent.From) {
				t := *m.NormalizedStart
				intent.From = &t
			}
		}
		if m.NormalizedEnd != nil {
			if intent.Until == nil || m.NormalizedEnd.After(*intent.Until) {
				t := *m.NormalizedEnd
				intent.Until = &t
			}
		}
	}
}

func mentionKeywords(mentions []temporal.ParseResult) []string {
	kw := make([]string, 0, len(mentions))
	for _, m := range mentions {
		kw = append(kw, m.RawText)
	}
	return kw
}

// cleanQueryForFTS removes temporal noise words from the query to improve FTS recall.
// For temporal queries, the intent keywords and common question words would hurt FTS
// matching since observations typically don't contain them.
func cleanQueryForFTS(query string, intent TemporalIntent) string {
	if intent.Kind == IntentNone {
		return query
	}

	cleaned := strings.ToLower(query)

	// Remove matched temporal keywords
	for _, kw := range intent.Keywords {
		cleaned = strings.ReplaceAll(cleaned, strings.ToLower(kw), " ")
	}

	// Remove common question/filler words that hurt FTS precision
	noise := map[string]bool{
		"when": true, "what": true, "how": true, "did": true, "was": true,
		"is": true, "are": true, "do": true, "does": true, "we": true,
		"the": true, "to": true, "of": true, "in": true, "a": true,
		"cuándo": true, "cuál": true, "cómo": true,
		"el": true, "la": true, "los": true, "las": true, "de": true,
		"en": true, "que": true, "por": true, "para": true,
	}

	tokens := strings.Fields(cleaned)
	var kept []string
	for _, t := range tokens {
		if !noise[t] {
			kept = append(kept, t)
		}
	}

	result := strings.TrimSpace(strings.Join(kept, " "))
	if result == "" {
		return query
	}
	return result
}

// --- Temporal mention loading for scoring ---

type mentionInfo struct {
	Kind            string
	NormalizedStart *time.Time
	NormalizedEnd   *time.Time
	Confidence      float64
}

func loadCandidateMentions(ctx context.Context, db *sql.DB, ids []string) (map[string][]mentionInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := db.QueryContext(ctx, `
		SELECT observation_id, mention_kind, normalized_start, normalized_end, confidence
		FROM temporal_mentions
		WHERE observation_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("load temporal mentions: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]mentionInfo)
	for rows.Next() {
		var obsID, kind string
		var start, end sql.NullString
		var confidence float64
		if err := rows.Scan(&obsID, &kind, &start, &end, &confidence); err != nil {
			continue
		}
		info := mentionInfo{Kind: kind, Confidence: confidence}
		if start.Valid {
			if parsed, parseErr := parseSQLiteTime(start.String); parseErr == nil {
				info.NormalizedStart = &parsed
			}
		}
		if end.Valid {
			if parsed, parseErr := parseSQLiteTime(end.String); parseErr == nil {
				info.NormalizedEnd = &parsed
			}
		}
		result[obsID] = append(result[obsID], info)
	}

	return result, rows.Err()
}

// --- Temporal scoring ---

// temporalMultiplier computes a multiplicative boost/penalty for a candidate
// based on the detected temporal intent and the candidate's temporal mentions.
func temporalMultiplier(c candidate, intent TemporalIntent, mentions []mentionInfo) float64 {
	if intent.Kind == IntentNone {
		return 1.0
	}

	boost := 0.0

	switch intent.Kind {
	case IntentCurrentState:
		boost += stalenessBoostCurrent(c.Staleness)
		if hasKind(mentions, "current_state") {
			boost += 0.15
		}

	case IntentHistory:
		boost += stalenessBoostHistory(c.Staleness)
		if len(mentions) > 0 {
			boost += 0.10
		}

	case IntentWhen:
		if len(mentions) > 0 {
			boost += 0.25
			if hasKind(mentions, "absolute") || hasKind(mentions, "relative") {
				boost += 0.10
			}
		}

	case IntentPointInTime:
		if intent.From != nil {
			best := bestProximity(mentions, *intent.From)
			boost += 0.30 * best
		}

	case IntentTimeRange:
		if mentionInRange(mentions, intent.From, intent.Until) {
			boost += 0.25
		}
		if len(mentions) > 0 {
			boost += 0.10
		}

	case IntentDuration:
		if len(mentions) > 0 {
			boost += 0.15
			if hasKind(mentions, "duration") {
				boost += 0.15
			}
		}
	}

	// Clamp to avoid extreme swings
	if boost < -0.3 {
		boost = -0.3
	}
	if boost > 0.5 {
		boost = 0.5
	}

	return 1.0 + boost
}

func stalenessBoostCurrent(staleness string) float64 {
	switch staleness {
	case "fresh":
		return 0.15
	case "revalidated":
		return 0.10
	case "stale":
		return -0.05
	case "expired":
		return -0.20
	default:
		return 0.0
	}
}

func stalenessBoostHistory(staleness string) float64 {
	switch staleness {
	case "expired":
		return 0.15
	case "stale":
		return 0.10
	case "fresh":
		return -0.05
	default:
		return 0.0
	}
}

func hasKind(mentions []mentionInfo, kind string) bool {
	for _, m := range mentions {
		if m.Kind == kind {
			return true
		}
	}
	return false
}

func bestProximity(mentions []mentionInfo, target time.Time) float64 {
	best := 0.0
	for _, m := range mentions {
		if m.NormalizedStart != nil {
			p := temporalProximity(*m.NormalizedStart, target)
			if p > best {
				best = p
			}
		}
	}
	return best
}

func temporalProximity(mention, target time.Time) float64 {
	diff := math.Abs(mention.Sub(target).Hours() / 24)
	if diff <= 1 {
		return 1.0
	}
	return math.Exp(-diff / 30.0)
}

func mentionInRange(mentions []mentionInfo, from, until *time.Time) bool {
	for _, m := range mentions {
		if m.NormalizedStart == nil {
			continue
		}
		t := *m.NormalizedStart
		if from != nil && t.Before(*from) {
			continue
		}
		if until != nil && t.After(*until) {
			continue
		}
		return true
	}
	return false
}
