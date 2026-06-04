# Spec-04 — Diagnostic Flag (DisableBackfill)

> El flag `DisableBackfill` permite medir el rendimiento del recall **sin** el namespace backfill band-aid, para validar que el fix union+RRF es estructural.

**Status:** Proposed  
**Last updated:** 2026-06-03

---

## Por qué existe este flag

El benchmark de Neurox está en 97.6/S (Grade S) **con** el namespace backfill band-aid activo. Ese band-aid rellena resultados bajo condiciones estrechas: namespace seteado + count<limit + sin file filters + query ≥3 words (engine.go:318-326).

**El problema diagnóstico:** No sabemos si el 97.6/S es:
- **(a)** mérito del recall engine (el fix union+RRF es suficientemente estructural, el band-aid es cosmético)
- **(b)** mérito del band-aid enmascarando un recall aún incompleto

**El fix (unión + RRF) ataca (b) directamente.** Para probarlo sin ambigüedad, necesitamos **medir con el band-aid apagado**:
- Si sin band-aid el score se mantiene ≥95/S → el fix es estructural, el band-aid se puede remover (Jira follow-up pre-justificado)
- Si sin band-aid el score cae → el fix no es suficiente solo, falta más work (convex combination / expansion)

---

## Contract

```go
RecallConfig.DisableBackfill bool  // default: false
```

- **Default:** `false` — producción siempre con backfill activo
- **Uso previsto:** solo en benchmark diagnostic runs
- **Nunca** se setea en producción por defecto

### Comportamiento en el engine

Cuando `DisableBackfill = true`, el gate `shouldNamespaceBackfill` (engine.go:318-326) debe retornar `false` incondicionalmente, lo que evita que `loadNamespaceBackfill` sea llamada.

**Decided DI mechanism — functional option (single approach, no alternatives):**

The engine uses functional options (engine.go:88-113). The `Engine` struct currently holds `db`, `embedder`, and `factStore`. To wire `RecallConfig` into the engine:

1. Add field `recallCfg config.RecallConfig` to the `Engine` struct (engine.go:22-26)
2. Add a new functional option `func WithRecallConfig(cfg config.RecallConfig) EngineOption` (mirrors `WithFactStore` at engine.go:108-112)
3. The engine reads `e.recallCfg.DisableBackfill` and `e.recallCfg.RRF.K` directly from the struct field

The engine then passes `e.recallCfg.DisableBackfill` as a parameter when calling `shouldNamespaceBackfill`. Do NOT set the field externally via a raw exported struct field. Do NOT create a separate config setter alongside the option — `WithRecallConfig` is the only injection point.

El recall engine en sí no cambia su lógica — sigue funcionando igual con o sin backfill. La única diferencia visible es que `shouldNamespaceBackfill` retorna false cuando el flag está activo.

### Comportamiento en el benchmark runner

El **único consumidor** de este flag en producción es el benchmark runner. El benchmark runner crea su propio `Engine` con el flag activo:

```
NEUROX_RECALL_DISABLE_BACKFILL=true go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 10 -embed
```

El env var `NEUROX_RECALL_DISABLE_BACKFILL` **no existe hoy** — debe ser creado como parte de spec-03.

---

## Gherkin specs

### Scenario 1: Default producción (flag false)

```gherkin
Given  RecallConfig.DisableBackfill = false
And    query meets all shouldNamespaceBackfill conditions (namespace set, count<limit, no files, ≥3 words)
When   Search() is called
Then   loadNamespaceBackfill is called normally
And    behavior is identical to current production
```

### Scenario 2: Diagnostic run (flag true)

```gherkin
Given  RecallConfig.DisableBackfill = true
And    query would otherwise trigger shouldNamespaceBackfill
When   Search() is called
Then   shouldNamespaceBackfill returns false
And    loadNamespaceBackfill is NOT called
And    results are derived from union merge + RRF only (no backfill padding)
```

### Scenario 3: DisableBackfill suppresses backfill that would otherwise fire (explicit namespace)

