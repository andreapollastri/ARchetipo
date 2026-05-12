# ─── ARchetipo Installer ─────────────────────────────────────────────────────
# Installs ARchetipo skills + config for Claude Code, Codex, Gemini CLI, OpenCode, GitHub Copilot
# Usage: irm https://raw.githubusercontent.com/techreloaded-ar/ARchetipo/main/install.ps1 | iex
#        .\install.ps1 [-Local] [-Cleanup] [-Tool codex] [-Connector file] [-Yes] [-Help]
#   -Local    Installs from local .\skills\ folder instead of GitHub
#   -Cleanup  Removes installed skills from selected tools
#   -Tool     Installs or cleans up a single tool without prompts
#   -Connector Selects connector without prompts
#   -Yes      Accepts overwrite prompts automatically
#   -Help     Shows this help message
# ──────────────────────────────────────────────────────────────────────────────

param(
  [switch]$Local,
  [switch]$Cleanup,
  [string]$Tool,
  [ValidateSet("file", "github")]
  [string]$Connector,
  [switch]$Yes,
  [switch]$Help
)

$ErrorActionPreference = "Stop"

$RepoZip    = "https://github.com/techreloaded-ar/ARchetipo/archive/refs/heads/main.zip"
$SkillNames = @("archetipo-autopilot", "archetipo-design", "archetipo-implement", "archetipo-inception", "archetipo-plan", "archetipo-spec")
$ScriptDir  = Split-Path -Parent $MyInvocation.MyCommand.Path

# ─── Tool definitions ─────────────────────────────────────────────────────────
$Tools = @(
  @{ Name = "Claude Code";     Path = ".claude\skills" }
  @{ Name = "Codex";           Path = ".agents\skills" }
  @{ Name = "Gemini CLI";      Path = ".gemini\skills" }
  @{ Name = "OpenCode";        Path = ".opencode\skills" }
  @{ Name = "GitHub Copilot";  Path = ".github\skills" }
  @{ Name = "Pi";              Path = ".pi\skills" }
)

function Resolve-ToolIndex {
  param([string]$Raw)

  $normalized = if ($null -eq $Raw) { "" } else { $Raw.ToLowerInvariant().Replace(" ", "-") }
  switch ($normalized) {
    "claude" { return 0 }
    "claude-code" { return 0 }
    "codex" { return 1 }
    "gemini" { return 2 }
    "gemini-cli" { return 2 }
    "opencode" { return 3 }
    "open-code" { return 3 }
    "copilot" { return 4 }
    "github-copilot" { return 4 }
    "github" { return 4 }
    "pi" { return 5 }
    default {
      throw "Unsupported tool '$Raw'."
    }
  }
}

# ─── Install for a specific tool ──────────────────────────────────────────────
function Install-ForTool {
  param([int]$ToolIndex, [string]$SourceDir)

  $tool     = $Tools[$ToolIndex]
  $toolPath = $tool.Path

  foreach ($skillName in $SkillNames) {
    $src = Join-Path $SourceDir $skillName
    if (-not (Test-Path $toolPath)) {
      New-Item -ItemType Directory -Path $toolPath -Force | Out-Null
    }
    Copy-Item -Path $src -Destination $toolPath -Recurse -Force
  }

  Write-Host ""
  Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
  Write-Host "$($tool.Name)" -ForegroundColor White -NoNewline
  Write-Host " -> $toolPath" -ForegroundColor DarkGray
  foreach ($skillName in $SkillNames) {
    Write-Host "     $skillName/" -ForegroundColor DarkGray
  }
}

