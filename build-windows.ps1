# build-windows.ps1 — builds api-playground.exe for Windows.
# Run from the project root in PowerShell:
#   .\build-windows.ps1          # build only
#   .\build-windows.ps1 -Run     # build, then launch
#   .\build-windows.ps1 -Console # keep a console window attached (shows logs)
#
# If scripts are blocked, run once:
#   Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
param(
    [switch]$Run,
    [switch]$Console
)
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# ── Toolchain checks ───────────────────────────────────────────────────────
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "Go is not on PATH. Install it from https://go.dev/dl/ and reopen PowerShell."
}
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    Write-Error @"
gcc is not on PATH. webview_go needs a C/C++ compiler (CGO). Install MinGW-w64, e.g.:
  winget install BrechtSanders.WinLibs.POSIX.UCRT
or MSYS2 (https://www.msys2.org) then: pacman -S mingw-w64-ucrt-x86_64-gcc
and add its bin folder to PATH.
"@
}

# ── Build ──────────────────────────────────────────────────────────────────
$env:CGO_ENABLED = "1"
$ldflags = "-s -w"
if (-not $Console) {
    # Build as a GUI app so no console window opens alongside it
    $ldflags += " -H windowsgui"
}

Write-Host "🔨  Downloading dependencies…"
go mod download

Write-Host "🔨  Building api-playground.exe (with CGO for native window)…"
go build -ldflags="$ldflags" -o api-playground.exe .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host ""
Write-Host "✅  Built: api-playground.exe"
Write-Host "    Run it from this folder (templates are loaded relative to the working directory):"
Write-Host "      .\api-playground.exe"

if ($Run) {
    & .\api-playground.exe
}
