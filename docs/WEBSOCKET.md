# WebSocket ve SSE (Faz 6)

## Kısa cevap

- **SSE (`text/event-stream`) kutudan çalışır**, bayrak gerekmez.
- **WebSocket için tek bayrak:** `--inject-ws-shim`. Varsayılan **kapalı**,
  çünkü sayfanın HTML'ine müdahale ediyor.

```bash
peerpages serve --target http://localhost:5173 --inject-ws-shim
```

## Neden ayrı bir köprü gerekti

Service Worker yalnızca `fetch` olaylarını yakalar; **WebSocket el sıkışması
onun kapsamında değil**. Yani shim olmadan, tünelden geçen bir sayfanın
`new WebSocket(...)` çağrısı gerçek ağa gider ve başarısız olur. Vite HMR,
Socket.IO ve canlı önizleme yapan her dev sunucusu tünelde sessizce yarım
çalışır: sayfa görünür ama canlı güncelleme gelmez.

## Nasıl çalışıyor

```
ziyaretçi sekmesi                         tünel sahibi sekme        senin makinen
┌───────────────────────┐                 ┌──────────────┐          ┌───────────┐
│ sayfa + ws-shim.js    │ MessageChannel  │ RTCPeerConn. │  ws-open │ localhost │
│  new WebSocket(...) ──┼──► SW ──port──► │  data channel├─────────►│   :5173   │
└───────────────────────┘                 └──────────────┘  ws yükü └───────────┘
```

1. Ajan `--inject-ws-shim` ile çalışıyorsa HTML cevaplarına
   `x-peerpages-ws-shim: 1` başlığını ekler. **Gövdeye dokunmaz.**
2. Service Worker bu başlığı görünce HTML akışına `<head>`'den sonra
   `<script src="…/ws-shim.js">` sokar ve o dosyayı kendi paketinden
   servis eder (tünelden geçmez).
3. Shim `window.WebSocket`'i sarar. Tünele aldığı adresler için bir
   `MessageChannel` kurup portu SW'ye yollar; SW tünel sahibi sekmeyi bulup
   portu ona **aktarır**. Sonrası sekme ↔ sekme: SW boşta öldürülebilir ve
   uzun yaşayan bir soket ona dayanamaz.
4. Tünelde üç kontrol mesajı var (`ws-open`, `ws-ok`, `ws-close`); yük
   `data` kanalından, çerçeve gövdesinde bir bayrak baytıyla gider
   (metin/ikili + son-parça). Ayrıntı: `protocol/README.md`.

> **Enjeksiyonu neden ajan değil SW yapıyor?** README'nin ilk planı HTML'i
> ajanın yeniden yazmasını öngörüyordu. Kapsam önekini (`/PeerPages/`) bilen
> taraf SW, ajan değil; ayrıca bu seçim `internal/proxy`'nin **bayt bayt
> geçirgen** kalmasını ve protokol sürümünün kırılmamasını sağlıyor. Politika
> ajanda (bayrak), mekanik SW'de. BRIEF'ten sapma; gerekçesi BRIEF §8.

## Shim hangi adresleri tünele alır

| Adres | Ne olur |
|---|---|
| `new WebSocket("ws")` (göreli) | tünelden geçer |
| `wss://<senin-sayfan>/yol` (aynı hostname) | tünelden geçer |
| `wss://<senin-sayfan>:5173/` (aynı hostname, farklı port) | tünelden geçer |
| `wss://baska-site.example/` | **gerçek** `WebSocket`'e gider |

Port neden önemsiz: Vite HMR adresine kendi portunu koyabiliyor, ama o port
ziyaretçinin tarayıcısında anlamsız — hedefi ajandaki allowlist belirler
(BRIEF ilke 1). Başka hostname'lere hiç dokunmuyoruz (ilke 5'in shim
karşılığı).

## Vite HMR

Çalışması için **hiçbir ayar gerekmez**: Vite'ın istemcisi ws adresini
sayfanın kendi origin'inden türetir, shim de o adresi tünele alır. E2E
testinde doğrulanıyor (`web/e2e/faz6.spec.ts`): kaynak dosya değişince sayfa
**yenilenmeden** güncelleniyor.

Yine de iki şey bilmeye değer:

