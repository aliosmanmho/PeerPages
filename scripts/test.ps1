# Tum birim testleri: web (vitest) + ajan (go test).
# `make test` esdegeri.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "== web birim testleri ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "web")
try { npm test; if ($LASTEXITCODE -ne 0) { throw "web testleri basarisiz" } } finally { Pop-Location }

Write-Host "== ajan birim testleri ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "agent")
try {
  if (Test-Path "internal/testsite/gen") { go run ./internal/testsite/gen }
  go test ./...
  if ($LASTEXITCODE -ne 0) { throw "ajan testleri basarisiz" }
} finally { Pop-Location }

Write-Host "tum testler yesil" -ForegroundColor Green
