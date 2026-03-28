<p align="center">
  <!-- <img src="assets/neurox-banner.png" alt="Neurox" width="800"> -->
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>Motor de memoria inspirado en el cerebro para agentes de IA</strong>
  </p>
  <p align="center">
    Memoria en tres capas &bull; Busqueda hibrida &bull; Razonamiento temporal &bull; Decaimiento Ebbinghaus &bull; Pipelines de consolidacion
  </p>
  <p align="center">
    <a href="#resultados-del-benchmark">98% Recall en LongMemEval</a> &bull;
    <a href="#inicio-rapido">Inicio Rapido</a> &bull;
    <a href="README.md">Read in English</a>
  </p>
</p>

---

Neurox da a los agentes de IA una memoria persistente y estructurada que funciona como un cerebro. Almacena observaciones en tres capas de memoria, promueve automaticamente los recuerdos importantes, detecta y resuelve contradicciones, y entiende *cuando* pasaron las cosas — no solo *que*.

**98% de precision en retrieval** en el benchmark LongMemEval (configuracion S, 48 sesiones de distraccion por query). FTS5 puro, sin LLM.

## Como funciona

```
        Tu programas con un agente de IA
                      |
                      v
    +------------------------------+
    |    El agente guarda memoria  |   "Migramos a SQLite hace una semana"
    +------------------------------+
                      |
                      v
    +------------------------------+
    |           Neurox             |
    |                              |
    |  1. Parsear info temporal    |   -> "hace una semana" = 2026-03-13
    |  2. Extraer facts            |   -> migration | happened_on | 2026-03-13
    |  3. Guardar en Buffer        |   -> Indexado FTS5, embeddings en cola
    |  4. Vincular a archivos      |   -> internal/db/schema.sql
    +------------------------------+
                      |
              (ciclo de 30 min)
                      v
    +------------------------------+
    |       Consolidacion          |
    |                              |
    |  Decay -> Promover ->        |
    |  Dedup -> Contradicciones    |
    |  -> Reflexion -> Eviccion    |
    +------------------------------+
                      |
                      v
    +------------------------------+
    |  El agente pregunta memoria  |   "Que DB usamos actualmente?"
    +------------------------------+
                      |
                      v
    +------------------------------+
    |     Recall con consciencia   |
    |         temporal             |
    |                              |
    |  1. Detectar intent          |   -> current_state
    |  2. FTS5 + BM25              |   -> matching por keywords
    |  3. Busqueda semantica       |   -> similitud coseno
    |  4. Scoring temporal         |   -> boost fresh, penalizar stale
    |  5. Retornar rankeado        |   -> "SQLite" queda primero
    +------------------------------+
```

## El Modelo de Memoria en Tres Capas

Inspirado en los sistemas de memoria humana, Neurox organiza el conocimiento en tres capas con promocion automatica basada en importancia y patrones de acceso.

```
 Capa 0: Buffer                 Capa 1: Working               Capa 2: Core
 +-------------------+         +-------------------+         +-------------------+
 |                    |         |                    |         |                    |
 |  Corto plazo       |  --->  |  Mediano plazo     |  --->  |  Largo plazo       |
 |  Observaciones     | promov |  Info validada      | promov |  Conocimiento      |
 |  nuevas, sin       |        |  Acceso frecuente   |        |  probado           |
 |  filtrar           |         |                    |         |  Alta confianza    |
 |                    |         |                    |         |                    |
 |  Capacidad: 200    |         |  Dedup + Reflexion |         |  Permanente        |
 |  Decay: rapido     |         |  Decay: moderado   |         |  Decay: lento      |
 +-------------------+         +-------------------+         +-------------------+
         |                              |                              |
         +------------------------------+------------------------------+
                                        |
                              Decay de Ebbinghaus
                     (episodico: rapido, semantico: medio, procedural: lento)
```

**Reglas de promocion:**
- Buffer -> Working: umbral de importancia (0.3) o tipo procedural, con quality gate LLM opcional
- Working -> Core: accedido 5+ veces Y mayor a 7 dias Y `retention = 'durable'` (observaciones operacionales se quedan en Working)
- Cada capa tiene su propia tasa de decay segun el tipo de memoria

## Razonamiento Temporal

Neurox entiende el *tiempo*. Cuando guardas "Migramos a SQLite la semana pasada" o preguntas "Que base de datos usabamos antes?", sabe a que te refieres.

