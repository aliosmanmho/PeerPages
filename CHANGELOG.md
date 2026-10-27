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
    imkansiz (SSRF korumasi, BRIEF ilke 1).
  - `internal/logging`: varsayilan olarak URL path'leri maskelenir (ilke 6).
  - `internal/size`: boyut ifadeleri (1024 tabanli).
- Lint/format: `biome` (web), `gofmt` + `go vet` + `golangci-lint` (agent).
- `Makefile` + Windows icin `scripts/*.ps1` esdegerleri, `docs/DEV.md`.
- `.github/workflows/ci.yml`: web lint+test, agent lint+test (-race), e2e.

**Kabul (dogrulandi):** `npm run dev` -> `http://localhost:5173` 200 donuyor;
`peerpages --help`, `peerpages serve --help` ve `peerpages --version` calisiyor.

### Faz 1 - Cekirdek kanit: elle signaling + SW proxy

Signaling sunucusu ve Nostr YOK: offer/answer kopyala-yapistir ile tasiniyor.
Amac SW + WebRTC + reverse proxy zincirini kanitlamak (BRIEF Faz 1).

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
- BRIEF bolum 8: iki karar kapatildi, Firefox/Safari acik kaldi.

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

### Faz 3 - Coklu hedef ve ziyaretci deneyimi

Tek ajan birden cok yerel projeyi yayinliyor ve baglanti ekrani artik
anlamli bilgi gosteriyor.

#### Coklu hedef

- Hedef adi URL'de tasiniyor: `#npub1…/admin`. Fragment dilbilgisi
  `npub [ "/" hedef ] [ ":" sir ]`; '/' yalnizca ':' ONCESINDE araniyor,
  cunku sir serbest metin ve icinde '/' bulunabilir.
- Hedef signaling `offer`'inda tasiniyor (`protocol/signaling.md`), data
  channel protokolu ve Faz 1'in kanonik anahtar sirasi degismedi. Oturum
  basina tek hedef.
- Bilinmeyen hedef `bye`/`unknown-target` ile reddediliyor ve oturum HIC
  kurulmuyor. Sessizce `default`'a dusmek yok.
- Ajan her hedef icin ayri ziyaretci URL'si basiyor. Elle signaling yolunda
  hedefi `--manual-target` seciyor.

#### Baglanti ekrani

