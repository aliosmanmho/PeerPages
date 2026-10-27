# Ölçümler

## Faz 1 — 2026-10-26

Hepsi **tek makinede (loopback)** alındı: tarayıcı, ajan ve yayınlanan site
aynı bilgisayarda. Yani bunlar **ağ gecikmesi içermeyen taban değerler**:
protokolün ve zincirin kendi maliyetini gösterir, gerçek dünyadaki
laptop↔telefon rakamlarını değil. Ağ üstünden ölçüm Faz 2'de (Nostr
signaling ile iki gerçek cihaz) yapılacak.

**Ortam:** Windows 11, Go 1.27.0, Node 22.12.0, Chromium (Playwright 1.64),
pion/webrtc v4.2.25.

### Zincirin iki yarısı ayrı ayrı ölçüldü

**1) Yalnızca ajan yarısı** — gerçek WebRTC bağlantısı + gerçek HTTP sunucusu,
tarayıcı yok (`agent/internal/proxy` entegrasyon testi):

| Ölçüm | Değer |
|---|---|
| 5 MB indirme | 63 ms (~79 MB/s) |
| 5 MB'da ilk bayta kadar | < 1 ms |
| `ctrl` ping RTT | ortalama 80 µs, en kötü 0.5 ms (n=20) |
| Küçük GET (`/api/items`, 290 bayt) | ortalama 0.18–0.32 ms (n=20) |

**2) Tam zincir** — tarayıcı → Service Worker → tünel sayfası → WebRTC → ajan
→ localhost (Playwright e2e, üç ayrı koşum):

| Ölçüm | Değer |
|---|---|
| 5 MB indirme | 101 / 107 / 100 ms → **~47–50 MB/s** |
| `ctrl` ping RTT (sayfadan) | medyan 0.2–0.3 ms (n=10) |
| Küçük GET (SW + tünel) | medyan 1.15–1.35 ms, p95 2.3–2.4 ms (n=20) |

### Okunuşu

- **Service Worker köprüsünün maliyeti belirgin ama kabul edilebilir.**
  5 MB'da ajan yarısı 63 ms, tam zincir ~103 ms: aradaki ~40 ms
  `postMessage` + `ReadableStream` katmanının bedeli. Throughput 79 MB/s'den
  ~48 MB/s'ye düşüyor. Bir geliştirme sitesini paylaşmak için fazlasıyla
  yeterli; darboğaz gerçek kullanımda ağ olacak.
- **Küçük istekte ek gecikme ~1 ms.** Ajan yarısı ~0.2 ms, tam zincir ~1.2 ms.
  Yani SW köprüsü istek başına yaklaşık 1 ms ekliyor. Sayfa başına 50 istek
  için ~50 ms; fark edilmez.
- **İlk bayt hemen geliyor** (< 1 ms): gövde akıtılıyor, tam yanıt
  beklenmiyor. 5 MB'lık indirmede bellekte tüm gövde birikmiyor.
- **Backpressure çalışıyor:** 5 MB, 320 çerçeve (16 KiB) olarak gidiyor ve
  `bufferedAmount` 1 MiB eşiğinin üstüne çıkınca gönderim bekliyor. Çerçeve
  kaybı ya da sıra bozulması gözlenmedi.

### Henüz ölçülmeyenler (dürüstçe)

- **Firefox ve Safari.** README §8'deki "SW `ReadableStream` yanıtları ve
  `postMessage` performansı" sorusu **Chromium'da** yanıtlandı; Firefox ve
  Safari ölçülmedi. Faz 1 e2e'si yalnızca Chromium çalıştırıyor. Elle matris
  (README §7) Faz 4 öncesinde yapılacak; Safari'de streaming yanıtların
  davranışı ayrıca kontrol edilmeli.
- **Gerçek ağ.** İki cihaz, farklı ağlar, STUN/TURN ile gecikme ve throughput:
  Faz 2'nin kabul kriteriyle birlikte ölçülecek.
- **Eşzamanlı yük.** Tek sayfanın onlarca paralel isteği (gerçek bir SPA
  yüklenmesi) ölçülmedi; `--max-concurrent` varsayılanı 64.
- **Uzun süreli oturum.** Bellek sızıntısı ve uyku/uyanma Faz 4 konusu.

### Ölçümü yeniden üretme

