# Faz 1'i elle deneme

İki yol var:

- **Nostr (varsayılan, asıl yol):** ziyaretçi sadece URL'yi açar. Kopyala-yapıştır
  yok. Aşağıdaki 1–6. adımlar bunu anlatıyor.
- **Elle (`--manual`):** signaling'i sen taşırsın. Nostr'un çalışmadığı yerde
  ya da zinciri izole etmek istediğinde işe yarar. "Elle signaling" bölümünde.

Otomatik karşılığı: `cd web && npx playwright test` (16 test: 8 elle yol +
8 Nostr yolu). Gerçek relay'lerle:

```bash
cd web
PEERPAGES_E2E_RELAYS="wss://nos.lol,wss://nostr.mom" npx playwright test nostr.spec.ts
```

> **Yön notu.** Faz 2'de roller değişti: **ziyaretçi** offer üretir, ajan
> answer döner. Nedeni: Nostr'da ajan ziyaretçinin geçici pubkey'ini önceden
> bilemediği için ilk mesajı gönderemez. Elle yol da aynı yönü kullanıyor ki
> tek bir kod yolu olsun (BRIEF §8).

## 0. Hazırlık (bir kez)

```bash
cd web && npm install
cd ../agent && go mod download
```

İki terminal açacaksın. Terminal komutları `agent/` ve `web/` dizinlerinden
çalışıyor.

## 1. Yayınlanacak siteyi çalıştır

Kendi projen varsa onu çalıştır (örneğin `npm run dev` → `localhost:3000`) ve
2. adıma geç. Yoksa repodaki test sitesini kullan:

```bash
cd agent
go run . testsite --port 3000
```

Çıktıda şunu görürsün:

```
---PEERPAGES-TESTSITE-LISTENING--- http://127.0.0.1:3000
big.bin sha256: f2793bdc…
```

Test sitesinde HTML, CSS, JS, bir PNG, `/api/items` JSON ucu, `/redirect`
(302) ve `/sse` var; ayrıca 5 MB'lık `big.bin` (ilk çalıştırmada üretilir).

## 2. Web uygulamasını yayınla

İki seçenek var.

**a) Hızlı yol — dev sunucusu** (`localhost:5173`, base `/`):

```bash
cd web
npm run dev
```

**b) GitHub Pages'e benzeyen alt yol** (`localhost:4173/PeerPages/`) —
gerçek dağıtıma daha yakın, alt yol davranışını da sınar:

```bash
cd web
node scripts/build.mjs /PeerPages/
npx vite preview --port 4173
```

> Git Bash kullanıyorsan derleme satırını
> `MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/` diye yaz; MSYS yoksa
> `/PeerPages/` değerini Windows yoluna çeviriyor.

Aşağıda (b) varsayılıyor. (a) için adresleri `http://localhost:5173/…` diye oku.

## 3. Relay'leri kontrol et (önerilir)

```bash
cd agent
go run . doctor
```

Her relay için bağlantı, NIP-11 ve **ephemeral yankı** testi yapar. Sonuncusu
en önemlisi: bir relay event'i "OK" ile kabul edip sessizce düşürebilir, o
zaman signaling çalışmaz ama hata da görünmez. Ayrıca iki STUN sunucusunun
gördüğü dış portu karşılaştırıp NAT tipi tahmini verir.

"NAT tahmini: simetrik" görürsen not et: karşı taraf da simetrik NAT
arkasındaysa doğrudan bağlantı kurulamaz ve TURN gerekir. Çıktının sonundaki
**tavsiye satırı** NAT kararıyla TURN durumunu birleştirip ne yapman
gerektiğini tek cümlede söylüyor ([TURN.md](TURN.md)).

## 4. Ajanı Nostr modunda başlat

```bash
cd agent
go run . serve --target http://localhost:3000 --app-url "http://localhost:4173/PeerPages/"
```

