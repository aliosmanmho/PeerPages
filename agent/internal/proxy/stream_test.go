package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// The timeout of long-lived streams (phase 6).
//
// Until phase 6 `Timeout` wrapped the WHOLE response: every SSE stream
// living longer than 60 seconds was cut off silently. It now bounds the
// HEADER phase only.

func newProxyWithTimeout(t *testing.T, serverURL string, timeout time.Duration) *Proxy {
	t.Helper()
	targets, err := target.Parse([]string{serverURL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{
		Targets:    targets,
		TargetName: target.DefaultName,
		MaxBody:    1 << 20,
		Timeout:    timeout,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Once the head has arrived the body may stay quiet for LONGER than the
// timeout.
func TestServeBodyHasNoDeadline(t *testing.T) {
	const timeout = 300 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("flush desteklenmiyor")
			return
		}
		_, _ = io.WriteString(w, "data: ilk\n\n")
		flusher.Flush()

		// Stay quiet clearly longer than the timeout: the old behaviour
		// would cut the stream off here.
		select {
		case <-time.After(4 * timeout):
		case <-r.Context().Done():
			return
		}

		_, _ = io.WriteString(w, "data: son\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := newProxyWithTimeout(t, srv.URL, timeout)
	sink := &fakeSink{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Serve(context.Background(), protocol.Req{
			ID: 1, Method: "GET", Path: "/sse", Headers: map[string]string{},
		}, nil, sink)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not finish")
	}

	_, _, body, ended, abort := sink.snapshot()
	if abort != "" {
		t.Fatalf("the stream was cancelled: %q (body: %q)", abort, body)
	}
	if !ended {
		t.Error("the stream should have ended with end")
	}
	if !strings.Contains(string(body), "data: son") {
		t.Errorf("the delayed event did not arrive, body: %q", body)
	}
}

// The bound on the header phase IS STILL THERE: a target that never
// answers cannot hold a request forever.
func TestServeHeaderPhaseStillTimesOut(t *testing.T) {
	const timeout = 300 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait without writing any head at all.
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newProxyWithTimeout(t, srv.URL, timeout)
	sink := &fakeSink{}

	started := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Serve(context.Background(), protocol.Req{
			ID: 2, Method: "GET", Path: "/yavas", Headers: map[string]string{},
		}, nil, sink)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the header phase did not time out: the request hung")
	}

	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("the timeout took %s, the bound is %s", elapsed, timeout)
	}
	status, _, _, _, abort := sink.snapshot()
	if abort == "" && status != http.StatusBadGateway {
		t.Errorf("some outcome should have reached the visitor (status=%d abort=%q)", status, abort)
	}
}

// If the visitor gives up the stream has to end right away (context
// cancellation).
func TestServeStreamStopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 1000; i++ {
			if _, err := io.WriteString(w, "data: x\n\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	p := newProxyWithTimeout(t, srv.URL, 5*time.Second)
	sink := &fakeSink{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Serve(ctx, protocol.Req{
			ID: 3, Method: "GET", Path: "/sse", Headers: map[string]string{},
		}, nil, sink)
	}()

	// Let the first events flow, then give up.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, body, _, _ := sink.snapshot(); len(body) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not stop after the cancellation")
	}
}

// Without --inject-ws-shim the HTML response is NOT TOUCHED: no shim
// hint is added either.
func TestShimHeaderOnlyWhenEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><head></head><body>selam</body></html>")
	}))
	defer srv.Close()

	targets, err := target.Parse([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		inject bool
		want   string
	}{
		{"off", false, ""},
		{"on", true, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(Config{
				Targets:      targets,
				TargetName:   target.DefaultName,
				MaxBody:      1 << 20,
				Logger:       quietLogger(),
				InjectWSShim: tc.inject,
			})
			if err != nil {
				t.Fatal(err)
			}
			sink := &fakeSink{}
			p.Serve(context.Background(), protocol.Req{
				ID: 1, Method: "GET", Path: "/", Headers: map[string]string{},
			}, nil, sink)

			_, headers, body, _, _ := sink.snapshot()
			if got := headers[protocol.HeaderWSShim]; got != tc.want {
				t.Errorf("the %s header is %q, expected %q", protocol.HeaderWSShim, got, tc.want)
			}
			// The body is byte-for-byte the same IN EVERY CASE: the SW
			// does the injection.
			if string(body) != "<html><head></head><body>selam</body></html>" {
				t.Errorf("the body changed: %q", body)
			}
		})
	}
}

// The shim hint goes on HTML only: JSON and image responses are not
// marked.
func TestShimHeaderOnlyForHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", r.URL.Query().Get("ct"))
		_, _ = io.WriteString(w, "body")
	}))
	defer srv.Close()

	targets, err := target.Parse([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{
		Targets:      targets,
		TargetName:   target.DefaultName,
		MaxBody:      1 << 20,
		Logger:       quietLogger(),
		InjectWSShim: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/HTML", true},
		{"application/xhtml+xml", true},
		{"application/json", false},
		{"image/png", false},
		{"text/plain", false},
	} {
		sink := &fakeSink{}
		p.Serve(context.Background(), protocol.Req{
			// QueryEscape is required: raw `;` and `+` mean something
			// else ("application/xhtml+xml" would turn into a space).
			ID: 1, Method: "GET", Path: "/?ct=" + url.QueryEscape(tc.ct),
			Headers: map[string]string{},
		}, nil, sink)

		_, headers, _, _, _ := sink.snapshot()
		if got := headers[protocol.HeaderWSShim] == "1"; got != tc.want {
			t.Errorf("content-type %q: hint %v, expected %v", tc.ct, got, tc.want)
		}
	}
}

func TestIsHTML(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"text/html", true},
		{" text/html ; charset=utf-8", true},
		{"application/xhtml+xml", true},
		{"", false},
		{"text/htmlx", false},
		{"application/json", false},
	} {
		if got := isHTML(tc.in); got != tc.want {
			t.Errorf("isHTML(%q) = %v, expected %v", tc.in, got, tc.want)
		}
	}
}
