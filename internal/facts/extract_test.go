package facts

import "testing"

func TestParseTriples(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     int
	}{
		{"valid triples", "project | uses | react\nauth | depends_on | jwt", 2},
		{"with dashes", "- project | uses | react\n- auth | depends_on | jwt", 2},
		{"with bullets", "• project | uses | react", 1},
		{"NONE response", "NONE", 0},
		{"empty", "", 0},
		{"invalid format", "this is not a triple", 0},
		{"mixed valid/invalid", "project | uses | react\nnot a triple\nauth | depends_on | jwt", 2},
		{"max 5", "a|b|c\nd|e|f\ng|h|i\nj|k|l\nm|n|o\np|q|r", 5},
		{"spaces around pipes", "  project  |  uses  |  react  ", 1},
		{"empty parts", "| uses | react", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriples(tt.response)
			if len(got) != tt.want {
				t.Errorf("parseTriples() returned %d triples, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseTriplesContent(t *testing.T) {
	triples := parseTriples("project | uses_framework | react\nauth | depends_on | jwt_library")

	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(triples))
	}

	if triples[0].subject != "project" || triples[0].predicate != "uses_framework" || triples[0].object != "react" {
		t.Errorf("triple[0] = %+v", triples[0])
	}
	if triples[1].subject != "auth" || triples[1].predicate != "depends_on" || triples[1].object != "jwt_library" {
		t.Errorf("triple[1] = %+v", triples[1])
	}
}
