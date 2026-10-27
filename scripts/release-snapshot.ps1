# goreleaser ile yerel anlik derleme: uc platform x iki mimari, YAYIN YAPMAZ.
# `make release-snapshot` esdegeri.
#
# Cikti: dist/ altinda 6 arsiv + checksums.txt.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command goreleaser -ErrorAction SilentlyContinue)) {
  Write-Host "goreleaser kurulu degil. Kurmak icin:" -ForegroundColor Yellow
  Write-Host "  go install github.com/goreleaser/goreleaser/v2@latest"
  throw "goreleaser bulunamadi"
}

Push-Location $root
try {
  goreleaser check
  if ($LASTEXITCODE -ne 0) { throw "goreleaser yapilandirmasi gecersiz" }

  goreleaser release --snapshot --clean
  if ($LASTEXITCODE -ne 0) { throw "anlik derleme basarisiz" }
} finally { Pop-Location }

Get-ChildItem (Join-Path $root "dist") -Filter "peerpages_*" |
  Where-Object { $_.Extension -in ".gz", ".zip" } |
  ForEach-Object { "{0,10:N0}  {1}" -f $_.Length, $_.Name }

Write-Host "anlik derleme hazir: dist/" -ForegroundColor Green
