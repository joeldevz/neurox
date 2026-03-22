package benchmark

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

// validCategories is the set of recognised --category values.
var validCategories = map[string]bool{
	"cognitive":   true,
	"performance": true,
	"agent":       true,
	"all":         true,
}

// RunCLI parses flags and runs the full benchmark suite.
// args should be os.Args[2:] (after "neurox benchmark").
//
// Supported flags:
//
//	--scale small|medium|large               — controls dataset size (default: small)
//	--category cognitive|performance|agent|all — filter by category (default: all)
//	--dimensions dim1,dim2,...               — comma-separated dimension names to run
//	--output path.json                       — write JSON report to this file
//	--verbose                                — print per-check details
func RunCLI(args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	scale := fs.String("scale", "small", "Scale: small, medium, large")
	output := fs.String("output", "", "JSON output file path")
	category := fs.String("category", "all", "Filter by category: cognitive, performance, agent, all")
	dimensions := fs.String("dimensions", "", "Comma-separated dimension names to run (overrides --category when set)")
	verbose := fs.Bool("verbose", false, "Show detailed check results per dimension")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	// Validate category.
	if !validCategories[*category] {
		return fmt.Errorf("invalid --category %q: must be cognitive, performance, agent, or all", *category)
	}

	cfg := NewScaleConfig(*scale)
	suite := NewSuite(cfg)

	// All registered dimensions in declaration order.
	allDims := []Dimension{
		// Cognitive (45%)
		CogKnowledgeUpdate{},
		CogSignalNoise{},
		CogCrossSession{},
		CogTemporal{},
		CogLifecycle{},
		// Performance (20%)
		PerfWrite{},
		PerfRecall{},
		PerfConcurrent{},
		PerfContext{},
		// Agent simulation (35%)
		AgentLazyVsPerfect{},
		AgentWorkflows{},
		AgentParamImpact{},
	}

	// Build the wanted-dimension set from --dimensions if provided.
	wantedDims := parseDimensions(*dimensions)

	// Register dimensions that pass both the category filter and (if set) the
	// explicit --dimensions filter.
	for _, d := range allDims {
		if !categoryMatches(d.Category(), *category) {
			continue
		}
		if len(wantedDims) > 0 && !wantedDims[d.Name()] {
			continue
		}
		suite.Register(d)
	}

	if len(suite.dims) == 0 {
		if *dimensions != "" {
			return fmt.Errorf("no dimensions matched --dimensions %q (with --category %s)", *dimensions, *category)
		}
		return fmt.Errorf("no dimensions matched category %q", *category)
	}

	// Describe selected dimensions in the header.
	dimNames := make([]string, 0, len(suite.dims))
	for _, d := range suite.dims {
		dimNames = append(dimNames, d.Name())
	}
	fmt.Fprintf(os.Stdout,
		"Running Neurox Brain Benchmark (scale=%s, category=%s, dimensions=%d: %s)...\n\n",
		cfg.Name, *category, len(suite.dims), strings.Join(dimNames, ", "),
	)

	report, err := suite.Run(context.Background())
	if err != nil {
		return fmt.Errorf("benchmark failed: %w", err)
	}

	PrintReport(report)

	if *verbose {
		// Print detailed checks for each dimension.
		fmt.Println("\n  Detailed Check Results:")
		fmt.Println("  " + strings.Repeat("─", 80))
		for _, dim := range report.Dimensions {
			for _, check := range dim.Checks {
				status := "✓"
				if !check.Passed {
					status = "✗"
				}
				fmt.Printf("    %s [%s] %s\n", status, dim.DimensionName, check.Name)
				if !check.Passed && check.Detail != "" {
					fmt.Printf("        %s\n", check.Detail)
				}
			}
		}
		fmt.Println()
	}

	if *output != "" {
		if err := ExportJSON(report, *output); err != nil {
			return fmt.Errorf("export JSON: %w", err)
		}
		fmt.Printf("Results exported to %s\n", *output)
	}

	return nil
}

// parseDimensions splits a comma-separated list of dimension names into a
// lookup set. Returns nil (not an empty map) when the input is blank so that
// callers can distinguish "no filter" from "empty filter".
func parseDimensions(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]bool, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name != "" {
			out[name] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// categoryMatches reports whether a dimension's category passes the --category
// filter. "all" always passes; otherwise the category must match exactly.
func categoryMatches(dimCategory, filterCategory string) bool {
	return filterCategory == "all" || dimCategory == filterCategory
}
