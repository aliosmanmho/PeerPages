# A local snapshot build with goreleaser: three platforms x two architectures,
# and it DOES NOT PUBLISH.
# The equivalent of `make release-snapshot`.
#
# The output: 6 archives plus checksums.txt under dist/.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command goreleaser -ErrorAction SilentlyContinue)) {
  Write-Host "goreleaser is not installed. To install it:" -ForegroundColor Yellow
  Write-Host "  go install github.com/goreleaser/goreleaser/v2@latest"
  throw "goreleaser was not found"
}

Push-Location $root
try {
  goreleaser check
  if ($LASTEXITCODE -ne 0) { throw "the goreleaser configuration is invalid" }

  goreleaser release --snapshot --clean
  if ($LASTEXITCODE -ne 0) { throw "the snapshot build failed" }
} finally { Pop-Location }

Get-ChildItem (Join-Path $root "dist") -Filter "peerpages_*" |
  Where-Object { $_.Extension -in ".gz", ".zip" } |
  ForEach-Object { "{0,10:N0}  {1}" -f $_.Length, $_.Name }

Write-Host "the snapshot build is ready: dist/" -ForegroundColor Green
