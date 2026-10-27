# PeerPages — iç brief ve yol haritası

> **Bu dosya ne:** projenin iç brief'i, faz planı ve karar defteri (§8).
> Faz 0–6 boyunca repo kökündeki `README.md` idi; Faz 7'de kök artık
> **public İngilizce README**, bu dosya da `docs/BRIEF.md` oldu.
>
> **Koddaki atıflar:** kaynak dosyalardaki `BRIEF §8`, `BRIEF ilke 1`,
> `BRIEF bolum 4` gibi yorumlar bu dosyayı gösterir; bölüm numaraları
> taşınırken korundu. Altı güvenlik ilkesi (§6) **aynı numaralarla** public
> README'de de duruyor.
>
> **İsim:** `PeerPages` Faz 7'de kesinleşti (bkz. §5 Faz 7).

## 1. Tek cümlelik fikir

Ziyaretçi `https://<kullanıcı>.github.io/peerpages/#<peer-id>` adresini açar; sayfa bir Service Worker kurar, senin makinendeki ajanla WebRTC data channel açar ve senin **localhost:3000**'deki siten ziyaretçinin tarayıcısında github.io adresi altında normal bir site gibi çalışır. Arada **hiçbir sunucu yok**: statik sayfa GitHub Pages'te, signaling herkese açık Nostr relay'lerinde, trafik uçtan uca DTLS ile şifreli, ajan tek binary.

Rakiplerden farkı (bunu README'de ve lansmanda vurgula):

| | ngrok / Cloudflare Tunnel | web-p2p-tunnel (2024) | **PeerPages** |
|---|---|---|---|
| Relay sunucu | Var (üçüncü taraf) | Yok | Yok |
| Signaling sunucu | — | Var (kendi WebSocket sunucusu) | **Yok** (Nostr) |
| Kayıt / hesap | Gerekli | Gerekli değil | **Gerekli değil** |
| Ziyaretçiye kurulum | Yok | Yok | Yok |
| Trafiği kim görür | Sağlayıcı | Kimse | **Kimse** |
| Ücret | Ücretsiz katman sınırlı | Ücretsiz | **Ücretsiz** |

## 2. Mimari

```
Ziyaretçi tarayıcısı                              Senin makinen
┌──────────────────────────────┐                  ┌──────────────────────────┐
│ github.io/peerpages/#abc123  │                  │ peerpages agent (Go)     │
│  ├─ app shell (Vite+TS)      │   WebRTC DC      │  ├─ Nostr signaling      │
│  ├─ Service Worker ──fetch──►│◄════════════════►│  ├─ pion/webrtc          │
│  └─ RTCPeerConnection        │   (DTLS, E2E)    │  └─ reverse proxy ──────►│ localhost:3000
└──────────────┬───────────────┘                  └────────────┬─────────────┘
               │ offer/answer/ICE (NIP-44 şifreli, ephemeral event)         │
               └──────────────► Nostr relay'leri (wss://…) ◄────────────────┘
                                 (public, ücretsiz, en az 3 tane)
Opsiyonel: TURN fallback — yalnızca AJAN tarafında, kimlik bilgisi ziyaretçiye gitmez (Faz 5)
```

### Bileşenler

- **`web/`** — Statik uygulama. Vite + TypeScript, framework yok (bundle küçük kalsın). İki parça:
  - `app`: bağlantı ekranı, Nostr signaling istemcisi, `RTCPeerConnection`, SW'ye köprü (`postMessage` / `MessageChannel`).
  - `sw.ts`: Service Worker. Kapsamındaki tüm `fetch` olaylarını yakalar, aktif tünel sayfasına iletir, cevabı `Response` olarak döndürür (streaming).
- **`agent/`** — Go, tek binary (Windows/macOS/Linux, goreleaser). `pion/webrtc` + `nbd-wtf/go-nostr` (ya da eşdeğeri). Yerel hedeflere reverse proxy.
- **`protocol/`** — Data channel üzerindeki mesaj formatı (aşağıda). Hem TS hem Go tarafında aynı sabitler.
- **`.github/workflows/`** — `web/` → GitHub Pages deploy; `agent/` → release binary'leri.

> Neden Go, .NET değil? Tek statik binary, `go install`, Homebrew/Scoop dağıtımı ve pion ekosistemi. .NET istenirse SIPSorcery ile ikinci ajan yazılabilir ama MVP Go.

### Service Worker neden gerekli ve sınırları

- SW olmadan ziyaretçi sayfası sadece "iframe içinde HTML gösteren bir viewer" olurdu; SW ile relative link, CSS, JS, görsel, XHR/fetch dahil **her şey** şeffaf çalışır.
- SW içinde WebRTC yok → WebRTC bağlantısını **sayfa** tutar, SW isteği sayfaya `postMessage` ile yollar. Tünel sayfası kapalıysa SW "bağlan" ekranına yönlendirir.
- Aynı origin'de aynı anda **tek aktif tünel** olur (SW kapsamı origin bazlı). Birden fazla proje `#peer-id/proje-adi` ile seçilir, ajan tarafında hedef eşlemesi yapılır.
- Yerel uygulamanın kendi WebSocket'lerini SW yakalayamaz (`fetch` olayı WebSocket el sıkışmasını
  kapsamaz) → Faz 6'da ayrı köprü: `--inject-ws-shim` ile sayfaya `window.WebSocket`'i saran bir
  shim enjekte edilir ve yük data channel üzerinden taşınır (`docs/WEBSOCKET.md`).
- Arama motorları içeriği görmez. Bu bir demo/paylaşım aracı; SEO hedefi yok.

## 3. Protokol (data channel)

İki data channel: `ctrl` (ordered, reliable, JSON) ve `data` (ordered, reliable, binary). Alternatif: tek channel + 1 byte tip prefiksi; MVP'de iki channel daha okunaklı.

**ctrl (JSON):**
```jsonc
{ "t": "req",  "id": 17, "method": "GET", "path": "/api/items?x=1", "headers": {"accept": "..."}, "hasBody": false }
{ "t": "res",  "id": 17, "status": 200, "headers": {"content-type": "text/html"} }
{ "t": "end",  "id": 17 }                 // gövde bitti
{ "t": "abort","id": 17, "reason": "client-cancel" }
{ "t": "ping" } / { "t": "pong" }
{ "t": "hello","v": 1, "targets": ["default","admin"], "auth": "required|none" }
```

