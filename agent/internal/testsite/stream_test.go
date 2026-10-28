package testsite_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
)

// The test site's streaming endpoints (phase 6). They are what the e2e
// tests rest on: if an endpoint itself is broken, the bridge tests look
// in the wrong place.

func newSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(testsite.Handler(t.TempDir()))
	t.Cleanup(srv.Close)
	return srv
}

// The default behaviour has to be the SAME as in phase 1: three events,
// no delay. The existing e2e rests on that.
func TestSSEDefaultUnchanged(t *testing.T) {
	srv := newSite(t)

	res, err := http.Get(srv.URL + "/sse") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if ct := res.Header.Get("content-type"); ct != "text/event-stream" {
		t.Errorf("content-type: %q", ct)
	}
	// io.ReadAll is required: a single Read gives only the first flush.
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"data: event-0", "data: event-1", "data: event-2"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q is missing, body: %q", want, body)
		}
	}
}

// A delayed stream: the first event has to arrive BEFORE the stream
// closes. That is what proves incremental delivery (without ?delay= three
// events can fit into a single frame).
func TestSSEDelayedIsIncremental(t *testing.T) {
	srv := newSite(t)

	res, err := http.Get(srv.URL + "/sse?n=3&delay=300") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	started := time.Now()
	r := bufio.NewReader(res.Body)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Since(started)

	if !strings.Contains(line, "data: event-0") {
		t.Errorf("the first line: %q", line)
	}
	// The first event has to arrive with no wait; the whole stream takes
	// at least 2 delays.
	if firstAt > 250*time.Millisecond {
		t.Errorf("the first event arrived after %s: the stream may be being buffered", firstAt)
	}

	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	if total := time.Since(started); total < 250*time.Millisecond {
		t.Errorf("the stream took %s: no delay is being applied", total)
	}
}

func TestSSEBadParams(t *testing.T) {
	srv := newSite(t)
	for _, q := range []string{"?n=elma", "?delay=-1", "?delay=99999"} {
		res, err := http.Get(srv.URL + "/sse" + q) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, expected 400", q, res.StatusCode)
		}
	}
}

func TestWSEchoEndpoint(t *testing.T) {
	srv := newSite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?proto=json"
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	if c.Subprotocol() != "json" {
		t.Errorf("subprotocol: %q", c.Subprotocol())
	}

	if err := c.Write(ctx, websocket.MessageText, []byte("selam")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(data) != "selam" {
		t.Errorf("the echo: %v %q", typ, data)
	}

	if err := c.Write(ctx, websocket.MessageBinary, []byte{9, 8, 7}); err != nil {
		t.Fatal(err)
	}
	typ, data, err = c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || len(data) != 3 {
		t.Errorf("the binary echo: %v %v", typ, data)
	}
}

// A server close with ?close=: that is how the bridge tests check the
// close code passes through.
func TestWSServerInitiatedClose(t *testing.T) {
	srv := newSite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?close=4002&reason=bitti"
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	_, _, err = c.Read(ctx)
	if err == nil {
		t.Fatal("a close was expected")
	}
	if status := websocket.CloseStatus(err); status != 4002 {
		t.Errorf("close code %d, expected 4002 (%v)", status, err)
	}
}
