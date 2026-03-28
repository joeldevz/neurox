# Plan: Recalibrar la consolidación de memoria

## Goal

Corregir la lógica de consolidación de Neurox para que el sistema deje de empujar conocimiento durable hacia importancias residuales, y para que la promoción a Working/Core refleje mejor estabilidad y utilidad real de la memoria.

El objetivo de esta iteración es mejorar decay, retrieval y promotion dentro del motor de memoria. **No** incluye cambios de proveedor/modelo LLM ni experimentación con modelos.

## Business Context

- **Problema principal**: hoy muchas observaciones valiosas (`decision`, `bugfix`, `gotcha`, `preference`) terminan en `0.01` aunque sigan siendo conocimiento importante para el agente.
- **Consecuencia**: el grafo y el recall pierden capacidad de priorización, porque `importance` deja de discriminar bien entre conocimiento durable y ruido operativo.
- **Síntoma observable**:
  - `ApplyDecay()` reduce `importance` linealmente para Buffer/Working.
  - `bumpAccess()` vuelve a subir `importance` directamente al recordar.
  - `promoteWorkingToCore()` promueve por edad + accesos, pero no recalibra el valor semántico al entrar a Core.
  - Core acaba almacenando conocimiento estable con scores heredados ya degradados.
- **Resultado esperado**:
  - `importance` debe comportarse como valor durable, no como frescura momentánea.
  - La accesibilidad reciente debe afectar la activación, no destruir la importancia semántica.
  - La promoción a Core debe producir memorias estables y útiles, no solo memorias viejas y muy accedidas.
  - Los datos existentes deben poder reconciliarse sin perder historial.
- **Restricciones**:
  - Mantener compatibilidad con SQLite y el patrón actual de migraciones embebidas.
  - Preservar `retention='operational'` como barrera para no contaminar Core con trazas operativas.
  - No mezclar en este plan trabajo sobre selección de modelos o tuning del proveedor LLM.

## Technical Context

- `internal/decay/engine.go` hoy usa:
  - `ApplyDecay()` para restar importancia fija por epoch a `layer < 2`
  - `ActivationScore()` para GC a partir de `importance`, `daysSinceAccess` y `access_count`
- `internal/recall/engine.go` incrementa `access_count`, `last_accessed` y además sube `importance` en cada recall (`+0.03`).
- `internal/consolidate/pipeline.go` hoy promueve:
  - Buffer → Working por `importance >= 0.3` o `kind='procedural'`
  - Working → Core por `retention='durable'`, `access_count >= 5` y `age >= 7 days`
- `internal/db/schema.sql` ya contiene `access_count`, `last_accessed`, `repetition_count`, `decay_rate`, `modified_epoch`, pero la lógica actual no separa claramente valor durable vs activación.
- Los tests existentes en `internal/decay/engine_test.go`, `internal/consolidate/pipeline_test.go` y `internal/recall/engine_test.go` ya cubren decay, promotion y recall bump; son el punto natural para construir la red de seguridad.
- El proyecto ya tiene una política explícita de `retention` (`internal/observation/observation.go`) y memoria Core orientada a conocimiento estable; la consolidación actual no refleja bien esa intención.

## Implementation Steps

### Step 1: Crear red de seguridad y baseline de consolidación
- **What**: ampliar tests y métricas para capturar el comportamiento actual de decay, recall bump y promotion antes de refactorizar.
- **Why**: la consolidación es transversal; hace falta un baseline reproducible para evitar corregir una zona rompiendo otra.
- **Where**:
  - `internal/decay/engine_test.go`
  - `internal/consolidate/pipeline_test.go`
  - `internal/recall/engine_test.go`
- **Acceptance**:
  - Existen tests que demuestran explícitamente el problema actual: observaciones durables degradadas antes de Core y promotion a Core sin recalibración.
  - Existen fixtures diferenciando `decision`, `bugfix`, `preference`, `discovery` y `operational`.
  - Hay cobertura para accesos repetidos, edad de la observación y capas Buffer/Working/Core.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/decay ./internal/consolidate ./internal/recall`
  - **Status**: [x] done

### Step 2: Separar en storage la señal de valor durable y la señal de activación
- **What**: introducir columnas explícitas para activación/fortaleza de consolidación y ajustar el modelo persistido para que `importance` deje de cargar solo con recencia y accesos.
- **Why**: mientras `importance` siga representando a la vez valor, frescura y promotion eligibility, la escala seguirá colapsándose.
- **Where**:
  - `internal/db/` nueva migración
  - `internal/db/db.go`
  - `internal/db/schema.sql`
  - `internal/observation/store.go`
  - `internal/observation/observation.go`
  - tests de DB/store relacionados
- **Acceptance**:
  - El schema soporta guardar por separado la activación reciente y la fuerza de consolidación sin perder compatibilidad con datos existentes.
  - Las observaciones nuevas reciben defaults coherentes.
  - La migración deja los registros existentes en estado válido y sin NULLs inesperados.
  - Hay tests de migración y persistencia para los nuevos campos.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/db ./internal/observation`
  - **Status**: [x] done

### Step 3: Reescribir decay y recall bump para que afecten activación, no valor durable
- **What**:
  - cambiar `ApplyDecay()` para que degrade principalmente activación y no fuerce `importance` hacia `0.01` en memorias durables.
  - cambiar `bumpAccess()` para que un recall refuerce activación y consolidación de forma controlada, sin inflar `importance` ciegamente.
  - mantener `GarbageCollect()` alineado con la nueva semántica.
