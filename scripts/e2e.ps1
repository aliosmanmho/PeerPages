# Playwright e2e: Faz 1 kabul kriterlerini dogrular.
# `make e2e` esdegeri. Ilk calistirmada: cd web; npx playwright install chromium
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

# Playwright kendisi hazirlik yapiyor: web'i dogru alt yolla derler, 5 MB test
# verisini uretir ve ajani derler (web/playwright.config.ts + e2e/global-setup.ts).
Push-Location (Join-Path $root "web")
try {
  npm run e2e
  if ($LASTEXITCODE -ne 0) { throw "e2e basarisiz" }
} finally { Pop-Location }

Write-Host "e2e yesil" -ForegroundColor Green