### Como funciona

**Al guardar** — se extraen y normalizan expresiones temporales:
```
"Migramos a SQLite la semana pasada"
  +-> kind: relative, normalized: 2026-03-13, confidence: 0.85

"Actualmente usamos PostgreSQL 16"
  +-> kind: current_state, confidence: 0.95

"Desplegado el 5 de marzo de 2026"
  +-> kind: absolute, normalized: 2026-03-05, confidence: 0.95
```

Soporta ingles y espanol. Maneja fechas absolutas, expresiones relativas (ayer, hace 3 semanas, two months ago), marcadores de estado actual, duraciones y rangos de fechas.

**Al buscar** — se detecta el intent temporal en la query y se ajusta el scoring:

| Patron de query | Intent detectado | Efecto |
|---|---|---|
| "actualmente", "ahora", "latest" | `current_state` | Boost a fresh, penaliza stale |
| "antes", "previamente", "used to" | `history` | Incluye expirados, boost a antiguos |
| "cuando", "what date" | `when` | Boost a observaciones con fechas |
| "cuanto tiempo", "how long" | `duration` | Boost a menciones de duracion |
| "marzo 2026", "last week" | `point_in_time` | Boost por proximidad temporal |
| Sin palabras temporales | ninguno | Scoring tri-factor estandar |

**En contradiccion** — las secuencias temporales se preservan, no se destruyen:
```
Viejo: "Usamos PostgreSQL"      ->  staleness: stale (aun consultable como historia)
Nuevo: "Migramos a SQLite"      ->  staleness: fresh (rankea primero en queries actuales)
Link: nuevo supersede al viejo
```

La observacion vieja se vuelve *stale* (no *expired*), asi que "Que usabamos antes?" todavia la encuentra.

## Busqueda Hibrida

El recall combina multiples senales en un unico score:

```
Score = (Recencia x 0.3) + (Importancia x 0.3) + (Relevancia x 0.4)
        x Boost cross-signal (1.2x si FTS n semantico)
        x Multiplicador temporal (0.7x - 1.5x segun match de intent)
```

| Senal | Fuente | Que captura |
|---|---|---|
| **Relevancia** | FTS5 BM25 + coseno semantico | Que tan bien matchea el contenido con la query |
| **Recencia** | Curva de decay Ebbinghaus (half-life 30 dias) | Que tan reciente fue creado o accedido |
| **Importancia** | Peso inicial + boosts por acceso | Que tan valioso es el recuerdo |
| **Temporal** | Deteccion de intent + matching de menciones | Si este recuerdo encaja en el contexto temporal |
| **Cross-signal** | Overlap FTS n Semantico | Boost de confianza cuando ambos metodos coinciden |

## Pipeline de Consolidacion

Se ejecuta automaticamente cada 30 minutos (o bajo demanda con la tool `consolidate`):

```
 1. Decay         Aplicar curvas de Ebbinghaus a todas las observaciones
       |
 2. Retry         Re-evaluar observaciones previamente rechazadas (sistema 3-strikes)
       |
 3. Promover      Buffer -> Working (importancia + quality gate)
       |
 4. Promover      Working -> Core (conteo de accesos + edad)
       |
 5. Dedup         Fusionar casi-duplicados (coseno >= 0.85)
       |            +-- Saltar si ventanas temporales distintas (preserva lineas de tiempo)
 6. Contradicc.   Encontrar observaciones conflictivas
       |            +-- Secuencia temporal? -> supersesion suave (stale)
       |            +-- LLM confirma? -> superseder (con contexto temporal: stale; sin: expired)
       |            +-- Sin LLM? -> crear question para revision humana
 7. Reflexion     Sintetizar insights de clusters de capa Working
       |
 8. Eviccion      Remover overflow del Buffer por menor importancia
       |
 9. GC            Hard-delete de observaciones expiradas
```

## Grafo de Conocimiento

Las observaciones se enriquecen en facts estructurados (tripletas sujeto-predicado-objeto):

```
migration  | happened_on | 2026-03-06
database   | current     | sqlite
auth       | changed_to  | jwt
project    | uses        | go
```

Los facts tienen validez temporal — cuando un fact es supersedido, el anterior mantiene su historia (`valid_until` seteado, `superseded_by` vinculado). Puedes consultar tanto el estado actual como cambios historicos.

## Curacion Profunda

