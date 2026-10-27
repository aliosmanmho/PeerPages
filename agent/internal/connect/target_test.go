package connect_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Faz 3: hedef ziyaretcinin URL'sinden gelir ve offer'da tasinir.
//
// Buradaki testler tek ajanin IKI projeyi yayinlamasini (BRIEF Faz 3 kabul
// kriteri) ve bilinmeyen hedefin sessizce default'a dusmemesini dogrular.

// targetEchoProxy hangi hedefe kuruldugunu cevap govdesinde soyler.
type targetEchoProxy struct{ name string }

func (p targetEchoProxy) Serve(
	_ context.Context, req protocol.Req, _ io.Reader, sink tunnel.ResponseSink,
) {
	_ = sink.SendRes(200, map[string]string{"content-type": "text/plain"})
	_ = sink.Write([]byte(p.name + ":" + req.Path))
	_ = sink.End()
}

// startAgentWithTargets birden cok hedefle kabul akisini baslatir.
//
// Hangi hedefin secildigini gormek icin proxy'yi hedef adiyla etiketliyoruz:
// dogru hedefin secildigini iddia etmek ancak istek gercekten o proxy'ye
// dusunce anlam tasir.
func startAgentWithTargets(t *testing.T, relayURL string, names []string) *agentFixture {
	t.Helper()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relayURL}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close("test bitti") })

	acceptor, err := connect.New(connect.Config{
		Identity:      id,
		Pool:          pool,
		NewProxy:      func(name string) (tunnel.Proxy, error) { return targetEchoProxy{name: name}, nil },
		Targets:       names,
		MaxSessions:   2,
		MaxConcurrent: 8,
		MaxBody:       1 << 20,
		Logger:        quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = acceptor.Run(ctx)
	}()
	time.Sleep(500 * time.Millisecond)

	return &agentFixture{acceptor: acceptor, id: id, relayURL: relayURL, cancel: cancel, done: done}
}

// bodyOf tunelden tek bir GET yapar ve govdeyi dondurur.
func bodyOf(t *testing.T, v *visitor, path string) string {
	t.Helper()

	body := make(chan string, 1)
	v.data.OnMessage(func(m webrtc.DataChannelMessage) {
		frame, err := protocol.DecodeFrame(m.Data)
		if err != nil {
			t.Errorf("gecersiz cerceve: %v", err)
			return
		}
		select {
		case body <- string(frame.Payload):
		default:
		}
	})

	req := protocol.Req{
		ID: 1, Method: "GET", Path: path,
		Headers: map[string]string{}, HasBody: false,
	}
	if err := v.ctrl.SendText(req.Canonical()); err != nil {
		t.Fatal(err)
	}

	select {
	case b := <-body:
		return b
	case <-time.After(settle):
		t.Fatal("govde gelmedi")
		return ""
	}
}

// Tek ajan iki projeyi yayinliyor: her ziyaretci URL'sinde yazan hedefe
// baglaniyor (BRIEF Faz 3 kabul kriteri).
func TestTwoTargetsServedByOneAgent(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgentWithTargets(t, relay.URL(), []string{"admin", "default"})

	for _, tc := range []struct {
		name   string
		target string
		want   string
	}{
		{"hedefsiz offer default'a duser", "", "default:/x"},
		{"hedefli offer o hedefe gider", "admin", "admin:/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newVisitor(t, a, "")
			v.target = tc.target
			v.connect(settle)

			msg, ok := v.nextCtrl(settle)
			if !ok {
				t.Fatal("hello gelmedi")
			}
			hello, isHello := msg.(protocol.Hello)
			if !isHello {
				t.Fatalf("ilk mesaj hello olmali, geldi: %s", msg.Type())
			}
			// hello hedef listesini bildiriyor; ziyaretci hangi hedeflerin
			// var oldugunu boyle ogrenir.
			if len(hello.Targets) != 2 {
				t.Errorf("hello.targets: %v, beklenen iki hedef", hello.Targets)
			}

			if got := bodyOf(t, v, "/x"); got != tc.want {
				t.Errorf("govde %q, beklenen %q (yanlis hedefe baglanildi)", got, tc.want)
			}
		})
	}
}

// Bilinmeyen hedef SESSIZCE default'a dusmez: ajan bye ile reddeder ve
// oturum hic kurulmaz (bosa WebRTC maliyeti de olmaz).
func TestUnknownTargetRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgentWithTargets(t, relay.URL(), []string{"default"})
	v := newVisitor(t, a, "")
	v.target = "admin"
	v.offer()

	deadline := time.After(settle)
	for {
		msg, ok := v.nextSignaling(5 * time.Second)
		if !ok {
			select {
			case <-deadline:
				t.Fatal("bye gelmedi")
			default:
				continue
			}
		}
		if msg.Type != signaling.TypeBye {
			t.Fatalf("bye beklenirdi, geldi: %s", msg.Type)
		}
		if msg.Reason != "unknown-target" {
			t.Errorf("neden %q, beklenen unknown-target", msg.Reason)
		}
		if got := a.acceptor.SessionCount(); got != 0 {
			t.Errorf("acik oturum %d, beklenen 0 (oturum hic kurulmamali)", got)
		}
		return
	}
}
