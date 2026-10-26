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

### Faz 1 - Cekirdek kanit: elle signaling + SW proxy

Signaling sunucusu ve Nostr YOK: offer/answer kopyala-yapistir ile tasiniyor.
Amac SW + WebRTC + reverse proxy zincirini kanitlamak (README Faz 1).

#### Protokol (`protocol/`)

- `protocol/README.md` normatif tanim, `protocol/testvectors.json` makine
  karsiligi. **Hem TS hem Go testleri ayni dosyayi okuyor.**
- `data` cercevesi: `[4B id BE][4B seq BE][payload <= 16384]`.
- `ctrl`: `req`/`res`/`end`/`abort`/`ping`/`pong`/`hello`; hata kodlari ve
  hata sirasi tanimli.
- Kanonik serilestirme: iki uygulama bayt bayt ayni cikti uretiyor; vektorler
  bunu dogruluyor.
- **`end` mesaji `frames` ve `len` sayaclari tasiyor** (asagidaki karar).

#### Ajan (`agent/`)

- `internal/signal`: elle signaling icin base64(JSON) SDP tasima.
- `internal/tunnel`: pion/webrtc v4 oturumu, iki data channel, hello,
  ping/pong, istek coklama, backpressure (1 MiB / 256 KiB esikleri),
  eszamanlilik ve govde boyutu tavanlari.
- `internal/proxy`: reverse proxy. Hop-by-hop baslik temizleme, `Host`
  yeniden yazimi, `Location`/`Content-Location` goreli yola cevirme, baslik
  enjeksiyonu reddi, akis halinde govde (tam govde beklenmiyor),
  yonlendirmeleri izlememe, hata metni sizdirmama.
- `internal/testsite` + `testdata/site`: HTML/CSS/JS/PNG/JSON/302/SSE ve
  deterministik 5 MB `big.bin` (repoda tutulmuyor, uretiliyor).
- `peerpages serve` artik gercekten tunel aciyor; gizli `peerpages testsite`
  komutu testler icin yerel sunucu sagliyor.

#### Web (`web/`)

- `src/sw.ts`: elle yazilmis Service Worker. Kapsamindaki istekleri yakalar,
  `MessageChannel` ile tunel sayfasina iletir, cevabi `ReadableStream` ile
  akitir. Kapsam **calisma zamaninda** `registration.scope`'tan turetiliyor.
- `src/tunnel.ts`: sayfa tarafi coklayici; sayac kurali, seq dogrulamasi,
  backpressure, AbortSignal, zaman asimi.
- `src/signal.ts`, `src/bridge.ts`, `src/page-bridge.ts`, `src/boot.ts`.
- Baglanti ekrani: offer yapistir -> answer uret -> tunel ac, durum ve sayaclar.

#### Degisen kararlar (ikisi de olculerek bulundu, kullaniciya soruldu)

1. **`ctrl` ve `data` arasinda siralama garantisi yok.** Olcum: 90 KB'lik
   cevapta 6 govde cercevesinin tamami `end`'den SONRA geldi. Cozum: `end`
   mesajina `frames`/`len` sayaclari eklendi; alici sayaclar tamamlanana kadar
   bekliyor. Istek yonunde de `req`'ten once gelen cerceveler tamponlaniyor.
   Iki kanal, 16 KiB framing ve ctrl'deki `end` kararlari korundu.
2. **App shell ile yerel sitenin koku ayni URL'yi istiyordu.** Cozum: baglanti
   ekrani + tunel sahibi sayfa `/connect/` yolunda; scope koku yerel sitenin
   koku. Tunel sahibi sekme yenilense bile tunel yasiyor. `#peer-id` URL
   bicimi korundu.

#### Testler

- 128 TS birim testi, 8 Go paketi (framing, ctrl, hedef allowlist/SSRF,
  baslik temizleme, Location yeniden yazimi, boyut, loglama maskeleme,
  tunel siralama regresyonlari).
- Gercek pion loopback baglantisi uzerinde ajan yarisinin uctan uca testi.
- **Playwright e2e (Chromium, 8 test): Faz 1 kabul kriterlerinin tamami
  yesil.** HTML/CSS/JS/gorsel/JSON, 5 MB dosya (sha256), tunel yoksa baglanti
  ekranina dusme ve 503, Location yeniden yazimi, SSE, SSRF.

#### Olcumler

Loopback, Chromium: 5 MB indirme ~103 ms (~48 MB/s), ctrl ping RTT medyan
0.2-0.3 ms, kucuk GET medyan ~1.2 ms / p95 ~2.3 ms. Ajan yarisi tek basina
5 MB'i 63 ms'de (~79 MB/s) tasiyor; aradaki fark SW koprusunun maliyeti.
Ayrinti: `docs/MEASUREMENTS.md`.

#### Dokumantasyon

