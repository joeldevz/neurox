# Plan: Memory Quality, Tracking & Embedding Improvements

## Goal

Corregir 4 bugs identificados en el health-check, mejorar la calidad del pipeline de consolidación, y hacer el modelo de embedding completamente configurable con auto-migración al cambiar.

**Meta**: brain score 76/100 → ≥ 90/100.

---

## Business Context

- **Usuarios**: Agentes de IA (Claude Code, Cursor, etc.) que usan neurox como memoria persistente
- **Problemas encontrados**:
  1. **50% del cerebro invisible**: ~400/974 observaciones marcadas `expired` por falsos positivos del detector de contradicciones (`minContradictionSimilarity = 0.50` demasiado bajo para `nomic-embed-text`)
  2. **Reflections duplicadas**: `ForceRun` → `ForceReflect` → `getRecentSources` no verifica si las observaciones ya fueron reflejadas; se generaron 4 reflections casi idénticas en el mismo minuto
  3. **5 sesiones zombie activas**: `session_start` solo cierra sesiones del mismo namespace; el pipeline de consolidación no hace limpieza global
  4. **Reflections no buscables**: todas llevan título `"Reflection: {namespace}"` — imposible diferenciarlas en recall
  5. **Modelo de embedding hardcodeado**: `defaultOllamaModel = "nomic-embed-text"` y `OllamaConfig{}` vacío en `main.go` — el usuario no puede cambiarlo sin modificar el binario; al cambiarlo el DB queda en estado corrupto silencioso
- **Expected outcome**: Recall restituido para ~400 obs, Core memory sin ruido, cualquier modelo de embedding configurable con migración automática, brain score ≥ 90

---

## Technical Context

### Bug 1 — Reflections duplicadas

`pipeline.ForceRun()` llama `reflectEngine.ForceReflect()` → `getRecentSources()`, que devuelve el top-30 por importancia **sin verificar** si ya fueron reflejadas. En cambio, `pipeline.Run()` llama `reflectEngine.Run()` → `getUnreflectedSources()`, que excluye observaciones con enlace `derived_from`. Cada llamada al MCP tool `consolidate` activa `ForceRun` → reflection duplicada. La tabla `reflections` tiene `created_at` (confirmado en `schema.sql:166`), lo que permite hacer el guard de cooldown.

### Bug 2 — Contradiction threshold agresivo

`minContradictionSimilarity = 0.50` en `internal/contradiction/detector.go:19`. Con `nomic-embed-text` en corpus domain-specific, observaciones relacionadas pero no contradictorias alcanzan similarity > 0.50. El LLM las confirma como contradicciones → `staleness = 'expired'` → invisibles en `recall` y `proactive context`. No existe test `TestFindCandidates` en `detector_test.go` — hay que crearlo.

### Bug 3 — Sesiones zombie

`session.Manager.Start()` abandona sesiones del **mismo namespace** al crear una nueva, pero sesiones de otros proyectos quedan `active` indefinidamente. La consolidation pipeline no tiene paso de limpieza global. La tabla `sessions` tiene `started_at` y `status` con CHECK `('active','completed','abandoned')` — confirmado en `schema.sql:116`.

### Mejora 4 — Títulos genéricos en reflections

`saveReflection()` usa `"Reflection: " + namespace` como título (línea 252 de `engine.go`). Con 7+ reflections en Core con el mismo título, el recall no las diferencia. El LLM genera el contenido, puede también generar el título.

### Problema 5 — Modelo de embedding no configurable

`EmbeddingsConfig` (en `config.go:44-50`) tiene campos para Remote pero **no tiene `OllamaURL` ni `OllamaModel`**. En `main.go`, `embed.AutoDetect` se llama con `embed.OllamaConfig{}` vacío en 4 lugares:
- `runRecall` (línea 253)
- `runContext` (línea 290)
- `initDeps` (línea 660)
- `initDepsLight` (línea 736)

