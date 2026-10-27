package ice

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Gercek Cloudflare hesabina karsi DENENMEDI: burada yalnizca istegin
// bicimi ve cevap ayristirmasi dogrulaniyor. Canli dogrulama kullanici
// tarafinda (docs/TURN.md).

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

const cfBody = `{"iceServers":{
  "urls":["turn:turn.cloudflare.com:3478?transport=udp","turns:turn.cloudflare.com:5349?transport=tcp"],
  "username":"gecici-kullanici",
  "credential":"gecici-sifre"}}`

func TestFetchCredentials(t *testing.T) {
	var gotPath, gotAuth, gotType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(cfBody))
	}))
	defer srv.Close()

	creds, err := fetchFrom(t, srv.URL, CloudflareOptions{
		KeyID: "anahtar123",
		Token: "jeton-abc",
		TTL:   2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}

	if !strings.HasSuffix(gotPath, "/anahtar123/credentials/generate") {
		t.Errorf("yol = %q", gotPath)
	}
	if gotAuth != "Bearer jeton-abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if ttl, _ := gotBody["ttl"].(float64); int(ttl) != 7200 {
		t.Errorf("govdedeki ttl = %v, 7200 beklendi", gotBody["ttl"])
	}

	if len(creds.URLs) != 2 {
		t.Fatalf("adres sayisi %d, 2 beklendi: %v", len(creds.URLs), creds.URLs)
	}
	if creds.Username != "gecici-kullanici" || creds.Password != "gecici-sifre" {
		t.Errorf("kimlik bilgisi okunamadi: %+v", creds)
	}
	if creds.ExpiresAt.IsZero() {
		t.Error("ExpiresAt bos")
	}
}

// `urls` tek string de olabiliyor.
func TestFetchCredentialsSingleURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(
			`{"iceServers":{"urls":"turn:a.example.com:3478","username":"u","credential":"p"}}`))
	}))
	defer srv.Close()

	creds, err := fetchFrom(t, srv.URL, CloudflareOptions{KeyID: "k", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.URLs) != 1 || creds.URLs[0] != "turn:a.example.com:3478" {
		t.Errorf("adresler = %v", creds.URLs)
	}
}

func TestFetchCredentialsErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"yetkisiz", http.StatusUnauthorized, `{"errors":[{"message":"bad token"}]}`, "beklenmeyen durum 401"},
		{"bozuk json", http.StatusOK, `bu json degil`, "cevap JSON degil"},
		{"adres yok", http.StatusOK, `{"iceServers":{"urls":[],"username":"u","credential":"p"}}`, "TURN adresi yok"},
		{"kimlik yok", http.StatusOK, `{"iceServers":{"urls":["turn:a.example.com:3478"]}}`, "kimlik bilgisi yok"},
		// Cloudflare yalnizca STUN donerse TURN'umuz yok demektir.
		{"yalnizca stun", http.StatusOK, `{"iceServers":{"urls":["stun:a.example.com:3478"],"username":"u","credential":"p"}}`, "turn:/turns: adresi yok"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			_, err := fetchFrom(t, srv.URL, CloudflareOptions{KeyID: "k", Token: "t"})
			if err == nil {
				t.Fatal("hata beklendi")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("hata %q, %q icermeliydi", err.Error(), c.want)
			}
		})
	}
}

func TestFetchCredentialsNeedsKeyAndToken(t *testing.T) {
	if _, err := FetchCredentials(context.Background(), CloudflareOptions{Token: "t"}); err == nil {
		t.Error("anahtar kimligi olmadan hata beklendi")
	}
	if _, err := FetchCredentials(context.Background(), CloudflareOptions{KeyID: "k"}); err == nil {
		t.Error("jeton olmadan hata beklendi")
	}
}

