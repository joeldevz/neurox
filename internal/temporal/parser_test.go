package temporal

import (
	"testing"
	"time"
)

var anchor = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

func TestParseAbsoluteISO(t *testing.T) {
	p := NewParser()
	results := p.Parse("deployed on 2026-03-15", anchor)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Kind != KindAbsolute {
		t.Errorf("kind = %q, want 'absolute'", r.Kind)
	}
	if r.NormalizedStart == nil {
		t.Fatal("expected normalized_start")
	}
	want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !r.NormalizedStart.Equal(want) {
		t.Errorf("start = %v, want %v", r.NormalizedStart, want)
	}
	if r.Confidence < 0.9 {
		t.Errorf("confidence = %f, want >= 0.9", r.Confidence)
	}
}

func TestParseAbsoluteEnglish(t *testing.T) {
	p := NewParser()
	results := p.Parse("released on March 15, 2026", anchor)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("start = %v, want %v", results[0].NormalizedStart, want)
	}
}

func TestParseAbsoluteSpanish(t *testing.T) {
	p := NewParser()
	results := p.Parse("desplegado el 15 de marzo de 2026", anchor)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	want := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("start = %v, want %v", results[0].NormalizedStart, want)
	}
}

func TestParseAbsoluteMonthOnly(t *testing.T) {
	p := NewParser()
	results := p.Parse("we started in march 2026", anchor)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	r := results[0]
	wantStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !r.NormalizedStart.Equal(wantStart) {
		t.Errorf("start = %v, want %v", r.NormalizedStart, wantStart)
	}
	if r.NormalizedEnd == nil {
		t.Fatal("expected normalized_end for month range")
	}
	if r.Confidence >= 0.9 {
		t.Errorf("confidence = %f, want < 0.9 for month-only", r.Confidence)
	}
}

func TestParseRelativeYesterday(t *testing.T) {
	p := NewParser()
	results := p.Parse("fixed it yesterday", anchor)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	want := time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("start = %v, want %v", results[0].NormalizedStart, want)
	}
}

func TestParseRelativeNumericAgo(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
	}{
		{"migrated 3 days ago", time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)},
		{"changed 2 weeks ago", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)},
		{"started 6 months ago", time.Date(2025, 9, 20, 0, 0, 0, 0, time.UTC)},
		{"created 1 year ago", time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC)},
	}

	p := NewParser()
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			results := p.Parse(tt.input, anchor)
			if len(results) == 0 {
				t.Fatal("expected at least 1 result")
			}
			if results[0].NormalizedStart == nil {
				t.Fatal("expected normalized_start")
			}
			if !results[0].NormalizedStart.Equal(tt.want) {
				t.Errorf("start = %v, want %v", results[0].NormalizedStart, tt.want)
			}
		})
	}
}

func TestParseRelativeWordAgo(t *testing.T) {
	p := NewParser()
	results := p.Parse("two weeks ago we migrated", anchor)

	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	want := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("start = %v, want %v", results[0].NormalizedStart, want)
	}
}

func TestParseRelativeHace(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
	}{
		{"hace 2 semanas migramos", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)},
		{"hace 3 días se rompió", time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)},
		{"hace dos semanas", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)},
		{"hace un mes", time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)},
	}

	p := NewParser()
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			results := p.Parse(tt.input, anchor)
			if len(results) == 0 {
				t.Fatalf("expected at least 1 result for %q", tt.input)
			}
			if results[0].NormalizedStart == nil {
				t.Fatal("expected normalized_start")
			}
			if !results[0].NormalizedStart.Equal(tt.want) {
				t.Errorf("start = %v, want %v", results[0].NormalizedStart, tt.want)
			}
		})
	}
}

func TestParseRelativeLastNext(t *testing.T) {
	p := NewParser()

	results := p.Parse("last week we discussed", anchor)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	want := time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("last week: start = %v, want %v", results[0].NormalizedStart, want)
	}

	results = p.Parse("next month we deploy", anchor)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	want = time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	if !results[0].NormalizedStart.Equal(want) {
		t.Errorf("next month: start = %v, want %v", results[0].NormalizedStart, want)
	}
}