Con el tiempo, la memoria acumula ruido — observaciones de bajo valor, duplicados que escaparon al dedup, scores de importancia distorsionados por decay agresivo. La curacion arregla esto enviando un namespace completo a un modelo de lenguaje grande para revision masiva.

El curator examina cada observacion y decide:
- **KEEP** con importancia recalibrada (basada en valor real, no solo en matematica de decay)
- **DELETE** ruido, entradas redundantes y observaciones que ya no aportan senal

```bash
# Preview de lo que haria el curator
neurox curate --namespace miproyecto --dry-run

# Aplicar curacion
neurox curate --namespace miproyecto
```

La curacion requiere un curator provider configurado — tipicamente un modelo grande y capaz como Gemini Flash que pueda procesar cientos de observaciones en una sola ventana de contexto. Configuralo en `config.yaml` bajo `curator:` o via variables de entorno `NEUROX_CURATOR_*`.

**Archivo de prioridades**: Un `priorities.yaml` opcional te permite decirle al curator que es mas importante en tu dominio — por ejemplo, "las decisiones sobre base de datos son alta prioridad" o "las notas de debugging temporal son baja prioridad". Esto sesga la curacion hacia conservar conocimiento relevante al dominio.

Disponible via `neurox curate` en CLI y la tool `curate` de MCP (con modo `dry_run` opcional).

## Resultados del Benchmark

Evaluado en [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025) — un benchmark de memoria conversacional a largo plazo con 500 preguntas en 6 categorias.

### LongMemEval-S (48 sesiones de distraccion por query)

| Categoria | N | Recall@10 | NDCG@10 |
|---|---|---|---|
| knowledge-update | 72 | **100.0%** | 96.9% |
| single-session-user | 64 | 98.4% | 97.0% |
| single-session-assistant | 56 | 98.2% | 95.1% |
| temporal-reasoning | 127 | 97.6% | 87.2% |
| multi-session | 121 | 98.4% | 87.0% |
| single-session-preference | 30 | 93.3% | 73.8% |
| **Overall** | **470** | **98.1%** | **90.0%** |

> FTS5 + BM25 + scoring temporal, sin LLM. 500 preguntas en ~2 minutos.

## Brain Benchmark

Mas alla de la precision de retrieval (medida por LongMemEval), Neurox incluye un suite de benchmark autocontenido que evalua el motor de memoria completo en 12 dimensiones y 3 categorias.

| Categoria | Peso | Dimensiones |
|---|---|---|
| **Cognitivo** | 45% | Knowledge Update, Signal vs Noise, Cross-Session, Temporal Reasoning, Memory Lifecycle |
| **Rendimiento** | 20% | Write Throughput, Recall Latency, Concurrent Access, Context Retrieval |
| **Simulacion de Agente** | 35% | Lazy vs Perfect Agent, Realistic Workflows, Parameter Impact |

Cada dimension ejecuta checks aislados contra una base de datos en memoria (nunca toca tus datos de produccion) y produce un score mapeado a cuatro tiers:

| Tier | Rango | Significado |
|---|---|---|
| Beyond | 95-100 | Supera expectativas elite |
| Elite | 70-95 | Grado produccion |
| Target | 40-70 | Baseline aceptable |
| Base | 0-40 | Debajo de expectativas |

Los scores se agregan en una nota general (S/A/B/C/D/F) con promedio ponderado por categoria.

```bash
# Ejecucion rapida (1k observaciones)
neurox benchmark

# Ejecucion completa con reporte
neurox benchmark --scale large --output report.json --output-html report.html

# Ejecutar categoria especifica
neurox benchmark --category cognitive --verbose
```

Scale controla el tamano del dataset sintetico: `small` (1k), `medium` (10k), `large` (100k observaciones).

## Inicio Rapido

### Instalador interactivo

**Linux / macOS:**
```bash
./install.sh
```

**Windows (PowerShell):**
```powershell
irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
```

El instalador abre una TUI hecha con Bubble Tea donde puedes elegir el directorio del binario, el directorio de configuracion de Neurox, providers locales o remotos, integraciones de editor y si quieres instalar el git hook en el repo actual.

Antes de escribir nada, te muestra exactamente donde va a guardar la configuracion y que settings de provider hacen falta.

### Compilar

