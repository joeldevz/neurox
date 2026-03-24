package benchmark

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// colour palette
var (
	colorGreen  = lipgloss.Color("#22c55e") // S / A
	colorYellow = lipgloss.Color("#eab308") // B
	colorOrange = lipgloss.Color("#f97316") // C
	colorRed    = lipgloss.Color("#ef4444") // D / F
	colorBlue   = lipgloss.Color("#3b82f6")
	colorGray   = lipgloss.Color("#6b7280")
	colorWhite  = lipgloss.Color("#f8fafc")
)

// gradeColor maps a letter grade to a terminal colour.
func gradeColor(grade string) lipgloss.Color {
	switch grade {
	case "S", "A":
		return colorGreen
	case "B":
		return colorYellow
	case "C":
		return colorOrange
	default:
		return colorRed
	}
}

// PrintReport writes a formatted benchmark report to stdout using lipgloss.
func PrintReport(report *Report) {
	// --- header ---
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue).
		PaddingLeft(1)

	fmt.Println()
	fmt.Println(titleStyle.Render("🧠  Neurox Brain Benchmark"))
	fmt.Println(lipgloss.NewStyle().Foreground(colorGray).Render(
		fmt.Sprintf("   Scale: %s   Duration: %s", report.Scale, report.Duration.Round(1000000)),
	))
	fmt.Println()

	// --- overall score ---
	overallGrade := report.Grade
	overallColor := gradeColor(overallGrade)

	gradeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(overallColor).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(overallColor).
		Padding(0, 2).
		MarginLeft(2)

	scoreStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(overallColor)

	fmt.Printf("  Overall  %s  %s\n\n",
		scoreStyle.Render(fmt.Sprintf("%.1f / 100", report.OverallScore)),
		gradeStyle.Render(overallGrade),
	)

	// --- dimensions table ---
	header := lipgloss.NewStyle().Bold(true).Foreground(colorGray)
	fmt.Printf("  %s\n",
		header.Render(fmt.Sprintf("%-3s  %-32s  %-14s  %6s  %5s  %s",
			"#", "Dimension", "Category", "Score", "Grade", "Key Metric")),
	)
	fmt.Println("  " + strings.Repeat("─", 80))

	for i, r := range report.Dimensions {
		gc := gradeColor(r.Grade)
		gradeStr := lipgloss.NewStyle().Foreground(gc).Bold(true).Render(r.Grade)

		scoreStr := lipgloss.NewStyle().Foreground(gc).Render(
			fmt.Sprintf("%5.1f", r.Score),
		)

		keyMetric := keyMetricStr(r)

		fmt.Printf("  %-3d  %-32s  %-14s  %s  %s  %s\n",
			i+1,
			truncate(r.DimensionName, 32),
			truncate(r.Category, 14),
			scoreStr,
			gradeStr,
			lipgloss.NewStyle().Foreground(colorGray).Render(keyMetric),
		)

		// Print any errors indented
		for _, e := range r.Errors {
			fmt.Printf("       %s\n",
				lipgloss.NewStyle().Foreground(colorRed).Render("  ✗ "+e),
			)
		}
	}

	fmt.Println()

	// --- recommendations ---
	if len(report.Recommendations) > 0 {
		recHeader := lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
		fmt.Println("  " + recHeader.Render("Recommendations"))
		for i, rec := range report.Recommendations {
			bullet := lipgloss.NewStyle().Foreground(colorOrange).Render("  →")
			fmt.Printf("  %s %d. %s\n", bullet, i+1, rec)
		}
		fmt.Println()
	}
}

// ExportJSON writes the report as indented JSON to the given file path.
func ExportJSON(report *Report, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write report to %s: %w", path, err)
	}
	return nil
}

// keyMetricStr extracts the most informative metric from a dimension result.
func keyMetricStr(r DimensionResult) string {
	if len(r.Metrics) == 0 {
		if len(r.Checks) > 0 {
			passed := 0
			for _, c := range r.Checks {
				if c.Passed {
					passed++
				}
			}
			return fmt.Sprintf("%d/%d checks", passed, len(r.Checks))
		}
		return ""
	}

	// Prefer common metric keys.
	preferred := []string{
		"recall_rate", "precision", "accuracy",
		"throughput_ops_s", "p99_ms", "p50_ms",
		"checks_passed",
	}
	for _, k := range preferred {
		if v, ok := r.Metrics[k]; ok {
			return fmt.Sprintf("%s=%.2f", k, v)
		}
	}

	// Fall back to first metric alphabetically.
	for k, v := range r.Metrics {
		return fmt.Sprintf("%s=%.2f", k, v)
	}
	return ""
}

