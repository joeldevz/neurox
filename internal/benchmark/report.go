package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
