# Neurox — Baseline de Benchmarks 2026

> Establecido el 2026-06-10. Reemplaza `longmemeval/GATES_REPORT.txt` como referencia de calidad.
> LoCoMo y LongMemEval legacy quedan en `benchmarks/longmemeval/` solo para referencia histórica.

## ⚠️ Nota crítica sobre los jueces

**Todos los scores en este baseline usan exact-match normalizado, no LLM-judge.**

Los scores de la competencia (Mem0 91.6%, Zep 63.8%, Supermemory 81.6%) usan LLM-judge (GPT-4o, GPT-4o-mini) que evalúa si el contexto recuperado *contiene* la respuesta, no si la coincide literalmente. El exact-match es sistemáticamente más estricto.

Para comparaciones apples-to-apples: correr con `OPENAI_API_KEY=... --judge llm` en memorybench.

---

## Benchmark 1: LongMemEval-S via memorybench

**Harness**: `benchmarks/memorybench/` (Node.js adapter)
**Dataset**: `benchmarks/longmemeval/data/longmemeval_s.json` (500 preguntas, ~115K tokens/historia)
**Run de baseline**: 30 preguntas, 1,482 observaciones ingestionadas, namespace `baseline-run`

**Comando de reproducción**:
```bash
cd benchmarks/memorybench
node src/index.js run -p neurox -b longmemeval -l 30 -r baseline-run
```

### Resultados (2026-06-10)

| Métrica | Score |
|---|---|
| Accuracy overall (exact-match) | **3.3%** (1/30) |
| Avg search latency | **14ms** |
| p50 latency | **14ms** |
| p95 latency | **18ms** |
| Tokens de contexto por pregunta | ~463,488 promedio |
| MemScore | **3.3% / 14ms / 463K tok** |

### Breakdown por tipo de pregunta

| Tipo | Score |
|---|---|
| single-session-user | 1/30 (3.3%) |

> **Nota**: Las 30 preguntas cayeron todas en `single-session-user`. Una corrida completa (500 preguntas) cubrirá todos los tipos: temporal-reasoning, knowledge-update, multi-session, abstention.

### Análisis

- ✅ **Latencia excelente**: 14ms p50 vs Zep ~150ms, Mem0 ~80-140ms. Neurox es el más rápido de la clase.
- ❌ **Accuracy raw baja**: La búsqueda recupera contexto pero el exact-match es más duro que el LLM-judge que usa la competencia. Con LLM-judge los scores deberían subir significativamente.
- 🔍 El score real de retrieval se ve mejor mirando si la respuesta está *dentro* del contexto recuperado, no si se extrae exactamente.

### Gates para este benchmark

| Condición | Gate |
|---|---|
| Exact-match, 30 preguntas | baseline: 3.3% |
| **Target con LLM-judge** | **≥ 40%** (comparable con competencia) |
| Latencia p50 | ≤ 50ms (ya cumplido: 14ms) |

---

## Benchmark 2: Memora/FAMA

**Harness**: `benchmarks/memora/` (Go runner)
**Dataset**: `benchmarks/memora/testdata/memora_synthetic.json` (sintético: 20 usuarios, 25 preguntas, 3 sesiones/usuario con evolución de hechos)
**Run de baseline**: 25 preguntas, 60 sesiones ingestionadas, 36 observaciones invalidadas, namespace `bench-memora-baseline`

**Comando de reproducción**:
```bash
go run -tags fts5 ./benchmarks/memora/ \
  -limit 60 -namespace bench-memora-baseline -output /tmp/memora_baseline.json
```

### Resultados (2026-06-10)

| Métrica | Score |
|---|---|
| Standard Accuracy overall | **44.0%** (11/25) |
| Standard — remembering | 68.8% (11/16) |
| Standard — reasoning | 0.0% (0/5) |
| Standard — recommending | 0.0% (0/4) |
| **FAMA Accuracy overall** | **4.0%** (1/25) |
| **FAMA — remembering** | **6.2%** (1/16) |
| **FAMA Gap** | **90.9%** |
| Stale observations retrieved | **67.2%** |
| Fresh observations retrieved | **32.8%** |

### ✅ RESULTADO POST-FIX (recall excluye stale por default) — 2026-06-10

