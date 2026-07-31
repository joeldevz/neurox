package recall

import (
	"testing"
)

// TestStopwordFiltering verifies that stopwords are filtered from FTS queries.
// Spanish/English stopwords should not pollute the FTS match expression.
func TestStopwordFiltering(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantTokens map[string]bool // tokens that should appear in FTS query
		notTokens  map[string]bool // tokens that should NOT appear
	}{
		{
			name:  "Spanish orquestacion with stopword de",
			query: "orquestacion de containers",
			wantTokens: map[string]bool{
				"orquestacion": true,
				"containers":   true,
			},
			notTokens: map[string]bool{
				"de": true,
			},
		},
		{
			name:  "English phrase with the",
			query: "use the API",
			wantTokens: map[string]bool{
				"API": true,
			},
			notTokens: map[string]bool{
				"the": true,
				"use": true, // "use" is also a stopword
			},
		},
		{
			name:  "Degenerate all-stopwords falls back to original",
			query: "de la el",
			wantTokens: map[string]bool{
				"de": true, // should preserve original tokens
				"la": true,
				"el": true,
			},
			notTokens: map[string]bool{},
		},
		{
			name:  "Multiple stopwords interspersed",
			query: "como usar el API en y con containers de la orquesta",
			wantTokens: map[string]bool{
				"usar":       true,
				"API":        true,
				"containers": true,
				"orquesta":   true,
			},
			notTokens: map[string]bool{
				"como": true,
				"el":   true,
				"en":   true,
				"y":    true,
				"con":  true,
				"de":   true,
				"la":   true,
			},
		},
		{
			name:  "Code identifiers preserved",
			query: "use a PostgreSQL db instance",
			wantTokens: map[string]bool{
				"PostgreSQL": true,
				"db":         true,
				"instance":   true,
			},
			notTokens: map[string]bool{
				"a":   true,
				"use": true, // "use" is a stopword
			},
		},
		{
			name:  "Short stopwords like or and in",
			query: "error in the parsing or in decode",
			wantTokens: map[string]bool{
				"error":   true,
				"parsing": true,
				"decode":  true,
			},
			notTokens: map[string]bool{
				"in":  true,
				"the": true,
				"or":  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTSMatchQuery(tt.query)

			// Verify wanted tokens appear
			for token := range tt.wantTokens {
				if !contains(got, token) {
					t.Errorf("buildFTSMatchQuery(%q) missing token %q", tt.query, token)
					t.Logf("  got: %q", got)
				}
			}

			// Verify unwanted stopwords do NOT appear (as separate words, not part of larger words)
			for stopword := range tt.notTokens {
				// Check for the stopword in quotes (FTS wraps tokens in quotes)
				if contains(got, `"`+stopword+`"`) {
					t.Errorf("buildFTSMatchQuery(%q) should not include stopword %q", tt.query, stopword)
					t.Logf("  got: %q", got)
				}
			}
		})
	}
}

// TestStopwordFilteringPreservesCaseSensitiveMatches verifies that case is preserved
// and only semantic stopwords are filtered, not case-insensitive variations in content.
func TestStopwordFilteringPreservesCaseSensitiveMatches(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantToken    string
		notWantToken string
	}{
		{
			name:         "API preserved as identifier",
			query:        "the API",
			wantToken:    "API",
			notWantToken: "the",
		},
		{
			name:         "GO preserved as identifier",
			query:        "USE GO language",
			wantToken:    "GO",
			notWantToken: "USE", // "use" is a stopword (case-insensitive)
		},
		{
			name:         "FTS is a code acronym",
			query:        "FTS search feature in db",
			wantToken:    "FTS",
			notWantToken: "in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTSMatchQuery(tt.query)

			if !contains(got, tt.wantToken) {
				t.Errorf("buildFTSMatchQuery(%q) should keep %q, got: %q", tt.query, tt.wantToken, got)
			}

			if contains(got, `"`+tt.notWantToken+`"`) {
				t.Errorf("buildFTSMatchQuery(%q) should filter %q, got: %q", tt.query, tt.notWantToken, got)
			}
		})
	}
}

// TestStopwordFilteringRespectsPrefixWildcard verifies that long tokens (>=4 chars)
// still get the prefix wildcard even after stopword filtering.
func TestStopwordFilteringRespectsPrefixWildcard(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string // substring that should appear in output
	}{
		{
			name:  "orquestacion gets prefix wildcard",
			query: "orquestacion de containers",
			want:  "orquestacion\" OR \"orquestacion\"*",
		},
		{
			name:  "containers gets prefix wildcard",
			query: "orquestacion de containers",
			want:  "containers\" OR \"containers\"*",
		},
		{
			name:  "short tokens may still get prefix for 4+ char",
			query: "find db",
			want:  `"find" OR "find"*`, // "find" is 4 chars, gets wildcard
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFTSMatchQuery(tt.query)
			if !contains(got, tt.want) {
				t.Errorf("buildFTSMatchQuery(%q) should contain %q, got: %q", tt.query, tt.want, got)
			}
		})
	}
}

// helper: check if haystack contains needle as a substring
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
