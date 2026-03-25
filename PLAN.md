# Plan: Soporte Claude Desktop en el instalador

## Goal

Agregar soporte para **Claude Desktop** (la app GUI de escritorio de Anthropic) al instalador interactivo `neurox install`. Actualmente el instalador solo configura Claude Code (`~/.claude.json`). Claude Desktop usa una ruta completamente diferente y necesita su propia función de upsert y toggle en el wizard.

## Business Context

- **Usuario objetivo:** Desarrolladores que usan Claude Desktop (no solo Claude Code CLI)
- **Outcome:** Después de correr `neurox install` y seleccionar "Claude Desktop", el MCP de Neurox aparece disponible en la app de escritorio al reiniciarla
- **Regla de negocio:** La instalación es no destructiva — hace upsert del bloque `"neurox"` dentro de `mcpServers` sin tocar el resto del JSON
- **Plataformas soportadas por Claude Desktop** (confirmado en claude.ai/download — "Not available for Linux"):
  - macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
  - Windows: `%APPDATA%\Claude\claude_desktop_config.json`
  - Linux: **Claude Desktop no existe** para Linux. En Linux, `ClaudeDesktopConfig` queda vacío (`""`) y el toggle no aparece en el wizard.
- **Formato JSON requerido** (confirmado con la documentación oficial de MCP):
  ```json
  {
    "mcpServers": {
      "neurox": {
        "command": "neurox",
        "args": ["mcp"]
      }
    }
  }
  ```
  > Nota: A diferencia de Claude Code (que usa `"type": "stdio"`), Claude Desktop **no** requiere el campo `type`.

## Technical Context

**Archivos relevantes:**
- `internal/installer/installer.go` — todo el instalador vive aquí (1263 líneas)

**Estructura actual:**
- `Environment.ClaudeConfigPath` → `~/.claude.json` (Claude Code)
- `state.ConfigureClaude` + toggle `"claude"` → controla Claude Code
- `upsertClaudeConfig()` → escribe en `~/.claude.json`
- `installClaudeSkill()` → copia `SKILL.md` a `~/.claude/skills/neurox/`

**Lo que falta:**
1. Campo `ClaudeDesktopConfig` en `Environment` con path platform-aware
2. Campo `ConfigureClaudeDesktop` en `state`
3. Toggle `"claude_desktop"` en `stepIntegrations`
4. Función `upsertClaudeDesktopConfig()` — misma lógica que `upsertClaudeConfig` pero sin el campo `type`
5. Llamada en `executeInstall()` cuando `ConfigureClaudeDesktop == true`

**Detección del path (platform-aware):**
- `darwin` (macOS): `filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")`
- `windows`: `filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")`
- cualquier otro OS (linux, etc.): `""` — campo vacío, Claude Desktop no existe en esa plataforma

**Consecuencia de path vacío:** Si `ClaudeDesktopConfig == ""`, el toggle de Claude Desktop **no se muestra** en el wizard (igual que `install_hook` se oculta cuando `GitRoot == ""`). Así el instalador es correcto en Linux sin mostrar opciones que no aplican.

**Patrón de detección:** usar `runtime.GOOS` (stdlib de Go). No requiere modificar `go.mod` ni vendor.

---

## Implementation Steps

### Step 1: Detectar la ruta de Claude Desktop en `detectEnvironment`
- **What**: Agregar el campo `ClaudeDesktopConfig string` a `Environment` y llenarlo con la ruta correcta según la plataforma usando `runtime.GOOS`:
  ```go
  func claudeDesktopConfigPath(homeDir string) string {
      switch runtime.GOOS {
      case "darwin":
          return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
      case "windows":
          appData := os.Getenv("APPDATA")
          if appData == "" {
              return ""
          }
          return filepath.Join(appData, "Claude", "claude_desktop_config.json")
      default:
          // Linux y otros: Claude Desktop no existe oficialmente
          return ""
      }
  }
  ```
  Llamar esta función desde `detectEnvironment()` y asignar a `env.ClaudeDesktopConfig`.
