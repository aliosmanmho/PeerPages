# TURN (bağlanamayan ağlar için)

Çoğu ağda PeerPages'e TURN gerekmez: ajan ve ziyaretçi birbirine **doğrudan**
bağlanır (STUN yardımıyla). Ama bazı ağlarda doğrudan bağlantı kurulamaz ve o
zaman trafiğin bir **relay** üzerinden geçmesi gerekir. TURN tam olarak bunu
yapan standart.

## Gerekip gerekmediğini nasıl anlarsın

```bash
peerpages doctor
```

Çıktının sonundaki tavsiyeye bak:

| Ne görürsen | Anlamı |
|---|---|
| `NAT uyumlu: TURN gerekmez.` | Doğrudan bağlantı kurulabiliyor. Bir şey yapma. |
| `NAT simetrik ve TURN verilmedi` | Karşı taraf da simetrik NAT arkasındaysa **bağlanamaz**. TURN gerekiyor. |
| `STUN'a ulasilamadi` | UDP dışarıya kapalı. TURN de UDP kullanır; `turns:` (TCP/TLS) dene. |

Ziyaretçi tarafında belirti: bağlantı ekranı **"Veri kanallari acilmadi"**
diyor ve signaling adımları tamamlanmış görünüyor. Bu bir yazılım hatası
değil, ağ topolojisinin sonucu.

Tünel kurulduktan sonra hangi yolun seçildiğini bağlantı ekranındaki
**"Baglanti yolu"** satırı söyler: `dogrudan (yerel ag)`,
`dogrudan (NAT uzerinden)` ya da `TURN uzerinden (relay)`.

## Üç yol

### 1. Kendi TURN sunucun (coturn)

En çok kontrol, en az bağımlılık. Bir VPS'te:

```bash
sudo apt install coturn
```

`/etc/turnserver.conf`:

```
listening-port=3478
tls-listening-port=5349
fingerprint
lt-cred-mech
user=peerpages:COK-UZUN-BIR-SIFRE
realm=ornek.com
# Kendi genel IP'n:
external-ip=203.0.113.10
# Yalnızca senin kullanman için: kota koy.
user-quota=12
total-quota=1200
# Yerel ağına relay açılmasını engelle (SSRF benzeri kötüye kullanım):
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=192.168.0.0-192.168.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
```

Ajanı başlat:

```bash
peerpages serve --target http://localhost:3000 \
  --turn turn:ornek.com:3478 --turn-user peerpages \
  --turn-pass COK-UZUN-BIR-SIFRE
```

> **Şifreyi komut satırına yazmak zorunda değilsin:** `PEERPAGES_TURN_PASS`
> ortam değişkeni de okunuyor. Komut satırı argümanları `ps` çıktısında ve
> Görev Yöneticisi'nde başka kullanıcılara görünür.

### 2. Cloudflare Realtime TURN (kısa ömürlü kimlik bilgisi)

Cloudflare'in TURN hizmeti aylık 1 TB'a kadar ücretsiz. Ajan kimlik
bilgisini **kendisi** çeker ve süresi bitmeden yeniler:

```bash
export PEERPAGES_CF_TURN_TOKEN=...   # API jetonu
peerpages serve --target http://localhost:3000 \
  --turn-cloudflare-key-id <ANAHTAR-KIMLIGI>
```

Panelden alman gerekenler: **TURN anahtar kimliği** ve **API jetonu**
(Cloudflare Dashboard → Realtime → TURN).

> **Worker gerekmiyor.** README'nin ilk planı, kısa ömürlü kimlik bilgisi
> üretmek için `cloudflare-turn/` altında küçük bir Worker öngörüyordu.
> Worker'ın tek işi API jetonunu **tarayıcıdan** saklamaktı; PeerPages'te
> TURN'ü yalnızca ajan kullandığı ve jeton zaten senin makinende durduğu için
> aradaki sunucu gereksiz. Böylece "sıfır sunucu" iddiası TURN'le bile
> bozulmuyor.

