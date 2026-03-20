package temporal

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parser extracts temporal expressions from text using deterministic rules.
type Parser struct{}

// NewParser creates a new temporal parser.
func NewParser() *Parser {
	return &Parser{}
}

// Parse extracts all temporal mentions from text, anchored to the given reference time.
func (p *Parser) Parse(text string, anchor time.Time) []ParseResult {
	anchor = anchor.UTC()
	lower := strings.ToLower(text)

	var results []ParseResult

	// Order matters: try more specific patterns first.
	results = append(results, p.parseAbsoluteDates(lower, anchor)...)
	results = append(results, p.parseRelativeExpressions(lower, anchor)...)
	results = append(results, p.parseCurrentState(lower)...)
	results = append(results, p.parseDuration(lower, anchor)...)

	return dedup(results)
}

// --- Absolute dates ---

var (
	// ISO: 2026-03-15
	reISO = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	// English: March 15, 2026 or March 2026
	reEnglishDate = regexp.MustCompile(`\b(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{1,2}),?\s+(\d{4})\b`)
	reEnglishMonth = regexp.MustCompile(`\b(january|february|march|april|may|june|july|august|september|october|november|december)\s+(\d{4})\b`)
	// Spanish: 15 de marzo de 2026 or marzo 2026
	reSpanishDate = regexp.MustCompile(`\b(\d{1,2})\s+de\s+(enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|octubre|noviembre|diciembre)\s+(?:de\s+)?(\d{4})\b`)
	reSpanishMonth = regexp.MustCompile(`\b(enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|octubre|noviembre|diciembre)\s+(?:de\s+)?(\d{4})\b`)
)

var englishMonths = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
}

var spanishMonths = map[string]time.Month{
	"enero": time.January, "febrero": time.February, "marzo": time.March,
	"abril": time.April, "mayo": time.May, "junio": time.June,
	"julio": time.July, "agosto": time.August, "septiembre": time.September,
	"octubre": time.October, "noviembre": time.November, "diciembre": time.December,
}

func (p *Parser) parseAbsoluteDates(text string, anchor time.Time) []ParseResult {
	var results []ParseResult

	for _, m := range reISO.FindAllString(text, -1) {
		if parsed, err := time.Parse("2006-01-02", m); err == nil {
			start := parsed
			results = append(results, ParseResult{
				RawText:         m,
				Kind:            KindAbsolute,
				NormalizedStart: &start,
				Confidence:      0.95,
			})
		}
	}

	for _, m := range reEnglishDate.FindAllStringSubmatch(text, -1) {
		month := englishMonths[m[1]]
		day, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindAbsolute,
			NormalizedStart: &start,
			Confidence:      0.95,
		})
	}

	for _, m := range reEnglishMonth.FindAllStringSubmatch(text, -1) {
		// Skip if already captured as a full date
		if containsRawText(results, m[0]) {
			continue
		}
		month := englishMonths[m[1]]
		year, _ := strconv.Atoi(m[2])
		start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindAbsolute,
			NormalizedStart: &start,
			NormalizedEnd:   &end,
			Confidence:      0.85,
		})
	}

	for _, m := range reSpanishDate.FindAllStringSubmatch(text, -1) {
		day, _ := strconv.Atoi(m[1])
		month := spanishMonths[m[2]]
		year, _ := strconv.Atoi(m[3])
		start := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindAbsolute,
			NormalizedStart: &start,
			Confidence:      0.95,
		})
	}

	for _, m := range reSpanishMonth.FindAllStringSubmatch(text, -1) {
		if containsRawText(results, m[0]) {
			continue
		}
		month := spanishMonths[m[1]]
		year, _ := strconv.Atoi(m[2])
		start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0).Add(-time.Nanosecond)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindAbsolute,
			NormalizedStart: &start,
			NormalizedEnd:   &end,
			Confidence:      0.85,
		})
	}

	return results
}

// --- Relative expressions ---

