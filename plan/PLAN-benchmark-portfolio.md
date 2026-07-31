# Plan: Portfolio de benchmarks 2026 + mejoras de save/recall medidas

## Goal

Reemplazar LoCoMo/LongMemEval como gates de calidad por un portfolio de benchmarks 2026 que mida lo que Neurox realmente diferencia (mutación de hechos, staleness, memoria longitudinal, eficiencia en coding agents), establecer un baseline honesto contra la competencia (Mem0, Zep, Supermemory), y solo después implementar mejoras de recall (graph traversal, reranker, instruct prefix) validando cada una contra ese portfolio.

## Business Context

- **Problema**: LoCoMo fue formalmente desacreditado (audit Penfield Labs abr-2026: 6.4% del answer key erróneo, juez acepta 63% de respuestas incorrectas, techo real 93.6%) y LongMemEval cabe en context windows modernos (mide gestión de contexto, no memoria). Los gates actuales de Neurox (`benchmarks/longmemeval/GATES_REPORT.txt`) están anclados a benchmarks muertos.
- **Oportunidad competitiva**: la métrica FAMA de Memora penaliza usar memoria invalidada — exactamente lo que Neurox ya resuelve con staleness/supersedes/temporal supersession y donde Mem0 (ADD-only), Letta y Cognee (sin modelo temporal) fallan estructuralmente. Es el benchmark donde Neurox puede ganar de forma defendible.
- **Usuarios afectados**: la credibilidad del claim "Neurox mejora a un coding agent de forma medible" depende de números honestos y reproducibles. Stompy demostró que el ahorro real de memoria en coding agents es 15-28%, no el 80-95% que la industria claimea — publicar un número honesto diferencia.
- **Criterios de éxito de producto**:
  - Neurox aparece en una comparación head-to-head reproducible contra Mem0/Zep/Supermemory con MemScore (calidad/latencia/tokens).
  - Existe un score FAMA de Neurox que demuestra la ventaja del modelo temporal.
  - Cada mejora de recall se acepta o rechaza con un delta de benchmark, no con intuición.
  - LoCoMo queda relegado a comparación legacy, nunca como gate.

## Technical Context

