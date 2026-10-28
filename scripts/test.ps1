# Every unit test: web (vitest) plus the agent (go test).
# The equivalent of `make test`.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "== the web unit tests ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "web")
try { npm test; if ($LASTEXITCODE -ne 0) { throw "the web tests failed" } } finally { Pop-Location }

Write-Host "== the agent unit tests ==" -ForegroundColor Cyan
Push-Location (Join-Path $root "agent")
try {
  if (Test-Path "internal/testsite/gen") { go run ./internal/testsite/gen }
  go test ./...
  if ($LASTEXITCODE -ne 0) { throw "the agent tests failed" }
} finally { Pop-Location }

Write-Host "every test is green" -ForegroundColor Green
