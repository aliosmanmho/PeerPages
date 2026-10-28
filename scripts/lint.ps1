# biome + tsc + gofmt + go vet (plus golangci-lint when installed).
# The equivalent of `make lint`.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $root "web")
try { npm run lint; if ($LASTEXITCODE -ne 0) { throw "the web lint failed" } } finally { Pop-Location }

Push-Location (Join-Path $root "agent")
try {
  $unformatted = gofmt -l .
  if ($unformatted) { throw "gofmt: unformatted files:`n$unformatted" }
  go vet ./...
  if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
  if (Get-Command golangci-lint -ErrorAction SilentlyContinue) {
    golangci-lint run
    if ($LASTEXITCODE -ne 0) { throw "golangci-lint failed" }
  } else {
    Write-Host "golangci-lint is not installed, skipped" -ForegroundColor Yellow
  }
} finally { Pop-Location }

Write-Host "lint is clean" -ForegroundColor Green
