// Package main implements the FAMA benchmark runner for Neurox memory evaluation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	limitFlag := flag.Int("limit", 100, "Max questions to evaluate")
	configFlag := flag.String("config", "synthetic-weekly", "Config: synthetic-weekly|synthetic-monthly|synthetic-quarterly")
	outputFlag := flag.String("output", "", "Output report path (JSON)")
	namespaceFlag := flag.String("namespace", "bench-memora", "Neurox namespace for observations")
	serverURLFlag := flag.String("server", "http://localhost:7438", "Neurox API server URL")
	datasetFlag := flag.String("dataset", "", "Custom dataset JSON path (if empty, use embedded synthetic)")
	flag.Parse()

	// Load or generate dataset
	var dset *Dataset
	if *datasetFlag != "" {
		var err error
		dset, err = LoadDataset(*datasetFlag)
		if err != nil {
			log.Fatalf("Failed to load dataset: %v", err)
		}
	} else {
		embeddedPath := filepath.Join("benchmarks", "memora", "testdata", "memora_synthetic.json")
		altPath := filepath.Join("testdata", "memora_synthetic.json")

		data, err := os.ReadFile(embeddedPath)
		if err != nil {
			data, err = os.ReadFile(altPath)
			if err != nil {
				log.Fatalf("Could not load embedded dataset from %s or %s: %v", embeddedPath, altPath, err)
			}
		}

		dset = &Dataset{}
		if err := json.Unmarshal(data, dset); err != nil {
			log.Fatalf("Failed to unmarshal dataset: %v", err)
		}
	}

	fmt.Printf("NEUROX MEMORA/FAMA BENCHMARK\n")
	fmt.Printf("==============================\n\n")
	fmt.Printf("Config: %s\n", *configFlag)
	fmt.Printf("Namespace: %s\n", *namespaceFlag)
	fmt.Printf("Server: %s\n", *serverURLFlag)
	fmt.Printf("Dataset: %d users, %d questions\n", len(dset.Users), len(dset.Questions))
	fmt.Printf("Limit: %d questions\n\n", *limitFlag)

	// Create runner
	runner := NewRunner(*serverURLFlag, *namespaceFlag, dset)

	// Run benchmark
	report, err := runner.Run(*limitFlag)
	if err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}

	// Print report
	printReport(report)

	// Save JSON if requested
	if *outputFlag != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal report: %v", err)
		}
		if err := os.WriteFile(*outputFlag, data, 0644); err != nil {
			log.Fatalf("Failed to write report: %v", err)
		}
		fmt.Printf("\n✓ Report saved to %s\n", *outputFlag)
	}
}

func printReport(r *Report) {
	fmt.Printf("STANDARD ACCURACY (without FAMA penalty):\n")
	fmt.Printf("  remembering:  %d/%d (%.1f%%)\n",
		r.StandardAccuracy["remembering"].Correct,
		r.StandardAccuracy["remembering"].Total,
		r.StandardAccuracy["remembering"].Percent())
	fmt.Printf("  reasoning:    %d/%d (%.1f%%)\n",
		r.StandardAccuracy["reasoning"].Correct,
		r.StandardAccuracy["reasoning"].Total,
		r.StandardAccuracy["reasoning"].Percent())
	fmt.Printf("  recommending: %d/%d (%.1f%%)\n",
		r.StandardAccuracy["recommending"].Correct,
		r.StandardAccuracy["recommending"].Total,
		r.StandardAccuracy["recommending"].Percent())

	totalStd := r.StandardAccuracy["remembering"].Total +
		r.StandardAccuracy["reasoning"].Total +
		r.StandardAccuracy["recommending"].Total
	totalCorrectStd := r.StandardAccuracy["remembering"].Correct +
		r.StandardAccuracy["reasoning"].Correct +
		r.StandardAccuracy["recommending"].Correct
	fmt.Printf("  OVERALL:      %d/%d (%.1f%%)\n", totalCorrectStd, totalStd, float64(totalCorrectStd)*100.0/float64(totalStd))

	fmt.Printf("\nFAMA ACCURACY (penalizes stale memory use):\n")
	fmt.Printf("  remembering:  %d/%d (%.1f%%)\n",
		r.FAMAAccuracy["remembering"].Correct,
		r.FAMAAccuracy["remembering"].Total,
		r.FAMAAccuracy["remembering"].Percent())
	fmt.Printf("  reasoning:    %d/%d (%.1f%%)\n",
		r.FAMAAccuracy["reasoning"].Correct,
		r.FAMAAccuracy["reasoning"].Total,
		r.FAMAAccuracy["reasoning"].Percent())
	fmt.Printf("  recommending: %d/%d (%.1f%%)\n",
		r.FAMAAccuracy["recommending"].Correct,
		r.FAMAAccuracy["recommending"].Total,
		r.FAMAAccuracy["recommending"].Percent())

	totalFAMA := r.FAMAAccuracy["remembering"].Total +
		r.FAMAAccuracy["reasoning"].Total +
		r.FAMAAccuracy["recommending"].Total
	totalCorrectFAMA := r.FAMAAccuracy["remembering"].Correct +
		r.FAMAAccuracy["reasoning"].Correct +
		r.FAMAAccuracy["recommending"].Correct
	fmt.Printf("  OVERALL:      %d/%d (%.1f%%)\n", totalCorrectFAMA, totalFAMA, float64(totalCorrectFAMA)*100.0/float64(totalFAMA))

	fmt.Printf("\nFAMA Gap: %.1f%% (penalty from using invalidated memory)\n", r.FAMAGap)
	fmt.Printf("\nStaleness Distribution of Retrieved Observations:\n")
	fmt.Printf("  fresh:      %.1f%%\n", r.StalenessDistribution["fresh"])
	fmt.Printf("  stale:      %.1f%%\n", r.StalenessDistribution["stale"])
	fmt.Printf("  expired:    %.1f%%\n", r.StalenessDistribution["expired"])

	if len(r.Notes) > 0 {
		fmt.Printf("\nNotes:\n")
		for _, note := range r.Notes {
			fmt.Printf("  • %s\n", note)
		}
	}
}

// Accuracy represents accuracy metrics for a question type
type Accuracy struct {
	Correct int
	Total   int
}

// Percent returns the accuracy percentage
func (a *Accuracy) Percent() float64 {
	if a.Total == 0 {
		return 0
	}
	return float64(a.Correct) * 100.0 / float64(a.Total)
}
