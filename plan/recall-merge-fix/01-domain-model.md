# Domain Model — Recall Merge Fix

> **Nota de arquitectura:** Neurox es Go procedimental, no DDD con aggregate roots, ports, ni eventos de dominio. Este documento usa vocabulario DDD como herramienta conceptual para comunicar el diseño, pero los conceptos se mapean a funciones y structs Go concretos — no a interfaces Java-style ni a event buses. Todo lo descrito aquí existe como código en `internal/recall/`.

---

## Bounded Context: Hybrid Recall

El contexto cubre la búsqueda híbrida de observaciones: combinar señales FTS (BM25 via sqlite-fts5) y semánticas (cosine similarity sobre embeddings) en un ranking unificado.

### Ubiquitous Language

| Término | Definición |
|---|---|
| **Candidate** | `candidate` struct en `engine.go`: una observación con scores parciales (RawRelevance, SemanticScore) antes del scoring final |
| **FTS result** | Fila retornada por la query sqlite-fts5 en `buildSearchQuery`; el orden de retorno (BM25 desc) define el **FTS rank** |
| **Semantic result** | Entrada en `map[string]float64` retornado por `semanticSearch` (semantic.go:28); los valores son cosine similarity scores |
| **FTS rank** | Posición 1-based derivada del orden de resultado FTS (1er resultado = rank 1). **Net-new work** — no existe hoy |
| **Semantic rank** | Posición 1-based derivada de ordenar el cosine map descendente (score mayor = rank 1; tie-break por ID ascendente). **Net-new work** — no existe hoy |
| **Hybrid merge** | Bloque `engine.go:152-210`: FTS-anchored con conditional semantic-only fill. **Bug:** limit-saturation |
| **Limit-saturation bug** | Cuando FTS retorna ≥ `limit` candidatos, el Phase 2 (semantic-only fill, líneas 173-208) no corre → semantic-only fuertes se pierden silenciosamente |
| **Union merge (target)** | Reemplazo: computar `union(FTS, semantic)` antes de truncar al límite; truncar al final, después de scoring |
| **Tri-factor score** | `scoring.go:67`: `Recency×0.3 + Importance×0.3 + Relevance×0.4` (half-life 30d). Permanece sin cambios |
| **Relevance term** | El componente `Relevance` del tri-factor: actualmente `max(ftsRelevance, SemanticScore)` (scoring.go:61-65). **Reemplazar con RRF** |
| **RRF score** | `1/(k + rank_fts) + 1/(k + rank_sem)` cuando en ambos canales; `1/(k + rank_x)` cuando solo en uno. k configurable, default 60 |
| **crossSignalBoost** | Multiplicador 1.2x (scoring.go:69-74): **permanece, NO se toca** en este task |
| **Namespace backfill** | `shouldNamespaceBackfill` gate (engine.go:318-326) + `loadNamespaceBackfill` (semantic.go:218-315). Band-aid que cubre parte del bug bajo condiciones estrechas. Penaliza ×0.35 (scoring.go:91-93). Medición diagnóstica IN; remoción OUT |
| **RecallConfig** | Struct **nuevo a crear** en `internal/config/config.go`. NO existe actualmente en el codebase |
| **RRF.K** | `RecallConfig.RRF.K int`: k del RRF, default 60. Estructura `RRFConfig{K int}` para future-proof hacia `{KFts, KSem}` |
| **DisableBackfill** | `RecallConfig.DisableBackfill bool`: flag de diagnóstico, default false. Solo para correr medición diagnóstica; no shippear remoción |

---

## Pipeline real de `Engine.Search()` — mapeo a código

