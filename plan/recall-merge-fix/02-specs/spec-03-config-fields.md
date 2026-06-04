# Spec-03 — Config Fields

> Dos campos nuevos en `RecallConfig`: `RRF.K` (default 60) y `DisableBackfill` (default false). `RecallConfig` **no existe hoy** y debe crearse. Diseñados para crecer sin breaking change.

**Status:** Proposed  
**Last updated:** 2026-06-03

---

## Current state

`RecallConfig` does **not exist** in the codebase. `internal/config/config.go` (line 14) defines `Config` as:

```go
type Config struct {
    Database      DatabaseConfig      `yaml:"database"`
    LLM           LLMConfig           `yaml:"llm"`
    Embeddings    EmbeddingsConfig    `yaml:"embeddings"`
    Curator       CuratorConfig       `yaml:"curator"`
    Consolidation ConsolidationConfig `yaml:"consolidation"`
    Meta          MetaConfig          `yaml:"-"`
}
```

There is **no** `Recall` field. There are **no** `NEUROX_RECALL_*` env vars. The env parsing uses manual `os.Getenv` calls in `applyEnvOverrides` (config.go:144-238) — there is **no** struct-tag `env:` or `default:` mechanism. Both are inert if placed on struct tags.

---

## Required changes

### 1. Define the new structs (in `internal/config/config.go`)

```go
// RRFConfig holds the Reciprocal Rank Fusion parameters.
// Designed to grow into per-channel k (KFts, KSem) without API break.
type RRFConfig struct {
    K int `yaml:"k"`
}

type RecallConfig struct {
    RRF             RRFConfig `yaml:"rrf"`
    DisableBackfill bool      `yaml:"disable_backfill"`
}
```

> Do NOT add `env:` or `default:` struct tags — they are not processed by the existing config machinery.

### 2. Add field to `Config`

```go
type Config struct {
    Database      DatabaseConfig      `yaml:"database"`
    LLM           LLMConfig           `yaml:"llm"`
    Embeddings    EmbeddingsConfig    `yaml:"embeddings"`
    Curator       CuratorConfig       `yaml:"curator"`
    Consolidation ConsolidationConfig `yaml:"consolidation"`
    Recall        RecallConfig        `yaml:"recall"`  // NEW
    Meta          MetaConfig          `yaml:"-"`
}
```

### 3. Set defaults in `defaultConfig` (config.go:131-142)

```go
func defaultConfig(configDir string, configPath string) Config {
    return Config{
        // ... existing fields ...
        Recall: RecallConfig{
            RRF:             RRFConfig{K: 60},
            DisableBackfill: false,
        },
    }
}
```

### 4. Add env overrides in `applyEnvOverrides` (config.go:144-238)

Add at the end of `applyEnvOverrides`, following the existing manual pattern:

```go
if value := strings.TrimSpace(os.Getenv(envPrefix + "RECALL_RRF_K")); value != "" {
    if k, err := strconv.Atoi(value); err == nil {
        cfg.Recall.RRF.K = k
        cfg.Meta.Source = "env"
    }
}

if value := strings.TrimSpace(os.Getenv(envPrefix + "RECALL_DISABLE_BACKFILL")); value != "" {
    if v, err := strconv.ParseBool(value); err == nil {
        cfg.Recall.DisableBackfill = v
        cfg.Meta.Source = "env"
    }
}
```

Note: `strconv` must be imported (check if already present; add if not).

### 5. Validate K > 0 — placement and signature

`config.Load` (config.go:82) already has signature `func Load(configPath string) (Config, error)` and already returns `return Config{}, fmt.Errorf(...)` at lines 96 and 101 (parse/read failures). **The signature must NOT change.**

Add the K validation **after** `applyEnvOverrides` and `applyDerivedDefaults` have run (so defaults and env overrides are resolved before the check):

```go
if cfg.Recall.RRF.K <= 0 {
    return Config{}, fmt.Errorf("recall.rrf.k must be > 0, got %d", cfg.Recall.RRF.K)
}
```

**Notes:**
- This is the **first validation-style error** in `Load` (existing errors are parse/read failures, not value validation), but the `return Config{}, fmt.Errorf(...)` return pattern is identical — no new machinery needed.
- Do NOT place this check in `defaultConfig` or `applyDerivedDefaults` directly if those functions don't return errors. Place it as an explicit `if` block in `Load()` after the defaults/overrides pipeline completes.
- Do NOT change the function signature. Do NOT add a `validateRecallConfig` function that returns an error separately — inline the check in `Load` to match the existing error pattern.

---

## Env variable names

| Env var | Maps to | Type | Parse |
|---|---|---|---|
| `NEUROX_RECALL_RRF_K` | `Config.Recall.RRF.K` | int | `strconv.Atoi` |
| `NEUROX_RECALL_DISABLE_BACKFILL` | `Config.Recall.DisableBackfill` | bool | `strconv.ParseBool` |

Both env vars **do not exist yet** — they must be wired in `applyEnvOverrides` as part of this task.

---

## Defaults

| Field | Default | Justification |
|---|---|---|
| `Recall.RRF.K` | 60 | Industry zero-shot consensus (Cormack et al. 2009, Bruch et al. 2022, production defaults) |
| `Recall.DisableBackfill` | false | Safe default — production always uses backfill |

---

## YAML usage

```yaml
# config.yaml
recall:
  rrf:
    k: 30
  disable_backfill: true
```

---

## Gherkin specs

### Scenario 1: Defaults when no config provided

```gherkin
Given  no YAML file and no env vars set
When   Config is loaded via config.Load()
Then   Recall.RRF.K = 60
And    Recall.DisableBackfill = false
```

### Scenario 2: Override via YAML

```gherkin
Given  YAML with recall.rrf.k=30 and recall.disable_backfill=true
When   Config is loaded
Then   Recall.RRF.K = 30
And    Recall.DisableBackfill = true
```

### Scenario 3: Override via env var (takes priority over YAML)

```gherkin
Given  YAML with k=30
And    NEUROX_RECALL_RRF_K=90 set in environment
And    NEUROX_RECALL_DISABLE_BACKFILL=true set in environment
When   Config is loaded
Then   Recall.RRF.K = 90
And    Recall.DisableBackfill = true
And    env has priority (applyEnvOverrides runs after yaml.Unmarshal)
```

### Scenario 4: Invalid K value

```gherkin
Given  Recall.RRF.K = -5 (from YAML or env)
When   Config is loaded
Then   config.Load() returns an error: "recall.rrf.k must be > 0, got -5"
And    the engine does not start
```

---

## Future-proofing

When per-channel k tuning becomes a priority, the struct grows:

```go
type RRFConfig struct {
    K    int `yaml:"k"`     // used when KFts/KSem are zero
    KFts int `yaml:"k_fts"` // future: per-channel FTS k
    KSem int `yaml:"k_sem"` // future: per-channel semantic k
}
```

Migration:
- Today: only `K` is read; `KFts=0`, `KSem=0`
- Future: if `KFts != 0`, use it for FTS ranks; else fallback to `K`
- Zero breaking change in YAML keys or existing API

---

## Out of contract

- No automatic k tuning
- No persistence of effective k per corpus/namespace (requires labeled benchmark task)
- Only validates k > 0 (not that it is a "reasonable" value)

---

## Reference

- [Spec-02 — RRF Scoring](./spec-02-rrf-scoring.md) — usage of `k`
- [Spec-04 — Diagnostic Flag](./spec-04-diagnostic-flag.md) — usage of `DisableBackfill`
