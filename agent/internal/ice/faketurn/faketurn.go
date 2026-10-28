// Package faketurn provides an in-process TURN server for tests.
//
// Why it is needed: the phase 5 acceptance criterion is "it connects over
// TURN on symmetric NAT". We cannot imitate symmetric NAT on a single
// machine, but we can build a situation that produces the SAME RESULT:
// force the agent to gather relay candidates only
// (ice.Config.RelayOnly) and point it here. Then the direct path is
// IMPOSSIBLE, so the tunnel opening is proof that the traffic went over
// TURN — stronger than a mobile hotspot attempt, where the direct path
// may have worked as well.
//
// Same pattern as internal/nostr/fakerelay: a test that depends on the
// real internet is fragile.
package faketurn

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

// Realm is the TURN long-term credential realm.
const Realm = "peerpages.test"

// Options are the fake server's settings.
type Options struct {
	// User and Pass are the only accepted credentials.
	User, Pass string
}

// Server is the in-process TURN server.
type Server struct {
	srv  *turn.Server
	conn net.PacketConn
	addr *net.UDPAddr

	allocations atomic.Int64
}

// Start starts it on a free port.
func Start(opts Options) (*Server, error) { return StartOn(0, opts) }

// StartOn starts it on the given port (0 = pick a free one).
//
// Only 127.0.0.1 is listened on: this is a TEST server and must not be
// exposed to the network.
func StartOn(port int, opts Options) (*Server, error) {
	if opts.User == "" || opts.Pass == "" {
		return nil, errors.New("faketurn: a user and a password are required")
	}

	conn, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("faketurn: could not listen: %w", err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("faketurn: the listening address could not be read")
	}

	s := &Server{conn: conn, addr: addr}
	relayIP := localIPv4()

	key := turn.GenerateAuthKey(opts.User, Realm, opts.Pass)
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: Realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) {
			if ra == nil || ra.Username != opts.User || ra.Realm != Realm {
				return "", nil, false
			}
			return ra.Username, key, true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				// The advertised relay address is the machine's LAN
				// address, while the socket is bound to every interface.
				//
				// Why NOT 127.0.0.1: the other side (pion or the browser)
				// usually does not produce a loopback candidate, it uses
				// the LAN address, and a socket bound to 127.0.0.1 cannot
				// send packets there. The advertised address also has to
				// be the SAME as the source address the relay uses when
				// it sends packets: otherwise the other side counts the
				// incoming check as "peer-reflexive" and the selected
				// candidate pair does NOT LOOK like a relay (on a real
				// TURN server that consistency is there already).
				RelayAddress: relayIP,
				Address:      "0.0.0.0",
			},
		}},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(
				_, _ net.Addr, _, _, _ string, _ net.Addr, _ int,
			) {
				s.allocations.Add(1)
			},
		},
		// The tests run fast; the server logs are noise.
		LoggerFactory: &logging.DefaultLoggerFactory{Writer: io.Discard},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("faketurn: the server could not be built: %w", err)
	}
	s.srv = srv
	return s, nil
}

// URL is the TURN address to hand to the agent.
func (s *Server) URL() string {
	return fmt.Sprintf("turn:127.0.0.1:%d", s.addr.Port)
}

// Port is the port being listened on (to restart on the same address).
func (s *Server) Port() int { return s.addr.Port }

// Allocations is how many allocations were granted.
//
// This is the test's real claim: if the tunnel opened AND this counter
// went up, the traffic really did go over TURN.
func (s *Server) Allocations() int64 { return s.allocations.Load() }

// Close shuts the server down.
func (s *Server) Close() error {
	if s.srv == nil {
		return s.conn.Close()
	}
	return s.srv.Close()
}

// localIPv4 finds the IPv4 address on the machine's default outbound
// interface.
//
// No packet is sent: the UDP "connection" is only a way to ask the
// operating system which interface it would pick. With none found it
// falls back to loopback (which is enough in environments that only
// produce loopback candidates).
func localIPv4() net.IP {
	c, err := net.Dial("udp4", "203.0.113.1:9")
	if err != nil {
		return net.IPv4(127, 0, 0, 1)
	}
	defer func() { _ = c.Close() }()

	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return net.IPv4(127, 0, 0, 1)
	}
	return addr.IP
}
