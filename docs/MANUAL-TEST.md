# Faz 1'i elle deneme

Faz 1'de **signaling yok**: ajanın bastığı `offer`'ı sen ziyaretçi sayfasına
yapıştırırsın, sayfanın ürettiği `answer`'ı ajana geri yapıştırırsın. Amaç
Service Worker + WebRTC + reverse proxy zincirinin gerçekten çalıştığını
görmek (README Faz 1).

Otomatik karşılığı: `cd web && npx playwright test` (8 test, kabul
kriterlerinin tamamını doğruluyor). Aşağısı aynı şeyi elle yapmak için.

---

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

Test sitesinde HTML, CSS, JS, bir PNG, `/api/items` JSON ucu, `/yonlendir`
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

## 3. Ajanı başlat

İkinci terminalde:

```bash
cd agent
go run . serve --target http://localhost:3000
```

Çıktı:

```
---PEERPAGES-OFFER-BEGIN---
eyJ0eXBlIjoib2ZmZXIiLCJzZHAiOiJ2PTBcclxu…        ← tek satır, uzun
---PEERPAGES-OFFER-END---

Bu offer'i ziyaretci sayfasindaki … yapistir
---PEERPAGES-ANSWER-BEKLENIYOR---
```

İki işaretçi **arasındaki tek satırı** tamamen kopyala.

## 4. Bağlantı ekranını aç ve offer'ı yapıştır

Tarayıcıda:

```
http://localhost:4173/PeerPages/connect/
```

- Durum satırında **Service Worker** adımı yeşile dönmeli
  ("Service Worker aktif").
- Kopyaladığın offer'ı "Ajanin offer'ini yapistir" alanına yapıştır.
- **Offer'i kabul et**'e bas.

Birkaç saniye içinde alt alandaki `answer` dolar (tek satır base64).

> **Bu sekmeyi kapatma.** WebRTC bağlantısını bu sayfa tutuyor; Service
> Worker'ın içinde WebRTC yok (README §2). Sekme kapanırsa tünel kapanır.

## 5. Answer'ı ajana yapıştır

"Kopyala" düğmesine bas, ajanın çalıştığı terminale geç, yapıştır ve
**Enter**'a bas. Ajan şunu yazar:

```
---PEERPAGES-TUNNEL-OPEN---
time=… level=INFO msg="tunel acik" target=default
```

Bağlantı ekranında "Tunel acik" paneli görünür: istek sayısı, aktarılan bayt,
gecikme ve hedef listesi.

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
| 6 | Yönlendirme | `…/PeerPages/yonlendir` adresi siteye dönüyor, adres çubuğu `localhost:3000`'e **atlamıyor** |
| 7 | 5 MB indirme | `…/PeerPages/big.bin` iniyor, 5.242.880 bayt |
| 8 | SSE | `…/PeerPages/sse` üç `data: olay-N` satırı gösteriyor |

DevTools → Network'te isteklerin "Service Worker"dan geldiğini,
`localhost:3000`'e **hiç** istek gitmediğini görürsün. Ajan terminalinde
istek logları akar (yollar maskelidir: `/*3/*5`; açmak için `--verbose`).

## 7. Tünel kapanınca ne oluyor

1. Tünel sahibi sekmeyi (`/connect/`) **kapat**.
2. Site sekmesinde **yenile**.

Beklenen: bağlantı ekranına düşer (`/PeerPages/connect/`). Service Worker
tünel bulamayınca gezinmeyi oraya yönlendirir; alt kaynak istekleri `503`
döner. Ajan terminalinde `---PEERPAGES-TUNNEL-CLOSED---` görünür ve süreç
çıkar — yeni bir oturum için ajanı yeniden başlatman gerekir (otomatik
yeniden bağlanma Faz 4).

---

## İki cihazla deneme (ör. laptop + telefon)

Faz 1'de signaling elle taşındığı için ikinci cihazda yapman gereken tek şey
offer/answer metinlerini taşımak. En kolayı: metinleri kendine mesaj olarak
gönder ya da bir not uygulamasıyla senkronla.

1. **Laptop'ta** yayınlanacak siteyi ve ajanı çalıştır (adım 1 ve 3).
2. **Laptop'ta** web uygulamasını ağdan erişilebilir yayınla:

   ```bash
   cd web
   node scripts/build.mjs /PeerPages/
   npx vite preview --port 4173 --host
   ```

   Çıktıdaki `Network:` adresini not et (ör. `http://192.168.1.97:4173/`).

3. **Telefonda** o adresin `connect/` yolunu aç:
   `http://192.168.1.97:4173/PeerPages/connect/`

   > **Önemli:** Service Worker yalnızca güvenli bağlamda çalışır. `localhost`
   > ve `127.0.0.1` güvenli sayılır ama **düz IP adresi sayılmaz**. Telefonda
   > Service Worker kurulmazsa seçenekler:
   > - `vite preview` yerine HTTPS veren bir sunucu kullan (kendi
   >   sertifikanla), ya da
   > - Chrome'da `chrome://flags/#unsafely-treat-insecure-origin-as-secure`
   >   ayarına o adresi ekle (yalnızca test için), ya da
   > - Faz 7'de gerçek dağıtımda bu sorun yok: GitHub Pages HTTPS veriyor.

4. Laptop'taki ajanın offer'ını telefona taşı, yapıştır, "Offer'i kabul et".
5. Telefonun ürettiği answer'ı laptop'a taşı, ajanın terminaline yapıştır.
6. Telefonda `http://192.168.1.97:4173/PeerPages/` adresini aç: laptop'taki
   `localhost:3000` telefonda açılır.

Aynı Wi-Fi'daysanız WebRTC doğrudan bağlanır. **Farklı ağlardan** (telefon
mobil veride, laptop ev Wi-Fi'ında) bağlanmak da çoğu NAT'ta çalışır çünkü
ajan STUN kullanıyor; simetrik NAT'ta TURN gerekir (Faz 5).

---

## Sorun giderme

| Belirti | Sebep / çözüm |
|---|---|
| Durum satırı "Baslatiliyor…"da kalıyor | Service Worker kurulamadı. Güvenli bağlam mı (`localhost`/HTTPS)? DevTools → Application → Service Workers'a bak. |
| "base64 cozulemedi" | Offer metni eksik kopyalanmış. İki işaretçi arasındaki **tüm** satırı al. |
| Answer alanı boş kalıyor | ICE toplanması bitmemiş olabilir; 10 sn bekle. Konsolda hata var mı? |
| Ajan "answer okunamadi: girdi bitti" | Terminale yapıştırıp Enter'a basmadan ajan kapanmış. Yeniden başlat. |
| Site sekmesi bağlantı ekranına düşüyor | Tünel sahibi sekme kapalı. `/connect/` sekmesinin açık olduğundan emin ol. |
| Alt kaynaklar 503 | Aynı sebep: tünel yok. |
| Kök mutlak yollar (`/style.css`) 404 | Alt yolda (`/PeerPages/`) yayınlanan sitelerin yapısal sınırı: o yol Service Worker'ın kapsamı dışına düşer. Custom domain ya da `kullanici.github.io/` kökünde sorun kalmaz (README §8). |
| 5 MB indirme çok yavaş | Tarayıcı ve ajan aynı makinedeyse 100 ms civarı olmalı. Farklı ağlardaysan bağlantı hızına bağlı. |
