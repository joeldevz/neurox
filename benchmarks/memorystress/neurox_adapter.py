#!/usr/bin/env python3
"""
NeuroxAdapter: HTTP adapter para Neurox memory engine.
Implementa interface de ingestión y búsqueda contra la API HTTP de Neurox.
"""

import requests
import json
from typing import Optional, List, Dict, Any
from urllib.parse import urljoin


class NeuroxAdapter:
    """Adapter HTTP para Neurox."""

    def __init__(self, base_url: str = "http://localhost:7438", namespace: str = "memorystress"):
        """
        Inicializa el adapter.

        Args:
            base_url: URL base de Neurox (default: http://localhost:7438)
            namespace: Namespace para isolación de datos (default: memorystress)
        """
        self.base_url = base_url.rstrip("/")
        self.namespace = namespace
        self._session = requests.Session()
        # Keep alive connection for better performance
        adapter = requests.adapters.HTTPAdapter(
            pool_connections=5,
            pool_maxsize=10,
            max_retries=requests.adapters.Retry(total=1, backoff_factor=0.1)
        )
        self._session.mount('http://', adapter)
        self._session.mount('https://', adapter)
        self._session.timeout = (5, 30)  # (connect, read) timeouts

    def health_check(self) -> bool:
        """Verifica que el server esté disponible."""
        try:
            resp = self._session.get(f"{self.base_url}/health", timeout=2)
            return resp.status_code == 200
        except Exception as e:
            print(f"Health check failed: {e}")
            return False

    def ingest_session(self, session: Dict[str, Any], session_num: int) -> None:
        """
        Ingesta una sesión de conversación como observación en Neurox.

        Args:
            session: Dict con keys: turns (list of {role, content}), facts_introduced, facts_contradicted
            session_num: Número de sesión (para titulo y tags)
        """
        turns = session.get("turns", [])
        if not turns:
            return

        # Construir título
        first_turn = turns[0]["content"][:80] if turns else ""
        title = f"Session {session_num}: {first_turn}"

        # Construir contenido: join de todos los turnos
        content_parts = []
        for turn in turns:
            role = turn.get("role", "unknown").upper()
            msg = turn.get("content", "")
            content_parts.append(f"{role}: {msg}")
        content = "\n".join(content_parts)

        # Tags (as array)
        facts_intro = session.get("facts_introduced", [])
        facts_contra = session.get("facts_contradicted", [])
        tags = [f"session-{session_num}", "memorystress"]
        if facts_intro:
            tags.extend([f"introduced:{f}" for f in facts_intro])
        if facts_contra:
            tags.extend([f"contradicted:{f}" for f in facts_contra])

        # POST /api/v1/observations
        payload = {
            "title": title,
            "content": content,
            "namespace": self.namespace,
            "kind": "episodic",
            "observation_type": "discovery",
            "tags": tags,
        }

        try:
            resp = self._session.post(f"{self.base_url}/api/v1/observations", json=payload)
            if resp.status_code not in (200, 201):
                print(f"Warning: ingest_session returned {resp.status_code}: {resp.text[:200]}")
        except Exception as e:
            print(f"Error ingesting session {session_num}: {e}")

    def search(self, query: str, limit: int = 5) -> List[Dict[str, Any]]:
        """
        Busca observaciones relevantes.

        Args:
            query: Query de búsqueda
            limit: Número máximo de resultados (default: 5)

        Returns:
            Lista de resultados con keys: id, title, content, relevance, etc.
        """
        try:
            params = {"q": query, "namespace": self.namespace, "limit": limit}
            resp = self._session.get(f"{self.base_url}/api/v1/observations/search", params=params)
            if resp.status_code == 200:
                data = resp.json()
                return data.get("results", [])
            else:
                print(f"Warning: search returned {resp.status_code}")
                return []
        except Exception as e:
            print(f"Error searching: {e}")
            return []

    def trigger_consolidation(self) -> None:
        """Fuerza consolidación inmediata (útil entre fases)."""
        try:
            resp = self._session.post(f"{self.base_url}/api/v1/consolidate", json={})
            if resp.status_code not in (200, 204):
                print(f"Warning: consolidate returned {resp.status_code}")
        except Exception as e:
            print(f"Error triggering consolidation: {e}")

    def get_status(self) -> Optional[Dict[str, Any]]:
        """Obtiene el estado del sistema."""
        try:
            resp = self._session.get(f"{self.base_url}/api/v1/status")
            if resp.status_code == 200:
                return resp.json()
            return None
        except Exception as e:
            print(f"Error getting status: {e}")
            return None

    def clear(self) -> None:
        """
        Limpia todas las observaciones del namespace (via search + delete).
        Nota: requiere DELETE /api/v1/observations/{id} o equivalente.
        """
        try:
            # Buscar todas las observaciones del namespace
            params = {"q": "*", "namespace": self.namespace, "limit": 1000}
            resp = self._session.get(f"{self.base_url}/api/v1/observations/search", params=params)
            if resp.status_code == 200:
                data = resp.json()
                results = data.get("results", [])
                for obs in results:
                    obs_id = obs.get("id")
                    if obs_id:
                        try:
                            self._session.delete(f"{self.base_url}/api/v1/observations/{obs_id}")
                        except Exception:
                            pass
        except Exception as e:
            print(f"Warning: clear failed: {e}")
