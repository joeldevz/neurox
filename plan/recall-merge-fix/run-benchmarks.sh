#!/usr/bin/env bash
# ============================================================================
# run-benchmarks.sh — Execute the 6-gate acceptance suite for the
# recall-merge-fix. See 03-acceptance-gates.md and 04-task-breakdown.md.
#
# Pre-requisites:
#   - benchmarks/longmemeval/data/longmemeval_oracle.json (gitignored, manual)
#   - For -embed runs: ollama running locally with embedding model loaded
#   - For G6: the env var NEUROX_RECALL_DISABLE_BACKFILL is honored by the engine
#
# Usage:
#   chmod +x run-benchmarks.sh
#   ./run-benchmarks.sh                    # runs all 6 gates
#   ./run-benchmarks.sh --no-embed         # skip embedding-based runs
#   ./run-benchmarks.sh --gates G1,G2,G6   # run a subset
# ============================================================================

set -euo pipefail

cd "$(dirname "$0")/../.."

USE_EMBED=1
GATES="G1,G2,G3,G4,G5,G6"

for arg in "$@"; do
  case "$arg" in
    --no-embed) USE_EMBED=0 ;;
    --gates=*)  GATES="${arg#--gates=}" ;;
    *)          echo "Unknown arg: $arg" >&2; exit 1 ;;
  esac
done

DATA="benchmarks/longmemeval/data/longmemeval_oracle.json"
OUT="benchmarks/longmemeval/results.jsonl"
EMBED_FLAG=""
[ "$USE_EMBED" -eq 1 ] && EMBED_FLAG="-embed"

run_gate() {
  local gate="$1"; shift
  local desc="$1"; shift
  echo
  echo "========================================"
  echo "Gate $gate: $desc"
  echo "========================================"
  "$@"
}

# Pre-flight: data file must exist
if [ ! -f "$DATA" ]; then
  echo "ERROR: data file not found: $DATA" >&2
  echo "  Obtain from official LongMemEval source and place at the path above." >&2
  echo "  Note: file is gitignored to avoid committing large eval data." >&2
  exit 1
fi

# Pre-flight: build
echo "Building..."
CGO_ENABLED=1 go build -tags sqlite_fts5 ./...

# Pre-flight: unit tests
echo "Running unit tests..."
CGO_ENABLED=1 go test -tags sqlite_fts5 ./internal/config/... ./internal/recall/...

# Gates 1-5: LongMemEval-S
if [[ "$GATES" == *"G1"* || "$GATES" == *"G2"* || "$GATES" == *"G3"* || "$GATES" == *"G4"* || "$GATES" == *"G5"* ]]; then
  if [[ "$GATES" == *"G3"* || "$GATES" == *"G4"* || "$GATES" == *"G5"* ]]; then
    run_gate "G3+G4+G5" "Full LongMemEval-S run (overall recall_any, knowledge-update, ndcg)" \
      CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
        -data "$DATA" \
        -output "$OUT" \
        -k 10 $EMBED_FLAG
  fi
  if [[ "$GATES" == *"G1"* ]]; then
    run_gate "G1" "single-session-preference (target ≥88%)" \
      CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
        -data "$DATA" \
        -output "benchmarks/longmemeval/results_g1.jsonl" \
        -k 5 $EMBED_FLAG -type single-session-preference
  fi
  if [[ "$GATES" == *"G2"* ]]; then
    run_gate "G2" "multi-session (target ≥88%)" \
      CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
        -data "$DATA" \
        -output "benchmarks/longmemeval/results_g2.jsonl" \
        -k 5 $EMBED_FLAG -type multi-session
  fi
fi

# Gate 6: stretch (cross-session interno sin backfill)
if [[ "$GATES" == *"G6"* ]]; then
  run_gate "G6 (STRETCH)" "Cross-Session interno sin backfill (target ≥95/Elite)" \
    env NEUROX_RECALL_DISABLE_BACKFILL=true \
    CGO_ENABLED=1 go run -tags sqlite_fts5 . benchmark --dimensions "Cross-Session Memory"
fi

echo
echo "========================================"
echo "All requested gates completed."
echo "  Results: benchmarks/longmemeval/results.jsonl"
echo "  Metrics: benchmarks/longmemeval/results_metrics.json"
echo "========================================"
