# Gelistirme

## Gereksinimler

| Arac | Surum | Not |
|---|---|---|
| Go | 1.22+ (gelistirme: 1.27) | ajan |
| Node.js | 20+ (gelistirme: 22.12) | web |
| make | opsiyonel | Windows'ta yoksa `scripts/*.ps1` kullan |
| golangci-lint | opsiyonel | yoksa `gofmt` + `go vet` calisir, CI'da zorunlu |

Ilk kurulum:

```sh
cd web && npm install
cd ../agent && go mod download
cd ../web && npx playwright install chromium   # yalnizca e2e icin
```

## Komut esdegerleri

| Ne yapar | make | npm (web icinde) | PowerShell (repo kokunde) |
|---|---|---|---|
| Web dev sunucusu (5173) | `make dev` | `npm run dev` | `.\scripts\dev.ps1` |
| Tum birim testler | `make test` | — | `.\scripts\test.ps1` |
| Web birim testleri | `make test-web` | `npm test` | — |
| Ajan birim testleri | `make test-agent` | — | `cd agent; go test ./...` |
| Derleme (web + ajan) | `make build` | — | `.\scripts\build.ps1` |
| Pages derlemesi | `make build PEERPAGES_BASE=/PeerPages/` | — | `.\scripts\build.ps1 -Base "/PeerPages/"` |
| Lint / format kontrolu | `make lint` | `npm run lint` | `.\scripts\lint.ps1` |
| Bicimlendir | — | `npm run format` | — |
| E2E (Faz 1 kabul) | `make e2e` | `npm run e2e` | `.\scripts\e2e.ps1` |
| Test verisi uret | `make testdata` | — | `cd agent; go run ./internal/testsite/gen` |

## `base` (GitHub Pages alt yolu)

Uygulama `/` ya da `/PeerPages/` gibi bir alt yolda calisabilir. Deger derleme
zamani `PEERPAGES_BASE` ortam degiskeniyle verilir ve `import.meta.env.BASE_URL`
olarak koda gecer. Service Worker scope'u da bu degerden turetilir
(`src/base.ts`).

> **Git Bash uyarisi:** MSYS, `/PeerPages/` gibi ortam degiskeni degerlerini
> Windows yoluna cevirir (`C:/Program Files/Git/PeerPages/`). Git Bash'te
> derlerken degiskeni korumak icin:
>
> ```sh
> MSYS2_ENV_CONV_EXCL=PEERPAGES_BASE PEERPAGES_BASE=/PeerPages/ npm run build
> ```
>
> PowerShell'de (`scripts\build.ps1`) boyle bir sorun yok.

## Web derlemesi neden iki adim

`npm run build` sirayla iki Vite derlemesi calistirir:

1. `vite build` — app shell (`index.html` + hash'li `assets/`).
2. `vite build --config vite.sw.config.ts` — Service Worker, `dist/sw.js`.

Service Worker ayri derlenir cunku:

- dosya adi **hash'siz ve sabit** olmali (`register(base + "sw.js")`),
- scope kokunde tek dosya olmali (ek chunk = ek istek),
- ne bundle'landigi uzerinde tam kontrol gerekiyor.

`vite-plugin-pwa` bilerek kullanilmiyor (README karari).

İkinci derleme `emptyOutDir: false` ile calisir; bu yuzden **sira onemli**.

## TypeScript yapilandirmasi

Uc ayri `tsconfig` var, cunku app ve Service Worker farkli global ortamlarda
calisir (`DOM` ve `WebWorker` lib'leri ayni anda kullanilamaz):

| Dosya | Lib | Kapsam |
|---|---|---|
| `tsconfig.json` | `DOM` | `src/**` (sw.ts haric) |
| `tsconfig.sw.json` | `WebWorker` | `src/sw.ts`, `src/protocol/**` |
| `tsconfig.node.json` | node | `e2e/**`, `*.config.ts`, testler |

`npm run typecheck` ucunu birlikte calistirir; `npm run lint` buna biome'u ekler.

## Ajan

```sh
cd agent
go run . serve --target http://localhost:3000
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000
go run . serve --target http://localhost:3000 --verbose   # path'ler maskelenmez
```

Loglar varsayilan olarak URL path'lerini maskeler (`/api/items` → `/*3/*5`);
`--verbose` bunu kapatir (README guvenlik ilkesi 6).

Ajan **yalnizca** `--target` ile verilen hostlara istek atar. Istek yolundan
host turetmek `internal/target` icinde kasten imkansiz kilindi ve testlerle
korunuyor (ilke 1).