**data (binary):** `[4 byte id BE][4 byte seq BE][payload ≤ 16 KiB]` — hem istek gövdesi hem cevap gövdesi aynı framing. 16 KiB sınırı tarayıcı data channel uyumluluğu için; `bufferedAmountLowThreshold` ile backpressure uygula.

Kurallar:
- `host`, `cookie`, `connection`, `transfer-encoding` gibi hop-by-hop ve origin'e bağlı başlıklar ajan tarafında temizlenir/yeniden yazılır.
- Ajan **yalnızca** komut satırında verilen hedeflere proxy yapar; path'ten host türetmek yasak (SSRF önlemi).
- `Location` başlığındaki `http://localhost:3000/...` değerleri ziyaretçi origin'ine çevrilir.
- Cevaplar SW'de `ReadableStream` ile akıtılır; tam gövde beklenmez.

## 4. Signaling (Nostr)

- Ajan başlarken bir Nostr keypair üretir (ya da `~/.peerpages/key` dosyasından okur; nsec olarak tutulur). **peer-id = TAM npub** — URL'de `#` sonrası taşınır.
  - Faz 2'de düzeltildi: önce "npub'ın kısa formu (ilk 12 karakter)" yazıyordu, ama NIP-44 şifreleme ve `#p` tag'i tam 32 baytlık pubkey gerektiriyor; kısaltılmış id'den türetilemez ve relay'lerde pubkey önek araması güvenilir değil. **Okunur form ekranda**: `npub1yl4dshp…pdnn` gösterilir, URL tamını taşır. Gerekçe: §8.
