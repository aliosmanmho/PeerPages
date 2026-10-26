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

// newProxyWithStats sayac takili bir proxy kurar.
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
		gelen, _ := io.ReadAll(r.Body)
		_, _ = w.Write(make([]byte, 1000))
		if len(gelen) != 7 {
			t.Errorf("hedefe giden govde %d bayt", len(gelen))
		}
	}))
	defer srv.Close()

	p, reg := newProxyWithStats(t, srv.URL)
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}, strings.NewReader("yedibyt"), &fakeSink{})

	got := reg.Total()
	if got.Requests != 1 || got.Success != 1 || got.Errors != 0 {
		t.Errorf("sayaclar: %+v", got)
	}
	if got.BytesOut != 1000 {
		t.Errorf("giden bayt %d, beklenen 1000", got.BytesOut)
	}
	if got.BytesIn != 7 {
		t.Errorf("gelen bayt %d, beklenen 7", got.BytesIn)
	}
}

// Hedefin 404 ya da 500 dondurmesi proxy icin BASARIDIR: istek tasindi.
// Hata sayaci yalnizca ajanin cevabi tasiyamadigi durumlar icin.
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
		t.Errorf("hedefin 500'u hata sayilmamali: %+v", got)
	}
}

func TestStatsCountsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	targetURL := srv.URL
	srv.Close() // hedef kapali: ulasilamaz

	p, reg := newProxyWithStats(t, targetURL)

	// Ulasilamayan hedef.
	p.Serve(context.Background(), protocol.Req{
		ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false,
	}, nil, &fakeSink{})

	// Gecersiz yol (SSRF kalkani): hedef hic cozulemez.
	p.Serve(context.Background(), protocol.Req{
		ID: 2, Method: "GET", Path: "http://baska.example/", Headers: map[string]string{}, HasBody: false,
	}, nil, &fakeSink{})

	got := reg.Total()
	if got.Requests != 2 {
		t.Errorf("istek %d, beklenen 2", got.Requests)
	}
	if got.Errors != 2 || got.Success != 0 {
		t.Errorf("sayaclar: %+v, iki hata beklenirdi", got)
	}
}

// Sayac verilmezse (nil) proxy normal calismali: sayac tutmak zorunlu degil.
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
		t.Errorf("sayacsiz proxy calismali: govde %q ended %v", body, ended)
	}
}
