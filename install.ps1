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