- Ziyaretçi sayfası da geçici keypair üretir.
- Event türü: ephemeral kind **25050**. İçerik **NIP-44** ile şifreli, `#p` tag'i alıcı pubkey. Mesaj tipleri: `offer`, `answer`, `candidate`, `bye`.
- **Offer'ı ziyaretçi yayınlar, ajan `answer` döner.** Faz 2'de düzeltildi: önce "ajan offer yayınlar" yazıyordu, ama ajan ziyaretçinin geçici pubkey'ini önceden bilemediği için ilk mesajı gönderemez (kime şifreleyeceğini bilemez). Bu yönle mesaj listesi aynen geçerli kalıyor ve bağlantı 3 yerine 2 bacakta kuruluyor. Veri kanallarını da ziyaretçi açar. Normatif tanım: `protocol/signaling.md`.
- Varsayılan relay listesi (kullanıcı `--relay` ya da `?relay=` ile değiştirebilir), **Faz 2'de `peerpages doctor` ile canlı doğrulandı**:
  `wss://nos.lol`, `wss://relay.primal.net`, `wss://nostr.mom`, `wss://relay.damus.io` (dördü de ephemeral kind 25050'yi kabul edip aboneye iletti).
  `wss://relay.nostr.band` listeden çıkarıldı: geliştirme ağından WebSocket el sıkışması zaman aşımına uğradı. Dört tutuyoruz ki biri düşünce yine 3 kalsın. NIP-42 auth isteyen relay'leri `doctor` işaretliyor ve atlıyoruz.
- Aynı mesajı tüm relay'lere yolla, ilk gelene göre devam et (hedged). Relay düşerse yeniden bağlan.
- **Çoklu hedef (Faz 3):** hedef adı URL'de `#npub1…/admin` biçiminde taşınır ve signaling
  `offer`'ına konur. Oturum başına tek hedef: tünel kurulduktan sonra değişmez. Hedef adlarının
  listesi tünel açılınca ctrl `hello` mesajında da bildirilir. Bilinmeyen hedef reddedilir
  (`bye`/`unknown-target`).
- Opsiyonel paylaşılan sır: ajan `--secret` ile başlatılırsa URL `#peer-id:secret` olur; sır signaling içeriğini **NIP-44'ün altında** ek olarak şifreler (HKDF-SHA256 + ChaCha20-Poly1305) ve ajan sırsız/yanlış sırlı teklifleri reddeder. Sırrın **neyi korumadığı** da yazılı: relay'ler yine "bu npub bir şey aldı" görür. Ayrıntı: `protocol/signaling.md`.

## 5. Fazlar

Her faz **tamamlandığında**: testler yeşil, `CHANGELOG.md` güncel, commit mesajı `faz-N: ...` formatında. Bir faz bitmeden sonrakine geçme.

### Faz 0 — İskelet (yarım gün)
- Monorepo: `web/`, `agent/`, `protocol/`, `docs/`, `.github/workflows/`.
- `web/`: Vite + TS, `vite.config.ts` içinde `base` GitHub Pages alt yolu için ayarlanabilir. `sw.ts` ayrı entry (`vite-plugin-pwa` **kullanma**, SW'yi elle derle — tam kontrol lazım).
- `agent/`: `go mod init`, `cobra` CLI, `peerpages serve --target http://localhost:3000` iskeleti, loglama.
- Lint/format: `biome` (web), `golangci-lint` (agent). `make dev`, `make test`.
- MIT lisans, `CONTRIBUTING.md` taslağı.

**Kabul:** `make dev` ile web `localhost:5173`'te açılıyor, agent `--help` çalışıyor.

### Faz 1 — Çekirdek kanıt: manuel signaling + SW proxy (1–2 gün)
Signaling'i **kopyala-yapıştır** ile yap (sunucu yok, Nostr yok): ajan `offer`'ı stdout'a basar, ziyaretçi sayfasına yapıştırılır, sayfa `answer` üretir, ajana yapıştırılır. Amaç: SW + data channel + reverse proxy zincirinin gerçekten çalıştığını kanıtlamak.

- `protocol/` framing'i TS ve Go'da uygula, ikisinde de birim testi.
- SW: `fetch` yakalama, sayfaya `MessageChannel` ile iletim, `ReadableStream` ile cevap.
- Ajan: `net/http` reverse proxy, başlık temizleme, gövde chunk'lama, backpressure.
- Test hedefi olarak `agent/testdata/site/` içine statik bir site + küçük bir JSON API koy.

**Kabul:** Ziyaretçi tarayıcısında test sitesi (HTML, CSS, JS, görsel, fetch ile JSON) github.io benzeri origin'de eksiksiz render oluyor; 5 MB dosya indirme çalışıyor; sayfa yenilenince SW tünel yoksa bağlantı ekranına düşüyor.

### Faz 2 — Nostr signaling (1–2 gün)
- Ajan: keypair, relay bağlantısı, `offer` yayınlama, `answer`/`candidate` dinleme, NIP-44.
- Web: aynı akışın TS tarafı (`nostr-tools`). `#peer-id` URL'den okunur.
- Trickle ICE: candidate'ları ayrı event olarak gönder.
- Relay seçimi, timeout (30 sn), hata ekranları ("ajan çevrimdışı", "relay'lere ulaşılamadı").
- `--secret` desteği.

**Kabul:** Farklı ağlardaki iki cihaz (ör. telefon mobil veri + laptop ev Wi-Fi) sadece URL ile bağlanıyor; manuel signaling yolu `--manual` bayrağına taşındı, varsayılan Nostr.

### Faz 3 — Çoklu hedef ve ziyaretçi deneyimi (1 gün) — **tamamlandı**
- `--target default=http://localhost:3000 --target admin=http://localhost:4000`; URL `#peer-id/admin`.
  - Hedef adı signaling `offer`'ında taşınır (`protocol/signaling.md`); oturum başına sabittir.
    Bilinmeyen hedef `bye`/`unknown-target` ile **reddedilir** — sessizce `default`'a düşmek yok.
  - Elle signaling yolunda URL olmadığı için hedefi `--manual-target` seçer.
- Bağlantı ekranı: durum (SW → signaling → ICE → tünel), **sürekli** ölçülen gecikme (5 sn'de bir
  ping, son değer + medyan), istek/hata, alınan ve gönderilen byte, anlık hız, hedef listesi,
  aktif hedef, "kopyala" butonu ve QR kod.
  - QR kodlayıcı **elle yazıldı** (`web/src/ui/qr.ts`, byte modu / seviye M / sürüm 1–10): web
    tarafında tek çalışma zamanı bağımlılığı (`nostr-tools`) olarak kalsın diye. Doğrulaması
    üçüncü bir bağımsız uygulamayla üretilen vektörlere karşı.
- `hello` mesajıyla hedef listesi; ajan tarafında hedef başına istek/başarı/hata ve byte sayacı
  (`agent/internal/stats`), stdout'ta yalnızca değiştiğinde yenilenen durum satırı.
- Ziyaretçi sekmeyi kapatınca kaynak bırakılır: **ajan** 10 sn'de bir ctrl `ping` yollar ve 30 sn
  yanıt gelmezse oturumu kapatır; **SW** bayatlamış tünel sahibini yeniden yoklar.
  Gerekçe ve sınırlar: §8.

**Kabul:** Tek ajan iki projeyi yayınlıyor; ekranda anlamlı durum bilgisi var.
**Durum:** karşılandı — `web/e2e/faz3.spec.ts` (6 test) bunu otomatik doğruluyor.

### Faz 4 — Dağıtım ve dayanıklılık (1 gün) — **tamamlandı**
- Otomatik yeniden bağlanma (relay düşmesi, uyku/uyanma).
  - Ziyaretçi tarafı geri sayımla (1→2→4→8→15→30 sn) **sıfırdan yeni oturum** kurar:
    yeni `sid`, yeni geçici keypair, yeni offer. **ICE restart yapılmıyor** — gerekçe §8.
  - `online` ve `visibilitychange` geri sayımı sıfırlar (uyanma yolu). Canlılık denetimi:
    üst üste iki cevapsız ping kopma sayılır.
  - Ajan tarafı: ICE `Disconnected` artık 10 saniyelik tolerans penceresiyle karşılanıyor,
    boşta kalma denetimi monotonik saatle çalışıyor ve kendi gecikmesinden uyanmayı tanıyor,
    signaling aboneliği denetlenip yenileniyor, `relay 3/4` alanı durum satırında.
  - Service Worker kopma boşluğunda isteği hemen 503'lemiyor: alt kaynaklar 10 sn,
    gezinmeler 3 sn bekliyor; tam istek sırasında kopan GET/HEAD bir kez tekrar deneniyor.
- goreleaser: Windows/macOS/Linux × amd64/arm64, `go install github.com/aliosmanmho/PeerPages/agent@latest`,
  Scoop/Homebrew tap **taslağı** (tap depoları yok, yükleme kapalı). Release **taslak** açılır.
  `peerpages version` sürüm/commit/tarih/platform basar; `go install` ile kurulan binary de
  (ldflags olmadan) sürümünü bilir.
- `peerpages key` ve `peerpages doctor` Faz 2'de geldi; `docs/INSTALL.md` son kullanıcı kurulumu.
- Windows'ta servis olarak çalışma notu (NSSM/Task Scheduler) — `docs/INSTALL.md`, sadece dokümantasyon.

**Kabul:** Laptop uyuyup uyanınca tünel kendini toparlıyor; release workflow üç platform binary'si üretiyor.
**Durum:** yeniden bağlanma karşılandı — `web/e2e/faz4.spec.ts` (4 test) ajanın yeniden başlamasını,
relay düşüp dönmesini ve kopma boşluğunu otomatik doğruluyor; **gerçek uyku/uyanma elle denenmeli**
(`docs/MANUAL-TEST.md`). Binary tarafı yerelde doğrulandı (6 arşiv + checksums); **workflow GitHub'da
koşturulmadı**, çünkü hiç push/etiket yok (§8).

### Faz 5 — TURN fallback (yarım gün) — **tamamlandı**
- `--turn <url> --turn-user --turn-pass` ile kullanıcı kendi TURN'ünü verebilir
  (`PEERPAGES_TURN_PASS` ortam değişkeni de okunur: komut satırı argümanları `ps`
  çıktısında görünür). `--ice-relay-only` teşhis için doğrudan yolu imkânsız kılar.
- **TURN yalnızca ajan tarafında**; ziyaretçiye kimlik bilgisi hiç gönderilmez. Gerekçe §8.
- Cloudflare Realtime TURN: ajan kısa ömürlü kimlik bilgisini **API'den kendisi çeker** ve
  süresi bitmeden yeniler (`--turn-cloudflare-key-id`, `PEERPAGES_CF_TURN_TOKEN`).
  **Worker yazılmadı** — gerekçe §8; "tek sunucu gerektiren parça" ortadan kalktı.
- `doctor` her TURN sunucusundan **allocation ister** ("ayakta mı" değil "relay adresi
  veriyor mu") ve NAT kararıyla birleştiren tek satırlık tavsiye basar.
- Bağlantı ekranı seçilen yolu gösteriyor: doğrudan / TURN üzerinden (`pc.getStats()`).
- Ziyaretçi tarafına da STUN geldi: bu faza kadar sayfa **hiç** ICE sunucusu vermiyordu
  (yalnızca host adayı topluyordu, §8).
- Kurulum ve güvenlik notları: [`docs/TURN.md`](docs/TURN.md).

**Kabul:** Simetrik NAT simülasyonunda (ör. mobil hotspot ↔ kurumsal ağ) TURN ile bağlanıyor.
**Durum:** karşılandı — ajan `--ice-relay-only` ile yalnızca relay adayı topladığında doğrudan
yol **imkânsız** olur; `web/e2e/faz5.spec.ts` (4 test) ve `agent/internal/connect/turn_test.go`
tünelin bu durumda açıldığını, negatif kontrolde (TURN yok) açılmadığını doğruluyor.
Gerçek iki ağ denemesi ve Cloudflare hesabı sende (`docs/MANUAL-TEST.md`, `docs/TURN.md`).

### Faz 6 — WebSocket ve SSE köprüsü (1 gün) — **tamamlandı**
- SSE zaten streaming cevapla çalışmalı; test ekle.
- WebSocket: sayfaya enjekte edilen küçük bir shim `window.WebSocket`'i sarar, data channel üzerinde `ws-open/ws-frame/ws-close` mesajlarıyla ajan tarafında gerçek WS'e köprüler. Shim yalnızca `--inject-ws-shim` ile açılır (HTML'e müdahale ediyoruz, varsayılan kapalı).

**Kabul:** Vite HMR ve Socket.IO örneği tünel üzerinden çalışıyor.
**Durum:** Vite HMR **karşılandı ve otomatik** — `web/e2e/faz6.spec.ts` fixture bir projede
`vite dev` başlatıp kaynak dosyayı değiştiriyor, sayfa **yenilenmeden** güncelleniyor (38 ms).
SSE'nin artımlı aktığı da artık ölçülüyor (ilk olay 2 ms, üçüncü 804 ms); Faz 6'ya kadar
ajandaki 60 saniyelik zaman aşımı tüm cevabı sarıyor ve uzun akışları sessizce kesiyordu.
Socket.IO örneği **elle** (`docs/MANUAL-TEST.md`): iki yeni bağımlılık getireceği için
otomatik test edilmedi. Ayrıntı: `docs/WEBSOCKET.md`.

### Faz 7 — Yayın ve lansman (1 gün)
- GitHub Pages deploy workflow (`web/dist` → Pages, Actions kaynağı), custom domain rehberi (`CNAME`, `demo.alanadi.com`).
- Public **İngilizce README**: 10 saniyelik GIF, "Why" bölümü, karşılaştırma tablosu, güvenlik modeli, sınırlar (dürüstçe), FAQ.
- `docs/SECURITY.md`: tehdit modeli (peer-id tahmini, relay gözlemi, SSRF, ziyaretçi kötü niyetli olabilir).
- `docs/LAUNCH.md`: Show HN başlığı + ilk yorum, r/selfhosted ve r/webdev metinleri, X/LinkedIn (Pendra ile) kısa metin.
- Son isim kararı ve domain kontrolü.

**Kabul:** Yabancı biri README'yi okuyup 3 dakikada kendi localhost'unu paylaşabiliyor.

## 6. Güvenlik ilkeleri (her fazda geçerli)

1. Ajan asla CLI'da verilmeyen bir hosta istek atmaz.
2. Signaling içeriği her zaman şifreli; relay'ler sadece "bir pubkey bir şey aldı" görür.
3. Peer-id en az 64 bit entropi; `--secret` ile ek katman.
4. Ziyaretçi tarafı güvenilmez kabul edilir: path/header doğrulama, gövde boyut limiti (`--max-body 50MB`), istek sayısı limiti.
5. SW yalnızca kendi kapsamındaki istekleri yakalar; başka origin'lere dokunmaz.
6. Varsayılan loglarda URL path'leri maskelenir (`--verbose` ile açılır).

## 7. Test stratejisi

- **Birim:** framing (TS+Go), başlık temizleme, Nostr event encode/decode, NIP-44.
- **E2E:** Playwright (Chromium) — ajan + test sitesi başlat, manuel signaling ile bağla, sayfa render + fetch + indirme + SSE doğrula. CI'da her PR'da çalışır.
- **Elle matris (her release öncesi):** Chrome/Edge/Firefox/Safari(iOS dahil) × Windows/macOS/Linux ajan. Safari ve Firefox'ta streaming SW cevabı davranışını özellikle kontrol et.

## 8. Bilinen riskler ve açık sorular

- **(Faz 1'de olculdu, KAPANDI) `ctrl` ve `data` kanallari arasinda siralama garantisi yok.**
  Iki data channel iki ayri SCTP stream'idir; her biri kendi icinde `ordered`
  ama aralarinda siralama yoktur. Olcum (pion loopback, 90 KB cevap): `end`
  mesaji `ctrl` uzerinden geldikten **sonra** 6 gövde cercevesi `data`
  uzerinden geldi. Yani alici `end`'i "gövde bitti" diye yorumlayamaz; bugunki
  tanimla cevap bos gorunurdu.
  **Secilen cozum:** `end` mesaji artik `frames` ve `len` sayaclarini
  tasiyor (`{"t":"end","id":17,"frames":6,"len":90000}`). Alici `end`'i
  gorunce beklenen cerceve/bayt sayisina ulasana kadar `data` dinlemeye devam
  eder; fazla gelirse `abort` (`body-length-mismatch`). Istek yonunde de ayni
  sorun vardi (govde cerceveleri `req`'ten once gelebiliyor): ajan sahibi
  bilinmeyen cerceveleri kisa sureli tamponda tutup `req` gelince sirasiyla
  isliyor. Iki kanal, 16 KiB framing ve ctrl'deki `end` kararlari korundu.
  Ayrinti: `protocol/README.md`.

- **(Faz 1'de ortaya cikti, KAPANDI) App shell ile yerel sitenin koku ayni URL'yi istiyor.**
  Baglanti ekrani + tunel sahibi sayfa scope kokunde (`/peerpages/`) duruyor;
  yerel sitenin koku de ayni yerde olmali ki goreli linkler calissin. Ikisi
  ayni URL'de olamaz: e2e testinde `/peerpages/index.html` istegi app shell'e
  dustu ve test sitesi yerine baglanti ekrani goruldu. Ayrica tunel sahibi
  sayfa scope kokundeyse, o sekme yenilenince kendi URL'si tunelden gecer ve
  tunel kendini kapatirdi.
  **Secilen cozum:** baglanti ekrani + tunel sahibi sayfa
  `/peerpages/connect/` yolunda; scope koku (`/peerpages/`) yerel sitenin
  koku. Tunel sahibi sayfanin URL'si hicbir zaman tunelden gecmez, o sekme
  yenilense bile tunel yasar. Ilk ziyarette (SW henuz yokken) scope koku
  kucuk bir onyukleyici servis eder: SW'yi kurar ve hash'i koruyarak
  `connect/`e gecer. README'deki `#peer-id` URL bicimi korundu.

  Not: scope bir alt yolda oldugu surece yerel sitenin KOKTEN MUTLAK yollari
  (`/style.css`) her durumda scope disina duser ve SW onlari yakalamaz. Bu,
  GitHub Pages proje sayfalarinin yapisal siniri; custom domain ya da
  kullanici sayfasi (`kullanici.github.io/`) ile kalkar. README'nin "sinirlar"
  bolumune durustce yazilmali.

- Firefox/Safari'de SW `ReadableStream` cevaplari ve `postMessage` performansi —
  **Chromium'da olculdu ve sorun yok**: 5 MB indirme ~103 ms (~48 MB/s), kucuk
  istekte SW koprusunun ek maliyeti ~1 ms, ilk bayt < 1 ms (gecici buffer'a
  gerek kalmadi). Ayrintilar: `docs/MEASUREMENTS.md`. **Firefox ve Safari hala
  olculmedi**; Faz 1 e2e'si yalnizca Chromium kosuyor. Elle matris (bolum 7)
  Faz 4 oncesinde yapilacak, Safari'de streaming cevap davranisi ozellikle
  kontrol edilecek.
- **(Faz 2'de ölçüldü, KAPANDI) Relay'lerin ephemeral event davranışı.**
  `peerpages doctor` her relay için NIP-11, bağlantı ve **ephemeral yankı**
  testi yapıyor: kind 25050 yayınlanıp aboneden geri okunuyor. Kritik olan
  bu: bir relay event'i "OK" ile kabul edip sessizce düşürebilir, o zaman
  signaling çalışmaz ama hata da görünmez. 2026-10-26 ölçümü: dört relay
  uygun, `relay.nostr.band` bu ağdan erişilemedi. Liste buna göre güncellendi.

- **(Faz 2'de ölçüldü, AÇIK — TURN gerekebilir) Bu geliştirme ağında NAT simetrik.**
  `doctor` iki STUN sunucusunun gördüğü dış portu karşılaştırıyor; farklı
  çıktı, yani endpoint-dependent mapping. Karşı taraf da simetrik NAT
  arkasındaysa doğrudan WebRTC kurulamaz ve **TURN gerekir (Faz 5)**. Faz 2'nin
  "farklı ağlardaki iki cihaz" kabul kriterini denerken bu çıkabilir; `doctor`
  artık önceden söylüyor. Tek makinede ve aynı ağda sorun yok.

- **(Faz 2'de bulundu, KAPANDI) Ajan "hazır" demeden önce gerçekten dinlemeli.**
  Gerçek relay'lerle yapılan e2e koşumunda ortaya çıktı: ajan abonelik
  kurulmadan hazır olduğunu bildiriyordu ve URL'yi hemen açan ziyaretçinin
  offer'ı **geri dönüşü olmadan** kayboluyordu (ephemeral event'ler
  saklanmaz). Sahte relay localhost'ta olduğu için bu hiç görünmüyordu.
  Sıra artık: relay'lere bağlan → abone ol → "hazır" de.

- **(Faz 3'te bulundu ve KAPANDI) Tavanı aşan gövde `req`'ten önce gelirse istek askıda kalıyordu.**
  `ctrl` ve `data` ayrı SCTP akışları olduğu için aralarında sıralama garantisi yok (Faz 1'de
  ölçüldü, aşağıda). Gövde çerçevesi `req`'ten önce geldiğinde ve `--max-body` tavanını aştığında
  çerçeve **sessizce düşürülüyordu**: gövde hiç gelmediği için proxy sonsuza kadar bekliyor,
  ziyaretçi `abort` almıyor ve istek askıda kalıyordu — yani tavan koruması bir kaynak
  sızıntısına dönüşüyordu. Bunu kırılgan bir test (6 koşumda 3 kırmızı) ortaya çıkardı; sorun
  testin değil davranışın kendisiydi. Artık ihlal işaretlenip ziyaretçiye hemen
  `abort body-too-large` gidiyor ve istek proxy'ye hiç düşmüyor.

- **(Faz 3'te kapandı, SW tarafı bilinçli olarak sınırlı) Ziyaretçi gidince kaynak bırakma.**
  İki ayrı boşluk vardı. (a) Ajanda hiç boşta kalma denetimi yoktu: oturum yalnızca ICE/DTLS
  durumu değişince kapanıyordu, bu da 30 saniyeden çok daha uzun sürebilir ve `Disconnected`
  geri de dönebilir. Artık ajan 10 sn'de bir ctrl `ping` yolluyor ve 30 sn yanıt gelmezse
  oturumu kapatıyor (protokol değişmedi: sayfa gelen ping'e zaten `pong` dönüyordu).
  (b) SW'de `clients.get` sekme **hayatta** olduğu sürece client döndürüyordu, tünelini
  kaybetmiş olsa bile; sayfanın `pagehide`'da gönderdiği haber ise en iyi çabadır (sekme çökerse
  hiç gelmez). Artık bayatlamış tünel sahibi yeniden yoklanıyor.
  **Dürüst sınır:** SW tarafındaki tespit **tembeldir** — istek gelmezse kontrol de olmaz.
  Zaman sınırlı gerçek koruma ajandaki denetimdir. SW'de periyodik zamanlayıcı güvenilmez:
  worker boştayken öldürülür.

- **(Faz 3, AÇIK) QR'ın gerçekten okunduğu elle doğrulanmalı.** Kodlayıcı üçüncü bir bağımsız
  uygulamanın vektörlerine karşı bayt bayt doğrulanıyor ve canvas'ın çizildiği e2e'de kontrol
  ediliyor, ama "telefon kamerası bunu okuyor mu" sorusu otomatik testle cevaplanamaz.
  Yöntem: `docs/MANUAL-TEST.md` → "İki cihazla deneme".

- **(Faz 3, AÇIK — dış kaynaklı) Gerçek relay'lerle aynı koşumda üst üste ajan başlatmak.**
  `PEERPAGES_E2E_RELAYS` ile iki gerçek relay verildiğinde testlerden biri
  "hiçbir relay'e bağlanılamadı" ile düşüyor: her test yeni bir ajan başlatıyor ve public
  relay'ler aynı adresten gelen hızlı tekrar bağlantıları reddediyor. Tek relayle (nos.lol)
  7/7 yeşil. Kodumuzdan değil; Faz 4'te yeniden bağlanma çalışması bunu da iyileştirebilir.

- **(Faz 4'te karar verildi — README'den SAPMA) Yeniden bağlanma ICE restart ile değil, sıfırdan oturumla.**
  Faz 4 maddesi "ICE restart" diyordu; danışıldı ve değiştirildi. İki sebep: (a) ajan
  answerer-only ve oturum tek seferlik (`remoteSet` tek yönlü mandal), yani aynı oturuma ikinci
  bir offer kabul etmek için iki tarafta yeniden pazarlık ve yeni signaling kuralları gerekiyordu;
  (b) Faz 3'te eklenen 30 saniyelik boşta kalma denetimi, uzun kopmalarda ajan tarafındaki oturumu
  zaten kapatıyor — yani ICE restart'ın kurtarabileceği pencere 30 saniyeden kısa kopmalar.
  Her deneme yeni `sid` ve yeni geçici keypair ile sıfırdan kuruyor. Bedeli: kopma anında devam
  eden istekler kayboluyor (SW idempotent olanları bir kez tekrar deniyor).

- **(Faz 4'te bulundu ve KAPANDI) Uyanmadan sonra boşta kalma denetimi oturumları öldürüyordu.**
  `lastSeen` unix nano olarak saklanıp `time.Unix(...)` ile geri okunuyordu; böyle kurulan bir
  `time.Time` **monotonik okumayı taşımaz**, yani karşılaştırma duvar saatine dönüyordu. Askıya
  alma süresi olduğu gibi "sessizlik" sayılıyor ve uyanmadan sonraki ilk tik tüm oturumları
  `ErrIdle` ile kapatıyordu — ziyaretçi hâlâ orada olsa bile, tek bir ping denemesi yapmadan.
  Saat geriye giderse süre negatif oluyor ve denetim sessizce devre dışı kalıyordu.
  Artık süre monotonik ölçülüyor; ayrıca denetim **kendi gecikmesine** bakıyor: iki tik arası
  beklenenin 3 katından fazlaysa bu bir uyanmadır, sessizlik sayacı sıfırlanır ve oturum bir tur
  daha ping'le sınanır. Monotonik saatin askıya almayı sayıp saymaması platforma göre değişir
  (Linux `CLOCK_MONOTONIC` saymaz, Windows sayar) — bu yüzden iki koruma birlikte duruyor.

- **(Faz 4'te bulundu ve KAPANDI) ICE `Disconnected` temiz kapanış sayılıyordu.**
  Durum geçicidir ve pion sık sık kendi kendine geri döner; kodun kendi yorumu da bunu söylüyordu
  ama işleyici oturumu anında kapatıyor, üstelik `closeWith(nil)` ile ziyaretçinin `bye`'ından
  ayırt edilemez biçimde raporluyordu. Artık 10 saniyelik tolerans var ve neden
  (`bosta` / `baglanti-koptu` / `temiz`) log'da görünüyor.

- **(Faz 4'te bulundu ve KAPANDI) SW'nin bekleme penceresi kopma anındaki isteği kaçırıyordu.**
  İlk uygulama yalnızca "tünel sahibi sekme yok" durumunda bekliyordu. Tünel tam istek sırasında
  koparsa istek yine dispatch ediliyor, sayfada hata alıyor ve ziyaretçi **502** görüyordu — yani
  pencere o yolda hiç işlemiyordu. E2E testi bunu yakaladı. Artık sayfa, cevap başlığı hiç
  gitmediyse ve tünel kapandıysa hatayı "tekrar denenebilir" işaretliyor; SW yeniden bağlanmayı
  bekleyip **bir kez** tekrar deniyor — yalnızca GET/HEAD için, çünkü POST'un hedefe ulaşıp
  ulaşmadığı SW'den bilinemez.

- **(Faz 4, AÇIK) Release workflow'u GitHub Actions'ta çalıştırılmadı.**
  Hiç push yapılmadı ve hiç etiket atılmadı (senin kuralın). Yerelde doğrulanan: `goreleaser check`
  temiz, `goreleaser release --snapshot --clean` altı arşiv + `checksums.txt` üretti, çıkan Windows
  binary'si doğru sürümü bastı. Doğrulanmayan: `.github/workflows/release.yml`'in gerçekten koşması,
  taslak release'in açılması ve varlıkların yüklenmesi. İlk `v*` etiketinde görülecek.

- **(Faz 4, AÇIK) Ajan süreci çökerse kimse kaldırmıyor.**
  Ajan relay düşmesinden ve uyku/uyanmadan toparlanıyor ama bir panikten sonra süreci yeniden
  başlatacak bir denetleyici yok; README bunu bilinçli olarak "sadece dokümantasyon" diye
  işaretlemişti. `docs/INSTALL.md` Task Scheduler (yeniden başlatma politikasıyla) ve NSSM
  yollarını anlatıyor.

- **(Faz 5'te karar verildi) TURN yalnızca ajan tarafında.**
  Ziyaretçiye ne TURN adresi ne kimlik bilgisi gönderiliyor. Zorlayan sebep yapısal:
  ziyaretçi offer'ı ajandan tek kelime duymadan **önce** üretiyor (rol değişimi, yukarıda),
  yani TURN bilgisini signaling ile göndermek çok geç kalırdı. Tek taraflı TURN yeterli:
  ajan relay adayı toplar, ziyaretçi o genel adrese dışa doğru paket yollar — tek hedef
  olduğu için simetrik NAT'ta bile eşleme tutarlı — ve ajan onu peer-reflexive tanır.
  Kazanç: kota ve sır sahibinde kalır (ilke 4), adres kısa kalır, QR/pano üzerinden sır
  dolaşmaz. Bedeli: ziyaretçinin kendi relay'ini kullanması gereken egzotik durumlar
  kapsam dışı (yükseltme yolu açık).

- **(Faz 5'te karar verildi — README'den SAPMA) Cloudflare Worker yazılmadı.**
  Faz 5 maddesi `cloudflare-turn/` altında 30 satırlık bir Worker öngörüyordu; danışıldı ve
  iptal edildi. Worker'ın tek işi API jetonunu **tarayıcıdan** saklamaktı — TURN'ü yalnızca
  ajan kullandığı ve jeton zaten sahibinin makinesinde durduğu için aradaki sunucu
  gereksiz. Ajan kimlik bilgisini API'den kendisi çekiyor ve TTL'in %80'inde yeniliyor.
  Böylece **"tek sunucu gerektiren parça" tamamen ortadan kalktı**: sıfır sunucu iddiası
  TURN'le bile bozulmuyor.

- **(Faz 5'te bulundu ve KAPANDI) Ziyaretçi sayfası hiç ICE sunucusu vermiyordu.**
  `new RTCPeerConnection(undefined)` — yani tarayıcı yalnızca **host adayı** topluyordu ve
  ağlar arası bağlantı, ajanın srflx adayına gelen paketten ziyaretçiyi peer-reflexive
  öğrenmesine bel bağlamıştı. `ConnectOptions.iceServers` alanı vardı ama tek çağıranı
  yoktu (ölü kod). Artık varsayılan STUN listesi veriliyor (`web/src/ice.ts`, Go tarafındaki
  `ice.DefaultSTUN` ile aynı) ve ziyaretçi de kendi srflx adayını topluyor — bu tek başına
  TURN'e gerek kalmadan daha çok NAT'ta doğrudan bağlantı demek.

- **(Faz 5, AÇIK) Cloudflare TURN gerçek hesaba karşı denenmedi.**
  İstek biçimi ve cevap ayrıştırması `httptest` ile doğrulandı; canlı doğrulama kullanıcı
  tarafında (`docs/TURN.md`). API'nin cevap şekli belgelendiğinden farklıysa ilk denemede
  anlaşılır bir hata ile ajan başlamaz (sessizce TURN'süz devam etmiyor).

- **(Faz 5 notu) `--ice-relay-only` bir teşhis bayrağı.**
  Doğrudan yolu imkânsız kılar; "bağlantı gerçekten TURN'den mi geçiyor" sorusunu
  deterministik olarak cevaplamanın tek yolu bu, ve kabul kriterinin otomatik testi buna
  dayanıyor. Üretimde gerekmez: ICE doğrudan yolu zaten tercih eder (ölçüldü, §MEASUREMENTS).

- **(Faz 5'te öğrenildi) Sahte TURN sunucusunun duyurduğu adres, paket gönderirken
  kullandığı kaynak adresle aynı olmalı.** `faketurn` ilk sürümde relay'i 127.0.0.1'e
  bağlıyordu; karşı taraf (pion ve tarayıcılar) loopback adayı üretmediği için relay
  oraya paket gönderemiyordu. Sonra soket tüm arayüzlere bağlanıp adres 127.0.0.1 olarak
  duyurulunca tarayıcı gelen kontrolü **peer-reflexive** saydı ve seçilen aday çifti
  relay görünmedi — yani test, doğru çalışan bir TURN'ü ölçemiyordu. Gerçek bir TURN
  sunucusunda bu tutarlılık zaten var; sahte sunucu artık LAN adresini duyuruyor.

- **(Faz 6'da karar verildi — README'den SAPMA) Shim'i HTML'e ajan değil Service Worker sokuyor.**
  Faz 6 maddesi "sayfaya enjekte edilen shim" diyor ve bayrağı ajana koyuyor; bayrak ajanda
  kaldı ama **enjeksiyonu SW yapıyor**. Ajan yalnızca HTML cevaplarına
  `x-peerpages-ws-shim: 1` başlığını ekliyor, gövdeye hiç dokunmuyor. Üç sebep:
  (a) kapsam önekini (`/PeerPages/`) bilen taraf SW, ajan değil — ajan mutlak bir script
  yolu üretemez; (b) `internal/proxy` böylece **bayt bayt geçirgen** kalıyor ve bunu
  doğrulayan testler geçerliliğini koruyor; (c) alternatif, kapasiteyi `hello` mesajıyla
  bildirmekti, ama `hello` tek yönlü, `v != 1`'i sertçe reddediyor ve kanonik biçimde
  "opsiyonel alan yok" kuralı var — yani protokol sürümünü kırmak gerekirdi.
  Politika ajanda, mekanik SW'de. Ayrıntı: `docs/WEBSOCKET.md`.

- **(Faz 6'da bulundu ve KAPANDI) 60 saniyelik zaman aşımı uzun akışları sessizce kesiyordu.**
  `internal/proxy` istek bağlamını `context.WithTimeout(ctx, 60s)` ile sarıyordu ve bu
  **tüm cevabı** kapsıyordu. 60 saniyeden uzun yaşayan her SSE akışı (ve Socket.IO'nun
  uzun yoklaması) kesiliyordu; ziyaretçi bir hata görmüyor, yalnızca "akış bitti"
  görüyordu — yani sessiz bir bozulma. Artık sınır `http.Transport`'ta ve **yalnızca
  başlık fazında** (`ResponseHeaderTimeout`); gövde akarken sınır yok. Cevapsız kalan bir
  hedef hâlâ isteği askıda tutamıyor (test: `TestServeHeaderPhaseStillTimesOut`).

- **(Faz 6, AÇIK — dürüst sınır) Uzun yaşayan akışların bütçesi 8.**
  `--max-streams` ws oturumlarını **ve** SSE cevaplarını birlikte sınırlıyor; bütçe
  dolduğunda SSE `abort too-many-streams`, ws `ws-close 1013` alıyor. Neden ayrı bütçe:
  uzun yaşayan bir akış `--max-concurrent` slotunu ömrü boyunca tutar, yani tek bütçe
  olsa 64 SSE isteği tüneli normal istekler için kullanılamaz hale getirirdi. Hangi
  cevabın uzun yaşadığını **önceden bilemiyoruz**: `content-type: text/event-stream`
  görüldüğü anda yeniden sınıflıyoruz, bu dürüst olan tek işaret. Çok sayıda eşzamanlı
  SSE kullanan bir uygulama bu sayıyı yükseltmek zorunda.

- **(Faz 6, AÇIK) `Origin` başlığı hedefin kendi origin'i olarak gönderiliyor.**
  Yerel sunucular (Vite, Socket.IO) WebSocket el sıkışmasında `Origin` denetimi yapar ve
  ziyaretçinin `github.io` origin'ini reddederdi. Yani tünel, yerel sunucuya **yerel bir
  istemci gibi** görünüyor. Bedeli açık: yerel sunucunun origin denetimi tünelden gelen
  trafiği ayırt edemez. Köprü varsayılan kapalı olduğu için bu bilinçli bir takas;
  Faz 7'nin `SECURITY.md`'sine tehdit modeli olarak yazılmalı.

- **(Faz 6, AÇIK) Socket.IO otomatik test edilmedi.** Kabul kriterinin iki yarısından
  Vite HMR gerçekten koşuyor (`vite` zaten devDependency); Socket.IO `socket.io` +
  `socket.io-client` bağımlılıklarını getireceği için elle doğrulamada
  (`docs/MANUAL-TEST.md`). Polling aşaması normal HTTP, yükseltme shim üzerinden geçiyor,
  ikili yük destekli — ama "çalışıyor" iddiası ölçülmedi.

- **(Faz 6, AÇIK) `bufferedAmount` her zaman 0 ve uzantı pazarlığı yok.**
  Gerçek gönderim tamponu tünelin içinde; shim'in değeri taklit etmesi yanıltıcı olurdu.
  Alt protokol geçiyor, `permessage-deflate` gibi uzantılar geçmiyor (`extensions` boş).
  Tünel koparsa açık soketler 1006 ile düşüyor; yeniden bağlanmayı sayfanın kendi
  kütüphanesi yapar.

- Nostr relay'lerinin ephemeral event'leri bazı relay'ler düşürebilir → `doctor` ile relay sağlığı; liste güncel tutulur.
- GitHub Pages kullanım şartları: statik vitrin için uygun; projeyi ticari proxy gibi sunma, README'de "demo/paylaşım aracı" de.
- Ajan Go; .NET ajanı istenirse `agent-dotnet/` olarak ayrı faz.

## 9. Teknoloji seçimleri (özet)

| Katman | Seçim | Alternatif |
|---|---|---|
| Web | Vite + TypeScript, framework yok | Preact (UI büyürse) |
| SW | Elle yazılmış `sw.ts` | — |
| Nostr (web) | `nostr-tools` | — |
| Ajan | Go 1.27+, `pion/webrtc`, `go-nostr`, `cobra` | .NET + SIPSorcery |
| Release | goreleaser, GitHub Actions | — |
| Test | Vitest, Playwright, `go test` | — |

---

## 10. Claude Code'a verilecek başlangıç metni

> **Tarihsel kayıt.** Bu metin projenin ilk gününde, bu dosya henüz repo
> kökündeki `README.md` iken yazıldı ve aynen korunuyor: Faz 0–7'nin nasıl
> yürütüldüğünü açıklayan şey o. Bugün aynı işi yapacak biri dosya adını
> `docs/BRIEF.md` olarak okumalı.

Aşağıdaki metni repo kökünde (bu brief dosyası ile birlikte) Claude Code'a yapıştır:

```
Bu repodaki README.md projenin brief'i ve yol haritasıdır; önce tamamını oku.

Görev: Faz 0 ve Faz 1'i tamamla. Kurallar:
1. Başlamadan önce plan moduna geç, README'deki Faz 0 + Faz 1'i somut adımlara böl ve bana göster; onayımdan sonra uygula.
2. README'deki kararları (Go ajan, Vite+TS, framework yok, elle yazılmış Service Worker, iki data channel, 16 KiB framing, güvenlik ilkeleri) değiştirme. Değiştirmek istediğin bir şey olursa önce sor.
3. Faz 1'de signaling sunucusu veya Nostr YOK; offer/answer kopyala-yapıştır ile taşınır. Amaç SW + WebRTC + reverse proxy zincirini kanıtlamak.
4. Her alt adımdan sonra testleri çalıştır; Faz 1 bitince Playwright e2e testi ile kabul kriterlerini doğrula ve bana nasıl elle deneyeceğimi (iki tarayıcı penceresi/iki cihaz) adım adım yaz.
5. Protocol framing'i hem TS hem Go'da birim testli yaz; iki tarafın aynı test vektörlerini kullanmasını sağla (protocol/testvectors.json).
6. Bir karar noktasında takılırsan README'nin "Bilinen riskler" bölümüne not düş ve bana sor; tahmin yürütüp ilerleme.
7. Commit'ler küçük ve açıklayıcı olsun, format: "faz-0: ...", "faz-1: ...". CHANGELOG.md tut.
8. Faz 1 kabul kriterleri karşılanmadan Faz 2'ye geçme.

Bittiğinde: ne yaptığını, nelerin çalıştığını, ölçtüğün gecikme/throughput değerlerini ve Faz 2 için önerilerini kısa bir rapor olarak yaz.
```
