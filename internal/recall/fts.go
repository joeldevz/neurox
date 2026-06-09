package recall

import "strings"

// minPrefixLen is the minimum token length required for prefix wildcard matching.
// Tokens with 3 or fewer characters use exact matching only to avoid noisy
// short-token expansions (e.g. "db" matching "debug", "dbus", etc.).
const minPrefixLen = 4

// stopwords is a set of common Spanish and English stopwords.
// These tokens are filtered from FTS queries to avoid polluting relevance.
var stopwords = map[string]struct{}{
	// Spanish stopwords
	"de": {}, "la": {}, "el": {}, "en": {}, "y": {}, "a": {}, "los": {}, "las": {},
	"un": {}, "una": {}, "con": {}, "por": {}, "para": {}, "que": {}, "del": {},
	"al": {}, "se": {}, "su": {}, "lo": {}, "le": {}, "es": {}, "o": {}, "e": {},
	"u": {}, "mi": {}, "tu": {}, "no": {}, "si": {}, "como": {},

	// English stopwords
	"the": {}, "of": {}, "in": {}, "an": {}, "and": {}, "or": {}, "to": {}, "is": {},
	"for": {}, "on": {}, "at": {}, "by": {}, "with": {}, "as": {}, "be": {}, "it": {},
	"that": {}, "this": {}, "from": {}, "use": {}, "are": {}, "was": {},
	"have": {}, "has": {}, "had": {}, "do": {}, "does": {}, "did": {}, "can": {},
	"could": {}, "would": {}, "should": {}, "may": {}, "might": {}, "must": {},
	"will": {}, "shall": {}, "what": {}, "when": {}, "where": {}, "why": {}, "how": {},
}

func buildFTSMatchQuery(query string) string {
	tokens := strings.Fields(query)
	parts := make([]string, 0, len(tokens))
	allFiltered := true

	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}

		// Check if token is a stopword (case-insensitive)
		lowerToken := strings.ToLower(trimmed)
		if _, isStopword := stopwords[lowerToken]; isStopword {
			continue
		}

		allFiltered = false
		escaped := strings.ReplaceAll(trimmed, `"`, `""`)
		exact := `"` + escaped + `"`

		if len(trimmed) >= minPrefixLen {
			// Append prefix variant: "auth" OR "auth"*
			// This matches "auth" exactly AND any token starting with "auth"
			// (e.g. "authentication", "authorize", "auth-token").
			parts = append(parts, exact+` OR `+exact+`*`)
		} else {
			parts = append(parts, exact)
		}
	}

	// Guard against degenerate all-stopword queries: fall back to original tokenization
	if allFiltered {
		parts = make([]string, 0, len(tokens))
		for _, token := range tokens {
			trimmed := strings.TrimSpace(token)
			if trimmed == "" {
				continue
			}
			escaped := strings.ReplaceAll(trimmed, `"`, `""`)
			exact := `"` + escaped + `"`
			if len(trimmed) >= minPrefixLen {
				parts = append(parts, exact+` OR `+exact+`*`)
			} else {
				parts = append(parts, exact)
			}
		}
	}

	// Use OR so any matching term contributes to results.
	// BM25 naturally ranks documents matching more terms higher.
	return strings.Join(parts, " OR ")
}
