# PeerPages — Sıfır sunucuyla localhost'u GitHub Pages üzerinden yayınla

> Çalışma adı: **PeerPages** (geçici; Faz 7'de son isim seçilecek). 
> Bu dosya Claude Code için proje brief'i + yol haritasıdır. Public README Faz 7'de İngilizce olarak ayrıca yazılacak.

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
Opsiyonel: TURN fallback (Cloudflare Realtime TURN, ayda 1 TB ücretsiz)
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
- Yerel uygulamanın kendi WebSocket'lerini SW yakalayamaz → Faz 6'da ayrı köprü.
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

- Ajan başlarken bir Nostr keypair üretir (ya da `~/.peerpages/key` dosyasından okur). **peer-id** = npub'ın kısa/okunur formu (ör. bech32 ilk 12 karakter + checksum) — URL'de `#` sonrası taşınır.
- Ziyaretçi sayfası da geçici keypair üretir.
- Event türü: ephemeral kind **25050** (hashtree/remotekit ekosistemiyle uyumlu). İçerik **NIP-44** ile şifreli, `#p` tag'i alıcı pubkey. Mesaj tipleri: `offer`, `answer`, `candidate`, `bye`.
- Varsayılan relay listesi (en az 3, kullanıcı değiştirebilir): `wss://relay.damus.io`, `wss://nos.lol`, `wss://relay.nostr.band` — gerçek listeyi Faz 2'de canlı test edip güncelle; NIP-42 auth isteyen relay'leri atla.
- Aynı mesajı tüm relay'lere yolla, ilk gelene göre devam et (hedged). Relay düşerse yeniden bağlan.
- Opsiyonel paylaşılan sır: ajan `--secret` ile başlatılırsa URL `#peer-id:secret` olur; sır signaling içeriğini ek olarak şifreler ve ajan sırsız teklifleri reddeder.

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

### Faz 3 — Çoklu hedef ve ziyaretçi deneyimi (1 gün)
- `--target default=http://localhost:3000 --target admin=http://localhost:4000`; URL `#peer-id/admin`.
- Bağlantı ekranı: durum (SW kuruluyor → relay → ICE → bağlı), gecikme, aktarılan byte, "kopyala" butonu, QR kod.
- `hello` mesajıyla hedef listesi; ajan tarafında istek/başarı/hata sayacı.
- Ziyaretçi sekmeyi kapatınca SW 30 sn içinde bağlantıyı düşürür (ajan kaynak sızdırmasın).

**Kabul:** Tek ajan iki projeyi yayınlıyor; ekranda anlamlı durum bilgisi var.

### Faz 4 — Dağıtım ve dayanıklılık (1 gün)
- Otomatik yeniden bağlanma (ICE restart, relay düşmesi, uyku/uyanma).
- goreleaser: Windows/macOS/Linux binary'leri, `go install github.com/<kullanıcı>/peerpages/agent@latest`, Scoop/Homebrew tap taslağı.
- `peerpages serve` dışında `peerpages key`, `peerpages doctor` (relay erişimi, STUN, NAT tipi raporu).
- Windows'ta servis olarak çalışma notu (NSSM/Task Scheduler) — sadece dokümantasyon.

**Kabul:** Laptop uyuyup uyanınca tünel kendini toparlıyor; release workflow üç platform binary'si üretiyor.

### Faz 5 — TURN fallback (yarım gün)
- `--turn <url> --turn-user --turn-pass` ile kullanıcı kendi TURN'ünü verebilir.
- Opsiyonel: `cloudflare-turn/` içinde 30 satırlık Worker — Cloudflare Realtime TURN için kısa ömürlü credential üretir. README'de **opsiyonel** ve "tek sunucu gerektiren parça" diye açıkça yazılır.
- `doctor` komutu TURN gerekip gerekmediğini söyler.

**Kabul:** Simetrik NAT simülasyonunda (ör. mobil hotspot ↔ kurumsal ağ) TURN ile bağlanıyor.

### Faz 6 — WebSocket ve SSE köprüsü (1 gün)
- SSE zaten streaming cevapla çalışmalı; test ekle.
- WebSocket: sayfaya enjekte edilen küçük bir shim `window.WebSocket`'i sarar, data channel üzerinde `ws-open/ws-frame/ws-close` mesajlarıyla ajan tarafında gerçek WS'e köprüler. Shim yalnızca `--inject-ws-shim` ile açılır (HTML'e müdahale ediyoruz, varsayılan kapalı).

**Kabul:** Vite HMR ve Socket.IO örneği tünel üzerinden çalışıyor.

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

- **(Faz 1'de olculdu, KARAR BEKLIYOR) `ctrl` ve `data` kanallari arasinda siralama garantisi yok.**
  Iki data channel iki ayri SCTP stream'idir; her biri kendi icinde `ordered`
  ama aralarinda siralama yoktur. Olcum (pion loopback, 90 KB cevap): `end`
  mesaji `ctrl` uzerinden geldikten **sonra** 6 gövde cercevesi `data`
  uzerinden geldi. Yani alici `end`'i "gövde bitti" diye yorumlayamaz; bugunki
  tanimla cevap bos gorunur. Secenekler:
  1. `end` mesajina gövde olcusu eklemek (`{"t":"end","id":17,"frames":3}` ya
     da `"len":90000`): alici beklenen tum cerceveleri aldiktan sonra akisi
     kapatir. Iki kanal ve 16 KiB framing kararlari degismez.
  2. Gövde sonunu `data` kanalina tasimak (ozel EOF cercevesi): siralama
     dogal olarak korunur, ama `end` ctrl'den cikar.
  3. Tek kanal + 1 bayt tip prefiksi (README §3'teki alternatif): en buyuk
     degisiklik.

- **(Faz 1'de ortaya cikti, KARAR BEKLIYOR) App shell ile yerel sitenin koku ayni URL'yi istiyor.**
  Baglanti ekrani + tunel sahibi sayfa scope kokunde (`/peerpages/`) duruyor;
  yerel sitenin koku de ayni yerde olmali ki goreli linkler calissin. Ikisi
  ayni URL'de olamaz: e2e testinde `/peerpages/index.html` istegi app shell'e
  dustu ve test sitesi yerine baglanti ekrani goruldu. Ayrica tunel sahibi
  sayfa scope kokundeyse, o sekme yenilenince kendi URL'si tunelden gecer ve
  tunel kendini kapatir.
  Secenekler:
  1. Baglanti ekranini ayri bir yola tasimak (`/peerpages/connect/`), scope
     kokunu yerel siteye birakmak. Tunel sahibi sayfanin URL'si hicbir zaman
     tunelden gecmez; ilk ziyarette SW yokken scope koku statik app shell'i
     servis eder ve o da `connect/`e yonlendirir. README'deki URL bicimi
     `#peer-id` olarak kalir, yalnizca baglanti ekraninin yolu degisir.
     Derlemeye ikinci bir HTML girdisi eklenir.
  2. Yerel siteyi bir onek altina almak (`/peerpages/s/...`). App shell scope
     kokunde kalir ama ziyaretcinin gordugu URL'ler uzar.
  3. Oldugu gibi birakmak: app shell scope kokunde, yerel site diger tum
     yollarda. Sitenin koku yalnizca `/index.html` gibi bir yolla aciliyor ve
     sunucu onu `./`ya yonlendirirse yine app shell'e dusuyor (test sitesinde
     `http.FileServer` tam bunu yapiyor). Kirilgan.

  Not: scope bir alt yolda oldugu surece yerel sitenin KOKTEN MUTLAK yollari
  (`/style.css`) her durumda scope disina duser ve SW onlari yakalamaz. Bu,
  GitHub Pages proje sayfalarinin yapisal siniri; custom domain ya da
  kullanici sayfasi (`kullanici.github.io/`) ile kalkar. README'nin "sinirlar"
  bolumune durustce yazilmali.

- Firefox/Safari'de SW `ReadableStream` cevapları ve `postMessage` performansı — Faz 1'de ölçülecek; gerekirse büyük cevaplar için geçici buffer.
- Nostr relay'lerinin ephemeral event'leri bazı relay'ler düşürebilir → `doctor` ile relay sağlığı; liste güncel tutulur.
- GitHub Pages kullanım şartları: statik vitrin için uygun; projeyi ticari proxy gibi sunma, README'de "demo/paylaşım aracı" de.
- Ajan Go; .NET ajanı istenirse `agent-dotnet/` olarak ayrı faz.

## 9. Teknoloji seçimleri (özet)

| Katman | Seçim | Alternatif |
|---|---|---|
| Web | Vite + TypeScript, framework yok | Preact (UI büyürse) |
| SW | Elle yazılmış `sw.ts` | — |
| Nostr (web) | `nostr-tools` | — |
| Ajan | Go 1.22+, `pion/webrtc`, `go-nostr`, `cobra` | .NET + SIPSorcery |
| Release | goreleaser, GitHub Actions | — |
| Test | Vitest, Playwright, `go test` | — |

---

## 10. Claude Code'a verilecek başlangıç metni

Aşağıdaki metni repo kökünde (bu `README.md` dosyası ile birlikte) Claude Code'a yapıştır:

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
