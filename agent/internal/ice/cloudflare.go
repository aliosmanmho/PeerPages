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

// Cloudflare Realtime TURN support.
//
// WHY THERE IS NO WORKER: BRIEF phase 5 planned a 30-line Worker under
// `cloudflare-turn/` to mint short-lived credentials. The Worker's only
// job was to hide the API token FROM THE BROWSER — and since in PeerPages
// only the AGENT uses TURN and the token already sits on the owner's
// machine, the server in between is unnecessary. The agent fetches the
// credentials from the API itself, which removes the one "piece that
// needs a server" entirely and keeps the zero-server claim intact even
// with TURN. The decision and its reasoning: ../../docs/BRIEF.md section
// 8.

const (
	// cloudflareAPIBase is the endpoint that mints credentials.
	cloudflareAPIBase = "https://rtc.live.cloudflare.com/v1/turn/keys"
	// DefaultCloudflareTTL is the credential lifetime we ask for.
	DefaultCloudflareTTL = 12 * time.Hour
	// cloudflareTimeout bounds a single API call.
	cloudflareTimeout = 15 * time.Second
	// refreshFraction: the credential is refreshed once this much of its
	// lifetime has passed.
	refreshFraction = 0.8
)

// CloudflareOptions is the Cloudflare TURN configuration.
type CloudflareOptions struct {
	// KeyID is the Cloudflare TURN key id.
	KeyID string
	// Token is the API token. It is NEVER logged.
	Token string
	// TTL is the credential lifetime we ask for; DefaultCloudflareTTL
	// when 0.
	TTL time.Duration
	// HTTPClient can be injected for tests; the default when nil.
	HTTPClient *http.Client
	// Now is the clock for tests; time.Now when nil.
	Now func() time.Time
}

// Credentials are the credentials Cloudflare returns.
type Credentials struct {
	URLs     []string
	Username string
	Password string
	// ExpiresAt is when the credentials expire (by the local clock).
	ExpiresAt time.Time
}

// cloudflareResponse is the API response.
//
// Cloudflare returns `iceServers` as a single object; because `urls` can
// be either a string or an array we read it flexibly through
// json.RawMessage.
type cloudflareResponse struct {
	ICEServers struct {
		URLs       json.RawMessage `json:"urls"`
		Username   string          `json:"username"`
		Credential string          `json:"credential"`
	} `json:"iceServers"`
}

// FetchCredentials gets short-lived TURN credentials from Cloudflare.
func FetchCredentials(ctx context.Context, opts CloudflareOptions) (Credentials, error) {
	if strings.TrimSpace(opts.KeyID) == "" {
		return Credentials{}, errf("cloudflare: the key id is empty")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return Credentials{}, errf("cloudflare: the API token is empty")
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
		// The error text carries the URL but no token: the token only
		// lives in the header.
		return Credentials{}, errf("cloudflare: the request failed: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	// We read the response with a bound: the remote side is untrusted.
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return Credentials{}, errf("cloudflare: the response could not be read: %v", err)
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return Credentials{}, errf("cloudflare: unexpected status %d (%s)",
			res.StatusCode, firstLine(raw))
	}

	var parsed cloudflareResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Credentials{}, errf("cloudflare: the response is not JSON: %v", err)
	}

	urls, err := decodeURLs(parsed.ICEServers.URLs)
	if err != nil {
		return Credentials{}, err
	}
	if len(urls) == 0 {
		return Credentials{}, errf("cloudflare: the response carries no TURN address")
	}
	if parsed.ICEServers.Username == "" || parsed.ICEServers.Credential == "" {
		return Credentials{}, errf("cloudflare: the response carries no credentials")
	}

	normalized, err := Normalize(urls)
	if err != nil {
		return Credentials{}, errf("cloudflare: the returned address could not be used: %v", err)
	}
	// It can return STUN addresses too; we need TURN.
	turn := make([]string, 0, len(normalized))
	for _, u := range normalized {
		if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
			turn = append(turn, u)
		}
	}
	if len(turn) == 0 {
		return Credentials{}, errf("cloudflare: the response carries no turn:/turns: address")
	}

	return Credentials{
		URLs:      turn,
		Username:  parsed.ICEServers.Username,
		Password:  parsed.ICEServers.Credential,
		ExpiresAt: now().Add(ttl),
	}, nil
}

// decodeURLs reads the `urls` field: it may be a string or an array of
// strings.
func decodeURLs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errf("cloudflare: the response has no urls field")
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	return nil, errf("cloudflare: the urls field could not be read")
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

// Provider holds the current ICE server list and refreshes the
// Cloudflare credentials before they expire.
//
// Open sessions are unaffected by a refresh: a TURN allocation lives
// independently of the credential lifetime, which only matters for a NEW
// allocation.
type Provider struct {
	base  Config
	log   *slog.Logger
	opts  CloudflareOptions
	now   func() time.Time
	fetch func(context.Context, CloudflareOptions) (Credentials, error)

	mu    sync.RWMutex
	creds Credentials
}

// NewProvider returns a provider for static configuration only.
func NewProvider(cfg Config, log *slog.Logger) *Provider {
	return &Provider{base: cfg, log: log, now: time.Now, fetch: FetchCredentials}
}

// NewCloudflareProvider fetches the first credentials from Cloudflare
// and returns the provider.
//
// If the first fetch FAILS it returns an error and the agent does not
// start: if the user asked for TURN, carrying on without it silently
// would be wrong — visitors who cannot stay connected would never learn
// why.
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
	log.Info("cloudflare turn credentials fetched",
		slog.Int("addresses", len(creds.URLs)),
		slog.Time("valid_until", creds.ExpiresAt))
	return p, nil
}

// Config returns the current ICE configuration.
func (p *Provider) Config() Config {
	p.mu.RLock()
	creds := p.creds
	p.mu.RUnlock()

	cfg := p.base
	if len(creds.URLs) == 0 {
		return cfg
	}
	cfg.TURN = append(append([]string(nil), cfg.TURN...), creds.URLs...)
	// Cloudflare credentials OVERRIDE the static ones: when both are
	// given we can only carry one user/password pair, and the fresh one is
	// the right one.
	cfg.User = creds.Username
	cfg.Pass = creds.Password
	return cfg
}

// Servers matches the connect.ICEFactory signature.
func (p *Provider) Servers() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
	cfg := p.Config()
	return cfg.Servers(), cfg.Policy()
}

// Run refreshes the credentials before they expire; it returns once ctx
// ends.
//
// If a refresh fails the old list keeps being used until it expires and a
// warning is logged: we do not want to lose working TURN over a transient
// API error.
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
			p.log.Warn("cloudflare turn credentials could not be refreshed, using the old ones",
				slog.String("err", err.Error()))
			continue
		}
		p.mu.Lock()
		p.creds = creds
		p.mu.Unlock()
		p.log.Info("cloudflare turn credentials refreshed",
			slog.Time("valid_until", creds.ExpiresAt))
	}
}

// refreshIn is the time until the next refresh.
func (p *Provider) refreshIn() time.Duration {
	p.mu.RLock()
	exp := p.creds.ExpiresAt
	p.mu.RUnlock()

	ttl := p.opts.TTL
	if ttl <= 0 {
		ttl = DefaultCloudflareTTL
	}
	// Refresh once 80% of the lifetime has passed; immediately if less
	// than that is left.
	deadline := exp.Add(-time.Duration(float64(ttl) * (1 - refreshFraction)))
	wait := deadline.Sub(p.now())

	// Wait at least a minute: if the API keeps erroring we must not spin
	// in a tight loop.
	if wait < time.Minute {
		return time.Minute
	}
	return wait
}