- **`base` hizalaması.** Dev sunucunun ürettiği **kök-mutlak** yollar
  (`/src/main.js`) GitHub Pages proje sayfasında kapsam dışına düşer ve SW
  onları yakalamaz (BRIEF §8'deki yapısal sınır). Çaresi: dev sunucunun
  `base` değerini kapsam köküyle aynı yapıp ajan hedefine de o öneki vermek:

  ```bash
  # vite.config.js: base: "/PeerPages/"
  peerpages serve --target http://localhost:5173/PeerPages/ --inject-ws-shim
  ```

  Custom domain ya da kullanıcı sayfasında (`kullanici.github.io/`) kapsam
  kök olduğu için bu gerekmez.
- **`hmr.clientPort`.** Vite ws adresine kendi portunu koyarsa shim yine
  tünele alır (aynı hostname kuralı). Bir sorun çıkarsa
  `server.hmr.clientPort: 443` bunu kesinleştirir.

## Socket.IO

Çalışır ama **otomatik test edilmedi** (iki yeni bağımlılık getireceği için):

- Uzun yoklama (`polling`) aşaması normal HTTP olduğu için tünelden zaten
  geçiyor; Faz 6'dan önce 60 saniyelik zaman aşımı buna da takılıyordu, artık
  takılmıyor.
- WebSocket yükseltmesi shim üzerinden geçer. İkili yükler (`binaryType`)
  destekli.
- Sunucu istemciyi `io(location.origin)` ile aynı origin'den bağlıyorsa hiçbir
  ayar gerekmez.

Elle deneme adımları: `docs/MANUAL-TEST.md` → "WebSocket ile deneme (Faz 6)".

## Sınırlar ve bilinçli kararlar

- **Tek bayrak.** `--inject-ws-shim` köprüyü **ve** enjeksiyonu birlikte açar:
  shim olmadan hiçbir tarayıcı sayfası `ws-open` yollamaz, köprü olmadan shim
  işe yaramaz. Bayrak verilmediyse gelen `ws-open` istekleri `ws-close 1008`
  ile reddedilir.
- **`bufferedAmount` her zaman 0.** Gerçek gönderim tamponu tünelin içinde;
  shim'in o değeri taklit etmesi yanıltıcı olurdu. Pratikte etkisi:
  `bufferedAmount`'un düşmesini bekleyen kütüphaneler hiç beklemez.
- **Uzantı pazarlığı yok.** Alt protokol (`Sec-WebSocket-Protocol`) geçer,
  `permessage-deflate` gibi uzantılar geçmez (`extensions` boş string).
- **`Origin` hedefin kendi origin'i olarak gider.** Yerel sunucular (Vite,
  Socket.IO) `Origin` denetimi yapar ve ziyaretçinin `github.io` origin'ini
  reddederdi. Yani tünel, yerel sunucuya **yerel bir istemci gibi** görünür.
  Bu bilinçli bir seçim ve bir sınır: yerel sunucunun origin denetimi
  tünelden gelen trafiği ayırt edemez (BRIEF §8).
- **Tünel koparsa açık soketler düşer** (kod 1006). Faz 4'ün denetleyicisi
  yeni bir oturum kurar ama soketleri geri getirmez; yeniden bağlanmayı
  sayfanın kendi kütüphanesi yapar (Vite ve Socket.IO ikisi de yapar).
- **Mesaj tavanı** `--max-ws-message` (varsayılan 1MB), iki yönde de.
  Ziyaretçi güvenilmez (BRIEF ilke 4): sınırsız mesaj ajanın belleğini tek
  istekle şişirebilirdi. Aşılırsa `ws-close 1009`.

## Uzun yaşayan akış bütçesi

`--max-streams` (varsayılan 8) **ws oturumu + SSE cevabı** sayısını sınırlar:

```bash
peerpages serve -t http://localhost:3000 --inject-ws-shim --max-streams 16
```

Neden `--max-concurrent`'tan ayrı: uzun yaşayan bir akış slotunu ömrü boyunca
tutar, yani tek bütçe olsa 64 SSE isteği tüneli normal istekler için
kullanılamaz hale getirirdi. Bütçe dolduğunda SSE isteği
`abort too-many-streams`, ws isteği `ws-close 1013` alır — ikisi de sebebi
söyler.

Bir HTTP cevabı `content-type: text/event-stream` görüldüğü anda akış
bütçesine **taşınır**: hangi cevabın uzun yaşadığını önceden bilemiyoruz, bu
dürüst olan tek işaret.

## SSE hakkında bilinmesi gereken tek şey

Faz 6'dan önce ajandaki 60 saniyelik zaman aşımı **tüm cevabı** sarıyordu:
60 saniyeden uzun yaşayan her SSE akışı sessizce kesiliyordu — ziyaretçi hata
bile görmüyordu, yalnızca "akış bitti" görüyordu. Artık bu süre yalnızca
**cevap başlığının** gelmesi için; gövde akarken süre sınırı yok. Akışı
bitiren şeyler: ziyaretçinin vazgeçmesi, oturumun kapanması ya da yerel
sunucunun akışı bitirmesi.

Test ucu: `peerpages testsite` içinde `/sse?n=5&delay=400&keepalive=1`.

## Sorun giderme

| Belirti | Olası neden |
|---|---|
| `onclose` kodu **1008** | Köprü kapalı (`--inject-ws-shim` yok) ya da yerel sunucu el sıkışmayı reddetti (yolu kontrol et) |
| `onclose` kodu **1011** | Yerel sunucuya bağlanılamadı (sunucu ayakta mı, yol doğru mu) |
| `onclose` kodu **1009** | Mesaj `--max-ws-message` tavanını aştı |
| `onclose` kodu **1013** | Akış bütçesi dolu (`--max-streams`) |
| `onclose` kodu **1002** | Protokol hatası: bozuk çerçeve ya da sıra. Bir hata; `--verbose` ile ajan logunu paylaş |
| Shim hiç yüklenmiyor | Cevap HTML değil (yalnızca `text/html` ve `application/xhtml+xml` işaretlenir) ya da bayrak verilmemiş |
| Vite HMR bağlanmıyor | `base` hizalaması (yukarıda) ya da sayfanın CSP'si `script-src`'i daraltmış olabilir |
| SSE 60 saniyede kesiliyor | Faz 6 öncesi bir ajan kullanıyorsun (`peerpages version`) |
