// Package nostr PeerPages'in Nostr relay katmani.
//
// Signaling icin kullanilan tek event turu: ephemeral kind 25050, icerigi
// NIP-44 ile sifreli, `#p` tag'i alici pubkey (README bolum 4). Relay'ler
// yalnizca "bir pubkey bir sey aldi" gorur (guvenlik ilkesi 2).
//
// Bu paket go-nostr'un SimplePool'unu ince bir katmanla sarar; amac
// README'nin iki kuralini tek yerde uygulamak:
//   - Ayni mesaj TUM relay'lere yollanir ve ILK basari yeterlidir (hedged).
//   - Bir relay dusse digerleri calismaya devam eder.
package nostr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
)

// KindSignaling signaling event'lerinin kind'i.
//
// 20000-29999 araligi NIP-01'e gore **ephemeral**: relay'lerin saklamasi
// beklenmez, abonelere iletip atarlar. Signaling icin dogru secim, cunku
// offer/answer'in kalici olmasini istemiyoruz.
const KindSignaling = 25050

// TagRecipient alici pubkey'i tasiyan tag adi.
const TagRecipient = "p"

// DefaultRelays varsayilan relay listesi.
//
// 2026-10-26'da `peerpages doctor` ile CANLI dogrulandi: dordu de baglandi,
// ephemeral kind 25050 event'i kabul etti ve aboneye geri iletti
// (yanki 72-103 ms). Olcum: docs/MEASUREMENTS.md.
//
// README bolum 4'teki ucuncu relay (wss://relay.nostr.band) listeden cikarildi:
// gelistirme agindan WebSocket el sikismasi zaman asimina ugradi. Baska bir
// agdan calisabilir, bu yuzden "bozuk" demiyoruz — varsayilan olmaktan
// cikardik. Kendi listeni `--relay` ile verebilirsin; `peerpages doctor`
// hangilerinin ise yaradigini soyler.
//
// README en az 3 istiyor; dort tutuyoruz ki biri dusunce yine 3 kalsin.
var DefaultRelays = []string{
	"wss://nos.lol",
	"wss://relay.primal.net",
	"wss://nostr.mom",
	"wss://relay.damus.io",
}

// Config havuz yapilandirmasi.
type Config struct {
	// URLs relay adresleri (wss:// ya da ws://).
	URLs []string
	// Logger zorunlu.
	Logger *slog.Logger
}

// Pool birden cok relay'i birlikte kullanan havuz.
type Pool struct {
	pool *gonostr.SimplePool
	urls []string
	log  *slog.Logger
}