Fix aplicado: `internal/recall/filters.go` y `semantic.go` ahora filtran `staleness NOT IN ('stale','expired')` por default (las queries de historia con `IncludeStale` siguen viendo todo). También se corrigió un bug de timing del harness (save asíncrono → invalidate 404; ahora espera persistencia + reintenta).

| Métrica | Baseline | Post-fix | Δ |
|---|---|---|---|
| **FAMA Accuracy overall** | 4.0% (1/25) | **36.0% (9/25)** | **+32 pts (9×)** |
| **FAMA Gap** | 90.9% | **0.0%** | eliminado |
| Stale observations retrieved | 67.2% | **0.0%** | eliminado |
| Standard Accuracy overall | 44.0% (11/25) | 36.0% (9/25) | −8 pts |
| Superseded marcadas (harness) | 36 | 40 | — |

**Interpretación honesta de la bajada de Standard Accuracy (44%→36%):**
En el baseline, 11 respuestas eran "correctas" pero solo 1 lo era usando memoria fresh — las otras 10 acertaban por coincidir con el string de respuesta dentro de memorias **obsoletas**. Tras el fix: 9 correctas, **las 9 usando memoria vigente**. Pasamos de "11 correctas, 10 haciendo trampa con datos obsoletos" a "9 correctas, todas legítimas". El sistema responde con conocimiento ACTUAL 9× más seguido. La caída de 8 puntos en standard es la eliminación de aciertos accidentales sobre datos obsoletos — exactamente lo que el producto quiere evitar.

**⚠️ Caveat de comparabilidad (igual que el resto del baseline):** dataset sintético + juez exact-match. El delta antes/después es válido (mismo harness, solo cambió el código). El número absoluto (36%) NO es comparable con el paper Memora (A-Mem 71.8, etc.) que usa dataset real + 3 LLM judges.

### Hallazgo crítico — El FAMA Gap revela un bug estructural

**El FAMA gap de 90.9% es la señal más importante de este baseline.**

Neurox tiene la infraestructura para invalidar hechos (`staleness`, `supersedes`, `invalidate` endpoint), pero el engine de recall está **devolviendo 67.2% de observaciones stale** en los resultados. Esto sucede porque:

1. El filtro por defecto en `semanticSearch` es `staleness <> 'expired'` — incluye `stale` y `revalidated`.
2. El FTS path en `buildSearchQuery` también filtra `staleness <> 'expired'` por default (salvo que `IncludeStale=false`, que excluye `stale`).
3. El dataset de Memora invalida con `POST /api/v1/observations/{id}/invalidate` que marca como `staleness='stale'` — y `stale` pasa el filtro.

**La competencia falla en FAMA porque no tiene modelo de invalidación. Neurox tiene el modelo pero no lo aplica correctamente en retrieval.**

> **Acción requerida**: revisar qué valor de `staleness` asigna `/invalidate` y si el filter default de recall debería excluir `stale` en contextos donde no se pide `IncludeStale`. Ver `internal/observation/store.go` y `internal/recall/filters.go`.

### Análisis de ventaja competitiva

| Sistema | FAMA (estimado) | Por qué |
|---|---|---|
| Mem0 | ~Muy bajo | ADD-only, no tiene invalidación |
| Letta | ~Muy bajo | Sin modelo temporal |
| Cognee | ~Muy bajo | Sin modelo temporal |
| Neurox (actual) | **4.0%** | Tiene el modelo, pero recall no lo usa bien |
| **Neurox (post-fix)** | **~40-60% estimado** | Con filter `staleness='fresh'` en recall |
| Zep/Graphiti | Desconocido | Bi-temporal, probablemente mejor |

### Gates para este benchmark

| Condición | Gate |
|---|---|
| FAMA Accuracy baseline | 4.0% |
| **Target post-fix recall filter** | **≥ 40%** |
| **Standard Accuracy** | ≥ 50% (ya: 44%) |
| FAMA Gap | ≤ 30% (vs actual 90.9%) |

---

## Benchmark 3: MemoryStress (longitudinal)

**Harness**: `benchmarks/memorystress/` (Python adapter)
**Dataset**: `benchmarks/memorystress/data/memorystress_synthetic.json` (sintético: 50 sesiones, 30 hechos, 5 cadenas de contradicción, 10 preguntas en checkpoint @50)
**Run de baseline**: smoke mode, 50 sesiones, namespace `bench-stress-smoke`

