# biome + tsc + gofmt + go vet (+ golangci-lint kurulu ise).
# `make lint` esdegeri.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $root "web")
try { npm run lint; if ($LASTEXITCODE -ne 0) { throw "web lint basarisiz" } } finally { Pop-Location }

Push-Location (Join-Path $root "agent")
try {
  $unformatted = gofmt -l .
  if ($unformatted) { throw "gofmt: bicimlenmemis dosyalar:`n$unformatted" }
  go vet ./...
  if ($LASTEXITCODE -ne 0) { throw "go vet basarisiz" }
  if (Get-Command golangci-lint -ErrorAction SilentlyContinue) {
    golangci-lint run
    if ($LASTEXITCODE -ne 0) { throw "golangci-lint basarisiz" }
  } else {
    Write-Host "golangci-lint kurulu degil, atlandi" -ForegroundColor Yellow
  }
} finally { Pop-Location }

Write-Host "lint temiz" -ForegroundColor Green