`NewOllama(cfg OllamaConfig) *Ollama` **no tiene `context.Context`**, por lo que el auto-detect de modelo NO puede vivir en `NewOllama`. La lógica correcta es moverla a `AutoDetect`, que ya tiene `ctx`. Al cambiar el modelo sin re-embeber, `CosineSimilarity` devuelve `0.0` para pares de distinta dimensión o produce scores sin sentido para misma dimensión pero distinto espacio vectorial.

---

## Implementation Steps

### Step 1: Cooldown en ForceReflect para evitar reflections duplicadas
- **What**: Agregar guard de cooldown por namespace en `ForceReflect`. Nuevo método privado `lastReflectionAt(ctx, namespace) (time.Time, bool, error)` que ejecuta `SELECT MAX(created_at) FROM reflections WHERE namespace = ?`. Si existe una reflection creada en las últimas 2 horas, retornar `ReflectionsCreated: 0` sin generar nada. Constante `ForceReflectCooldown = 2 * time.Hour`.
- **Why**: `ForceReflect` no tiene ninguna guarda contra llamadas repetidas. La llamada al MCP tool `consolidate` dispara `ForceRun` → `ForceReflect` con los mismos top-30 sources → reflection casi idéntica cada vez.
- **Where**: `internal/reflect/engine.go`
  - Nuevo método privado `lastReflectionAt(ctx, namespace) (time.Time, bool, error)`
  - Guard al inicio de `ForceReflect()` antes de `getRecentSources`, usando `ForceReflectCooldown`
- **Acceptance**:
  - Llamar `ForceReflect` dos veces en < 2h mismo namespace → segunda vez `ReflectionsCreated: 0`
  - Llamar con intervalo > 2h → genera reflection normalmente
  - Nuevo test `TestForceReflectCooldown` en `internal/reflect/engine_test.go`
  - `go test ./internal/reflect/...` verde
- **Status**: [x] done

### Step 2: Subir umbral mínimo de detección de contradicciones
- **What**: Cambiar `minContradictionSimilarity` de `0.50` a `0.65` en `internal/contradiction/detector.go`.
- **Why**: Con `nomic-embed-text`, la zona 0.50–0.65 captura observaciones relacionadas pero no contradictorias. El umbral 0.65 filtra ese ruido. Nota: este threshold deberá recalibrarse en Step 7 si se cambia el modelo de embedding, ya que cada modelo tiene su propia distribución de cosine similarity.
- **Where**: `internal/contradiction/detector.go` — constante `minContradictionSimilarity`
- **Acceptance**:
  - `minContradictionSimilarity == 0.65`
  - Nuevo test `TestFindCandidates` creado en `internal/contradiction/detector_test.go` que verifica que pares con similarity en [0.65, 0.85) son candidatos y pares en [0.50, 0.65) no lo son. **No existe este test actualmente — crearlo desde cero.**
  - `go test ./internal/contradiction/...` verde
- **Status**: [x] done

### Step 3: Migration — rescatar observaciones expired incorrectamente
- **What**: Nueva migration `internal/db/007_rescue_expired.sql` que resetea `staleness = 'fresh'` para las observaciones que tienen `staleness = 'expired'` pero **no tienen** un enlace `supersedes` apuntando a ellas (es decir, no fueron supersedidas legítimamente):
  ```sql
  UPDATE observations
  SET staleness = 'fresh',
      valid_until = NULL,
      invalidated_by = NULL,
      updated_at = datetime('now')
  WHERE deleted_at IS NULL
    AND staleness = 'expired'
    AND id NOT IN (
        SELECT target_id FROM observation_links
        WHERE relation_type = 'supersedes'
    );
  ```
  Registrar en `internal/db/db.go` como migration versión 7: agregar `//go:embed 007_rescue_expired.sql` y el entry en el slice `migrations`.