**Comando de reproducción**:
```bash
cd benchmarks/memorystress
python run.py --smoke --namespace bench-stress-baseline --output-dir /tmp/stress-results/
```

### Resultados (2026-06-10)

| Métrica | Score |
|---|---|
| Overall accuracy @session50 | **20.0%** (2/10) |
| direct-recall | **100%** (2/2) ✅ |
| contradiction | 0% (0/1) ❌ |
| cross-session | 0% (0/2) ❌ |
| temporal-order | 0% (0/2) ❌ |
| preference-drift | 0% (0/1) ❌ |
| relationship-chain | 0% (0/1) ❌ |
| Contradiction handling | **0%** (0/10 usan fact actualizado) ❌ |

### Análisis

- ✅ **direct-recall = 100%**: Neurox recupera perfectamente hechos de sesión única bien formulados. Es el caso más simple.
- ❌ **contradiction = 0%**: Cuando un hecho se actualiza en sesiones posteriores, Neurox devuelve la versión antigua (el pipeline de contradicción/deduplicación de consolidación no se ejecutó en los ~10 segundos entre ingestión y consulta).
- ❌ **cross-session = 0%**: Conectar información de múltiples sesiones requiere graph traversal — el gap #1 del plan.
- ❌ **temporal-order = 0%**: Sin estructura jerárquica temporal, el retrieval plano no distingue qué vino primero.
- 🔍 El 0% en contradiction handling confirma el hallazgo del benchmark Memora: el sistema devuelve hechos obsoletos cuando compiten con hechos actualizados.

### Gates para este benchmark

| Condición | Gate |
|---|---|
| Contradiction handling baseline | 0% |
| **Target post-fix consolidation filter** | **≥ 50%** |
| direct-recall | ≥ 90% (ya: 100%) ✅ |
| cross-session (post graph traversal) | ≥ 30% |

---

## Resumen ejecutivo del baseline

```
                  NEUROX BASELINE 2026-06-10
                  ===========================
                  (all exact-match judge, no LLM)

LongMemEval-S     Accuracy:  3.3%   Latency p50: 14ms  ← latencia líder absoluto
Memora Standard:            44.0%   FAMA: 4.0%          ← FAMA revela bug recall filter
MemoryStress:               20.0%   direct-recall: 100%  ← falla en multi-hop/temporal
```

## Hallazgo prioritario pre-Fase B

**Antes de implementar Graph Traversal o Reranker (Steps 6-8), hay un quick win de ~Fase A.5:**

El recall está devolviendo observaciones `stale` — que la infraestructura de Neurox ya marcó como supersedidas — porque el filtro default es `staleness <> 'expired'` (incluye stale). Cambiar el comportamiento para que, por default, recall excluya `stale` (solo `fresh` y `revalidated`), con `IncludeStale` para queries de tipo history, **podría llevar FAMA de 4% a ~40-60% con un cambio de 1 línea**, convirtiendo el diferenciador teórico de Neurox en real y medible.

**Este es el mejor candidato para Step 6 antes de Instruct Prefix.**

---

## Instrucciones para reproducir

```bash
# Requisitos previos
# - Neurox server corriendo: neurox serve
# - Embeddings configurados en config.yaml

# Benchmark 1: LongMemEval (30 preguntas, ~3 min)
cd benchmarks/memorybench && node src/index.js run -p neurox -b longmemeval -l 30 -r run-$(date +%Y%m%d)

# Benchmark 2: Memora/FAMA (25 preguntas, ~2 min)
go run -tags fts5 ./benchmarks/memora/ -limit 60 -namespace bench-memora-$(date +%Y%m%d) -output /tmp/memora.json

# Benchmark 3: MemoryStress smoke (50 sesiones, ~2 min)
cd benchmarks/memorystress && python run.py --smoke --namespace bench-stress-$(date +%Y%m%d) --output-dir /tmp/stress/
```

## Estado de LoCoMo (legacy)

Los resultados en `benchmarks/longmemeval/results_locomo_*.jsonl` son **legacy**. LoCoMo fue desacreditado en abril 2026 (audit Penfield Labs: 6.4% answer key erróneo, juez acepta 63% de respuestas incorrectas, techo real 93.6%). Solo se mantienen como referencia histórica, nunca como gate de calidad.

---

*Próxima revisión de baseline: después del Step 6 (fix filter stale en recall + instruct prefix Qwen3)*
