#!/usr/bin/env python3
"""
Evaluador de checkpoints del benchmark MemoryStress.
Computa métricas: accuracy, contradiction handling, temporal order preservation.
"""

from typing import List, Dict, Any, Optional


def exact_match(predicted: str, ground_truth: str) -> bool:
    """
    Verifica si la predicción coincide (case-insensitive, substring).
    """
    gt_lower = ground_truth.lower().strip()
    pred_lower = predicted.lower().strip()
    return gt_lower in pred_lower or any(w in pred_lower for w in gt_lower.split())


def evaluate_question(
    question: Dict[str, Any], search_results: List[Dict[str, Any]]
) -> Dict[str, Any]:
    """
    Evalúa una pregunta individual contra los resultados de búsqueda.

    Args:
        question: Dict con 'question' y 'answer'
        search_results: Lista de resultados de búsqueda con 'content', 'title'

    Returns:
        Dict con 'correct', 'answer_found', 'confidence'
    """
    answer = question.get("answer", "")
    q_type = question.get("type", "unknown")

    # Buscar exactamente la respuesta en los resultados
    found = False
    best_match_content = ""

    for result in search_results:
        content = (result.get("content", "") + " " + result.get("title", "")).lower()
        if exact_match(answer, content):
            found = True
            best_match_content = content
            break

    return {
        "correct": found,
        "type": q_type,
        "answer": answer,
        "found_content": best_match_content[:200] if best_match_content else "",
    }


def evaluate_contradiction_handling(
    question: Dict[str, Any], search_results: List[Dict[str, Any]], contradiction_chains: List[Dict[str, Any]]
) -> Dict[str, Any]:
    """
    Verifica si el sistema devuelve la versión actualizada de un hecho (en preguntas de tipo 'contradiction').

    Args:
        question: Pregunta de tipo 'contradiction'
        search_results: Resultados de búsqueda
        contradiction_chains: Cadenas de contradicción del dataset

    Returns:
        Dict con 'uses_updated_version', 'uses_original_version', 'contradiction_chain_id'
    """
    result = {
        "uses_updated_version": False,
        "uses_original_version": False,
        "contradiction_chain_id": None,
    }

    if question.get("type") != "contradiction":
        return result

    # Combinar todos los resultados en un solo texto
    combined = " ".join([r.get("content", "") + " " + r.get("title", "") for r in search_results])

    # Buscar cadenas de contradicción relevantes
    for chain in contradiction_chains:
        updates = chain.get("updates", [])
        if len(updates) < 2:
            continue

        original = updates[0].get("value", "").lower()
        latest = updates[-1].get("value", "").lower()

        # Check if the combined result uses the updated version
        if latest and latest in combined.lower():
            result["uses_updated_version"] = True
            result["contradiction_chain_id"] = chain.get("chain_id")
            break
        # Or the original version
        elif original and original in combined.lower():
            result["uses_original_version"] = True
            result["contradiction_chain_id"] = chain.get("chain_id")

    return result


def evaluate_temporal_order(
    question: Dict[str, Any], search_results: List[Dict[str, Any]]
) -> Dict[str, Any]:
    """
    Para preguntas de tipo 'temporal-order', verifica si el contexto recuperado
    preserva información temporal (cuándo ocurrió cada cosa).

    Args:
        question: Pregunta de tipo 'temporal-order'
        search_results: Resultados de búsqueda

    Returns:
        Dict con 'has_temporal_info', 'temporal_markers'
    """
    result = {
        "has_temporal_info": False,
        "temporal_markers": [],
    }

    if question.get("type") != "temporal-order":
        return result

    temporal_keywords = [
        "session",
        "first",
        "second",
        "then",
        "later",
        "before",
        "after",
        "when",
        "during",
        "initially",
    ]

    combined = " ".join([r.get("content", "") + " " + r.get("title", "") for r in search_results])

    for keyword in temporal_keywords:
        if keyword in combined.lower():
            result["temporal_markers"].append(keyword)

    result["has_temporal_info"] = len(result["temporal_markers"]) > 0

    return result


def evaluate_checkpoints(
    questions_by_checkpoint: List[List[Dict[str, Any]]],
    search_results_by_question: List[List[Dict[str, Any]]],
    contradiction_chains: List[Dict[str, Any]],
) -> Dict[str, Any]:
    """
    Evalúa todos los checkpoints.

    Args:
        questions_by_checkpoint: Preguntas agrupadas por checkpoint
        search_results_by_question: Resultados de búsqueda correspondientes
        contradiction_chains: Cadenas de contradicción del dataset

    Returns:
        Dict con métricas agregadas
    """
    metrics = {
        "checkpoints": {},
        "by_type": {},
        "contradiction_analysis": {},
        "temporal_analysis": {},
    }

    flat_q_idx = 0
    for cp_idx, questions in enumerate(questions_by_checkpoint):
        cp_metrics = {
            "total": len(questions),
            "correct": 0,
            "by_type": {},
        }

        for question in questions:
            search_results = search_results_by_question[flat_q_idx] if flat_q_idx < len(
                search_results_by_question
            ) else []
            flat_q_idx += 1

            # Evaluate exactness
            eval_result = evaluate_question(question, search_results)
            if eval_result["correct"]:
                cp_metrics["correct"] += 1

            # Track by type
            q_type = eval_result["type"]
            if q_type not in cp_metrics["by_type"]:
                cp_metrics["by_type"][q_type] = {"correct": 0, "total": 0}
            cp_metrics["by_type"][q_type]["total"] += 1
            if eval_result["correct"]:
                cp_metrics["by_type"][q_type]["correct"] += 1

            # Global type tracking
            if q_type not in metrics["by_type"]:
                metrics["by_type"][q_type] = {"correct": 0, "total": 0}
            metrics["by_type"][q_type]["total"] += 1
            if eval_result["correct"]:
                metrics["by_type"][q_type]["correct"] += 1

            # Contradiction analysis
            if q_type == "contradiction":
                contra_result = evaluate_contradiction_handling(question, search_results, contradiction_chains)
                if "contradiction" not in metrics["contradiction_analysis"]:
                    metrics["contradiction_analysis"]["contradiction"] = {
                        "uses_updated": 0,
                        "uses_original": 0,
                        "total": 0,
                    }
                metrics["contradiction_analysis"]["contradiction"]["total"] += 1
                if contra_result["uses_updated_version"]:
                    metrics["contradiction_analysis"]["contradiction"]["uses_updated"] += 1
                if contra_result["uses_original_version"]:
                    metrics["contradiction_analysis"]["contradiction"]["uses_original"] += 1

            # Temporal analysis
            if q_type == "temporal-order":
                temporal_result = evaluate_temporal_order(question, search_results)
                if "temporal-order" not in metrics["temporal_analysis"]:
                    metrics["temporal_analysis"]["temporal-order"] = {
                        "has_temporal_info": 0,
                        "total": 0,
                    }
                metrics["temporal_analysis"]["temporal-order"]["total"] += 1
                if temporal_result["has_temporal_info"]:
                    metrics["temporal_analysis"]["temporal-order"]["has_temporal_info"] += 1

        cp_accuracy = cp_metrics["correct"] / cp_metrics["total"] if cp_metrics["total"] > 0 else 0
        cp_metrics["accuracy"] = cp_accuracy
        metrics["checkpoints"][f"checkpoint_{cp_idx}"] = cp_metrics

    return metrics
