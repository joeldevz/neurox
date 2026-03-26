# Plan: Windows Support

## Goal

Add Windows as a supported platform for neurox — prebuilt binaries in CI, a PowerShell installer, and self-update support. Users on Windows should be able to install and use neurox with the same experience as Linux/macOS.

## Business Context

- **Users**: Developers on Windows who use AI coding agents (Claude Code, Cursor, VS Code)
- **Problem**: There is no Windows binary in CI releases, no Windows installer, and `install.sh` is bash-only
- **Expected outcome**: `neurox.exe` available in GitHub Releases, installable via a one-liner PowerShell command

## Technical Context

### Current state
- **CI**: `.github/workflows/release.yml` has 2 jobs: `release-linux` (amd64, arm64) and `release-macos` (amd64, arm64). No Windows job.
- **Installer**: `install.sh` is bash-only. Line 34-37 explicitly rejects non-linux/darwin.
- **Self-update**: `internal/installer/updater.go` has a Windows guard at line 196 (`runtime.GOOS == "windows"` → error). Asset matching at line 61 hardcodes `.tar.gz`. `extractBinary` at line 140 only handles tar.gz. `DownloadAndReplace` at line 80 uses direct `os.Rename` (fails on Windows when binary is locked).
- **Config paths**: Already uses `~/.config/neurox` on all platforms — no changes needed.
- **Binary naming**: `neurox_{version}_{os}_{arch}.tar.gz`. Windows will use `neurox_{version}_windows_amd64.zip` with `neurox.exe` inside.

## Implementation Steps

### Step 1: Add Windows build job to CI release
- **What**: Add a `release-windows` job to `.github/workflows/release.yml`. Insert it after the existing `release-macos` job. The exact YAML to append:
  ```yaml
  
    release-windows:
      runs-on: windows-latest
      steps:
        - uses: actions/checkout@v4
          with:
            fetch-depth: 0

        - uses: actions/setup-go@v5
          with:
            go-version: "1.23"

        - name: Build windows/amd64
          shell: bash
          run: |
            VERSION="${GITHUB_REF_NAME#v}"
            CGO_ENABLED=1 GOARCH=amd64 GOOS=windows \
              go build -tags fts5 -ldflags "-s -w -X main.version=$VERSION" \
              -o dist/neurox.exe .

        - name: Package as zip
          shell: pwsh
          run: |
            $version = "${{ github.ref_name }}".TrimStart("v")
            Compress-Archive -Path dist/neurox.exe -DestinationPath "dist/neurox_${version}_windows_amd64.zip"

        - name: Upload windows artifact to release
          uses: softprops/action-gh-release@v2
          with:
            files: dist/*.zip
  ```
- **Why**: Without a prebuilt binary, Windows users can't install neurox
- **Where**: `.github/workflows/release.yml` — append after the `release-macos` job (after line 82)
- **Acceptance**:
  - File has 3 jobs: `release-linux`, `release-macos`, `release-windows`
  - Windows job uses `windows-latest`, builds with `-tags fts5`, packages as `.zip`
  - `go vet ./...` passes (no Go changes)
- **Status**: [x] done

### Step 2: Create PowerShell installer (`install.ps1`)
- **What**: Create `install.ps1` in the project root. Full script content:
  ```powershell
  #Requires -Version 5.1
  <#
  .SYNOPSIS
      Installs neurox on Windows.
  .DESCRIPTION
      Downloads the latest neurox release from GitHub and installs it.
  .PARAMETER Version
      Version to install (default: latest). Example: v0.1.11
  .PARAMETER InstallDir
      Installation directory (default: $env:LOCALAPPDATA\neurox)
  .EXAMPLE
      irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
  #>
  param(
      [string]$Version = "latest",
      [string]$InstallDir = "$env:LOCALAPPDATA\neurox"
  )
  
  $ErrorActionPreference = "Stop"
  $Repo = "joeldevz/neurox"
  $Binary = "neurox.exe"
  
  # Resolve latest version
  if ($Version -eq "latest") {
      Write-Host "Fetching latest version..."
      $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "neurox-installer" }
      $Version = $release.tag_name
      if (-not $Version) {
          Write-Error "Could not resolve latest version."
          exit 1
      }
  }
  
  $VersionNum = $Version.TrimStart("v")
  Write-Host "Installing neurox $Version (windows/amd64) -> $InstallDir"
  
  # Download
  $ZipName = "neurox_${VersionNum}_windows_amd64.zip"
  $Url = "https://github.com/$Repo/releases/download/$Version/$ZipName"
  $TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "neurox-install"
  
  if (Test-Path $TmpDir) { Remove-Item -Recurse -Force $TmpDir }
  New-Item -ItemType Directory -Force -Path $TmpDir | Out-Null
  
  $ZipPath = Join-Path $TmpDir $ZipName
  Write-Host "Downloading $Url..."
  try {
      Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing
  } catch {
      Write-Error "Download failed: $Url"
      exit 1
  }
  
  # Extract
  Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force
  $ExtractedBin = Join-Path $TmpDir $Binary
  if (-not (Test-Path $ExtractedBin)) {
      Write-Error "neurox.exe not found in archive."
      exit 1
  }
  
  # Install
  if (-not (Test-Path $InstallDir)) {
      New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  }
  Copy-Item -Path $ExtractedBin -Destination (Join-Path $InstallDir $Binary) -Force
  
  # Clean up
  Remove-Item -Recurse -Force $TmpDir
  
  # Add to PATH if needed
  $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
  if ($UserPath -notlike "*$InstallDir*") {
      [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$UserPath", "User")
      Write-Host "Added $InstallDir to your PATH. Restart your terminal for it to take effect."
  }
  
  Write-Host ""
  Write-Host "neurox $Version installed to $InstallDir\$Binary" -ForegroundColor Green
  Write-Host ""
  Write-Host "Next: configure your AI clients"
  Write-Host "  neurox install"
  Write-Host ""
  ```
