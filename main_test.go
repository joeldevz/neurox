package main

import (
	"testing"
)

func TestParseRecallArgs(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedQuery     string
		expectedLimit     int
		expectedNamespace string
		expectedDebug     bool
		shouldFail        bool
	}{
		{
			name:          "valued flag before positional",
			args:          []string{"--limit", "3", "Docker"},
			expectedQuery: "Docker",
			expectedLimit: 3,
		},
		{
			name:          "valued flag after positional",
			args:          []string{"Docker", "--limit", "3"},
			expectedQuery: "Docker",
			expectedLimit: 3,
		},
		{
			name:              "multiple valued flags",
			args:              []string{"--namespace", "demo", "--limit", "5", "query"},
			expectedQuery:     "query",
			expectedLimit:     5,
			expectedNamespace: "demo",
		},
		{
			name:          "boolean flag with positional",
			args:          []string{"--debug", "Docker"},
			expectedQuery: "Docker",
			expectedDebug: true,
			expectedLimit: 10, // default
		},
		{
			name:          "complex: namespace, limit, debug, query",
			args:          []string{"--namespace", "demo", "--debug", "--limit", "5", "kubernetes pods"},
			expectedQuery: "kubernetes pods",
			expectedLimit: 5,
			expectedDebug: true,
			expectedNamespace: "demo",
		},
		{
			name:       "empty args should fail",
			args:       []string{},
			shouldFail: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, opts, err := parseRecallArgs(tt.args)
			
			if tt.shouldFail {
				if err == nil && len(tt.args) > 0 {
					t.Errorf("expected error, got nil")
				}
				return
			}
			
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			
			if query != tt.expectedQuery {
				t.Errorf("query: got %q, want %q", query, tt.expectedQuery)
			}
			if opts.limit != tt.expectedLimit {
				t.Errorf("limit: got %d, want %d", opts.limit, tt.expectedLimit)
			}
			if opts.namespace != tt.expectedNamespace {
				t.Errorf("namespace: got %q, want %q", opts.namespace, tt.expectedNamespace)
			}
			if opts.debug != tt.expectedDebug {
				t.Errorf("debug: got %v, want %v", opts.debug, tt.expectedDebug)
			}
		})
	}
}
