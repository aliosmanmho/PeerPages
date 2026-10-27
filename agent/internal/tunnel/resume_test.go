package tunnel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Faz 4: laptop uyuyup uyaninca tunel kendini toparlamali.
//
// Faz 3'un bosta kalma denetimi uyanmadan sonra oturumu KESIN olarak
// olduruyordu: lastSeen unix nano olarak saklanip time.Unix ile geri
// okunuyordu, bu da monotonik okumayi dusurup karsilastirmayi duvar saatine
// ceviriyordu. Askiya alma suresi oldugu gibi "sessizlik" sayiliyor ve
// uyanmadan sonraki ilk tik tum oturumlari kapatiyordu — ziyaretci hala
// orada olsa bile, tek bir ping denemesi yapmadan.

func TestIdleDecide(t *testing.T) {
	const (
		idle     = 30 * time.Second
		interval = 10 * time.Second
	)

	cases := []struct {
		name    string
		gap     time.Duration
		silence time.Duration
		want    idleAction
	}{
		{"normal tik", interval, 3 * time.Second, idlePing},
		{"sessizlik tavanda", interval, idle, idleClose},
		{"sessizlik tavani asti", interval, 2 * idle, idleClose},
		{"tik biraz gecikti", 2 * interval, 5 * time.Second, idlePing},
		// Asil mesele bu: uzun bir askiya almadan sonra sessizlik tavani
		// asmis GORUNUR. Kapatmak yanlis olur; once ping'le sinanir.
		{"uyanma, sessizlik tavani asmis gorunuyor", 20 * time.Minute, 20 * time.Minute, idleResume},
		{"uyanma, sessizlik az", 5 * interval, time.Second, idleResume},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idleDecide(c.gap, c.silence, idle, interval); got != c.want {
				t.Errorf("idleDecide(%v, %v) = %v, beklenen %v", c.gap, c.silence, got, c.want)
			}
		})
	}
}

// lastSeen bir ZAMAN DAMGASI degil, oturumun kurulusundan bu yana gecen
// SURE tutmali. Bu ayrim duvar saati degisimlerine (NTP adimi, elle saat
// degisikligi) karsi korumanin tamami: unix nano'ya geri donen bir
// degisiklik bu testi buyuklugunden yakalar.
func TestLastSeenIsMonotonicElapsed(t *testing.T) {
	sess, _ := connect(t, Config{
		IdleTimeout:  time.Hour,
		PingInterval: time.Hour,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	sess.touch()
	stored := sess.lastSeen.Load()

	// Oturum yeni kuruldu: gecen sure bir kac saniyeden fazla olamaz.
	if stored < 0 || time.Duration(stored) > time.Minute {
		t.Errorf("lastSeen = %v; gecen sure gibi gorunmuyor (unix damgasi mi?)", time.Duration(stored))
	}
	if s := sess.silence(); s < 0 || s > time.Minute {
		t.Errorf("silence() = %v, makul degil", s)
	}
}

// ICE "Disconnected"a dustugunde oturum HEMEN kapanmamali: bu durum
// gecicidir ve geri donebilir (tunnel.go'daki yorumun kendisi de boyle
// diyor). Faz 3'e kadar kod bunu aninda ve TEMIZ kapanis olarak
// isliyordu.
func TestDisconnectGraceRecovers(t *testing.T) {
	sess, _ := connect(t, Config{
		IdleTimeout:     time.Hour,
		PingInterval:    time.Hour,
		DisconnectGrace: 800 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	sess.armDisconnectTimer()

	// Baglanti geri geldi: bekleyen kapatma iptal edilmeli.
	time.Sleep(200 * time.Millisecond)
	if !sess.cancelDisconnectTimer() {
		t.Fatal("bekleyen kapatma bulunamadi")
	}

	select {
	case <-sess.Done():
		t.Fatalf("baglanti geri geldiginde oturum kapanmamaliydi: %v", sess.Err())
	case <-time.After(1500 * time.Millisecond):
	}
}

// Geri donmezse sure dolunca kapanir ve neden AYIRT EDILEBILIR olur: daha
// once bu yol closeWith(nil) ile ziyaretcinin `bye`'indan farksizdi.
func TestDisconnectGraceCloses(t *testing.T) {
	sess, _ := connect(t, Config{
		IdleTimeout:     time.Hour,
		PingInterval:    time.Hour,
		DisconnectGrace: 300 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	sess.armDisconnectTimer()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("geri donmeyen baglanti kapatilmadi")
	}
	if !errors.Is(sess.Err(), ErrDisconnected) {
		t.Errorf("kapanis nedeni: %v, beklenen ErrDisconnected", sess.Err())
	}
}

// Ikinci bir Disconnected ilk kopmanin saatini sifirlamamali: aksi halde
// durum iki arada gidip gelirken kapatma hic gerceklesmezdi.
func TestDisconnectTimerNotRearmed(t *testing.T) {
	sess, _ := connect(t, Config{
		IdleTimeout:     time.Hour,
		PingInterval:    time.Hour,
		DisconnectGrace: 600 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	start := time.Now()
	sess.armDisconnectTimer()
	time.Sleep(400 * time.Millisecond)
	sess.armDisconnectTimer() // sureyi uzatmamali

	select {
	case <-sess.Done():
		if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
			t.Errorf("kapanis %v surdu; sure sifirlanmis gorunuyor", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("oturum kapanmadi")
	}
}