> **Denenmedi:** bu yol gerçek bir Cloudflare hesabına karşı çalıştırılmadı.
> İstek biçimi ve cevap ayrıştırması testlerle doğrulandı (`httptest`), canlı
> doğrulama sende. Çalışmazsa `peerpages doctor
> --turn-cloudflare-key-id ...` çıktısını paylaş.

### 3. Elle alınmış statik kimlik bilgisi

Herhangi bir TURN sağlayıcısından aldığın kullanıcı/şifreyi `--turn-user` /
`--turn-pass` ile verebilirsin. Kimlik bilgisinin süresi varsa bitiminde
ajanı yeniden başlatman gerekir (Cloudflare yolunda bu otomatik).

## Doğrulama

```bash
peerpages doctor --turn turn:ornek.com:3478 --turn-user peerpages --turn-pass ...
```

`TURN` bölümünde `durum: tamam` ve bir **relay adresi** görmelisin. Ölçülen
şey "sunucu ayakta mı" değil, **"bana relay adresi veriyor mu"**: yanlış
kimlik bilgisi ya da dolmuş kota, ayakta görünüp allocation vermeyen bir
sunucu üretir. TURN verildiği hâlde hiçbiri allocation vermezse `doctor`
sıfırdan farklı bir kodla çıkar.

Yerel deneme (gerçek TURN sunucusu olmadan):

```bash
# 1. terminal
cd agent && go run . faketurn --port 0 --user u --pass p
# Bastığı adresi al: turn:127.0.0.1:XXXXX

# 2. terminal — dogrudan yolu IMKANSIZ kil
cd agent && go run . serve --target http://localhost:3000 \
  --turn turn:127.0.0.1:XXXXX --turn-user u --turn-pass p --ice-relay-only
```

`--ice-relay-only` yalnızca relay adayı toplar, yani bağlantı kurulabiliyorsa
**mutlaka** TURN üzerinden kurulmuştur. Bu bir teşhis bayrağı; üretimde
gerekmez (ICE doğrudan yolu zaten tercih eder, kota boşa gitmez).

## Güvenlik ve maliyet

- **Kimlik bilgisi ajandan çıkmaz.** Ziyaretçi TURN'ü hiç bilmez: sayfaya ne
  adres ne kullanıcı ne şifre gönderilir. Tek taraflı TURN yeterli, çünkü
  ziyaretçi ajanın relay adresine doğrudan paket yollar. Böylece kotanı
  tanımadığın ziyaretçiler harcayamaz (BRIEF §6 ilke 4: ziyaretçi
  güvenilmez kabul edilir).
- **Trafik TURN üzerinden geçse bile uçtan uca şifreli kalır.** WebRTC data
  channel DTLS ile şifrelidir; TURN sunucusu baytları taşır, **içeriği
  göremez**. Göreceği şey: iki ucun adresi ve ne kadar veri aktığı.
- **Yine de bir sunucuya bağımlı hale gelirsin.** Bu, projenin "arada hiçbir
  sunucu yok" iddiasının tek istisnası — bu yüzden TURN **opsiyonel** ve
  varsayılan olarak kapalı. Bağlantı ekranı TURN kullanıldığında bunu
  ziyaretçiye de söyler.
- **Kota:** TURN tüm trafiği taşır, yani 5 MB'lık bir indirme sağlayıcının
  kotasından 5 MB yer. Kendi coturn'ünde `user-quota` koy.
- Ölçülen bedel (aynı makinede, loopback): 5 MB indirme doğrudan 96 ms,
  TURN üzerinden 114 ms (~%16 yavaş). Gerçek ağda TURN sunucusunun
  konumuna bir gidiş-dönüş eklenir. Ayrıntı: `MEASUREMENTS.md`.

## Sınırlar

- `turns:` (TLS) adresleri ajana verilebilir ve WebRTC kullanır, ama
  `peerpages doctor`'ın allocation denemesi TLS'i atlayıp UDP dener —
  rapor bunu dürüstçe yazar.
- İki uçlu TURN (ziyaretçinin de kendi relay'ini kullanması) yapılmıyor.
  Gerekli değil: tek taraflı relay, iki taraf da simetrik NAT arkasındayken
  bile çalışır.
