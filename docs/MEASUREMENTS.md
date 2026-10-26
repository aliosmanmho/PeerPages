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
