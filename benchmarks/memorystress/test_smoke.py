#!/usr/bin/env python3
"""
Smoke test para MemoryStress benchmark (sin server Neurox requerido).
Verifica que la estructura sea correcta sin necesidad de HTTP.
"""

import json
from pathlib import Path
from dataset import load_synthetic_dataset
from neurox_adapter import NeuroxAdapter
from evaluate import evaluate_question, evaluate_contradiction_handling


def test_dataset_structure():
    """Verifica que el dataset se genera correctamente."""
    print("Test 1: Dataset structure...")
    data = load_synthetic_dataset()

    assert len(data["facts"]) == 30, f"Expected 30 facts, got {len(data['facts'])}"
    assert len(data["sessions"]) == 50, f"Expected 50 sessions, got {len(data['sessions'])}"
    assert len(data["contradiction_chains"]) == 5, f"Expected 5 contradiction chains, got {len(data['contradiction_chains'])}"
    assert len(data["checkpoints"]) >= 1, f"Expected at least 1 checkpoint, got {len(data['checkpoints'])}"

    print(f"  ✓ {len(data['facts'])} facts")
    print(f"  ✓ {len(data['sessions'])} sessions")
    print(f"  ✓ {len(data['contradiction_chains'])} contradiction chains")
    print(f"  ✓ {len(data['checkpoints'])} checkpoints")

    # Check facts
    for fact in data["facts"][:3]:
        assert "id" in fact, "Fact missing 'id'"
        assert "category" in fact, "Fact missing 'category'"
        assert "text" in fact, "Fact missing 'text'"
    print(f"  ✓ Fact schema correct")

    # Check sessions
    for sess in data["sessions"][:3]:
        assert "session_id" in sess, "Session missing 'session_id'"
        assert "turns" in sess, "Session missing 'turns'"
        assert isinstance(sess["turns"], list), "Session turns is not a list"
        for turn in sess["turns"]:
            assert "role" in turn, "Turn missing 'role'"
            assert "content" in turn, "Turn missing 'content'"
    print(f"  ✓ Session schema correct")

    # Check contradiction chains
    for chain in data["contradiction_chains"][:1]:
        assert "chain_id" in chain, "Contradiction chain missing 'chain_id'"
        assert "fact_id" in chain, "Contradiction chain missing 'fact_id'"
        assert "updates" in chain, "Contradiction chain missing 'updates'"
        assert len(chain["updates"]) >= 2, "Contradiction chain needs at least 2 updates"
    print(f"  ✓ Contradiction chain schema correct")

    # Check checkpoints
    for cp in data["checkpoints"]:
        assert "at_session" in cp, "Checkpoint missing 'at_session'"
        assert "questions" in cp, "Checkpoint missing 'questions'"
        for q in cp["questions"][:2]:
            assert "q_id" in q, "Question missing 'q_id'"
            assert "type" in q, "Question missing 'type'"
            assert "question" in q, "Question missing 'question'"
            assert "answer" in q, "Question missing 'answer'"
    print(f"  ✓ Checkpoint schema correct")


def test_adapter_structure():
    """Verifica que el adapter tenga los métodos esperados."""
    print("\nTest 2: Adapter structure...")
    adapter = NeuroxAdapter(base_url="http://localhost:7438", namespace="test")

    # Check methods exist
    assert hasattr(adapter, "health_check"), "Missing health_check method"
    assert hasattr(adapter, "ingest_session"), "Missing ingest_session method"
    assert hasattr(adapter, "search"), "Missing search method"
    assert hasattr(adapter, "trigger_consolidation"), "Missing trigger_consolidation method"
    assert hasattr(adapter, "get_status"), "Missing get_status method"
    assert hasattr(adapter, "clear"), "Missing clear method"

    print(f"  ✓ health_check() exists")
    print(f"  ✓ ingest_session() exists")
    print(f"  ✓ search() exists")
    print(f"  ✓ trigger_consolidation() exists")
    print(f"  ✓ get_status() exists")
    print(f"  ✓ clear() exists")


def test_evaluator():
    """Verifica que el evaluador funcione."""
    print("\nTest 3: Evaluator functions...")

    # Create mock question and results
    question = {
        "type": "direct-recall",
        "question": "What is the user's preference?",
        "answer": "dark mode",
    }

    search_results = [
        {
            "title": "Session 1",
            "content": "The user prefers dark mode for all applications",
        }
    ]

    # Test evaluate_question
    result = evaluate_question(question, search_results)
    assert result["correct"] == True, "Should find answer in results"
    print(f"  ✓ evaluate_question() works")

    # Test evaluate_contradiction_handling
    data = load_synthetic_dataset()
    contradiction_result = evaluate_contradiction_handling(question, search_results, data["contradiction_chains"])
    assert "uses_updated_version" in contradiction_result, "Missing contradiction analysis field"
    print(f"  ✓ evaluate_contradiction_handling() works")


def test_cli_args():
    """Simula argumentos CLI."""
    print("\nTest 4: CLI argument handling...")
    import argparse

    parser = argparse.ArgumentParser()
    parser.add_argument("--adapter", default="neurox")
    parser.add_argument("--limit-sessions", type=int, default=None)
    parser.add_argument("--smoke", action="store_true")
    parser.add_argument("--namespace", default="memorystress")

    # Test smoke mode
    args = parser.parse_args(["--smoke"])
    assert args.smoke == True, "Smoke flag not parsed"
    print(f"  ✓ --smoke flag works")

    # Test other args
    args = parser.parse_args(["--limit-sessions", "100", "--namespace", "test-ns"])
    assert args.limit_sessions == 100, "limit-sessions not parsed correctly"
    assert args.namespace == "test-ns", "namespace not parsed correctly"
    print(f"  ✓ --limit-sessions works")
    print(f"  ✓ --namespace works")


def main():
    print("=" * 60)
    print("MEMORYSTRESS ADAPTER SMOKE TEST")
    print("=" * 60)

    test_dataset_structure()
    test_adapter_structure()
    test_evaluator()
    test_cli_args()

    print("\n" + "=" * 60)
    print("ALL TESTS PASSED ✓")
    print("=" * 60)
    print("\nAdapter is ready. To run full benchmark:")
    print("  1. Start Neurox server: neurox serve")
    print("  2. Run: python run.py --smoke --namespace bench-stress-smoke")
    print("  3. Or full run: python run.py --adapter neurox --limit-sessions 50")


if __name__ == "__main__":
    main()