- **Why**: Sin este campo, no hay manera de saber dónde escribir la config de Claude Desktop en cada OS. En Linux/otros, retornar `""` es la respuesta correcta — no existe un path válido.
- **Where**: `internal/installer/installer.go` — struct `Environment` + función `detectEnvironment()` + nueva helper `claudeDesktopConfigPath()`
- **Acceptance**:
  - En macOS: `env.ClaudeDesktopConfig == "~/Library/Application Support/Claude/claude_desktop_config.json"` (con home expandido)
  - En Windows: `env.ClaudeDesktopConfig == "%APPDATA%\Claude\claude_desktop_config.json"` (con APPDATA expandido)
  - En Linux: `env.ClaudeDesktopConfig == ""` — campo vacío, sin fallback falso
  - Compila sin errores con `go build -tags fts5 ./...`
- **Status**: [x] done

### Step 2: Agregar toggle y estado para Claude Desktop
- **What**:
  1. Agregar `ConfigureClaudeDesktop bool` al struct `state`
  2. Inicializarlo a `false` en `newModel()` (opt-in, igual que Claude Code)
  3. En `currentFields()` → `stepIntegrations`, agregar el toggle **solo cuando** `m.env.ClaudeDesktopConfig != ""`:
     ```go
     if m.env.ClaudeDesktopConfig != "" {
         fields = append(fields, field{Type: fieldToggle, Key: "claude_desktop", Label: "Claude Desktop", Description: m.env.ClaudeDesktopConfig})
     }
     ```
     Esto hace que en Linux el toggle simplemente no aparezca, igual que `install_hook` se oculta cuando no hay git repo.
  4. Agregar case `"claude_desktop"` en `toggleCurrent()` y `toggleValue()`
  5. Agregar `"Claude Desktop"` en `shortIntegrations()` cuando `ConfigureClaudeDesktop == true`
- **Why**: En macOS/Windows el toggle aparece con la ruta real como descripción. En Linux no aparece porque el path está vacío — no tiene sentido mostrar una opción que no puede funcionar.
- **Where**: `internal/installer/installer.go` — structs `state`, funciones `newModel`, `currentFields`, `toggleCurrent`, `toggleValue`, `shortIntegrations`
- **Acceptance**:
  - En macOS/Windows: toggle visible en Integrations con la ruta como hint
  - En Linux: toggle invisible (misma mecánica que `install_hook` sin git)
  - El toggle se puede activar/desactivar con Space
  - La pantalla de Review muestra "Claude Desktop" cuando está activado
  - Compila sin errores
- **Status**: [x] done

### Step 3: Implementar `upsertClaudeDesktopConfig`
- **What**: Nueva función que escribe el bloque `neurox` en `mcpServers` del archivo `claude_desktop_config.json`:
  ```go
  func upsertClaudeDesktopConfig(path string, binaryPath string) error {
      var cfg map[string]any
      if err := readJSONFile(path, &cfg); err != nil {
          return err
      }
      if cfg == nil {
          cfg = map[string]any{}
      }
      servers := ensureObject(cfg, "mcpServers")
      // Claude Desktop NO usa "type": "stdio" — solo command + args
      servers["neurox"] = map[string]any{
          "command": "neurox",
          "args":    []string{"mcp"},
      }
      return writeJSONFile(path, cfg)
  }
  ```
  Reutiliza `readJSONFile`, `writeJSONFile`, y `ensureObject` que ya existen.
- **Why**: Claude Desktop no acepta el campo `"type"` en su config (distinto a Claude Code que sí lo usa). Necesita su propia función para que el JSON sea correcto.
- **Where**: `internal/installer/installer.go` — nueva función al final del archivo
- **Acceptance**:
  - Si el archivo no existe, lo crea con el bloque correcto
  - Si el archivo existe con otros servidores, los preserva y agrega/sobreescribe solo `"neurox"`
  - El JSON generado NO contiene el campo `"type"`
  - Compila sin errores