// truncate shortens s to n runes with "…" if needed.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// ExportHTML writes a self-contained HTML benchmark report to path.
// The report includes a radar chart, per-dimension breakdown, and a
// competitor comparison table. No external dependencies.
func ExportHTML(report *Report, path string) error {
	html := renderHTMLReport(report)
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		return fmt.Errorf("write HTML report to %s: %w", path, err)
	}
	return nil
}

// renderHTMLReport builds and returns the full HTML document as a string.
func renderHTMLReport(report *Report) string {
	var sb strings.Builder

	sb.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	sb.WriteString("<meta charset=\"UTF-8\">\n")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	sb.WriteString("<title>Neurox Brain Benchmark Report</title>\n")
	sb.WriteString(htmlCSS())
	sb.WriteString("</head>\n<body>\n<div class=\"container\">\n")

	sb.WriteString(htmlHeader(report))
	sb.WriteString(htmlRadar(report))
	sb.WriteString(htmlDimensions(report))
	sb.WriteString(htmlCompetitor(report))
	sb.WriteString(htmlRecommendations(report))

	sb.WriteString("</div>\n</body>\n</html>\n")
	return sb.String()
}

func htmlCSS() string {
	return `<style>
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #0f0f13; color: #e2e8f0; margin: 0; padding: 2rem; }
.container { max-width: 960px; margin: 0 auto; }
h1 { font-size: 1.8rem; color: #a78bfa; margin-bottom: 0.5rem; }
.score-big { font-size: 5rem; font-weight: 800; line-height: 1; }
.grade-badge { display: inline-block; padding: 0.3rem 1rem; border-radius: 8px; font-size: 1.5rem; font-weight: 700; margin-left: 1rem; }
.green { color: #22c55e; } .amber { color: #eab308; } .red { color: #ef4444; }
.bg-green { background: #22c55e; color: #000; } .bg-amber { background: #eab308; color: #000; } .bg-red { background: #ef4444; color: #000; }
.card { background: #1a1a2e; border-radius: 12px; padding: 1.5rem; margin-bottom: 1.5rem; }
.meta { color: #6b7280; font-size: 0.9rem; margin-bottom: 2rem; }
.radar-wrap { display: flex; justify-content: center; margin: 1rem 0; }
table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
th { text-align: left; padding: 0.6rem 0.8rem; color: #6b7280; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 1px; border-bottom: 1px solid #2d2d3d; }
td { padding: 0.6rem 0.8rem; border-bottom: 1px solid #1f1f2d; vertical-align: top; }
.bar-wrap { background: #2d2d3d; border-radius: 3px; height: 8px; width: 120px; display: inline-block; }
.bar { height: 8px; border-radius: 3px; }
.badge { display: inline-block; padding: 0.15rem 0.5rem; border-radius: 4px; font-size: 0.75rem; background: #2d2d3d; color: #a0aec0; }
.badge-cog { background: #1e1b4b; color: #a78bfa; }
.badge-perf { background: #1a2f1a; color: #22c55e; }
.badge-agent { background: #1e2a3d; color: #3b82f6; }
details summary { cursor: pointer; color: #6b7280; font-size: 0.8rem; padding: 0.3rem 0; }
.check-pass { color: #22c55e; } .check-fail { color: #ef4444; }
.comp-note { color: #6b7280; font-size: 0.8rem; margin-top: 0.5rem; }
.rec-list li { padding: 0.3rem 0; color: #f97316; }
</style>
`
}

func htmlHeader(report *Report) string {
	pct := report.OverallScore
	colorClass := scoreColorClassPct(pct)
	gradeBg := gradeBgClass(report.Grade)
	now := time.Now().Format("2006-01-02 15:04:05 UTC")

	return fmt.Sprintf(`<div class="card">
<h1>🧠 Neurox Brain Benchmark Report</h1>
<div>
  <span class="score-big %s">%.1f</span>
  <span class="grade-badge %s">%s</span>
</div>
<div class="meta" style="margin-top:1rem;">
  Scale: %s &nbsp;·&nbsp; Duration: %s &nbsp;·&nbsp; Generated: %s
</div>
</div>
`, colorClass, pct, gradeBg, report.Grade, report.Scale, report.Duration.Round(time.Millisecond), now)
}

