package curate

import (
	"os"
	"path/filepath"
	"testing"
)

// writeYAML is a test helper that writes content to a temp file and returns its path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "priorities.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp priorities file: %v", err)
	}
	return path
}

func TestLoadPriorities(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string // empty means use a nonexistent path
		nonexistent    bool   // if true, pass a path that doesn't exist
		wantGlobal     []string
		wantNamespaced map[string][]string
		wantErr        bool
	}{
		{
			name:           "missing file returns empty priorities without error",
			nonexistent:    true,
			wantGlobal:     nil,
			wantNamespaced: map[string][]string{},
		},
		{
			name: "valid file with namespace and global",
			yaml: `neurox:
  - "Architecture decisions about Buffer→Working→Core model"
  - "Go patterns and conventions used in the project"
_global:
  - "My name and personal preferences"
`,
			wantGlobal: []string{"My name and personal preferences"},
			wantNamespaced: map[string][]string{
				"neurox": {
					"Architecture decisions about Buffer→Working→Core model",
					"Go patterns and conventions used in the project",
				},
			},
		},
		{
			name: "file with only global key",
			yaml: `_global:
  - "Always remember the user's name"
  - "Prefer verbose error messages"
`,
			wantGlobal:     []string{"Always remember the user's name", "Prefer verbose error messages"},
			wantNamespaced: map[string][]string{},
		},
		{
			name: "file with only namespace keys",
			yaml: `myapp:
  - "Use repository pattern"
another:
  - "Always validate input"
`,
			wantGlobal: nil,
			wantNamespaced: map[string][]string{
				"myapp":   {"Use repository pattern"},
				"another": {"Always validate input"},
			},
		},
		{
			name:           "empty file returns empty priorities",
			yaml:           "",
			wantGlobal:     nil,
			wantNamespaced: map[string][]string{},
		},
		{
			name:    "invalid YAML returns error",
			yaml:    ":\tinvalid: [yaml",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.nonexistent {
				path = filepath.Join(t.TempDir(), "does-not-exist.yaml")
			} else {
				path = writeYAML(t, tc.yaml)
			}

			got, err := LoadPriorities(path)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check global
			if len(tc.wantGlobal) != len(got.Global) {
				t.Errorf("Global len = %d, want %d", len(got.Global), len(tc.wantGlobal))
			} else {
				for i, want := range tc.wantGlobal {
					if got.Global[i] != want {
						t.Errorf("Global[%d] = %q, want %q", i, got.Global[i], want)
					}
				}
			}

			// Check namespaced
			for ns, wantEntries := range tc.wantNamespaced {
				gotEntries := got.Namespaced[ns]
				if len(gotEntries) != len(wantEntries) {
					t.Errorf("Namespaced[%q] len = %d, want %d", ns, len(gotEntries), len(wantEntries))
					continue
				}
				for i, want := range wantEntries {
					if gotEntries[i] != want {
						t.Errorf("Namespaced[%q][%d] = %q, want %q", ns, i, gotEntries[i], want)
					}
				}
			}

			// Ensure no unexpected namespaces
			for ns := range got.Namespaced {
				if _, ok := tc.wantNamespaced[ns]; !ok {
					t.Errorf("unexpected namespace in result: %q", ns)
				}
			}
		})
	}
}

func TestForNamespace(t *testing.T) {
	tests := []struct {
		name string
		p    Priorities
		ns   string
		want []string
	}{
		{
			name: "namespace-specific and global combined",
			p: Priorities{
				Namespaced: map[string][]string{
					"neurox": {"Architecture decisions", "Go patterns"},
				},
				Global: []string{"User preferences"},
			},
			ns:   "neurox",
			want: []string{"Architecture decisions", "Go patterns", "User preferences"},
		},
		{
			name: "unknown namespace returns only global",
			p: Priorities{
				Namespaced: map[string][]string{
					"other": {"something"},
				},
				Global: []string{"Global priority"},
			},
			ns:   "unknown",
			want: []string{"Global priority"},
		},
		{
			name: "namespace with no global returns only specific",
			p: Priorities{
				Namespaced: map[string][]string{
					"myapp": {"App-specific rule"},
				},
				Global: nil,
			},
			ns:   "myapp",
			want: []string{"App-specific rule"},
		},
		{
			name: "empty priorities returns empty slice not nil",
			p:    Priorities{Namespaced: map[string][]string{}},
			ns:   "any",
			want: []string{},
		},
		{
			name: "zero-value Priorities returns empty slice",
			p:    Priorities{},
			ns:   "any",
			want: []string{},
		},
		{
			name: "namespace-specific appear before global",
			p: Priorities{
				Namespaced: map[string][]string{
					"ns": {"first", "second"},
				},
				Global: []string{"third"},
			},
			ns:   "ns",
			want: []string{"first", "second", "third"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.p.ForNamespace(tc.ns)
			if got == nil {
				t.Error("ForNamespace returned nil, want non-nil slice")
				return
			}
			if len(got) != len(tc.want) {
				t.Errorf("len = %d, want %d (got %v)", len(got), len(tc.want), got)
				return
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}
