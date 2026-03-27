# Plan: Reconsolidación de reflections + spreading activation entre namespaces

## Goal

Hacer que Neurox mantenga una única reflection activa por namespace en `observations`, preservando historial mediante `supersedes` y soft-delete del nodo anterior, y añadir links semánticos `relates_to` entre namespaces como base de spreading activation en el grafo.

Esta primera versión **no** cambia el motor de recall para atravesar links cross-namespace. El objetivo de spreading activation aquí es enriquecer el grafo y dejar la base lista para una futura integración en recall, sin prometer comportamiento que el código actual todavía no implementa.

## Business Context

- **Problema 1 — reflections acumuladas**: hoy cada ciclo de reflect crea un nuevo observation `source='reflection'` y un nuevo row en `reflections`, por lo que un namespace activo acumula múltiples síntesis activas difíciles de distinguir y mantener.
- **Problema 2 — namespaces aislados en el grafo**: observaciones de namespaces distintos pueden compartir patrones, pero hoy no se enlazan automáticamente salvo por relaciones explícitas ya existentes.
- **Resultado esperado**:
  - En `observations`, debe existir **solo una reflection activa por namespace**.
  - Las reflections previas deben mantenerse accesibles como historial lógico mediante `supersedes` + soft-delete.
  - La tabla `reflections` debe quedar **append-only** como historial de síntesis; esta iteración no requiere cleanup ni mutación retroactiva allí.
  - El pipeline puede crear links `relates_to` entre namespaces cuando haya similitud semántica suficiente, pero **sin cambiar todavía recall cross-namespace**.
- **Restricciones**:
  - Usar el enum real de `relation_type`; el valor válido es `relates_to`.
  - Evitar scope extra de schema salvo la migración necesaria para cleanup legado de reflections activas en `observations`.
  - Mantener compatibilidad con el wiring actual de configuración y migraciones.

## Technical Context

- `internal/reflect/engine.go` hoy tiene `Run()`, `ForceReflect()`, `synthesize()`, `saveReflection()` y `lastReflectionAt()`. Ambos paths siempre generan una reflection nueva.
- `saveReflection()` inserta tanto en la tabla `reflections` como en `observations`, y además crea links `derived_from` hacia las observaciones fuente.
- `internal/consolidate/pipeline.go` ya tiene:
  - `dedup()` para layer 1
  - `dedupReflections()` para `observations` en layer 2 con `source='reflection'`
- `internal/links/link.go` y `internal/db/schema.sql` definen `relation_type` como enum cerrado con `supersedes`, `contradicts`, `relates_to`, `derived_from`, `validates`, `refines`.
- `internal/recall/engine.go` no consulta `observation_links` ni hace traversal, por lo que links cross-namespace **no** alteran el resultado de `Search()` por sí solos.
- `internal/config/config.go` y `main.go` hoy solo exponen `DedupThreshold`, `ContradictionMin` y `ContradictionMax` para consolidación.
- `internal/db/db.go` embebe migraciones hasta `008_db_settings.sql`; `009` todavía no existe.
- Los tests existentes en `internal/reflect/engine_test.go`, `internal/consolidate/pipeline_test.go`, `internal/config/config_test.go` y `internal/recall/engine_test.go` marcan el patrón esperado para ampliar cobertura.

## Implementation Steps

### Step 1: Detectar la reflection activa e incorporar reconsolidación al prompt

- **What**:
  - Añadir en `internal/reflect/engine.go` un helper privado para obtener la reflection activa de un namespace desde `observations` (`source='reflection'` y `deleted_at IS NULL`).
  - Actualizar `synthesize()` para aceptar opcionalmente el contenido de la reflection activa previa y usarlo como contexto de reconsolidación en el prompt.
  - Mantener el comportamiento actual cuando no exista reflection previa.
- **Why**:
  - La reconsolidación necesita que el LLM vea el “schema” previo antes de sintetizar la nueva versión enriquecida.
- **Where**:
  - `internal/reflect/engine.go`
  - `internal/reflect/engine_test.go`
