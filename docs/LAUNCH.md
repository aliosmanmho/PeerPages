# Lansman malzemesi

Hazır metinler ve kontrol listesi. **Hiçbiri paylaşılmadı** — paylaşma kararı
ve zamanlaması sende.

Tonun kuralı, projenin kendi kuralıyla aynı: **abartma yok, ölçülmeyen iddia
yok.** Hacker News ve r/selfhosted, "no server" iddiasını ilk yorumda
delmeye çalışır; onlara malzemeyi kendin ver, kazanırsın.

---

## 1. Önce: 10 saniyelik GIF

README'nin en üstündeki iki ekran görüntüsü gerçek ama **durağan**. Asıl ikna
edici şey, bir terminalden çıkan URL'nin başka bir cihazda açılması. Bunu
senin makinenin kaydı olarak çekmek, benim üreteceğim sentetik bir
animasyondan daha iyi: gerçek gecikme, gerçek fare hareketi, gerçek site.

**Senaryo (hedef: 10–12 saniye)**

| Saniye | Ekranda |
|---|---|
| 0–2 | Terminal: `peerpages serve --target http://localhost:3000` yazılır ve çalışır |
| 2–4 | Ajan hazır olur, ziyaretçi URL'si görünür (peer-id'yi bulanıklaştırmana gerek yok, zaten her çalıştırmada aynı — istersen `--key` ile geçici bir anahtar kullan) |
| 4–6 | URL kopyalanır, **ikinci bir tarayıcı penceresinde** (tercihen telefon ekranı yansıması) açılır |
| 6–9 | Bağlantı ekranı adımları geçer, site görünür; bir-iki tıkla gezilir |
| 9–12 | Yerel editörde bir metin değiştirilir, sayfa yenilenir ve değişiklik ziyaretçide görünür |

**Nasıl kaydedilir**

