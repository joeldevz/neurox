# Plan: Dashboard Redesign — Fey-inspired Dark Premium UI

## Goal

Redesign completo del dashboard de Neurox (`GET /`) para conseguir una visual premium inspirada en Fey (app financiera): fondo dark, tipografía grande para KPIs, cards con bordes sutiles, charts elegantes, whitespace generoso, y una experiencia cohesiva entre las 4 tabs (Brain, Explorer, Graph, Health). También añadir datos nuevos: activity timeline (saves/recalls por día), recent observations, y mejorar la tab de Graph para que esté integrada visualmente con el nuevo diseño.

## Business Context

- **Problema**: el dashboard actual es funcional pero tiene aspecto de dev tool — cards pequeñas, texto apretado, poco breathing room. No transmite la calidad premium del producto.
- **Referencia visual**: Fey (Mobbin) — KPIs en una fila horizontal con label arriba y big number abajo, chart principal con línea fina sobre fondo dark, cards con bordes `rgba(255,255,255,0.08)` y border-radius 12-16px, tabs limpios, mucho whitespace.
- **Preferencias del usuario**: Christopher prefiere consistentemente dark UI, palette purple-blue + pink, estética premium tipo Vercel/OpenCode/Claude.
- **El brain SVG animado se mantiene** — es un diferenciador, pero se pule visualmente.
- **Scope**: redesign visual de los 4 tabs + 2 nuevos endpoints de datos + nuevas secciones (activity chart, recent observations). Todo en el HTML embebido single-file.

## Technical Context

- **Dashboard actual**: `internal/api/dashboard.html` (1112 líneas), embebido via `go:embed` en `dashboard.go`. 4 tabs: Brain, Explorer, Graph, Health.
- **Graph standalone**: `internal/graph/render.go` tiene su propio template HTML para `neurox graph` CLI. El dashboard usa el graph API con `?format=json` y renderiza con vis-network en el tab.
- **CDN dependencies**: vis-network 9.1.9, Chart.js 4. Añadiremos Inter font desde Google Fonts.
- **APIs existentes que usa el dashboard**:
  - `GET /api/v1/status` — aggregate stats
  - `GET /api/v1/stats/breakdown` — counts by type/layer/namespace/kind/relation
  - `GET /api/v1/observations/browse` — paginated list
  - `GET /api/v1/graph?format=json` — graph nodes/edges
  - `GET /api/v1/health-check` — health score + dimensions
  - `GET /api/v1/decay-timeline` — avg importance by layer per day
- **APIs que faltan** (necesarias para nuevas features):
  - `GET /api/v1/stats/activity` — saves/recalls per day (datos en `tool_calls` table)
  - `GET /api/v1/observations/browse?sort=recent` — soporte de sort cronológico
- **Constraint**: todo el dashboard es un single HTML file embebido — no hay build step, bundler, ni framework JS.

## Implementation Steps

### Step 1: Añadir endpoints de datos para las nuevas secciones
- **What**: Crear `GET /api/v1/stats/activity?days=30` que devuelve tool calls agrupados por día y tool_name (saves, recalls, etc.). Añadir parámetro `sort=recent` al endpoint `/api/v1/observations/browse` para ordenar por `created_at DESC`.
- **Why**: El dashboard necesita datos de actividad temporal y observaciones recientes cronológicas que no existen todavía.
- **Where**:
  - `internal/api/handlers.go` — nuevo handler `handleActivity`, modificar `handleBrowse`
  - `internal/api/server.go` — registrar nueva ruta
- **Acceptance**:
  - `GET /api/v1/stats/activity?days=30` devuelve `{ "days": [...], "series": { "save": [...], "recall": [...], ... } }`
  - `GET /api/v1/observations/browse?sort=recent&limit=10` devuelve las 10 observaciones más recientes
  - `CGO_ENABLED=1 go test -tags fts5 ./internal/api/...`
  - `CGO_ENABLED=1 go build -tags fts5 ./...`
- **Status**: [x] done

### Step 2: Redesign del Brain Tab — hero visual con KPI row y activity chart
- **What**: Rediseñar el Brain tab con:
  1. Header refinado con logo más premium, tabs estilo Fey (pill-shaped o underline sutil), y provider tags más elegantes
  2. Brain SVG mantenido pero con glow más refinado y tipografía más grande dentro de los anillos
  3. KPI row estilo Fey: 9 métricas en cards horizontales (Total, Core, Working, Buffer, Links, Facts, Health Score, Sessions, Stale) con label arriba en texto dim y valor grande abajo
  4. Activity chart nuevo debajo de KPIs: Chart.js area chart mostrando saves/recalls por día (últimos 30 días) con la estética de Fey (línea fina blanca, area fill sutil, dark background)
  5. "Recent observations" card tipo "News summary" de Fey: últimas 5 observaciones con title, type badge, y timestamp
- **Why**: El Brain tab es la primera impresión. Debe transmitir la calidad del producto y dar un overview completo del brain health.
- **Where**:
  - `internal/api/dashboard.html` — CSS variables, header, brain-tab HTML + JS
- **Acceptance**:
  - El Brain tab muestra el SVG animado, KPI row, activity chart, y recent observations
  - Los KPIs se actualizan cada 5 segundos vía polling
  - El activity chart se carga con datos del nuevo endpoint
  - Visual cohesiva con palette dark, borders sutiles, tipografía Inter, border-radius 12-16px
  - Responsive: se ve bien en 1280px+ (no necesita ser mobile)
- **Status**: [x] done

