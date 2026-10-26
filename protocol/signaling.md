# PeerPages signaling protokolü — normatif tanım

Bu dosya Nostr üzerinden taşınan signaling mesajlarının **tek doğru
kaynağıdır**. `signaling-testvectors.json` bu tanımın makine tarafından
okunabilir karşılığıdır ve **hem** TypeScript (`web/src/nostr/`) **hem** Go
(`agent/internal/signaling/`) tarafı aynı dosyayı okuyarak test edilir.

Data channel protokolü ayrı bir dosyada: [`README.md`](README.md).

> Değiştiriyorsan: bu dosyayı güncelle, vektör ekle, iki tarafı da yeşile
> geçir. Tek taraflı değişiklik kabul edilmez (`../CONTRIBUTING.md`).

## Taşıma: Nostr event'i

| Alan | Değer |
|---|---|
| `kind` | **25050** — ephemeral aralıkta (20000–29999), relay'ler saklamaz |
| `tags` | `[["p", "<alıcı pubkey hex>"]]` |
| `content` | **NIP-44** ile şifrelenmiş *payload* (aşağıda) |
| `pubkey` | gönderenin pubkey'i (ziyaretçide her ziyarette yeni, geçici) |

Relay'ler yalnızca "bir pubkey bir şey aldı" görür; içerik onlara kapalıdır
(`../README.md` §6 ilke 2).

### Kimler kimdir

- **Ajan**: uzun ömürlü kimlik. peer-id = **tam npub**, URL'de `#` sonrası.
- **Ziyaretçi**: her ziyarette **yeni geçici keypair** üretir.

### Akış

```
ziyaretçi                        relay'ler                        ajan
    │                                │                              │
    │  offer     (#p = ajan npub) ──►│──────────────────────────────►│
    │                                │                              │ Session(answerer)
    │◄───────────────────────────────│◄── answer   (#p = ziyaretçi) ─│
    │  candidate (#p = ajan)      ──►│──────────────────────────────►│
    │◄───────────────────────────────│◄── candidate (#p = ziyaretçi) │
    │                                │                              │
    │           … WebRTC kurulur, signaling biter …                  │
    │                                │                              │
    │  bye       (#p = ajan)      ──►│──────────────────────────────►│
```

**Offer'ı ziyaretçi yayınlar.** Ajan, ziyaretçinin geçici pubkey'ini önceden
bilemez — dolayısıyla ilk mesajı o gönderemez (kime şifreleyeceğini bilemez).
Karar ve gerekçesi `../README.md` §8'de.

## Payload

NIP-44'ün *içine* konan düz metin. İki biçimi var:

| Biçim | Ne zaman |
|---|---|
| kanonik JSON (aşağıda) | paylaşılan sır yok |
| `pps1.<base64url>` | paylaşılan sır var (`--secret`) |

Alıcı, payload'ın `pps1.` ile başlayıp başlamadığına bakarak hangisi olduğunu
anlar. Ajan `--secret` ile başlatılmışsa **sırsız payload'ı reddeder**; sır
olmadan başlatılmışsa `pps1.` payload'ı çözemez ve reddeder.

## Mesajlar

```jsonc
{ "v": 1, "t": "offer",     "sid": "0123456789abcdef", "sdp": "v=0\r\n…" }
{ "v": 1, "t": "offer",     "sid": "0123456789abcdef", "sdp": "v=0\r\n…", "target": "admin" }
{ "v": 1, "t": "answer",    "sid": "0123456789abcdef", "sdp": "v=0\r\n…" }
{ "v": 1, "t": "candidate", "sid": "0123456789abcdef", "cand": "candidate:…", "mid": "0" }
{ "v": 1, "t": "bye",       "sid": "0123456789abcdef", "reason": "visitor-left" }
```

### Alan kuralları

| Alan | Tip | Kural |
|---|---|---|
| `v` | tamsayı | şimdilik yalnızca `1` |
| `t` | string | `offer` / `answer` / `candidate` / `bye` |
| `sid` | string | **tam 16 küçük harf hex** karakter (64 bit) |
| `sdp` | string | boş olamaz, en fazla 65536 bayt |
| `target` | string | **yalnızca `offer`'da, opsiyonel**; `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$` (1–32 karakter) |
| `cand` | string | boş olamaz, en fazla 1024 bayt |
| `mid` | string | boş olamaz, en fazla 32 bayt (`sdpMid`) |
| `reason` | string | boş olamaz, en fazla 64 bayt |

### Hedef seçimi (`target`)

Bir ajan birden çok yerel projeyi yayınlayabilir
(`--target default=http://localhost:3000 --target admin=http://localhost:4000`).
Ziyaretçi hangisini istediğini **URL'de** söyler (`#npub1…/admin`) ve bu ad
`offer`'da taşınır.

| Durum | Davranış |
|---|---|
| `target` yok | ajan `default`'u kullanır; o da yoksa alfabetik ilk hedef |
| `target` tanımlı bir ad | o hedefe bağlanılır |
| `target` tanımsız bir ad | ajan `bye` + `reason: "unknown-target"` döner, oturum **hiç kurulmaz** |