# ─── Cleanup for a specific tool ──────────────────────────────────────────────
function Remove-ForTool {
  param([int]$ToolIndex)

  $tool     = $Tools[$ToolIndex]
  $toolPath = $tool.Path
  $removed  = $false

  foreach ($skillName in $SkillNames) {
    $target = Join-Path $toolPath $skillName
    if (Test-Path $target) {
      Remove-Item -Path $target -Recurse -Force
      $removed = $true
    }
  }

  Write-Host ""
  if ($removed) {
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host "$($tool.Name)" -ForegroundColor White -NoNewline
    Write-Host " -> rimosso da $toolPath" -ForegroundColor DarkGray
  } else {
    Write-Host "  - " -ForegroundColor Yellow -NoNewline
    Write-Host "$($tool.Name)" -ForegroundColor White -NoNewline
    Write-Host " -> nessuna skill trovata in $toolPath" -ForegroundColor DarkGray
  }
}

# ─── Interactive multi-select menu ────────────────────────────────────────────
function Show-Menu {
  $selected  = @(0) * $Tools.Count
  $cursor    = 0
  $toolCount = $Tools.Count

  # Check if we have an interactive console
  $isInteractive = $true
  try {
    [Console]::CursorVisible = $false
  } catch {
    $isInteractive = $false
  }

  if (-not $isInteractive) {
    return Show-FallbackMenu
  }

  try {
    # Initial draw
    for ($i = 0; $i -lt $toolCount; $i++) {
      $checkbox = if ($selected[$i] -eq 1) { "[x]" } else { "[ ]" }
      $prefix   = if ($i -eq $cursor) { ">" } else { " " }

      if ($i -eq $cursor) {
        Write-Host "  $prefix $checkbox $($Tools[$i].Name)" -ForegroundColor Cyan -NoNewline
      } else {
        Write-Host "  $prefix $checkbox $($Tools[$i].Name)" -NoNewline
      }
      Write-Host " ($($Tools[$i].Path))" -ForegroundColor DarkGray
    }
    Write-Host "  Up/Down: navigate  Space: toggle  Enter: confirm" -ForegroundColor DarkGray -NoNewline

    while ($true) {
      $key = [Console]::ReadKey($true)

      switch ($key.Key) {
        "UpArrow"   { if ($cursor -gt 0) { $cursor-- } }
        "DownArrow" { if ($cursor -lt ($toolCount - 1)) { $cursor++ } }
        "Spacebar"  { $selected[$cursor] = if ($selected[$cursor] -eq 1) { 0 } else { 1 } }
        "Enter" {
          Write-Host ""
          [Console]::CursorVisible = $true

          $result = @()
          for ($i = 0; $i -lt $toolCount; $i++) {
            if ($selected[$i] -eq 1) { $result += $i }
          }
          return $result
        }
      }

      # Redraw — move cursor up
      [Console]::SetCursorPosition(0, [Console]::CursorTop - $toolCount)

      for ($i = 0; $i -lt $toolCount; $i++) {
        $checkbox = if ($selected[$i] -eq 1) { "[x]" } else { "[ ]" }
        $prefix   = if ($i -eq $cursor) { ">" } else { " " }

        # Clear line
        Write-Host ("`r" + (" " * [Console]::WindowWidth)) -NoNewline
        Write-Host "`r" -NoNewline

        if ($i -eq $cursor) {
          Write-Host "  $prefix $checkbox $($Tools[$i].Name)" -ForegroundColor Cyan -NoNewline
        } else {
          Write-Host "  $prefix $checkbox $($Tools[$i].Name)" -NoNewline
        }
        Write-Host " ($($Tools[$i].Path))" -ForegroundColor DarkGray
      }
      Write-Host ("`r" + (" " * [Console]::WindowWidth)) -NoNewline
      Write-Host "`r  Up/Down: navigate  Space: toggle  Enter: confirm" -ForegroundColor DarkGray -NoNewline
    }
  } finally {
    try { [Console]::CursorVisible = $true } catch {}
  }
}

