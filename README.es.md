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
- Working -> Core: accedido 5+ veces Y mayor a 7 dias
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

## Inicio Rapido

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
```

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
POST   /api/v1/sessions                     Iniciar sesion
PUT    /api/v1/sessions/{id}/end            Terminar sesion
POST   /api/v1/hooks/git                    Git hook
GET    /api/v1/graph                        Vista interactiva del grafo (o JSON con ?format=json)
POST   /api/v1/reflect                      Disparar reflexion
```

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
```

Los ejemplos de configuracion de agentes siguen lo que genera `install.sh`: Claude y Cursor usan `command` + `args`, mientras que OpenCode usa una entrada MCP local con `command` como array.

Variables de entorno con prefijo `NEUROX_`:

```bash
NEUROX_DATABASE_PATH=/ruta/custom/path.db
NEUROX_LLM_PROVIDER=ollama
NEUROX_LLM_GATE_MODE=auto
NEUROX_EMBED_PROVIDER=ollama
```

### Degradacion Graceful

Neurox funciona sin ningun servicio externo. Las features se activan segun lo que este disponible:

| Disponible | Features habilitadas |
|---|---|
| Nada | Busqueda FTS5, parsing temporal, promocion heuristica, decay |
| + Ollama embeddings | Busqueda hibrida, dedup semantico, deteccion de contradicciones |
| + Ollama LLM | Quality gate, extraccion de facts, reflexion, extraccion de sesion |
| + API remota | Lo mismo que arriba con provider en la nube |

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
|   +-- observation/           Tipos core, CRUD, extraccion temporal
|   +-- recall/                FTS5 + semantico + busqueda temporal-aware
|   +-- temporal/              Parser de fechas, almacenamiento de menciones
|   +-- facts/                 Tripletas de conocimiento, extraccion LLM
|   +-- consolidate/           Pipeline background (promover, dedup, evictar)
|   +-- contradiction/         Deteccion de conflictos + supersesion temporal
|   +-- decay/                 Curvas de Ebbinghaus, garbage collection
|   +-- reflect/               Sintesis de insights (patron Generative Agents)
|   +-- session/               Ciclo de vida de sesiones, extraccion LLM
|   +-- proactive/             Retrieval de contexto sin queries explicitas
|   +-- embed/                 Embeddings Ollama + compatible OpenAI
|   +-- llm/                   Providers LLM, quality gate, sistema 3-strikes
|   +-- links/                 Relaciones entre observaciones (supersedes, contradicts)
|   +-- db/                    Schema SQLite, migraciones, modo WAL
|   +-- mcp/                   Servidor protocolo MCP
|   +-- api/                   Servidor HTTP REST + dashboard
|   +-- graph/                 Render HTML interactivo + queries del grafo
|   +-- config/                Carga de config YAML + env
|   +-- filelink/              Vinculacion archivo-observacion
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
