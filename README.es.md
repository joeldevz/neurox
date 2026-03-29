<p align="center">
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>Memoria persistente para agentes de IA</strong>
  </p>
  <p align="center">
    Un binario Go &bull; Un archivo SQLite &bull; Cero dependencias externas
  </p>
  <p align="center">
    <a href="#inicio-rapido">Inicio Rapido</a> &bull;
    <a href="#como-funciona">Como Funciona</a> &bull;
    <a href="#resultados-del-benchmark">98% Recall</a> &bull;
    <a href="#documentacion">Docs</a> &bull;
    <a href="README.md">Read in English</a>
  </p>
</p>

---

Tu agente de IA olvida todo entre sesiones. Cada conversacion empieza desde cero — sin memoria de las decisiones de arquitectura de la semana pasada, el bug que arreglaste ayer, o tu preferencia por tabs sobre espacios.

Neurox le da a tu agente memoria persistente y estructurada.

```bash
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
neurox setup claude-code    # o: opencode, cursor, vscode, antigravity, claude-desktop
```

Eso es todo. Sin Node.js, sin Python, sin Docker, sin compilador C. Binario Go puro, un solo archivo SQLite.

---

## Que Recuerda

Tu agente guarda observaciones mientras trabaja — decisiones, bugs, patrones, preferencias — y las recupera cuando son relevantes.

```
Agente: "Decidimos usar SQLite en vez de PostgreSQL para deploy single-file"
  → Neurox lo guarda como tipo: decision, lo vincula a schema.sql
  → Parsea "en vez de PostgreSQL" como un knowledge update
  → Tres meses despues, el agente pregunta "que base de datos usamos?"
  → Devuelve la decision de SQLite primero, PostgreSQL como historial
```

**Nada esta oculto.** Cada observacion es una fila en SQLite. Puedes consultarla directamente, exportarla, borrarla, o inspeccionar como fue puntuada.

