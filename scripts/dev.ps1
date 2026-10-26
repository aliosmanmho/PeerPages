# Web gelistirme sunucusunu baslatir (http://localhost:5173).
# `make dev` esdegeri — Windows'ta make kurulu olmadiginda.
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location (Join-Path $root "web")
try { npm run dev } finally { Pop-Location }
