# Playwright e2e: Faz 1 kabul kriterlerini dogrular.
# `make e2e` esdegeri. Ilk calistirmada: cd web; npx playwright install chromium
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $root "agent")
try {
  if (Test-Path "internal/testsite/gen") { go run ./internal/testsite/gen }
  go build -o peerpages.exe .
  if ($LASTEXITCODE -ne 0) { throw "ajan derlemesi basarisiz" }
} finally { Pop-Location }

Push-Location (Join-Path $root "web")
try {
  npm run build
  if ($LASTEXITCODE -ne 0) { throw "web derlemesi basarisiz" }
  npm run e2e
  if ($LASTEXITCODE -ne 0) { throw "e2e basarisiz" }
} finally { Pop-Location }

Write-Host "e2e yesil" -ForegroundColor Green