```bash
# Ajan yarısı (tarayıcı yok)
cd agent && go test ./internal/proxy/ -run TestIntegration -v 2>&1 | grep OLCUM

# Tam zincir (Chromium)
cd web && npx playwright test 2>&1 | grep OLCUM
```

---

## Faz 2 — 2026-10-26 (Nostr signaling)

### Relay sağlığı (`peerpages doctor`, gerçek relay'ler)

Bu geliştirme ağından, gerçek public relay'lere karşı:

| Relay | Durum | Bağlantı | Yayın | Yankı |
|---|---|---|---|---|
| `wss://nos.lol` | uygun | 142 ms | 50 ms | 72 ms |
| `wss://relay.primal.net` | uygun | 238 ms | 83 ms | 78 ms |
| `wss://nostr.mom` | uygun | 137 ms | 47 ms | 101 ms |
| `wss://relay.damus.io` | uygun | 319 ms | 196 ms | 103 ms |
| `wss://relay.nostr.band` | **ulaşılamadı** | — | — | — |

"Yankı" = yayınlanan ephemeral event'in aboneye geri gelmesi. Bu adım en
önemlisi: relay event'i "OK" ile kabul edip sessizce düşürebilir.

### NAT / STUN

| | Görülen dış adres |
|---|---|
| `stun.l.google.com:19302` | `91.93.232.50:18450` |
| `stun1.l.google.com:19302` | `91.93.232.50:18836` |

**Farklı portlar → simetrik NAT.** Bu makine karşı tarafı da simetrik NAT
arkasındaysa doğrudan bağlanamaz; TURN gerekir (Faz 5). Tek makinede ve aynı
ağda sorun değil.

### URL'den açık tünele kadar

| Kurulum | Süre |
|---|---|
| Sahte relay (loopback) | **356 ms** |
| Gerçek relay'ler (nos.lol + primal + nostr.mom) | **979 ms** |

Yani ziyaretçi URL'yi açtıktan ~1 saniye sonra site geliyor. Bu süre
signaling'in gidiş-dönüşü + ICE kurulumu; relay gecikmesi baskın.

### Nostr ile kurulan tünelden veri

| Ölçüm | Değer |
|---|---|
| 5 MB indirme | 88–93 ms (~54–57 MB/s) |
| `ctrl` ping RTT | medyan 0.20 ms |
| Küçük GET (SW + tünel) | medyan 1.2–1.4 ms |

**Signaling yolu veri yolunu etkilemiyor** — beklenen davranış: signaling
yalnızca bağlantıyı kurar, veri doğrudan WebRTC üzerinden akar. Faz 1'deki
loopback değerleriyle aynı mertebede.

### Bundle boyutu

| | gzip |
|---|---|
| Faz 1 (Nostr yok) | 6.8 KB |
| Faz 2 (Nostr dahil) | **35.8 KB** |

