<p align="center">
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>Memoria persistente para agentes de IA</strong>
  </p>
  <p align="center">
    Un binario &bull; Un archivo SQLite &bull; Cero dependencias externas
  </p>
  <p align="center">
    <a href="#inicio-rapido">Inicio Rapido</a> &bull;
    <a href="#que-recuerda">Que Recuerda</a> &bull;
    <a href="#resultados-del-benchmark">98% Recall</a> &bull;
    <a href="README.md">Read in English</a>
  </p>
</p>

---

Tu agente de IA olvida todo entre sesiones. Cada conversacion empieza desde cero — sin memoria de las decisiones de arquitectura de la semana pasada, el bug que arreglaste ayer, o tu preferencia por tabs sobre espacios.

Neurox le da a tu agente memoria persistente y estructurada.

```bash
# Instalar (macOS / Linux)
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Configurar tu agente
neurox setup claude-code    # o: opencode, cursor, vscode, antigravity, claude-desktop
```

Eso es todo. Sin Node.js, sin Python, sin Docker. **Un binario, un archivo SQLite.**

---

## Que Recuerda

Tu agente guarda observaciones mientras trabaja — decisiones, bugs, patrones, preferencias — y las recupera cuando son relevantes. Cada observacion es un registro estructurado en una base de datos SQLite local, completamente inspeccionable y auditable.

```
Agente: "Decidimos usar SQLite en vez de PostgreSQL para deploy single-file"
  → Neurox lo guarda como tipo: decision, lo vincula a schema.sql
  → Parsea "en vez de PostgreSQL" como un knowledge update
  → Tres meses despues, el agente pregunta "que base de datos usamos?"
  → Neurox devuelve la decision de SQLite primero, PostgreSQL como historial
```

**Nada esta oculto.** Cada observacion es una fila en SQLite. Puedes consultarla directamente, exportarla, borrarla, o inspeccionar como fue puntuada. No hay consolidacion opaca que elimine tus datos silenciosamente — todo es trazable a traves de metadatos de provenance y logs de auditoria.

### Que lo diferencia de un key-value store