- **Estado actual del código**:
  - `benchmarks/longmemeval/main.go` es un runner Go autocontenido que ya sabe montar un env de Neurox, ingestar sesiones y medir Recall@K/NDCG — patrón reutilizable para los nuevos runners.
  - `internal/recall/engine.go:148-340`: pipeline híbrido FTS5+semantic con RRF (k=60), union merge, fact store, namespace backfill. **No traversa `observation_links`** (gap #1 para multi-hop; ya documentado en memoria como decisión pendiente).
  - `internal/recall/engine.go:195`: `if semErr == nil` traga errores del provider de embeddings sin loguear — la búsqueda semántica degrada a FTS-only en silencio (verificado empíricamente: vLLM caído y ningún log lo delató).
  - `internal/recall/semantic.go:31`: la query se embebe cruda. Qwen3-Embedding es instruction-aware: sin instruct prefix en query se pierde 1-5% de calidad de retrieval (documentación oficial de Qwen).
  - `internal/embed/tracker.go`: `db_settings` registra `embed_dims=1536` pero los blobs reales son 4096 (Qwen3-Embedding-8B) — mismatch que puede disparar/impedir re-embeds incorrectamente.
  - Medición empírica (DB real, 6,181 embeddings × 4096 dims): semanticSearch ≈ 80-137ms; I/O+deserialización = 86% del costo; piso con cache de vectores pre-normalizados + scan paralelo = 2.7ms.
- **Benchmarks externos a integrar**:
  - `supermemoryai/memorybench` (TypeScript/Bun): harness pluggable ingest→index→search→answer→evaluate con providers via interfaz (`initialize/ingest/search/clear`). Soporta LoCoMo/LongMemEval/ConvoMem y judges intercambiables. Neurox entra implementando un provider que hable con la API HTTP (puerto 7438).
  - `geniesinc/Memora` (Apache-2.0): conversaciones semanales/mensuales/trimestrales con mutación de hechos; métrica FAMA (Forgetting-Aware Memory Accuracy).
  - `omega-memory/memorystress` (Python, Apache-2.0): 1,000 sesiones longitudinales, 40 cadenas de contradicción, 300 preguntas en 4 checkpoints; adapters por provider, soporta MCP servers.
- **Constraints**: los runners externos viven fuera del módulo Go (Bun/Python); se integran vía la API HTTP o MCP de Neurox, no embebiendo Neurox como librería. Los runners propios en Go siguen el patrón de `benchmarks/longmemeval/`.

## Implementation Steps

### Step 1: Hardening previo — eliminar la degradación silenciosa del path semántico
- **What**: Loguear `semErr` en `engine.go:195` con contexto (provider, query truncada). Exponer estado del provider de embeddings (reachable/unreachable, último error, modelo, dims reales vs registradas) en `health_check` y `status`. Corregir el mismatch del `ModelTracker` (registrar dims observadas del primer blob real, no las configuradas).
- **Why**: Cualquier baseline corrido con el path semántico silenciosamente caído es basura. Esto es prerrequisito de validez de todo el plan.
- **Where**: `internal/recall/engine.go`, `internal/embed/tracker.go`, `internal/health/health.go`, `internal/mcp/handlers.go` (status).
- **Acceptance**: Con el provider caído, `recall` emite log WARNING y `health_check` reporta la dimensión embeddings como degradada con causa. Test unitario que simula provider con error y verifica el log + health. `db_settings.embed_dims` refleja las dims reales de los blobs.
- **Status**: [x] done

### Step 2: Provider adapter de Neurox para supermemoryai/memorybench
- **What**: Fork/clone de `supermemoryai/memorybench` bajo `benchmarks/memorybench/` (o submódulo) con un provider `neurox` que implemente `initialize/ingest/search/clear` contra la API HTTP de Neurox (`POST /api/v1/observations`, `GET /api/v1/observations/search`). Mapear sesiones del benchmark a observaciones con namespace por run (`containerTag`).
- **Why**: Comparación head-to-head reproducible contra Supermemory/Mem0/Zep con MemScore (calidad/latencia/tokens) usando el mismo harness que publica la competencia. LoCoMo/LongMemEval quedan disponibles solo como comparación legacy.
- **Where**: `benchmarks/memorybench/` (nuevo, TypeScript), `internal/api/` solo si falta algún endpoint (no se espera).
- **Acceptance**: `bun run src/index.ts run -p neurox -b longmemeval -j <judge> -l 50` completa el pipeline y produce `report.json` con MemScore. Documentado cómo correr la comparación `compare -p neurox,mem0,zep`.
- **Status**: [x] done

### Step 3: Runner Memora/FAMA en Go
- **What**: Nuevo runner `benchmarks/memora/main.go` siguiendo el patrón de `benchmarks/longmemeval/main.go`: descarga/parsea el dataset de `geniesinc/Memora`, ingesta las conversaciones (semanal/mensual/trimestral) como sesiones+observaciones, ejecuta las tareas (remembering/reasoning/recommending) y computa accuracy estándar + FAMA (penalizando respuestas basadas en memoria invalidada).
- **Why**: FAMA mide el diferenciador de Neurox (staleness/supersedes/temporal). Es el benchmark donde la arquitectura puede demostrar ventaja estructural sobre Mem0/Letta/Cognee.
- **Where**: `benchmarks/memora/` (nuevo, Go), reutilizando helpers de env de `benchmarks/longmemeval/` si conviene extraerlos a un paquete compartido `benchmarks/internal/`.
- **Acceptance**: `go run ./benchmarks/memora` produce un reporte JSON con accuracy y FAMA por configuración (weekly/monthly/quarterly) y por tarea. Los resultados distinguen respuestas correctas vs respuestas que usaron memoria obsoleta.
- **Status**: [x] done

### Step 4: Adapter de Neurox para MemoryStress
- **What**: Adapter Python para `omega-memory/memorystress` que hable con Neurox vía HTTP (save/recall por sesión), bajo `benchmarks/memorystress/`. Correr el dataset completo (1,000 sesiones, 4 checkpoints) y capturar las métricas longitudinales (cross-session recall, contradiction handling, cold start recovery).
- **Why**: Es el único benchmark longitudinal (sesión 1,000, no sesión 5) y soporta MCP/memory servers explícitamente. Las cadenas de contradicción ejercitan el pipeline de consolidación/contradiction detection de Neurox en condiciones realistas.
- **Where**: `benchmarks/memorystress/` (nuevo, Python adapter + script de orquestación).
- **Acceptance**: `python scripts/run.py --adapter neurox --grade` completa los 4 checkpoints y produce métricas por fase. El reporte muestra explícitamente el comportamiento en las 40 cadenas de contradicción.
- **Status**: [x] done

### Step 5: Baseline consolidado + nuevos gates
- **What**: Correr los tres benchmarks (Steps 2-4) sobre el Neurox actual (post Step 1) y escribir `benchmarks/BASELINE_2026.md` con: números por benchmark, comparación contra competencia donde exista, y definición de los nuevos gates de aceptación (ej. FAMA ≥ X, MemScore quality ≥ Y, contradiction handling ≥ Z). Marcar `benchmarks/longmemeval/GATES_REPORT.txt` como legacy.
- **Why**: Sin baseline congelado, los deltas de los Steps 6-8 no son interpretables. Los gates nuevos reemplazan formalmente a los de LoCoMo.
- **Where**: `benchmarks/BASELINE_2026.md` (nuevo), `benchmarks/longmemeval/README.md` (nota legacy).
- **Acceptance**: Documento con baseline reproducible (comandos exactos, versiones, judge usado) y tabla de gates con umbrales justificados. Revisión humana de los umbrales antes de continuar.
- **Status**: [x] done

### Step 6: Instruct prefix para queries con Qwen3-Embedding
- **What**: Añadir soporte de instruct prefix asimétrico en el path de query: cuando el modelo de embeddings es instruction-aware (configurable, default on para `Qwen3-Embedding*`), `semanticSearch` embebe la query como `Instruct: Given a code-agent memory search query, retrieve relevant observations\nQuery: {query}`; los documentos siguen embebiéndose crudos. Sin re-embed de la base.
- **Why**: Qwen3 documenta 1-5% de pérdida de retrieval sin instruct en query. Es el quick win de calidad más barato (≈10 líneas + config).
- **Where**: `internal/recall/semantic.go`, `internal/embed/` (capability flag por provider/modelo), `internal/config/`.
- **Acceptance**: Tests unitarios del prefijo condicional. Delta medido contra baseline del Step 5 (al menos en memorybench): se acepta si no degrada y se documenta el delta real.
- **Status**: [x] done

### Step 7: Graph traversal en recall (spreading activation sobre observation_links)
- **What**: Tras el merge FTS+semantic, expandir candidatos 1 salto por `observation_links` (tipos `relates_to`, `derived_from`, `validates`, `refines`; excluir `contradicts`/`supersedes` salvo intent history) y por facts compartidos. Los candidatos por traversal entran con score descontado (factor configurable, ej. 0.6× del score del nodo origen) y marcados en `ScoreBreakdown` (`GraphHop`). Cap de expansión para no inflar el pool (ej. máx `limit` candidatos por traversal).
- **Why**: Multi-hop es el gap estructural de Neurox (68-70% en el benchmark legacy). Todos los líderes (Zep, ByteRover, Hindsight) hacen traversal o jerarquía; Neurox tiene el grafo construido pero el recall no lo consulta.
- **Where**: `internal/recall/engine.go`, `internal/recall/scoring.go`, nuevo `internal/recall/graph.go`, `internal/links/store.go` (batch loader de vecinos si falta).
- **Acceptance**: Tests unitarios del traversal (descuento, cap, exclusión de contradicts). Gate: mejora multi-hop en memorybench/Memora sin degradar el resto del portfolio más del margen acordado en Step 5. Si degrada, queda detrás de flag y se documenta.
- **Status**: [ ] pending

### Step 8: Etapa de reranking post-retrieval
- **What**: Etapa opcional de rerank de los top 30-50 candidatos antes del truncado final: (a) provider cross-encoder vía HTTP (Qwen3-Reranker en el mismo vLLM ya configurado) con interfaz `Reranker` pluggable, (b) fallback heurístico sin red (MMR para diversidad) cuando no hay provider. Configurable off/on; off por default hasta que el gate lo valide. Nota: si el pool de candidatos crece, aplicar primero el cache in-memory de vectores pre-normalizados (medido: 96ms→2.7ms) como habilitador.
- **Why**: Ataca directamente la pérdida de precisión de ranking (NDCG -13% con embeddings en el benchmark legacy). Es el patrón estándar de los líderes: retrieve amplio y barato → rerank estrecho y caro, sin LLM generativo en query time.
- **Where**: nuevo `internal/rerank/` (interfaz + provider vLLM + MMR), `internal/recall/engine.go` (wiring), `internal/config/`.
- **Acceptance**: Tests unitarios de la interfaz y del fallback MMR. Gate: mejora NDCG/quality en el portfolio con latencia p50 < umbral acordado (definir en Step 5; referencia: competencia publica sub-300ms). Se acepta solo con delta positivo documentado.
- **Status**: [ ] pending

## Verification

```bash
go build ./...
go vet ./...
go test ./...
# Runners de benchmark
go run ./benchmarks/memora -limit 20            # smoke
cd benchmarks/memorybench && bun run src/index.ts run -p neurox -b longmemeval -l 10
cd benchmarks/memorystress && python scripts/run.py --adapter neurox --limit-sessions 50
```

## Risks / Notes

- **Orden no negociable**: Step 1 antes que cualquier baseline (un baseline con semantic caído invalida todo), Step 5 antes que Steps 6-8 (sin baseline no hay gates).
- **Datasets externos**: Memora y MemoryStress son repos jóvenes (2026); validar licencias (ambos Apache-2.0) y calidad de datos antes de adoptarlos como gate duro. Si el dataset de Memora resulta inestable, FAMA puede computarse sobre MemoryStress (sus cadenas de contradicción dan señal equivalente).
- **Costo de judges**: los tres benchmarks usan LLM-as-judge; presupuestar llamadas y fijar judge + versión en `BASELINE_2026.md` para reproducibilidad. Considerar el judge refinado de LoCoMo-Refined (Qwen3-14B + prompt estricto, 86% agreement humano) como referencia de rigor.
- **No optimizar velocidad prematuramente**: las optimizaciones del path semántico (cache de vectores, pre-normalización, paralelismo — ya medidas y documentadas en memoria) entran solo como habilitador del Step 8 o como plan separado posterior; no son gates de este plan.
- **Benchmark de coding agents (Stompy/STATE-Bench)**: medir tokens/turnos/resolution-rate en codebase real es la métrica de producto definitiva, pero requiere harness propio con agente real; queda como iniciativa separada post-portfolio para no inflar este plan.
- **vLLM como dependencia operativa**: Steps 6 y 8 asumen el provider remoto (Qwen3) disponible; el Step 1 garantiza que su ausencia sea visible, y los fallbacks (sin instruct, MMR) mantienen el sistema funcional sin él.