```gherkin
Given  RecallConfig.DisableBackfill = true
And    SearchOptions.Namespace = "neurox" (explicit, non-empty namespace)
And    currentCount < limit
And    no file filters
And    query has ≥ 3 words
When   Search() is called
Then   shouldNamespaceBackfill returns false (DisableBackfill active)
And    loadNamespaceBackfill is NOT called
Note   Without DisableBackfill, this gate returns TRUE here (namespace set,
       count<limit, no file filters, query≥3 words → eligible). DisableBackfill=true
       forces it to false. This confirms the flag is SUBTRACTIVE — it suppresses
       backfill that would otherwise fire (the diagnostic purpose), not additive.
```

### Scenario 3b: Gate already-false case (namespace empty — independent of flag)

```gherkin
Given  RecallConfig.DisableBackfill = true
And    SearchOptions.Namespace = "" (empty — no explicit namespace)
When   Search() is called
Then   shouldNamespaceBackfill returns false (gate already false: Namespace=="" condition)
Note   This is a SEPARATE already-false case: the gate returns false regardless of
       DisableBackfill because Namespace is empty. The flag has no observable effect
       here — the gate was already false before DisableBackfill is evaluated.
       Do NOT confuse this with Scenario 3: DisableBackfill is SUBTRACTIVE (suppresses
       eligible backfill), not additive (it cannot cause an already-false gate to fire).
```

### Scenario 4: Flag propagated from NEUROX_RECALL_DISABLE_BACKFILL env var

```gherkin
Given  NEUROX_RECALL_DISABLE_BACKFILL=true set in environment
When   config.Load() is called
Then   Config.Recall.DisableBackfill = true
And    engine respects the flag
```

---

## Two-piece stretch gate (medir ≠ shippear)

| Pieza | ¿Entra al task? | Costo | Por qué |
|---|---|---|---|
| **Medir** con backfill off (vía flag) | ✅ SÍ | ~20 min | Único signal que pre-justifica el follow-up de remoción |
| **Shippear** la remoción del backfill | ❌ NO | medio día | Requiere entender impl completa, qué rompe en edge cases, re-validar stack |

**Resultados posibles de la medición:**

| Resultado | Qué se aprende | Acción |
|---|---|---|
| ≥95/S sin backfill | union+RRF es fix estructural | Follow-up Jira de remoción queda pre-justificado |
| 85-95/S sin backfill | union+RRF ayuda, pero no es suficiente solo | Mantener backfill; crear task de convex/expansion |
| <85/S sin backfill | Fix no funciona como esperado | NO cerrar el task — investigar RRF implementation, k value, 10k cap semántico |

En ningún caso queda ambigüedad. La ambigüedad solo aparece si se shippea la remoción sin medir primero.

---

## Implementation note

El flag vive en `RecallConfig` (spec-03), pero el flujo de implementación es:

1. Spec-03 crea `RecallConfig.DisableBackfill` y el env var `NEUROX_RECALL_DISABLE_BACKFILL`
2. Este spec añade una condición en `shouldNamespaceBackfill` (engine.go:318-326):

```go
func shouldNamespaceBackfill(options SearchOptions, currentCount int, disableBackfill bool) bool {
    if disableBackfill {
        return false
    }
    if options.Namespace == "" || currentCount >= options.Limit {
        return false
    }
    if len(options.Files) > 0 {
        return false
    }
    return len(strings.Fields(options.Query)) >= 3
}
```

3. The engine reads `e.recallCfg.DisableBackfill` (injected via `WithRecallConfig`) and passes it when calling `shouldNamespaceBackfill`

---

## Out of contract

- El flag **no** es un toggle de producción
- El flag **no** afecta el comportamiento del recall engine para FTS ni para union merge, solo suprime el backfill
- El flag **no** se persiste — cada corrida de benchmark lo setea explícitamente via env var
- La remoción permanente del backfill está **fuera de scope** (ver 05-out-of-scope.md)

---

## Reference

- [Spec-03 — Config Fields](./spec-03-config-fields.md) — tipo y default del flag, env var wiring
- [Acceptance Gates](../03-acceptance-gates.md) — el stretch gate G6 que usa este flag
