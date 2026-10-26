# Katkı rehberi (taslak)

PeerPages henüz erken aşamada (Faz 1). Dışarıdan katkı için public README ve
`docs/SECURITY.md` Faz 7'de yazılacak; şimdilik bu dosya repo içi çalışma
kurallarını tutar.

## Monorepo düzeni

| Dizin | İçerik |
|---|---|
| `web/` | Statik uygulama: Vite + TypeScript, framework yok. App shell + elle yazılmış Service Worker. |
| `agent/` | Go ajanı (tek binary). WebRTC + reverse proxy. |
| `protocol/` | Data channel protokolünün normatif tanımı ve **paylaşılan test vektörleri**. |
| `docs/` | Geliştirici ve elle test dokümanları. |
| `scripts/` | Windows (PowerShell) dev scriptleri ve test verisi üreticileri. |

## Komutlar

`make` kurulu ise `make dev` / `make test` / `make lint` / `make e2e`.
Windows'ta eşdeğerleri için `docs/DEV.md`'ye bak.

## Değişmez kurallar

Bu kararlar brief'te (`README.md`) sabitlendi; değiştirmek için önce tartışma açılır:

1. Ajan **Go**, tek statik binary.
2. Web **Vite + TypeScript**, UI framework yok (bundle küçük kalsın).
3. Service Worker **elle** yazılır ve elle derlenir — `vite-plugin-pwa` kullanılmaz.
4. Data channel **iki tane**: `ctrl` (JSON) ve `data` (binary).
5. Binary framing payload sınırı **16 KiB**.
6. `README.md` §6'daki güvenlik ilkeleri her fazda geçerlidir. Özellikle:
   ajan yalnızca CLI'da verilen hedeflere istek atar (path'ten host türetmek yasak).

## Protokol değişikliği

`protocol/` içindeki framing veya ctrl mesajları değişirse:

1. `protocol/README.md` güncellenir,
2. `protocol/testvectors.json`'a vektör eklenir,
3. **hem** TS (`web/src/protocol/`) **hem** Go (`agent/internal/protocol/`) tarafı
   aynı vektörlerle yeşile geçer.

Tek taraflı protokol değişikliği kabul edilmez.

## Commit mesajları

Faz boyunca: `faz-<N>: kısa açıklama`. Küçük ve tek konulu commit'ler.
Her faz bitiminde `CHANGELOG.md` güncellenir.

## Test

- Birim: `vitest` (web), `go test` (agent).
- E2E: Playwright (Chromium), ajan + test sitesi gerçekten başlatılır.
- Bir faz, kabul kriterleri testlerle doğrulanmadan kapanmaz.
