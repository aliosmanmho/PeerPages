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

// This package runs against a REAL WebSocket server (httptest plus
// coder/websocket): a fake client would not prove the handshake and the
// close codes pass through correctly.

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// echoServer echoes the messages it receives; on the /missing path it
// refuses the handshake.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "there is no ws here", http.StatusNotFound)
	})

	mux.HandleFunc("/origin", func(w http.ResponseWriter, r *http.Request) {
		// Local servers check Origin; we look at what the agent sent.
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
			t.Logf("accept error: %v", err)
			return
		}
		defer func() { _ = c.CloseNow() }()

		if r.URL.Query().Get("close") != "" {
			_ = c.Close(4002, "the server closed it")
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
		t.Fatalf("the connection could not be made: %v", err)
	}
	defer func() { _ = conn.Close(protocol.WSCloseNormal, "") }()

	// The SERVER chooses (the first match in its own list), not the
	// client's ordering: the agent only carries the request and the
	// result.
	if proto != "vite-hmr" {
		t.Errorf("the subprotocol is %q, expected %q", proto, "vite-hmr")
	}

	if err := conn.Write(ctx, false, []byte("merhaba")); err != nil {
		t.Fatal(err)
	}
	binary, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if binary || string(data) != "merhaba" {
		t.Errorf("the echo: binary=%v %q", binary, data)
	}

	if err := conn.Write(ctx, true, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	binary, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !binary || len(data) != 3 {
		t.Errorf("the binary echo: binary=%v %v", binary, data)
	}
}

// Origin has to be the target's own origin: local servers would refuse
// the visitor's github.io origin (BRIEF section 8).
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
		t.Errorf("Origin %q, expected %q", data, srv.URL)
	}
}

// If the handshake is refused the status code has to reach the visitor
// as a meaningful reason; it is a policy close, not a 1011 "internal
// error".
func TestDialRejectedHandshake(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/missing"})
	if err == nil {
		t.Fatal("an error was expected")
	}
	var de *tunnel.WSDialError
	if !errors.As(err, &de) {
		t.Fatalf("a WSDialError was expected: %T", err)
	}
	if de.Code != protocol.WSClosePolicy {
		t.Errorf("code %d, expected %d", de.Code, protocol.WSClosePolicy)
	}
	if !strings.Contains(de.Reason, "404") {
		t.Errorf("the reason has to carry the status code: %q", de.Reason)
	}
}

// The server's close code has to pass through EXACTLY (4000-4999 belong
// to the application).
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
		t.Fatalf("a WSCloseError was expected: %v (%T)", err, err)
	}
	if ce.Code != 4002 || ce.Reason != "the server closed it" {
		t.Errorf("close: %d %q", ce.Code, ce.Reason)
	}
}

// A clean close has to return ErrWSClosedNormally: the tunnel turns that
// into code 1000.
func TestReadNormalClosure(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: "/ws"})
	if err != nil {
		t.Fatal(err)
	}
	// We close; the server confirms the close as well.
	if err := conn.Close(protocol.WSCloseNormal, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("a read succeeded after the close")
	}
}

// The path cannot escape the allowlist (BRIEF principle 1): the host is
// not derived from it.
func TestDialRejectsBadPath(t *testing.T) {
	srv := echoServer(t)
	d := newDialer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, bad := range []string{"//evil.example/ws", "/../../etc/passwd", "ws"} {
		_, _, err := d.Dial(ctx, protocol.WSOpen{ID: 1, Path: bad})
		if err == nil {
			t.Errorf("%q was accepted", bad)
			continue
		}
		var de *tunnel.WSDialError
		if !errors.As(err, &de) || de.Code != protocol.WSClosePolicy {
			t.Errorf("a policy error was expected for %q: %v", bad, err)
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
		{"no target", wsbridge.Config{MaxMessage: 1, Logger: quietLogger()}},
		{"the target name is empty", wsbridge.Config{Targets: set, MaxMessage: 1, Logger: quietLogger()}},
		{"an unknown target", wsbridge.Config{
			Targets: set, TargetName: "missing", MaxMessage: 1, Logger: quietLogger(),
		}},
		{"a zero message limit", wsbridge.Config{
			Targets: set, TargetName: "default", Logger: quietLogger(),
		}},
		{"logger nil", wsbridge.Config{Targets: set, TargetName: "default", MaxMessage: 1}},
	} {
		if _, err := wsbridge.New(tc.cfg); err == nil {
			t.Errorf("%s: an error was expected", tc.name)
		}
	}
}
