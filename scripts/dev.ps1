# Starts the web dev server (http://localhost:5173).
# The equivalent of `make dev`, for when make is not installed on Windows.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $root "web")
try { npm run dev } finally { Pop-Location }
