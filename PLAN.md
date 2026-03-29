# Plan: Surface parity and claim alignment for CLI, MCP, and HTTP

## Goal

Cerrar la primera brecha del roadmap de producto unificando el comportamiento real entre CLI, MCP y HTTP para las rutas críticas de memoria, y alinear la documentación pública con lo que el producto realmente entrega hoy. El resultado debe ser que Neurox se comporte como un solo producto con tres superficies, no como tres experiencias con distinta profundidad.

## Business Context

- **Problema**: El roadmap en `internal-docs/neurox-roadmap-2026-2028.md` prioriza `Surface Parity & Claim Alignment`, pero hoy todavía existen diferencias visibles entre superficies: CLI guarda por un flujo distinto, HTTP expone menos provenance que MCP, y algunas docs presentan una paridad o capacidades más uniformes de lo que el producto realmente entrega.
- **Usuarios afectados**: agentes que usan MCP, integraciones HTTP, y usuarios directos del CLI. Las diferencias actuales degradan confianza, complican debugging y hacen más difícil demostrar que Neurox mejora a un coding agent de forma medible.
- **Resultado esperado**: cualquier superficie debe producir observaciones con el mismo pipeline de calidad, exponer campos clave de provenance y devolver respuestas equivalentes para los flujos centrales (`save`, `recall`, `context`, `session`). Las docs deben reflejar exactamente ese contrato.
- **Criterios de éxito de producto**:
  - `save` deja de tener niveles distintos de calidad según la superficie.
  - La provenance visible en `recall`/`context` es consistente con lo prometido.
  - La historia de sesión y extracción de observaciones ya no queda reservada solo a servidores.
  - La documentación de CLI/MCP/HTTP deja de sobreprometer o esconder gaps.

## Technical Context

- **Estado actual del código**:
  - `main.go:222-293` implementa `runSave` por una ruta propia (`observation.Store.Save`) con facts/embeddings best-effort, sin `SaveQueue`, sin `LLMGate` compartido y sin adjuntar `source_session_id`.
  - `internal/mcp/handlers.go:51-165` y `internal/api/handlers.go:77-171` usan `SaveQueue` cuando está disponible, con retención auto-clasificada y hooks de facts + embeddings.
  - `internal/mcp/handlers.go:79-84` adjunta la sesión activa al `save`; `internal/api/handlers.go:94-133` no hace lo mismo.
  - `internal/mcp/handlers.go:217-255` devuelve provenance en `recall`; `internal/api/handlers.go:208-231` aún no incluye esos campos en los resultados.
  - `internal/session/manager.go:77-165` concentra la lógica real de `session_end`, incluida la extracción de observaciones, pero el CLI no expone esa capacidad.
  - `tests/integration/parity_test.go` ya cubre parte de la paridad, pero necesita evolucionar desde “equivalencia parcial” a “contrato compartido de producto”.
- **Patrones y constraints**:
  - La arquitectura ya favorece servicios compartidos (`SaveQueue`, `SessionManager`, `ProactiveEngine`, `RecallEngine`); el plan debe profundizar ese patrón, no introducir una cuarta vía.
  - `schema.sql` ya soporta provenance (`source_surface`, `source_session_id`, `source_tool`), así que la mayor parte del trabajo es wiring y consistencia de respuestas, no migración de schema.
  - Hay soporte existente para `audit` en CLI (`main.go:457-619`), pero el roadmap de debugging/audit puede quedar como fase posterior si no es necesario para cerrar la primera ola de parity.
- **Archivos/modulos probablemente afectados**:
  - `main.go`
  - `internal/api/handlers.go`
  - `internal/api/server.go`
  - `internal/mcp/handlers.go`
  - `internal/mcp/server.go`
  - `internal/session/manager.go`
  - `internal/observation/savequeue.go`
  - `tests/integration/parity_test.go`
  - `README.md`, `README.es.md`, `docs/reference.md`, `docs/concepts.md`

## Implementation Steps