- **Why**: en memoria humana la accesibilidad cambia rápido, pero el valor durable no debería destruirse por falta de uso reciente.
- **Where**:
  - `internal/decay/engine.go`
  - `internal/decay/engine_test.go`
  - `internal/recall/engine.go`
  - `internal/recall/engine_test.go`
- **Acceptance**:
  - El decay deja de reducir linealmente `importance` en Buffer/Working como comportamiento principal.
  - Recall aumenta accesibilidad reciente sin volver a mezclarla con la importancia durable.
  - GC sigue pudiendo eliminar trazas débiles y antiguas, usando la nueva señal correcta.
  - Los tests muestran que una observación durable puede perder activación sin perder su valor semántico base.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/decay ./internal/recall`
  - **Status**: [x] done

### Step 4: Recalibrar las promociones Buffer→Working y Working→Core
- **What**:
  - redefinir los criterios de promotion usando valor durable + activación + fuerza de consolidación + retention.
  - recalibrar explícitamente observaciones que entren en Core para que no hereden scores colapsados.
  - mantener `operational` fuera de Core.
- **Why**: hoy la promotion a Core representa antigüedad y accesos, pero no la estabilidad semántica de la memoria consolidada.
- **Where**:
  - `internal/consolidate/pipeline.go`
  - `internal/consolidate/pipeline_test.go`
  - `internal/config/config.go` y tests si aparecen nuevos thresholds configurables
  - `main.go` si se exponen nuevos parámetros
- **Acceptance**:
  - Buffer → Working deja de depender solo de un cutoff plano de `importance`.
  - Working → Core requiere estabilidad real además de accesos/edad.
  - Una observación durable promovida a Core nunca queda con un valor semántico "muerto" por herencia de decay previo.
  - Observaciones operativas permanecen en Buffer/Working aunque tengan mucho uso.
  - Hay tests de promoción por tipo, retention, acceso espaciado y antigüedad.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/consolidate ./internal/config`
  - **Status**: [x] done

### Step 5: Reconciliar datos existentes y corregir scores heredados
- **What**: implementar una migración o rutina de backfill que recalibre observaciones existentes usando capa, retention, tipo, accesos y edad, para sacar de `0.01` el conocimiento durable que hoy está artificialmente deprimido.
- **Why**: aunque la lógica nueva sea correcta, la base actual seguiría arrastrando miles de observaciones mal calibradas.
- **Where**:
  - `internal/db/` nueva migración o rutina explícita de backfill
  - `internal/consolidate/pipeline.go` si el backfill forma parte de consolidación controlada
  - tests de integración o DB
- **Acceptance**:
  - El backfill no cambia IDs, historial ni relaciones.
  - Observaciones durables de Core/Working recuperan una escala útil de prioridad.
  - Observaciones claramente operativas o efímeras no se inflan artificialmente.
  - Hay tests o fixtures que validan antes/después sobre datos representativos.
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/db ./internal/consolidate`
  - **Status**: [x] done

### Step 6: Validación integral de comportamiento y calidad del ranking
- **What**: ejecutar validación técnica completa y una auditoría manual de muestras para comprobar que el ranking final tiene más sentido semántico que antes.
- **Why**: la mejora real no es solo pasar tests, sino que `importance` vuelva a servir para priorizar conocimiento útil.
- **Where**:
  - raíz del proyecto
  - queries manuales sobre la DB
- **Acceptance**:
  - `CGO_ENABLED=1 go build -tags fts5 ./...`
  - `CGO_ENABLED=1 go vet -tags fts5 ./...`
  - `CGO_ENABLED=1 go test -tags fts5 ./...`
  - `CGO_ENABLED=1 go test -race -tags fts5 ./...`
  - `CGO_ENABLED=1 go build -tags fts5 -o neurox .`
  - Auditoría manual confirma que observaciones durables ya no se concentran masivamente en `0.01`.
  - Auditoría manual confirma que Core contiene conocimiento estable mejor calibrado que antes.
  - **Status**: [x] done

## Verification

```bash
CGO_ENABLED=1 go test -tags fts5 ./internal/db ./internal/observation
CGO_ENABLED=1 go test -tags fts5 ./internal/decay ./internal/recall ./internal/consolidate ./internal/config
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go test -race -tags fts5 ./...
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

Verificaciones manuales recomendadas:

```sql
SELECT observation_type, layer, retention,
       COUNT(*) AS total,
       ROUND(AVG(importance), 3) AS avg_importance
FROM observations
WHERE deleted_at IS NULL
GROUP BY observation_type, layer, retention
ORDER BY avg_importance DESC;

SELECT COUNT(*)
FROM observations
WHERE deleted_at IS NULL
  AND retention = 'durable'
  AND layer = 2
  AND importance <= 0.01;

SELECT id, title, observation_type, layer, retention, importance, access_count
FROM observations
WHERE deleted_at IS NULL
ORDER BY importance DESC, layer DESC, created_at DESC
LIMIT 25;
```

## Risks / Notes

- **Cambio transversal**: decay, recall, GC, promotion y migraciones comparten semántica; no conviene implementar esto por partes sin baseline fuerte.
- **Compatibilidad histórica**: si se añaden nuevas señales persistidas, el backfill debe ser deterministic y reversible a nivel de migración de datos.
- **Riesgo de sobrecorrección**: subir demasiado las memorias durables podría volver casi todo “importante”; por eso hace falta verificación con muestras reales tras el backfill.
- **Scope intencional**: esta iteración mejora la consolidación y la semántica del score; no incluye trabajo sobre proveedor/modelo LLM.
- **Política de Core**: Core debe seguir representando conocimiento estable del proyecto, no trazas operativas ni logs de ejecución.