func TestParseCurrentState(t *testing.T) {
	tests := []string{
		"we currently use SQLite",
		"right now the API is stable",
		"actualmente usamos Go",
		"at the moment we're on v2",
	}

	p := NewParser()
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			results := p.Parse(input, anchor)
			if len(results) == 0 {
				t.Fatalf("expected at least 1 result for %q", input)
			}
			found := false
			for _, r := range results {
				if r.Kind == KindCurrentState {
					found = true
					if r.Confidence < 0.9 {
						t.Errorf("confidence = %f, want >= 0.9", r.Confidence)
					}
				}
			}
			if !found {
				t.Error("expected at least one current_state result")
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	p := NewParser()

	results := p.Parse("running for 3 weeks", anchor)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	r := results[0]
	if r.Kind != KindDuration {
		t.Errorf("kind = %q, want 'duration'", r.Kind)
	}
	if r.NormalizedStart == nil || r.NormalizedEnd == nil {
		t.Fatal("expected both start and end for duration")
	}
	wantStart := time.Date(2026, 2, 27, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	if !r.NormalizedStart.Equal(wantStart) {
		t.Errorf("start = %v, want %v", r.NormalizedStart, wantStart)
	}
	if !r.NormalizedEnd.Equal(wantEnd) {
		t.Errorf("end = %v, want %v", r.NormalizedEnd, wantEnd)
	}
}

func TestParseSince(t *testing.T) {
	p := NewParser()

	results := p.Parse("since march we use SQLite", anchor)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	r := results[0]
	if r.Kind != KindDuration {
		t.Errorf("kind = %q, want 'duration'", r.Kind)
	}
	wantStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !r.NormalizedStart.Equal(wantStart) {
		t.Errorf("start = %v, want %v", r.NormalizedStart, wantStart)
	}
}

func TestParseDesde(t *testing.T) {
	p := NewParser()

	results := p.Parse("desde marzo usamos SQLite", anchor)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	r := results[0]
	if r.Kind != KindDuration {
		t.Errorf("kind = %q, want 'duration'", r.Kind)
	}
	wantStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !r.NormalizedStart.Equal(wantStart) {
		t.Errorf("start = %v, want %v", r.NormalizedStart, wantStart)
	}
}

func TestParseMultipleMentions(t *testing.T) {
	p := NewParser()
	results := p.Parse("we migrated to SQLite two weeks ago and currently use it in production", anchor)

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d: %+v", len(results), results)
	}

	var hasRelative, hasCurrentState bool
	for _, r := range results {
		switch r.Kind {
		case KindRelative:
			hasRelative = true
		case KindCurrentState:
			hasCurrentState = true
		}
	}
	if !hasRelative {
		t.Error("expected a relative mention")
	}
	if !hasCurrentState {
		t.Error("expected a current_state mention")
	}
}

func TestParseNoTemporalContent(t *testing.T) {
	p := NewParser()
	results := p.Parse("the auth module uses JWT tokens", anchor)

	if len(results) != 0 {
		t.Errorf("expected 0 results for non-temporal text, got %d: %+v", len(results), results)
	}
}

func TestParseAmbiguousDegrades(t *testing.T) {
	p := NewParser()

	// Month-only should have lower confidence than full date
	monthResults := p.Parse("march 2026", anchor)
	fullResults := p.Parse("March 15, 2026", anchor)

	if len(monthResults) == 0 || len(fullResults) == 0 {
		t.Fatal("expected results for both")
	}
	if monthResults[0].Confidence >= fullResults[0].Confidence {
		t.Errorf("month-only confidence (%f) should be < full date confidence (%f)",
			monthResults[0].Confidence, fullResults[0].Confidence)
	}
}

func TestParseRealWorldScenarios(t *testing.T) {
	p := NewParser()

	tests := []struct {
		name      string
		input     string
		wantKinds []MentionKind
		wantMin   int
	}{
		{
			name:      "migration story",
			input:     "Migramos a SQLite hace dos semanas. Actualmente funciona bien en producción.",
			wantKinds: []MentionKind{KindRelative, KindCurrentState},
			wantMin:   2,
		},
		{
			name:      "deployment date",
			input:     "deployed the fix on 2026-03-10 and it has been stable since march",
			wantKinds: []MentionKind{KindAbsolute, KindDuration},
			wantMin:   2,
		},
		{
			name:      "past and present",
			input:     "we used PostgreSQL until last month, now we currently use SQLite",
			wantKinds: []MentionKind{KindRelative, KindCurrentState},
			wantMin:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := p.Parse(tt.input, anchor)
			if len(results) < tt.wantMin {
				t.Fatalf("expected >= %d results, got %d: %+v", tt.wantMin, len(results), results)
			}
			for _, wantKind := range tt.wantKinds {
				found := false
				for _, r := range results {
					if r.Kind == wantKind {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected kind %q in results: %+v", wantKind, results)
				}
			}
		})
	}
}