// Jeton hicbir hata metnine girmemeli: hatalar log'a yaziliyor.
func TestFetchCredentialsHidesToken(t *testing.T) {
	const token = "cok-gizli-jeton"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"message":"nope"}]}`))
	}))
	defer srv.Close()

	_, err := fetchFrom(t, srv.URL, CloudflareOptions{KeyID: "k", Token: token})
	if err == nil {
		t.Fatal("hata beklendi")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("hata jetonu iceriyor: %v", err)
	}
}

// Provider gecerli kimlik bilgisini statik yapilandirmanin ustune koyar.
func TestProviderMergesCredentials(t *testing.T) {
	base, err := Config{STUN: []string{"stun:a.example.com:3478"}}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	p := &Provider{
		base: base,
		log:  quietLogger(),
		now:  time.Now,
		creds: Credentials{
			URLs:      []string{"turn:cf.example.com:3478"},
			Username:  "cf-kullanici",
			Password:  "cf-sifre",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}

	cfg := p.Config()
	if len(cfg.TURN) != 1 || cfg.TURN[0] != "turn:cf.example.com:3478" {
		t.Errorf("TURN = %v", cfg.TURN)
	}
	if cfg.User != "cf-kullanici" || cfg.Pass != "cf-sifre" {
		t.Errorf("kimlik bilgisi birlesmedi: %+v", cfg)
	}

	servers, policy := p.Servers()
	if len(servers) != 2 {
		t.Errorf("sunucu girdisi %d, 2 beklendi", len(servers))
	}
	if policy.String() != "all" {
		t.Errorf("politika %v", policy)
	}
	if strings.Contains(cfg.Describe(), "cf-sifre") {
		t.Error("Describe sifreyi yaziyor")
	}
}

// Kimlik bilgisi yoksa statik yapilandirma oldugu gibi kalir.
func TestProviderWithoutCredentials(t *testing.T) {
	base, err := Config{
		STUN: []string{"stun:a.example.com:3478"},
		TURN: []string{"turn:t.example.com:3478"},
		User: "u",
		Pass: "p",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	p := NewProvider(base, quietLogger())
	cfg := p.Config()
	if len(cfg.TURN) != 1 || cfg.User != "u" {
		t.Errorf("statik yapilandirma degisti: %+v", cfg)
	}
}

// Yenileme omrun %80'inde; omur neredeyse bitmisse hemen (en az 1 dakika).
func TestRefreshIn(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	p := &Provider{
		log:  quietLogger(),
		opts: CloudflareOptions{KeyID: "k", Token: "t", TTL: 10 * time.Hour},
		now:  func() time.Time { return now },
	}

	// 10 saatlik omur, yeni alinmis: %80'i gecince, yani 8 saat sonra.
	p.creds = Credentials{ExpiresAt: now.Add(10 * time.Hour)}
	if got := p.refreshIn(); got != 8*time.Hour {
		t.Errorf("refreshIn = %v, 8h beklendi", got)
	}

	// Omru bitmek uzere: hemen, ama en az bir dakika sonra (sonsuz dongu
	// olmasin).
	p.creds = Credentials{ExpiresAt: now.Add(time.Second)}
	if got := p.refreshIn(); got != time.Minute {
		t.Errorf("refreshIn = %v, 1m beklendi", got)
	}

	// Omru gecmis.
	p.creds = Credentials{ExpiresAt: now.Add(-time.Hour)}
	if got := p.refreshIn(); got != time.Minute {
		t.Errorf("refreshIn = %v, 1m beklendi", got)
	}
}

// Yenileme basarisiz olursa eski kimlik bilgisi kullanilmaya devam eder:
// gecici bir API hatasi yuzunden calisan TURN'u kaybetmek istemiyoruz.
func TestProviderKeepsOldCredentialsOnFailure(t *testing.T) {
	old := Credentials{
		URLs:      []string{"turn:eski.example.com:3478"},
		Username:  "eski",
		Password:  "eski-sifre",
		ExpiresAt: time.Now().Add(time.Hour),
	}

	calls := 0
	p := &Provider{
		base: Config{},
		log:  quietLogger(),
		opts: CloudflareOptions{KeyID: "k", Token: "t", TTL: time.Minute},
		now:  time.Now,
		fetch: func(context.Context, CloudflareOptions) (Credentials, error) {
			calls++
			return Credentials{}, errf("API dustu")
		},
		creds: old,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	p.Run(ctx) // ctx bitince doner

	if cfg := p.Config(); cfg.User != "eski" {
		t.Errorf("eski kimlik bilgisi korunmadi: %+v", cfg)
	}
	_ = calls
}

// fetchFrom testteki httptest sunucusuna istek atar.
//
// FetchCredentials uc noktayi sabit tuttugu icin (uretimde dogru olan bu)
// test icin HTTP istemcisini sunucuya yonlendiriyoruz.
func fetchFrom(t *testing.T, base string, opts CloudflareOptions) (Credentials, error) {
	t.Helper()
	opts.HTTPClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: redirectTo(base),
	}
	return FetchCredentials(context.Background(), opts)
}

// redirectTo tum istekleri verilen adrese yonlendiren bir taşıyıcı.
type redirectTransport struct{ base string }

func redirectTo(base string) http.RoundTripper { return &redirectTransport{base: base} }

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := req.URL.Parse(r.base)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
