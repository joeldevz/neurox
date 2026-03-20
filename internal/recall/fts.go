package recall

import "strings"

func buildFTSMatchQuery(query string) string {
	tokens := strings.Fields(query)
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		escaped := strings.ReplaceAll(trimmed, `"`, `""`)
		parts = append(parts, `"`+escaped+`"`)
	}
	// Use OR so any matching term contributes to results.
	// BM25 naturally ranks documents matching more terms higher.
	return strings.Join(parts, " OR ")
}