+29 KB: tarayıcıda Nostr kriptosunun dürüst maliyeti (secp256k1 schnorr,
ChaCha20-Poly1305, HKDF, bech32). Kaynak haritasından doğrulandı: bundle'da
yalnızca `@noble/hashes`, `@noble/curves`, `@noble/ciphers`, `@scure/base` ve
`nostr-tools` var; `@scure/bip32`/`bip39`, wordlist ve `nostr-wasm`
**girmedi** (alt yol import'ları sayesinde).

### Henüz ölçülmeyenler (dürüstçe)

- **İki cihaz / iki ağ.** Faz 2'nin asıl kabul kriteri. Tek makineden
  doğrulanamaz; `docs/MANUAL-TEST.md` → "İki cihaz" bölümü. Bu ağda NAT
  simetrik olduğu için karşı taraf da simetrikse TURN (Faz 5) gerekebilir.
- **Firefox ve Safari.** Hâlâ ölçülmedi; e2e yalnızca Chromium koşuyor.
- **Relay dayanıklılığı.** Oturum ortasında relay düşmesi, yeniden bağlanma
  (Faz 4 konusu).
- **Eşzamanlı ziyaretçi.** `--max-sessions` varsayılanı 4; gerçek yük altında
  ölçülmedi.

### Ölçümü yeniden üretme

```bash
# Relay sağlığı ve NAT
cd agent && go run . doctor

# Sahte relay ile (deterministik)
cd web && npx playwright test nostr.spec.ts 2>&1 | grep OLCUM

# Gerçek relay'lerle
cd web && PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
```

---

## Faz 3 — 2026-10-26 (çoklu hedef ve ziyaretçi deneyimi)

### Bundle boyutu

| | gzip |
|---|---|
| Faz 1 (Nostr yok) | 6.8 KB |
| Faz 2 (Nostr dahil) | 35.8 KB |
| Faz 3 (+ QR kodlayıcı) | **39.7 KB** |

+3.9 KB: elle yazılan QR kodlayıcı (byte modu, seviye M, sürüm 1–10).
Hazır paket (`qrcode`) ayrı bir chunk olarak ~15–20 KB gzip gelirdi ve
ikinci bir çalışma zamanı bağımlılığı demekti.

### Veri yolu değişmedi

| Ölçüm | Faz 2 | Faz 3 |
|---|---|---|
| URL'den açık tünele (sahte relay) | 356 ms | 357 ms |
| URL'den açık tünele (gerçek relay) | 979 ms | 962–987 ms |
| 5 MB indirme (Nostr yolu) | 88–93 ms | 86–106 ms |
| 5 MB indirme (elle yol) | ~100 ms | 95 ms |
| `ctrl` ping RTT | 0.20 ms | 0.20 ms |
| Küçük GET (SW + tünel) | 1.2–1.4 ms | medyan 1.25 ms, p95 1.90 ms |

Beklenen sonuç: Faz 3 hedef seçimi ve ekran işi yaptı, veri yoluna
dokunmadı. Ajanın 10 saniyede bir yolladığı canlılık ping'i ölçülebilir bir
etki yapmıyor.

### QR

| | Değer |
|---|---|
| Ziyaretçi URL'si (npub + hedef) | 100 karakter → sürüm 5 |
| Canvas | 228 px (modül başına 4 px + 4 modül sessiz bölge) |
| Koyu modül oranı | 0.378–0.388 |

Koyu oran e2e'de kontrol ediliyor: boş ya da tamamen siyah bir canvas da
böyle yakalanır.

### Ajan sayaçları

`peerpages serve` stdout'una 5 saniyede bir, **yalnızca değiştiğinde** durum
satırı basıyor:

```
[durum] oturum 1 | istek 42 (basari 42, hata 0) | gelen 1.2 KB | giden 5.0 MB
```

Giden bayt yalnızca **gövde**: ctrl JSON ve çerçeve başlıkları sayılmıyor,
gerçek tel trafiği bundan biraz fazla.

### Boşta kalma denetimi

| | Değer |
|---|---|
| Canlılık ping aralığı | 10 sn |
| Boşta kalma sınırı | 30 sn |
| SW'de tünel sahibine güven süresi | 5 sn (sonra yeniden yoklama) |

Go testleri bu süreleri enjekte edilmiş kısa değerlerle doğruluyor
(`agent/internal/tunnel/idle_test.go`); e2e'de sekme kapanınca ajanın
oturumu bıraktığı kontrol ediliyor.

### Henüz ölçülmeyenler (dürüstçe)

- **İki cihaz / iki ağ ve QR'ın gerçekten okunması.** Tek makineden
  doğrulanamaz; `docs/MANUAL-TEST.md` → "İki cihazla deneme".
- **Tarayıcıdan öldürülen sekme.** Testlerde sekme düzgün kapanıyor, yani
  sayfa `bye` yolluyor. 30 saniyelik denetimin TEK koruma olduğu durum
  (tarayıcı çökmesi) elle denenmeli.
- **Firefox ve Safari.** Hâlâ ölçülmedi; e2e yalnızca Chromium koşuyor.
- **Eşzamanlı çok hedef yükü.** İki hedefe aynı anda ağır trafik ölçülmedi.

### Ölçümü yeniden üretme

```bash
# Faz 3 e2e (coklu hedef, ekran, QR, bosta kalma)
cd web && npx playwright test faz3.spec.ts 2>&1 | grep OLCUM

# Bundle boyutu
cd web && MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/

# Gercek relay'lerle (TEK relay ver: ust uste ajan baslatinca public
# relay'ler hizli tekrar baglantilari reddediyor)
cd web && PEERPAGES_E2E_RELAYS="wss://nos.lol" npx playwright test nostr.spec.ts
```

---

## Faz 4 — 2026-10-27 (dağıtım ve dayanıklılık)

### Yeniden bağlanma süresi

Ajan öldürülüp **aynı anahtarla** yeniden başlatıldı; ziyaretçi sekmesinde
hiçbir şeye tıklanmadı (`web/e2e/faz4.spec.ts`).

| Ne | Süre |
|---|---|
| Ajan yeniden başlayınca tünelin geri gelmesi | **6.3 sn** (iki koşumda 6.26 / 6.33 sn) |
| İlk geri sayım adımı | 1 sn |
| Ajanın "hazır" demesi (sahte relay) | ~0.3 sn |

6.3 saniyenin büyük kısmı geri sayımın ikinci adımı: ilk deneme ajan daha
dinlemeye başlamadan yapılıyor ve boşa gidiyor (ephemeral event saklanmaz),
ikinci deneme tutuyor. Gerçek relay'lerde bu süre relay gecikmesi kadar
uzar (Faz 2 ölçümü: URL'den tünele ~1 sn).

### Kopma boşluğunda istek

Tünel tam istek sırasında koparken ziyaretçi sekmesinden `GET /api/items`:
**200** döndü (önceki davranış: 502). Service Worker yeniden bağlanmayı
bekleyip isteği bir kez tekrar deniyor; bekleme üst sınırı alt kaynaklar
için 10 sn, gezinmeler için 3 sn.

### Üç platform binary'si (goreleaser, yerel anlık derleme)

| Platform | Arşiv |
|---|---|
| linux/amd64 | 7.1 MB |
| linux/arm64 | 6.5 MB |
| darwin/amd64 | 7.3 MB |
| darwin/arm64 | 6.8 MB |
| windows/amd64 | 7.3 MB |
| windows/arm64 | 6.5 MB |

Altı arşiv + `checksums.txt`. Derleme süresi (altı hedef, soğuk önbellek):
**1 dk 32 sn**; sıcak önbellekle 11 sn. `-trimpath`, `CGO_ENABLED=0`,
`-s -w`.

### Bundle boyutu

| | gzip |
|---|---|
| Faz 3 | 39.7 KB |
| Faz 4 (+ yeniden bağlanma denetleyicisi) | **40.7 KB** |

+1.0 KB: `src/reconnect.ts` ve durum ekranındaki yeni satırlar.

### Veri yolu değişmedi

Ping RTT medyan 0.30 ms, küçük GET medyan 1.50 ms / p95 2.50 ms,
5 MB indirme 260 ms — Faz 1–3 aralıklarının içinde. Yeni denetimlerin
(canlılık ping'i, relay sağlık yoklaması) ölçülebilir etkisi yok.

### Henüz ölçülmeyenler (dürüstçe)

- **Gerçek uyku/uyanma.** Laptop kapağı kapatılıp açılmadı; otomatik testler
  ajanı yeniden başlatarak aynı sonucu üretiyor ama aynı şey değil.
  Yöntem: `docs/MANUAL-TEST.md` → "Uyku/uyanma (Faz 4)".
- **Release workflow'unun GitHub Actions'ta koşması.** Hiç push yapılmadı,
  hiç etiket atılmadı. Yerelde `goreleaser check` + `--snapshot` doğrulandı;
  workflow'un kendisi **çalıştırılmadı**.
- **Uzun süreli relay dayanıklılığı.** Saatler boyunca açık kalan bir ajanın
  relay bağlantılarını kaç kez kaybettiği ölçülmedi.
- **Firefox ve Safari.** Hâlâ ölçülmedi (README §7 elle matris).

### Ölçümü yeniden üretme

```bash
# Faz 4 e2e (yeniden baglanma, relay dusmesi, SW bekleme penceresi)
cd web && npx playwright test faz4.spec.ts 2>&1 | grep OLCUM

# Uc platform binary'si (yayin YAPMAZ)
goreleaser check && goreleaser release --snapshot --clean
ls -la dist/
```

---

## Faz 5 — 2026-10-27 (TURN fallback)

### TURN'ün bedeli: doğrudan yol ile karşılaştırma

Aynı makinede, sahte TURN sunucusuyla (`peerpages faketurn`), aynı test
sitesinden. `relayOnly=true` ajanı yalnızca relay adayı toplamaya zorluyor,
yani doğrudan yol **imkânsız**.

| | Seçilen yol | Ping RTT (medyan) | 5 MB indirme | Küçük GET (medyan / p95) |
|---|---|---|---|---|
| Doğrudan | `prflx` | 0.30 ms | **96 ms** (52.2 MB/s) | 1.70 / 2.20 ms |
| TURN üzerinden | `relay` | 0.30 ms | **114 ms** (43.8 MB/s) | 1.60 / 2.10 ms |

**Okunuşu:** loopback'te TURN'ün ek sıçraması 5 MB'da ~%16 (18 ms) maliyet
çıkardı; ping ve küçük istekler ölçüm gürültüsünün içinde kaldı. Gerçek ağda
bedel bundan büyük olur: her paket TURN sunucusunun bulunduğu yere kadar
gidip döner, yani gecikmeye o mesafenin bir gidiş-dönüşü eklenir. Kota da
gerçek: 5 MB'lık indirme sağlayıcının kotasından 5 MB yer.

### TURN ile tünel kurulma süresi

| Ne | Süre |
|---|---|
| URL'den açık tünele (yalnızca TURN, sahte relay) | **306 ms** |
| Aynı yol, Go tarafı (tarayıcısız) | 3.1 sn |
| TURN allocation (`doctor`) | 52–57 ms |

Go testinin daha uzun sürmesi ICE değil kurulum: sahte relay aboneliğinin
oturması için beklenen 500 ms ve pion'un aday toplaması.

### ICE doğrudan yolu tercih ediyor

TURN verildiği hâlde `--ice-relay-only` olmadan koşulduğunda seçilen yol
`prflx` (doğrudan) çıktı. Yani TURN tanımlamak kotayı boşa harcamıyor;
relay yalnızca başka yol kalmadığında kullanılıyor.

### NAT / STUN (değişmedi)

Bu ağda `doctor` hâlâ **simetrik NAT** görüyor (iki STUN farklı dış port:
Faz 2 ölçümü). Faz 5'in tavsiye satırı artık bunu TURN durumuyla birleştirip
tek cümlede söylüyor: "NAT simetrik ama TURN hazır".

### Bundle boyutu

| | gzip |
|---|---|
| Faz 3 | 39.7 KB |
| Faz 4 | 40.7 KB |
| Faz 5 (+ ICE yapılandırması ve yol göstergesi) | **41.6 KB** |

+0.9 KB: `src/ice.ts` (STUN listesi, URL ayrıştırma, `getStats` okuması) ve
`#metrics`'teki yeni satır.

### Henüz ölçülmeyenler (dürüstçe)

- **Gerçek TURN sunucusu.** Tüm ölçümler süreç içi sahte sunucuyla; gerçek
  bir coturn ya da Cloudflare TURN üzerinden gecikme/throughput ölçülmedi.
- **Cloudflare Realtime TURN.** Gerçek hesaba karşı hiç çalıştırılmadı
  (`docs/TURN.md`).
- **İki ağ (hotspot ↔ Wi-Fi).** Faz 5'in asıl kabul denemesi; tek makineden
  yapılamaz, yöntem `docs/MANUAL-TEST.md`.
- **`turns:` (TLS) yolu.** Ajana verilebiliyor ama ölçülmedi; `doctor`'ın
  allocation denemesi TLS'i atlayıp UDP deniyor.

### Ölçümü yeniden üretme

```bash
# Faz 5 e2e (TURN uzerinden tunel, negatif kontrol, doctor raporu)
cd web && npx playwright test faz5.spec.ts 2>&1 | grep OLCUM

# Go tarafi (tarayicisiz): relay-only + faketurn
cd agent && go test ./internal/connect/ -run TURN -v

# doctor TURN kontrolu (sahte sunucuya karsi)
cd agent && go run . faketurn --port 0 --user u --pass p   # 1. terminal
cd agent && go run . doctor --turn turn:127.0.0.1:PORT --turn-user u --turn-pass p
```

## Faz 6 — 2026-10-27 (WebSocket ve SSE köprüsü)

Ölçümler aynı makinede (Windows 11, Chromium, loopback): ziyaretçi sekmesi,
tünel sahibi sekme ve ajan hepsi aynı bilgisayarda.

### WebSocket köprüsü

| Ne | Değer |
|---|---|
| Küçük mesaj gidiş-dönüş (medyan, n=20) | **0.40 ms** |
| 1 MB mesaj gidiş-dönüş (yani 2 MB tünel trafiği) | **40 ms** |
| Metin + ikili + 40 KB mesaj + kapanış turu | 10 ms |

Okunuşu: küçük bir ws mesajının turu, `ctrl` ping RTT'si (0.25 ms, Faz 1) ile
aynı mertebede — yani köprünün kendi maliyeti ölçüm gürültüsünün içinde. Yol
şu: ziyaretçi sekmesi → `MessagePort` → tünel sahibi sekme → data channel →
ajan → yerel ws sunucusu ve geri. 1 MB'lık mesaj iki yönde 64 çerçeveye
bölünüyor (16 KiB - 1 bayrak baytı).

### Vite HMR (kabul kriteri)

| Ne | Değer |
|---|---|
| Kaynak dosya değişiminden sayfanın güncellenmesine | **38 ms** |
| Sayfa yenilendi mi | **hayır** (window işareti duruyor) |

Hiçbir HMR ayarı verilmedi: Vite'ın istemcisi ws adresini sayfanın kendi
origin'inden türetiyor, shim de o adresi tünele alıyor. Alt protokol
(`vite-hmr`) ve token olduğu gibi geçiyor.

### SSE artımlı teslim

| Ne | Değer |
|---|---|
| İlk olay | **2 ms** |
| Üçüncü olay (sunucu 400 ms bekliyor) | 804 ms |

Bu, Faz 1'deki SSE testinin **ölçemediği** şey: o test `res.text()` ile tüm
gövdeyi bekliyordu, yani üç olay tek çerçevede gelse de geçerdi. İlk olayın
üçüncüden 800 ms önce gelmesi akışın gerçekten artımlı olduğunu gösteriyor.

### Zaman aşımı düzeltmesi

Faz 6'ya kadar `internal/proxy` istek bağlamını 60 saniyelik bir süreyle
sarıyordu ve bu **tüm cevabı** kapsıyordu: 60 saniyeden uzun yaşayan her akış
sessizce kesiliyordu. Artık sınır yalnızca başlık fazında. Go testleri bunu
hızlı bir şekilde sabitliyor (300 ms'lik bir sınırla, 4 katı kadar susan bir
upstream):

| Test | Ne kanıtlıyor |
|---|---|
| `TestServeBodyHasNoDeadline` | Gövde akarken süre sınırı yok |
| `TestServeHeaderPhaseStillTimesOut` | Cevapsız hedef isteği askıda tutamıyor (0.30 s) |
| `TestServeStreamStopsOnCancel` | Ziyaretçi vazgeçince akış hemen duruyor |

### Bundle boyutu

| | gzip |
|---|---|
| Faz 4 | 40.7 KB |
| Faz 5 | 41.6 KB |
| Faz 6 (+ soket API'si, enjektör, köprü tipleri) | **43.2 KB** |
| `ws-shim.js` (ayrı dosya, yalnızca bayrak açıkken yüklenir) | **1.9 KB** |
| `sw.js` | 2.7 KB |

Shim ayrı bir dosya ve app shell'in bundle'ına girmiyor: bayrak kapalıyken
ziyaretçi onu hiç indirmiyor.

### Henüz ölçülmeyenler (dürüstçe)

- **Socket.IO.** Otomatik test edilmedi (iki yeni bağımlılık); elle doğrulama
  `docs/MANUAL-TEST.md`'de. Yani "Socket.IO çalışıyor" iddiası ölçülmedi.
- **Gerçek ağda ws gecikmesi.** Tüm ws ölçümleri loopback; farklı ağlarda
  mesaj turu, bağlantının kendi gidiş-dönüş süresine ek olarak data channel
  üzerinden geçer.
- **Uzun süreli soket dayanıklılığı.** Dakikalar/saatler boyunca açık kalan
  bir soketin davranışı (uyku/uyanma, relay düşmesi) ölçülmedi; tünel
  koparsa soketler 1006 ile düşüyor ve yeniden bağlanmayı sayfanın kendi
  kütüphanesi yapıyor.
- **Firefox/Safari.** Shim ve `TransformStream` enjeksiyonu yalnızca
  Chromium'da koştu (e2e matrisi Chromium).

### Ölçümü yeniden üretme

```bash
# Faz 6 e2e (ws echo + olcumler, SSE, Vite HMR)
cd web && npx playwright test faz6.spec.ts 2>&1 | grep OLCUM

# Zaman asimi davranisi (Go, tarayicisiz)
cd agent && go test ./internal/proxy/ -run "Stream|Deadline|HeaderPhase" -v

# ws koprusu (Go, pion loopback)
cd agent && go test ./internal/tunnel/ -run TestWS -v

# Bundle boyutu
cd web && node scripts/build.mjs /PeerPages/
```
