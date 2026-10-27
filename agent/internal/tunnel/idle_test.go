package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Faz 3: ziyaretci gidince ajan kaynagi birakmali.
//
// Neden tek basina WebRTC'ye guvenmiyoruz: sayfanin pagehide'da gonderdigi
// haber en iyi cabadir (sekme cokerse hic gelmez), ICE'in Disconnected'a
// gecmesi ise cok daha uzun surebilir ve geri donebilir.

// Ziyaretci susunca oturum IdleTimeout icinde kapanir.
func TestIdleSessionClosed(t *testing.T) {
	sess, v := connect(t, Config{
		IdleTimeout:  600 * time.Millisecond,
		PingInterval: 100 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle) // hello

	// Ziyaretci hicbir sey yollamiyor ve ping'e pong donmuyor: gitmis kabul.
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("bosta kalan oturum kapanmadi")
	}
	if !errors.Is(sess.Err(), ErrIdle) {
		t.Errorf("kapanis nedeni: %v, beklenen ErrIdle", sess.Err())
	}
}

// Ziyaretci yanit verdigi surece oturum kapanmaz.
//
// Ajan ping yolluyor, sayfa pong donuyor (web/src/tunnel.ts): sessiz ama acik
// bir tunel bu sayede ayakta kalir.
func TestActiveSessionStaysOpen(t *testing.T) {
	sess, v := connect(t, Config{
		IdleTimeout:  600 * time.Millisecond,
		PingInterval: 100 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	// Ziyaretci tarafini taklit et: gelen ping'e pong don.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case m := <-v.ctrlCh:
				if _, isPing := m.(protocol.Ping); isPing {
					_ = v.sendCtrl(protocol.Pong{})
				}
			}
		}
	}()

	select {
	case <-sess.Done():
		t.Fatalf("pong donen oturum kapanmamaliydi: %v", sess.Err())
	case <-time.After(2 * time.Second):
	}
}

// Tavani asan govde `req`'TEN ONCE gelirse de istek iptal edilir.
//
// Bu yol gercekten gorulebilir: ctrl ve data ayri SCTP akislari oldugu icin
// aralarinda siralama garantisi YOK (Faz 1'de olculdu, BRIEF bolum 8).
// Daha once bu durumda cerceve sessizce dusuruluyordu: govde hic gelmedigi
// icin proxy sonsuza kadar bekliyor, ziyaretci abort almiyor ve istek
// askida kaliyordu — yani tavan korumasi kaynak sizintisina donusuyordu.
func TestEarlyOversizedBodyAborts(t *testing.T) {
	served := make(chan struct{}, 1)
	_, v := connect(t, Config{
		MaxBody: 1024,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			served <- struct{}{}
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle) // hello

	// Once cerceve, SONRA req: erken tampon yolu.
	if err := v.sendFrame(11, 0, bytes.Repeat([]byte("y"), 2048)); err != nil {
		t.Fatal(err)
	}
	// Cercevenin ajana varmasina zaman tani; aksi halde sira tersine donup
	// testin olcmek istedigi yolu atlayabiliriz.
	time.Sleep(300 * time.Millisecond)

	if err := v.sendCtrl(protocol.Req{
		ID: 11, Method: "POST", Path: "/api", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}

	got := v.collect(t, 11, settle)
	if got.aborted != "body-too-large" {
		t.Errorf("abort nedeni %q, beklenen body-too-large", got.aborted)
	}
	// Istek proxy'ye HIC dusmemeli: tavani asan govdeyi hedefe tasimanin
	// anlami yok.
	select {
	case <-served:
		t.Error("tavani asan istek proxy'ye dusmemeliydi")
	default:
	}
}
