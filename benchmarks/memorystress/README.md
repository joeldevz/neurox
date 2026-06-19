# MemoryStress Benchmark for Neurox

Adapter Python para el benchmark **MemoryStress** (omega-memory/memorystress, Apache-2.0).

## Dataset

MemoryStress simula 1,000 sesiones longitudinales de conversación a lo largo de 10 meses ficticios, con:
- 583 hechos en 6 categorías (preferences, decisions, technical facts, personal info, events, relationships)
- 40 cadenas de contradicción (fact updates, reversals, partial changes, accumulations)
- 300 preguntas en 4 checkpoints (sessions 50, 200, 500, 1000)
- 7 tipos de preguntas: direct-recall, cross-session, temporal-order, contradiction, cold-start-recovery, preference-drift, relationship-chain

Este adapter carga un dataset sintético (si no puedes descargar de HuggingFace) y mide:
- **Recall accuracy** por checkpoint
- **Contradiction handling**: ¿devuelve el sistema la versión actualizada o la original?
- **Temporal order**: ¿el contexto preserva información sobre cuándo ocurrió cada cosa?

## Estructura

```
benchmarks/memorystress/
├── requirements.txt           — Dependencias Python (requests, tqdm)
├── README.md                  — Este archivo
├── neurox_adapter.py          — NeuroxAdapter para HTTP API
├── dataset.py                 — Carga/genera dataset
├── run.py                     — CLI principal
├── evaluate.py                — Evaluación + métricas
└── data/
    └── memorystress_synthetic.json  — Dataset sintético (auto-generado si falta)
```

## Instalación

```bash
pip install -r requirements.txt
```

## Uso

### Generar dataset sintético

```bash
python dataset.py --generate --output data/memorystress_synthetic.json
```

### Ejecutar benchmark completo (1,000 sesiones)

```bash
# Server Neurox debe estar en http://localhost:7438
python run.py --adapter neurox --limit-sessions 1000 --namespace bench-stress-full --output-dir results/
```

### Smoke test (50 sesiones, 1 checkpoint)

```bash
python run.py --smoke --namespace bench-stress-smoke --output-dir /tmp/stress-results/
```

## Output

Genera un reporte JSON con métricas por checkpoint y por tipo de pregunta:

```
NEUROX MEMORYSTRESS REPORT
===========================
Sessions ingested: XXX
Dataset: synthetic (50 sessions, 30 facts, 5 contradiction chains)

CHECKPOINT RESULTS:
  @ session 50:   X/10 correct (XX%)
  @ session 200:  (not reached if limit < 200)

BY QUESTION TYPE:
  direct-recall:      X/N (XX%)
  contradiction:      X/N (XX%)  ← clave para Neurox
  preference-drift:   X/N (XX%)

Contradiction handling: X/N answers use updated fact (XX%)
```

## API Endpoints

Neurox debe estar disponible en `http://localhost:7438`:

- `POST /api/v1/observations` — save observation
- `GET /api/v1/observations/search?q=...&namespace=...&limit=10` — search
- `POST /api/v1/consolidate` — force consolidation
- `GET /api/v1/status` — system status

## Notas

- Si el server no está disponible, el script intenta conexión pero reporta fallo gracefully.
- El dataset sintético incluye al menos: 50 sesiones, 30 hechos, 5 cadenas de contradicción, 4 checkpoints.
- Las métricas especiales miden cómo Neurox maneja la mutación de hechos a lo largo del tiempo.