```bash
# Requiere CGO para SQLite
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

Nota: el ejecutable resultante es portable, pero no totalmente estatico; con SQLite via CGO enlaza contra `libc`/`libm` del sistema.

### Usar con agentes de IA (MCP)

```bash
./neurox mcp
```

### Usar como API HTTP

```bash
./neurox serve  # localhost:7438
```

El hook `post-commit` envia eventos al servidor HTTP en `POST /api/v1/hooks/git`. El puerto por defecto del hook es `7438`; si tu servidor escucha en otro puerto, define `NEUROX_PORT` antes de instalar o ejecutar el hook.

### CLI

```bash
# Guardar un recuerdo
neurox save "Patron de auth JWT" \
  --content "Usando JWT con RS256 para auth de API" \
  --type decision \
  --tags "auth,jwt" \
  --files "internal/auth/middleware.go"

# Buscar recuerdos
neurox recall "autenticacion" --namespace miproyecto --limit 5

# Obtener contexto para trabajo actual
neurox context --namespace miproyecto --files "src/auth.go"

# Ver salud del cerebro
neurox status

# Forzar consolidacion
neurox consolidate

# Abrir la vista interactiva del grafo
neurox graph --output neurox-graph.html

# Instalar git hook (marca recuerdos como stale cuando cambian archivos)
neurox install-hook

# Exportar e importar memorias
neurox export --namespace miproyecto --output ./backup
neurox import --source ./backup

# Ejecutar brain benchmark
neurox benchmark --scale medium --output report.json

# Curacion profunda (preview primero, luego aplicar)
neurox curate --namespace miproyecto --dry-run
neurox curate --namespace miproyecto