| Feature | Store simple | Neurox |
|---|---|---|
| Guardar y recuperar texto | Si | Si |
| Busqueda full-text (FTS5) | Quizas | Integrado |
| Entiende tiempo ("la semana pasada", "actualmente") | No | [Razonamiento temporal](docs/concepts.md#temporal-intent) |
| Sabe cuando cambian los hechos | No | Knowledge updates — los hechos viejos se vuelven historial, no ruido |
| Vincula memorias a archivos fuente | No | Integracion Git — marca stale automaticamente cuando cambian archivos |
| Explica por que un resultado quedo primero | No | [Modo debug](docs/concepts.md#memory-debugging) — desglose completo de score |
| Rastrea de donde vienen las memorias | No | [Provenance](docs/concepts.md#provenance) — que tool, sesion y superficie |
| Funciona sin ningun servicio externo | — | Si — LLM y embeddings son [mejoras opcionales](#degradacion-graceful) |

---

## Inicio Rapido

### Instalar

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Windows
irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex

# Compilar desde source (sin compilador C — driver SQLite puro en Go)
go build -o neurox .
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

### Ejecutar

```bash
neurox mcp     # Servidor MCP (stdio) — para Claude Code, Cursor, OpenCode, etc.
neurox serve   # API HTTP + dashboard web en localhost:7438
```

El dashboard web tiene cuatro tabs: **Brain** (stats y actividad), **Explorer** (navegar y buscar observaciones), **Graph** (visualizacion interactiva force-directed), y **Health** (brain power score con recomendaciones).

### Integracion Git

```bash
neurox install-hook   # hook post-commit — marca observaciones vinculadas como stale cuando cambian archivos
```

---

## Como Funciona

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

El decay reduce la *accesibilidad* (que tan facil es encontrar algo), no el *valor* (si existe o no). Una decision de hace seis meses permanece en Core — solo se vuelve menos prominente a menos que se acceda. Nada se elimina sin accion explicita.

### Scoring

```
Score = (Recencia x 0.3) + (Importancia x 0.3) + (Relevancia x 0.4)
        x Multiplicador temporal (0.7x – 1.5x segun intent temporal)
        x Boost cross-signal (1.2x cuando FTS y semantico coinciden)
```

### Degradacion Graceful

Neurox funciona sin ningun servicio externo. Las features se activan segun lo que este disponible:

| Disponible | Features habilitadas |
|---|---|
| **Nada** (default) | Busqueda FTS5, parsing temporal, decay, promocion |
| + Embeddings (Ollama o remoto) | Busqueda hibrida, dedup semantico, deteccion de contradicciones |
| + LLM (Ollama o remoto) | Quality gate, extraccion de facts, reflexion |
| + Curator LLM (remoto) | Curacion profunda con recalibracion de importancia |

La configuracion base — cero dependencias — ya entrega [98% de recall](#resultados-del-benchmark).

### Consolidacion

Un pipeline en background se ejecuta cada 30 minutos: decay → promover → dedup → contradiccion → reflexion → eviccion. Cada etapa es determinista y auditable. Ver [docs/concepts.md](docs/concepts.md#consolidation-epoch) para el pipeline completo.

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

FTS5 + BM25 + scoring temporal. Sin LLM. Reproducible en ~2 minutos.

Un [Brain Benchmark](docs/concepts.md#brain-benchmark) autocontenido (12 dimensiones, 3 categorias) tambien esta incluido: `neurox benchmark`.

---

## Paridad de Superficies

Neurox expone tres superficies de acceso. Las operaciones centrales de memoria — `save`, `recall`, `context` y `session` — usan el **mismo pipeline compartido** en las tres, garantizando calidad, provenance y hooks identicos sin importar como te conectes.

| Capacidad | CLI | MCP | HTTP |
|---|:---:|:---:|:---:|
| **save** (pipeline compartido, provenance, facts, embeddings) | ✓ | ✓ | ✓ |
| **recall** (FTS5 + semantico + intent temporal + provenance) | ✓ | ✓ | ✓ |
| **context** (recuperacion proactiva + reflexiones) | ✓ | ✓ | ✓ |
| **session_start / session_end** (extraccion de observaciones) | ✓ | ✓ | ✓ |
| **update** | — | ✓ | ✓ |
| **forget** (soft-delete) | — | ✓ | ✓ |
| **invalidate** (+ reemplazo) | ✓ | ✓ | ✓ |
| **status** | ✓ | ✓ | ✓ |
| **git_hook** | — | ✓ | ✓ |
| **reflect** | — | ✓ | ✓ |
| **consolidate** | ✓ | ✓ | ✓ |
| **health_check** | — | ✓ | ✓ |
| **curate** | ✓ | ✓ | ✓ |
| **backup** | ✓ | ✓ | ✓ |
| **audit** (ciclo de vida completo de observacion) | ✓ | — | — |
| **graph** (visualizacion interactiva) | ✓ | — | ✓ |
| **benchmark** | ✓ | — | — |
| **export / import** | ✓ | — | — |
| **reembed** | ✓ | — | — |
| **Dashboard web** (Brain, Explorer, Graph, Health) | — | — | ✓ |

**Modelo de concurrencia:** MCP y HTTP usan un `SaveQueue` asincrono con workers en background. CLI usa el mismo pipeline de forma sincrona (el proceso termina despues de cada comando). Los quality gates, extraccion de facts y hooks de embedding son identicos en todos los casos.

### Tools MCP

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
| `backup` | Backup seguro de la base de datos mientras el servidor corre |

Inputs y parametros completos: [docs/reference.md](docs/reference.md#mcp-tool-inputs)

---

## Documentacion

| Tema | Link |
|---|---|
| **Quickstart** | [docs/quickstart.md](docs/quickstart.md) |
| **Conceptos y vocabulario** | [docs/concepts.md](docs/concepts.md) — capas de memoria, intent temporal, curvas de decay, knowledge graph, provenance, modo debug, brain power score |
| **Referencia completa** | [docs/reference.md](docs/reference.md) — comandos CLI, API REST, inputs de tools MCP, configuracion, variables de entorno, arquitectura |
| **Claude Code** | [docs/claude-code.md](docs/claude-code.md) |
| **Claude Desktop** | [docs/claude-desktop.md](docs/claude-desktop.md) |
| **Cursor** | [docs/cursor.md](docs/cursor.md) |
| **VS Code** | [docs/vscode.md](docs/vscode.md) |
| **OpenCode** | [docs/opencode.md](docs/opencode.md) |

---

## Tecnologia

- **Go 1.26+** — binario unico, goroutines para consolidacion background
- **SQLite 3** — modo WAL, busqueda full-text FTS5, via [ncruces/go-sqlite3](https://github.com/ncruces/go-sqlite3) (Go puro, sin CGO)
- **MCP** — Model Context Protocol via [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)
- **Embeddings** — Ollama o cualquier API compatible OpenAI (opcional)
- **LLM** — Ollama o compatible OpenAI (opcional)
- **IDs** — ULID (monotonico, sorteable) via [oklog/ulid](https://github.com/oklog/ulid)

## Licencia

[BSL 1.1](LICENSE) — Puedes usar, modificar y distribuir Neurox para cualquier proposito **excepto** ofrecerlo como servicio comercial hospedado que compita con el Licenciante. El **2030-03-28**, se convierte automaticamente a **Apache 2.0**.

Mismo modelo que [Sentry](https://blog.sentry.io/relicensing-sentry/), [CockroachDB](https://www.cockroachlabs.com/blog/oss-relicensing-cockroachdb/), [HashiCorp](https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license) y [MariaDB](https://mariadb.com/bsl-faq-adopting/).