### Step 3: Redesign del Explorer Tab — layout refinado con glass cards
- **What**: Rediseñar el Explorer con:
  1. Sidebar con categorías en cards más elegantes, icons por tipo, y contadores con tipografía tabular
  2. Observation cards más espaciados con hover effects sutiles, badges de tipo más refinados, importance bars más visual
  3. Detail panel con glass-morphism (backdrop-filter blur), tipografía más limpia, y mejor jerarquía visual
  4. Toolbar con filtros estilizados (selects con bordes sutiles, search input con icon)
- **Why**: El Explorer es donde el usuario pasa más tiempo explorando observaciones. Necesita sentirse premium sin perder funcionalidad.
- **Where**:
  - `internal/api/dashboard.html` — CSS del explorer + HTML restructure
- **Acceptance**:
  - Toda la funcionalidad existente sigue intacta (filtros, browse, detail panel, load more)
  - Visual consistente con el nuevo Brain tab
  - Hover transitions suaves (150-200ms)
  - Scrollbar custom sutil
- **Status**: [x] done

### Step 4: Redesign del Graph Tab — integración visual completa
- **What**: Rediseñar el Graph tab para que sea cohesivo con el nuevo diseño:
  1. Sidebar flotante con glass-morphism, filtros estilizados, y legend refinada
  2. Stats overlay más elegante (top-right) con glass background
  3. Detail panel (bottom-right) con glass-morphism y tipografía consistente
  4. Botón "Load Graph" con estilo de los nuevos buttons
  5. Namespace/type selects poblados dinámicamente desde breakdown API
- **Why**: El Graph tab usa el mismo vis-network pero su chrome (sidebar, overlays, legend) debe ser consistente con el redesign.
- **Where**:
  - `internal/api/dashboard.html` — CSS y HTML del graph tab
- **Acceptance**:
  - Graph carga y funciona igual que antes (vis-network, filtros, click para detalle)
  - Chrome visual (sidebar, overlays) consistente con el diseño Fey
  - Glass-morphism en todos los overlays flotantes
- **Status**: [x] done

### Step 5: Redesign del Health Tab — score card premium y charts refinados
- **What**: Rediseñar el Health tab:
  1. Score card grande tipo "hero metric" con el número gigante, grade badge, y summary — estilo similar al "$329.28" de Fey
  2. Top actions como pills/cards en vez de lista plana
  3. Dimension breakdown con progress bars más anchas, colores por status, y tooltips para recommendations
  4. Memory Layer Funnel con bars más anchas y visuales
  5. Decay timeline chart con la estética refinada de Chart.js (grid lines sutiles, colores consistentes con el palette)
- **Why**: El Health tab es la vista analítica principal. Debe sentirse como un dashboard financiero premium.
- **Where**:
  - `internal/api/dashboard.html` — CSS y HTML del health tab
- **Acceptance**:
  - Score card prominente con animación de entrada
  - Dimension bars y funnel visualmente mejorados
  - Chart con grid sutil y colores consistentes
  - Todo funcional con datos del health-check API existente
- **Status**: [x] done

### Step 6: Polish final — transiciones, loading states, y cohesión
- **What**:
  1. Loading states para tabs que cargan datos (skeleton screens o spinners sutiles)
  2. Transiciones de entrada para cards (fade-in staggered)
  3. Verificar que los 4 tabs tienen una experiencia cohesiva
  4. Asegurar que el header, tabs, y footer (si existe) son consistentes
  5. Verificar que el graph standalone template (`internal/graph/render.go`) mantiene su visual (es independiente del dashboard pero debería ser consistente en palette)
- **Why**: El polish marca la diferencia entre "funcional" y "premium".
- **Where**:
  - `internal/api/dashboard.html` — CSS animations, JS loading states
  - `internal/graph/render.go` — actualizar palette si hay divergencia
- **Acceptance**:
  - No hay flash of unstyled content ni jumps al cargar tabs
  - Transiciones suaves entre tabs
  - No hay errores en la consola del browser
  - `CGO_ENABLED=1 go build -tags fts5 ./...`
  - `CGO_ENABLED=1 go vet -tags fts5 ./...`
  - `CGO_ENABLED=1 go test -tags fts5 ./...`
- **Status**: [x] done

## Verification

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

Verificación manual:
1. Abrir `http://localhost:7438` y verificar cada tab
2. Brain tab: SVG animado visible, KPIs con datos reales, activity chart con datos, recent observations
3. Explorer tab: filtros funcionales, browse/paginate, detail panel
4. Graph tab: Load Graph funciona, filtros, click para detalle
5. Health tab: score card con datos, dimensions, funnel, decay chart
6. Verificar que no hay errores en la consola del browser
7. Verificar en viewport 1280px+

## Risks / Notes

- **Single HTML file**: todo el dashboard es un archivo embebido. Con ~1100 líneas actuales, podría crecer a ~1800-2200. Es manejable pero hay que mantener el CSS organizado con secciones comentadas.
- **No build step**: sin bundler ni framework. Todo es vanilla HTML/CSS/JS con CDN para Chart.js, vis-network, e Inter font. Esto es intencional y se mantiene.
- **Graph standalone**: `internal/graph/render.go` tiene su propio template HTML. No se modifica estructuralmente, solo se alinea la palette si diverge.
- **Datos de actividad**: depende de que la tabla `tool_calls` tenga datos. Si no hay historial, el activity chart mostrará un mensaje "Not enough data" como ya hace el decay chart.
- **No mobile**: el dashboard está pensado para desktop (1280px+). No se invertirá en responsive mobile.
- **Inter font**: se carga desde Google Fonts CDN. Si no hay conectividad, fallback a system fonts (ya definido en font-family).