- **Why**: ~400 observaciones fueron marcadas `expired` por el threshold agresivo del Step 2. Sin enlace `supersedes` real, fueron víctimas de un falso positivo. Esta migration las devuelve al recall.
- **Where**: `internal/db/007_rescue_expired.sql` (nuevo) + `internal/db/db.go` (embed directive + migrations slice)
- **Acceptance**:
  - Post-migration: `SELECT COUNT(*) FROM observations WHERE staleness='expired' AND id NOT IN (SELECT target_id FROM observation_links WHERE relation_type='supersedes')` → 0
  - `neurox status` muestra `expired` ≤ 50
  - `go test ./internal/db/...` verde
- **Status**: [x] done

### Step 4: Limpieza de sesiones zombie en la pipeline de consolidación
- **What**: Nuevo método privado `cleanupStaleSessions(ctx) (int64, error)` en `pipeline.go` que abandona sesiones con `status = 'active'` y `started_at < datetime('now', '-24 hours')`:
  ```sql
  UPDATE sessions
  SET status = 'abandoned', ended_at = datetime('now')
  WHERE status = 'active'
    AND started_at < datetime('now', '-24 hours')
  ```
  Llamarlo al inicio de `Run()` y `ForceRun()`, antes del decay. Agregar campo `SessionsCleaned int64` a `RunResult` y loggearlo en los mensajes de log de ambos métodos.
- **Why**: `session_start` solo cierra sesiones del namespace actual. Las de otros proyectos/namespaces quedan activas para siempre. La consolidation (cada 30 min) es el lugar correcto para limpieza global.
- **Where**: `internal/consolidate/pipeline.go` — nuevo método + campo en `RunResult`
- **Acceptance**:
  - Sesiones con `started_at < now - 24h` y `status = 'active'` → `abandoned` en el próximo ciclo
  - Las 5 sesiones activas actuales se resuelven en el próximo `consolidate`
  - Nuevo test `TestCleanupStaleSessions`: insertar sesión con timestamp viejo → `Run()` → status `abandoned`
  - `go test ./internal/consolidate/...` verde
- **Status**: [x] done

### Step 5: Títulos semánticos para reflections
- **What**: Actualizar el prompt de `synthesize()` para que el LLM incluya `TITLE: <título>` en la primera línea antes de los insights. Nuevo helper privado `extractTitle(content string) (title, body string)` en `saveReflection()` que parsea esa primera línea. Fallback a `"Synthesis: {namespace}"` si el LLM no incluye el prefijo `TITLE:`.
- **Why**: 7+ reflections en Core con el mismo título `"Reflection: neurox"` son inútiles para búsqueda y diferenciación. Un título generado por el LLM sobre el contenido sintetizado es buscable y significativo.
- **Where**: `internal/reflect/engine.go` — `synthesize()` (prompt) y `saveReflection()` (extracción + fallback)
- **Acceptance**:
  - Nuevas reflections: título como `"Pattern: Temporal Context Improves Memory Recall"` en lugar de `"Reflection: neurox"`
  - Si LLM omite `TITLE:` → fallback `"Synthesis: neurox"`
  - Test con mock LLM que incluye `TITLE:` → extracción correcta del título y cuerpo por separado
  - Test con mock LLM que omite `TITLE:` → fallback aplicado
  - `go test ./internal/reflect/...` verde
- **Status**: [x] done

