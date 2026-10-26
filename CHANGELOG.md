# Changelog

Bu dosyanın formatı [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/)
temel alır. Proje henüz sürümlenmiyor; girdiler faz bazlı tutulur.

## [Unreleased]

### Faz 0 — İskelet

- Monorepo iskeleti: `web/`, `agent/`, `protocol/`, `docs/`, `scripts/`,
  `.github/workflows/`.
- MIT lisansi, `CONTRIBUTING.md` taslagi, bu changelog.
- `web/`: Vite + TypeScript, UI framework yok, runtime bagimliligi yok.
  Service Worker **ayri** derlenir (`vite.sw.config.ts` -> hash'siz `dist/sw.js`);
  `vite-plugin-pwa` kullanilmadi. `base`, `PEERPAGES_BASE` ile GitHub Pages alt
  yolu icin ayarlanabilir. Uc ayri `tsconfig` (app / sw / node).
- `agent/`: Go + cobra. `peerpages serve --target ...`; `--max-body` (50MB),
  `--max-concurrent` (64), `--manual`, `--stun`, `--verbose`.
  - `internal/target`: hedef allowlist'i; istek yolundan host turetmek
    imkansiz (SSRF korumasi, README ilke 1).
  - `internal/logging`: varsayilan olarak URL path'leri maskelenir (ilke 6).
  - `internal/size`: boyut ifadeleri (1024 tabanli).
- Lint/format: `biome` (web), `gofmt` + `go vet` + `golangci-lint` (agent).
- `Makefile` + Windows icin `scripts/*.ps1` esdegerleri, `docs/DEV.md`.
- `.github/workflows/ci.yml`: web lint+test, agent lint+test (-race), e2e.

**Kabul (dogrulandi):** `npm run dev` -> `http://localhost:5173` 200 donuyor;
`peerpages --help`, `peerpages serve --help` ve `peerpages --version` calisiyor.