Hedef **oturum başına sabittir**: tünel kurulduktan sonra değişmez, değiştirmek
yeni bir bağlantı (yeni URL) demektir. Bu yüzden alan `offer`'da, data channel
protokolünün `req` mesajında değil — Faz 1'in çerçeveleme ve kanonik anahtar
sırası hiç değişmedi. Hedef adlarının listesi tünel açıldıktan sonra ctrl
`hello` mesajındaki `targets` alanıyla da bildirilir (bkz. [`README.md`](README.md)).

`target` diğer mesaj tiplerinde **anlamsızdır** ve okunmaz (ayrıştırıcı
tanımadığı alanları genel olarak yok sayar).

`sid` neden var: bir ajana aynı anda birden çok ziyaretçi gelebilir ve
`candidate` mesajlarının hangi oturuma ait olduğu bilinmek zorunda. Ziyaretçi
üretir, ajan aynısını yansıtır.

**Aday sonu (end-of-candidates) taşınmaz.** Tarayıcı `onicecandidate`'i `null`
ile bir kez daha çağırır; biz bunu göndermiyoruz, sadece göndermeyi bırakıyoruz.
Bu yüzden `cand` boş olamaz.

### Hata kodları

Data channel protokolüyle **aynı** kod kümesi (`README.md`):

| Kod | Ne zaman |
|---|---|
| `bad-json` | JSON okunamadı ya da en üst düzey bir nesne değil |
| `unknown-type` | `t` tanınmayan bir değer (boş string dahil) |
| `missing-field` | tip için zorunlu bir alan yok |
| `bad-field` | alan var ama tipi/aralığı/biçimi geçersiz |
| `bad-secret` | `pps1.` payload çözülemedi (yanlış/eksik sır) ya da sır beklenirken düz JSON geldi |

Sıralama: `bad-secret` → `bad-json` → `missing-field` (`t` yoksa) →
`bad-field` (`t` string değilse) → `unknown-type` → tipe özgü kontroller.

### Kanonik serileştirme

Vektörlerin iki dilde **bayt bayt aynı** doğrulanabilmesi için:

1. Boşluk yok.
2. Anahtar sırası tipe göre sabit:

   | Tip | Sıra |
   |---|---|
   | `offer` | `v, t, sid, sdp` + hedef varsa `, target` |
   | `answer` | `v, t, sid, sdp` |
   | `candidate` | `v, t, sid, cand, mid` |
   | `bye` | `v, t, sid, reason` |

3. Opsiyonel alan yoksa **hiç yazılmaz** — boş string ya da `null` olarak
   yazılmaz. Bugün bu yalnızca `offer.target` için geçerli; hedefsiz bir
   `offer`'ın kanonik biçimi bu alan eklenmeden önceki haliyle **birebir
   aynıdır**, yani eski vektörler aynen geçerli kalır.
4. HTML kaçışı yapılmaz, ASCII dışı karakterler `\u` ile kaçırılmaz.
   Kontrol karakterleri `JSON.stringify` ile aynı kaçırılır (`\n`, `\r`,
   `\t`, `\b`, `\f`, diğerleri `\u00xx`). SDP `\r\n` içerdiği için bu kural
   pratikte her mesajda işliyor.

## Paylaşılan sır katmanı (`--secret`)

Sır, **NIP-44'ün altında** ek bir katmandır. Amacı: peer-id'yi (npub) bilen
ama sırrı bilmeyen birinin ajana geçerli bir offer gönderememesi.

```
anahtar = HKDF-SHA256(
    ikm  = UTF-8(secret),
    salt = UTF-8("peerpages-secret-v1"),
    info = UTF-8("signaling"),
    len  = 32)

nonce   = 12 rastgele bayt
ct      = ChaCha20-Poly1305_Seal(
              key = anahtar,
              nonce = nonce,
              plaintext = kanonik JSON,
              aad = UTF-8("peerpages-signaling-v1"))

payload = "pps1." + base64url_nopad(nonce || ct)
```

- `base64url_nopad`: RFC 4648 §5 alfabesi (`-` ve `_`), **dolgu yok**.
- `ct` AEAD etiketini (16 bayt) içerir.
- Çözme ters sırada; etiket doğrulanamazsa `bad-secret`.

**Sır neyi korumaz:** relay'ler yine "bu npub bir şey aldı" görür, yani
ajanın çevrimiçi olduğu ve ne zaman trafik aldığı gizlenmez. Sır yalnızca
*bağlanma yetkisini* korur.

## Güvenlik kuralları (ajan tarafı)

Ziyaretçi güvenilmez (`../README.md` §6 ilke 4):

| Kural | Neden |
|---|---|
| `created_at` ±5 dk dışındaysa reddet | eski event'lerin tekrar oynatılması |
| Görülen event id'lerini kısa süre tut, tekrarını reddet | replay |
| Ziyaretçi pubkey'i başına hız limiti | spam / kaynak tüketimi |
| Eşzamanlı oturum tavanı | kaynak tüketimi |
| Bilinmeyen `sid` için `candidate` reddet | durum kirletme |
| `sdp` / `cand` boyut sınırları | bellek tüketimi |
| Loglarda pubkey kısaltılır | gereksiz kimlik izi |

Bu kuralların uygulanması `agent/internal/signaling` ve ajan akışındadır
(Faz 2.5); bu dosya yalnızca **ne** olduğunu tanımlar.
