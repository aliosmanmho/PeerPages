package ice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// Cloudflare Realtime TURN destegi.
//
// NEDEN WORKER YOK: BRIEF Faz 5, kisa omurlu kimlik bilgisi uretmek icin
// `cloudflare-turn/` altinda 30 satirlik bir Worker ongoruyordu. Worker'in
// tek isi API jetonunu TARAYICIDAN saklamakti — PeerPages'te TURN'u yalnizca
// AJAN kullandigi ve jeton zaten sahibinin makinesinde durdugu icin aradaki
// sunucu gereksiz. Ajan kimlik bilgisini API'den kendisi cekiyor, boylece
// "tek sunucu gerektiren parca" tamamen ortadan kalkiyor ve sifir sunucu
// iddiasi TURN'le bile bozulmuyor. Karar ve gerekcesi: ../../docs/BRIEF.md §8.

const (
	// cloudflareAPIBase kimlik bilgisi ureten uc nokta.
	cloudflareAPIBase = "https://rtc.live.cloudflare.com/v1/turn/keys"
	// DefaultCloudflareTTL istenen kimlik bilgisi omru.
	DefaultCloudflareTTL = 12 * time.Hour
	// cloudflareTimeout tek bir API cagrisi icin ust sinir.
	cloudflareTimeout = 15 * time.Second
	// refreshFraction kimlik bilgisi omrunun bu kadari gecince yenilenir.
	refreshFraction = 0.8
)

// CloudflareOptions Cloudflare TURN yapilandirmasi.
type CloudflareOptions struct {
	// KeyID Cloudflare TURN anahtar kimligi.
	KeyID string
	// Token API jetonu. ASLA loglanmaz.
	Token string
	// TTL istenen kimlik bilgisi omru; 0 ise DefaultCloudflareTTL.
	TTL time.Duration
	// HTTPClient test icin enjekte edilebilir; nil ise varsayilan.
	HTTPClient *http.Client
	// Now test icin saat; nil ise time.Now.
	Now func() time.Time
}

// Credentials Cloudflare'den donen kimlik bilgisi.
type Credentials struct {
	URLs     []string
	Username string
	Password string
	// ExpiresAt kimlik bilgisinin gecerlilik sonu (yerel saate gore).
	ExpiresAt time.Time
}

// cloudflareResponse API cevabi.
//
// Cloudflare `iceServers`'i tek nesne olarak donuyor; `urls` hem string hem
// dizi olabildigi icin json.RawMessage uzerinden esnek okuyoruz.
type cloudflareResponse struct {
	ICEServers struct {
		URLs       json.RawMessage `json:"urls"`
		Username   string          `json:"username"`
		Credential string          `json:"credential"`
	} `json:"iceServers"`
}

// FetchCredentials Cloudflare'den kisa omurlu TURN kimlik bilgisi alir.
func FetchCredentials(ctx context.Context, opts CloudflareOptions) (Credentials, error) {
	if strings.TrimSpace(opts.KeyID) == "" {
		return Credentials{}, errf("cloudflare: anahtar kimligi (key id) bos")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return Credentials{}, errf("cloudflare: API jetonu bos")
	}

	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultCloudflareTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cloudflareTimeout}
	}

	body, err := json.Marshal(map[string]any{"ttl": int(ttl.Seconds())})
	if err != nil {
		return Credentials{}, err
	}

	url := fmt.Sprintf("%s/%s/credentials/generate", cloudflareAPIBase, opts.KeyID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)
	req.Header.Set("Content-Type", "application/json")

	res, err := client.Do(req)
	if err != nil {
		// Hata metninde URL var ama jeton yok: jeton yalnizca baslikta.
		return Credentials{}, errf("cloudflare: istek basarisiz: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Cevabi sinirli okuyoruz: uzak taraf guvenilmez.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return Credentials{}, errf("cloudflare: cevap okunamadi: %v", err)
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return Credentials{}, errf("cloudflare: beklenmeyen durum %d (%s)",
			res.StatusCode, firstLine(raw))
	}

	var parsed cloudflareResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Credentials{}, errf("cloudflare: cevap JSON degil: %v", err)
	}

	urls, err := decodeURLs(parsed.ICEServers.URLs)
	if err != nil {
		return Credentials{}, err
	}
	if len(urls) == 0 {
		return Credentials{}, errf("cloudflare: cevapta TURN adresi yok")
	}
	if parsed.ICEServers.Username == "" || parsed.ICEServers.Credential == "" {
		return Credentials{}, errf("cloudflare: cevapta kimlik bilgisi yok")
	}

	normalized, err := Normalize(urls)
	if err != nil {
		return Credentials{}, errf("cloudflare: donen adres kullanilamadi: %v", err)
	}
	// STUN adresleri de donebiliyor; bize TURN gerekiyor.
	turn := make([]string, 0, len(normalized))
	for _, u := range normalized {
		if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
			turn = append(turn, u)
		}
	}
	if len(turn) == 0 {
		return Credentials{}, errf("cloudflare: cevapta turn:/turns: adresi yok")
	}

	return Credentials{
		URLs:      turn,
		Username:  parsed.ICEServers.Username,
		Password:  parsed.ICEServers.Credential,
		ExpiresAt: now().Add(ttl),
	}, nil
}