- **Status**: [x] done

### Step 4: Llamar a `upsertClaudeDesktopConfig` en `executeInstall`
- **What**: En `executeInstall()`, agregar el bloque condicional después del bloque de Claude Code:
  ```go
  if s.ConfigureClaudeDesktop {
      if err := upsertClaudeDesktopConfig(env.ClaudeDesktopConfig, binaryPath); err != nil {
          result.Warnings = append(result.Warnings, fmt.Sprintf("Claude Desktop config: %v", err))
      } else {
          result.Updated = append(result.Updated, env.ClaudeDesktopConfig)
      }
  }
  ```
- **Why**: Sin esta llamada, el toggle existe pero no hace nada
- **Where**: `internal/installer/installer.go` — función `executeInstall()`
- **Acceptance**:
  - Cuando `ConfigureClaudeDesktop == true`, el archivo `claude_desktop_config.json` es creado/actualizado
  - La ruta aparece en `result.Updated` en la pantalla de Done
  - Si falla (permiso, etc.), aparece en `result.Warnings` sin abortar el resto de la instalación
  - Compila y pasa `go build -tags fts5 ./...` + `go vet ./...`
- **Status**: [x] done

### Step 5: Verificación final
- **What**: Compilar el proyecto completo y correr los tests
- **Why**: Asegurarse de que no hay regresiones y que el binario compila correctamente
- **Where**: Raíz del proyecto
- **Acceptance**:
  - `CGO_ENABLED=1 go build -tags fts5 ./...` ✓ sin errores
  - `go vet ./...` ✓ sin warnings
  - `CGO_ENABLED=1 go test -tags fts5 ./...` ✓ todos los tests pasan
  - `neurox install` muestra "Claude Desktop" en el paso de integraciones
- **Status**: [x] done

---

## Verification

```bash
# Build completo
CGO_ENABLED=1 go build -tags fts5 ./...
go vet ./...

# Tests
CGO_ENABLED=1 go test -tags fts5 ./...

# Verificación manual: correr el wizard y activar Claude Desktop
neurox install
# → Paso Integrations → "Claude Desktop" aparece solo en macOS/Windows, NO en Linux
# → Activar con Space → Completar instalación

# Verificar en macOS:
cat ~/Library/Application\ Support/Claude/claude_desktop_config.json | python3 -m json.tool
# Debe tener: "neurox": {"command": "neurox", "args": ["mcp"]}
# NO debe tener: "type": "stdio"

# Verificar en Windows (PowerShell):
# Get-Content "$env:APPDATA\Claude\claude_desktop_config.json"

# En Linux: el toggle NO aparece en el wizard (comportamiento correcto, Claude Desktop no existe)
```

---

## Risks / Notes

- **Linux: Claude Desktop NO existe.** Fuente oficial confirmada: claude.ai/download dice explícitamente "Not available for Linux". No hay fallback, no hay ruta inventada. `ClaudeDesktopConfig` queda `""` en Linux y el toggle no se muestra. Esta es la respuesta correcta.
- **`"type": "stdio"` NO debe aparecer** en la config de Claude Desktop — la documentación oficial del MCP (modelcontextprotocol.io) solo muestra `command` + `args`. Claude Code sí usa `type`, por eso se necesita una función separada `upsertClaudeDesktopConfig`.
- **Windows APPDATA:** Si la variable de entorno `APPDATA` no está definida (raro pero posible), la función retorna `""` y el toggle queda oculto, evitando una escritura en un path incorrecto.
- **Scope acotado:** Este plan NO toca Claude Code (que ya funciona), NO modifica el skill de Claude Code, y NO cambia la lógica de detección de Ollama ni de providers. Solo agrega Claude Desktop como nuevo target de integración.
- **Import `runtime`:** Necesario para `runtime.GOOS`. Es stdlib de Go, no requiere modificar `go.mod` ni vendor.
