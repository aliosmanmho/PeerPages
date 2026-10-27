package wsbridge_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
	"github.com/aliosmanmho/PeerPages/agent/internal/wsbridge"
)

// Bu paket GERCEK bir WebSocket sunucusuna karsi kosar (httptest +
// coder/websocket): sahte bir istemci, el sikismanin ve kapanis kodlarinin
// dogru gectigini kanitlamaz.

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// echoServer gelen mesajlari yankilar; /yok yolunda el sikismayi reddeder.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/yok", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "burada ws yok", http.StatusNotFound)
	})

	mux.HandleFunc("/origin", func(w http.ResponseWriter, r *http.Request) {
		// Yerel sunucular Origin denetimi yapar; ajanin ne yolladigini
		// goruyoruz.
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		_ = c.Write(r.Context(), websocket.MessageText, []byte(r.Header.Get("Origin")))
		<-r.Context().Done()
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols:   []string{"vite-hmr", "json"},
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			t.Logf("accept hatasi: %v", err)
			return
		}
		defer func() { _ = c.CloseNow() }()

		if r.URL.Query().Get("close") != "" {
			_ = c.Close(4002, "sunucu kapatti")
			return
		}

		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newDialer(t *testing.T, targetURL string) *wsbridge.Dialer {
	t.Helper()
	set, err := target.Parse([]string{"default=" + targetURL})
	if err != nil {
		t.Fatal(err)
	}
	d, err := wsbridge.New(wsbridge.Config{
		Targets:    set,
		TargetName: "default",
		MaxMessage: 1 << 20,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDialEchoAndSubprotocol(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, proto, err := d.Dial(ctx, protocol.WSOpen{
		ID: 1, Path: "/ws?x=1", Protos: []string{"json", "vite-hmr"},
	})
	if err != nil {
		t.Fatalf("baglanti kurulamadi: %v", err)
	}
	defer func() { _ = conn.Close(protocol.WSCloseNormal, "") }()

	// Secimi SUNUCU yapar (kendi listesindeki ilk eslesme), istemcinin
	// siralamasi degil: ajan yalnizca istegi ve sonucu tasiyor.
	if proto != "vite-hmr" {
		t.Errorf("alt protokol %q, beklenen %q", proto, "vite-hmr")
	}

	if err := conn.Write(ctx, false, []byte("merhaba")); err != nil {
		t.Fatal(err)
	}
	binary, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if binary || string(data) != "merhaba" {
		t.Errorf("yanki: binary=%v %q", binary, data)
	}

	if err := conn.Write(ctx, true, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	binary, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !binary || len(data) != 3 {
		t.Errorf("ikili yanki: binary=%v %v", binary, data)
	}
}

// Origin hedefin kendi origin'i olmali: yerel sunucular ziyaretcinin
// github.io origin'ini reddederdi (README §8).
func TestDialSendsTargetOrigin(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/origin"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(protocol.WSCloseNormal, "") }()

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != srv.URL {
		t.Errorf("Origin %q, beklenen %q", data, srv.URL)
	}
}

// El sikisma reddedilirse durum kodu ziyaretciye anlamli bir sebep olarak
// gecmeli; 1011 "ic hata" degil politika.
func TestDialRejectedHandshake(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/yok"})
	if err == nil {
		t.Fatal("hata beklenirdi")
	}
	var de *tunnel.WSDialError
	if !errors.As(err, &de) {
		t.Fatalf("WSDialError beklenirdi: %T", err)
	}
	if de.Code != protocol.WSClosePolicy {
		t.Errorf("kod %d, beklenen %d", de.Code, protocol.WSClosePolicy)
	}
	if !strings.Contains(de.Reason, "404") {
		t.Errorf("sebep durum kodunu tasimali: %q", de.Reason)
	}
}

// Sunucunun kapanis kodu AYNEN gecmeli (4000-4999 uygulamaya ait).
func TestReadCarriesCloseCode(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/ws?close=1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(protocol.WSCloseNormal, "") }()

	_, _, err = conn.Read(ctx)
	var ce *tunnel.WSCloseError
	if !errors.As(err, &ce) {
		t.Fatalf("WSCloseError beklenirdi: %v (%T)", err, err)
	}
	if ce.Code != 4002 || ce.Reason != "sunucu kapatti" {
		t.Errorf("kapanis: %d %q", ce.Code, ce.Reason)
	}
}

// Temiz kapanis ErrWSClosedNormally donmeli: tunel bunu kod 1000'e cevirir.
func TestReadNormalClosure(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	// Biz kapatiyoruz; sunucu da kapanisi onaylar.
	if err := conn.Close(protocol.WSCloseNormal, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("kapandiktan sonra okuma basarili oldu")
	}
}

// Yol allowlist'in disina cikamaz (README ilke 1): host path'ten turetilmez.
func TestDialRejectsBadPath(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, bad := range []string{"//evil.example/ws", "/../../etc/passwd", "ws"} {
		_, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: bad})
		if err == nil {
			t.Errorf("%q kabul edildi", bad)
			continue
		}
		var de *tunnel.WSDialError
		if !errors.As(err, &de) || de.Code != protocol.WSClosePolicy {
			t.Errorf("%q icin politika hatasi beklenirdi: %v", bad, err)
		}
	}
}

func TestNewValidation(t *testing.T) {
	set, err := target.Parse([]string{"default=http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cfg  wsbridge.Config
	}{
		{"hedef yok", wsbridge.Config{MaxMessage: 1, Logger: quietLogger()}},
		{"hedef adi bos", wsbridge.Config{Targets: set, MaxMessage: 1, Logger: quietLogger()}},
		{"bilinmeyen hedef", wsbridge.Config{
			Targets: set, TargetName: "yok", MaxMessage: 1, Logger: quietLogger(),
		}},
		{"mesaj siniri sifir", wsbridge.Config{
			Targets: set, TargetName: "default", Logger: quietLogger(),
		}},
		{"logger nil", wsbridge.Config{Targets: set, TargetName: "default", MaxMessage: 1}},
	} {
		if _, err := wsbridge.New(tc.cfg); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}
