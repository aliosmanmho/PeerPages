# Web (app shell + sw.js) ve ajan binary'sini derler.
# `make build` esdegeri.
#
# GitHub Pages alt yolu icin:
#   .\scripts\build.ps1 -Base "/PeerPages/"
param([string]$Base = "/")

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "== web derleniyor (base=$Base) ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "web")
try {
  node scripts/build.mjs $Base
  if ($LASTEXITCODE -ne 0) { throw "web derlemesi basarisiz" }
} finally { Pop-Location }

Write-Host "== ajan derleniyor ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "agent")
try {
  go build -o peerpages.exe .
  if ($LASTEXITCODE -ne 0) { throw "ajan derlemesi basarisiz" }
} finally { Pop-Location }

Write-Host "hazir: web/dist ve agent/peerpages.exe" -ForegroundColor Green
