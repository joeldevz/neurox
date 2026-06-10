# Experimento: Contexto LLM-ready para MemoryBench

## Problema Actual

Las puntuaciones de benchmark en MemoryBench mezclan dos responsabilidades:
1. **Recuperación (Neurox)**: ¿recuperamos las memorias correctas?
2. **Generación de respuestas (Claude)**: ¿Claude puede formular una respuesta correcta con esa evidencia?

Análisis de fallos recientes muestra que **muchos errores tienen evidencia correcta en la recuperación**, pero el formato de contexto (raw concatenado) confunde a Claude:
- Múltiples memorias sin separación clara → ambigüedad
- Falta de metadata (rank, confianza, fecha) → no sabe qué es más relevante
- Límites de contenido difusos → ruido y distractores
- Respuestas hedged/vagas cuando hay conflicto de evidencia

## Cambio Propuesto 1: Formateo de Contexto en el Adaptador de Benchmark

En `benchmarks/memorybench/src/providers/neurox.js`, cambiar el formateo de memorias recuperadas:

### Antes (Raw Concatenado)
```
Neurox retrieved 3 items:
Discovered the team uses shared config files. The config system caches values in 
memory for 5 minutes by default. This helps reduce database load.
MemoryBench uses LLM-based retrieval. The system caches query results across 
requests to speed up repeated lookups. Cache invalidation happens on updates.
Session started with focus on database optimization. Tests showed 40% improvement 
in query time after adding connection pooling...
```

### Después (LLM-ready con Metadata)
```
**Retrieved Memories (3)**

1. [Rank #1 | Kind: episodic | Confidence: 0.94 | Staleness: fresh]
   **Title**: Team config system caching behavior
   **Tags**: caching, config, performance
   **Observation Type**: discovery
   **Content**:
   > The config system caches values in memory for 5 minutes by default. 
   > This helps reduce database load.

2. [Rank #2 | Kind: semantic | Confidence: 0.87 | Staleness: fresh]
   **Title**: MemoryBench retrieval and caching mechanics
   **Tags**: retrieval, caching, architecture
   **Observation Type**: pattern
   **Content**:
   > The system caches query results across requests to speed up repeated lookups. 
   > Cache invalidation happens on updates.

3. [Rank #3 | Kind: procedural | Confidence: 0.71 | Staleness: stale]
   **Title**: Database optimization results
   **Tags**: optimization, database, performance-testing
   **Observation Type**: discovery
   **Content**:
   > Tests showed 40% improvement in query time after adding connection pooling.
```

**Note on API Field Names**: 
- **Kind** (episodic | semantic | procedural): Memory category, indicates storage layer/type
- **Observation Type** (discovery | pattern | decision | gotcha | bugfix | config | preference | question): Semantic classification of what the observation represents
- These are distinct fields; kind describes *how* it's stored, observation_type describes *what* it is.
- Rank/order, title, tags, confidence, and staleness are current Neurox API fields.
- Layer is an internal storage detail and is **not recommended to include** in LLM context (likely adds noise).
- Session metadata (session_id, date) is not currently exposed in search results and can only be added if future API changes include it (e.g., via tags or enhanced result payload).

## Por Qué Aplicar Primero en el Adaptador de Benchmark (No en el Core Go)

1. **Bajo riesgo**: Solo afecta el benchmark; no cambia la API/core de Neurox
2. **Aislamiento**: Podemos validar si **el formato de contexto** es el problema real, no la recuperación
3. **Validación antes de API**: Antes de agregar un campo `format=llm` o `llm_context` al core, queremos evidencia de que mejora la calidad
4. **Iteración rápida**: Cambios en JS son más rápidos que recompilar Go

## Criterios de Éxito — Protocolo Estadístico Reforzado

**Scoring and Rank/Order Fields (Not Raw Score)**
- El adaptador expone `rank`/`order` desde el array de resultados, no el score numérico tri-factor.
- **Justificación**: El score tri-factor (recency × importance × relevance) no está calibrado como probabilidad y no debe exponerse directamente al modelo de respuesta. Si se necesita relevancia cualitativa, usar etiquetas empíricamente útiles basadas en rank/posición (ej: "Primary" para top-3, "Supporting" para 4-10).
- Metadata de fecha/sesión: Solo se pueden incluir si la adaptación de ingestión añade campos a `tags` o el core API futuro expone `source_session_id` en resultados.

**Requerimientos de Éxito Estadístico (No +3% en Una Sola Pasada)**
- ✅ **Temperatura de respuesta = 0** para todos los experimentos (determinismo, comparable)
- ✅ **Mínimo 3 ejecuciones** de cada variante (raw vs llm-context) con datos de test idénticos
- ✅ **Mejora consistente** en categorías no-temporales (semántica, procedural, pattern) *antes de aceptar*
- ✅ **Reducción en fallos de generación** (mismo retrieval, mejor formato → menos hedging/vaguedad)
- ✅ Fallos de recuperación siguen siendo identificables (no enmascaramos misses)
- ✅ **No hay prompt leakage**: Instrucciones de benchmark no aparecen en contexto
- ✅ Reasoning temporal excluido del criterio de aceptación principal (se evalúa por separado, sin gate)
- ✅ **Staleness Analysis**: After runs, correlate any new failures with retrieved memories marked `staleness: stale`. Document whether the model over-discounts stale memories that still contain useful historical context; this informs whether staleness should be emphasized, de-emphasized, or formatted differently for LLM consumption.

## Costo de Tokens y Comparativa de Contexto

- El contexto formateado con metadatos (rank, confidence, staleness, layer, tipo, tags) **incrementa el volumen de tokens aproximadamente 2–3x** respecto al raw concatenado.
- La comparativa **debe reportar** tokens de contexto aproximados por variante (raw vs llm-context) usando la aproximación del runner existente, si está disponible, o cálculo conservador (1 token ≈ 4 caracteres).
- **Recomendación**: Evaluar trade-off calidad/costo antes de adoptar como estándar en API.

## Pasos Siguientes (Si Tienen Éxito)

Si el experimento confirma mejora consistente en 3+ ejecuciones (no en una sola pasada):
- Considerar agregar formato oficial en Go (`/v1/observations/search` con `?format=llm` o campo `llm_context`)
- Estandarizar en la API para otros consumidores LLM
- Documentar en CONVENTIONS.md, incluida advertencia de costo de tokens

Si no hay mejora después de 3+ ejecuciones:
- El problema está en recuperación (Neurox) o en las preguntas del benchmark, no en formateo
- Investigar otro aspecto

## Anti-Overfitting Guardrails

- **Train/Holdout**: Benchmarks de control separados (no ajustar con datos de test)
- **Un cambio a la vez**: Solo contexto formateado; sin cambios a semántica o lógica de recuperación
- **No por pregunta**: Sin "hacks" específicos para preguntas que fallan
- **Comparación v1 vs v2**: Mantener ambos en git; resultados comparativos claros

## Implementación Técnica

Cambio mínimo en `benchmarks/memorybench/src/providers/neurox.js`:
```javascript
// Nuevo: función formatForLLM(memories)
// Entrada: data.results = [{ id, title, content, kind, observation_type, confidence, tags, staleness, layer }]
// rank = índice del array + 1
// Salida: string formateado con estructura clara
// NOTA: Implementation should NOT rely on score, session, or date fields as they are not provided by the API.
```

Integración:
- Mantener opción de "raw" para debugging
- Flag `--context-format=raw|llm` en CLI de benchmark
- Comparar resultados automáticamente
