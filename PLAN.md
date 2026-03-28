# Plan: Batch consolidation UPDATEs to fix integration test timeout

## Goal

Eliminar el timeout de 10 minutos en `TestBenchmarkSuite_Small` optimizando el pipeline de consolidación para usar UPDATEs batch en vez de UPDATEs individuales por fila. Esto es una mejora de rendimiento real para producción, no solo un fix de tests.

## Business Context

- **Problema**: El CI falla con `panic: test timed out after 10m0s` en `TestBenchmarkSuite_Small` desde la migración a ncruces/go-sqlite3 (pure-Go SQLite). El test ejecuta 9 dimensiones de benchmark que acumulan ~8,000 observaciones en una DB compartida, y la consolidación se vuelve exponencialmente lenta.
- **Impacto**: CI bloqueado — no se pueden mergear PRs.
- **Criterio de éxito**: `go test -race ./...` pasa en CI (< 10 minutos). El pipeline de consolidación procesa 8K+ observaciones sin degradación significativa.

## Technical Context

### Causa raíz

El trigger FTS5 `trg_obs_au` (schema.sql:78-83) se dispara en **cada UPDATE** a la tabla `observations`, ejecutando un DELETE+INSERT en `observations_fts`. Cuando el pipeline de consolidación actualiza campos no-FTS (`layer`, `consolidation_status`, `deleted_at`, `activation_level`, `importance`), el trigger re-indexa innecesariamente `title`, `content` y `tags`.

Con el driver pure-Go (ncruces/go-sqlite3-wasm), las operaciones FTS5 (`_fts5IndexMergeLevel`, `_fts5DataDelete`) son significativamente más lentas que con el viejo driver CGO, amplificando el problema.

### Puntos calientes identificados

| Función | Archivo | Patrón | Impacto |
|---|---|---|---|
| `promoteBufferToWorking` | `consolidate/pipeline.go:516-552` | UPDATE individual por candidato | **Crítico** — procesa todos los buffer pendientes |
| `promoteWorkingToCore` | `consolidate/pipeline.go:757-764` | UPDATE individual por candidato | Medio — filtra bien |
| `dedup` | `consolidate/pipeline.go:915-918` | UPDATE individual por duplicado | Alto — O(n²) + FTS5 por cada |
| `dedupReflections` | `consolidate/pipeline.go:1026-1029` | UPDATE individual | Bajo — pocas reflections |
| `GarbageCollect` | `decay/engine.go:152-163` | UPDATE individual por candidato | Medio |
| `ApplyDecay` | `decay/engine.go:74-82` | ✅ Ya es batch — UPDATE con WHERE | OK |
| `evictBuffer` | `consolidate/pipeline.go:836-846` | ✅ Ya es batch — subquery | OK |

### Estrategia de optimización

Según la investigación (PowerSync blog, SQLite FTS5 docs, batch processing patterns):

1. **Batch UPDATEs con `WHERE id IN (...)`**: Agrupar IDs de candidatos y hacer un solo UPDATE. Reduce N disparos de trigger FTS5 a 1.
2. **Transacciones explícitas**: Envolver batches en una transacción para amortizar el overhead de WAL checkpoint.
3. **Chunking**: Para listas muy largas de IDs, dividir en chunks de ~500 para evitar alcanzar el límite de variables SQLite (32766 en versiones modernas, pero 999 en algunas compilaciones).

### Archivos afectados

- `internal/consolidate/pipeline.go` — promoteBufferToWorking, promoteWorkingToCore, dedup, dedupReflections
- `internal/decay/engine.go` — GarbageCollect
- `tests/integration/bench_test.go` — agregar timeout safety net

## Implementation Steps

