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

// Test sitesinin akis uclari (Faz 6). Bunlar e2e'nin dayanagi: ucun kendisi
// bozuksa kopru testleri yanlis yerde arar.

func newSite(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(testsite.Handler(t.TempDir()))
	t.Cleanup(srv.Close)
	return srv
}

// Varsayilan davranis Faz 1'den beri AYNI olmali: uc olay, beklemesiz.
// Mevcut e2e bunun uzerine kurulu.
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
	// io.ReadAll sart: tek Read yalnizca ilk flush'i verir.
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"data: olay-0", "data: olay-1", "data: olay-2"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q yok, govde: %q", want, body)
		}
	}
}

// Gecikmeli akis: ilk olay, akis KAPANMADAN once gelmeli. Artimli teslimi
// kanitlayan sey bu (?delay= olmadan uc olay tek cerceveye sigabilir).
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

	if !strings.Contains(line, "data: olay-0") {
		t.Errorf("ilk satir: %q", line)
	}
	// Ilk olay beklemeden gelmeli; tum akis en az 2 gecikme surer.
	if firstAt > 250*time.Millisecond {
		t.Errorf("ilk olay %s sonra geldi: akis tamponlaniyor olabilir", firstAt)
	}

	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	if total := time.Since(started); total < 250*time.Millisecond {
		t.Errorf("akis %s surdu: gecikme uygulanmiyor", total)
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
			t.Errorf("%s: durum %d, beklenen 400", q, res.StatusCode)
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
		t.Errorf("alt protokol: %q", c.Subprotocol())
	}

	if err := c.Write(ctx, websocket.MessageText, []byte("selam")); err != nil {
		t.Fatal(err)
	}
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(data) != "selam" {
		t.Errorf("yanki: %v %q", typ, data)
	}

	if err := c.Write(ctx, websocket.MessageBinary, []byte{9, 8, 7}); err != nil {
		t.Fatal(err)
	}
	typ, data, err = c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || len(data) != 3 {
		t.Errorf("ikili yanki: %v %v", typ, data)
	}
}

// ?close= ile sunucu kapanisi: kopru testleri kapanis kodunun gectigini
// boyle dogruluyor.
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
		t.Fatal("kapanis beklenirdi")
	}
	if status := websocket.CloseStatus(err); status != 4002 {
		t.Errorf("kapanis kodu %d, beklenen 4002 (%v)", status, err)
	}
}
