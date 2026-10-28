# Playwright e2e: it verifies the phase 1 acceptance criteria.
# The equivalent of `make e2e`. On the first run: cd web; npx playwright install chromium
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

# Playwright does the preparation itself: it builds the web app with the
# right subpath, generates the 5 MB test data and builds the agent
# (web/playwright.config.ts + e2e/global-setup.ts).
Push-Location (Join-Path $root "web")
try {
  npm run e2e
  if ($LASTEXITCODE -ne 0) { throw "e2e failed" }
} finally { Pop-Location }

Write-Host "e2e is green" -ForegroundColor Green