| Feature | Store simple | Neurox |
|---|---|---|
| Guardar y recuperar texto | Si | Si |
| Busqueda full-text (FTS5) | Quizas | Integrado |
| Entiende tiempo ("la semana pasada", "actualmente") | No | [Razonamiento temporal](#razonamiento-temporal) |
| Sabe cuando cambian los hechos | No | [Knowledge updates](#razonamiento-temporal) — los hechos viejos se vuelven historial, no ruido |
| Vincula memorias a archivos fuente | No | [Integracion Git](#integracion-git) — marca stale automaticamente cuando cambian archivos |
| Explica por que un resultado quedo primero | No | [Modo debug](#modo-debug) — desglose completo de score por resultado |
| Rastrea de donde vienen las memorias | No | [Provenance](#provenance) — que tool, sesion y superficie creo cada memoria |
| Funciona sin ningun servicio externo | — | Si — [LLM y embeddings son mejoras opcionales](#degradacion-graceful) |

## Inicio Rapido

### Instalar

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Windows
irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
```

### Configurar tu agente

```bash
neurox setup claude-code       # Claude Code
neurox setup opencode          # OpenCode
neurox setup cursor            # Cursor
neurox setup vscode            # VS Code (Copilot)
neurox setup antigravity       # Gemini CLI / Antigravity
neurox setup claude-desktop    # Claude Desktop
```

### Instalador interactivo (avanzado)

```bash
neurox install
```

Lanza un TUI con Bubble Tea donde puedes elegir directorio de instalacion, directorio de config, configuracion de providers, integraciones de editor, e instalacion del git hook. Muestra exactamente que se va a escribir antes de hacerlo.

### Compilar desde source

```bash
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

### Usar como servidor MCP

```bash
neurox mcp     # stdio — para Claude Code, Cursor, OpenCode, etc.
```

### Usar como API HTTP

```bash
neurox serve   # localhost:7438
```

---

## Como Funciona

```
1. El agente guarda una memoria    →  neurox save "titulo" --content "..." --type decision
2. Neurox la indexa                →  FTS5 full-text + embeddings opcionales
3. Se extrae info temporal         →  "la semana pasada" → 2026-03-13
4. Se vinculan archivos            →  --files "schema.sql" → tracked para staleness en git
5. El agente busca despues         →  neurox recall "que base de datos" → resultados rankeados
6. Mantenimiento en background     →  cada 30 min: decay, promover, dedup, reflexion
```

### Capas de Memoria

Las observaciones se mueven entre tres capas basandose en importancia y patrones de acceso:

```
 Buffer (nuevas)       Working (validadas)      Core (probadas)
 ┌────────────────┐    ┌────────────────┐    ┌────────────────┐
 │ Todos los saves │───>│ Pasaron quality │───>│ Accedidas 5+   │
 │ Capacidad: 200  │    │ gate o alta     │    │ veces, 7+ dias │
 │ Decay rapido    │    │ importancia     │    │ de edad, durable│
 └────────────────┘    └────────────────┘    └────────────────┘
```

**Importante:** el decay reduce la *accesibilidad* (que tan facil es encontrar algo), no el *valor* (si existe o no). Una decision tomada hace seis meses permanece en Core permanentemente — solo se vuelve menos prominente en resultados de busqueda a menos que la accedas. Nada se elimina sin accion explicita o reglas de eviccion configurables.

### Scoring

Cada resultado de busqueda se rankea con un score compuesto:

```
Score = (Recencia x 0.3) + (Importancia x 0.3) + (Relevancia x 0.4)
        x Multiplicador temporal (0.7x – 1.5x segun intent temporal)
        x Boost cross-signal (1.2x cuando FTS y semantico coinciden)
```

FTS5 keyword matching es el motor de busqueda principal. Los embeddings semanticos son un reranker opcional que mejora resultados cuando esta configurado — pero Neurox funciona a maxima velocidad sin ellos.

---

## Razonamiento Temporal

Neurox entiende *cuando* pasaron las cosas. Esta es la diferencia entre que "Que base de datos usamos?" devuelva la respuesta correcta, o devuelva un hecho deprecado de hace seis meses.

**Al guardar** — se extraen y normalizan expresiones temporales:
```
"Migramos a SQLite la semana pasada"     → relative, 2026-03-13, confidence: 0.85
"Actualmente usamos PostgreSQL 16"       → current_state, confidence: 0.95
"Desplegado el 5 de marzo de 2026"       → absolute, 2026-03-05, confidence: 0.95
```

Soporta ingles y espanol. Maneja fechas absolutas, expresiones relativas (ayer, hace 3 semanas, two months ago), marcadores de estado actual, duraciones y rangos de fechas.

**Al buscar** — se detecta el intent temporal y se ajusta el scoring:

| Patron de query | Efecto |
|---|---|
| "actualmente", "ahora", "latest" | Boost a fresh, penaliza stale |
| "antes", "previamente", "used to" | Incluye historial, boost a antiguos |
| "cuando", "what date" | Boost a resultados con fechas |
| "cuanto tiempo", "how long" | Boost a menciones de duracion |
| "marzo 2026", "last week" | Boost por proximidad temporal |

**En contradiccion** — las secuencias temporales se preservan como historial:
```
Viejo: "Usamos PostgreSQL"      →  marcado stale (aun encontrable como historial)
Nuevo: "Migramos a SQLite"      →  marcado fresh (rankea primero)
Link: nuevo supersede al viejo
```

La observacion vieja es *stale*, no *deleted*. "Que usabamos antes?" todavia la encuentra.

---

## Integracion Git

Instalar el hook post-commit:

```bash
neurox install-hook
```

Cuando haces commit, Neurox recibe la lista de archivos cambiados y marca las observaciones vinculadas como stale. Si guardaste "Auth usa JWT con RS256" vinculado a `auth/middleware.go`, y luego refactorizas ese archivo, la observacion se marca para revision — no se confia silenciosamente en ella.

Los eventos del git hook se envian al servidor HTTP (`neurox serve` debe estar corriendo).

---

## Degradacion Graceful

Neurox funciona sin ningun servicio externo. Las features se activan segun lo que este disponible:

| Disponible | Features habilitadas |
|---|---|
| **Nada** (default) | Busqueda FTS5, parsing temporal, promocion heuristica, decay |
| + Embeddings (Ollama o remoto) | Busqueda hibrida, dedup semantico, deteccion de contradicciones |
| + LLM (Ollama o remoto) | Quality gate, extraccion de facts, reflexion, extraccion de sesion |
| + Curator LLM (remoto) | Curacion profunda con recalibracion de importancia |

La configuracion base — cero dependencias — ya entrega 98% de recall en LongMemEval. Todo lo demas mejora precision y mantenimiento.

---

## Modo Debug

Pasa `debug: true` a cualquier busqueda para ver exactamente por que cada resultado quedo donde quedo:

```json
{
  "score_breakdown": {
    "recency": 0.85,
    "importance": 0.70,
    "relevance": 0.92,
    "semantic_score": 0.88,
    "temporal_multiplier": 1.2,
    "cross_signal_boost": 1.0,
    "final_score": 0.83
  }
}
```

Disponible via MCP (`debug: true`), CLI (`--debug`), y API HTTP (`?debug=true`).

---

## Provenance

Cada observacion registra de donde vino:

| Campo | Descripcion | Valores |
|---|---|---|
| `source_surface` | Punto de entrada | `mcp`, `http`, `cli`, `consolidator` |
| `source_session_id` | Sesion al momento de creacion | ULID o vacio |
| `source_tool` | Operacion | `save`, `invalidate`, `reflect`, `curate` |

Usa `neurox audit <id>` para ver el ciclo de vida completo de cualquier observacion: creacion, promociones, links, transiciones de staleness, menciones temporales y estado actual.

---

## Resultados del Benchmark

Evaluado en [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025) — 500 preguntas en 6 categorias, 48 sesiones de distraccion por query.

| Categoria | N | Recall@10 | NDCG@10 |
|---|---|---|---|
| knowledge-update | 72 | **100.0%** | 96.9% |
| single-session-user | 64 | 98.4% | 97.0% |
| single-session-assistant | 56 | 98.2% | 95.1% |
| temporal-reasoning | 127 | 97.6% | 87.2% |
| multi-session | 121 | 98.4% | 87.0% |
| single-session-preference | 30 | 93.3% | 73.8% |
| **Overall** | **470** | **98.1%** | **90.0%** |

FTS5 + BM25 + scoring temporal. Sin LLM. 500 preguntas en ~2 minutos.

### Brain Benchmark

Un suite autocontenido que prueba el motor de memoria completo en 12 dimensiones:

| Categoria | Peso | Dimensiones |
|---|---|---|
| **Cognitivo** | 45% | Knowledge Update, Signal vs Noise, Cross-Session, Temporal Reasoning, Memory Lifecycle |
| **Rendimiento** | 20% | Write Throughput, Recall Latency, Concurrent Access, Context Retrieval |
| **Simulacion de Agente** | 35% | Lazy vs Perfect Agent, Realistic Workflows, Parameter Impact |

```bash
neurox benchmark                                            # Ejecucion rapida (1k observaciones)
neurox benchmark --scale large --output-html report.html    # Ejecucion completa con reporte HTML
```

Todos los tests corren contra una base de datos en memoria — los datos de produccion nunca se tocan.

---

## Grafo de Conocimiento

Las observaciones se enriquecen en facts estructurados (tripletas sujeto-predicado-objeto):

```
migration  | happened_on | 2026-03-06
database   | current     | sqlite
auth       | changed_to  | jwt
project    | uses        | go
```

Los facts tienen validez temporal — cuando se superseden, el fact anterior mantiene su historia (`valid_until` seteado, `superseded_by` vinculado). Puedes consultar tanto estado actual como cambios historicos.

## Curacion Profunda

Con el tiempo, la memoria acumula ruido. El curator envia un namespace completo a un modelo de lenguaje grande para revision masiva:

- **KEEP** con importancia recalibrada (basada en valor real, no solo en matematica de decay)
- **DELETE** ruido, duplicados y observaciones que ya no aportan senal

```bash
neurox curate --namespace miproyecto --dry-run   # Preview de cambios
neurox curate --namespace miproyecto             # Aplicar
```

Un `priorities.yaml` opcional sesga la curacion hacia senales de valor especificas del dominio.

## Pipeline de Consolidacion

Se ejecuta automaticamente cada 30 minutos (o bajo demanda):

```
 1. Decay         Aplicar decay de activacion (tasas por kind)
 2. Retry         Re-evaluar observaciones previamente rechazadas
 3. Promover      Buffer → Working (importancia + quality gate)
 4. Promover      Working → Core (conteo de accesos + edad)
 5. Dedup         Fusionar casi-duplicados (saltar si ventanas temporales distintas)
 6. Contradicc.   Encontrar conflictos → secuencia temporal? stale suave : supersesion dura
 7. Reflexion     Sintetizar insights de clusters de capa Working
 8. Eviccion      Remover overflow del Buffer por menor importancia
 9. GC            Hard-delete de observaciones expiradas
```

Cada etapa es determinista y auditable. Los runs de consolidacion se loguean con timestamps. Nada pasa en background que no puedas inspeccionar.

---

## Tools MCP

| Tool | Descripcion |
|---|---|
| `save` | Guardar observacion con indexado FTS5 y extraccion temporal |
| `recall` | Busqueda con scoring hibrido (FTS5 + semantico + temporal) |
| `context` | Contexto proactivo: recientes + importantes + vinculadas a archivos |
| `update` | Actualizar observacion por ID |
| `forget` | Soft-delete |
| `invalidate` | Marcar incorrecta, opcionalmente crear reemplazo con link supersedes |
| `status` | Stats del cerebro: capas, staleness, facts, providers |
| `session_start` | Iniciar sesion de trabajo, retornar contexto relevante |
| `session_end` | Terminar sesion con resumen |
| `git_hook` | Reportar archivos cambiados, marcar observaciones vinculadas como stale |
| `reflect` | Sintetizar insights de observaciones de capa Working |
| `consolidate` | Forzar ciclo de consolidacion inmediato |
| `health_check` | Score de brain power (0-100%) con recomendaciones |
| `curate` | Curacion profunda con LLM externo |

### Inputs de Tools

| Tool | Inputs clave |
|---|---|
| `save` | `title`, `content`, `observation_type`, `kind`, `confidence`, `topic_key`, `tags`, `files`, `namespace`, `retention` |
| `recall` | `query`, `observation_type`, `kind`, `namespace`, `files`, `include_stale`, `limit`, `debug` |
| `context` | `namespace`, `files`, `limit` |
| `update` | `id`, `title`, `content`, `observation_type`, `kind`, `confidence`, `tags`, `files`, `retention` |
| `forget` | `id` |
| `invalidate` | `observation_id`, `reason`, `replacement_title`, `replacement_content` |
| `session_start` | `title`, `directory`, `branch`, `namespace` |
| `session_end` | `session_id`, `summary` |
| `git_hook` | `changed_files`, `commit_sha`, `branch` |
| `health_check` | `days` |
| `curate` | `namespace`, `dry_run` |

---

## Referencia CLI

| Comando | Que hace | Flags utiles |
|---|---|---|
| `neurox mcp` | Iniciar servidor MCP (stdio) | — |
| `neurox serve` | Iniciar servidor HTTP en puerto 7438 | `--host` |
| `neurox save "titulo"` | Guardar observacion | `--content`, `--type`, `--kind`, `--confidence`, `--topic-key`, `--tags`, `--files`, `--namespace` |
| `neurox recall "query"` | Buscar con ranking temporal-aware | `--type`, `--kind`, `--namespace`, `--files`, `--include-stale`, `--limit`, `--debug` |
| `neurox context` | Contexto proactivo por namespace/archivos | `--namespace`, `--files`, `--limit` |
| `neurox invalidate <id>` | Marcar observacion incorrecta + reemplazar | `--reason`, `--replacement-title`, `--replacement-content` |
| `neurox status` | Stats del cerebro, providers y DB | — |
| `neurox audit <id>` | Ciclo de vida completo de una observacion | — |
| `neurox consolidate` | Forzar consolidacion completa | — |
| `neurox graph` | Vista HTML interactiva del grafo | `--namespace`, `--type`, `--tags`, `--min-importance`, `--limit`, `--linked-only`, `--output`, `--no-browser` |
| `neurox setup <agente>` | Configurar integracion de agente IA | — |
| `neurox config` | Imprimir config resuelta en runtime | — |
| `neurox install-hook` | Instalar hook git post-commit | — |
| `neurox curate` | Curacion profunda de memoria | `--namespace`, `--dry-run` |
| `neurox reembed` | Re-embeddear todas las observaciones | — |
| `neurox export` | Exportar como archivos Markdown | `--format`, `--output`, `--namespace` |
| `neurox import` | Importar archivos .md de observaciones | `--source` |
| `neurox benchmark` | Ejecutar suite de brain benchmark | `--scale`, `--category`, `--dimensions`, `--output`, `--output-html`, `--verbose` |
| `neurox update` | Actualizar a la ultima version | `--yes` |

Todos los comandos de datos (`save`, `recall`, `context`, `invalidate`, `status`, `audit`, `config`) devuelven JSON.

---

## API REST

```
GET    /health                               Health check
GET    /api/v1/status                        Estadisticas del cerebro
GET    /api/v1/health-check                  Score de brain power
GET    /api/v1/decay-timeline                Importancia por capa por dia
GET    /api/v1/stats/activity                Actividad de tool calls por dia
GET    /api/v1/stats/breakdown               Desglose por tipo/capa/namespace/kind
GET    /api/v1/observations/browse           Navegar observaciones recientes
POST   /api/v1/observations                  Guardar observacion
GET    /api/v1/observations/search?q=...     Buscar memorias
GET    /api/v1/observations/context          Contexto proactivo
GET    /api/v1/observations/{id}             Obtener observacion
PUT    /api/v1/observations/{id}             Actualizar
DELETE /api/v1/observations/{id}             Soft-delete
POST   /api/v1/observations/{id}/invalidate  Invalidar + reemplazar
POST   /api/v1/sessions                      Iniciar sesion
PUT    /api/v1/sessions/{id}/end             Terminar sesion
POST   /api/v1/hooks/git                     Git hook
GET    /api/v1/graph                         Vista del grafo (HTML o ?format=json)
POST   /api/v1/reflect                       Disparar reflexion
POST   /api/v1/consolidate                   Forzar consolidacion
POST   /api/v1/curate                        Curacion profunda
```

### Query Parameters

| Ruta | Parametros |
|---|---|
| `GET /observations/search` | `q`, `type`, `kind`, `namespace`, `files`, `staleness`, `include_stale`, `limit`, `debug` |
| `GET /observations/context` | `namespace`, `files`, `limit` |
| `GET /observations/browse` | `limit`, `offset`, `type`, `layer`, `namespace`, `kind`, `staleness` |
| `GET /graph` | `namespace`, `type`, `tags`, `min_importance`, `limit`, `linked_only`, `format` |
| `GET /stats/activity` | `days` |
| `GET /health-check` | `days` |
| `GET /decay-timeline` | `days`, `layers` |

### Ejemplos de Payload

```json
POST /api/v1/observations
{
  "title": "Middleware JWT",
  "content": "What: Added RS256 middleware\nWhy: Standardize API auth\nWhere: internal/auth/middleware.go",
  "observation_type": "decision",
  "kind": "semantic",
  "confidence": 0.9,
  "tags": ["auth", "jwt"],
  "files": ["internal/auth/middleware.go"],
  "namespace": "miproyecto"
}
```

```json
POST /api/v1/hooks/git
{
  "changed_files": ["README.md", "main.go"],
  "commit_sha": "b04b533",
  "branch": "main"
}
```

---

## Configuracion de Agentes

Guias de configuracion por cliente:

| Cliente IA | Guia |
|---|---|
| Claude Code | [docs/claude-code.md](docs/claude-code.md) |
| Claude Desktop | [docs/claude-desktop.md](docs/claude-desktop.md) |
| Cursor | [docs/cursor.md](docs/cursor.md) |
| VS Code | [docs/vscode.md](docs/vscode.md) |
| OpenCode | [docs/opencode.md](docs/opencode.md) |

Todos los clientes usan el mismo patron: instalar el binario, agregar `neurox` a `mcpServers` con `command: "neurox"` y `args: ["mcp"]`, reiniciar.

**Lectura adicional:** [docs/concepts.md](docs/concepts.md) — terminos clave: curva de decay, consolidacion, capas de memoria, staleness, intent temporal, observaciones vs. facts, brain power score, provenance, modo debug.

---

## Tipos de Observacion

| Tipo | Cuando usarlo | Ejemplo |
|---|---|---|
| `decision` | Decisiones de arquitectura o diseno | "Elegimos SQLite para deploy single-file" |
| `bugfix` | Que se rompio y por que | "Query N+1 en lista de usuarios, arreglado con preload" |
| `discovery` | Aprendiste algo del codebase | "El middleware de auth corre antes que CORS" |
| `pattern` | Convenciones recurrentes | "Todos los stores usan inyeccion por constructor" |
| `gotcha` | Trampas y edge cases | "Hay que usar -tags fts5 para compilar" |
| `config` | Setup de entorno y herramientas | "El CI usa Go 1.23 con CGO" |
| `preference` | Correcciones y preferencias del usuario | "Preferir tests table-driven" |
| `question` | Preguntas abiertas para revision | "Deberiamos separar este paquete?" |

---

## Configuracion

Archivo de config: `~/.config/neurox/config.yaml`

```yaml
database:
  path: ~/.config/neurox/neurox.db

llm:
  provider: ""          # "ollama", "remote", "" (auto-detectar)
  gate_mode: "auto"     # "auto", "full", "off"
  ollama_url: ""        # default: http://localhost:11434
  ollama_model: ""      # default: qwen2.5:3b
  remote_url: ""        # Endpoint compatible con OpenAI
  remote_api_key: ""
  remote_model: ""

embeddings:
  provider: ""          # "ollama", "remote", "" (auto-detectar)
  remote_url: ""        # Endpoint de embeddings compatible con OpenAI
  remote_api_key: ""
  remote_model: ""
  dimensions: 0         # auto-detectar del provider

curator:
  provider: ""          # "remote" o "" (deshabilitado)
  remote_url: ""        # Endpoint compatible OpenAI
  remote_api_key: ""
  remote_model: ""      # ej. "gemini-2.5-flash"
  priorities_file: ""   # ruta a priorities.yaml

consolidation:
  dedup_threshold: 0.85
  contradiction_min: 0.65
  contradiction_max: 0.85
  related_min: 0.65
  related_max: 0.85
```

### Variables de Entorno

Todos los settings se pueden sobreescribir con prefijo `NEUROX_`:

| Variable | Proposito |
|---|---|
| `NEUROX_DATABASE_PATH` | Ruta personalizada de la base SQLite |
| `NEUROX_HTTP_HOST` | Direccion de bind HTTP (default: `127.0.0.1`) |
| `NEUROX_LLM_PROVIDER` | `ollama`, `remote`, o vacio |
| `NEUROX_LLM_GATE_MODE` | `auto`, `full`, u `off` |
| `NEUROX_EMBED_PROVIDER` | Provider de embeddings |
| `NEUROX_CURATOR_PROVIDER` | Provider del curator (`remote` o vacio) |

Lista completa de overrides de entorno en [docs/concepts.md](docs/concepts.md).

---

## Rendimiento

| Operacion | Latencia | Notas |
|---|---|---|
| `save` | <1ms | Insert SQLite + indice FTS5 + extraccion temporal |
| `recall` (FTS) | <5ms | Ranking BM25 con scoring temporal |
| `recall` (hibrido) | <50ms | FTS + semantico + boost cross-signal |
| `context` | <10ms | Retrieval proactivo multi-senal |
| `consolidate` | <1s | Ciclo completo para 1,000 observaciones |
| Tamano binario | ~15MB | Ejecutable unico (enlaza dinamicamente libc para SQLite/CGO) |
| Memoria | <150MB | Con 10k observaciones + embeddings |

---

## Arquitectura

```
neurox/
├── main.go                    Punto de entrada CLI
├── internal/
│   ├── api/                   Servidor HTTP REST + dashboard
│   ├── benchmark/             Suite de brain benchmark
│   ├── classify/              Auto-clasificacion de tipo y kind
│   ├── config/                Carga de config YAML + env
│   ├── consolidate/           Pipeline background
│   ├── contradiction/         Deteccion de conflictos + supersesion temporal
│   ├── curate/                Curacion profunda con LLM externo
│   ├── db/                    Schema SQLite, migraciones, modo WAL
│   ├── decay/                 Decay por activacion, garbage collection
│   ├── embed/                 Embeddings Ollama + compatible OpenAI
│   ├── export/                Exportacion e importacion Markdown
│   ├── facts/                 Tripletas de conocimiento, extraccion LLM
│   ├── filelink/              Vinculacion archivo-observacion
│   ├── graph/                 Render HTML interactivo + queries del grafo
│   ├── health/                Scoring de brain power con recomendaciones
│   ├── installer/             Instalador TUI con Bubble Tea
│   ├── links/                 Relaciones entre observaciones
│   ├── llm/                   Providers LLM, quality gate
│   ├── mcp/                   Servidor protocolo MCP
│   ├── observation/           Tipos core, CRUD, extraccion temporal
│   ├── proactive/             Retrieval de contexto sin queries
│   ├── recall/                FTS5 + semantico + busqueda temporal
│   ├── reflect/               Sintesis de insights
│   ├── session/               Ciclo de vida de sesiones
│   ├── telemetry/             Tracking de tool calls
│   └── temporal/              Parser de fechas, almacenamiento de menciones
├── benchmarks/longmemeval/    Harness del benchmark LongMemEval
├── tests/integration/         Tests E2E + benchmarks de rendimiento
└── scripts/post-commit        Git hook para tracking de staleness
```

## Tecnologia

- **Go 1.23** — binario unico, goroutines para consolidacion background
- **SQLite 3** — modo WAL, busqueda full-text FTS5, via mattn/go-sqlite3 (CGO)
- **Embeddings** — Ollama (nomic-embed-text) o cualquier API compatible OpenAI (opcional)
- **LLM** — Ollama o compatible OpenAI (opcional — para quality gate, reflexion, facts)
- **MCP** — Model Context Protocol via mark3labs/mcp-go
- **IDs** — ULID (monotonico, sorteable) via oklog/ulid

## Licencia

[BSL 1.1](LICENSE) (Business Source License 1.1)

Puedes usar, modificar y distribuir Neurox para cualquier proposito **excepto** ofrecerlo como un servicio comercial hospedado que compita con las ofertas pagas del Licenciante. El **2030-03-28**, la licencia se convierte automaticamente a **Apache 2.0**.

Consulta el archivo [LICENSE](LICENSE) para el texto completo.