# ─── Fallback menu for non-interactive terminals ──────────────────────────────
function Show-FallbackMenu {
  Write-Host ""
  for ($i = 0; $i -lt $Tools.Count; $i++) {
    Write-Host "  $($i + 1)) $($Tools[$i].Name) ($($Tools[$i].Path))"
  }
  Write-Host ""
  $choices = Read-Host "Enter tool numbers separated by spaces (e.g. 1 2 3), or 'all'"

  $result = @()
  if ($choices -eq "all") {
    for ($i = 0; $i -lt $Tools.Count; $i++) { $result += $i }
  } elseif (-not [string]::IsNullOrWhiteSpace($choices)) {
    foreach ($c in $choices -split '\s+') {
      try {
        $idx = [int]$c - 1
        if ($idx -ge 0 -and $idx -lt $Tools.Count) {
          $result += $idx
        }
      } catch {
        # Ignore invalid input
      }
    }
  }
  return $result
}

# ─── Connector selection (radio-button, single choice) ─────────────────────────
$ConnectorOptions      = @("file", "github")
$ConnectorDescriptions = @("backlog e planning come file Markdown locali", "backlog e planning su GitHub Projects v2 - richiede GitHub CLI")

function Show-ConnectorMenu {
  $cursor = 0
  $optCount = $ConnectorOptions.Count

  $isInteractive = $true
  try {
    [Console]::CursorVisible = $false
  } catch {
    $isInteractive = $false
  }

  if (-not $isInteractive) {
    return Show-FallbackConnector
  }

  try {
    # Initial draw
    for ($i = 0; $i -lt $optCount; $i++) {
      $radio  = if ($i -eq $cursor) { "(x)" } else { "( )" }
      $prefix = if ($i -eq $cursor) { ">" } else { " " }

      if ($i -eq $cursor) {
        Write-Host "  $prefix $radio $($ConnectorOptions[$i])" -ForegroundColor Cyan -NoNewline
      } else {
        Write-Host "  $prefix $radio $($ConnectorOptions[$i])" -NoNewline
      }
      Write-Host "  $($ConnectorDescriptions[$i])" -ForegroundColor DarkGray
    }
    Write-Host "  Up/Down: navigate  Enter: confirm" -ForegroundColor DarkGray -NoNewline

    while ($true) {
      $key = [Console]::ReadKey($true)

      switch ($key.Key) {
        "UpArrow"   { if ($cursor -gt 0) { $cursor-- } }
        "DownArrow" { if ($cursor -lt ($optCount - 1)) { $cursor++ } }
        "Enter" {
          Write-Host ""
          [Console]::CursorVisible = $true
          return $ConnectorOptions[$cursor]
        }
      }

      # Redraw
      [Console]::SetCursorPosition(0, [Console]::CursorTop - $optCount)

      for ($i = 0; $i -lt $optCount; $i++) {
        $radio  = if ($i -eq $cursor) { "(x)" } else { "( )" }
        $prefix = if ($i -eq $cursor) { ">" } else { " " }

        Write-Host ("`r" + (" " * [Console]::WindowWidth)) -NoNewline
        Write-Host "`r" -NoNewline

        if ($i -eq $cursor) {
          Write-Host "  $prefix $radio $($ConnectorOptions[$i])" -ForegroundColor Cyan -NoNewline
        } else {
          Write-Host "  $prefix $radio $($ConnectorOptions[$i])" -NoNewline
        }
        Write-Host "  $($ConnectorDescriptions[$i])" -ForegroundColor DarkGray
      }
      Write-Host ("`r" + (" " * [Console]::WindowWidth)) -NoNewline
      Write-Host "`r  Up/Down: navigate  Enter: confirm" -ForegroundColor DarkGray -NoNewline
    }
  } finally {
    try { [Console]::CursorVisible = $true } catch {}
  }
}

function Show-FallbackConnector {
  Write-Host ""
  for ($i = 0; $i -lt $ConnectorOptions.Count; $i++) {
    Write-Host "  $($i + 1)) $($ConnectorOptions[$i])  ($($ConnectorDescriptions[$i]))"
  }
  Write-Host ""
  $choice = Read-Host "Select connector [1]"

  if ($choice -eq "2") {
    return "github"
  }
  return "file"
}