### Step 6: Modelo de embedding completamente configurable
- **What**: Conectar la config de Ollama embeddings (que hoy llega vacía) y eliminar el modelo hardcodeado. Cinco cambios interdependientes:

  **6a. `EmbeddingsConfig` — campos Ollama faltantes** (`internal/config/config.go`)
  Agregar `OllamaURL string yaml:"ollama_url"` y `OllamaModel string yaml:"ollama_model"` a `EmbeddingsConfig`. Agregar env vars en `applyEnvOverrides`:
  - `NEUROX_EMBED_OLLAMA_URL` → `cfg.Embeddings.OllamaURL`
  - `NEUROX_EMBED_OLLAMA_MODEL` → `cfg.Embeddings.OllamaModel`

  Ahora cualquier usuario puede poner en `~/.config/neurox/config.yaml`:
  ```yaml
  embeddings:
    provider: ollama
    ollama_model: mxbai-embed-large
  ```

  **6b. Wiring en `main.go`** — reemplazar `embed.OllamaConfig{}` por `embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}` en los 4 puntos: `runRecall` (l.253), `runContext` (l.290), `initDeps` (l.660), `initDepsLight` (l.736).

  **6c. Auto-detect de modelo en `AutoDetect`** (`internal/embed/queue.go`)
  **IMPORTANTE**: `NewOllama` no tiene `context.Context` y no puede hacer llamadas de red — la lógica de ranking debe vivir en `AutoDetect`, que ya tiene `ctx`.
  
  Nuevo helper privado `pickBestEmbedModel(ctx context.Context, baseURL string) string` que llama `GET /api/tags`, parsea la lista de modelos disponibles, y retorna el primero que aparezca en la lista de preferencia:
  ```go
  var embedModelRanking = []string{
      "qwen3-embedding",
      "mxbai-embed-large",
      "bge-m3",
      "bge-large",
      "nomic-embed-text",
      "snowflake-arctic-embed",
      "all-minilm",
  }
  ```
  En `AutoDetect`: cuando `ollamaCfg.Model == ""`, llamar `pickBestEmbedModel` ANTES de `NewOllama` para determinar el modelo. Si ningún modelo de la lista está disponible → log `"no embedding model found; run: ollama pull qwen3-embedding:0.6b"` y retornar `Disabled{}`. Si el modelo está configurado explícitamente en `ollamaCfg.Model`, usarlo directamente sin consultar la lista.

  Eliminar la constante `defaultOllamaModel = "nomic-embed-text"` de `ollama.go` — ya no se necesita.

  **6d. Dimensiones dinámicas** (`internal/embed/ollama.go`)
  Eliminar `ollamaDimensions = 768`. La struct `Ollama` detecta las dimensiones reales del primer `EmbedBatch` exitoso y las almacena con `atomic.Int32` para ser goroutine-safe (el worker de `Queue` llama `EmbedBatch` en background):
  ```go
  type Ollama struct {
      url    string
      model  string
      client *http.Client
      dims   atomic.Int32  // populated on first successful EmbedBatch
  }
  ```
  `EmbedBatch` hace `o.dims.CompareAndSwap(0, int32(len(embeddings[0])))` tras el primer batch exitoso. `Dimensions()` retorna `int(o.dims.Load())`, o `0` si aún no se detectó. Si `cfg.Embeddings.Dimensions > 0`, ese valor tiene prioridad (útil para Remote APIs).

  **6e. Agregar `"qwen3-embedding"` a `looksLikeEmbeddingModel`** (`internal/installer/installer.go:227`) — el wizard detectará automáticamente `qwen3-embedding` al buscar modelos disponibles en Ollama.

  **Nota sobre installer**: `writeConfigFile` en `installer.go` intencionalmente NO escribe `ollama_model` cuando el provider es ollama — el auto-detect de Step 6c elige el mejor modelo instalado en runtime. Esto es comportamiento correcto, no un olvido.

  **6f. `health.go` agnóstico al modelo** — reemplazar los dos strings hardcodeados que mencionan `"nomic-embed-text"` (líneas 208 y 245) por referencias al modelo activo:
  - `checkEmbeddingsCoverage`: `"Ensure Ollama is running with an embedding model. Run: ollama pull qwen3-embedding:0.6b"`
  - `checkEmbedProvider`: `"Ensure Ollama is running with an embedding model for semantic search."`