### Step 1: Convertir la auditoría en contrato de paridad verificable
- **What**: Expandir las pruebas de integración para codificar explícitamente el contrato que debe ser igual entre superficies en `save`, `recall`, `context`, `session`, provenance visible y shape de respuestas críticas. Mantener como foco primero los comportamientos compartidos, no el dashboard ni endpoints exploratorios.
- **Why**: Antes de tocar wiring, necesitamos una red de seguridad que falle cuando una superficie vuelva a divergir.
- **Where**: `tests/integration/parity_test.go`, y tests unitarios cercanos en `internal/api/` y `internal/mcp/` si hace falta cubrir casos concretos.
- **Acceptance**:
  - Existen tests que fallan hoy para los gaps prioritarios identificados en la auditoría.
  - Los tests verifican provenance visible, session attachment y shape de respuestas, no solo persistencia básica.
  - Queda claro qué diferencias siguen siendo intencionales y cuáles no.
- **Status**: [x] done

### Step 2: Unificar el pipeline de `save` para las tres superficies
- **What**: Refactorizar el guardado para que CLI, MCP y HTTP usen el mismo pipeline canónico: defaults, clasificación de retención, `LLMGate` cuando aplique, `SaveQueue`/persistencia compartida, hooks post-save y adjunte de sesión activa cuando exista. Si es necesario, extraer helpers compartidos para construir observaciones de entrada y resolver sesión activa por namespace.
- **Why**: `save` es el punto más importante del producto; mientras tenga rutas distintas, Neurox sigue siendo tres productos diferentes.
- **Where**: `main.go`, `internal/mcp/handlers.go`, `internal/api/handlers.go`, y cualquier helper nuevo bajo `internal/observation/` o `internal/session/`.
- **Acceptance**:
  - CLI deja de usar un flujo ad hoc separado de MCP/HTTP para `save`.
  - HTTP adjunta `source_session_id` con la misma semántica que MCP cuando hay sesión activa.
  - El mensaje/shape de respuesta de `save` queda intencionalmente alineado o documentado si debe diferir.
  - Facts y embeddings se disparan por la misma ruta de hooks, no por lógica duplicada por superficie.
- **Status**: [x] done

### Step 3: Llevar el lifecycle de sesión a paridad real
- **What**: Exponer en CLI la misma disciplina de sesiones que hoy existe en MCP/HTTP (`session_start`, `session_end` o comandos equivalentes) y asegurar que las tres superficies usen `SessionManager` para comenzar/cerrar sesión, obtener contexto inicial y extraer observaciones del resumen.
- **Why**: El roadmap pide continuidad y session extraction unificadas; hoy el CLI queda fuera del flujo más rico del producto.
- **Where**: `main.go`, `internal/session/manager.go`, `internal/api/handlers.go`, `internal/mcp/handlers.go`, `docs/reference.md`.
- **Acceptance**:
  - Existe superficie CLI para iniciar y cerrar sesiones usando `SessionManager`.
  - `session_end` produce el mismo comportamiento base en las tres superficies, incluida extracción cuando hay LLM.
  - Las respuestas incluyen los campos mínimos equivalentes (`session_id`, `observations_extracted`, warning si aplica).
- **Status**: [x] done

### Step 4: Alinear provenance y respuestas de `recall` / `context`
- **What**: Hacer que HTTP y CLI expongan la misma información de provenance y temporal intent que MCP ya devuelve, y revisar los fallbacks para que no degraden silenciosamente el contrato cuando falten motores avanzados. Si un fallback no puede igualar el contrato, documentar y señalizarlo explícitamente.
- **Why**: La proposition de Neurox depende de memory traceable y auditable; si provenance aparece solo en una superficie, la claim principal se rompe.
- **Where**: `internal/api/handlers.go`, `main.go`, `internal/proactive/context.go`, tipos/serializadores de `internal/mcp/`, y posiblemente helpers de formateo JSON.
- **Acceptance**:
  - `recall` HTTP devuelve `source_surface`, `source_session_id`, `source_tool` y `temporal_intent` de forma consistente.
  - `context` mantiene provenance cuando usa el camino principal y tiene un fallback explícito/razonado cuando no puede hacerlo.
  - CLI `recall` y `context` devuelven payloads alineados con las superficies servidoras para los campos centrales.
