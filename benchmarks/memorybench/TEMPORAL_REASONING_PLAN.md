# Plan de Mejora: Temporal-Reasoning en LongMemEval (v2 — corregido)

> **v2**: Versión corregida tras revisión con evidencia empírica del run `ab50-llm-dates-3`,
> el dataset real y el código del adapter. La v1 proponía mecanismos basados en `created_at`
> que no pueden funcionar (ver "Hallazgos de la Revisión").

## Estado Actual

Fuente: `data/runs/ab50-llm-dates-3/report.json` (run más reciente, 50 preguntas estratificadas, judge LLM).

| Métrica | Valor | Contexto |
|---------|-------|---------|
| Desempeño general | 30/50 = 60% | Neurox actual |
| Temporal-reasoning | 0/13 = 0% | **Bloqueador crítico** |
| No-temporal | 30/37 = 81.1% | Nivel competitivo |

Por categoría: knowledge-update 6/8, multi-session 9/13, single-session-assistant 6/6, single-session-preference 3/3, single-session-user 6/7, temporal-reasoning 0/13.

**Competencia**: Supermemory 81.6% · Zep 63.8% · **Neurox 60%** · Mem0 49%

**Impacto**: temporal-reasoning arrastra el score ~21 puntos (81.1% sin temporal vs 60% total).

---

## Hallazgos de la Revisión (evidencia empírica)

Verificado contra dataset, código y el run `ab50-llm-dates-3`:

1. **El dataset usa fechas con slashes, el regex de ingest espera guiones.**
   `haystack_dates` y `question_date` vienen como `"2023/05/20 (Sat) 02:21"` (verificado: 100% formato slash en `longmemeval_s.json`). El regex de `src/providers/neurox.js:99` (`/(\d{4}-\d{2}-\d{2})/`) devuelve `null` contra ese formato → **los tags `date-YYYY-MM-DD` jamás se crean** (0 tags en los 50 contextos del run). Falla silenciosa.

2. **Todas las memorias muestran la fecha de ingesta, no la de sesión.**
   `_formatAsLLMContext` (`neurox.js:161-175`) prefiere `created_at` sobre el tag de fecha. Como todo se ingiere el mismo día, **las 500 memorias de los 50 contextos muestran `Date: 2026-06-10`**. La fecha real de la conversación (2023) nunca llega al modelo.

3. **10 de 13 preguntas temporales son de duración** ("how many days/weeks/months passed/ago between X and Y"). Son **irresolubles sin las fechas reales de sesión**. Ordenar por `created_at` (timestamps del mismo día separados por ms) no aporta nada para estas.

4. **`include_stale` es no-op en el benchmark.** Las 500 memorias de los contextos están `fresh` (se ingieren minutos antes de preguntar; la consolidación no corre). El filtrado de stale NO es causa del 0%. Además, el core Go ya auto-incluye stale para intent de historia (`internal/recall/engine.go:159-165`).

5. **`question_date` se carga pero se descarta.** El loader lo expone (`src/benchmarks/longmemeval.js:51`), pero el runner no lo incluye en `checkpoint.results` (`runner.js:243-256`) ni lo pasa a `generateAnswer` (`runner.js:360-364`).

6. **Los archivos de la v1 no existen.** No hay `src/adapter.js` ni `src/context-formatter.js`. Los targets reales son `src/providers/neurox.js` (adapter + `_formatAsLLMContext`), `src/runner.js` y `src/answer.js`.

7. **Coherencia temporal del prompt**: inyectar `question_date` (2023) como "ahora" junto a memorias fechadas `2026-06-10` haría que las memorias parezcan del futuro. La fecha de referencia y las fechas de las memorias deben venir del mismo eje temporal (el del dataset).

**Cadena causal del 0%**: fechas reales nunca ingresan (regex roto) → formatter muestra fecha de ingesta uniforme → sin fechas distinguibles ni "ahora", las preguntas de duración/orden son imposibles → 0/13 reproducido en `ab50-llm-dates-1/2/3`.

---

## Fundamento Ya Construido (estado real)

- ✅ `created_at` expuesto en search API y MCP recall (commit `2974fa9`) — funciona, pero es la fecha equivocada para este benchmark
- ✅ Flags `--context-format llm` y `--ingest-delay-ms` (commit `da5bafc`) operativos
- ✅ `question_date` y `haystack_dates` disponibles en el loader; el runner ya pasa `haystack_dates` a `ingest()` (`runner.js:238`)
- ✅ Formato LLM muestra campo `Date:` por memoria
- ❌ Tags `date-YYYY-MM-DD` (commit `2974fa9`): **rotos** por formato de fecha (hallazgo 1)
- ❌ Prioridad de fecha en formatter: **invertida** para este caso de uso (hallazgo 2)

