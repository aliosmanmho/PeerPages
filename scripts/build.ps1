# Builds the web app (the app shell plus sw.js) and the agent binary.
# The equivalent of `make build`.
#
# For the GitHub Pages subpath:
#   .\scripts\build.ps1 -Base "/PeerPages/"
param([string]$Base = "/")

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "== building the web app (base=$Base) ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "web")
try {
  node scripts/build.mjs $Base
  if ($LASTEXITCODE -ne 0) { throw "the web build failed" }
} finally { Pop-Location }

Write-Host "== building the agent ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "agent")
try {
  go build -o peerpages.exe .
  if ($LASTEXITCODE -ne 0) { throw "the agent build failed" }
} finally { Pop-Location }

Write-Host "ready: web/dist and agent/peerpages.exe" -ForegroundColor Green
