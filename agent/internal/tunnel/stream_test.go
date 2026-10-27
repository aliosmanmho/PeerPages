package tunnel

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Uzun yasayan HTTP akislari (SSE) ayri butceden sayilir: bir SSE cevabi
// MaxConcurrent slotunu omru boyunca tutsa, 64 SSE istegi tuneli normal
// istekler icin kullanilamaz hale getirirdi.

// sseProxy /sse icin bitmeyen bir event-stream cevabi, digerleri icin kisa
// bir metin doner.
func sseProxy() proxyFunc {
	return func(ctx context.Context, req protocol.Req, _ io.Reader, sink ResponseSink) {
		if req.Path != "/sse" {
			if err := sink.SendRes(200, map[string]string{"content-type": "text/plain"}); err != nil {
				return
			}
			_ = sink.Write([]byte("ok"))
			_ = sink.End()
			return
		}

		if err := sink.SendRes(200, map[string]string{
			"content-type":  "text/event-stream",
			"cache-control": "no-store",
		}); err != nil {
			// Butce dolu: sink zaten abort yolladi.
			return
		}
		_ = sink.Write([]byte("data: ilk\n\n"))
		// Akis ziyaretci ya da oturum kapanana kadar surer.
		<-ctx.Done()
		_ = sink.Abort("upstream-canceled")
	}
}

// waitRes bir id icin res ya da abort bekler.
func (v *visitor) waitRes(t *testing.T, id uint32, d time.Duration) (*protocol.Res, string) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.Res:
				if msg.ID == id {
					cp := msg
					return &cp, ""
				}
			case protocol.Abort:
				if msg.ID == id {
					return nil, msg.Reason
				}
			}
		case <-v.dataCh:
			// Govde cerceveleri bu testte onemsiz.
		case <-deadline:
			t.Fatalf("id %d icin cevap %s icinde gelmedi", id, d)
			return nil, ""
		}
	}
}

// Acik bir SSE akisi normal istek butcesini KILITLEMEMELI.
func TestSSEReleasesRequestBudget(t *testing.T) {
	sess, v := connect(t, Config{
		Proxy:         sseProxy(),
		MaxConcurrent: 1,
		MaxStreams:    2,
	})
	defer func() { _ = sess.Close() }()

	if err := v.sendCtrl(protocol.Req{
		ID: 1, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason := v.waitRes(t, 1, 5*time.Second)
	if res == nil {
		t.Fatalf("SSE cevabi gelmedi: %s", reason)
	}

	// MaxConcurrent 1 ve SSE akisi hala acik; yine de normal istek gecmeli.
	if err := v.sendCtrl(protocol.Req{
		ID: 2, Method: "GET", Path: "/x", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason = v.waitRes(t, 2, 5*time.Second)
	if res == nil {
		t.Fatalf("normal istek reddedildi: %s (SSE slotu birakmamis olabilir)", reason)
	}
}

// Akis butcesi dolduysa SSE cevabi SEBEBIYLE reddedilmeli: sessizce askida
// kalmak ya da 502 donmek yanlis teshise yol acar.
func TestSSEBudgetFullAborts(t *testing.T) {
	sess, v := connect(t, Config{
		Proxy:         sseProxy(),
		MaxConcurrent: 4,
		MaxStreams:    1,
	})
	defer func() { _ = sess.Close() }()

	if err := v.sendCtrl(protocol.Req{
		ID: 1, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	if res, reason := v.waitRes(t, 1, 5*time.Second); res == nil {
		t.Fatalf("ilk SSE cevabi gelmedi: %s", reason)
	}

	if err := v.sendCtrl(protocol.Req{
		ID: 2, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason := v.waitRes(t, 2, 5*time.Second)
	if res != nil {
		t.Fatalf("butce dolu olmasina ragmen ikinci SSE kabul edildi (status %d)", res.Status)
	}
	if reason != "too-many-streams" {
		t.Errorf("abort sebebi %q, beklenen %q", reason, "too-many-streams")
	}
}

// content-type ayristirmasi: parametreler ve bosluklar yok sayilmali.
func TestIsEventStream(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"  TEXT/Event-Stream ;charset=utf-8", true},
		{"text/plain", false},
		{"", false},
		{"text/event-streaming", false},
		{"application/json; x=text/event-stream", false},
	} {
		if got := isEventStream(tc.in); got != tc.want {
			t.Errorf("isEventStream(%q) = %v, beklenen %v", tc.in, got, tc.want)
		}
	}
}
