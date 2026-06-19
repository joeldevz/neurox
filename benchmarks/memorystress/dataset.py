#!/usr/bin/env python3
"""
Dataset loader/generator para MemoryStress benchmark.
Genera un dataset sintético con la estructura esperada si no puedes descargar de HuggingFace.
"""

import json
import argparse
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any


class SyntheticMemoryStressDataset:
    """Genera un dataset sintético compatible con MemoryStress."""

    def __init__(self, num_sessions: int = 50, num_facts: int = 30, num_contradictions: int = 5):
        self.num_sessions = num_sessions
        self.num_facts = num_facts
        self.num_contradictions = num_contradictions
        self.data = {
            "facts": [],
            "sessions": [],
            "contradiction_chains": [],
            "checkpoints": [],
        }

    def generate_facts(self):
        """Genera hechos de base en 6 categorías."""
        categories = [
            "preference",
            "decision",
            "technical_fact",
            "personal_info",
            "event",
            "relationship",
        ]
        preferences_templates = [
            "User prefers {mode} mode",
            "User likes {tool} for {task}",
            "User's favorite language is {lang}",
            "User prefers {approach} over {alternative}",
        ]
        decisions_templates = [
            "User decided to use {tech}",
            "Team decided on {approach}",
            "Project uses {framework}",
        ]
        technical_templates = [
            "The {component} is written in {lang}",
            "The system handles {metric} with {value}",
            "The API returns {format} format",
        ]
        event_templates = [
            "User attended {event} on {date}",
            "Team shipped feature {feature}",
            "Incident {incident} occurred",
        ]

        fact_id = 0
        # Preferences
        modes = ["dark", "light"]
        tools = ["VSCode", "Vim", "Neovim", "IntelliJ"]
        tasks = ["editing", "debugging", "refactoring"]
        langs = ["Python", "Go", "TypeScript", "Rust"]
        for i in range(5):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "preference",
                    "text": f"User prefers {modes[i % 2]} mode",
                }
            )
        for i in range(5):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "preference",
                    "text": f"User likes {tools[i % len(tools)]} for {tasks[i % len(tasks)]}",
                }
            )

        # Decisions
        frameworks = ["React", "Vue", "Angular", "Svelte"]
        approaches = ["TDD", "BDD", "DDD", "Agile"]
        for i in range(5):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "decision",
                    "text": f"Team decided on {approaches[i % len(approaches)]} methodology",
                }
            )

        # Technical facts
        components = ["API", "Database", "Frontend", "Cache"]
        for i in range(5):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "technical_fact",
                    "text": f"The {components[i % len(components)]} is written in Go",
                }
            )

        # Personal info
        for i in range(3):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "personal_info",
                    "text": f"User has {3 + i} years of experience",
                }
            )

        # Events
        for i in range(3):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "event",
                    "text": f"Team shipped feature Feature-{i + 1}",
                }
            )

        # Relationships
        colleagues = ["Alice", "Bob", "Charlie", "Diana"]
        for i in range(4):
            fact_id += 1
            self.data["facts"].append(
                {
                    "id": f"f{fact_id:03d}",
                    "category": "relationship",
                    "text": f"User works with {colleagues[i]} on the backend team",
                }
            )

    def generate_sessions(self):
        """Genera sesiones longitudinales."""
        base_date = datetime(2024, 1, 1)
        days_per_session = 10 * 30 // self.num_sessions  # Spread across 10 months

        conversation_templates = [
            ("user", "How should I handle {topic}?"),
            ("assistant", "Based on your preferences, I recommend {recommendation}. You mentioned you prefer {fact}."),
            ("user", "That makes sense. But I'm thinking about {topic2}."),
            ("assistant", "That's a good point. Let me consider the context..."),
            ("user", "What do you remember about {context}?"),
            ("assistant", "I recall that {memory}. Has anything changed?"),
        ]

        topics = [
            "error handling",
            "code organization",
            "performance optimization",
            "testing strategies",
            "documentation",
        ]
        contexts = [
            "the database migration",
            "the API redesign",
            "the team structure",
            "the project timeline",
            "the release plan",
        ]

        for session_num in range(1, self.num_sessions + 1):
            date_offset = (session_num - 1) * days_per_session
            turns = []

            # Simulate 4-6 turns per session
            for turn_idx in range(4 + (session_num % 3)):
                template = conversation_templates[turn_idx % len(conversation_templates)]
                role = template[0]
                text = template[1]

                topic_idx = turn_idx % len(topics)
                topic2_idx = (turn_idx + 1) % len(topics)
                context_idx = turn_idx % len(contexts)

                # Format based on template
                try:
                    text = text.format(
                        topic=topics[topic_idx],
                        topic2=topics[topic2_idx],
                        recommendation="using TDD",
                        fact="dark mode and Go",
                        context=contexts[context_idx],
                        memory="you prefer dark mode and automated tests",
                    )
                except KeyError:
                    # Template doesn't need all placeholders
                    pass

                turns.append({"role": role, "content": text})

            # Facts introduced in this session (sparse)
            facts_introduced = []
            if session_num % 5 == 0:  # Every 5 sessions
                idx = (session_num // 5) % len(self.data["facts"])
                facts_introduced.append(self.data["facts"][idx]["id"])

            # Facts contradicted in this session
            facts_contradicted = []
            if session_num > 20 and session_num % 15 == 0:  # After session 20, every 15
                idx = (session_num // 15) % (len(self.data["facts"]) - 1)
                facts_contradicted.append(self.data["facts"][idx]["id"])

            self.data["sessions"].append(
                {
                    "session_id": session_num,
                    "date_offset_days": date_offset,
                    "turns": turns,
                    "facts_introduced": facts_introduced,
                    "facts_contradicted": facts_contradicted,
                }
            )

    def generate_contradictions(self):
        """Genera cadenas de contradicción (fact updates)."""
        contradiction_points = [
            (5, 15, "update"),  # session 5->15: update
            (10, 25, "reversal"),  # session 10->25: reversal
            (20, 40, "partial_change"),  # session 20->40: partial
            (30, 45, "accumulation"),  # session 30->45: accumulation
            (40, 50, "update"),  # session 40->50: update
        ]

        for chain_idx, (start_session, end_session, update_type) in enumerate(contradiction_points):
            if chain_idx >= self.num_contradictions:
                break

            fact_idx = chain_idx % len(self.data["facts"])
            fact_id = self.data["facts"][fact_idx]["id"]
            original_value = self.data["facts"][fact_idx]["text"]

            updates = []

            # Original state at session 0
            updates.append(
                {
                    "session": 0,
                    "value": original_value,
                    "type": "original",
                }
            )

            # Update at end_session
            if update_type == "update":
                new_value = original_value.replace("dark", "light").replace("Go", "Python")
            elif update_type == "reversal":
                new_value = original_value.replace("prefers", "now dislikes")
            elif update_type == "partial_change":
                new_value = original_value + " (but considering alternatives)"
            else:  # accumulation
                new_value = original_value + " along with several other tools"

            updates.append(
                {
                    "session": end_session,
                    "value": new_value,
                    "type": update_type,
                }
            )

            self.data["contradiction_chains"].append(
                {
                    "chain_id": f"c{chain_idx + 1:03d}",
                    "fact_id": fact_id,
                    "updates": updates,
                }
            )

    def generate_checkpoints(self):
        """Genera checkpoints con preguntas."""
        # Adapt checkpoint sessions based on num_sessions
        if self.num_sessions >= 50:
            checkpoint_sessions = [min(50, self.num_sessions)]
        if self.num_sessions >= 200:
            checkpoint_sessions = [50, min(200, self.num_sessions)]
        if self.num_sessions >= 500:
            checkpoint_sessions = [50, 200, min(500, self.num_sessions)]
        if self.num_sessions >= 1000:
            checkpoint_sessions = [50, 200, 500, 1000]
        else:
            checkpoint_sessions = []
            if self.num_sessions >= 50:
                checkpoint_sessions.append(self.num_sessions)

        question_types = [
            "direct-recall",
            "cross-session",
            "temporal-order",
            "contradiction",
            "cold-start-recovery",
            "preference-drift",
            "relationship-chain",
        ]

        for checkpoint_idx, session_num in enumerate(checkpoint_sessions):
            if session_num > self.num_sessions:
                continue

            questions = []
            for q_idx in range(10):  # 10 questions per checkpoint
                q_type = question_types[q_idx % len(question_types)]

                if q_type == "direct-recall":
                    q = "What display mode does the user prefer?"
                    answer = "dark mode"
                elif q_type == "cross-session":
                    q = "What tool does the user use for editing?"
                    answer = "VSCode"
                elif q_type == "temporal-order":
                    q = "What was the first decision the team made?"
                    answer = "TDD methodology"
                elif q_type == "contradiction":
                    q = "Has the user's preference changed? If so, how?"
                    answer = "Yes, updated to light mode"
                elif q_type == "cold-start-recovery":
                    q = "Summarize the user's technical background."
                    answer = "Go and Python developer, prefers dark mode"
                elif q_type == "preference-drift":
                    q = "How have the user's tool preferences evolved?"
                    answer = "Started with VSCode, considered alternatives"
                else:  # relationship-chain
                    q = "Who does the user work with and on what?"
                    answer = "Alice, Bob, Charlie on backend"

                # Determine when this fact was introduced (for scoring)
                introduced_at_session = max(1, session_num - 50 * (q_idx + 1) % 3)
                last_updated_session = session_num - (10 * (q_idx % 3))

                questions.append(
                    {
                        "q_id": f"q{checkpoint_idx:02d}{q_idx:02d}",
                        "type": q_type,
                        "question": q,
                        "answer": answer,
                        "introduced_at_session": min(introduced_at_session, session_num),
                        "last_updated_session": min(last_updated_session, session_num),
                    }
                )

            self.data["checkpoints"].append(
                {
                    "at_session": session_num,
                    "questions": questions,
                }
            )

    def generate(self) -> dict[str, Any]:
        """Genera el dataset completo."""
        self.generate_facts()
        self.generate_sessions()
        self.generate_contradictions()
        self.generate_checkpoints()
        return self.data


def load_synthetic_dataset(output_file: str = "data/memorystress_synthetic.json") -> dict:
    """Carga el dataset sintético (genera si no existe)."""
    path = Path(output_file)
    if path.exists():
        with open(path) as f:
            return json.load(f)

    print(f"Generating synthetic dataset to {output_file}...")
    generator = SyntheticMemoryStressDataset(num_sessions=50, num_facts=30, num_contradictions=5)
    data = generator.generate()

    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w") as f:
        json.dump(data, f, indent=2)

    print(f"Generated: {len(data['sessions'])} sessions, {len(data['facts'])} facts, {len(data['contradiction_chains'])} contradiction chains")
    return data


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="MemoryStress dataset generator")
    parser.add_argument("--generate", action="store_true", help="Generate synthetic dataset")
    parser.add_argument("--output", default="data/memorystress_synthetic.json", help="Output file path")
    args = parser.parse_args()

    if args.generate:
        load_synthetic_dataset(args.output)
        print(f"Dataset saved to {args.output}")
    else:
        # Just load
        data = load_synthetic_dataset(args.output)
        print(f"Loaded dataset: {len(data['sessions'])} sessions")