- Windows: [ScreenToGif](https://www.screentogif.com/) — pencere seç, 15 fps,
  kayıttan sonra "Reduce frame count" + 256 renk.
- macOS: QuickTime ile ekran kaydı → `ffmpeg -i demo.mov -vf "fps=15,scale=900:-1" demo.gif`.
- Linux: `peek` ya da aynı `ffmpeg` komutu.

**Hedef:** 900 px genişlik, **2 MB altı** (GitHub README'de 2 MB üstü GIF'ler
ağır açılır). Dosyayı `docs/img/demo.gif` olarak koy ve README'nin en üstündeki
tabloyu şununla değiştir:

```markdown
![PeerPages demo](docs/img/demo.gif)
```

> Ekran görüntülerini silme: GIF yüklenmeyen ortamlarda (RSS, bazı mobil
> istemciler) tablo yedek kalsın diye GIF'in altına taşı.

---

## 2. Show HN

**Başlık adayları** (HN başlıkları sade olmalı; emoji ve ünlem düşürür):

1. `Show HN: PeerPages – Share your localhost over GitHub Pages, no server in between`
   — en açık olanı. "no server" iddiası tartışmayı başlatır, ki istediğimiz bu.
2. `Show HN: Tunnel to localhost with WebRTC and Nostr signaling, no relay server`
   — teknolojiyi öne çıkarır; WebRTC/Nostr bilen kitleyi çeker ama "ne işe
   yarıyor" sorusunu ikinci plana atar.
3. `Show HN: A localhost tunnel where the traffic never touches a third party`
   — faydayı öne çıkarır; en az teknik, en çok yanlış anlaşılmaya açık.

**Önerilen: 1.**

**İlk yorum** (HN kuralı: yazar ilk yorumda bağlamı verir)

> I built this because every localhost tunnel I used terminates TLS on
> someone else's machine. ngrok and Cloudflare Tunnel are good products, but
> the provider can read the traffic by construction — that is how they route
> it.
>
> PeerPages has no server in the path. The page is static on GitHub Pages, the
> WebRTC handshake goes through public Nostr relays as NIP-44-encrypted
> ephemeral events, and the data is a direct DTLS connection between the
> visitor's browser and a small Go agent on my machine. The visitor installs
> nothing; a Service Worker makes relative links, CSS, XHR, SSE and (opt-in)
> WebSockets work transparently.
>
> What I think is interesting technically:
>
> - The Service Worker can't hold the WebRTC connection (no WebRTC in workers),
>   so the page holds it and the SW forwards each request over a MessageChannel.
> - `ctrl` and `data` are two SCTP streams with no ordering guarantee between
>   them. I measured `end` arriving before six body frames, so the end message
>   carries frame and byte counters instead of meaning "done".
> - WebSocket handshakes are invisible to `fetch`, so there is an opt-in shim
>   that wraps `window.WebSocket` and tunnels the payload over the same data
>   channel. Vite HMR works through it; that's covered by an end-to-end test
>   that edits a file and asserts the page updated without reloading.
>
> What does not work, honestly:
>
> - On a GitHub project page the app lives at `/PeerPages/`, so a site asking
>   for `/style.css` escapes the Service Worker scope and 404s. A custom domain
>   removes this.
> - If both sides are behind symmetric NAT you need TURN, which is the single
>   exception to "no server" — optional, off by default, and the UI tells the
>   visitor when traffic is relayed.
> - Only Chromium is verified by the test suite. Socket.IO works in manual
>   testing but isn't covered.
> - The agent's CLI and the detailed docs are still in Turkish. README and the
>   threat model are English.
> - No tags yet, so `go install` is the only install path today.
>
> Threat model: https://github.com/aliosmanmho/PeerPages/blob/main/docs/SECURITY.md
> Measurements (latency, throughput, bundle size, per phase):
> https://github.com/aliosmanmho/PeerPages/blob/main/docs/MEASUREMENTS.md
>
> Happy to answer anything about the protocol or the failure modes.

**Beklenen zor sorular ve dürüst cevapları**

| Soru | Cevap |
|---|---|
| "Nostr relay de bir sunucu değil mi?" | Evet, ve bunu saklamıyoruz: relay'ler el sıkışmayı taşır, trafiği değil. İstersen kendi relay'ini verirsin (`--relay`). Fark şu: relay'in içeriği okuma imkânı yok, ngrok'un var. |
| "GitHub Pages da bir sunucu." | Statik dosya sunuyor, trafiğini değil. Sayfayı kendi alan adında da barındırabilirsin. |
| "TURN gerekirse iddian çöküyor." | Simetrik NAT'ta evet, bir relay gerekiyor; o yüzden opsiyonel, varsayılan kapalı ve ekranda ziyaretçiye söyleniyor. TURN bile içeriği göremez (DTLS). |
| "URL'yi bilen herkes giriyor." | Doğru, ve README bunu "URL'yi şifre gibi gör" diye yazıyor. `--secret` ikinci katman. |
| "Güvenlik denetimi var mı?" | Yok. NIP-44 ve DTLS kendi uygulamaları değil, ama kompozisyon denetlenmedi; SECURITY.md bunu açıkça söylüyor. |

---

## 3. r/selfhosted

**Başlık:** `PeerPages: share localhost over GitHub Pages — WebRTC data channel, Nostr signaling, no relay server`

> Self-hosting crowd will appreciate the specific thing here: there is no
> third-party server in the data path and no account anywhere.
>
> The visitor opens a static page on GitHub Pages, it installs a Service Worker
> and opens a WebRTC data channel to a single Go binary on my machine. The
> WebRTC handshake travels through public Nostr relays, encrypted end to end —
> a relay only learns that some public key received a blob. Traffic itself is
> DTLS between the two machines.
>
> - Single binary, `go install`, no daemon, no config file.
> - Allowlist only: the agent proxies to the targets you pass on the command
>   line; deriving a host from the request path is impossible by construction.
> - Optional TURN if your NAT needs it (own coturn or Cloudflare's free tier);
>   credentials never leave your machine.
> - `peerpages doctor` tells you up front whether your NAT will cooperate.
>
> Limits are in the README and there is a real threat model in docs/SECURITY.md
> (replay/clock-skew/rate-limit guards, body and stream ceilings, what the
> relays can see, what we deliberately traded away).
>
> It is a sharing tool, not a hosting product — the agent has to stay running
> and the page is not indexable.

---

## 4. r/webdev

**Başlık:** `Share your dev server with a client in one command — no signup, no tunnel provider`

> You run `peerpages serve --target http://localhost:3000`, you get a URL, you
> send it. Whoever opens it sees your dev server in their browser: relative
> links, CSS, images, fetch, downloads, SSE, and WebSockets (including Vite
> HMR) all work, because a Service Worker on the page intercepts every request
> and forwards it over a direct WebRTC connection to your machine.
>
> No account, no third-party proxy, nothing for the visitor to install. The
> static page is on GitHub Pages; the connection is browser-to-your-machine.
>
> Practical notes:
>
> - Vite HMR works with no configuration; editing a file updates the visitor's
>   page without a reload (there's an e2e test that asserts exactly that).
> - If your site uses root-absolute paths (`/style.css`) you'll want a custom
>   domain — on a GitHub project page those escape the Service Worker scope.
> - Keep the terminal open; the tunnel lives as long as the agent does.
>
> Chromium is verified by the test suite; Firefox and Safari should work but
> I haven't measured them. Feedback welcome, especially on browsers I can't
> test.

---

## 5. X / LinkedIn

Kurumsal hesap adını `<kurumsal-hesap>` yerine yaz.

**X (kısa):**

> Share your localhost over GitHub Pages — with no server in between.
>
> Static page + Service Worker + WebRTC data channel + Nostr for signaling.
> The visitor installs nothing, the traffic goes straight from their browser
> to your machine, and nobody in the middle can read it.
>
> Open source, MIT: github.com/aliosmanmho/PeerPages

**LinkedIn (biraz daha uzun):**

> Most localhost tunnels work by terminating TLS on the provider's servers.
> That is fine until you are sharing a staging build with a client and realise
> a third party is decrypting it.
>
> PeerPages takes the server out of the path: a static page on GitHub Pages
> installs a Service Worker, opens a WebRTC data channel directly to a small Go
> agent on your machine, and uses public Nostr relays only to carry the
> encrypted handshake. No account, nothing for the visitor to install.
>
> It was built in seven phases, each with measurements and an honest list of
> what does not work — including a threat model and the trade-offs we
> deliberately accepted.
>
> MIT licensed: github.com/aliosmanmho/PeerPages
> (built with <kurumsal-hesap>)

---

## 6. Lansmandan önce kontrol listesi

Sırayla, her biri **gerçekten denenerek**:

- [ ] Pages sitesi canlı ve bağlantı ekranı açılıyor.
- [ ] `go install github.com/aliosmanmho/PeerPages/agent@latest` **temiz bir
      makinede** çalışıyor (ya da en azından temiz bir `GOPATH`'te).
- [ ] Ajan bayraksız çalıştırıldığında bastığı URL **gerçekten açılıyor**
      (`DefaultAppURL` doğru).
- [ ] README'deki her komut kopyala-çalıştır: yazım hatası olan tek bir komut
      Show HN'de ilk yorumu belirler.
- [ ] README'deki her bağlantı çözülüyor (görseller dahil).
- [ ] `docs/SECURITY.md`'deki bildirim kanalı gerçekten çalışıyor (Security
      Advisories sekmesi açık mı).
- [ ] GIF eklendi ve 2 MB altında.
- [ ] Depo açıklaması (`gh repo edit --description`) ve `homepageUrl` dolu;
      topics eklendi (`webrtc`, `nostr`, `tunnel`, `service-worker`, `golang`).
- [ ] İlk etiket kararı: etiketsiz lansmanda indirme linki yok, yalnızca
      `go install`. `v0.1.0` atmak ister misin? (`docs/INSTALL.md`'deki
      "henüz etiket yok" notu o zaman kalkar.)
- [ ] Lansman günü için 2–3 saat ayır: Show HN'de ilk saat içinde gelen
      sorulara cevap vermemek, gönderiyi öldürür.

**Zamanlama:** Show HN için Salı–Perşembe, 08:00–10:00 ET (TSİ 15:00–17:00)
istatistiksel olarak en iyi pencere. Reddit gönderilerini aynı güne yığma;
HN'den bir gün sonra paylaş ki soruların ikisine birden yetişebil.