### Step 1: Crear helper de batch UPDATE con chunking
- **What**: Crear una función utilitaria `batchUpdateByIDs(ctx, db, query, ids, chunkSize)` en `internal/consolidate/batch.go` que ejecute un UPDATE con `WHERE id IN (?, ?, ...)` en chunks de hasta 500 IDs. La función construye los placeholders dinámicamente y ejecuta cada chunk en una transacción. También crear un helper similar o reutilizable para `internal/decay/`.
- **Why**: Centralizar la lógica de batch para no repetirla en cada función. El chunking previene exceder el límite de parámetros SQLite.
- **Where**: `internal/consolidate/batch.go` (nuevo)
- **Acceptance**:
  - Helper compila y tiene tests unitarios con tabla de casos (0 IDs, 1 ID, 499 IDs, 500 IDs, 1001 IDs)
  - Cada chunk se ejecuta en una transacción
  - Retorna el total de filas afectadas
- **Status**: [x] done

### Step 2: Convertir promoteBufferToWorking a batch
- **What**: Refactorizar `promoteBufferToWorking` (gate-off path, líneas 484-555) para:
  1. Recolectar todos los IDs candidatos a promover en slices separados por tipo de promoción (procedural, high-importance, composite score)
  2. Ejecutar un solo `UPDATE observations SET layer = 1, consolidation_status = 'promoted', updated_at = datetime('now') WHERE id IN (...)` usando el helper batch por cada grupo
  3. Hacer lo mismo con el gate-on path (líneas 558-640)
- **Why**: Esta es la función más crítica — procesa TODOS los buffer pendientes sin límite, causando miles de triggers FTS5 individuales.
- **Where**: `internal/consolidate/pipeline.go` (función `promoteBufferToWorking`)
- **Acceptance**:
  - Un solo UPDATE (o pocos chunks) en vez de N UPDATEs individuales
  - Los tests existentes de consolidación (`internal/consolidate/`) siguen pasando
  - Mismo comportamiento lógico: procedural auto-promote, high-importance auto-promote, composite score threshold
- **Status**: [x] done

### Step 3: Convertir promoteWorkingToCore y dedup a batch
- **What**: 
  - `promoteWorkingToCore`: Recolectar IDs + sus nuevos valores de importance, hacer batch UPDATE. Nota: cada candidato tiene un `newImportance` diferente, así que se necesita un enfoque de UPDATE por valor o agrupar por rango de importance.
  - `dedup`: Recolectar todos los IDs a soft-delete, hacer un solo `UPDATE ... SET deleted_at = datetime('now') WHERE id IN (...)`.
  - `dedupReflections`: Mismo patrón que dedup.
- **Why**: dedup con O(n²) comparaciones + UPDATE individual es el segundo punto caliente. promoteWorkingToCore es menos crítico pero tiene el mismo anti-patrón.
- **Where**: `internal/consolidate/pipeline.go` (funciones `promoteWorkingToCore`, `dedup`, `dedupReflections`)
- **Acceptance**:
  - dedup hace un solo batch DELETE al final en vez de N individuales
  - promoteWorkingToCore hace batch UPDATEs (agrupados por newImportance o con UPDATE FROM CTE)
  - Tests existentes pasan
- **Status**: [x] done

### Step 4: Convertir GarbageCollect a batch
- **What**: Refactorizar `GarbageCollect` en `decay/engine.go` (líneas 150-164) para recolectar todos los IDs `toDelete` y hacer un solo batch UPDATE usando el helper o un enfoque inline `WHERE id IN (...)` con chunking.
- **Why**: El GC actual dice `// Soft-delete in batches` pero hace UPDATEs individuales. Con el helper ya creado, esto es trivial de arreglar.
- **Where**: `internal/decay/engine.go` (función `GarbageCollect`)
- **Acceptance**:
  - Un solo UPDATE (o chunked) en vez del loop for-each
  - Tests existentes de decay pasan
  - Comentario `// Soft-delete in batches` es ahora verdadero
- **Status**: [x] done