// NormalizeURL relay adresini dogrular ve normalize eder.
//
// Yalnizca ws/wss kabul edilir; sondaki bolu atilir ki ayni relay iki kez
// listelenmesin.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("relay adresi bos")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("relay adresi okunamadi %q: %w", raw, err)
	}
	switch u.Scheme {
	case "ws", "wss":
	default:
		return "", fmt.Errorf("relay adresi ws:// ya da wss:// olmali, verilen: %q", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("relay adresinde host yok: %q", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.Fragment = ""
	return u.String(), nil
}

// NormalizeURLs listeyi normalize eder ve tekrarlari ayiklar.
func NormalizeURLs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("en az bir relay gerekli")
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		u, err := NormalizeURL(r)
		if err != nil {
			return nil, err
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}

// NewPool havuzu kurar. ctx havuzun yasam suresini belirler.
func NewPool(ctx context.Context, cfg Config) (*Pool, error) {
	if cfg.Logger == nil {
		return nil, errors.New("nostr: Logger nil")
	}
	urls, err := NormalizeURLs(cfg.URLs)
	if err != nil {
		return nil, err
	}
	return &Pool{
		pool: gonostr.NewSimplePool(ctx),
		urls: urls,
		log:  cfg.Logger,
	}, nil
}

// URLs havuzdaki relay adreslerini dondurur.
func (p *Pool) URLs() []string {
	out := make([]string, len(p.urls))
	copy(out, p.urls)
	return out
}

// PublishReport bir yayinin relay bazinda sonucu.
type PublishReport struct {
	// OK yayinin kabul edildigi relay'ler.
	OK []string
	// Failed basarisiz relay'ler ve nedenleri.
	Failed map[string]string
	// FirstOK ilk kabul edene kadar gecen sure.
	FirstOK time.Duration
	// Elapsed tum relay'ler cevap verene (ya da ctx bitene) kadar gecen sure.
	Elapsed time.Duration
}

// Succeeded en az bir relay kabul etti mi.
func (r PublishReport) Succeeded() bool { return len(r.OK) > 0 }

// Publish event'i tum relay'lere yollar.
//
// Hedged: EN AZ BIR relay kabul ederse basarili sayilir ve hata donmez.
// Hicbiri kabul etmezse hata doner. Cagiran ilk basariyi beklemek icin
// ctx'e sure koyabilir; fonksiyon tum cevaplari toplamaya devam etmez,
// ilk basaridan sonra kalan relay'leri arka planda birakir.
func (p *Pool) Publish(ctx context.Context, ev gonostr.Event) (PublishReport, error) {
	started := time.Now()
	report := PublishReport{Failed: map[string]string{}}

	results := p.pool.PublishMany(ctx, p.urls, ev)

	for i := 0; i < len(p.urls); i++ {
		select {
		case res, ok := <-results:
			if !ok {
				i = len(p.urls)
				break
			}
			if res.Error != nil {
				report.Failed[res.RelayURL] = res.Error.Error()
				continue
			}
			if len(report.OK) == 0 {
				report.FirstOK = time.Since(started)
			}
			report.OK = append(report.OK, res.RelayURL)
		case <-ctx.Done():
			report.Elapsed = time.Since(started)
			if report.Succeeded() {
				// Ilk basari yeterli; kalanlari beklemeyi birakiyoruz.
				return report, nil
			}
			return report, fmt.Errorf("yayin tamamlanamadi: %w", ctx.Err())
		}
	}

	report.Elapsed = time.Since(started)
	if !report.Succeeded() {
		return report, fmt.Errorf("hicbir relay event'i kabul etmedi (%d relay denendi)", len(p.urls))
	}

	p.log.Debug("event yayinlandi",
		slog.Int("ok", len(report.OK)),
		slog.Int("basarisiz", len(report.Failed)),
		slog.Duration("ilk_ok", report.FirstOK))
	return report, nil
}

// ConnectReport onceden baglanma sonucu.
type ConnectReport struct {
	// OK baglanilabilen relay'ler.
	OK []string
	// Failed baglanilamayan relay'ler ve nedenleri.
	Failed map[string]string
	// Elapsed tum denemelerin suresi.
	Elapsed time.Duration
}

// Connect relay'lere onceden baglanir.
//
// Neden gerekli: abonelik kurulmadan "hazirim" demek yanlis. Ephemeral
// event'ler saklanmadigi icin, ajan dinlemeye baslamadan gelen bir offer
// GERI DONUSU OLMADAN kaybolur. Bu yuzden ajan once baglanir, sonra abone
// olur, en son "hazir" der.
//
// EN AZ BIR relay'e baglanilabilirse basarili sayilir (hedged).
func (p *Pool) Connect(ctx context.Context) (ConnectReport, error) {
	started := time.Now()
	report := ConnectReport{Failed: map[string]string{}}

	type result struct {
		url string
		err error
	}
	results := make(chan result, len(p.urls))

	for _, u := range p.urls {
		go func(u string) {
			_, err := p.pool.EnsureRelay(u)
			results <- result{url: u, err: err}
		}(u)
	}

	for i := 0; i < len(p.urls); i++ {
		select {
		case r := <-results:
			if r.err != nil {
				report.Failed[r.url] = r.err.Error()
				continue
			}
			report.OK = append(report.OK, r.url)
		case <-ctx.Done():
			report.Elapsed = time.Since(started)
			if len(report.OK) > 0 {
				return report, nil
			}
			return report, fmt.Errorf("relay baglantisi tamamlanamadi: %w", ctx.Err())
		}
	}

	report.Elapsed = time.Since(started)
	if len(report.OK) == 0 {
		return report, fmt.Errorf("hicbir relay'e baglanilamadi (%d relay denendi)", len(p.urls))
	}
	p.log.Info("relay'lere baglanildi",
		slog.Int("ok", len(report.OK)),
		slog.Int("basarisiz", len(report.Failed)),
		slog.Duration("sure", report.Elapsed))
	return report, nil
}

// Subscribe tum relay'lerden gelen event'leri tek kanalda dondurur.
//
// go-nostr ayni event'i birden cok relay'den alirsa tekrarlari ayiklar;
// yine de signaling katmani event id'lerini kendi de takip eder (replay
// korumasi, internal/signaling).
//
// Kanal ctx bitince kapanir.
func (p *Pool) Subscribe(ctx context.Context, filter gonostr.Filter) <-chan gonostr.RelayEvent {
	return p.pool.SubscribeMany(ctx, p.urls, filter)
}

// Close havuzu ve tum relay baglantilarini kapatir.
func (p *Pool) Close(reason string) {
	p.pool.Close(reason)
}

// RelayHealth bir relay'in anlik durumu.
type RelayHealth struct {
	// URL relay adresi (bizim normalize ettigimiz bicimde).
	URL string
	// Connected WebSocket su an acik mi.
	Connected bool
	// Err son baglanti hatasi; yoksa bos.
	Err string
}

// Health relay basina anlik durumu dondurur.
//
// Neden gerekli: yeniden baglanmanin tamamini go-nostr'un kendi dongusu
// yapiyor ve bu is PeerPages'e hic gorunmuyordu. Ajan "hazir" dedikten
// sonra tum relay'ler dusse bile banner'daki sayi donmus kaliyor, log'a tek
// satir cikmiyordu — yani ziyaretcilerin ulasamadigi bir ajan, kendini
// calisiyor sanmaya devam ediyordu.
func (p *Pool) Health() []RelayHealth {
	out := make([]RelayHealth, 0, len(p.urls))
	for _, u := range p.urls {
		h := RelayHealth{URL: u}
		// go-nostr havuzu kendi normalize bicimiyle anahtarliyor.
		if r, ok := p.pool.Relays.Load(gonostr.NormalizeURL(u)); ok && r != nil {
			h.Connected = r.IsConnected()
			if err := r.ConnectionError; err != nil {
				h.Err = err.Error()
			}
		}
		out = append(out, h)
	}
	return out
}

// HealthyCount bagli relay sayisi.
func (p *Pool) HealthyCount() int {
	n := 0
	for _, h := range p.Health() {
		if h.Connected {
			n++
		}
	}
	return n
}

// SignalingFilter belirli bir alici pubkey'e gelen signaling event'leri icin
// filtre uretir.
//
// since: bu andan once uretilmis event'ler yok sayilir. Eski event'lerin
// tekrar oynatilmasini (replay) daha ilk adimda sinirlar.
func SignalingFilter(recipientPubKey string, since time.Time) gonostr.Filter {
	ts := gonostr.Timestamp(since.Unix())
	return gonostr.Filter{
		Kinds: []int{KindSignaling},
		Tags:  gonostr.TagMap{TagRecipient: []string{recipientPubKey}},
		Since: &ts,
	}
}

// NewSignalingEvent imzalanmamis bir signaling event'i hazirlar.
//
// content zaten sifrelenmis olmali (NIP-44); bu fonksiyon sifreleme yapmaz.
func NewSignalingEvent(recipientPubKey, content string) gonostr.Event {
	return gonostr.Event{
		CreatedAt: gonostr.Now(),
		Kind:      KindSignaling,
		Tags:      gonostr.Tags{gonostr.Tag{TagRecipient, recipientPubKey}},
		Content:   content,
	}
}

// RecipientOf event'in `#p` tag'indeki alici pubkey'ini dondurur.
func RecipientOf(ev *gonostr.Event) (string, bool) {
	if ev == nil {
		return "", false
	}
	tag := ev.Tags.GetFirst([]string{TagRecipient})
	if tag == nil || len(*tag) < 2 {
		return "", false
	}
	return (*tag)[1], true
}

// ShortPubKey loglar icin kisaltilmis pubkey (guvenlik ilkesi 6 ruhu:
// loglar gereksiz tam kimlik tasimasin).
func ShortPubKey(pub string) string {
	if len(pub) <= 12 {
		return pub
	}
	return pub[:8] + "…" + pub[len(pub)-4:]
}
