# Recall Merge Fix — DDD + Spec-Driven Artifacts

> Hybrid search merge en Neurox: cambiar de merge FTS-anchored con conditional fill (efectivamente intersección cuando FTS satura el límite) a unión explícita, y de `max(FTS, semantic)` a Reciprocal Rank Fusion. Levanta los puntos débiles de LongMemEval-S (preference 80%, multi-session recall_all 84.49%) y hace removible el namespace backfill band-aid.

**Status:** Drafting — pending coder implementation  
**Owner:** TBD  
**Last updated:** 2026-06-03  
**Estimation:** ~1.5–2h total (config wiring añade ~30min)

## What this is for

- **Reader (coder/manager):** encuentra toda la información necesaria para implementar el fix sin reinterpretar
- **Reader (reviewer):** tiene el contrato de comportamiento y los gates de aceptación objetivos
- **Reader (futuro):** entiende por qué se tomaron las decisiones, qué quedó fuera, y cómo extender

## What this is NOT

- **No es** un tutorial de DDD ni de RRF — se asumen los conceptos
- **No es** un benchmark suite — la construcción del benchmark labeled es un task aparte
- **No es** un plan de remoción del namespace backfill — esa decisión está gated al stretch diagnóstico

## Sections

| # | Doc | Propósito |
|---|---|---|
| 1 | [Domain Model](./01-domain-model.md) | Vocabulario, pipeline real de `Search()`, decisiones de diseño |
| 2 | [Specs](./02-specs/) | Contratos de comportamiento (Given/When/Then) para los 4 cambios |
| 3 | [Acceptance Gates](./03-acceptance-gates.md) | 6 PASS/FAIL objetivos sobre LongMemEval-S |
| 4 | [Task Breakdown](./04-task-breakdown.md) | 4 subtasks con orden de dependencia y comandos reales |
| 5 | [Out of Scope](./05-out-of-scope.md) | Frontera explícita + 5 riesgos conocidos |

## TL;DR del approach

**Baseline honesto (LongMemEval-S, Marzo 2026, 470q):**  
overall recall\_any@5 = 95.96%, preference = 80%, multi-session recall\_all@5 = 84.49%, ndcg@5 = 88.13%.  
El baseline "33% / Grade F" está históricamente obsoleto — fue corregido por backfill band-aid → 97.6/S.  
El fix apunta a los dos puntos débiles que el band-aid no cubre: preference y multi-session.

**El bug real — limit-saturation:**  
`engine.go:152-210` implementa merge FTS-anchored con conditional semantic-only fill (Phase 2: líneas 173–208).  
Cuando FTS devuelve ≥ `limit` candidatos, `len(candidates) < limit` es **false** → Phase 2 no corre → semantic-only fuertes se descartan silenciosamente.  
Esto NO es "FTS vacío" (ya manejado por Phase 2) — es **saturación por el límite de FTS**.

**Fix:**  
(A) **Unión explícita:** computar `union(FTS, semantic)` **antes** de truncar, luego limitar al final después de scoring.  
(B) **Reciprocal Rank Fusion:** reemplazar `max(ftsRelevance, SemanticScore)` (`scoring.go:61-65`) por `rrf(rank_fts, rank_sem, k)`.  
RRF requiere derivar ranks explícitamente (**net-new work** — ningún canal expone ranks hoy):  
- `semanticSearch` retorna `map[string]float64` (ID→cosine); ordenar desc → asignar rank 1-based  
- FTS result order → rank (1er row = rank 1)  
`k=60` default zero-shot (Bruch et al. 2022, arXiv:2210.11934 — RRF gana zero-shot; CC superior con labeled data que Neurox no tiene).  
`k` configurable via `RecallConfig.RRF.K` (**struct a crear** — no existe en el codebase).

**Scope acotado:**  
`crossSignalBoost=1.2x` y todos los demás multipliers (tri-factor, temporal, typeIntent, namespaceBackfill) **permanecen** — RRF reemplaza sólo el término `relevance` en `scoring.go:61-65`.

**Validación:** 6 gates sobre LongMemEval-S. Gate 6 (stretch) = medir Cross-Session interno ≥95/S con backfill desactivado; no shippear remoción.

## Decisiones heredadas de la sesión de grill-me

| Decisión | Type | Source | Nota |
|---|---|---|---|
| Scope: unión + RRF (no unión ingenua) | grill-me decision | Round 1 user confirmation | RRF como operación de union-recall |
| 6 acceptance gates sobre LongMemEval-S | grill-me decision | Round 2 user input | Baseline correcto (no 33%) |
| `k` configurable, default 60, struct preparado para per-channel | grill-me decision | Round 3 user input | `RRFConfig{K int}` → futuro `{KFts, KSem}` |
| DisableBackfill como flag de diagnóstico, no remoción | grill-me decision | Round 3 user input | Medir ≠ shippear |
| crossSignalBoost y demás multipliers NO se tocan | code fact | Verified engine.go:69-74, Jun 2026 | Fuera de scope frozen |
| RecallConfig debe CREARSE (no existe) | code fact | Verified config.go:14, Jun 2026 | Wiring manual en `applyEnvOverrides`; NO struct tags env:/default: |
| Rank derivation es net-new work | code fact | Verified semantic.go:28, engine.go:140-150, Jun 2026 | `semanticSearch` retorna scores, no ranks; FTS retorna rows ordenados, no ranks numerados |
| `shouldNamespaceBackfill` gate conditions | code fact | Verified engine.go:318-326, Jun 2026 | Returns false ONLY when Namespace=="" OR currentCount≥limit OR files present OR query<3 words |
| `config.Load` signature unchanged (no new return type) | code fact | Verified config.go:82, Jun 2026 | Already returns `(Config, error)`; K validation fits existing return pattern |
| Engine uses functional options for DI | code fact | Verified engine.go:88-113, Jun 2026 | `WithRecallConfig` mirrors `WithFactStore` — single injection point |
