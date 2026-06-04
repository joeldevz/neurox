# 05 — Out of Scope & Known Risks

> Frontera explícita del task. Lo que NO se hace en este Jira, y por qué. 5 riesgos conocidos que el coder/manager debe tener en mente.

**Last updated:** 2026-06-03

---

## Explicitly out of scope (→ follow-up Jira)

| Item | Por qué está fuera | Cuándo se hace |
|---|---|---|
| **(1) Remoción de producción del namespace backfill** | Gated al stretch gate G6 (≥95/S sin backfill). La medición diagnóstica vía `DisableBackfill` flag está IN; shippear la remoción es OUT. Remover sin validar es "remover y rezar" | Si G6 pasa, Jira follow-up pre-justificado por el stretch run |
| **(2) Tuning de k sobre labeled queries** | Sin labeled benchmark, optimizar k (`{10,30,60,90,120}`) es optimizar a ciegas. El campo de config ya está (K=60 default); el tuning espera datos | Cuando exista el labeled benchmark (task aparte) |
| **(3) Convex combination `α*FTS + (1-α)*sem`** | Gana a RRF con labeled data (Bruch et al. 2022, arXiv:2210.11934). Sin alpha tuneado, CC es frágil. RRF es el default correcto para el escenario zero-shot de Neurox | Después del labeled benchmark, si los datos muestran que CC supera a RRF en Neurox específicamente |
| **(4) Query expansion / re-ranking (MMR, cross-encoder)** | Query expansion is a separate retrieval stage that runs **before** the merge, and re-ranking runs **after** — both are orthogonal to the union-vs-intersection merge decision being fixed here. Adding either would widen scope to a different pipeline stage without validating the merge fix itself. | Task aparte, con su propio benchmark |
| **(5) Graph-hop BFS via `observation_links`** | Graph traversal requires separate link-traversal infrastructure and addresses a different recall gap (relational links, not vector similarity). Orthogonal to the FTS∪semantic union merge. | Task aparte, posiblemente después de cross-namespace recall spec |

---

## Why each item is out, not in

### (1) Remoción de backfill (the big one)

- **Tentación:** incluirla porque el stretch la mide
- **Por qué fuera:** la **medición** es barata (~20 min, un flag) y pre-justifica el follow-up. La **remoción** es medio día + re-validación de todo el stack
- **Costo de meterla:** si G6 no pasa, no sabés si el backfill era el problema o si union+RRF no llegó. El task pierde claridad
- **Costo de sacarla:** el follow-up queda pre-justificado por el stretch. Cero ambigüedad en interpretación

### (2) Tuning de k

- **Tentación:** correr `k ∈ {10, 30, 60, 90, 120}` mientras estás en el codebase
- **Por qué fuera:** sin labeled queries, no hay forma de validar qué k es mejor. Cualquier conclusión sería overfitting al dataset disponible
- **Costo de meterlo:** effort significativo (experiment harness) sin valor real
- **Costo de sacarlo:** el campo de config ya está. El tuning espera el labeled benchmark

### (3) Convex combination

- **Tentación:** "si RRF es bueno, convex debe ser mejor"
- **Por qué fuera:** convex gana **con labeled data** (Bruch et al. 2022). Sin datos para tunear `alpha`, CC es sensible a normalización y no transfiere bien out-of-domain. RRF es la decisión correcta hoy (zero-shot)
- **Costo de meterlo:** implementar y mantener dos scorers sin saber cuál usar en producción
- **Costo de sacarlo:** RRF es correcto para el escenario actual; convex es una decisión futura gated a datos

---

## Known risks (in scope, pero a tener en cuenta)

### Risk A — Union + RRF puede introducir noise en exact-match queries

**What:** La unión expande el candidate pool. Documentos con alta similitud semántica pero baja precisión exacta pueden entrar al pool y diluir el ranking de resultados precisos.

**Impact on this task:** Cubierto por el no-regression gate G3 (overall recall_any@5 ≥96%) y G5 (ndcg@5 ≥88.13%).

**Mitigation:** Los no-regression gates son el mecanismo de detección. Si alguno falla, investigar antes de cerrar.

**Status:** Cubierto por gates.

### Risk B — RRF descarta magnitud de score

**What:** RRF discards score magnitude — rank #1 con cosine 0.99 es indistinguible de rank #1 con cosine 0.51. Pierde información de confianza para zero-shot y recall problems.

**Impact on this task:** Aceptado — el problema de Neurox es recall (documentos perdidos), no precisión de ranking. RRF como "set union operation" arregla exactamente eso. Limitación documentada honestamente.

**Mitigation:** Ninguna en este task. Si después de RRF se quiere precision tuning, convex combination con labeled data es el path.

**Forward reference:** This risk is precisely what motivates the convex-combination follow-up (out-of-scope item 3); when labeled data exists, convex combination preserves score magnitude and can supersede RRF.

**Status:** Aceptado.

### Risk C — k=60 default no es óptimo per-corpus

**What:** k=60 es el consenso zero-shot, pero el óptimo varía por corpus. Independent k per channel (k_fts ≠ k_sem) puede dar +2-3% NDCG (según investigación).

**Impact on this task:** El fix usa k=60 sin tuning. Probablemente no alcanza el máximo teórico de recall, pero es el mejor default disponible sin datos de tuning.

**Mitigation:** El campo de config es el primer paso. `RRFConfig{K int}` está estructurado para crecer a `{KFts, KSem int}` sin breaking change. Tuning real espera el labeled benchmark.

**Status:** Aceptado — k=60 configurable es suficiente para este task.

### Risk D — Backfill removal downstream puede perder safety net

**What:** Si G6 pasa y se crea el follow-up Jira de remoción, y ese Jira se implementa sin el mismo rigor de gates, el backfill se remueve y puede desaparecer un safety net para edge cases que union+RRF no cubre perfectamente.

**Impact on this task:** No aplica directamente — la remoción está OUT. El riesgo es del follow-up Jira.

**Mitigation:** El follow-up Jira debe incluir el mismo rigor de gates (reproducir G1-G5 sin backfill, no solo G6). Este plan lo pre-documenta.

**Status:** Fuera de scope — aplica al follow-up.

### Risk E — Rank derivation implementation risk (sort-by-score-assign-rank)

**What:** La derivación de ranks es net-new work. Un bug en el sort (ej: no tie-breaking, off-by-one en 1-based indexing) puede producir ranks incorrectos y degradar en lugar de mejorar.

**Impact on this task:** Unit tests `TestRRFScore` y `TestRankDerivation` son el mecanismo de detección. El tie-break por ID debe ser explícito para determinismo.

**Mitigation:** Implementar rank derivation con tie-break `sort by ID ascending`, con unit tests que cubran el caso tie. Verificar que 1-based indexing es correcto (`i+1`, no `i`).

**Status:** Manejado in-task via unit tests.

---

## Open questions

Ninguna abierta. Todas las ambigüedades detectadas en grill-me están resueltas en README.md (sección "Decisiones heredadas").

Si el coder encuentra ambigüedad nueva durante implementación:
1. Documentar aquí bajo "New open questions"
2. No asumir — preguntar al manager antes de implementar

---

## Reference

- [README](./README.md) — overview y decisiones heredadas
- [Domain Model](./01-domain-model.md) — términos y pipeline real
- [Acceptance Gates](./03-acceptance-gates.md) — qué mide el éxito
- [Task Breakdown](./04-task-breakdown.md) — qué se entrega y cómo