- **Why**: El modelo de embedding es una decisión del usuario. Hoy es imposible cambiarlo sin modificar el binario. La lista de preferencia en auto-detect no es un default hardcodeado — es un ranking que se usa solo cuando el usuario no configuró nada, y puede ignorarse completamente con una línea en `config.yaml`.
- **Where**:
  - `internal/config/config.go` — campos + env vars
  - `internal/embed/ollama.go` — eliminar constantes hardcodeadas + `atomic.Int32` para dims
  - `internal/embed/queue.go` — `pickBestEmbedModel` + lógica en `AutoDetect`
  - `internal/installer/installer.go` — agregar `"qwen3-embedding"` a `looksLikeEmbeddingModel`
  - `internal/health/health.go` — strings agnósticos al modelo
  - `main.go` — wiring en 4 funciones
- **Acceptance**:
  - `config.yaml` con `embeddings.ollama_model: mxbai-embed-large` → el sistema usa ese modelo
  - `NEUROX_EMBED_OLLAMA_MODEL=bge-m3` → override por env var funciona
  - Sin configuración: auto-detect elige el mejor modelo instalado en Ollama
  - Si Ollama no tiene ningún modelo de embedding → `Disabled` con mensaje de ayuda claro
  - `Ollama.Dimensions()` retorna 0 hasta el primer batch, luego el valor real (ej: 768 para nomic)
  - `neurox install` detecta `qwen3-embedding` como modelo válido
  - Test `TestEmbeddingsOllamaConfigWired` — config con `ollama_model: "custom"` → llega al `OllamaConfig`
  - Test `TestPickBestEmbedModel` — mock HTTP con varios modelos → elige el de mayor ranking
  - Test `TestPickBestEmbedModelNoneAvailable` → retorna `""`; `AutoDetect` retorna `Disabled`
  - Test `TestOllamaDynamicDimensions` — primer `EmbedBatch` retorna 768-dim → `Dimensions()` = 768; goroutine-safe (no data race)
  - `go test ./internal/embed/...` verde
  - `go test ./internal/config/...` verde
- **Status**: [x] done

