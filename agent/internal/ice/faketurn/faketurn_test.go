package faketurn_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
)

// Sahte sunucunun GERCEKTEN veri tasidigini dogrular: allocation vermek
// yetmez, relay adresine gelen paket istemciye ulasmali.
func TestRelaysData(t *testing.T) {
	srv, err := faketurn.Start(faketurn.Options{User: "u", Pass: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	addr := srv.URL()[len("turn:"):]
	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: addr,
		TURNServerAddr: addr,
		Username:       "u",
		Password:       "p",
		Conn:           conn,
		LoggerFactory:  &logging.DefaultLoggerFactory{Writer: io.Discard},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		t.Fatal(err)
	}

	relayConn, err := client.Allocate()
	if err != nil {
		t.Fatalf("allocation alinamadi: %v", err)
	}
	defer func() { _ = relayConn.Close() }()
	t.Logf("relay adresi: %s", relayConn.LocalAddr())

	// Esin tarafi: relay adresine paket yollar.
	// 0.0.0.0'a baglaniyoruz: duyurulan relay adresi LAN adresi
	// (gercek bir TURN sunucusunda da oyle), loopback'e bagli bir soket
	// oraya paket gonderemez.
	peer, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()

	// 0.0.0.0'a bagli bir soketin LocalAddr'i 0.0.0.0 dondurur; hedef
	// adresi acikca kuruyoruz. IP, relay'in duyurdugu adresten aliniyor:
	// TURN izinleri peer IP'si basinadir, yani esin relay'e yazarken
	// kullandigi kaynak IP ile izin verilen IP AYNI olmali.
	relayAddr, ok := relayConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("relay adresi okunamadi")
	}
	peerPort := peer.LocalAddr().(*net.UDPAddr).Port
	peerAddr := &net.UDPAddr{IP: relayAddr.IP, Port: peerPort}

	// Izin olmadan trafik gecmez; istemci esin adresini ogrenince izin
	// veriyor (ICE de boyle yapiyor).
	if _, err := relayConn.WriteTo([]byte("merhaba"), peerAddr); err != nil {
		t.Fatalf("relay uzerinden yazilamadi: %v", err)
	}

	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, from, err := peer.ReadFrom(buf)
	if err != nil {
		t.Fatalf("es paketi almadi: %v", err)
	}
	if string(buf[:n]) != "merhaba" {
		t.Errorf("govde %q", buf[:n])
	}
	t.Logf("es paketi %s adresinden aldi", from)

	// Ters yon: es relay adresine yollar, istemci alir.
	if _, err := peer.WriteTo([]byte("cevap"), relayConn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = relayConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err = relayConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("istemci relay'den okuyamadi: %v", err)
	}
	if string(buf[:n]) != "cevap" {
		t.Errorf("govde %q", buf[:n])
	}

	if srv.Allocations() != 1 {
		t.Errorf("allocation sayisi %d, 1 beklendi", srv.Allocations())
	}
}