func htmlRadar(report *Report) string {
	const r, cx, cy = 180.0, 200.0, 200.0

	actualPts := radarPoints(report.Dimensions, r, cx, cy)
	targetPts := radarFullPoints(len(report.Dimensions), r, cx, cy)
	axes := radarAxes(report.Dimensions, r, cx, cy)

	// concentric rings at 25, 50, 75, 100
	var rings strings.Builder
	for _, pct := range []float64{25, 50, 75, 100} {
		rr := r * pct / 100
		rings.WriteString(fmt.Sprintf(`<circle cx="%.1f" cy="%.1f" r="%.1f" fill="none" stroke="#2d2d3d" stroke-width="1"/>`, cx, cy, rr))
	}

	return fmt.Sprintf(`<div class="card">
<h2 style="color:#a78bfa;margin-top:0;">Radar Chart</h2>
<div class="radar-wrap">
<svg viewBox="0 0 400 400" width="500" height="500" xmlns="http://www.w3.org/2000/svg">
%s
%s
<polygon points="%s" fill="rgba(167,139,250,0.15)" stroke="#a78bfa" stroke-width="2"/>
<polygon points="%s" fill="none" stroke="#3b82f6" stroke-width="1" stroke-dasharray="4,3"/>
</svg>
</div>
<p style="color:#6b7280;font-size:0.8rem;text-align:center;">Purple fill = actual scores · Blue dashed = target (100%%)</p>
</div>
`, rings.String(), axes, actualPts, targetPts)
}

func htmlDimensions(report *Report) string {
	var sb strings.Builder
	sb.WriteString(`<div class="card">
<h2 style="color:#a78bfa;margin-top:0;">Dimensions</h2>
<table>
<thead><tr>
<th>#</th><th>Dimension</th><th>Category</th><th>Score</th><th>Grade</th><th>Key Metric</th><th>Checks</th>
</tr></thead>
<tbody>
`)
	for i, d := range report.Dimensions {
		colorCls := scoreColorClass(d.Score, d.Max)
		gradeBg := gradeBgClass(d.Grade)
		catCls := categoryBadgeClass(d.Category)
		pct := 0.0
		if d.Max > 0 {
			pct = d.Score / d.Max * 100
		}
		barPct := pct
		if barPct > 100 {
			barPct = 100
		}

		// count checks
		passCount := 0
		for _, c := range d.Checks {
			if c.Passed {
				passCount++
			}
		}

		km := keyMetricStr(d)

		// Build check details
		var checkDetails strings.Builder
		for _, c := range d.Checks {
			sym := `<span class="check-pass">✓</span>`
			if !c.Passed {
				sym = `<span class="check-fail">✗</span>`
			}
			checkDetails.WriteString(fmt.Sprintf("<div>%s %s</div>", sym, c.Name))
			if !c.Passed && c.Detail != "" {
				checkDetails.WriteString(fmt.Sprintf(`<div style="color:#6b7280;font-size:0.75rem;padding-left:1.2rem;">%s</div>`, c.Detail))
			}
		}

		var detailsBlock string
		if len(d.Checks) > 0 {
			detailsBlock = fmt.Sprintf(`<details><summary>%d/%d checks passed</summary>%s</details>`,
				passCount, len(d.Checks), checkDetails.String())
		} else {
			detailsBlock = fmt.Sprintf("%d/%d", passCount, len(d.Checks))
		}

		sb.WriteString(fmt.Sprintf(`<tr>
<td>%d</td>
<td><strong>%s</strong></td>
<td><span class="%s">%s</span></td>
<td>
  <span class="%s">%.1f / %.0f</span><br>
  <span class="bar-wrap"><span class="bar %s" style="width:%.0f%%;background:%s;display:block;"></span></span>
</td>
<td><span class="grade-badge %s" style="font-size:0.9rem;padding:0.1rem 0.5rem;">%s</span></td>
<td style="color:#6b7280;">%s</td>
<td>%s</td>
</tr>
`, i+1, d.DimensionName, catCls, d.Category,
			colorCls, d.Score, d.Max,
			colorCls, barPct, scoreBarColor(colorCls),
			gradeBg, d.Grade,
			km,
			detailsBlock))
	}
	sb.WriteString("</tbody></table>\n</div>\n")
	return sb.String()
}

func htmlCompetitor(report *Report) string {
	return fmt.Sprintf(`<div class="card">
<h2 style="color:#a78bfa;margin-top:0;">How Neurox Compares <small style="color:#6b7280;font-size:0.75rem;">(methodology differs — see notes)</small></h2>
<table>
<thead><tr><th>System</th><th>Benchmark</th><th>Score</th><th>Notes</th></tr></thead>
<tbody>
<tr>
  <td><strong style="color:#a78bfa;">Neurox</strong></td>
  <td>Brain Benchmark (this run)</td>
  <td><strong>%.1f%%</strong></td>
  <td>Cognitive+Perf+Agent, 12 dimensions</td>
</tr>
<tr>
  <td>mem0</td>
  <td>LOCOMO dataset</td>
  <td>49.3%%</td>
  <td>Recall accuracy only, no temporal degradation axis</td>
</tr>
<tr>
  <td>Zep/Graphiti</td>
  <td>LOCOMO dataset</td>
  <td>51.6%%</td>
  <td>Recall accuracy only; run aborted at 9h due to token cost</td>
</tr>
<tr>
  <td>Long-context baseline</td>
  <td>LOCOMO dataset</td>
  <td>84.6%%</td>
  <td>Full conversation history in context window</td>
</tr>
</tbody>
</table>
<p class="comp-note">⚠ These benchmarks measure different things. LOCOMO tests recall accuracy; the Neurox Brain Benchmark additionally tests temporal reasoning, decay correctness, and agent workflow quality.</p>
</div>
`, report.OverallScore)
}

