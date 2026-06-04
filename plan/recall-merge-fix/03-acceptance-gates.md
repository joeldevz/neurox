# 03 — Acceptance Gates

> 6 gates PASS/FAIL objetivos sobre LongMemEval-S. El task se cierra solo si los 5 gates primarios pasan. El stretch (G6) prueba que el fix es estructural, no un parche.

**Last updated:** 2026-06-03

---

## Current state (baseline honesto)

**LongMemEval-S** (Marzo 2026, 470 preguntas, con namespace backfill band-aid activo):

| Métrica | Current | Categoría |
|---|---|---|
| single-session-preference recall_any@5 | **80%** | 🔴 Weak — target primario del fix |
| multi-session recall_all@5 | **84.49%** | 🔴 Weak — target primario del fix |
| overall recall_any@5 | **95.96%** | 🟢 Strong — no regresión |
| knowledge-update | **100%** | 🟢 Strong — no regresión |
| ndcg@5 | **88.13%** | 🟢 Strong — no regresión |
| Cross-Session interno (con backfill) | **≥95/S** | 🟢 Strong — baseline del stretch gate |

> El baseline "33% / Grade F" está históricamente obsoleto — fue corregido vía backfill band-aid → 97.6/S. Los números reales de LongMemEval-S son los anteriores.

---

## The 6 gates

| # | Gate | Tipo | Baseline | Target | Pasa si |
|---|---|---|---|---|---|
| G1 | single-session-preference recall_any@5 | Primary (lift) | 80% | **≥88%** | ≥88% |
| G2 | multi-session recall_all@5 | Primary (lift) | 84.49% | **≥88%** | ≥88% |
| G3 | overall recall_any@5 | Primary (no-regression) | 95.96% | **≥96%** | ≥96% |
| G4 | knowledge-update | No-regression | 100% | **=100%** | =100% |
| G5 | ndcg@5 | No-regression | 88.13% | **≥88.13%** | ≥88.13% |
| G6 | Cross-Session interno sin backfill | Stretch (root-cause proof) | ≥95/S (con backfill) | **≥95/S** (sin backfill) | ≥95/S |

**Cierre del task:** los 5 gates primarios (G1-G5) deben pasar. G6 es **opcional** para cerrar el task, pero **obligatorio ejecutarlo** para pre-justificar (o descartar) el follow-up de remoción del backfill.

---

## Comandos exactos para cada gate

### G1, G2, G3, G4, G5 — LongMemEval-S full run

```bash
# Pre-requisito: data file presente en benchmarks/longmemeval/data/
# (archivo gitignored — obtener por separado)

# Correr full benchmark con embeddings
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 10 \
    -embed

# Resultado en: benchmarks/longmemeval/results.jsonl
# Métricas por tipo disponibles en el JSON de aggregate metrics
```

Para G1 (preference subset):
```bash
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type single-session-preference
```

Para G2 (multi-session):
```bash
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type multi-session
```

### G6 — Cross-Session interno sin backfill (stretch)

```bash
# Desactivar backfill via env var (construido en spec-03)
CGO_ENABLED=1 NEUROX_RECALL_DISABLE_BACKFILL=true \
    go run . benchmark --dimensions "Cross-Session Memory"
```

Pasa si el score reportado es **≥95/Elite** en la dimensión "Cross-Session Memory".  
Thresholds en `dim_cog_cross_session.go`: `Base:60 / Target:80 / Elite:95`.

### Build y tests de unidad

```bash
# Build (obligatorio antes de cualquier gate)
CGO_ENABLED=1 go build -tags sqlite_fts5 ./...

# Tests unitarios
CGO_ENABLED=1 go test -tags sqlite_fts5 ./internal/recall/...
```

> **Nota:** no existe `go test ./benchmarks/longmemeval/` ni `TestNDCG` — el benchmark es `go run`, no `go test`. No hay `*_test.go` en `benchmarks/`.

---

## Why these targets, not 75%

| Aspecto | 75% target (descartado) | 88% target (aceptado) |
|---|---|---|
| Baseline | 33% (obsoleto, pre-band-aid) | 95.96% (current, con band-aid) |
| Filosofía | "Saltar lo más posible" | "Liftar lo débil, no romper lo fuerte" |
| Riesgo | Pedir 75% sería pedir una **regresión** del estado actual | 88% es realista para fix aislado sin convex/expansion |
| Validación | "Mejoró" sin contexto | Diferenciado: weak lifted, strong preserved |

---

## What "passing" looks like

```bash
# 1. Implementar el fix (subtasks 1-3 de 04-task-breakdown.md)

# 2. Build
CGO_ENABLED=1 go build -tags sqlite_fts5 ./...

# 3. Unit tests
CGO_ENABLED=1 go test -tags sqlite_fts5 ./internal/recall/...

# 4. Correr LongMemEval-S completo (gates G1-G5)
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 10 -embed

# 5. Verificar por tipo para G1 y G2
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type single-session-preference
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type multi-session

# 6. Correr stretch gate G6
CGO_ENABLED=1 NEUROX_RECALL_DISABLE_BACKFILL=true \
    go run . benchmark --dimensions "Cross-Session Memory"

# 7. Reportar resultados en el parent issue con tabla de 6 gates
```

---

## Stretch gate G6 — interpretación

| Resultado | Interpretación | Siguiente acción |
|---|---|---|
| **≥95/S** sin backfill | union+RRF es fix estructural; el backfill era cosmético en la mayoría de casos | Crear Jira follow-up: "Remove namespace backfill from production" |
| **85-95/S** sin backfill | union+RRF ayuda, pero no cubre todos los casos | Mantener backfill; crear Jira: "Investigate convex combination / query expansion" |
| **<85/S** sin backfill | Fix no funciona como esperado | NO cerrar el task — revisar implementación RRF, valor de k, cap semántico 10k |

---

## Verification checklist (pre-completion)

Antes de marcar el task como done, verificar:

- [ ] G1-G5 pasan con evidencia (output del benchmark tabulado)
- [ ] No hay regresión en los strong metrics (G3, G4, G5)
- [ ] G6 fue ejecutado e interpretado (independiente del resultado)
- [ ] El reporte de resultados está en el parent issue
- [ ] Si G6 pasa (≥95/S): el follow-up Jira de remoción está creado con pre-text

---

## Reference

- [Spec-04 — Diagnostic Flag](./02-specs/spec-04-diagnostic-flag.md) — cómo funciona `DisableBackfill` para G6
- [Out of Scope](./05-out-of-scope.md) — por qué el labeled benchmark está fuera