```
Search(ctx, SearchOptions)                   ← engine.go:115
  │
  ├─ normalizeSearchOptions()                ← validación + defaults
  ├─ DetectTemporalIntent()                  ← temporal intent detection
  ├─ buildSearchQuery() → FTS SQL query      ← sqlite-fts5 (BM25, ordered by relevance desc)
  ├─ scan FTS rows → []candidate             ← engine.go:140-150
  │
  ├─ [if embeddings available]               ← engine.go:154
  │   ├─ semanticSearch(limit*2)             ← semantic.go:28 — returns map[string]float64 (ID→cosine)
  │   ├─ Phase 1 (162-171): boost FTS candidates con semScores (unchanged)
  │   └─ Phase 2 (173-208): fill slots SOLO IF len(candidates) < limit  ← BUG AQUÍ (limit-saturation)
  │       └─ loadObservationsByIDs para semantic-only
  │
  ├─ searchFacts() / merge fact candidates   ← engine.go:212-232  ← THIRD SOURCE (see note below)
  │
  ├─ shouldNamespaceBackfill() gate          ← engine.go:318-326
  │   └─ loadNamespaceBackfill() if true     ← semantic.go:218-315
  │
  ├─ loadCandidateMentions()                 ← temporal mentions
  │
  ├─ applyScores(candidates, weights, now, intent, mentionMap, debug, query)
  │   ├─ relevance = max(ftsRelevance, SemanticScore)  ← scoring.go:61-65  → REEMPLAZAR CON RRF
  │   ├─ tri-factor score (scoring.go:67)
  │   ├─ crossSignalBoost ×1.2              ← scoring.go:69-74  (PERMANECE)
  │   ├─ temporalMultiplier                 ← scoring.go:81-82  (PERMANECE)
  │   ├─ typeIntentBoost ×1.3               ← scoring.go:84-89  (PERMANECE)
  │   └─ namespaceBackfillBoost ×0.35       ← scoring.go:91-93  (PERMANECE)
  │
  ├─ sort by Score desc (stable)
  ├─ [truncate to limit — HAPPENS HERE, not before]
  └─ bumpAccess(ids)
```

> **Facts — Third Candidate Source (scope boundary):** `searchFacts` (engine.go:212-232) is a **third distinct source**, separate from FTS observations and semantic observations. Facts are appended **after** the FTS∪semantic union block (152-210) completes, deduped by observation ID, with `RawRelevance=0`, and ranked by the existing tri-factor (importance/recency) in `applyScores`. Facts are **NOT** part of the union merge and are **NOT** subject to RRF in this task. The union merge change must preserve the fact-integration block (212-232) untouched and in its current position.

**Estado actual del merge (engine.go:152-210):**  
FTS-anchored con conditional semantic-only fill — efectivamente una intersección cuando FTS satura el límite, porque Phase 2 solo corre si `len(candidates) < limit`.

**Target tras el fix:**  
Unión explícita computada antes de truncar: todos los IDs de FTS ∪ todos los IDs de `semScores` cargan como candidates. Los ranks (FTS: orden de scan; Semantic: ordenar cosine map desc) se derivan en el merge block y se propagan como campos en `candidate`. `applyScores` usa los ranks para computar RRF como término `relevance`. Truncación al `limit` ocurre **después** del sort por Score final.

---

## Decisiones de diseño

### Por qué RRF y no convex combination

RRF gana en el escenario zero-shot (sin labeled validation data). Bruch et al. 2022 (arXiv:2210.11934) muestra que convex combination supera a RRF in-domain **cuando hay datos de tuning**. Neurox no los tiene. Sin alpha tuneado, CC es frágil (sensible a normalización, no transferible out-of-domain). RRF con k=60 es el default validado en cientos de sistemas en producción.

Limitación conocida: RRF descarta magnitud — doc #1 con cosine 0.99 = doc #1 con cosine 0.51. Aceptado para este task (el problema es recall, no precisión de ranking).

### Por qué k debe ser configurable

k=60 es el default zero-shot. El óptimo es per-corpus y puede variar varios puntos NDCG. `RRFConfig{K int}` hoy → extensible a `{KFts, KSem int}` en follow-up sin breaking change en YAML keys ni en la API existente.

### Por qué crossSignalBoost permanece

`crossSignalBoost=1.2x` (scoring.go:69-74) no está en el scope frozen de este task. RRF reemplaza **solo** el término `relevance` (scoring.go:61-65). Los demás multipliers del tri-factor pipeline no se tocan.

### Por qué backfill removal es OUT

El backfill dispara bajo condiciones estrechas (namespace seteado + count<limit + sin file filters + query ≥3 words). Medir con `DisableBackfill=true` diagnostica si el fix union+RRF es suficientemente estructural. Si ≥95/S sin backfill → fix real; si <95/S → el backfill cubre algo que falta. La remoción de producción es un task separado con su propia validación.

---

## Inventario de domain events

> Los siguientes son **ilustrativos** — el engine es síncrono y actualmente **no emite eventos**. Se incluyen aquí como referencia para observabilidad futura, no como comportamiento implementado.

- `HybridMergePerformed(query, ftsCount, semOnlyCount, totalCandidates)` — métricas/debug futuro
- `LimitSaturationDetected(query, ftsCount=limit)` — observable cuando FTS llena el límite exactamente
- `BackfillSkipped(reason)` — debug del gate `shouldNamespaceBackfill`
