package connect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// The phase 5 acceptance criterion: "it connects over TURN in a
// symmetric NAT simulation".
//
// We cannot imitate symmetric NAT on a single machine, but we can build a
// situation that produces the SAME RESULT: force the agent to gather
// relay candidates only with --ice-relay-only. Then the direct path is
// IMPOSSIBLE and the tunnel opening is proof that the traffic went over
// TURN. The negative control (that nothing connects without TURN) sits
// right below: without it this test could pass over the direct path and
// stay green by accident.

// startRelayOnlyAgent starts the agent so it gathers relay candidates
// only.
func startRelayOnlyAgent(t *testing.T, relayURL string, iceCfg ice.Config) *agentFixture {
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
		Identity: id,
		Pool:     pool,
		NewProxy: func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:  []string{"default"},
		ICE: func() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
			return iceCfg.Servers(), iceCfg.Policy()
		},
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

func TestTunnelOverTURNOnly(t *testing.T) {
	turnSrv, err := faketurn.Start(faketurn.Options{User: "user", Pass: "sifre"})
	if err != nil {
		t.Fatalf("the fake TURN could not be started: %v", err)
	}
	defer func() { _ = turnSrv.Close() }()

	iceCfg, err := ice.Config{
		TURN:      []string{turnSrv.URL()},
		User:      "user",
		Pass:      "sifre",
		RelayOnly: true,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startRelayOnlyAgent(t, relay.URL(), iceCfg)
	v := newVisitor(t, a, "")

	// The visitor arrives with the NORMAL configuration (host
	// candidates): that is how it is in production too, no TURN
	// credential ever reaches the page.
	// Diagnostics: we want to see which candidates the agent sent.
	v.connect(settle)

	if got := bodyOf(t, v, "/turn-testi"); got == "" {
		t.Error("the response body is empty")
	}

	// The real proof: the agent's only candidate was a TURN relay
	// candidate, so this traffic went through the TURN server.
	if n := turnSrv.Allocations(); n == 0 {
		t.Error("the TURN server granted no allocation: the connection did not go over the relay")
	}
}

// The negative control: relay-only, but TURN is unreachable. The agent
// gathers no usable candidate and the tunnel MUST NOT OPEN.
//
// Without this test the one above could pass over the direct path and
// stay green by accident.
func TestRelayOnlyWithoutTURNFails(t *testing.T) {
	iceCfg, err := ice.Config{
		// An address nothing listens on: no allocation can be granted.
		TURN:      []string{"turn:127.0.0.1:1"},
		User:      "user",
		Pass:      "sifre",
		RelayOnly: true,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startRelayOnlyAgent(t, relay.URL(), iceCfg)
	v := newVisitor(t, a, "")

	if err := tryConnect(v, 12*time.Second); err == nil {
		t.Error("a relay-only agent with no TURN should not have been reachable")
	}
}

// tryConnect runs the same flow as connect() but does NOT FAIL the test
// on failure, it returns the error: in the negative control the expected
// outcome is exactly that nothing connects.
func tryConnect(v *visitor, d time.Duration) error {
	v.offer()

	deadline := time.After(d)
	for {
		select {
		case <-v.ready:
			return nil
		case <-deadline:
			return errors.New("the tunnel did not open")
		default:
		}

		msg, ok := v.nextSignaling(time.Second)
		if !ok {
			continue
		}
		if msg.SID != v.sid {
			continue
		}
		switch msg.Type {
		case signaling.TypeAnswer:
			if err := v.pc.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				return err
			}
		case signaling.TypeCandidate:
			mid := msg.Mid
			_ = v.pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate: msg.Candidate, SDPMid: &mid,
			})
		case signaling.TypeBye:
			return errors.New("the agent sent a bye: " + msg.Reason)
		case signaling.TypeOffer:
			return errors.New("ajandan offer geldi")
		}
	}
}