# Re-embeddear tras cambio de modelo de embeddings
neurox reembed
```

La mayoria de los comandos CLI imprimen JSON, asi que se integran bien con scripts y pipes.

## Referencia CLI

| Comando | Que hace | Flags utiles |
|---|---|---|
| `neurox mcp` | Inicia el servidor MCP por stdio | ninguna |
| `neurox serve` | Inicia el servidor HTTP en el puerto `7438` | ninguna |
| `neurox save "title"` | Guarda una observacion en Buffer | `--content`, `--type`, `--kind`, `--confidence`, `--topic-key`, `--tags`, `--files`, `--namespace` |
| `neurox recall "query"` | Busca memoria con ranking temporal-aware | `--type`, `--kind`, `--namespace`, `--files`, `--include-stale`, `--limit` |
| `neurox context` | Devuelve contexto proactivo por namespace o archivos | `--namespace`, `--files`, `--limit` |
| `neurox invalidate <id>` | Marca una observacion como incorrecta y puede crear reemplazo | `--reason`, `--replacement-title`, `--replacement-content` |
| `neurox status` | Muestra stats del cerebro, providers y DB | ninguna |
| `neurox consolidate` | Fuerza un ciclo completo de consolidacion | ninguna |
| `neurox graph` | Genera una vista HTML interactiva del grafo | `--namespace`, `--type`, `--tags`, `--min-importance`, `--limit`, `--linked-only`, `--output`, `--no-browser` |
| `neurox config` | Imprime la configuracion resuelta en runtime | ninguna |
| `neurox install-hook` | Instala el hook `post-commit` en el repo actual | ninguna |
| `neurox curate` | Curacion profunda de memoria con LLM externo | `--namespace`, `-n`, `--dry-run` |
| `neurox reembed` | Re-embeddear todas las observaciones (util tras cambio de modelo) | ninguna |
| `neurox export` | Exportar observaciones como archivos Markdown | `--format`, `--output`, `--namespace` |
| `neurox import` | Importar archivos .md de observaciones a la base de datos | `--source` |
| `neurox benchmark` | Ejecutar suite de brain benchmark | `--scale`, `--category`, `--dimensions`, `--output`, `--output-html`, `--verbose` |
| `neurox update` | Actualizar neurox a la ultima version | `--yes`, `-y` |

### Notas CLI

- `save`, `recall`, `context`, `invalidate`, `status` y `config` devuelven JSON por stdout.
- `graph` escribe `neurox-graph.html` por defecto y abre el navegador salvo que uses `--no-browser`.
- `install-hook` no sobreescribe un hook existente; elimina `.git/hooks/post-commit` primero si quieres reemplazarlo.
- `curate` requiere un curator provider configurado (ver Configuracion).
- `export` escribe un archivo `.md` por observacion. `import` los lee de vuelta.
- `benchmark` ejecuta un suite de tests aislado en memoria — no toca tu base de datos de produccion.

## Configuracion de Agentes

### Claude Code

Agregar a `~/.claude/settings.json` o `.mcp.json` del proyecto:

```json
{
  "mcpServers": {
    "neurox": {
      "command": "/ruta/a/neurox",
      "args": ["mcp"]
    }
  }
}
```

### Cursor

Settings > MCP Servers > Add:
- Name: `neurox`
- Command: `/ruta/a/neurox mcp`
- Transport: `stdio`

### OpenCode

Agregar a `opencode.json`:

```json
{
  "mcp": {
    "neurox": {
      "type": "local",
      "command": ["/ruta/a/neurox", "mcp"],
      "enabled": true
    }
  }
}
```

### Windsurf / Copilot / Clientes HTTP

```bash
neurox serve  # API REST en puerto 7438
```

## Tools MCP

| Tool | Descripcion |
|---|---|
| **`save`** | Guardar observacion en Buffer con indexado FTS5 y extraccion temporal |
| **`recall`** | Busqueda temporal-aware con scoring hibrido (FTS5 + semantico + temporal) |
| **`context`** | Contexto proactivo: observaciones recientes + importantes + vinculadas a archivos |
| **`update`** | Actualizar observacion existente por ID |
| **`forget`** | Soft-delete de observacion |
| **`invalidate`** | Marcar como incorrecta, opcionalmente crear reemplazo con link supersedes |
| **`status`** | Stats del cerebro: capas, staleness, facts, menciones temporales, providers |
| **`session_start`** | Iniciar sesion de trabajo, cerrar anterior, retornar contexto relevante |
| **`session_end`** | Terminar sesion con resumen, LLM extrae observaciones atomicas |
| **`git_hook`** | Reportar archivos cambiados de commit, marcar observaciones vinculadas como stale |
| **`reflect`** | Sintetizar observaciones de capa Working en insights de alto nivel |
| **`consolidate`** | Forzar ciclo completo de consolidacion inmediato |
| **`health_check`** | Calcular score de brain power (0-100%) con desglose por dimension y recomendaciones |
| **`curate`** | Curacion profunda de memoria: revisar namespace con modelo grande, eliminar ruido, recalibrar importancia |

### Inputs de Tools MCP

| Tool | Inputs clave |
|---|---|
| `save` | `title`, `content`, `observation_type`, `kind`, `confidence`, `topic_key`, `tags`, `files`, `namespace`, `retention` |
| `recall` | `query`, `observation_type`, `kind`, `namespace`, `files`, `include_stale`, `limit` |
| `context` | `namespace`, `files`, `limit` |
| `update` | `id`, `title`, `content`, `observation_type`, `kind`, `confidence`, `tags`, `files`, `retention` |
| `forget` | `id` |
| `invalidate` | `observation_id`, `reason`, `replacement_title`, `replacement_content` |
| `status` | sin inputs |
| `session_start` | `title`, `directory`, `branch`, `namespace` |
| `session_end` | `session_id`, `summary` |
| `git_hook` | `changed_files`, `commit_sha`, `branch` |
| `reflect` | `namespace` |
| `consolidate` | sin inputs |
| `health_check` | `days` |
| `curate` | `namespace`, `dry_run` |

La superficie MCP es la mejor opcion para agentes de codigo; la CLI y la API HTTP exponen el mismo motor para scripts locales, dashboards y debugging.

## API REST

```
GET    /health                              Health check
GET    /api/v1/status                       Estadisticas del cerebro
GET    /api/v1/observations/browse          Navegar observaciones recientes
POST   /api/v1/observations                 Guardar observacion
GET    /api/v1/observations/search?q=...    Buscar recuerdos
GET    /api/v1/observations/context         Obtener contexto proactivo
GET    /api/v1/observations/{id}            Obtener observacion
PUT    /api/v1/observations/{id}            Actualizar observacion
DELETE /api/v1/observations/{id}            Soft-delete
POST   /api/v1/observations/{id}/invalidate Invalidar + reemplazar
GET    /api/v1/stats/breakdown              Desglose por tipo/capa/namespace/kind
GET    /api/v1/health-check                 Score de brain power y desglose por dimensiones
GET    /api/v1/decay-timeline               Importancia promedio por capa por dia
GET    /api/v1/stats/activity               Actividad de tool calls por dia
POST   /api/v1/sessions                     Iniciar sesion
PUT    /api/v1/sessions/{id}/end            Terminar sesion
POST   /api/v1/hooks/git                    Git hook
GET    /api/v1/graph                        Vista interactiva del grafo (o JSON con ?format=json)
POST   /api/v1/reflect                      Disparar reflexion
```

### Query Params REST

| Ruta | Query params soportados |
|---|---|
| `GET /api/v1/observations/search` | `q`, `type`, `kind`, `namespace`, `files`, `staleness`, `include_stale`, `limit` |
| `GET /api/v1/observations/context` | `namespace`, `files`, `limit` |
| `GET /api/v1/observations/browse` | `limit`, `offset`, `type`, `layer`, `namespace`, `kind`, `staleness` |
| `GET /api/v1/graph` | `namespace`, `type`, `tags`, `min_importance`, `limit`, `linked_only`, `format=json` |
| `GET /api/v1/stats/activity` | `days` |
| `GET /api/v1/health-check` | `days` |
| `GET /api/v1/decay-timeline` | `days`, `layers` |

### Ejemplos de payload REST

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
  "namespace": "neurox"
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

`POST /api/v1/reflect` hoy devuelve una respuesta placeholder; la sintesis reflectiva completa esta mejor expuesta via MCP y el motor interno, mientras que la entrada REST sigue minima.

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
  remote_url: ""        # Endpoint compatible OpenAI para curacion
  remote_api_key: ""
  remote_model: ""      # ej. "gemini-2.5-flash"
  priorities_file: ""   # ruta a priorities.yaml para pesos de dominio

consolidation:
  dedup_threshold: 0.85           # umbral de similitud coseno para dedup
  contradiction_min: 0.65         # similitud minima para check de contradiccion
  contradiction_max: 0.85         # similitud maxima para check de contradiccion
  related_min: 0.65               # similitud minima para links relates_to
  related_max: 0.85               # similitud maxima para links relates_to
```