- **Why**: Windows users need a native installer. `curl | bash` doesn't work on Windows.
- **Where**: `install.ps1` (new file in project root)
- **Acceptance**:
  - File exists at project root as `install.ps1`
  - PowerShell syntax is valid (no syntax errors)
  - `go vet ./...` passes (no Go changes)
- **Status**: [x] done

### Step 3: Update self-update to support Windows
- **What**: Modify `internal/installer/updater.go` with these specific changes:

  **3a. Add `archive/zip` to imports** (line 3-15):
  ```go
  import (
      "archive/tar"
      "archive/zip"
      "compress/gzip"
      ...
  )
  ```

  **3b. Fix `CheckLatest` asset name** (line 61). Replace:
  ```go
  expectedName := fmt.Sprintf("neurox_%s_%s_%s.tar.gz", latestVersion, runtime.GOOS, runtime.GOARCH)
  ```
  With:
  ```go
  ext := "tar.gz"
  if runtime.GOOS == "windows" {
      ext = "zip"
  }
  expectedName := fmt.Sprintf("neurox_%s_%s_%s.%s", latestVersion, runtime.GOOS, runtime.GOARCH, ext)
  ```

  **3c. Update `DownloadAndReplace`** (line 80-111). Replace the entire function with:
  ```go
  func DownloadAndReplace(downloadURL, binaryPath string) error {
      isZip := strings.HasSuffix(downloadURL, ".zip")
  
      var archivePath string
      if isZip {
          archivePath = binaryPath + ".tmp.zip"
      } else {
          archivePath = binaryPath + ".tmp.tar.gz"
      }
      tmpBin := binaryPath + ".tmp"
      oldBin := binaryPath + ".old"
  
      defer func() {
          os.Remove(archivePath)
          os.Remove(tmpBin)
          os.Remove(oldBin) // clean up .old from previous run
      }()
  
      if err := downloadFile(downloadURL, archivePath); err != nil {
          return fmt.Errorf("download: %w", err)
      }
  
      if isZip {
          if err := extractBinaryFromZip(archivePath, tmpBin); err != nil {
              return fmt.Errorf("extract zip: %w", err)
          }
      } else {
          if err := extractBinary(archivePath, tmpBin); err != nil {
              return fmt.Errorf("extract tar.gz: %w", err)
          }
      }
  
      if err := os.Chmod(tmpBin, 0o755); err != nil {
          return fmt.Errorf("chmod: %w", err)
      }
  
      // On Windows the running binary is locked, so rename current → .old first.
      if runtime.GOOS == "windows" {
          _ = os.Remove(oldBin)
          if err := os.Rename(binaryPath, oldBin); err != nil {
              return fmt.Errorf("rename current binary: %w", err)
          }
      }
  
      if err := os.Rename(tmpBin, binaryPath); err != nil {
          return fmt.Errorf("replace binary: %w", err)
      }
  
      return nil
  }
  ```

  **3d. Add `extractBinaryFromZip` function** (after `extractBinary`, around line 186):
  ```go
  // extractBinaryFromZip opens a .zip file, finds the neurox binary, and writes it to dst.
  func extractBinaryFromZip(zipPath, dst string) error {
      r, err := zip.OpenReader(zipPath)
      if err != nil {
          return fmt.Errorf("open zip: %w", err)
      }
      defer r.Close()
  
      binaryName := "neurox"
      if runtime.GOOS == "windows" {
          binaryName = "neurox.exe"
      }
  
      for _, f := range r.File {
          if filepath.Base(f.Name) != binaryName {
              continue
          }
  
          rc, err := f.Open()
          if err != nil {
              return fmt.Errorf("open entry: %w", err)
          }
          defer rc.Close()
  
          out, err := os.Create(dst)
          if err != nil {
              return err
          }
          if _, err := io.Copy(out, rc); err != nil {
              out.Close()
              return err
          }
          out.Close()
          return nil
      }
  
      return fmt.Errorf("%s not found in zip", binaryName)
  }
  ```

  **3e. Update `extractBinary` to also match `neurox.exe`** (line 165). Replace:
  ```go
  if filepath.Base(hdr.Name) != "neurox" {
  ```
  With:
  ```go
  baseName := filepath.Base(hdr.Name)
  if baseName != "neurox" && baseName != "neurox.exe" {
  ```

  **3f. Remove the Windows guard in `RunUpdate`** (line 195-200). Delete these lines:
  ```go
  // Windows is not supported (no CI release artifacts).
  if runtime.GOOS == "windows" {
      return fmt.Errorf(
          "neurox update is not supported on Windows — download the latest release manually from https://github.com/joeldevz/neurox/releases",
      )
  }
  ```

