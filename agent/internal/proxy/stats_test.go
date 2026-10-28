package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/stats"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// newProxyWithStats builds a proxy with counters attached.
func newProxyWithStats(t *testing.T, serverURL string) (*Proxy, *stats.Registry) {
	t.Helper()
	targets, err := target.Parse([]string{serverURL})
	if err != nil {
		t.Fatal(err)
	}
	reg := stats.New([]string{target.DefaultName})
	p, err := New(Config{
		Targets:    targets,
		TargetName: target.DefaultName,
		Stats:      reg.Target(target.DefaultName),
		MaxBody:    1 << 20,
		Timeout:    10 * time.Second,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, reg
}

func TestStatsCountsSuccessAndBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		_, _ = w.Write(make([]byte, 1000))
		if len(got) != 7 {
			t.Errorf("the body that reached the target is %d bytes", len(got))
		}
	}))
	defer srv.Close()

	p, reg := newProxyWithStats(t, srv.URL)
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}, strings.NewReader("yedibyt"), &fakeSink{})

	got := reg.Total()
	if got.Requests != 1 || got.Success != 1 || got.Errors != 0 {
		t.Errorf("counters: %+v", got)
	}
	if got.BytesOut != 1000 {
		t.Errorf("%d bytes out, expected 1000", got.BytesOut)
	}
	if got.BytesIn != 7 {
		t.Errorf("%d bytes in, expected 7", got.BytesIn)
	}
}

// The target answering 404 or 500 IS A SUCCESS for the proxy: the
// request was carried. The error counter is only for the cases where the
// agent could not carry the response.
func TestStatsUpstreamErrorStatusIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p, reg := newProxyWithStats(t, srv.URL)
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false,
	}, nil, &fakeSink{})

	if got := reg.Total(); got.Success != 1 || got.Errors != 0 {
		t.Errorf("a 500 from the target must not count as an error: %+v", got)
	}
}

func TestStatsCountsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	targetURL := srv.URL
	srv.Close() // the target is closed: unreachable

	p, reg := newProxyWithStats(t, targetURL)

	// An unreachable target.
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false,
	}, nil, &fakeSink{})

	// An invalid path (the SSRF shield): the target never resolves.
	p.Serve(context.Background(), protocol.Req{
		ID: 2, Method: "GET", Path: "http://baska.example/", Headers: map[string]string{}, HasBody: false,
	}, nil, &fakeSink{})

	got := reg.Total()
	if got.Requests != 2 {
		t.Errorf("%d requests, expected 2", got.Requests)
	}
	if got.Errors != 2 || got.Success != 0 {
		t.Errorf("counters: %+v, two errors were expected", got)
	}
}

// With no counters given (nil) the proxy has to work normally: counting
// is not mandatory.
func TestStatsNilIsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "merhaba")
	}))
	defer srv.Close()

	p := newProxyFor(t, srv.URL) // Stats alani bos
	sink := &fakeSink{}
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false,
	}, nil, sink)

	if _, _, body, ended, _ := sink.snapshot(); string(body) != "merhaba" || !ended {
		t.Errorf("a proxy with no counters has to work: body %q ended %v", body, ended)
	}
}