---

## Solución Propuesta: 6 Cambios en Benchmark (Sin Core Go)

**Ámbito**: `benchmarks/memorybench/src` solamente. Validar primero; cambios Go después.

### 1. Arreglar normalización de fechas de sesión en ingest (bugfix)
**Archivo**: `src/providers/neurox.js:94-104`

Aceptar ambos formatos (slash y guion) y normalizar a ISO:
```javascript
const dateMatch = dateStr.match(/(\d{4})[\/-](\d{2})[\/-](\d{2})/);
if (dateMatch) {
  tags.push(`date-${dateMatch[1]}-${dateMatch[2]}-${dateMatch[3]}`);
}
```
Resultado: cada observación lleva la fecha real de su sesión (`date-2023-05-20`).

### 2. Formatter: preferir fecha de sesión sobre `created_at` (bugfix)
**Archivo**: `src/providers/neurox.js:161-175`

Invertir la prioridad: primero el tag `date-YYYY-MM-DD` (fecha del evento), `created_at` solo como fallback. Así `Date:` refleja el eje temporal del dataset.

### 3. Thread `question_date` → prompt de respuesta
**Archivos**: `src/runner.js` (243-256: agregar `question_date` al checkpoint; 360-364: pasarlo a `generateAnswer`) + `src/answer.js` (aceptar `questionDate`, normalizar a ISO)

Inyección en el prompt (solo si hay fecha):
```
Current date: 2023-05-30 (the question is being asked on this date).
```

### 4. Detección de intención temporal + sort cronológico
**Archivos**: `src/utils.js` (nueva `detectTemporalIntent`, compartida) + `src/providers/neurox.js` (`search()`)

- Keywords: `first`, `last`, `before`, `after`, `when`, `earliest`, `latest`, `how long`, `how many days/weeks/months`, `since`, `until`, `ago`, `between`
- El query de `search()` ES la pregunta (`runner.js:318`), así que la detección puede vivir en el adapter
- Si temporal: ordenar resultados por **fecha de sesión** (del tag, no `created_at`) ascendente antes de formatear; memorias sin fecha van al final conservando orden de relevancia
- Si no temporal: orden por relevancia (sin cambios)

### 5. Sección temporal en prompt de respuesta
**Archivo**: `src/answer.js`

Solo para preguntas con intención temporal:
```
TEMPORAL REASONING INSTRUCTIONS:
1. Each memory includes a Date (the date of that conversation/event). Build a timeline.
2. Use the current date above as "now" for questions about "ago", "since", "how long".
3. For sequence questions: "first" = earliest Date, "last" = most recent Date.
4. For duration questions: compute the difference between the relevant Dates in days/weeks/months.
5. Cite the dates you used in your answer.
```

### 6. (Defensivo, impacto esperado: nulo) `include_stale=true` con intención temporal
**Archivo**: `src/providers/neurox.js` (`search()`)

El server ya lo soporta (`internal/api/handlers.go:162`). En el benchmark todo está `fresh` (hallazgo 4), así que NO se espera impacto — se agrega por paridad con uso real de Neurox, donde sí importa. No atribuirle mejoras.

### Variante B (solo si A queda corta): fecha embebida en contenido
Flag `--embed-session-dates`: prefijar `[Session date: 2023-05-20]` al `content` en ingest (patrón estándar del paper LongMemEval). Ventajas: funciona también en formato `raw` y es indexable por FTS. Se mantiene como experimento separado para no contaminar la medición de A — y evitar acoplar el formato del benchmark al contenido de memorias (mantenerlo benchmark-side).

---

## Plan de Validación

### Fase 1: Implementación
1. Bugfixes 1-2 (incondicionales — son bugs, no features)
2. Cambios 3-5 detrás de flag `--no-temporal-branch` para poder desactivarlos (default: activos)
3. Cambio 6 junto con el 4

### Fase 2: Testing
- **temperature=0** (ya es el default en `runner.js:363`), judge LLM, `--limit 50 --stratified` (mismo sampling que baseline)
- 3 runs: `ab50-realdates-{1,2,3}`
- Baseline de comparación: `ab50-llm-dates-3` (60%, temporal 0/13, no-temporal 30/37)
- **Análisis de fallos obligatorio**: para cada temporal incorrecta, clasificar *retrieval-miss* (las sesiones relevantes no están en el top-10) vs *reasoning-miss* (estaban y el modelo falló). Esto decide el siguiente paso.