func htmlRecommendations(report *Report) string {
	if len(report.Recommendations) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<div class="card">
<h2 style="color:#a78bfa;margin-top:0;">Recommendations</h2>
<ul class="rec-list">
`)
	for _, r := range report.Recommendations {
		sb.WriteString(fmt.Sprintf("<li>%s</li>\n", r))
	}
	sb.WriteString("</ul>\n</div>\n")
	return sb.String()
}

// radarPoints returns a SVG polygon points string for the given dimension scores.
func radarPoints(dims []DimensionResult, r, cx, cy float64) string {
	n := len(dims)
	if n == 0 {
		return ""
	}
	pts := make([]string, n)
	for i, d := range dims {
		angle := (2 * math.Pi * float64(i) / float64(n)) - math.Pi/2
		score := 0.0
		if d.Max > 0 {
			score = d.Score / d.Max
		}
		if score > 1 {
			score = 1
		}
		x := cx + score*r*math.Cos(angle)
		y := cy + score*r*math.Sin(angle)
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	return strings.Join(pts, " ")
}

// radarFullPoints returns a polygon at 100% on all axes (target outline).
func radarFullPoints(n int, r, cx, cy float64) string {
	if n == 0 {
		return ""
	}
	pts := make([]string, n)
	for i := range pts {
		angle := (2 * math.Pi * float64(i) / float64(n)) - math.Pi/2
		x := cx + r*math.Cos(angle)
		y := cy + r*math.Sin(angle)
		pts[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	return strings.Join(pts, " ")
}

// radarAxes returns SVG lines and text labels for each dimension axis.
func radarAxes(dims []DimensionResult, r, cx, cy float64) string {
	var sb strings.Builder
	n := len(dims)
	for i, d := range dims {
		angle := (2 * math.Pi * float64(i) / float64(n)) - math.Pi/2
		x2 := cx + r*math.Cos(angle)
		y2 := cy + r*math.Sin(angle)
		sb.WriteString(fmt.Sprintf(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#2d2d3d" stroke-width="1"/>`, cx, cy, x2, y2))
		lx := cx + (r+24)*math.Cos(angle)
		ly := cy + (r+24)*math.Sin(angle)
		anchor := "middle"
		if lx < cx-10 {
			anchor = "end"
		} else if lx > cx+10 {
			anchor = "start"
		}
		name := truncate(d.DimensionName, 14)
		sb.WriteString(fmt.Sprintf(`<text x="%.1f" y="%.1f" text-anchor=%q font-size="9" fill="#6b7280">%s</text>`, lx, ly, anchor, name))
	}
	return sb.String()
}

// scoreColorClass returns a CSS class based on score percentage (score/max).
func scoreColorClass(score, max float64) string {
	if max <= 0 {
		return "red"
	}
	pct := score / max * 100
	return scoreColorClassPct(pct)
}

// scoreColorClassPct returns a CSS class based on a 0-100 percentage.
func scoreColorClassPct(pct float64) string {
	if pct >= 70 {
		return "green"
	}
	if pct >= 40 {
		return "amber"
	}
	return "red"
}

// gradeBgClass returns a CSS background class for a letter grade.
func gradeBgClass(grade string) string {
	switch grade {
	case "S", "A":
		return "bg-green"
	case "B", "C":
		return "bg-amber"
	default:
		return "bg-red"
	}
}

// categoryBadgeClass returns the CSS class for a category badge.
func categoryBadgeClass(cat string) string {
	switch cat {
	case "cognitive":
		return "badge badge-cog"
	case "performance":
		return "badge badge-perf"
	case "agent":
		return "badge badge-agent"
	default:
		return "badge"
	}
}

// scoreBarColor maps a CSS color class to an inline hex color for the bar fill.
func scoreBarColor(cls string) string {
	switch cls {
	case "green":
		return "#22c55e"
	case "amber":
		return "#eab308"
	default:
		return "#ef4444"
	}
}