### Step 7: Auto-migración al cambiar el modelo de embedding
- **What**: Sistema que detecta automáticamente cuando el usuario cambia el modelo de embedding y re-embebe el corpus en background sin bloquear el startup.

  **7a. Migration 008 — tabla `db_settings`** (`internal/db/008_db_settings.sql`)
  Tabla KV genérica para metadata del sistema:
  ```sql
  CREATE TABLE db_settings (
      key        TEXT PRIMARY KEY,
      value      TEXT NOT NULL,
      updated_at TEXT NOT NULL DEFAULT (datetime('now'))
  );
  ```
  Registrar en `internal/db/db.go` como migration versión 8: agregar `//go:embed 008_db_settings.sql` y el entry en el slice `migrations`.

  **7b. `ReembedAll(ctx)` en `embed/queue.go`**
  Nueva función pública que hace `UPDATE observations SET embedding = NULL WHERE deleted_at IS NULL` y luego llama `BackfillPending(ctx)`. También exponer como subcomando CLI `neurox reembed` en `main.go` para migración manual forzada.

  **7c. `embed.ModelTracker`** (`internal/embed/tracker.go`)
  ```go
  type ModelTracker struct {
      db    *sql.DB
      queue *Queue
  }

  func NewModelTracker(db *sql.DB, queue *Queue) *ModelTracker

  // CheckAndMigrate:
  //   - db_settings vacío (primer arranque) → guarda modelo actual, no reembed
  //   - modelo igual → no-op (< 1ms)
  //   - modelo distinto → log + ReembedAll en goroutine background + actualiza db_settings al terminar
  func (t *ModelTracker) CheckAndMigrate(ctx context.Context, provider Provider) error
  ```
  Llamado desde `main.go` después de `embed.Queue.Start()`, antes de iniciar el MCP server. No bloquea.
  
  El tracker guarda en `db_settings`:
  - `embed_model` → nombre completo (`provider.Name()`, ej: `"ollama/nomic-embed-text"`)
  - `embed_dims` → dimensiones como string (ej: `"768"`)

  **7d. Thresholds configurables vía `config.yaml`**

  El plan Step 2 cambia `minContradictionSimilarity` a 0.65 como constante. Step 7 lo hace configurable para que el usuario pueda recalibrar al cambiar de modelo sin recompilar.

  **Mecanismo de threading** (explícito para evitar ambigüedad):
  
  1. Agregar `ConsolidationConfig` a `internal/config/config.go`:
     ```go
     type ConsolidationConfig struct {
         DedupThreshold   float64 `yaml:"dedup_threshold"`
         ContradictionMin float64 `yaml:"contradiction_min"`
         ContradictionMax float64 `yaml:"contradiction_max"`
     }
     ```
     Con defaults en `applyDerivedDefaults`: `DedupThreshold=0.85`, `ContradictionMin=0.65`, `ContradictionMax=0.85`. Agregar `Consolidation ConsolidationConfig yaml:"consolidation"` a `Config`.

  2. Expandir `consolidate.Config` (`pipeline.go`):
     ```go
     type Config struct {
         Interval         time.Duration
         DedupThreshold   float64
         ContradictionMin float64
         ContradictionMax float64
     }
     ```

  3. Expandir `contradiction.NewDetector` para aceptar thresholds:
     ```go
     func NewDetector(db *sql.DB, embedder embed.Provider, llmProvider llm.Provider,
         linkStore *links.Store, minSim, maxSim float64) *Detector
     ```
     `Detector` almacena `minSim float64` y `maxSim float64` como campos, reemplazando las constantes de paquete. `findCandidates` usa `d.minSim` y `d.maxSim`.

  4. En `NewPipeline` (`pipeline.go`): pasar `cfg.ContradictionMin` y `cfg.ContradictionMax` a `contradiction.NewDetector`. Usar `cfg.DedupThreshold` en `dedup()` en lugar de la constante `dedupCosineThreshold`.

  5. En `main.go`: poblar `consolidate.Config` desde `cfg.Consolidation` al instanciar el pipeline.

  Valores de referencia por modelo:
  - `nomic-embed-text` (768d): dedup ≥ 0.85, contradiction [0.65, 0.85)
  - `qwen3-embedding` (1024–2560d): dedup ≥ 0.88, contradiction [0.70, 0.88)
  - `bge-m3` (1024d): dedup ≥ 0.87, contradiction [0.68, 0.87)

- **Why**: Sin este sistema, cambiar el modelo en config deja el DB en estado corrupto. El auto-reembed en background garantiza que el corpus converge al nuevo espacio vectorial sin interrumpir el servicio. Los thresholds configurables permiten recalibrar sin recompilar al cambiar de modelo.
- **Where**:
  - `internal/db/008_db_settings.sql` (nuevo)
  - `internal/db/db.go` — embed directive + migration 8
  - `internal/embed/queue.go` — `ReembedAll(ctx)`
  - `internal/embed/tracker.go` (nuevo) — `ModelTracker`
  - `internal/config/config.go` — nueva sección `ConsolidationConfig` con defaults en `applyDerivedDefaults`
  - `internal/consolidate/pipeline.go` — expandir `Config` + usar thresholds desde campos
  - `internal/contradiction/detector.go` — expandir `NewDetector` + campos en `Detector` en lugar de constantes
  - `main.go` — instanciar `ModelTracker` + subcomando `neurox reembed` + poblar `consolidate.Config`
- **Acceptance**:
  - Primer arranque (DB nueva) → guarda modelo en `db_settings`, sin reembed
  - Arranque con mismo modelo → no-op, < 1ms overhead
  - Arranque con modelo distinto → log `"embed model changed nomic-embed-text→qwen3-embedding:4b, re-embedding 974 observations"` + `BackfillPending` asíncrono
  - `neurox reembed` fuerza el proceso manualmente desde CLI
  - `config.yaml` con `consolidation.dedup_threshold: 0.90` → pipeline usa ese valor
  - Test `TestModelTrackerFirstRun` → solo guarda, sin reembed
  - Test `TestModelTrackerNoChange` → no-op
  - Test `TestModelTrackerChanged` → `ReembedAll` invocado + `db_settings` actualizado
  - Test `TestReembedAll` → obs con embedding ≠ NULL → post call → embedding = NULL + enqueued
  - Test `TestConsolidationThresholdsFromConfig` → config custom → pipeline usa esos valores
  - Test `TestDetectorThresholds` → `NewDetector` con `minSim=0.70` → pares con sim=0.65 no son candidatos
  - `go test -race ./...` verde (verifica goroutine-safety de `atomic.Int32` en `Ollama.dims`)