- **Acceptance**:
  - Si no existe reflection activa en el namespace, el helper retorna `nil` sin error.
  - Si existe una reflection activa, retorna la más reciente no soft-deleted.
  - `synthesize()` conserva el prompt actual cuando no hay reflection previa.
  - `synthesize()` incluye explícitamente la reflection previa cuando sí existe.
  - Hay tests que cubren ambos caminos.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/reflect`
- **Status**: [x] done

### Step 2: Guardar la nueva reflection de forma transaccional y dejar una sola activa

- **What**:
  - Reestructurar `Run()` y `ForceReflect()` para:
    1. cargar la reflection activa del namespace,
    2. sintetizar la nueva reflection con o sin contexto previo,
    3. guardar todo en una transacción.
  - Actualizar `saveReflection()` para que la operación sea atómica:
    - insertar un nuevo row en `reflections` (historial append-only),
    - insertar el nuevo observation `source='reflection'`,
    - crear links `derived_from` a las fuentes,
    - si había reflection activa previa, crear link `supersedes` del nuevo observation al anterior,
    - soft-delete del observation anterior.
  - Mantener `lastReflectionAt()` basado en la tabla `reflections`, ya que sigue siendo el historial cronológico completo.
- **Why**:
  - La regla de negocio central es “una sola reflection activa por namespace”, sin perder trazabilidad histórica.
  - La transacción evita estados inconsistentes entre `reflections`, `observations` y `observation_links`.
- **Where**:
  - `internal/reflect/engine.go`
  - `internal/reflect/engine_test.go`
- **Acceptance**:
  - Primera reflection de un namespace: crea row en `reflections`, crea observation activo, no crea `supersedes`.
  - Segunda reflection del mismo namespace: crea nuevo row en `reflections`, crea nuevo observation activo, crea link `supersedes`, soft-delete del observation anterior.
  - Tras múltiples reconsolidaciones, `SELECT COUNT(*) FROM observations WHERE namespace=? AND source='reflection' AND deleted_at IS NULL` devuelve siempre `1`.
  - La tabla `reflections` sigue creciendo append-only; no se borra ni actualiza historial en esta iteración.
  - Hay tests para la cadena de `supersedes` y la unicidad del nodo activo.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/reflect`
- **Status**: [x] done

### Step 3: Limpiar datos heredados y exponer thresholds de spreading activation

- **What**:
  - Crear migración `internal/db/009_reconcile_active_reflections.sql` para soft-delete de reflections heredadas en `observations`, dejando solo una activa por namespace.
  - Registrar la migración en `internal/db/db.go`.
  - Extender `internal/config/config.go`, `internal/consolidate/pipeline.go` y `main.go` con thresholds explícitos para spreading activation (`RelatedMin`, `RelatedMax`), con defaults coherentes con dedup:
    - `RelatedMin = 0.65`
    - `RelatedMax = DedupThreshold`
- **Why**:
  - La migración deja el estado histórico compatible con la nueva regla antes de que corra la reconsolidación viva.
  - Los thresholds deben quedar configurables como ya ocurre con dedup y contradiction detection.
- **Where**:
  - `internal/db/009_reconcile_active_reflections.sql`
  - `internal/db/db.go`
  - `internal/db/db_test.go`
  - `internal/config/config.go`
  - `internal/config/config_test.go`
  - `internal/consolidate/pipeline.go`
  - `main.go`
- **Acceptance**:
  - Después de migrar, cada namespace tiene como máximo una reflection activa en `observations`.
  - La migración no borra rows de la tabla `reflections`.
  - `config.Load()` aplica defaults para `RelatedMin` y `RelatedMax`.
  - `main.go` pasa los nuevos campos a `consolidate.Config`.
  - Hay tests para defaults/config parsing y para registrar/aplicar la migración 009.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/config ./internal/db ./internal/consolidate`
- **Status**: [x] done

### Step 4: Añadir `relates_to` cross-namespace al pipeline de consolidación

- **What**:
  - Implementar un paso privado en `internal/consolidate/pipeline.go` que evalúe observaciones con embedding en namespaces distintos y cree links `relates_to` cuando la similitud caiga en la ventana `[RelatedMin, RelatedMax)`.
  - Respetar el enum real del schema usando `links.RelationRelatesTo`.
  - Evitar duplicados comprobando links existentes entre el par en cualquier dirección.
  - Limitar cardinalidad inicial por observación (por ejemplo top-N) para controlar explosión de links.
  - Añadir contadores al `RunResult` y logging del paso.
  - Ejecutar este paso después de dedup y antes de finalizar el pipeline.
- **Why**:
  - Esto construye la capa de grafo necesaria para future spreading activation sin tocar todavía el comportamiento de recall.
- **Where**:
  - `internal/consolidate/pipeline.go`
  - `internal/consolidate/pipeline_test.go`
  - opcionalmente `internal/links/store_test.go` si hace falta validar helpers
- **Acceptance**:
  - Dos observaciones de namespaces distintos con similitud dentro de la ventana crean un link `relates_to`.
  - Observaciones del mismo namespace no generan `relates_to` por este paso.
  - Pares con similitud `>= RelatedMax` no generan `relates_to`.
  - Pares con similitud `< RelatedMin` no generan `relates_to`.
  - Si ya existe un link equivalente entre el par, no se duplica.
  - El código usa `relates_to`, no `related_to`.
  - La aceptación **no** promete recall cross-namespace; el resultado esperado en v1 es enriquecimiento del grafo, no cambio en `recall.Search()`.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/consolidate`