# ─── Install config ──────────────────────────────────────────────────────────
function Install-Config {
  param([string]$SourceDir, [string]$Connector, [bool]$AssumeYes = $false)

  $configDir  = ".archetipo"
  $configFile = Join-Path $configDir "config.yaml"

  # Determine source config path
  $sourceConfig = Join-Path (Split-Path $SourceDir -Parent) ".archetipo\config.yaml"
  if (-not (Test-Path $sourceConfig)) {
    Write-Host ""
    Write-Host "  - " -ForegroundColor Yellow -NoNewline
    Write-Host "config.yaml non trovato nella source, skip" -ForegroundColor DarkGray
    return
  }

  # Check if config already exists
  if (Test-Path $configFile) {
    if (-not $AssumeYes) {
      Write-Host ""
      Write-Host "  ! " -ForegroundColor Yellow -NoNewline
      Write-Host ".archetipo\config.yaml esiste gia. Sovrascrivere? [s/N] " -NoNewline
      $answer = Read-Host
      if ($answer -ne "s" -and $answer -ne "S" -and $answer -ne "y" -and $answer -ne "Y") {
        Write-Host "  Config non modificato" -ForegroundColor DarkGray
        return
      }
    }
  }

  if (-not (Test-Path $configDir)) {
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
  }
  Copy-Item -Path $sourceConfig -Destination $configFile -Force

  # Update connector value
  $content = Get-Content $configFile -Raw
  $content = $content -replace "^connector:.*", "connector: $Connector"
  Set-Content -Path $configFile -Value $content -NoNewline

  Write-Host ""
  Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
  Write-Host ".archetipo\config.yaml" -ForegroundColor White -NoNewline
  Write-Host " (connector: $Connector)" -ForegroundColor DarkGray

  # Install shared runtime metadata
  $sourceRoot = Split-Path $SourceDir -Parent
  $sharedRuntimeSource = Join-Path $sourceRoot ".archetipo\shared-runtime.md"
  if (Test-Path $sharedRuntimeSource) {
    Copy-Item -Path $sharedRuntimeSource -Destination (Join-Path $configDir "shared-runtime.md") -Force
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host ".archetipo\shared-runtime.md" -ForegroundColor White
  }
}

