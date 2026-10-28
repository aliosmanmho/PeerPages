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

// NOT tried against a real Cloudflare account: only the request shape
// and the response parsing are checked here. The live verification is on
// the user's side (docs/TURN.md).

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
		KeyID: "key123",
		Token: "jeton-abc",
		TTL:   2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasSuffix(gotPath, "/key123/credentials/generate") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer jeton-abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if ttl, _ := gotBody["ttl"].(float64); int(ttl) != 7200 {
		t.Errorf("the ttl in the body = %v, 7200 expected", gotBody["ttl"])
	}

	if len(creds.URLs) != 2 {
		t.Fatalf("%d addresses, 2 expected: %v", len(creds.URLs), creds.URLs)
	}
	if creds.Username != "gecici-kullanici" || creds.Password != "gecici-sifre" {
		t.Errorf("the credentials could not be read: %+v", creds)
	}
	if creds.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is empty")
	}
}

// `urls` may also be a single string.
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
		t.Errorf("addresses = %v", creds.URLs)
	}
}

func TestFetchCredentialsErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"yetkisiz", http.StatusUnauthorized, `{"errors":[{"message":"bad token"}]}`, "unexpected status 401"},
		{"malformed json", http.StatusOK, `this is not json`, "the response is not JSON"},
		{"no address", http.StatusOK, `{"iceServers":{"urls":[],"username":"u","credential":"p"}}`, "carries no TURN address"},
		{"no credentials", http.StatusOK, `{"iceServers":{"urls":["turn:a.example.com:3478"]}}`, "carries no credentials"},
		// If Cloudflare returns STUN only, we have no TURN.
		{"stun only", http.StatusOK, `{"iceServers":{"urls":["stun:a.example.com:3478"],"username":"u","credential":"p"}}`, "carries no turn:/turns: address"},
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
				t.Fatal("an error was expected")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error %q should have contained %q", err.Error(), c.want)
			}
		})
	}
}

func TestFetchCredentialsNeedsKeyAndToken(t *testing.T) {
	if _, err := FetchCredentials(context.Background(), CloudflareOptions{Token: "t"}); err == nil {
		t.Error("an error was expected without a key id")
	}
	if _, err := FetchCredentials(context.Background(), CloudflareOptions{KeyID: "k"}); err == nil {
		t.Error("an error was expected without a token")
	}
}

// The token must not appear in any error text: the errors get written
// to the log.
func TestFetchCredentialsHidesToken(t *testing.T) {
	const token = "very-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"message":"nope"}]}`))
	}))
	defer srv.Close()

	_, err := fetchFrom(t, srv.URL, CloudflareOptions{KeyID: "k", Token: token})
	if err == nil {
		t.Fatal("an error was expected")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the error contains the token: %v", err)
	}
}

// Provider puts the current credentials on top of the static
// configuration.
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
		t.Errorf("the credentials were not merged: %+v", cfg)
	}

	servers, policy := p.Servers()
	if len(servers) != 2 {
		t.Errorf("%d server entries, 2 expected", len(servers))
	}
	if policy.String() != "all" {
		t.Errorf("politika %v", policy)
	}
	if strings.Contains(cfg.Describe(), "cf-sifre") {
		t.Error("Describe writes the password out")
	}
}

// With no credentials the static configuration stays as it is.
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
		t.Errorf("the static configuration changed: %+v", cfg)
	}
}

// The refresh happens at 80% of the lifetime; immediately when it is
// nearly over (and at least a minute later).
func TestRefreshIn(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	p := &Provider{
		log:  quietLogger(),
		opts: CloudflareOptions{KeyID: "k", Token: "t", TTL: 10 * time.Hour},
		now:  func() time.Time { return now },
	}

	// A 10-hour lifetime, just fetched: once 80% has passed, so in 8
	// hours.
	p.creds = Credentials{ExpiresAt: now.Add(10 * time.Hour)}
	if got := p.refreshIn(); got != 8*time.Hour {
		t.Errorf("refreshIn = %v, 8h expected", got)
	}

	// About to expire: immediately, but at least a minute later (so there
	// is no tight loop).
	p.creds = Credentials{ExpiresAt: now.Add(time.Second)}
	if got := p.refreshIn(); got != time.Minute {
		t.Errorf("refreshIn = %v, 1m expected", got)
	}

	// Already expired.
	p.creds = Credentials{ExpiresAt: now.Add(-time.Hour)}
	if got := p.refreshIn(); got != time.Minute {
		t.Errorf("refreshIn = %v, 1m expected", got)
	}
}

// If a refresh fails the old credentials keep being used: we do not want
// to lose working TURN over a transient API error.
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
		t.Errorf("the old credentials were not kept: %+v", cfg)
	}
	_ = calls
}

// fetchFrom makes the request against the httptest server in the test.
//
// Because FetchCredentials keeps the endpoint fixed (which is the right
// thing in production) we point the HTTP client at the server for the
// test.
func fetchFrom(t *testing.T, base string, opts CloudflareOptions) (Credentials, error) {
	t.Helper()
	opts.HTTPClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: redirectTo(base),
	}
	return FetchCredentials(context.Background(), opts)
}

// redirectTo is a transport that sends every request to the given
// address.
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
