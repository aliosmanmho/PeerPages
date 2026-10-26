# PeerPages data channel protokolü — normatif tanım

Bu dosya `ctrl` ve `data` kanallarındaki mesaj biçiminin **tek doğru
kaynağıdır**. `testvectors.json` bu tanımın makine tarafından okunabilir
karşılığıdır ve **hem** TypeScript (`web/src/protocol/`) **hem** Go
(`agent/internal/protocol/`) tarafı aynı dosyayı okuyarak test edilir.

> Protokolü değiştiriyorsan: bu dosyayı güncelle, `testvectors.json`'a vektör
> ekle, iki tarafı da yeşile geçir. Tek taraflı değişiklik kabul edilmez
> (`CONTRIBUTING.md`).

## Kanallar

| Kanal | `ordered` | `reliable` | İçerik |
|---|---|---|---|
| `ctrl` | evet | evet | UTF-8 JSON metin, satır başına bir mesaj |
| `data` | evet | evet | ikili çerçeveler (aşağıda) |

İki kanal kullanılmasının nedeni okunabilirlik: tek kanal + 1 bayt tip prefiksi
de mümkündü, MVP'de iki kanal tercih edildi (README §3).

Kanalları **ajan** açar (ajan offerer'dır), ziyaretçi sayfası `ondatachannel`
ile alır.

## `data` çerçevesi

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        id (uint32, BE)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                       seq (uint32, BE)                        |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                     payload (0 … 16384 bayt)                  |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

| Alan | Tip | Anlam |
|---|---|---|
| `id` | uint32, big-endian | isteğin kimliği; `ctrl` `req`/`res` ile aynı |
| `seq` | uint32, big-endian | bu yöndeki gövde parçasının sırası, **0'dan** başlar |
| `payload` | 0–16384 bayt | gövde parçası |

- Başlık **8 bayt**, payload en fazla **16384 bayt (16 KiB)**, dolayısıyla en
  büyük çerçeve **16392 bayt**. 16 KiB sınırı tarayıcı data channel
  uyumluluğu için (README §3).
- **Boş payload geçerlidir** (8 baytlık çerçeve). Gövdenin bittiğini payload
  değil, `ctrl` kanalındaki `end` mesajı söyler.
- Aynı framing **her iki yön** için kullanılır: istek gövdesi ve cevap gövdesi.
  `id` istek başına tekil olduğu için ve her taraf yalnızca karşı yönden veri
  aldığı için karışma olmaz.
- `seq` alıcıda sıra doğrulaması içindir. Kanal `ordered` olduğundan normalde
  artarak gelir; **beklenmeyen `seq` protokol hatasıdır** ve istek `abort`
  edilir (sessizce yeniden sıralanmaz).

### Hata kodları (framing)

Her iki tarafta aynı kodlar kullanılır; `testvectors.json` bu adlara göre
doğrulama yapar.

| Kod | Ne zaman |
|---|---|
| `short-frame` | çerçeve 8 bayttan kısa (başlık tamamlanmıyor) |
| `payload-too-large` | payload 16384 bayttan büyük (kodlama ya da çözme) |

## `ctrl` mesajları

UTF-8 JSON nesneleri. Zorunlu alan `t` (tip).

```jsonc
{ "t": "req",   "id": 17, "method": "GET", "path": "/api/items?x=1",
                "headers": { "accept": "..." }, "hasBody": false }
{ "t": "res",   "id": 17, "status": 200, "headers": { "content-type": "text/html" } }
{ "t": "end",   "id": 17 }                          // gövde bitti
{ "t": "abort", "id": 17, "reason": "client-cancel" }
{ "t": "ping" }
{ "t": "pong" }
{ "t": "hello", "v": 1, "targets": ["default", "admin"], "auth": "none" }
```

### Alan kuralları

| Alan | Tip | Kural |
|---|---|---|
| `id` | tamsayı | `0 … 4294967295` (uint32). Ondalık ve negatif geçersiz. |
| `method` | string | boş olamaz; yalnızca büyük harf HTTP token'ı (`^[A-Z]+$`) |
| `path` | string | `/` ile başlar; mutlak URL ve `//host` biçimi geçersiz |
| `headers` | nesne | string→string; anahtarlar küçük harfe normalize edilir |
| `hasBody` | boolean | istekte gövde var mı |
| `status` | tamsayı | `100 … 599` |
| `reason` | string | boş olamaz |
| `v` | tamsayı | şimdilik yalnızca `1` |
| `targets` | string dizisi | en az bir eleman, hepsi string |
| `auth` | string | `"none"` ya da `"required"` |

`path` doğrulaması **güvenlik kontrolünün ilk katmanıdır**; ikinci ve belirleyici
katman ajan tarafındaki `internal/target.Resolve`'dur (README ilke 1).

### Hata kodları (ctrl)

| Kod | Ne zaman |
|---|---|
| `bad-json` | JSON okunamadı ya da en üst düzey bir nesne değil |
| `unknown-type` | `t` tanınmayan bir değer (boş string dahil) |
| `missing-field` | tip için zorunlu bir alan yok |
| `bad-field` | alan var ama tipi/aralığı/biçimi geçersiz |

Sıralama: `bad-json` → `missing-field` (`t` yoksa) → `bad-field` (`t` string
değilse) → `unknown-type` → tipe özgü `missing-field` → `bad-field`.

### Kanonik serileştirme

Test vektörlerinin iki dilde **bayt bayt aynı** doğrulanabilmesi için kanonik
bir biçim tanımlıdır:

1. Boşluk yok (`{"t":"end","id":17}`).
2. Anahtar sırası tipe göre sabittir:

   | Tip | Sıra |
   |---|---|
   | `req` | `t, id, method, path, headers, hasBody` |
   | `res` | `t, id, status, headers` |
   | `end` | `t, id` |
   | `abort` | `t, id, reason` |
   | `ping` / `pong` | `t` |
   | `hello` | `t, v, targets, auth` |

3. `headers` anahtarları küçük harf ve **sözlük sırasında**.
4. Opsiyonel alan yok: `hasBody` her zaman yazılır (`false` olsa bile),
   `headers` boş olsa bile `{}` olarak yazılır.
5. HTML kaçışı yapılmaz (`<`, `>`, `&` olduğu gibi), ASCII dışı karakterler
   `\u` ile kaçırılmaz.

Testler `canonical(parse(text)) == vektördeki canonical` eşitliğini doğrular;
bu, iki uygulamanın hem ayrıştırmada hem üretmede aynı davrandığını garanti eder.

## Akış

```
ziyaretçi                                              ajan
    │                                                   │
    │◄──────────── ctrl: hello (v, targets, auth) ───────│  bağlantı kurulunca
    │                                                   │
    │─── ctrl: req  (id=1, GET /index.html) ────────────►│
    │                                                   │  → localhost:3000
    │◄── ctrl: res  (id=1, 200, content-type) ───────────│
    │◄── data: [id=1][seq=0][16 KiB] ────────────────────│
    │◄── data: [id=1][seq=1][…]      ────────────────────│
    │◄── ctrl: end  (id=1) ──────────────────────────────│
    │                                                   │
    │─── ctrl: req  (id=2, POST /api, hasBody) ─────────►│
    │─── data: [id=2][seq=0][…] ────────────────────────►│
    │─── ctrl: end  (id=2) ─────────────────────────────►│
    │◄── ctrl: res  (id=2, 201, …) ──────────────────────│
    │◄── ctrl: end  (id=2) ──────────────────────────────│
```

- `abort` her iki yönden gelebilir; alan taraf o `id` için akışı durdurur ve
  kaynakları bırakır.
- `ping`/`pong` canlılık ve gecikme ölçümü içindir; `id` taşımaz.
- `hello` bağlantı başına bir kez, ajandan ziyaretçiye.

## Backpressure

Gönderen taraf data channel'ın `bufferedAmount` değerini izler:

- `bufferedAmount > 1 MiB` → gövde okuması durur,
- `bufferedAmountLowThreshold = 256 KiB` ve `bufferedamountlow` olayı →
  okuma devam eder.

Bu, 5 MB'lık bir indirmede belleğin şişmesini ve kanalın tıkanmasını önler.