- **Status**: [x] done

### Step 5: Verificación integral y documentación de límites de la versión

- **What**:
  - Ejecutar validación completa de build, vet y tests.
  - Verificar manualmente en DB que:
    - solo existe una reflection activa por namespace en `observations`,
    - `reflections` conserva historial append-only,
    - existen links `supersedes` y `relates_to` donde corresponde.
  - Dejar explícito en notas o comentarios del cambio que recall cross-namespace queda fuera de esta versión.
- **Why**:
  - Cierra el cambio con verificación técnica realista y evita una expectativa de producto que el código aún no cumple.
- **Where**:
  - raíz del proyecto
  - comentarios/notas en los archivos tocados si aplica
- **Acceptance**:
  - `CGO_ENABLED=1 go build -tags fts5 ./...`
  - `CGO_ENABLED=1 go vet -tags fts5 ./...`
  - `CGO_ENABLED=1 go test -tags fts5 ./...`
  - `CGO_ENABLED=1 go test -race -tags fts5 ./...`
  - `CGO_ENABLED=1 go build -tags fts5 -o neurox .`
  - Verificación SQL manual:
    - `SELECT namespace, COUNT(*) FROM observations WHERE source='reflection' AND deleted_at IS NULL GROUP BY namespace;` devuelve máximo 1 por namespace.
    - `SELECT COUNT(*) FROM observation_links WHERE relation_type='supersedes';` refleja reconsolidaciones realizadas.
    - `SELECT COUNT(*) FROM observation_links WHERE relation_type='relates_to';` refleja links cross-namespace creados por el pipeline.
- **Status**: [x] done

## Verification

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/reflect
CGO_ENABLED=1 go test -tags fts5 ./internal/config ./internal/db ./internal/consolidate
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go test -race -tags fts5 ./...
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

Verificaciones manuales recomendadas:

```sql
SELECT namespace, COUNT(*)
FROM observations
WHERE source='reflection' AND deleted_at IS NULL
GROUP BY namespace;

SELECT relation_type, COUNT(*)
FROM observation_links
WHERE relation_type IN ('supersedes', 'relates_to')
GROUP BY relation_type;

SELECT namespace, COUNT(*)
FROM reflections
GROUP BY namespace;
```

## Risks / Notes

- **Límite intencional de la v1**: crear links `relates_to` entre namespaces no modifica `recall.Search()` todavía. Cualquier historia de “recall cross-namespace” requiere un cambio posterior en `internal/recall/engine.go`.
- **Historial dual de reflections**:
  - `observations`: una sola reflection activa por namespace.
  - `reflections`: historial append-only completo.
  Esto es intencional y debe mantenerse explícito.
- **Integridad transaccional**: la reconsolidación debe guardar observation nuevo, link `supersedes` y soft-delete del anterior en la misma transacción para evitar dos reflections activas temporalmente.
- **Migración 009**: solo debe reconciliar el espejo en `observations`; no debe borrar historial de `reflections`.
- **Costo del paso cross-namespace**: comparar embeddings entre namespaces puede crecer rápido; conviene limitar top-N por observación y revisar impacto inicial en tiempo de consolidación.
- **Compatibilidad de schema**: no hace falta migrar `relation_type`; el schema actual ya soporta `relates_to`.