- **Status**: [x] done

### Step 8: Verificación final
- **What**: Ejecutar la suite completa, verificar el brain score, instalar el git hook.
- **Why**: Gate de calidad antes de considerar el plan terminado.
- **Where**: Proyecto raíz
- **Acceptance**:
  - `CGO_ENABLED=1 go build -tags fts5 ./...` limpio
  - `go vet ./...` sin warnings
  - `CGO_ENABLED=1 go test -tags fts5 ./...` verde
  - `CGO_ENABLED=1 go test -race -tags fts5 ./...` verde
  - `neurox health_check` → score ≥ 90
  - `neurox status` → `expired` ≤ 50, `active_sessions` ≤ 1
  - `neurox install-hook` ejecutado correctamente
- **Status**: [x] done

---

## Verification

```bash
# Build
CGO_ENABLED=1 go build -tags fts5 -o neurox .

# Tests
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go test -race -tags fts5 ./...

# Health post-fix
./neurox health_check

# Estado del DB post-migration
./neurox status

# Git hook
./neurox install-hook
```

---

## Risks / Notes

- **Step 2 + Step 7 interacción**: El threshold 0.65 está calibrado para `nomic-embed-text`. Al migrar a otro modelo (Step 7), los thresholds deben recalibrarse. Por eso Step 7 los hace configurables en `config.yaml` en lugar de mantenerlos como constantes.
- **Step 3 (rescue migration)**: El criterio "sin enlace supersedes apuntando" es conservador. Observaciones legítimamente supersedidas conservan `expired`. Riesgo de rescatar una obs realmente inválida: bajo.
- **Step 5 (títulos)**: El LLM no garantiza el prefijo `TITLE:`. El fallback `"Synthesis: {namespace}"` es mejor que el actual `"Reflection: {namespace}"`. Las reflections duplicadas existentes en Core no se limpian automáticamente — usar `neurox forget <id>` manualmente si se desea.
- **Step 6 auto-detect**: `pickBestEmbedModel` hace una llamada HTTP adicional a `/api/tags` dentro de `AutoDetect`. `AutoDetect` ya llamaba a `Ping()` (que también usa `/api/tags`) seguido de un test embed. Se puede optimizar consolidando ambas llamadas, o dejarlo simple con dos llamadas ya que `AutoDetect` se ejecuta solo al startup y no es hot-path.
- **Step 6 installer**: `writeConfigFile` en `installer.go` intencionalmente NO escribe `embeddings.ollama_model` al elegir Ollama — el auto-detect elige el mejor modelo disponible en runtime. Si el usuario quiere fijar un modelo específico, puede editar `config.yaml` manualmente o usar `NEUROX_EMBED_OLLAMA_MODEL`.
- **Step 7 background reembed**: Durante el re-embed hay una ventana donde coexisten embeddings del modelo viejo y del nuevo. La ventana dura segundos/minutos para ~1000 obs — aceptable. `CosineSimilarity` retorna 0 para pares de dimensión distinta (falla visible), o scores incorrectos para mismo dim pero distinto espacio (falla silenciosa). En ambos casos el dedup y contradiction simplemente no actúan sobre esos pares — el impacto es bajo.
- **Tags/file-links coverage (33%)**: Problema de disciplina histórica de uso, no de código. Mejora orgánicamente. No se aborda en este plan.