İlk çalıştırmada `~/.peerpages/key` oluşur (nsec olarak). **Bu dosyayı
yedekle:** kaybedersen peer-id'n, yani paylaştığın URL değişir.

Çıktı:

```
PeerPages ajani hazir.

  peer-id : npub1yl4dshp…pdnn
  hedef   : default
  relay   : 4 adet (bagli)
  sir     : yok (peer-id'yi bilen herkes baglanabilir)

Ziyaretcinin acmasi gereken adres:

  http://localhost:4173/PeerPages/#npub1yl4dshp…
---PEERPAGES-READY---
```

"hazir" satırı **gerçekten dinlemeye başladıktan sonra** basılır (önce
relay'lere bağlanır, sonra abone olur). Bu sıra önemli: ephemeral event'ler
saklanmadığı için daha önce gelen bir offer geri dönüşü olmadan kaybolur.

İsterseniz paylaşılan sır ekleyin — peer-id'yi bilen ama sırrı bilmeyen
bağlanamaz:

```bash
go run . serve --target http://localhost:3000 --secret kedi-balik-7 \
  --app-url "http://localhost:4173/PeerPages/"
```

URL `#npub1…:kedi-balik-7` olur. Hash sunucuya gitmez, yani sır GitHub
Pages'e ulaşmaz.

## 5. Ziyaretçi: sadece URL'yi aç

Ajanın bastığı adresi tarayıcıya yapıştır. Beklenen akış, durum satırında:

```
Service Worker → Signaling → ICE → Tunel
```

Yaklaşık bir saniye içinde "Tunel acik" paneli görünür. **Kopyala-yapıştır
yok.** Bu sekme tünel sahibidir, kapatma.

Kendi relay listenizi denemek isterseniz (BRIEF §4: liste kullanıcı
tarafından değiştirilebilir):

```
http://localhost:4173/PeerPages/connect/?relay=wss://nos.lol&relay=wss://nostr.mom#npub1…
```

## 6. Siteyi gez — kontrol listesi

Bağlantı ekranındaki **"tunel uzerinden ac"** bağlantısı siteyi **yeni bir
sekmede** açar (`http://localhost:4173/PeerPages/`). Tünel sahibi sekme açık
kalır.

Yeni sekmede sırayla kontrol et:

| # | Ne | Beklenen |
|---|---|---|
| 1 | Sayfa başlığı | "PeerPages test sitesi" görünüyor |
| 2 | CSS | Arka plan koyu (`rgb(13,17,23)`), başlık mavi |
| 3 | JS | "API" listesinde `alfa = 1`, `beta = 2`, `gama = 3` |
| 4 | Görsel | 64×64 damalı logo yüklendi |
| 5 | fetch/JSON | 3. madde zaten bunu kanıtlıyor (`/api/items`) |
| 6 | Yönlendirme | `…/PeerPages/redirect` adresi siteye dönüyor, adres çubuğu `localhost:3000`'e **atlamıyor** |
| 7 | 5 MB indirme | `…/PeerPages/big.bin` iniyor, 5.242.880 bayt |
| 8 | SSE | `…/PeerPages/sse` üç `data: event-N` satırı gösteriyor |

DevTools → Network'te isteklerin "Service Worker"dan geldiğini,
`localhost:3000`'e **hiç** istek gitmediğini görürsün. Ajan terminalinde
istek logları akar (yollar maskelidir: `/*3/*5`; açmak için `--verbose`).

## 7. Tünel kapanınca ne oluyor

1. Tünel sahibi sekmeyi (`/connect/`) **kapat**.
2. Site sekmesinde **yenile**.

Beklenen: bağlantı ekranına düşer (`/PeerPages/connect/`). Service Worker
tünel bulamayınca gezinmeyi oraya yönlendirir; alt kaynak istekleri `503`
döner.

Nostr yolunda ajan **çalışmaya devam eder**: yeni bir ziyaretçi URL'yi açınca
yeniden bağlanır, ajanı yeniden başlatman gerekmez. Elle yolda (`--manual`)
ajan terminalinde `---PEERPAGES-TUNNEL-CLOSED---` görünür ve süreç çıkar.

---

## Elle signaling (`--manual`)

Nostr'u atlamak istediğinde. Yön: **sayfa** offer üretir, ajan
answer basar.

### Ajanı başlat

İkinci terminalde:

```bash
cd agent
go run . serve --manual --target http://localhost:3000
```

Çıktı:

```
Ziyaretci sayfasini ac (baglanti ekrani), "Offer uret" butonuna bas ve
uretilen offer'i asagiya yapistirip Enter'a bas.

---PEERPAGES-OFFER-WAITING---
```

Ajan şimdi senden offer bekliyor. Terminali açık bırak.

### Bağlantı ekranını aç ve offer üret

Tarayıcıda:

```
http://localhost:4173/PeerPages/connect/
```

- Durum satırında **Service Worker** adımı yeşile dönmeli
  ("Service Worker aktif").
- **Offer uret** düğmesine bas. Birkaç saniye içinde (ICE toplanması) alttaki
  alanda tek satırlık base64 offer oluşur.
- **Kopyala**'ya bas.

> **Bu sekmeyi kapatma.** WebRTC bağlantısını bu sayfa tutuyor; Service
> Worker'ın içinde WebRTC yok (BRIEF §2). Sekme kapanırsa tünel kapanır.

### Offer'ı ajana, answer'ı sayfaya taşı

1. Ajanın terminaline geç, offer'ı **yapıştır** ve **Enter**'a bas.
2. Ajan answer'ı basar:

   ```
   ---PEERPAGES-ANSWER-BEGIN---
   eyJ0eXBlIjoiYW5zd2VyIiwic2RwIjoi…
   ---PEERPAGES-ANSWER-END---
   ```

3. İki işaretçi arasındaki **tek satırı** kopyala, sayfadaki
   "Ajanin answer'ini yapistir" alanına yapıştır ve **Answer'i uygula**'ya bas.

Ajan şunu yazar:

```
---PEERPAGES-TUNNEL-OPEN---
time=… level=INFO msg="tunel acik" target=default
```

Bağlantı ekranında "Tunel acik" paneli görünür: istek sayısı, aktarılan bayt,
gecikme ve hedef listesi.

## Çoklu hedef (Faz 3)

Tek ajan birden çok yerel projeyi yayınlayabilir:

```bash
cd agent
go run . serve   --target http://localhost:3000   --target admin=http://localhost:4000   --app-url "http://localhost:4173/PeerPages/"
```

Ajan her hedef için ayrı bir adres basar:

```
  default  http://localhost:3000
           http://localhost:4173/PeerPages/#npub1yl4dshp…

  admin    http://localhost:4000
           http://localhost:4173/PeerPages/#npub1yl4dshp…/admin
```

Kontrol listesi:

- İki adresi **iki ayrı sekmede** aç: her biri kendi projesini göstermeli.
  Her sekme kendi tünelini kurar.
- Bağlantı ekranında peer-id'nin yanında aktif hedef görünür (`… / admin`),
  ölçümlerde "Hedefler" satırı iki hedefi birlikte listeler (bu liste ctrl
  `hello` mesajından gelir).
- URL'ye olmayan bir hedef yaz (`#npub1…/yokboyle`): bağlanmamalı ve
  "Bu ajanda 'yokboyle' hedefi yok" demeli. **Sessizce `default`'a düşmez** —
  yanlış projeyi gezdiğini fark etmemenden daha iyisi bunu söylemek.
- Elle signaling yolunda URL olmadığı için hedefi bayrak seçer:
  `--manual --manual-target admin`.

> Aynı sekmede hedef değiştirmek mümkün değil: hedef oturum başına sabit,
> değiştirmek yeni bir adres (yeni bağlantı) demek.

## Boşta kalan oturum (Faz 3)

Ziyaretçi gidince ajan kaynağı bırakmalı:

1. Tünel aç, sonra tünel sahibi sekmeyi kapat.
2. Ajanın logunda (stderr) en geç ~30 saniye içinde `tunel kapandi` ya da
   `oturum bosta, kapatiliyor` görmelisin.
3. `[durum]` satırındaki `oturum` sayısı 0'a dönmeli.

Sekmeyi kapatmak yerine **görev yöneticisinden tarayıcıyı öldürmeyi** de
dene: sayfanın "gidiyorum" haberi bu durumda hiç gitmez, yani 30 saniyelik
denetim tek koruma olur. Asıl test bu.

## İki cihazla deneme (Faz 2'nin asıl kabul kriteri)

**Bunu benim yerime sen doğrulamalısın:** iki ayrı cihazı iki ayrı ağda
bağlamayı tek makineden test edemem.

Nostr ile signaling taşımak gerekmiyor — tek yapman gereken URL'yi taşımak.

1. **Laptop'ta** yayınlanacak siteyi ve ajanı çalıştır (1. ve 4. adım).
   `--app-url` değerini telefondan erişilebilir adres yap.
2. **Laptop'ta** web uygulamasını ağa aç:

   ```bash
   cd web
   node scripts/build.mjs /PeerPages/
   npx vite preview --port 4173 --host
   ```

   > **Git Bash'te** derleme satırını
   > `MSYS_NO_PATHCONV=1 node scripts/build.mjs /PeerPages/` diye yaz.

3. Ajanın bastığı URL'deki `localhost` yerine laptop'ın ağ adresini koy ve
   **telefonda aç**. URL'yi taşımanın en kolay yolu artık **QR**: laptop'ta
   tünel açıldıktan sonra bağlantı ekranındaki **QR kod** butonuna bas ve
   telefonun kamerasıyla okut ("Adresi kopyala" butonu da var).

   > **QR'ın gerçekten okunduğunu doğrulayan tek şey bu adım.** Kodlayıcı
   > üçüncü bir bağımsız uygulamanın vektörlerine karşı bayt bayt test
   > ediliyor ve canvas'ın çizildiği e2e'de kontrol ediliyor, ama "telefon
   > kamerası bunu okuyor mu" sorusunu otomatik test cevaplayamaz.
   > Okunmazsa haber ver.

   > **Service Worker güvenli bağlam ister.** `localhost` ve `127.0.0.1`
   > güvenli sayılır ama **düz IP adresi sayılmaz**. Telefonda Service Worker
   > kurulmazsa seçenekler:
   > - HTTPS veren bir sunucu kullan (kendi sertifikanla),
   > - Chrome'da `chrome://flags/#unsafely-treat-insecure-origin-as-secure`
   >   ayarına o adresi ekle (yalnızca test için),
   > - Faz 7'de gerçek dağıtımda sorun yok: GitHub Pages HTTPS veriyor.

4. Telefonda sayfa kendiliğinden bağlanmalı: durum satırı
   `Service Worker → Signaling → ICE → Tunel` ilerler.

**Farklı ağlardan** (telefon mobil veride, laptop ev Wi-Fi'ında) bağlanmak
çoğu NAT'ta çalışır: artık **iki taraf da** STUN kullanıyor (Faz 5'e kadar
yalnızca ajan kullanıyordu). Ama `doctor` bu ağda **simetrik NAT** gördü:
karşı taraf da simetrik NAT arkasındaysa doğrudan bağlantı kurulamaz. O
zaman "Veri kanallari acilmadi" hatası görürsün — yazılım hatası değil, ağ
topolojisinin sonucu. Çözüm TURN: "TURN ile deneme (Faz 5)" bölümü.

## Uyku/uyanma (Faz 4)

**Kabul kriterinin asıl denemesi bu.** Otomatik test ajanı yeniden başlatarak
aynı sonucu üretiyor ama gerçek askıya alma aynı şey değil: saatler,
WebSocket'ler ve işletim sistemi hepsi işin içinde.

1. Ajanı başlat, ziyaretçi URL'sini aç, tünelin açıldığını gör.
2. Ziyaretçi sekmesinde siteyi gez (birkaç istek olsun).
3. **Laptop'ı uyut** (kapağı kapat ya da `Win+X → Kapat → Uyku`). En az
   **2 dakika** bekle: 30 saniyelik boşta kalma sınırını da, geri sayımın
   üst sınırını da aşmış olalım.
4. Uyandır ve ajan terminaline bak.

Beklenen:

- Ajan **ölmemiş** olmalı. `[durum]` satırı akmaya devam eder.
- Ajan logunda `denetim gecikti, surec askiya alinmis olabilir` görebilirsin —
  bu doğru davranış: oturum saat zıpladığı için değil, ping'e cevap gelmediği
  için kapatılır.
- `relay 3/4` gibi bir alan görürsün; uyandıktan sonra düşen relay'ler
  birkaç saniye içinde geri gelmeli (log: `relay baglantilari degisti`).
- Bağlantı ekranında "Baglanti koptu — yeniden baglaniliyor" yazısı
  görünür ve **kendiliğinden** "Tunel acik"a döner. Hiçbir şeye tıklama.
- `#metrics` içinde **Yeniden baglanma: 1** (ya da daha fazla) satırı
  belirir; istek ve bayt sayaçları **sıfırlanmaz**.
- Ziyaretçi sekmesini yenile: site yine çalışıyor olmalı.

Olmazsa: ekrandaki hata metnini ve ajan logunun son 20 satırını not al.
En olası sebep ajan tarafı değil relay tarafıdır (`doctor` çalıştır).

## Relay düşmesi (Faz 4)

Ajan tek bir relay'e bağımlı olmamalı ve relay dönünce toparlamalı.

1. Ajanı **kendi** relay'inle başlat (düşürebileceğin bir relay gerekiyor):

   ```sh
   # 1. terminal: sahte relay sabit portta
   cd agent && go run . fakerelay --port 7447

   # 2. terminal
   cd agent && go run . serve --target http://localhost:3000 --relay ws://localhost:7447
   ```

2. Ziyaretçi URL'sini `?relay=ws://localhost:7447` ekleyerek aç, tünelin
   açıldığını gör.
3. Sahte relay'i **öldür** (1. terminalde Ctrl+C).

   Beklenen: ajan **çıkmaz**. `[durum]` satırında `relay 0/1` görünür ve
   logda `hicbir relay'e baglanti yok: ziyaretciler su an sana ulasamaz`
   uyarısı belirir. Açık tünel çalışmaya devam eder — WebRTC relay'e bağlı
   değil, signaling bitmişti.
4. Sahte relay'i **aynı portta** yeniden başlat.

   Beklenen: `relay 1/1`, logda `tum relay'ler bagli`. Şimdi yeni bir
   ziyaretçi sekmesi açabilmelisin: abonelik yenilenmiş olmalı.

## Kurulumun doğrulanması (Faz 4)

```sh
peerpages version
```

Beklenen: sürüm, commit, tarih, platform ve Go sürümü. Kaynaktan
`go build` ile derlediysen sürüm `dev (<commit>)` görünür; release
binary'sinde `v0.1.0` gibi bir etiket olur. Ayrıntı: `docs/INSTALL.md`.

## TURN ile deneme (Faz 5)

Bu ağda `doctor` **simetrik NAT** görüyor: karşı taraf da simetrik NAT
arkasındaysa doğrudan bağlantı kurulamaz. TURN bu duruma çıkış yolu.
Ayrıntılı kurulum ve güvenlik notları: [TURN.md](TURN.md).

### (i) Yerelde beş dakikalık deneme (gerçek TURN sunucusu gerekmez)

```bash
# 1. terminal: surec ici TURN sunucusu
cd agent && go run . faketurn --port 0 --user u --pass p
# Bastigi adresi al: turn:127.0.0.1:XXXXX

# 2. terminal: ajan YALNIZCA relay adayi toplasin
cd agent && go run . serve --target http://localhost:3000   --turn turn:127.0.0.1:XXXXX --turn-user u --turn-pass p --ice-relay-only
```

Ajanın bastığı URL'yi aç. Beklenen:

- Tünel açılıyor (banner'daki `ice` satırı `turn 1 … YALNIZCA RELAY` diyor).
- `#metrics` içindeki **Baglanti yolu** satırı **`TURN uzerinden (relay)`**.
- Site normal çalışıyor.

`--ice-relay-only` doğrudan yolu **imkânsız** kılıyor, yani tünel açıldıysa
trafik mutlaka TURN'den geçmiştir. Teşhis bayrağı; üretimde gerekmez.

Karşılaştırma için `--ice-relay-only`'yi kaldırıp tekrar dene: aynı TURN
tanımlıyken bile **Baglanti yolu** `dogrudan` çıkmalı (ICE doğru yolu seçer,
kota boşa gitmez).

### (ii) Asıl kriter: iki ağ (mobil hotspot ↔ Wi-Fi)

**Bunu benim yerime sen doğrulamalısın.** Gerçek bir TURN sunucusu gerekiyor
(coturn ya da Cloudflare — [TURN.md](TURN.md)).

1. `peerpages doctor --turn <adres> --turn-user … --turn-pass …` çalıştır.
   `TURN` bölümünde **relay adresi** görmelisin; tavsiye satırı
   "NAT simetrik ama TURN hazır" demeli.
2. Ajanı aynı TURN bilgisiyle başlat (`--ice-relay-only` **KOYMA**: gerçek
   hayatta doğrudan yol varsa kullanılsın).
3. Telefonu **mobil veriye** al (laptop Wi-Fi'da kalsın) ve ziyaretçi URL'sini
   telefonda aç (QR en kolay yol).
4. Bağlantı ekranındaki **Baglanti yolu** satırına bak.

Beklenen: tünel açılıyor. Satır `dogrudan (NAT uzerinden)` derse bu ağ
çiftinde TURN'e gerek kalmamış (iyi haber); `TURN uzerinden (relay)` derse
Faz 5'in çözdüğü durum tam olarak gerçekleşmiş demektir. İkisi de başarı
— başarısızlık yalnızca "Veri kanallari acilmadi" hatası.

> **TURN kimlik bilgisi telefona gitmiyor.** Ziyaretçi TURN'ü hiç bilmez;
> relay'i ajan açar ve telefon yalnızca o genel adrese paket yollar. Yani
> URL'yi paylaştığın kişi kotanı harcayamaz.

## WebSocket ile deneme (Faz 6)

WebSocket köprüsü **varsayılan kapalı** (HTML'e müdahale ediyor). Açmak için
tek bayrak: `--inject-ws-shim`.

### (i) Vite HMR — beş dakika

```bash
# 1. terminal: herhangi bir Vite projesi
npm create vite@latest deneme -- --template vanilla
cd deneme && npm install && npm run dev

# 2. terminal: ajan (shim acik)
cd agent && go run . serve --target http://localhost:5173 --inject-ws-shim
```

Ajanın bastığı URL'yi aç, sonra ziyaretçi sekmesinde siteyi gez. Şimdi
`deneme/src/main.js` içindeki bir metni değiştir ve kaydet:

- [ ] Sayfa **yenilenmeden** güncellendi (adres çubuğunda yükleme olmadı).
- [ ] Tünel sekmesinde `#metrics`'te **"WebSocket: 1 acik"** satırı var.
- [ ] DevTools → Console'da `[vite] connected.` yazıyor.

> **Kök mutlak yollar.** Proje sayfasında (`kullanici.github.io/PeerPages/`)
> Vite'ın ürettiği `/src/main.js` gibi yollar kapsam dışına düşer. Çaresi
> `vite.config.js`'te `base: "/PeerPages/"` ve ajan hedefine aynı öneki
> vermek: `--target http://localhost:5173/PeerPages/`. Gerekçesi ve ayrıntısı
> [WEBSOCKET.md](WEBSOCKET.md).

### (ii) Socket.IO — asıl elle kriter

Bu yol **otomatik test edilmedi** (BRIEF §8); doğrulaması sende.

```bash
mkdir io-deneme && cd io-deneme && npm init -y && npm i socket.io
```

`sunucu.js`:

```js
const { createServer } = require("node:http");
const { Server } = require("socket.io");

const http = createServer((req, res) => {
  res.setHeader("content-type", "text/html; charset=utf-8");
  res.end(`<!doctype html><html><head><title>io</title></head><body>
    <h1 id="durum">baglaniyor…</h1>
    <script src="/socket.io/socket.io.js"></script>
    <script>
      const s = io();
      s.on("connect", () => { document.getElementById("durum").textContent = "bagli: " + s.id; });
      s.on("saat", (t) => { document.getElementById("durum").textContent = t; });
      s.on("disconnect", (r) => { document.getElementById("durum").textContent = "kopdu: " + r; });
    </script></body></html>`);
});

const io = new Server(http);
io.on("connection", (sock) => {
  const timer = setInterval(() => sock.emit("saat", new Date().toISOString()), 1000);
  sock.on("disconnect", () => clearInterval(timer));
});
http.listen(3100, () => console.log("http://localhost:3100"));
```

```bash
node sunucu.js
# baska terminalde:
cd agent && go run . serve --target http://localhost:3100 --inject-ws-shim
```

Ziyaretçi sekmesinde:

- [ ] Başlık "bagli: <id>" oluyor, sonra saniyede bir saat güncelleniyor.
- [ ] DevTools → Network → WS sekmesinde **gerçek bir WS bağlantısı
      görünmüyor** (shim onu tünele aldı; görünen tek şey ilk polling
      istekleri olabilir).
- [ ] Tünel sekmesinde "WebSocket: 1 acik" satırı var.
- [ ] Ajanı Ctrl+C ile kapatınca sayfa "kopdu: transport close" diyor ve
      ajanı geri açınca Socket.IO kendi kendine yeniden bağlanıyor.

### (iii) SSE — uzun akış artık kesilmiyor

Faz 6'ya kadar ajandaki 60 saniyelik zaman aşımı **tüm cevabı** sarıyordu,
yani uzun yaşayan bir SSE akışı sessizce kesiliyordu. Test ucu:

```bash
cd agent && go run . testsite --port 3000      # 1. terminal
cd agent && go run . serve --target http://localhost:3000   # 2. terminal
```

Ziyaretçi sekmesinin konsolunda:

```js
const es = new EventSource("sse?n=0&delay=5000&keepalive=1");
es.onmessage = (e) => console.log(new Date().toISOString(), e.data);
```

- [ ] Olaylar 5 saniyede bir geliyor ve **60 saniyeyi geçtikten sonra da
      devam ediyor** (en az 90 saniye bekle).
- [ ] Sekmeyi kapatınca ajan logunda akış kapanıyor (kaynak sızmıyor).

## Sorun giderme

| Belirti | Sebep / çözüm |
|---|---|
| Durum satırı "Baslatiliyor…"da kalıyor | Service Worker kurulamadı. Güvenli bağlam mı (`localhost`/HTTPS)? DevTools → Application → Service Workers'a bak. |
| "base64 cozulemedi" | Metin eksik kopyalanmış. İki işaretçi arasındaki **tüm** satırı al. |
| Offer alanı boş kalıyor | ICE toplanması bitmemiş olabilir; 10 sn bekle. Konsolda hata var mı? |
| Ajan "girdi okunamadi: girdi bitti" | Terminale yapıştırıp Enter'a basmadan ajan kapanmış. Yeniden başlat. |
| "Ajan yanit vermedi" | Ajan çalışmıyor, ya da relay'ler mesajı iletmiyor. `go run . doctor` çalıştır: relay'ler "uygun" mu? |
| "Paylasilan sir uyusmuyor" | URL'deki sır ajanın `--secret` değeriyle aynı değil. |
| "Veri kanallari acilmadi" | Signaling çalıştı ama WebRTC kurulamadı. Büyük olasılıkla simetrik NAT. `doctor`'ın tavsiye satırına bak; `--turn` ile bir TURN sunucusu ver ([TURN.md](TURN.md)). |
| TURN verdim, yine olmadı | `peerpages doctor --turn …` çalıştır: allocation alınıyor mu? Alınmıyorsa adres/kimlik bilgisi yanlış ya da UDP kapalı (`turns:` dene). |
| Baglanti yolu "TURN uzerinden" diyor | Beklenen davranış: doğrudan yol kurulamadı, trafik relay'den geçiyor. Şifreleme sürüyor (DTLS), ama kota harcanıyor. |
| "hicbir relay'e baglanilamadi" | Ağ WebSocket'i engelliyor olabilir. `--relay` ile kendi relay'ini ver. |
| peer-id hatası | URL'nin `#` sonrasını eksiksiz kopyalamadın. npub 63 karakter. |
| Site sekmesi bağlantı ekranına düşüyor | Tünel sahibi sekme kapalı. `/connect/` sekmesinin açık olduğundan emin ol. |
| Alt kaynaklar 503 | Aynı sebep: tünel yok. Bağlantı ekranı "yeniden baglaniliyor" diyorsa birkaç saniye bekle: SW kopma boşluğunda isteği bir süre bekletir (alt kaynak 10 sn, gezinme 3 sn), sonra 503'e döner. |
| "yeniden baglaniliyor" bitmiyor | Ajan gerçekten kapalı ya da relay'lere ulaşılamıyor. Ajan terminaline bak: `relay 0/N` mi? |
| Uyandıktan sonra tünel geri gelmiyor | Sekme arka plandaysa geri sayım yavaşlar; sekmeye geç (odaklanma geri sayımı sıfırlar) ya da "Simdi dene"ye bas. |
| Kök mutlak yollar (`/style.css`) 404 | Alt yolda (`/PeerPages/`) yayınlanan sitelerin yapısal sınırı: o yol Service Worker'ın kapsamı dışına düşer. Custom domain ya da `kullanici.github.io/` kökünde sorun kalmaz (BRIEF §8). |
| WebSocket `onclose` kodu 1008 | Köprü kapalı: ajanı `--inject-ws-shim` ile başlat. Bayrak verildiyse yerel sunucu el sıkışmayı reddetmiş (yolu kontrol et). |
| WebSocket `onclose` kodu 1011 | Yerel sunucuya bağlanılamadı: sunucu ayakta mı, yol doğru mu? |
| WebSocket `onclose` kodu 1013 | Akış bütçesi dolu; `--max-streams` değerini yükselt. |
| WebSocket `onclose` kodu 1009 | Mesaj `--max-ws-message` tavanını aştı. |
| Vite HMR bağlanmıyor | Konsolda `[vite] failed to connect` varsa: `base` hizalaması (yukarıdaki not) ya da sayfanın CSP'si. |
| Sayfa yenileniyor ama HMR çalışmıyor | Bazı çatılarda değişiklik "full reload" gerektirir; bu HMR'ın kendi davranışı, tünelin sorunu değil. |
| 5 MB indirme çok yavaş | Tarayıcı ve ajan aynı makinedeyse 100 ms civarı olmalı. Farklı ağlardaysan bağlantı hızına bağlı. |
