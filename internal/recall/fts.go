package recall

import "strings"

// minPrefixLen is the minimum token length required for prefix wildcard matching.
// Tokens with 3 or fewer characters use exact matching only to avoid noisy
// short-token expansions (e.g. "db" matching "debug", "dbus", etc.).
const minPrefixLen = 4

func buildFTSMatchQuery(query string) string {
	tokens := strings.Fields(query)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
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
	// Use OR so any matching term contributes to results.
	// BM25 naturally ranks documents matching more terms higher.
	return strings.Join(parts, " OR ")
}