- **Status**: [x] done

### Step 5: Cerrar gaps del pipeline secundario y de actualización
- **What**: Revisar rutas que hoy se salen del pipeline principal — especialmente `update` y las observaciones derivadas de `session_end` — para decidir e implementar la mínima paridad necesaria: re-embedding, facts, temporal extraction y provenance coherente cuando cambie contenido o se creen observaciones derivadas.
- **Why**: No basta con arreglar `save` si luego las rutas secundarias vuelven a generar memoria de menor calidad o con metadata incompleta.
- **Where**: `internal/api/handlers.go`, `internal/mcp/handlers.go`, `internal/session/manager.go`, `internal/observation/store.go` o helpers compartidos.
- **Acceptance**:
  - Hay una decisión explícita y codificada sobre qué post-procesos deben correr en `update` y en observaciones extraídas de sesión.
  - La provenance de observaciones derivadas sigue siendo consistente (`session_end`, superficie, sesión fuente).
  - Los tests cubren al menos un caso donde el contenido cambia y el pipeline secundario mantiene calidad equivalente.
- **Status**: [x] done

### Step 6: Alinear docs y referencias con el contrato realmente shipped
- **What**: Actualizar README, README.es, `docs/reference.md` y `docs/concepts.md` para reflejar exactamente la paridad actual entre superficies, evitar overclaiming sobre facts/reflection/provenance, y documentar cualquier diferencia todavía intencional o fuera de scope.
- **Why**: La mitad del milestone es claim alignment; no sirve cerrar gaps si la documentación sigue contando otra historia.
- **Where**: `README.md`, `README.es.md`, `docs/reference.md`, `docs/concepts.md`, y opcionalmente `internal-docs/` si hace falta enlazar el avance al roadmap.
- **Acceptance**:
  - Las tablas de CLI/MCP/HTTP están actualizadas con la superficie real.
  - Las claims sobre provenance “across all surfaces” solo permanecen si ya son verdad.
  - Facts/reflection se describen con el nivel correcto de disponibilidad y condicionantes.
- **Status**: [x] done

### Step 7: Verificación integral de parity y documentación
- **What**: Ejecutar build, tests unitarios y tests de integración relevantes, con énfasis en la suite de paridad y en comandos/endpoints modificados. Añadir validación manual puntual del shape JSON para al menos una operación por superficie si los tests no cubren la presentación final.
- **Why**: El objetivo no es solo compilar; es demostrar que el contrato compartido realmente quedó estable entre superficies.
- **Where**: Todo el proyecto, con foco en `tests/integration/`, `internal/api/`, `internal/mcp/`, `internal/session/` y `main.go`.
- **Acceptance**:
  - `go build ./...` pasa.
  - `go test ./...` pasa incluyendo `tests/integration/parity_test.go`.
  - Los comandos y endpoints tocados devuelven payloads consistentes con el plan y la documentación actualizada.
- **Status**: [x] done

## Verification

```bash
go build ./...
go test ./internal/api/... ./internal/mcp/... ./internal/session/... ./internal/observation/...
go test ./tests/integration/... -run Parity
go test ./...
```

## Risks / Notes

- **No intentar resolver todo el roadmap a la vez**: este plan cubre la primera ola de parity + claim alignment; el roadmap de `debug` / `audit` más profundo puede seguir como iniciativa separada si no es necesario para cerrar este milestone.
- **CLI puede requerir decisiones de UX**: añadir `session_start`/`session_end` al CLI cambia la superficie pública; conviene mantener naming simple y consistente con MCP/HTTP para evitar otra divergencia documental.
- **Fallbacks explícitos**: si alguna ruta sin LLM/embeddings no puede devolver exactamente la misma riqueza, la diferencia debe ser visible y documentada, no implícita.
- **Evitar duplicar hooks**: facts, embeddings y futuras señales de provenance deben colgar de un pipeline común; si se copian entre superficies, el problema reaparecerá.
- **Sobre facts y reflection**: el objetivo aquí es claim alignment y consistencia de exposición básica, no convertir facts en una superficie completa de producto; eso queda para fases posteriores del roadmap.