- `docs/MANUAL-TEST.md`: iki pencere ve iki cihaz senaryosu, kontrol listesi,
  sorun giderme.
- `docs/MEASUREMENTS.md`: olculenler ve **olculmeyenler** (Firefox/Safari,
  gercek ag, eszamanli yuk).
- README bolum 8: iki karar kapatildi, Firefox/Safari acik kaldi.

### Faz 2 - Nostr signaling

Kopyala-yapistir kalkti: ziyaretci yalnizca URL'yi aciyor. Signaling herkese
acik Nostr relay'leri uzerinden; hala sunucu yok, relay'ler icerigi goremiyor
(NIP-44).

#### Degisen README kararlari (ikisi de soruldu ve onaylandi)

1. **peer-id = TAM npub** (ekranda kisaltilmis gosterim). README "npub'in kisa
   formu" diyordu ama NIP-44 ve `#p` tag'i tam 32 baytlik pubkey istiyor;
   kisaltilmis id'den turetilemez.
2. **Offer'i ZIYARETCI yayinlar**, ajan answer doner. Ajan ziyaretcinin gecici
   pubkey'ini onceden bilemedigi icin ilk mesaji gonderemez. Bu yonle mesaj
   listesi aynen gecerli kaldi ve baglanti 3 yerine 2 bacakta kuruluyor.
   Kanallari da ziyaretci aciyor; Session artik yalnizca answerer (tek kod
   yolu, elle signaling dahil).

#### Protokol

- `protocol/signaling.md` normatif tanim, `signaling-testvectors.json` makine
  karsiligi. Vektorler UCUNCU bir bagimsiz uygulama ile uretildi
  (`gen-signaling-vectors.py`); Go ve TS ikisi de ona karsi dogrulaniyor.
- Mesajlar: offer / answer / candidate / bye. `sid` (16 hex) oturum kimligi.
- `--secret` katmani NIP-44'un ALTINDA: HKDF-SHA256 + ChaCha20-Poly1305,
  `pps1.` onekli payload. Uc uygulama da ayni anahtari ve ayni nonce ile
  bayt bayt ayni payload'i uretiyor.

#### Ajan

- `internal/nostr`: relay havuzu (hedged yayin, onceden baglanma), NIP-44.
- `internal/nostr/fakerelay`: surec ici test relay'i; ephemeral dusurme,
  auth isteme, reddetme senaryolari.
- `internal/doctor` + `peerpages doctor`: NIP-11, baglanti ve **ephemeral
  yanki** testi; iki STUN sunucusuyla NAT tipi tahmini.
- `internal/identity` + `peerpages key`: keypair, `~/.peerpages/key` (nsec),
  peer-id. Var olan anahtarin uzerine YAZILMIYOR.
- `internal/connect`: offer'lari oturumlara ceviren akis + ziyaretci
  kurallari (replay, saat kaymasi, hiz limiti, oturum tavani).
- `serve` varsayilani Nostr; `--manual` elle yol. Yeni bayraklar: `--relay`,
  `--secret`, `--key`, `--app-url`, `--max-sessions`.

#### Web

- `src/nostr/`: peer-id ayristirma, relay havuzu, ziyaretci akisi, relay
  listesi (`?relay=`).
- Baglanti ekrani: URL'de peer-id varsa kendiliginden baglaniyor; hata
  ekranlari ("ajan yanit vermedi", "sir uyusmuyor", "veri kanallari
  acilmadi").

#### Olculen ve bulunan

- Relay listesi canli dogrulandi; `relay.nostr.band` bu agdan erisilemedigi
  icin varsayilandan cikarildi, yerine primal ve nostr.mom geldi.
- **Bu agda NAT simetrik** (iki STUN farkli dis port gordu): karsi taraf da
  simetrikse TURN gerekir (Faz 5). `doctor` bunu onceden soyluyor.
- **Gercek relay'lerle kosumda bulunan hata:** ajan abonelik kurulmadan
  "hazir" diyordu; ephemeral event'ler saklanmadigi icin hemen gelen offer
  kayboluyordu. Sira duzeltildi: baglan -> abone ol -> hazir de.
- URL'den acik tunele: 356 ms (sahte relay), **979 ms (gercek relay'ler)**.
- Bundle 6.8 KB -> 35.8 KB gzip; bip32/bip39/wasm girmedi.

#### Testler

216 TS birim testi, 13 Go paketi (connect akisi sahte relay uzerinde gercek
NIP-44 ve gercek WebRTC ile test ediliyor), 16 Playwright testi (8 elle yol +
8 Nostr yolu). Gercek relay'lerle Nostr e2e 7/7.

#### Kabul durumu

Faz 2'nin "farkli aglardaki iki cihaz" kriteri **kullanici dogrulamasi
bekliyor**: tek makineden test edilemez. Yontem `docs/MANUAL-TEST.md`'de.