- Gecikme SUREKLI olculuyor (5 sn'de bir ping): son deger + medyan + n.
- Istek/hata, alinan ve gonderilen bayt, anlik hiz, devam eden istek, hedef
  listesi (ctrl `hello`'dan), aktif hedef.
- "Adresi kopyala" butonu (guvenli olmayan baglam icin execCommand yedegi —
  iki cihazla denemede clipboard API yok) ve QR paneli.

#### QR kodlayici

- `web/src/ui/qr.ts`: elle yazilmis, bagimlilik yok. Byte modu, seviye M,
  surum 1-10. Cizim yapmaz, matris donduru — DOM'suz test edilebiliyor.
- Vektorler UCUNCU bir bagimsiz uygulama (python `qrcode`) ile uretildi.
  15 maske-zorlanmis vektor bayt bayt, 5 vektor otomatik maske secimi.
  Vektorler iki hata yakaladi (bicim bilgisi kopyalari, veri yerlestirmede
  kolon kaydirmasi).
- Bundle 35.8 -> 39.7 KB gzip.

#### Sayaclar

- `agent/internal/stats`: hedef basina istek/basari/hata ve bayt, atomik.
- Hedefin 404/500'u BASARI sayiliyor: proxy isini yapti. Hata sayaci
  yalnizca ajanin cevabi tasiyamadigi durumlar.
- stdout'ta 5 sn'de bir, yalnizca degistiginde durum satiri.

#### Kaynak birakma

- Ajan 10 sn'de bir ctrl `ping` yolluyor, 30 sn yanitsiz oturumu kapatiyor.
  Protokol degismedi: sayfa gelen ping'e zaten pong donuyordu.
- SW bayatlamis tunel sahibini yeniden yokluyor (`clients.get` sekme hayatta
  oldugu surece client donduruyordu).
- SW tarafindaki tespit TEMBEL: istek gelmezse kontrol de olmaz. Zaman
  sinirli gercek koruma ajandaki denetim.

#### Yolda bulunan hata

Tavani asan govde cercevesi `req`'TEN ONCE gelirse sessizce dusuruluyordu:
govde hic gelmedigi icin proxy sonsuza kadar bekliyor, ziyaretci abort
almiyor, istek askida kaliyordu — tavan korumasi kaynak sizintisina
donusuyordu. Kirilgan bir test (6 kosumda 3 kirmizi) ortaya cikardi; sorun
testin degil davranisin kendisiydi.

#### Testler

276 TS birim testi, 15 Go paketi, 22 Playwright testi (8 elle yol +
8 Nostr + 6 Faz 3). Gercek relay'lerle (tek relay) Nostr e2e 7/7.

#### Kabul durumu

Faz 3 kabul kriteri (tek ajan iki projeyi yayinliyor, ekranda anlamli durum
bilgisi var) otomatik testlerle karsilandi. QR'in telefonla gercekten
okundugu ve iki cihaz/iki ag dogrulamasi kullanici tarafinda:
`docs/MANUAL-TEST.md`.

### Faz 4 - Dagitim ve dayaniklilik

Kopan baglanti artik kendini topluyor ve ajan kurulabilir hale geldi.

#### Dayaniklilik (ajan)

- Bosta kalma denetimi MONOTONIK saatle calisiyor. Onceki hali `lastSeen`'i
  unix nano olarak saklayip `time.Unix` ile geri okuyordu; boyle kurulan bir
  `time.Time` monotonik okumayi tasimaz, yani karsilastirma duvar saatiydi.
  Askiya alma suresi oldugu gibi "sessizlik" sayiliyor ve uyanmadan sonraki
  ILK TIK tum oturumlari kapatiyordu.
- Denetim kendi gecikmesine de bakiyor: iki tik arasi beklenenin 3 katindan
  fazlaysa bu bir uyanmadir, sessizlik sayaci sifirlanir ve oturum bir tur
  daha ping'le sinanir. Kapatma karari her zaman cevapsiz kalan bir ping'e
  dayaniyor.
- ICE `Disconnected` artik terminal degil: 10 saniyelik tolerans
  (`DisconnectGrace`); geri donerse oturum yasar, donmezse `ErrDisconnected`.
  Daha once aninda kapatiliyor ve TEMIZ kapanis gibi raporlaniyordu.
- Kapanis nedeni log'da: `bosta` / `baglanti-koptu` / `temiz`.
- `internal/nostr`: relay basina saglik (`Health`, `HealthyCount`).
- `Acceptor.Run` denetimli dongu: abonelik kapandiginda, hicbir relay bagli
  olmadiginda (3 yoklama) ya da surec askiya alinmis gorundugunde yeniden
  abone olur; her donemde filtre `since` tazelenir. Geri sayim 1→60 sn.
  Onceden abonelik kapandiginda Run hata donuyor ve ajan `exit 1` ile
  oluyordu.
- Durum satirinda `relay 3/4` alani.

#### Dayaniklilik (web)

- Yeni `src/reconnect.ts`: geri sayimli denetleyici (1-2-4-8-15-30 sn).
  Her deneme SIFIRDAN oturum kurar (yeni sid + yeni gecici keypair) — ajan
  answerer-only ve oturum tek seferlik oldugu icin ICE restart yerine bu yol
  secildi (BRIEF §8 sapma notu).
- `online` ve `visibilitychange` geri sayimi sifirlar (uyanma yolu).
- Canlilik denetimi: ust uste iki cevapsiz ping kopma sayilir. WebRTC durum
  makinesi her kopmayi bildirmiyor.
- Kopmada elle signaling paneli ACILMIYOR; ekran geri sayimi gosteriyor,
  sayaclar korunuyor, `#metrics`'te yeniden baglanma sayisi var.
- SW kopma boslugunda istegi bekletiyor: alt kaynaklar 10 sn, gezinmeler
  3 sn. Tam istek sirasinda kopan GET/HEAD bir kez tekrar deneniyor
  (yalnizca cevap basligi hic gitmediyse: hedeften gelen hatalar tekrar
  denenmez).

#### Dagitim

- `internal/version`: ldflags (-X) + `runtime/debug.ReadBuildInfo` yedegi.
  `go install ...@latest` ile kurulan binary de surumunu biliyor.
- Yeni `peerpages version` komutu (surum, commit, tarih, platform, Go).
- `.goreleaser.yaml`: linux/darwin/windows x amd64/arm64, CGO kapali,
  `-trimpath`, tar.gz (Windows'ta zip), `checksums.txt`. Release TASLAK
  acilir. Scoop/Homebrew taslak ama yukleme kapali (tap depolari yok).
- `.github/workflows/release.yml`: `v*` etiketinde goreleaser.
- `ci.yml`: ubuntu/macos/windows derleme matrisi.
- `docs/INSTALL.md`: binary indirme, `go install`, kaynaktan derleme,
  dogrulama, Windows'ta servis (Task Scheduler / NSSM — yalnizca belge).
- `Makefile`: `release-snapshot`, `release-check`; `lint-agent`'in sessiz
  gofmt hatasi duzeltildi (dosya listelese bile 0 donuyordu).
- `go mod tidy`: pion, go-nostr ve coder/websocket dogrudan bagimlilik.

#### Yolda bulunan hata

SW bekleme penceresinin ilk hali yalnizca "tunel sahibi sekme yok"
durumunda devreye giriyordu. Tunel tam istek sirasinda koparsa istek yine
dispatch ediliyor, sayfada hata aliyor ve ziyaretci **502** goruyordu — yani
pencere o yolda hic islemiyordu. Yeni e2e testi yakaladi.

#### Testler

285 TS birim testi (+9), 16 Go paketi, 26 Playwright testi (+4: yeniden
baglanma, kopma ekrani, relay dusmesi, SW bekleme penceresi).

OLCUM: ajan yeniden basladiktan sonra tunel 6.3 sn'de geri geldi; alti
platform arsivi 6.5-7.3 MB; bundle 39.7 → 40.7 KB gzip.

#### Kabul durumu

Yeniden baglanma kriteri otomatik testlerle karsilandi (ajanin yeniden
baslamasi, relay dusup donmesi, kopma boslugu). GERCEK uyku/uyanma elle
denenmeli: `docs/MANUAL-TEST.md` → "Uyku/uyanma (Faz 4)".
Release workflow'u YERELDE dogrulandi (`goreleaser check` + `--snapshot`:
6 arsiv + checksums); GitHub Actions'ta kosturulmadi, cunku hic push ya da
etiket yok (BRIEF §8).

### Faz 5 - TURN fallback

Baglanamayan aglara (simetrik NAT) cikis yolu. Protokol DEGISMEDI: ne
signaling ne data channel mesajlarina alan eklendi.

#### Karar: TURN yalnizca ajan tarafinda

Ziyaretciye ne TURN adresi ne kimlik bilgisi gonderiliyor. Zorlayan sebep
yapisal: ziyaretci offer'i ajandan tek kelime duymadan ONCE uretiyor, yani
TURN bilgisini signaling ile gondermek cok gec kalirdi. Tek tarafli TURN
yeterli (ajan relay adayi toplar, ziyaretci o genel adrese paket yollar) ve
kota ile sir sahibinde kaliyor.

#### Karar: Cloudflare Worker yazilmadi (BRIEF'ten sapma)

Worker'in tek isi API jetonunu TARAYICIDAN saklamakti; TURN'u yalnizca ajan
kullandigi ve jeton zaten sahibinin makinesinde durdugu icin aradaki sunucu
gereksiz. Ajan kimlik bilgisini API'den kendisi cekiyor. "Tek sunucu
gerektiren parca" tamamen ortadan kalkti.

#### Ajan

- Yeni `internal/ice`: ParseServerURL (stun/stuns/turn/turns, portsuz
  adrese 3478/5349, ?transport korunur, URL icinde kullanici bilgisi
  REDDEDILIR), Config.Validated(), Servers() (STUN ve TURN ayri girdiler:
  Username/Credential girdi basinadir), Policy(), Describe() (sifre asla
  yazilmaz), DefaultSTUN.
- `tunnel.Config.STUN` yerine `ICEServers` + `ICEPolicy`; `ICETransportPolicy`
  ilk kez kullaniliyor. `connect.Config.ICE` bir FONKSIYON (ICEFactory):
  Cloudflare kimlik bilgisi sureli, her yeni oturum gecerli listeyi aliyor.
- `serve`: --turn, --turn-user, --turn-pass (PEERPAGES_TURN_PASS),
  --ice-relay-only, --turn-cloudflare-key-id, --turn-cloudflare-token
  (PEERPAGES_CF_TURN_TOKEN). Baslangicta dogrulama ve banner'da `ice` satiri.
- `internal/ice/cloudflare.go`: kisa omurlu kimlik bilgisi, TTL'in %80'inde
  yenileme, yenileme basarisizsa eskisini kullanmaya devam. Ilk cekim
  basarisizsa ajan BASLAMAZ (sessizce TURN'suz devam etmek yanlis olur).
- `doctor`: her TURN sunucusundan ALLOCATION isteniyor ("ayakta mi" degil
  "relay adresi veriyor mu"), rapora relay adresi yaziliyor; Report.Advice
  NAT karariyla TURN sonucunu birlestiriyor; TURN istendi ama hicbiri
  allocation vermediyse cikis kodu sifirdan farkli. Simetrik NAT tek basina
  hata DEGIL.
- `internal/ice/faketurn` + gizli `peerpages faketurn` komutu (pion/turn v5,
  zaten dolayli bagimliliktaydi).

#### Web

- Yeni `src/ice.ts`: DEFAULT_STUN (Go tarafiyla ayni), normalizeStunURL,
  stunFromSearch (?stun=...), iceServers(), routeFromStats().
- Sayfa bu faza kadar HIC ICE sunucusu vermiyordu (yalnizca host adayi
  topluyordu); artik varsayilan STUN listesi veriliyor ve ziyaretci de kendi
  srflx adayini topluyor.
- `#metrics`'e "Baglanti yolu" satiri: dogrudan / TURN uzerinden
  (pc.getStats()'tan secilen aday cifti). Ziyaretci trafigin bir sunucudan
  gectigini goruyor; ucten uca sifreleme surer ama yol degisir.

#### Yolda ogrenilen

Sahte TURN sunucusunun DUYURDUGU adres, paket gonderirken kullandigi kaynak
adresle ayni olmali. Relay 127.0.0.1'e bagliyken karsi taraf (loopback adayi
uretmeyen pion ve tarayicilar) ulasamiyordu; tum arayuzlere baglanip
127.0.0.1 duyurulunca da tarayici gelen kontrolu peer-reflexive sayiyor ve
secilen aday cifti relay GORUNMUYORDU — test calisan bir TURN'u olcemiyordu.
Gercek bir TURN sunucusunda bu tutarlilik zaten var.

#### Testler

309 TS birim testi (+24), 18 Go paketi, 30 Playwright testi (+4). Go
tarafinda relay-only + faketurn ile tunel aciliyor ve negatif kontrol
(TURN yok) baglanamiyor.

OLCUM: 5 MB indirme dogrudan 96 ms / TURN uzerinden 114 ms (~%16);
URL'den TURN tuneline 306 ms; allocation 52-57 ms; bundle 40.7 -> 41.6 KB.

#### Kabul durumu

Kriter (simetrik NAT simulasyonunda TURN ile baglaniyor) otomatik testlerle
karsilandi: --ice-relay-only dogrudan yolu imkansiz kiliyor, tunel aciliyor
ve ekran "TURN uzerinden" diyor. GERCEK iki ag denemesi ve Cloudflare hesabi
kullanici tarafinda (docs/MANUAL-TEST.md, docs/TURN.md).

### Faz 6 - WebSocket ve SSE koprusu

#### Protokol

- Uc yeni ctrl mesaji: `ws-open` (id, path, protos), `ws-ok` (id, proto),
  `ws-close` (id, code, reason). `ws-frame` YOK: yuk `data` kanalindan
  gidiyor.
- ws cerceve alt bicimi: dis cerceve ([4B id][4B seq][payload]) AYNEN
  korundu, bir ws id'sine ait cercevenin payload'i [1B bayrak][parca] oldu
  (bayrak: metin/ikili + son-parca). Backpressure, 16 KiB parcalama ve seq
  denetimi bedavaya geldi; ikili veri base64 sismesi yemiyor ve toplu ws
  trafigi ctrl kanalini tikamiyor.
- ws oturumlari HTTP istekleriyle AYNI id uzayini paylasiyor.
- Vektorler iki dilde ayni dosyadan: 7 gecerli + 16 gecersiz ctrl, 6 gecerli
  + 4 gecersiz ws cerceve vektoru.

#### Ajan

- Yeni `internal/wsbridge`: coder/websocket ile yerel hedefe baglanti
  (zaten dogrudan bagimliliktaydi). Hedef URL'si YALNIZCA allowlist'ten
  (ilke 1). `Origin` basligi hedefin kendi origin'i olarak gidiyor, yoksa
  Vite ve Socket.IO el sikismayi reddediyor.
- `internal/tunnel`: ws oturum dagitimi, iki yonlu pompa, `WSDialer`
  arayuzu (nil = kopru kapali -> ws-close 1008). `Proxy` arayuzune metot
  EKLENMEDI (testlerde birkac yerde gercekleniyor).
- Yeni bayraklar: `--inject-ws-shim` (kopru + shim, varsayilan KAPALI),
  `--max-ws-message` (1MB), `--max-streams` (8).
- Test sitesi: `/sse?n=&delay=&keepalive=` (varsayilanlar degismedi) ve
  yeni `/ws` yanki ucu.

#### Web

- `Tunnel.openSocket`: mesaj bazli ws arayuzu; parcalama, FIN birlestirme,
  seq denetimi ve backpressure iceride.
- Yeni `src/ws-shim.ts` -> `dist/ws-shim.js` (klasik IIFE, ucuncu vite
  yapilandirmasi). Yalnizca AYNI HOSTNAME'e giden ws adreslerini tunele
  aliyor; scope onekini `document.currentScript.src`'den hesapliyor.
- Yeni `src/inject.ts`: HTML akisina `<script>` sokan TransformStream
  (etiket cerceveye bolunmus olabilir; 8 KiB sonra basa yazar).
- SW: shim el sikismasinda portu tunel sahibi sekmeye AKTARIYOR, sonrasi
  sekme <-> sekme. SW bosta oldurulebilir, uzun yasayan bir soket ona
  dayanamaz.
- `#metrics`'e kosullu "WebSocket: N acik" satiri.

#### Yolda bulunan hata

`internal/proxy` istek baglamini 60 saniyelik bir sureyle sariyordu ve bu
TUM cevabi kapsiyordu: 60 saniyeden uzun yasayan her SSE akisi sessizce
kesiliyordu -- ziyaretci hata gormuyor, yalnizca "akis bitti" goruyordu.
Sinir artik Transport'ta ve yalnizca baslik fazinda. Cevapsiz kalan hedef
hala istegi askida tutamiyor.

#### Kararlar (BRIEF'ten sapma)

Shim'i HTML'e ajan degil Service Worker sokuyor; ajan yalnizca
`x-peerpages-ws-shim` basligini ekliyor. Boylece protokol surumu kirilmiyor
(`hello` tek yonlu ve opsiyonel alan kabul etmiyor) ve `internal/proxy`
bayt bayt gecirgen kaliyor. Gerekcesi BRIEF §8, ayrinti docs/WEBSOCKET.md.

#### Testler

397 TS birim testi (+88), 19 Go paketi (yeni: `internal/wsbridge`), 35 Playwright
testi (+5).

OLCUM ws kucuk mesaj RTT medyan 0.40 ms; 1 MB gidis-donus 40 ms;
Vite HMR guncellemesi 38 ms (sayfa yenilenmeden); SSE ilk olay 2 ms /
ucuncu 804 ms; bundle 41.6 -> 43.2 KB + ayri 1.9 KB shim.

#### Kabul durumu

Vite HMR kriteri otomatik testle karsilandi: fixture projede `vite dev`
kosuyor, kaynak dosya degisiyor ve sayfa YENILENMEDEN guncelleniyor.
Socket.IO ornegi elle dogrulamada (docs/MANUAL-TEST.md): iki yeni
bagimlilik getirecegi icin otomatik test edilmedi.
