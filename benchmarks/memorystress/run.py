#!/usr/bin/env python3
"""
CLI runner para MemoryStress benchmark con Neurox.

Uso:
  python run.py --adapter neurox --limit-sessions 100 --namespace bench-stress-test --output-dir results/
  python run.py --smoke --namespace bench-stress-smoke --output-dir /tmp/stress-results/
"""

import argparse
import json
import sys
from pathlib import Path
from tqdm import tqdm
from typing import List, Dict, Any

from neurox_adapter import NeuroxAdapter
from dataset import load_synthetic_dataset
from evaluate import evaluate_checkpoints


def main():
    parser = argparse.ArgumentParser(description="MemoryStress benchmark runner for Neurox")
    parser.add_argument("--adapter", default="neurox", choices=["neurox"], help="Memory adapter to use")
    parser.add_argument("--limit-sessions", type=int, default=None, help="Max sessions to ingest (default: all)")
    parser.add_argument("--namespace", default="memorystress", help="Namespace for isolation")
    parser.add_argument("--output-dir", default="results/", help="Output directory for reports")
    parser.add_argument("--smoke", action="store_true", help="Smoke test: 50 sessions, 1 checkpoint")
    parser.add_argument("--no-consolidate", action="store_true", help="Skip consolidation between checkpoints")
    parser.add_argument("--base-url", default="http://localhost:7438", help="Neurox server URL")
    parser.add_argument("--grade", action="store_true", help="Grade the benchmark (same as full run)")

    args = parser.parse_args()

    # Smoke mode
    if args.smoke:
        args.limit_sessions = 50
        args.namespace = "bench-stress-smoke"
        print("SMOKE MODE: 50 sessions, 1 checkpoint")

    # Paths
    output_path = Path(args.output_dir)
    output_path.mkdir(parents=True, exist_ok=True)

    # Load dataset
    print("Loading dataset...")
    dataset = load_synthetic_dataset()

    # Initialize adapter
    print(f"Initializing adapter (base_url={args.base_url}, namespace={args.namespace})...")
    adapter = NeuroxAdapter(base_url=args.base_url, namespace=args.namespace)

    # Health check
    if not adapter.health_check():
        print("ERROR: Neurox server is not available at {args.base_url}")
        print("Please start: neurox serve")
        sys.exit(1)

    print("✓ Server is available")

    # Ingest sessions
    sessions = dataset["sessions"]
    limit = args.limit_sessions or len(sessions)
    sessions_to_ingest = sessions[:limit]

    print(f"\nIngesting {len(sessions_to_ingest)} sessions...")
    sys.stdout.flush()
    for i, sess in enumerate(sessions_to_ingest, 1):
        try:
            adapter.ingest_session(sess, sess["session_id"])
        except Exception as e:
            print(f"  Error ingesting session {sess['session_id']}: {e}", file=sys.stderr)
        if i % 10 == 0:
            print(f"  Progress: {i}/{len(sessions_to_ingest)}")
            sys.stdout.flush()

    print("✓ Ingestion complete")

    # Evaluate checkpoints
    print("\nEvaluating checkpoints...")
    checkpoints = dataset["checkpoints"]

    # Filter checkpoints to those within session limit
    relevant_checkpoints = [cp for cp in checkpoints if cp["at_session"] <= limit]

    if not relevant_checkpoints:
        print(f"No checkpoints within session limit ({limit})")
        sys.exit(1)

    results = {
        "adapter": args.adapter,
        "namespace": args.namespace,
        "sessions_ingested": len(sessions_to_ingest),
        "dataset_stats": {
            "total_sessions": len(sessions),
            "facts": len(dataset["facts"]),
            "contradiction_chains": len(dataset["contradiction_chains"]),
            "checkpoints": len(checkpoints),
        },
        "checkpoint_results": {},
        "by_question_type": {},
        "contradiction_analysis": {},
    }

    for checkpoint in relevant_checkpoints:
        cp_session = checkpoint["at_session"]
        print(f"\n  Checkpoint @ session {cp_session}...")

        # Trigger consolidation before checkpoint (unless disabled)
        if not args.no_consolidate and cp_session > 1:
            print(f"    Triggering consolidation...")
            adapter.trigger_consolidation()

        # Evaluate questions
        questions = checkpoint["questions"]
        correct = 0
        contradiction_updates_used = 0

        for q in questions:
            q_text = q["question"]

            # Search for answer
            results_list = adapter.search(q_text, limit=5)
            if not results_list:
                continue

            # Simple exact match: check if any result contains the answer
            answer = q["answer"]
            found = False
            updated_fact_used = False

            for result in results_list:
                result_text = result.get("content", "") + " " + result.get("title", "")
                if answer.lower() in result_text.lower():
                    found = True
                    # Check if this looks like it's using the updated version
                    if "light mode" in result_text and "dark mode" in q_text:
                        updated_fact_used = True
                    break

            if found:
                correct += 1
                if updated_fact_used:
                    contradiction_updates_used += 1

        # Track by question type
        by_type = {}
        for q in questions:
            q_type = q["type"]
            if q_type not in by_type:
                by_type[q_type] = {"total": 0, "correct": 0}
            by_type[q_type]["total"] += 1

            # Re-evaluate for this type
            answer = q["answer"]
            results_list = adapter.search(q["question"], limit=5)
            if results_list:
                for result in results_list:
                    result_text = result.get("content", "") + " " + result.get("title", "")
                    if answer.lower() in result_text.lower():
                        by_type[q_type]["correct"] += 1
                        break

        checkpoint_accuracy = correct / len(questions) if questions else 0
        results["checkpoint_results"][f"session_{cp_session}"] = {
            "correct": correct,
            "total": len(questions),
            "accuracy": f"{checkpoint_accuracy:.1%}",
        }

        # Merge by_type into global results
        for qtype, stats in by_type.items():
            if qtype not in results["by_question_type"]:
                results["by_question_type"][qtype] = {"correct": 0, "total": 0}
            results["by_question_type"][qtype]["correct"] += stats["correct"]
            results["by_question_type"][qtype]["total"] += stats["total"]

        # Contradiction handling
        if len(questions) > 0:
            results["contradiction_analysis"][f"session_{cp_session}"] = {
                "questions_using_updated_fact": contradiction_updates_used,
                "total_questions": len(questions),
                "percentage": f"{contradiction_updates_used / len(questions):.1%}" if questions else "0%",
            }

        print(f"    Accuracy: {checkpoint_accuracy:.1%} ({correct}/{len(questions)})")

    # Final report
    print("\n" + "=" * 50)
    print("NEUROX MEMORYSTRESS REPORT")
    print("=" * 50)
    print(f"Sessions ingested: {len(sessions_to_ingest)}")
    print(f"Dataset: synthetic ({len(dataset['facts'])} facts, {len(dataset['contradiction_chains'])} contradiction chains)")
    print()

    if results["checkpoint_results"]:
        print("CHECKPOINT RESULTS:")
        for cp_key, cp_result in results["checkpoint_results"].items():
            session_num = cp_key.replace("session_", "")
            accuracy = cp_result["accuracy"]
            correct = cp_result["correct"]
            total = cp_result["total"]
            print(f"  @ session {session_num:4s}: {correct:2d}/{total} correct ({accuracy})")
    print()

    if results["by_question_type"]:
        print("BY QUESTION TYPE:")
        for qtype in sorted(results["by_question_type"].keys()):
            stats = results["by_question_type"][qtype]
            acc = stats["correct"] / stats["total"] if stats["total"] > 0 else 0
            print(f"  {qtype:20s}: {stats['correct']:2d}/{stats['total']:2d} ({acc:.1%})")
    print()

    if results["contradiction_analysis"]:
        print("CONTRADICTION HANDLING:")
        for cp_key, cp_result in results["contradiction_analysis"].items():
            session_num = cp_key.replace("session_", "")
            percentage = cp_result["percentage"]
            count = cp_result["questions_using_updated_fact"]
            total = cp_result["total_questions"]
            print(f"  @ session {session_num:4s}: {count}/{total} answers use updated fact ({percentage})")

    # Save report
    report_file = output_path / "benchmark_report.json"
    with open(report_file, "w") as f:
        json.dump(results, f, indent=2)

    print()
    print(f"Report saved to: {report_file}")


if __name__ == "__main__":
    main()
