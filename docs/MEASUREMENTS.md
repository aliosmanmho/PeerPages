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
