# Gelistirme

## Gereksinimler

| Arac | Surum | Not |
|---|---|---|
| Go | 1.27+ (agent/go.mod'daki `go` yonergesi) | ajan |
| Node.js | 20+ (gelistirme: 22.12) | web |
| make | opsiyonel | Windows'ta yoksa `scripts/*.ps1` kullan |
| golangci-lint | opsiyonel | yoksa `gofmt` + `go vet` calisir, CI'da zorunlu |
| goreleaser | opsiyonel | yalnizca release derlemesi icin: `go install github.com/goreleaser/goreleaser/v2@latest` |

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
| E2E (tum fazlar) | `make e2e` | `npm run e2e` | `.\scripts\e2e.ps1` |
| Test verisi uret | `make testdata` | — | `cd agent; go run ./internal/testsite/gen` |
| Release anlik derleme | `make release-snapshot` | — | `scripts/release-snapshot.ps1` |
| Release yapilandirmasi | `make release-check` | — | `goreleaser check` |

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

# Teshis: relay sagligi + NAT tipi (+ TURN allocation)
go run . doctor
go run . doctor --json
go run . doctor --turn turn:ornek.com:3478 --turn-user u --turn-pass p

# TURN (simetrik NAT'ta gerekir; yalnizca AJAN tarafinda kullanilir)
go run . serve --target http://localhost:3000   --turn turn:ornek.com:3478 --turn-user u --turn-pass p
# Cloudflare Realtime TURN: kimlik bilgisi API'den cekilir ve yenilenir
PEERPAGES_CF_TURN_TOKEN=... go run . serve --target http://localhost:3000   --turn-cloudflare-key-id <anahtar>
# Teshis: dogrudan yolu imkansiz kil (baglanti kurulursa TURN'den gecti)
go run . serve --target http://localhost:3000   --turn turn:127.0.0.1:3478 --turn-user u --turn-pass p --ice-relay-only

# WebSocket koprusu (Faz 6): shim HTML'e enjekte edilir, varsayilan KAPALI
go run . serve --target http://localhost:5173 --inject-ws-shim
# Sinirlar: uzun yasayan akis (ws + SSE) sayisi ve ws mesaj tavani
go run . serve --target http://localhost:3000 --max-streams 16 --max-ws-message 4MB

# Surum ve derleme bilgisi
go run . version

# Kimlik
go run . key                   # peer-id ve ziyaretci URL'si
go run . key --target admin    # hedefli ziyaretci URL'si
go run . key --reveal-secret   # gizli anahtari da goster

# Ayrintili log (path'ler MASKELENMEZ)
go run . serve --target http://localhost:3000 --verbose
```

Testler icin gizli komutlar: `peerpages testsite` (yerel test sitesi),
`peerpages fakerelay` (surec ici Nostr relay'i), `peerpages faketurn`
(surec ici TURN sunucusu, Faz 5).

Test sitesinin akis uclari (Faz 6): `/sse?n=5&delay=400&keepalive=1` ve
`/ws` (metin + ikili yanki; `?proto=` alt protokol, `?close=4002` sunucu
kapanisi). SSE'nin varsayilani Faz 1'deki gibi: uc olay, beklemesiz.

WebSocket koprusu ve shim: [WEBSOCKET.md](WEBSOCKET.md).

Sifre ve jeton icin ortam degiskenleri: `PEERPAGES_TURN_PASS`,
`PEERPAGES_CF_TURN_TOKEN`. Komut satiri argumanlari `ps` ciktisinda baska
kullanicilara gorunur, bu yuzden bayrak tek yol degil.

TURN kurulumu ve guvenlik notlari: [TURN.md](TURN.md).

Loglar varsayilan olarak URL path'lerini maskeler (`/api/items` → `/*3/*5`);
`--verbose` bunu kapatir (README guvenlik ilkesi 6).

Ajan **yalnizca** `--target` ile verilen hostlara istek atar. Istek yolundan
host turetmek `internal/target` icinde kasten imkansiz kilindi ve testlerle
korunuyor (ilke 1).

## Release (Faz 4)

Surum bilgisi `-ldflags -X` ile `main.buildVersion/buildCommit/buildDate`'e
enjekte edilir; verilmezse `runtime/debug.ReadBuildInfo` kullanilir
(`internal/version`). Yani `go install ...@latest` ile kurulan binary de
dogru surumu basar.

```sh
goreleaser check                       # yapilandirma gecerli mi
goreleaser release --snapshot --clean  # dist/ altinda 6 arsiv, YAYIN YAPMAZ
```

Gercek release: `v*` bicimli bir etiket push edilince
`.github/workflows/release.yml` kosar ve goreleaser release'i **taslak**
olarak acar (notlar elle gozden gecirilir). Scoop/Homebrew taslaklari
`.goreleaser.yaml` icinde ama `skip_upload: true` ile kapali: tap depolari
henuz yok ve proje adi Faz 7'de degisebilir.

Son kullanici kurulumu: `docs/INSTALL.md`.