var (
	// "yesterday", "today", "tomorrow"
	reSimpleRelative = regexp.MustCompile(`\b(yesterday|today|tomorrow|ayer|hoy|mañana)\b`)
	// "N days/weeks/months/years ago"
	reNumericAgo = regexp.MustCompile(`\b(\d+)\s+(days?|weeks?|months?|years?|días?|semanas?|meses?|años?)\s+ago\b`)
	// "a week ago", "a month ago", "a year ago"
	reSingleAgo = regexp.MustCompile(`\b(?:a|an|one|un|una)\s+(week|month|year|semana|mes|año)\s+ago\b`)
	// "last/next week/month/year" (English)
	reLastNext = regexp.MustCompile(`\b(last|next|past|previous)\s+(week|month|year|monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b`)
	// "hace N días/semanas/meses/años"
	reHace = regexp.MustCompile(`\bhace\s+(\d+)\s+(días?|semanas?|meses?|años?)\b`)
	// "hace un/una semana/mes/año"
	reHaceSingle = regexp.MustCompile(`\bhace\s+(?:un|una)\s+(semana|mes|año)\b`)
	// Word-number relative: "two weeks ago", "three months ago"
	reWordAgo = regexp.MustCompile(`\b(two|three|four|five|six|seven|eight|nine|ten|dos|tres|cuatro|cinco|seis|siete|ocho|nueve|diez)\s+(days?|weeks?|months?|years?|días?|semanas?|meses?|años?)\s+ago\b`)
	// "hace dos semanas", "hace tres meses"
	reHaceWord = regexp.MustCompile(`\bhace\s+(dos|tres|cuatro|cinco|seis|siete|ocho|nueve|diez)\s+(días?|semanas?|meses?|años?)\b`)
)

var wordToNum = map[string]int{
	"two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10,
	"dos": 2, "tres": 3, "cuatro": 4, "cinco": 5, "seis": 6,
	"siete": 7, "ocho": 8, "nueve": 9, "diez": 10,
}