- **Why**: `neurox update` should work on Windows now that CI produces Windows binaries
- **Where**: `internal/installer/updater.go`
- **Acceptance**:
  - `CGO_ENABLED=1 go build -tags fts5 ./...` passes
  - `go vet ./...` passes
  - No Windows guard in `RunUpdate` anymore
  - `CheckLatest` returns `.zip` asset name on Windows, `.tar.gz` on others
  - `DownloadAndReplace` handles both `.zip` and `.tar.gz`
  - `extractBinaryFromZip` extracts `neurox.exe` from zip
  - Existing `extractBinary` also matches `neurox.exe` for tar.gz
  - Windows rename-to-old pattern used when `runtime.GOOS == "windows"`
- **Status**: [x] done

### Step 4: Update READMEs with Windows install instructions
- **What**: In both `README.md` and `README.es.md`, update the install section.

  **In `README.md`** — Replace the current "30-second install" section (lines 39-48):
  ```markdown
  ### 30-second install

  **Linux / macOS:**
  ```bash
  curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
  ```

  **Windows (PowerShell):**
  ```powershell
  irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
  ```

  Then configure your AI clients:
  ```bash
  neurox install
  ```
  ```

  **In `README.es.md`** — Find the equivalent install section and add the same structure but in Spanish:
  ```markdown
  ### Instalacion en 30 segundos

  **Linux / macOS:**
  ```bash
  curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
  ```

  **Windows (PowerShell):**
  ```powershell
  irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
  ```

  Luego configura tus clientes de IA:
  ```bash
  neurox install
  ```
  ```

- **Why**: Users need to know Windows is supported and how to install
- **Where**: `README.md` (lines 39-48), `README.es.md` (find equivalent section)
- **Acceptance**:
  - Both READMEs show Linux/macOS and Windows install commands
  - PowerShell command syntax is correct
  - No other content in the READMEs is changed
- **Status**: [x] done

### Step 5: Verify — build, vet, tests
- **What**: Run full project verification:
  ```bash
  CGO_ENABLED=1 go build -tags fts5 ./...
  go vet ./...
  CGO_ENABLED=1 go test -tags fts5 ./...
  ```
- **Why**: Ensure the updater.go changes don't break anything
- **Where**: Terminal
- **Acceptance**:
  - Build passes
  - Vet clean
  - All existing tests pass, zero regressions
- **Status**: [x] done

## Verification

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
go vet ./...
CGO_ENABLED=1 go test -tags fts5 ./...
```

## Risks / Notes

- **Windows arm64**: Not supported. Can add later if users request.
- **CGO on Windows**: `windows-latest` GitHub runner has MinGW pre-installed. If the build fails due to gcc issues, use `shell: bash` with explicit `CC=gcc` env var.
- **Binary locking**: Windows locks running executables. The rename-to-old pattern handles this. If unreliable, fall back to asking the user to close neurox first.
- **PATH persistence**: `[Environment]::SetEnvironmentVariable("PATH", ..., "User")` is permanent but requires a new terminal session. The installer prints this clearly.
- **Config directory**: `~/.config/neurox` already works on Windows — Go resolves `~` to `%USERPROFILE%`. No changes needed.
