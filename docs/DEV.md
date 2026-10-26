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
| Pages derlemesi | `make build PEERPAGES_BASE=/PeerPages/` | `node scripts/build.mjs /PeerPages/` | `.\scripts\build.ps1 -Base "/PeerPages/"` |
| Lint / format kontrolu | `make lint` | `npm run lint` | `.\scripts\lint.ps1` |
| Bicimlendir | — | `npm run format` | — |
| E2E (Faz 1 kabul) | `make e2e` | `npm run e2e` | `.\scripts\e2e.ps1` |
| Test verisi uret | `make testdata` | — | `cd agent; go run ./internal/testsite/gen` |

## `base` (GitHub Pages alt yolu)

Uygulama `/` ya da `/PeerPages/` gibi bir alt yolda calisabilir. Deger derleme
zamani `PEERPAGES_BASE` ortam degiskeniyle verilir ve `import.meta.env.BASE_URL`
olarak koda gecer. Service Worker scope'u da bu degerden turetilir
(`src/base.ts`).

Derleme `scripts/build.mjs` uzerinden yapilir; base'i ortam degiskeni olarak
alt sureclere kendisi gecirir. Boylece "Windows'ta `VAR=x cmd` calismaz" ve
"Git Bash `/PeerPages/` degerini Windows yoluna cevirir" sorunlarinin ikisi de
asilir.

> **Git Bash uyarisi:** base'i ARGUMAN olarak verirken MSYS yine araya girer:
>
> ```sh
> MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/
> ```
>
> Script boyle bir donusumu tespit ederse anlasilir bir hata verir.
> PowerShell'de (`scripts\build.ps1`) boyle bir sorun yok.

## URL yerlesimi

| Yol | Ne |
|---|---|
| `/<base>/` | **Yerel sitenin koku.** Tunel acikken SW burayi proxy'ler. Tunel yokken kucuk bir onyukleyici servis edilir: SW'yi kurar ve `connect/`e gecer. |
| `/<base>/connect/` | **Baglanti ekrani + tunel sahibi sayfa.** SW bu yolu asla tunelden gecirmez, boylece bu sekme yenilense bile tunel yasar. |
| `/<base>/sw.js` | Service Worker. Hash'siz ve scope kokunde olmak zorunda. |
| `/<base>/assets/…` | App shell'in JS/CSS dosyalari. |

Gerekcesi README bolum 8'de ("App shell ile yerel sitenin koku ayni URL'yi
istiyor"). SW kapsamini **calisma zamaninda** `registration.scope`'tan
turetiyor (`src/sw.ts`), derleme zamani bir sabitten degil.

## Web derlemesi neden iki adim

`npm run build` (yani `node scripts/build.mjs`) sirayla iki Vite derlemesi
calistirir:

1. `vite build` — iki HTML girdisi (`index.html` onyukleyici,
   `connect/index.html` baglanti ekrani) ve hash'li `assets/`.
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

# Nostr signaling (varsayilan): ziyaretci yalnizca URL'yi acar
go run . serve --target http://localhost:3000

# Paylasilan sirla
go run . serve --target http://localhost:3000 --secret kedi-balik-7

# Coklu hedef: ajan her hedef icin ayri ziyaretci URL'si basar
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000

# Elle signaling (kopyala-yapistir); hedefi bayrak secer
go run . serve --target http://localhost:3000 --manual
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000 --manual --manual-target admin

# Kendi relay listen
go run . serve --target http://localhost:3000 --relay wss://nos.lol

# Coklu hedef (URL ile secim Faz 3'te)
go run . serve -t http://localhost:3000 -t admin=http://localhost:4000

# Teshis: relay sagligi + NAT tipi
go run . doctor
go run . doctor --json

# Kimlik
go run . key                   # peer-id ve ziyaretci URL'si
go run . key --target admin    # hedefli ziyaretci URL'si
go run . key --reveal-secret   # gizli anahtari da goster

# Ayrintili log (path'ler MASKELENMEZ)
go run . serve --target http://localhost:3000 --verbose
```

Testler icin gizli komutlar: `peerpages testsite` (yerel test sitesi),
`peerpages fakerelay` (surec ici Nostr relay'i).

Loglar varsayilan olarak URL path'lerini maskeler (`/api/items` → `/*3/*5`);
`--verbose` bunu kapatir (README guvenlik ilkesi 6).

Ajan **yalnizca** `--target` ile verilen hostlara istek atar. Istek yolundan
host turetmek `internal/target` icinde kasten imkansiz kilindi ve testlerle
korunuyor (ilke 1).