// decodeURLs `urls` alanini okur: string ya da string dizisi olabilir.
func decodeURLs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errf("cloudflare: cevapta urls alani yok")
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	return nil, errf("cloudflare: urls alani okunamadi")
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Provider gecerli ICE sunucu listesini tutar ve Cloudflare kimlik bilgisini
// suresi dolmadan yeniler.
//
// Acik oturumlar yenilemeden etkilenmez: TURN allocation'i kimlik bilgisinin
// omrunden bagimsiz yasar, sure yalnizca YENI allocation icin gerekir.
type Provider struct {
	base  Config
	log   *slog.Logger
	opts  CloudflareOptions
	now   func() time.Time
	fetch func(context.Context, CloudflareOptions) (Credentials, error)

	mu    sync.RWMutex
	creds Credentials
}

// NewProvider yalnizca statik yapilandirma icin bir saglayici dondurur.
func NewProvider(cfg Config, log *slog.Logger) *Provider {
	return &Provider{base: cfg, log: log, now: time.Now, fetch: FetchCredentials}
}

// NewCloudflareProvider Cloudflare'den ilk kimlik bilgisini alir ve
// saglayiciyi dondurur.
//
// Ilk cekim BASARISIZ olursa hata doner ve ajan baslamaz: kullanici TURN
// istediyse sessizce TURN'suz devam etmek yanlis olur — bagli kalamayan
// ziyaretciler sebebini hic ogrenemezdi.
func NewCloudflareProvider(
	ctx context.Context, cfg Config, opts CloudflareOptions, log *slog.Logger,
) (*Provider, error) {
	p := &Provider{base: cfg, log: log, opts: opts, now: time.Now, fetch: FetchCredentials}
	if opts.Now != nil {
		p.now = opts.Now
	}
	creds, err := p.fetch(ctx, opts)
	if err != nil {
		return nil, err
	}
	p.creds = creds
	log.Info("cloudflare turn kimlik bilgisi alindi",
		slog.Int("adres", len(creds.URLs)),
		slog.Time("gecerli", creds.ExpiresAt))
	return p, nil
}

// Config gecerli ICE yapilandirmasini dondurur.
func (p *Provider) Config() Config {
	p.mu.RLock()
	creds := p.creds
	p.mu.RUnlock()

	cfg := p.base
	if len(creds.URLs) == 0 {
		return cfg
	}
	cfg.TURN = append(append([]string(nil), cfg.TURN...), creds.URLs...)
	// Cloudflare kimlik bilgisi statik olani EZER: ikisi birlikte verildiginde
	// tek bir kullanici/sifre cifti tasiyabiliyoruz ve taze olan dogru olan.
	cfg.User = creds.Username
	cfg.Pass = creds.Password
	return cfg
}

// Servers connect.ICEFactory imzasina uyar.
func (p *Provider) Servers() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
	cfg := p.Config()
	return cfg.Servers(), cfg.Policy()
}

// Run kimlik bilgisini suresi dolmadan yeniler; ctx bitince doner.
//
// Yenileme basarisiz olursa eski liste suresi bitene kadar kullanilmaya
// devam eder ve uyari loglanir: gecici bir API hatasi yuzunden calisan
// TURN'u kaybetmek istemiyoruz.
func (p *Provider) Run(ctx context.Context) {
	if p.opts.KeyID == "" {
		return // statik yapilandirma: yenilenecek bir sey yok
	}

	for {
		wait := p.refreshIn()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		creds, err := p.fetch(ctx, p.opts)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.log.Warn("cloudflare turn kimlik bilgisi yenilenemedi, eskisi kullaniliyor",
				slog.String("err", err.Error()))
			continue
		}
		p.mu.Lock()
		p.creds = creds
		p.mu.Unlock()
		p.log.Info("cloudflare turn kimlik bilgisi yenilendi",
			slog.Time("gecerli", creds.ExpiresAt))
	}
}

// refreshIn bir sonraki yenilemeye kalan sure.
func (p *Provider) refreshIn() time.Duration {
	p.mu.RLock()
	exp := p.creds.ExpiresAt
	p.mu.RUnlock()

	ttl := p.opts.TTL
	if ttl <= 0 {
		ttl = DefaultCloudflareTTL
	}
	// Omrun %80'i gecince yenile; kalan sure bundan kisaysa hemen.
	deadline := exp.Add(-time.Duration(float64(ttl) * (1 - refreshFraction)))
	wait := deadline.Sub(p.now())

	// En az bir dakika bekle: API hatasi donup duruyorsa sonsuz donguye
	// girmeyelim.
	if wait < time.Minute {
		return time.Minute
	}
	return wait
}
