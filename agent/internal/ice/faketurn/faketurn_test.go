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

// Checks the fake server REALLY carries data: granting an allocation is
// not enough, a packet sent to the relay address has to reach the
// client.
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
		t.Fatalf("no allocation was granted: %v", err)
	}
	defer func() { _ = relayConn.Close() }()
	t.Logf("the relay address: %s", relayConn.LocalAddr())

	// The peer's side: it sends a packet to the relay address.
	// We bind to 0.0.0.0: the advertised relay address is the LAN address
	// (as it is on a real TURN server too), and a socket bound to
	// loopback cannot send packets there.
	peer, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()

	// The LocalAddr of a socket bound to 0.0.0.0 comes back as 0.0.0.0,
	// so we build the destination address explicitly. The IP is taken
	// from the address the relay advertised: TURN permissions are per peer
	// IP, so the source IP the peer uses when writing to the relay has to
	// be the SAME as the permitted one.
	relayAddr, ok := relayConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatal("the relay address could not be read")
	}
	peerPort := peer.LocalAddr().(*net.UDPAddr).Port
	peerAddr := &net.UDPAddr{IP: relayAddr.IP, Port: peerPort}

	// No traffic passes without a permission; the client grants one once
	// it learns the peer's address (which is what ICE does too).
	if _, err := relayConn.WriteTo([]byte("merhaba"), peerAddr); err != nil {
		t.Fatalf("it could not write over the relay: %v", err)
	}

	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, from, err := peer.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the peer did not receive the packet: %v", err)
	}
	if string(buf[:n]) != "merhaba" {
		t.Errorf("body %q", buf[:n])
	}
	t.Logf("the peer received the packet from %s", from)

	// The reverse direction: the peer sends to the relay address and the
	// client receives.
	if _, err := peer.WriteTo([]byte("cevap"), relayConn.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = relayConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err = relayConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("the client could not read from the relay: %v", err)
	}
	if string(buf[:n]) != "cevap" {
		t.Errorf("body %q", buf[:n])
	}

	if srv.Allocations() != 1 {
		t.Errorf("%d allocations, 1 expected", srv.Allocations())
	}
}
