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

// Uzun yasayan akislarin zaman asimi (Faz 6).
//
// Faz 6'ya kadar `Timeout` TUM cevabi sariyordu: 60 saniyeden uzun yasayan
// her SSE akisi sessizce kesiliyordu. Artik yalnizca BASLIK fazinin siniri.

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

// Baslik geldikten sonra govde, zaman asimindan UZUN sure susabilir.
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

		// Zaman asimindan belirgin sekilde uzun sus: eski davranis burada
		// akisi keserdi.
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
		t.Fatal("Serve bitmedi")
	}

	_, _, body, ended, abort := sink.snapshot()
	if abort != "" {
		t.Fatalf("akis iptal edildi: %q (govde: %q)", abort, body)
	}
	if !ended {
		t.Error("akis end ile bitmeliydi")
	}
	if !strings.Contains(string(body), "data: son") {
		t.Errorf("gecikmeli olay gelmedi, govde: %q", body)
	}
}

// Baslik fazinin siniri DURUYOR: cevap vermeyen bir hedef istegi sonsuza
// kadar askida tutamaz.
func TestServeHeaderPhaseStillTimesOut(t *testing.T) {
	const timeout = 300 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hic baslik yazmadan bekle.
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
		t.Fatal("baslik fazi zaman asimina ugramadi: istek askida kaldi")
	}

	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("zaman asimi %s surdu, sinir %s", elapsed, timeout)
	}
	status, _, _, _, abort := sink.snapshot()
	if abort == "" && status != http.StatusBadGateway {
		t.Errorf("ziyaretciye bir sonuc gitmeliydi (status=%d abort=%q)", status, abort)
	}
}

// Ziyaretci vazgecerse akis hemen bitmeli (context iptali).
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

	// Ilk olaylar aksin, sonra vazgec.
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
		t.Fatal("iptalden sonra akis durmadi")
	}
}

// --inject-ws-shim verilmediyse HTML cevabina DOKUNULMAZ: shim isareti de
// eklenmez.
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
		{"kapali", false, ""},
		{"acik", true, "1"},
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
				t.Errorf("%s basligi %q, beklenen %q", protocol.HeaderWSShim, got, tc.want)
			}
			// Govde HER DURUMDA bayt bayt ayni: enjeksiyonu SW yapiyor.
			if string(body) != "<html><head></head><body>selam</body></html>" {
				t.Errorf("govde degismis: %q", body)
			}
		})
	}
}

// Shim isareti yalnizca HTML'e: JSON ya da gorsel cevaplari isaretlenmez.
func TestShimHeaderOnlyForHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", r.URL.Query().Get("ct"))
		_, _ = io.WriteString(w, "govde")
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
			// QueryEscape sart: `;` ve `+` ham halde baska anlam tasiyor
			// ("application/xhtml+xml" bosluga donusurdu).
			ID: 1, Method: "GET", Path: "/?ct=" + url.QueryEscape(tc.ct),
			Headers: map[string]string{},
		}, nil, sink)

		_, headers, _, _, _ := sink.snapshot()
		if got := headers[protocol.HeaderWSShim] == "1"; got != tc.want {
			t.Errorf("content-type %q: isaret %v, beklenen %v", tc.ct, got, tc.want)
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
			t.Errorf("isHTML(%q) = %v, beklenen %v", tc.in, got, tc.want)
		}
	}
}