func (p *Parser) parseRelativeExpressions(text string, anchor time.Time) []ParseResult {
	var results []ParseResult

	for _, m := range reSimpleRelative.FindAllString(text, -1) {
		var start time.Time
		switch m {
		case "yesterday", "ayer":
			start = anchor.AddDate(0, 0, -1)
		case "today", "hoy":
			start = anchor
		case "tomorrow", "mañana":
			start = anchor.AddDate(0, 0, 1)
		}
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m,
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.95,
		})
	}

	for _, m := range reNumericAgo.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		start := subtractUnit(anchor, n, normalizeUnit(m[2]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	for _, m := range reWordAgo.FindAllStringSubmatch(text, -1) {
		n := wordToNum[m[1]]
		if n == 0 {
			continue
		}
		start := subtractUnit(anchor, n, normalizeUnit(m[2]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	for _, m := range reSingleAgo.FindAllStringSubmatch(text, -1) {
		start := subtractUnit(anchor, 1, normalizeUnit(m[1]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	for _, m := range reLastNext.FindAllStringSubmatch(text, -1) {
		direction := m[1]
		unit := m[2]
		var start time.Time
		mult := -1
		if direction == "next" {
			mult = 1
		}
		switch unit {
		case "week":
			start = truncateDay(anchor.AddDate(0, 0, 7*mult))
		case "month":
			start = truncateDay(anchor.AddDate(0, mult, 0))
		case "year":
			start = truncateDay(anchor.AddDate(mult, 0, 0))
		default:
			// Day of week
			start = truncateDay(findDayOfWeek(anchor, unit, mult))
		}
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.80,
		})
	}

	for _, m := range reHace.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		start := subtractUnit(anchor, n, normalizeUnit(m[2]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	for _, m := range reHaceWord.FindAllStringSubmatch(text, -1) {
		n := wordToNum[m[1]]
		if n == 0 {
			continue
		}
		start := subtractUnit(anchor, n, normalizeUnit(m[2]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	for _, m := range reHaceSingle.FindAllStringSubmatch(text, -1) {
		start := subtractUnit(anchor, 1, normalizeUnit(m[1]))
		start = truncateDay(start)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindRelative,
			NormalizedStart: &start,
			Confidence:      0.85,
		})
	}

	return results
}

// --- Current state ---

var reCurrentState = regexp.MustCompile(`\b(currently|right now|at the moment|at present|as of now|actualmente|ahora mismo|en este momento|por ahora)\b`)

func (p *Parser) parseCurrentState(text string) []ParseResult {
	var results []ParseResult
	for _, m := range reCurrentState.FindAllString(text, -1) {
		results = append(results, ParseResult{
			RawText:    m,
			Kind:       KindCurrentState,
			Confidence: 0.95,
		})
	}
	return results
}

// --- Duration ---

var (
	// "for N days/weeks/months/years"
	reForDuration = regexp.MustCompile(`\bfor\s+(\d+)\s+(days?|weeks?|months?|years?)\b`)
	// "since March", "since 2025"
	reSinceMonth = regexp.MustCompile(`\bsince\s+(january|february|march|april|may|june|july|august|september|october|november|december)(?:\s+(\d{4}))?\b`)
	// "desde marzo", "desde 2025"
	reDesdeMonth = regexp.MustCompile(`\bdesde\s+(enero|febrero|marzo|abril|mayo|junio|julio|agosto|septiembre|octubre|noviembre|diciembre)(?:\s+(?:de\s+)?(\d{4}))?\b`)
)

func (p *Parser) parseDuration(text string, anchor time.Time) []ParseResult {
	var results []ParseResult

	for _, m := range reForDuration.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		end := anchor
		start := subtractUnit(anchor, n, normalizeUnit(m[2]))
		start = truncateDay(start)
		end = truncateDay(end)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindDuration,
			NormalizedStart: &start,
			NormalizedEnd:   &end,
			Confidence:      0.80,
		})
	}

	for _, m := range reSinceMonth.FindAllStringSubmatch(text, -1) {
		month := englishMonths[m[1]]
		year := anchor.Year()
		if m[2] != "" {
			year, _ = strconv.Atoi(m[2])
		}
		start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindDuration,
			NormalizedStart: &start,
			Confidence:      0.80,
		})
	}

	for _, m := range reDesdeMonth.FindAllStringSubmatch(text, -1) {
		month := spanishMonths[m[1]]
		year := anchor.Year()
		if m[2] != "" {
			year, _ = strconv.Atoi(m[2])
		}
		start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		results = append(results, ParseResult{
			RawText:         m[0],
			Kind:            KindDuration,
			NormalizedStart: &start,
			Confidence:      0.80,
		})
	}

	return results
}

// --- Helpers ---

func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func normalizeUnit(unit string) string {
	switch unit {
	case "day", "days":
		return "day"
	case "week", "weeks":
		return "week"
	case "month", "months":
		return "month"
	case "year", "years":
		return "year"
	case "día", "días", "dia", "dias":
		return "day"
	case "semana", "semanas":
		return "week"
	case "mes", "meses":
		return "month"
	case "año", "años":
		return "year"
	default:
		return unit
	}
}

func subtractUnit(anchor time.Time, n int, unit string) time.Time {
	switch unit {
	case "day":
		return anchor.AddDate(0, 0, -n)
	case "week":
		return anchor.AddDate(0, 0, -7*n)
	case "month":
		return anchor.AddDate(0, -n, 0)
	case "year":
		return anchor.AddDate(-n, 0, 0)
	default:
		return anchor
	}
}

var dayOfWeekMap = map[string]time.Weekday{
	"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
	"sunday": time.Sunday,
}

func findDayOfWeek(anchor time.Time, dayName string, direction int) time.Time {
	target, ok := dayOfWeekMap[dayName]
	if !ok {
		return anchor
	}
	current := anchor.Weekday()
	diff := int(target) - int(current)
	if direction < 0 {
		if diff >= 0 {
			diff -= 7
		}
	} else {
		if diff <= 0 {
			diff += 7
		}
	}
	return anchor.AddDate(0, 0, diff)
}

func containsRawText(results []ParseResult, raw string) bool {
	for _, r := range results {
		if strings.Contains(r.RawText, raw) || strings.Contains(raw, r.RawText) {
			return true
		}
	}
	return false
}

func dedup(results []ParseResult) []ParseResult {
	if len(results) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []ParseResult
	for _, r := range results {
		if seen[r.RawText] {
			continue
		}
		seen[r.RawText] = true
		out = append(out, r)
	}
	return out
}
