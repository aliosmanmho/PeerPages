package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// fakeSink is the test implementation of tunnel.ResponseSink; no WebRTC
// needed.
type fakeSink struct {
	mu      sync.Mutex
	status  int
	headers map[string]string
	body    bytes.Buffer
	ended   bool
	abort   string
	resSent bool
}

func (f *fakeSink) SendRes(status int, headers map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resSent {
		return errors.New("res was already sent")
	}
	f.resSent = true
	f.status = status
	f.headers = headers
	return nil
}

func (f *fakeSink) Write(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.resSent {
		return errors.New("a body with no res sent")
	}
	_, err := f.body.Write(p)
	return err
}

func (f *fakeSink) End() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = true
	return nil
}

func (f *fakeSink) Abort(reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abort = reason
	return nil
}

func (f *fakeSink) snapshot() (status int, headers map[string]string, body []byte, ended bool, abort string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.headers, f.body.Bytes(), f.ended, f.abort
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newProxyFor(t *testing.T, serverURL string) *Proxy {
	t.Helper()
	targets, err := target.Parse([]string{serverURL})
	if err != nil {
		t.Fatalf("the target could not be parsed: %v", err)
	}
	p, err := New(Config{
		Targets:    targets,
		TargetName: target.DefaultName,
		MaxBody:    1 << 20,
		Timeout:    10 * time.Second,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatalf("the proxy could not be built: %v", err)
	}
	return p
}

func TestNewValidation(t *testing.T) {
	targets, err := target.Parse([]string{"http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	ok := Config{Targets: targets, TargetName: "default", MaxBody: 1, Logger: quietLogger()}
	if _, err := New(ok); err != nil {
		t.Fatalf("a valid configuration was refused: %v", err)
	}

	bad := map[string]func(*Config){
		"a nil target":             func(c *Config) { c.Targets = nil },
		"the target name is empty": func(c *Config) { c.TargetName = "" },
		"an unknown target":        func(c *Config) { c.TargetName = "missing" },
		"maxbody sifir":            func(c *Config) { c.MaxBody = 0 },
		"logger nil":               func(c *Config) { c.Logger = nil },
	}
	for name, mutate := range bad {
		c := ok
		mutate(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: should have been refused", name)
		}
	}
}

func TestServeGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/merhaba" {
			t.Errorf("the path that reached the target: %q", r.URL.Path)
		}
		if r.URL.RawQuery != "x=1" {
			t.Errorf("query: %q", r.URL.RawQuery)
		}
		w.Header().Set("content-type", "text/plain")
		w.Header().Set("x-ozel", "value")
		_, _ = io.WriteString(w, "merhaba dunya")
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	sink := &fakeSink{}
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/merhaba?x=1", Headers: map[string]string{}, HasBody: false,
	}, nil, sink)

	status, headers, body, ended, abort := sink.snapshot()
	if abort != "" {
		t.Fatalf("abort: %s", abort)
	}
	if status != 200 {
		t.Errorf("status %d", status)
	}
	if string(body) != "merhaba dunya" {
		t.Errorf("body: %q", body)
	}
	if !ended {
		t.Error("no end arrived")
	}
	if headers["content-type"] != "text/plain" {
		t.Errorf("content-type: %q", headers["content-type"])
	}
	if headers["x-ozel"] != "value" {
		t.Errorf("x-ozel: %q", headers["x-ozel"])
	}
}

// The Host header is written from the target: the visitor's origin does
// not leak to the local server.
func TestServeRewritesHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/",
		Headers: map[string]string{"host": "kullanici.github.io"},
	}, nil, &fakeSink{})

	if strings.Contains(gotHost, "github.io") {
		t.Errorf("the visitor's host leaked to the target: %q", gotHost)
	}
	if !strings.Contains(gotHost, "127.0.0.1") && !strings.Contains(gotHost, "localhost") {
		t.Errorf("the host should have been written from the target, got: %q", gotHost)
	}
}

func TestServePOSTWithBody(t *testing.T) {
	want := strings.Repeat("govde", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("the body could not be read: %v", err)
		}
		if string(got) != want {
			t.Errorf("the body differs: %d bytes, expected %d", len(got), len(want))
		}
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	sink := &fakeSink{}
	p.Serve(context.Background(), protocol.Req{
		ID: 2, Method: "POST", Path: "/api",
		Headers: map[string]string{"content-type": "text/plain"}, HasBody: true,
	}, strings.NewReader(want), sink)

	status, _, _, ended, abort := sink.snapshot()
	if abort != "" {
		t.Fatalf("abort: %s", abort)
	}
	if status != http.StatusCreated {
		t.Errorf("status %d", status)
	}
	if !ended {
		t.Error("no end arrived")
	}
}