Los ejemplos de configuracion de agentes siguen lo que genera `install.sh`: Claude y Cursor usan `command` + `args`, mientras que OpenCode usa una entrada MCP local con `command` como array.

Variables de entorno con prefijo `NEUROX_`:

```bash
NEUROX_DATABASE_PATH=/ruta/custom/path.db
NEUROX_LLM_PROVIDER=ollama
NEUROX_LLM_GATE_MODE=auto
NEUROX_EMBED_PROVIDER=ollama
```

Overrides comunes:

| Variable | Proposito |
|---|---|
| `NEUROX_CONFIG_DIR` | Cambia el directorio de configuracion por defecto |
| `NEUROX_CONFIG_PATH` | Carga config desde otro YAML |
| `NEUROX_DATABASE_PATH` | Apunta a otra base SQLite |
| `NEUROX_LLM_PROVIDER` | Define `ollama`, `remote` o vacio para auto-detect |
| `NEUROX_LLM_GATE_MODE` | Define `auto`, `full` u `off` |
| `NEUROX_LLM_OLLAMA_URL` / `NEUROX_LLM_OLLAMA_MODEL` | Override del endpoint/modelo Ollama para LLM |
| `NEUROX_LLM_REMOTE_URL` / `NEUROX_LLM_REMOTE_API_KEY` / `NEUROX_LLM_REMOTE_MODEL` | Override de settings remotos OpenAI-compatible para LLM |
| `NEUROX_EMBED_PROVIDER` | Define el provider de embeddings |
| `NEUROX_EMBED_REMOTE_URL` / `NEUROX_EMBED_REMOTE_API_KEY` / `NEUROX_EMBED_REMOTE_MODEL` | Override de settings remotos de embeddings |
| `NEUROX_CURATOR_PROVIDER` | Define el provider del curator (`remote` o vacio) |
| `NEUROX_CURATOR_REMOTE_URL` | Endpoint LLM del curator |
| `NEUROX_CURATOR_REMOTE_API_KEY` | API key del curator |
| `NEUROX_CURATOR_REMOTE_MODEL` | Nombre del modelo curator |
| `NEUROX_CURATOR_PRIORITIES_FILE` | Ruta a priorities.yaml |

### Degradacion Graceful

Neurox funciona sin ningun servicio externo. Las features se activan segun lo que este disponible:

| Disponible | Features habilitadas |
|---|---|
| Nada | Busqueda FTS5, parsing temporal, promocion heuristica, decay |
| + Ollama embeddings | Busqueda hibrida, dedup semantico, deteccion de contradicciones |
| + Ollama LLM | Quality gate, extraccion de facts, reflexion, extraccion de sesion |
| + API remota | Lo mismo que arriba con provider en la nube |
| + Curator LLM (remoto) | Curacion profunda, reflexiones de mayor calidad |

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

## Arquitectura

```
neurox/
+-- main.go                    Punto de entrada CLI
+-- internal/
|   +-- api/                   Servidor HTTP REST + dashboard
|   +-- benchmark/             Suite de brain benchmark (12 dimensiones, 3 categorias)
|   +-- classify/              Auto-clasificacion de tipo y kind de observacion
|   +-- config/                Carga de config YAML + env
|   +-- consolidate/           Pipeline background (promover, dedup, evictar)
|   +-- contradiction/         Deteccion de conflictos + supersesion temporal
|   +-- curate/                Curacion profunda de memoria con LLM externo
|   +-- db/                    Schema SQLite, migraciones, modo WAL
|   +-- decay/                 Curvas de Ebbinghaus, garbage collection
|   +-- embed/                 Embeddings Ollama + compatible OpenAI
|   +-- export/                Exportacion e importacion Markdown
|   +-- facts/                 Tripletas de conocimiento, extraccion LLM
|   +-- filelink/              Vinculacion archivo-observacion
|   +-- graph/                 Render HTML interactivo + queries del grafo
|   +-- health/                Scoring de brain power (0-100) con recomendaciones
|   +-- installer/             Instalador interactivo TUI con Bubble Tea
|   +-- links/                 Relaciones entre observaciones (supersedes, contradicts)
|   +-- llm/                   Providers LLM, quality gate, sistema 3-strikes
|   +-- mcp/                   Servidor protocolo MCP
|   +-- observation/           Tipos core, CRUD, extraccion temporal
|   +-- proactive/             Retrieval de contexto sin queries explicitas
|   +-- recall/                FTS5 + semantico + busqueda temporal-aware
|   +-- reflect/               Sintesis de insights (patron Generative Agents)
|   +-- session/               Ciclo de vida de sesiones, extraccion LLM
|   +-- telemetry/             Tracking de tool calls para analiticas de uso
|   +-- temporal/              Parser de fechas, almacenamiento de menciones
+-- benchmarks/
|   +-- longmemeval/           Harness del benchmark LongMemEval
+-- tests/
|   +-- integration/           Tests E2E + benchmarks de rendimiento
+-- scripts/
    +-- post-commit            Git hook para tracking de staleness
```

`internal/graph/` soporta la feature publica de grafo usada por `neurox graph` y `GET /api/v1/graph`, con visualizacion HTML interactiva de observaciones y links.

## Solucion de problemas

- Los eventos del git hook se envian al servidor HTTP, asi que `neurox serve` debe estar corriendo cuando haces commits.
- El hook usa el puerto `7438` por defecto. Si tu servidor corre en otro puerto, exporta `NEUROX_PORT=<puerto>` antes de instalar o ejecutar el hook.

## Rendimiento

| Operacion | Latencia | Notas |
|---|---|---|
| `save` | <1ms | Insert SQLite + indice FTS5 + extraccion temporal |
| `recall` (FTS) | <5ms | Ranking BM25 con scoring temporal |
| `recall` (hibrido) | <50ms | FTS + semantico + boost cross-signal |
| `context` | <10ms | Retrieval proactivo multi-senal |
| `consolidate` | <1s | Ciclo completo para 1000 observaciones |
| Tamano binario | ~15MB | Ejecutable unico, pero enlaza dinamicamente con libc/libm por SQLite/CGO |
| Memoria | <150MB | Con 10k observaciones + embeddings |

## Tecnologia

- **Go 1.23** — binario unico, goroutines para consolidacion background
- **SQLite 3** — modo WAL, busqueda full-text FTS5, via mattn/go-sqlite3 (CGO)
- **Embeddings** — Ollama (nomic-embed-text, 768d) o cualquier API compatible OpenAI
- **LLM** — Ollama o compatible OpenAI (opcional, para quality gate + reflexion + facts)
- **MCP** — Model Context Protocol via mark3labs/mcp-go
- **IDs** — ULID (monotonico, sorteable) via oklog/ulid

## Licencia

MIT