### Fase 3: Métricas (matemática corregida)
```
Antes:
  temporal-reasoning: 0/13 = 0%
  non-temporal:       30/37 = 81.1%
  overall:            30/50 = 60%

Objetivo:
  temporal-reasoning: 4-6/13 = 31-46%
  non-temporal:       ≥29/37 (sin regresión; tolerancia 1 pregunta de ruido)
  overall:            34-36/50 = 68-72%  → supera a Zep (63.8%)
```
Nota: 72% requiere 6/13, no 4-5/13.

---

## Guardrails

- ✅ **Benchmark-first**: cero cambios en Go core hasta validar
- ✅ **Bugfix vs feature separados**: 1-2 siempre activos; 3-5 desactivables con `--no-temporal-branch`
- ✅ **Temperature=0 + 3 runs**: reproducibilidad
- ✅ **Sin regresión**: la rama temporal solo se activa con keywords; medir deltas de las 5 categorías no-temporales (el cambio 2 altera el `Date:` mostrado en TODAS las preguntas → vigilar especialmente knowledge-update y multi-session)
- ✅ **`--ingest-delay-ms` deja de ser necesario** para separar fechas (era un hack sobre `created_at`); conservarlo solo como rate-limit

---

## Riesgos

| Riesgo | Probabilidad | Mitigación |
|--------|--------------|------------|
| Retrieval no trae las 2 sesiones que requiere una pregunta de duración | Media-alta | Análisis de fallos (Fase 2); si domina retrieval-miss, experimento con `limit` 15-20 para queries temporales |
| Aritmética de fechas del LLM ("how many weeks ago") imprecisa | Media | Instrucciones explícitas (cambio 5); el judge acepta rangos ("7 days. 8 days also acceptable") |
| Cambio de `Date:` afecta categorías no-temporales | Baja | Fechas reales son estrictamente más informativas; vigilar deltas por categoría |
| Doble detector de intención (JS benchmark vs Go core) divergen | Baja (corto plazo) | Aceptable para validación; unificar en Go core después (ver siguiente sección) |

---

## Siguiente Paso: Go Core (Post-Validación)

Si el benchmark valida la mejora, el aprendizaje clave para el core es: **`created_at` (tiempo de ingesta) ≠ fecha del evento recordado**. Neurox ya tiene la infraestructura para esto:

- `internal/temporal/` extrae menciones temporales (tabla `temporal_mentions`) — exponer la fecha de evento en los resultados de recall, no solo `created_at`
- `internal/recall/temporal.go` ya detecta intención temporal, pero sus regex no cubren `first/last/earliest/since/until/ago` (los del benchmark sí) → extender `IntentHistory`/detección
- Parámetro `as_of` (estilo question_date) para anclar "ahora" en recall
- Opción `sort=chronological` como alternativa al sort por relevancia
- `include_stale` ya existe en API y MCP — nada que hacer

**No hacer**:
- Cambios de schema (no necesarios: `temporal_mentions` y `created_at` ya existen)
- Refactor del scoring core sin validación previa en benchmark
- Breaking changes en API

---

## Resumen Ejecutivo

**Problema**: temporal-reasoning 0/13 arrastra el score de 81.1% (sin temporal) a 60%.

**Causa raíz (corregida)**: las fechas reales de las sesiones (2023) nunca llegan al modelo — el regex de tags está roto por formato (slash vs guion) y el formatter prefiere `created_at` (fecha de ingesta, uniforme). Sin fechas distinguibles ni fecha de referencia, las 10/13 preguntas de duración son irresolubles. `include_stale` y el orden por relevancia eran diagnósticos secundarios o incorrectos.

**Solución**: 2 bugfixes (normalización de fechas + prioridad del formatter) + 4 cambios (question_date en prompt, detección temporal, sort cronológico por fecha de sesión, instrucciones temporales).

**Validación**: 3 runs estratificados a temperature=0 contra baseline `ab50-llm-dates-3`, con análisis retrieval-miss vs reasoning-miss y guardrail de regresión en no-temporal.

**ROI esperado**: temporal 31-46% → overall 68-72% → supera a Zep.

---

*v2 generado tras revisión con evidencia. Revisar con arquitecto antes de cambios en Go core.*