// The most critical test: a visitor must not be able to make a request
// to another host through the path.
func TestServeRejectsSSRFPaths(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)

	for _, path := range []string{
		"http://evil.example/",
		"//evil.example/x",
		"/../../etc/passwd",
		"/%2e%2e/%2e%2e/etc/passwd",
		"api/items",
		"",
	} {
		reached = false
		sink := &fakeSink{}
		p.Serve(context.Background(), protocol.Req{
			ID: 1, Method: "GET", Path: path, Headers: map[string]string{},
		}, nil, sink)

		status, _, _, _, _ := sink.snapshot()
		if status != http.StatusBadRequest {
			t.Errorf("path %q: status %d, 400 was expected", path, status)
		}
		if reached {
			t.Errorf("path %q: a request reached the target and should not have", path)
		}
	}
}

// Redirects are not followed; Location is rewritten to a relative
// path.
func TestServeRewritesLocation(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/git" {
			w.Header().Set("location", srv.URL+"/varis?x=1")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "varis")
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	sink := &fakeSink{}
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/git", Headers: map[string]string{},
	}, nil, sink)

	status, headers, _, _, _ := sink.snapshot()
	if status != http.StatusFound {
		t.Errorf("the redirect should not have been followed, status %d", status)
	}
	if headers["location"] != "/varis?x=1" {
		t.Errorf("location has to be rewritten, got: %q", headers["location"])
	}
}

// With the target down it is 502, and no local error text leaks.
func TestServeUpstreamDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // kapat

	targets, err := target.Parse([]string{url})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{
		Targets: targets, TargetName: target.DefaultName,
		MaxBody: 1 << 20, Timeout: 2 * time.Second, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sink := &fakeSink{}
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/", Headers: map[string]string{},
	}, nil, sink)

	status, _, body, ended, _ := sink.snapshot()
	if status != http.StatusBadGateway {
		t.Errorf("status %d, 502 was expected", status)
	}
	if !ended {
		t.Error("no end arrived")
	}
	if strings.Contains(string(body), url) {
		t.Errorf("the target URL leaked to the visitor: %q", body)
	}
}

// The response body streams: the full body is never awaited.
func TestServeStreamsResponse(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("flush desteklenmiyor")
			return
		}
		_, _ = io.WriteString(w, "first-chunk")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "second-chunk")
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	sink := &fakeSink{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Serve(context.Background(), protocol.Req{
			ID: 1, Method: "GET", Path: "/", Headers: map[string]string{},
		}, nil, sink)
	}()

	// The first chunk has to arrive before the target finishes its
	// response.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, body, _, _ := sink.snapshot(); strings.Contains(string(body), "first-chunk") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, body, ended, _ := sink.snapshot(); !strings.Contains(string(body), "first-chunk") {
		t.Fatalf("the first chunk did not flow (body: %q, ended=%v)", body, ended)
	} else if ended {
		t.Error("end should not have arrived before the target's response finished")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not finish")
	}

	_, _, body, ended, _ := sink.snapshot()
	if string(body) != "first-chunksecond-chunk" {
		t.Errorf("body: %q", body)
	}
	if !ended {
		t.Error("no end arrived")
	}
}

// The body limit: the agent forwards at most MaxBody bytes.
func TestServeLimitsRequestBody(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = len(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	targets, err := target.Parse([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{
		Targets: targets, TargetName: target.DefaultName,
		MaxBody: 100, Timeout: 10 * time.Second, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}, strings.NewReader(strings.Repeat("a", 1000)), &fakeSink{})

	if got > 100 {
		t.Errorf("the limit was passed: %d bytes reached the target, the limit is 100", got)
	}
}

func TestServeContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	sink := &fakeSink{}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	p.Serve(ctx, protocol.Req{ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}}, nil, sink)

	_, _, _, _, abort := sink.snapshot()
	if abort == "" {
		t.Error("an abort was expected on cancellation")
	}
}

// A compile-time check: Proxy really does implement tunnel.Proxy.
var _ tunnel.Proxy = (*Proxy)(nil)