# ─── Install CLI binary ──────────────────────────────────────────────────────
function Install-Cli {
  param(
    [string]$SourceRoot,
    [bool]$UseLocal
  )
  $binDir = ".archetipo\bin"
  $binPath = Join-Path $binDir "archetipo.exe"
  $shimPath = Join-Path $binDir "archetipo.cmd"
  if (-not (Test-Path $binDir)) {
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
  }

  function Write-ArchetipoShim {
    Set-Content -Path $shimPath -Value "@echo off`r`n`"%~dp0archetipo.exe`" %*`r`n" -NoNewline -Encoding ASCII
  }

  if ($UseLocal) {
    $cliDir = Join-Path $SourceRoot "cli"
    if (-not (Test-Path $cliDir)) {
      Write-Host "  $([char]0x2013) " -ForegroundColor Yellow -NoNewline
      Write-Host "cli/ source not found at $cliDir, skip" -ForegroundColor DarkGray
      return
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
      Write-Host "  $([char]0x2717) " -ForegroundColor Red -NoNewline
      Write-Host "Go toolchain not found. Install Go 1.26+ then re-run -Local, or omit -Local to download a prebuilt binary." -ForegroundColor White
      return
    }
    Write-Host ""
    Write-Host "  Building archetipo from source..." -ForegroundColor DarkGray
    $absBin = Join-Path (Get-Location) $binPath
    Push-Location $cliDir
    try {
      & go build -o $absBin "./cmd/archetipo"
      if ($LASTEXITCODE -ne 0) {
        Write-Host "  $([char]0x2717) go build failed" -ForegroundColor Red
        return
      }
    } finally {
      Pop-Location
    }
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host ".archetipo\bin\archetipo.exe " -ForegroundColor White -NoNewline
    Write-Host "(local build)" -ForegroundColor DarkGray
    Write-ArchetipoShim
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host ".archetipo\bin\archetipo.cmd " -ForegroundColor White -NoNewline
    Write-Host "(shim)" -ForegroundColor DarkGray
    return
  }

  try {
    $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
  } catch {
    $osArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  }
  $normalizedArch = if ($osArch) { $osArch.ToString().ToLowerInvariant() } else { "" }
  $arch = switch -Regex ($normalizedArch) {
    "^(x64|amd64)$" { "amd64" }
    "^(arm64|aarch64)$" { "arm64" }
    default { "" }
  }
  if (-not $arch) {
    Write-Host "  $([char]0x2013) unsupported arch for prebuilt binary; rerun with -Local" -ForegroundColor Yellow
    return
  }
  $assetName = "archetipo-windows-$arch.exe"
  $url = "https://github.com/techreloaded-ar/ARchetipo/releases/latest/download/$assetName"
  Write-Host ""
  Write-Host "  Downloading $assetName from latest release..." -ForegroundColor DarkGray
  try {
    Invoke-WebRequest -Uri $url -OutFile $binPath -ErrorAction Stop
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host ".archetipo\bin\archetipo.exe " -ForegroundColor White -NoNewline
    Write-Host "(release download)" -ForegroundColor DarkGray
    Write-ArchetipoShim
    Write-Host "  $([char]0x2713) " -ForegroundColor Green -NoNewline
    Write-Host ".archetipo\bin\archetipo.cmd " -ForegroundColor White -NoNewline
    Write-Host "(shim)" -ForegroundColor DarkGray
  } catch {
    Write-Host "  $([char]0x2013) " -ForegroundColor Yellow -NoNewline
    Write-Host "Could not fetch $assetName. Re-run with -Local to compile from source." -ForegroundColor White
  }
}

# ─── Main ─────────────────────────────────────────────────────────────────────
function Main {
  Write-Host ""
  if ($Help) {
    Write-Host @"

ARchetipo Installer

Usage:
  irm https://raw.githubusercontent.com/techreloaded-ar/ARchetipo/main/install.ps1 | iex
  .\install.ps1 [-Local] [-Cleanup] [-Tool codex] [-Connector file] [-Yes] [-Help]

Flags:
  -Local    Install from local .\skills\ folder instead of downloading from GitHub
  -Cleanup  Remove installed skills from selected tools
  -Tool     Install or cleanup a single tool non-interactively (claude, codex, gemini, opencode, copilot, pi)
  -Connector Select connector non-interactively (file or github)
  -Yes      Overwrite config.yaml without prompting
  -Help     Show this help message

Skills installed:
  archetipo-autopilot
  archetipo-design
  archetipo-implement
  archetipo-inception
  archetipo-plan
  archetipo-spec

Configuration:
  .archetipo\config.yaml is created with the selected connector (file or github).

Supported tools:
  Claude Code, Codex, Gemini CLI, OpenCode, GitHub Copilot, Pi
"@
    return
  }

  Write-Host "  ARchetipo Installer" -ForegroundColor Cyan

  if ($Cleanup) {
    Write-Host "  Remove ARchetipo skills from your tools" -ForegroundColor DarkGray
    Write-Host ""
    Write-Host ""

    # Tool selection
    if ($Tool) {
      try {
        $selectedTools = @(Resolve-ToolIndex -Raw $Tool)
      } catch {
        Write-Host "  Error: $($_.Exception.Message)" -ForegroundColor Red
        exit 1
      }
    } else {
      Write-Host "  Select tools to clean up:" -ForegroundColor White
      Write-Host ""
      $selectedTools = Show-Menu
    }

    if ($null -eq $selectedTools -or $selectedTools.Count -eq 0) {
      Write-Host "  No tools selected. Exiting." -ForegroundColor Yellow
      return
    }

    # Cleanup
    Write-Host "  Cleaning up..." -ForegroundColor White

    foreach ($toolIndex in $selectedTools) {
      Remove-ForTool -ToolIndex $toolIndex
    }

    # Summary
    Write-Host ""
    Write-Host "  Done! " -ForegroundColor Green -NoNewline
    Write-Host "Cleaned up $($selectedTools.Count) tool(s)."
    Write-Host ""
    return
  }

  Write-Host "  Install ARchetipo skills for your tools" -ForegroundColor DarkGray
  Write-Host ""

  $sourceDir = ""
  $tempDir   = $null

  if ($Local) {
    # Use the local repository folder relative to this installer.
    $localSkillsDir = Join-Path $ScriptDir "skills"
    if (-not (Test-Path $localSkillsDir)) {
      Write-Host "  Error: $localSkillsDir folder not found." -ForegroundColor Red
      return
    }
    $sourceDir = $localSkillsDir
    Write-Host "  Using local skills folder..." -ForegroundColor DarkGray
  } else {
    # Create temp directory
    $tempDir = Join-Path ([System.IO.Path]::GetTempPath()) "archetipo-$(Get-Random)"
    New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

    Write-Host "  Downloading skills..." -ForegroundColor DarkGray
    $zipFile = Join-Path $tempDir "archetipo.zip"

    try {
      Invoke-WebRequest -Uri $RepoZip -OutFile $zipFile -UseBasicParsing -ErrorAction Stop
    } catch {
      Write-Host "  X Failed to download repository" -ForegroundColor Red
      if ($null -ne $tempDir -and (Test-Path $tempDir)) {
        Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
      }
      return
    }

    Expand-Archive -Path $zipFile -DestinationPath $tempDir -Force
    $sourceDir = Join-Path $tempDir "ARchetipo-main\skills"

    Write-Host "  $([char]0x2713) Downloaded skills" -ForegroundColor Green
  }

  Write-Host ""

  # Tool selection
  if ($Tool) {
    try {
      $selectedTools = @(Resolve-ToolIndex -Raw $Tool)
    } catch {
      Write-Host "  Error: $($_.Exception.Message)" -ForegroundColor Red
      exit 1
    }
  } else {
    Write-Host "  Select tools to install for:" -ForegroundColor White
    Write-Host ""
    $selectedTools = Show-Menu
  }

  if ($null -eq $selectedTools -or $selectedTools.Count -eq 0) {
    Write-Host "  No tools selected. Exiting." -ForegroundColor Yellow
    if ($null -ne $tempDir -and (Test-Path $tempDir)) {
      Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
    return
  }

  # Connector selection
  if ($Connector) {
    $selectedConnector = $Connector
  } else {
    Write-Host "  Select connector:" -ForegroundColor White
    Write-Host ""
    $selectedConnector = Show-ConnectorMenu
  }

  # Install
  Write-Host "  Installing..." -ForegroundColor White

  foreach ($toolIndex in $selectedTools) {
    Install-ForTool -ToolIndex $toolIndex -SourceDir $sourceDir
  }

  # Install config
  Install-Config -SourceDir $sourceDir -Connector $selectedConnector -AssumeYes $Yes

  # Install CLI binary
  $sourceRoot = Split-Path $sourceDir -Parent
  Install-Cli -SourceRoot $sourceRoot -UseLocal $Local

  # Summary
  Write-Host ""
  Write-Host "  Done! " -ForegroundColor Green -NoNewline
  Write-Host "Installed $($SkillNames.Count) skill(s) for $($selectedTools.Count) tool(s)."
  Write-Host ""

  # Cleanup temp directory
  if ($null -ne $tempDir -and (Test-Path $tempDir)) {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Main