### Step 5: Agregar timeout safety net al test de integración
- **What**: Agregar `context.WithTimeout(ctx, 8*time.Minute)` en `TestBenchmarkSuite_Small` para que el test falle con un mensaje claro en vez de un `panic: test timed out`. También considerar agregar `if testing.Short() { t.Skip("...") }` para permitir `go test -short`.
- **Why**: Safety net — si una futura regresión de rendimiento ocurre, el mensaje de error será útil en vez de un stack trace críptico de FTS5.
- **Where**: `tests/integration/bench_test.go`
- **Acceptance**:
  - Test tiene timeout propio con mensaje descriptivo
  - `go test -short ./tests/integration/` salta el test largo
- **Status**: [x] done

### Step 6: Verificación completa y race test
- **What**: Ejecutar la suite completa de verificación:
  1. `go build ./...`
  2. `go vet ./...`
  3. `go test ./internal/consolidate/...` — tests de consolidación
  4. `go test ./internal/decay/...` — tests de decay
  5. `go test ./tests/integration/...` — el test que falla
  6. `go test -race ./...` — race conditions
- **Why**: Validar que los cambios de batch no introducen regresiones ni race conditions. Los batch UPDATEs dentro de transacciones podrían tener comportamiento diferente bajo concurrencia.
- **Where**: Todo el proyecto
- **Acceptance**:
  - `go test -race ./...` pasa sin timeout
  - `TestBenchmarkSuite_Small` completa en < 5 minutos (idealmente < 3 minutos)
  - Cero race conditions detectadas
  - Cero tests rotos
- **Status**: [x] done

## Verification

```bash
# Build
go build ./...

# Vet
go vet ./...

# Tests unitarios del pipeline y decay
go test -v ./internal/consolidate/... ./internal/decay/...

# Test de integración que falla
go test -v -timeout 10m ./tests/integration/ -run TestBenchmarkSuite_Small

# Race test completo
go test -race -timeout 15m ./...
```

## Risks / Notes

- **Límite de parámetros SQLite**: El default de `SQLITE_MAX_VARIABLE_NUMBER` es 999 en algunas builds y 32766 en otras. Usar chunks de 500 es seguro para todas las builds. ncruces/go-sqlite3 usa el SQLite amalgamation con el default alto, pero ser conservador no cuesta nada.
- **Transacciones y FTS5**: Un batch UPDATE dentro de una transacción dispara el trigger FTS5 por cada fila afectada, pero el FTS5 merge se aplaza al COMMIT. Esto es mucho más eficiente que N transacciones individuales porque el merge se hace una sola vez.
- **promoteWorkingToCore con importancias distintas**: Cada candidato puede tener un `newImportance` diferente (calculado por `calculateCoreImportance`). Opciones: (a) agrupar por valor redondeado, (b) usar UPDATE FROM con CTE, (c) mantener individual si son pocos candidatos (< 50 típicamente). Opción (c) es pragmática dado el LIMIT 50 implícito del query.
- **Compatibilidad hacia atrás**: Los cambios son puramente de rendimiento — la lógica de negocio (qué se promueve, qué se deduplica) no cambia.
- **ApplyDecay ya es batch**: `decay.ApplyDecay` ya usa UPDATEs masivos con WHERE. No necesita cambios, pero sí dispara el trigger FTS5 para miles de filas. Considerar si el trigger `trg_obs_au` debería excluir actualizaciones que no tocan campos FTS (title, content, tags). Esto sería una optimización adicional fuera del scope de este plan, pero se documenta como mejora futura.
- **Mejora futura — trigger condicional**: SQLite no soporta `WHEN NEW.title != OLD.title` en triggers sobre tablas virtuales FTS5 de forma estándar, pero se podría hacer el trigger condicional: `WHEN NEW.title != OLD.title OR NEW.content != OLD.content OR NEW.tags != OLD.tags`. Esto eliminaría el re-indexado FTS5 para updates de metadata. Sin embargo, esto requiere cambios al schema (migración) y está fuera del scope de este plan.
