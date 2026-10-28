package connect_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// This file tests the agent's NOSTR PATH end to end: a real (fake) relay,
// real NIP-44 encryption, real WebRTC. No browser — we imitate the
// visitor side with pion.
//
// A fake relay is used instead of real internet relays: the flow itself
// has to be verified deterministically. The real relays are measured
// separately with `peerpages doctor`.

const settle = 30 * time.Second

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// echoProxy answers every request with a fixed response.
type echoProxy struct{}

func (echoProxy) Serve(_ context.Context, req protocol.Req, _ io.Reader, sink tunnel.ResponseSink) {
	_ = sink.SendRes(200, map[string]string{"content-type": "text/plain"})
	_ = sink.Write([]byte("merhaba " + req.Path))
	_ = sink.End()
}

// visitor imitates the visitor side: signaling over Nostr plus WebRTC.
type visitor struct {
	t     *testing.T
	id    identity.Identity
	pool  *nostr.Pool
	codec codec
	agent string // the agent's pubkey (hex)
	sid   string
	// target is the target name announced in the offer; when empty the
	// field is not sent at all.
	target string
	pc     *webrtc.PeerConnection
	ctrl   *webrtc.DataChannel
	data   *webrtc.DataChannel
	events <-chan gonostr.RelayEvent

	mu     sync.Mutex
	ctrlCh chan protocol.Msg
	ready  chan struct{}
	once   sync.Once
}

type codec interface {
	Encode(m signaling.Msg) (string, error)
	Decode(payload string) (signaling.Msg, error)
}

type agentFixture struct {
	acceptor *connect.Acceptor
	id       identity.Identity
	relayURL string
	cancel   context.CancelFunc
	done     chan struct{}
}

// startAgent starts the accept flow on the fake relay.
func startAgent(t *testing.T, relayURL, secret string) *agentFixture {
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

	var sealer *signaling.Sealer
	if secret != "" {
		if sealer, err = signaling.NewSealer(secret); err != nil {
			t.Fatal(err)
		}
	}

	acceptor, err := connect.New(connect.Config{
		Identity: id,
		Pool:     pool,
		Sealer:   sealer,
		NewProxy: func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:  []string{"default"},
		// No STUN: on loopback the host candidates are enough and the test
		// stays fast.
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

	// Give the subscription time to come up: an ephemeral event is not
	// stored, so an offer sent while the agent is not listening is lost.
	time.Sleep(500 * time.Millisecond)

	return &agentFixture{acceptor: acceptor, id: id, relayURL: relayURL, cancel: cancel, done: done}
}

// newVisitor builds the visitor side: it opens the channels and
// subscribes on Nostr.
func newVisitor(t *testing.T, a *agentFixture, secret string) *visitor {
	t.Helper()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{a.relayURL}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close("test bitti") })

	var c codec = (*signaling.Sealer)(nil)
	if secret != "" {
		s, serr := signaling.NewSealer(secret)
		if serr != nil {
			t.Fatal(serr)
		}
		c = s
	}

	sid, err := signaling.NewSID()
	if err != nil {
		t.Fatal(err)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	v := &visitor{
		t: t, id: id, pool: pool, codec: c,
		agent: a.id.PublicKey, sid: sid, pc: pc,
		ctrlCh: make(chan protocol.Msg, 64),
		ready:  make(chan struct{}),
	}

	// The VISITOR opens the channels.
	ordered := true
	if v.ctrl, err = pc.CreateDataChannel("ctrl", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		t.Fatal(err)
	}
	if v.data, err = pc.CreateDataChannel("data", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		t.Fatal(err)
	}

	v.ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		msg, perr := protocol.ParseCtrl(string(m.Data))
		if perr != nil {
			t.Errorf("invalid ctrl: %v", perr)
			return
		}
		v.ctrlCh <- msg
	})

	check := func() {
		if v.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
			v.data.ReadyState() == webrtc.DataChannelStateOpen {
			v.once.Do(func() { close(v.ready) })
		}
	}
	v.ctrl.OnOpen(check)
	v.data.OnOpen(check)

	// Listen for the signaling addressed to us.
	v.events = pool.Subscribe(ctx, nostr.SignalingFilter(id.PublicKey, time.Now().Add(-time.Minute)))
	time.Sleep(300 * time.Millisecond)

	// Send the local candidates to the agent (trickle).
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		mid := "0"
		if j := cand.ToJSON(); j.SDPMid != nil && *j.SDPMid != "" {
			mid = *j.SDPMid
		}
		v.send(signaling.Msg{
			Type: signaling.TypeCandidate, SID: sid,
			Candidate: cand.ToJSON().Candidate, Mid: mid,
		})
	})

	return v
}

// send sends a signaling message to the agent (NIP-44 plus the secret
// layer).
func (v *visitor) send(msg signaling.Msg) {
	payload, err := v.codec.Encode(msg)
	if err != nil {
		v.t.Errorf("the message could not be encoded: %v", err)
		return
	}
	content, err := nostr.Encrypt(v.id.SecretKey, v.agent, payload)
	if err != nil {
		v.t.Errorf("NIP-44: %v", err)
		return
	}
	ev := nostr.NewSignalingEvent(v.agent, content)
	if err := ev.Sign(v.id.SecretKey); err != nil {
		v.t.Errorf("signature: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := v.pool.Publish(ctx, ev); err != nil {
		v.t.Errorf("publish: %v", err)
	}
}

// offer creates and publishes an offer (trickle: gathering is not
// awaited).
func (v *visitor) offer() {
	v.t.Helper()
	offer, err := v.pc.CreateOffer(nil)
	if err != nil {
		v.t.Fatal(err)
	}
	if err := v.pc.SetLocalDescription(offer); err != nil {
		v.t.Fatal(err)
	}
	v.send(signaling.Msg{
		Type: signaling.TypeOffer, SID: v.sid, SDP: offer.SDP, Target: v.target,
	})
}

// nextSignaling waits for the next signaling message from the agent.
func (v *visitor) nextSignaling(d time.Duration) (signaling.Msg, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-v.events:
			if !ok {
				return signaling.Msg{}, false
			}
			if ev.Event == nil || ev.Event.PubKey != v.agent {
				continue
			}
			payload, err := nostr.Decrypt(v.id.SecretKey, v.agent, ev.Event.Content)
			if err != nil {
				continue
			}
			msg, err := v.codec.Decode(payload)
			if err != nil {
				continue
			}
			return msg, true
		case <-deadline:
			return signaling.Msg{}, false
		}
	}
}

// connect runs the whole handshake: offer -> answer -> candidates.
func (v *visitor) connect(d time.Duration) {
	v.t.Helper()
	v.offer()

	deadline := time.After(d)
	answered := false

	for {
		select {
		case <-v.ready:
			return
		case <-deadline:
			v.t.Fatalf("the tunnel did not open within %s (answer received: %v)", d, answered)
			return
		default:
		}

		msg, ok := v.nextSignaling(2 * time.Second)
		if !ok {
			continue
		}
		switch msg.Type {
		case signaling.TypeAnswer:
			if msg.SID != v.sid {
				continue
			}
			if err := v.pc.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				v.t.Fatalf("the answer could not be applied: %v", err)
			}
			answered = true
		case signaling.TypeCandidate:
			if msg.SID != v.sid {
				continue
			}
			mid := msg.Mid
			if err := v.pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate: msg.Candidate, SDPMid: &mid,
			}); err != nil {
				v.t.Logf("the candidate could not be added (may be harmless): %v", err)
			}
		case signaling.TypeBye:
			v.t.Fatalf("the agent sent a bye: %s", msg.Reason)
		case signaling.TypeOffer:
			v.t.Error("no offer should come from the agent")
		}
	}
}

func (v *visitor) nextCtrl(d time.Duration) (protocol.Msg, bool) {
	select {
	case m := <-v.ctrlCh:
		return m, true
	case <-time.After(d):
		return nil, false
	}
}

// --- the tests ---

func TestNostrHandshakeOverFakeRelay(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")

	v.connect(settle)

	// Once the connection is up the agent sends hello.
	msg, ok := v.nextCtrl(settle)
	if !ok {
		t.Fatal("no hello arrived")
	}
	hello, isHello := msg.(protocol.Hello)
	if !isHello {
		t.Fatalf("the first message has to be hello, got: %s", msg.Type())
	}
	if hello.Auth != protocol.AuthNone {
		t.Errorf("with no secret it has to be auth none, got: %q", hello.Auth)
	}

	if got := a.acceptor.SessionCount(); got != 1 {
		t.Errorf("%d open sessions, expected 1", got)
	}
}

// The shared secret: when both sides know it the connection is made and
// hello announces auth=required.
func TestNostrHandshakeWithSecret(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	const secret = "kedi-balik-7"
	a := startAgent(t, relay.URL(), secret)
	v := newVisitor(t, a, secret)

	v.connect(settle)

	msg, ok := v.nextCtrl(settle)
	if !ok {
		t.Fatal("no hello arrived")
	}
	hello, isHello := msg.(protocol.Hello)
	if !isHello {
		t.Fatalf("a hello was expected: %s", msg.Type())
	}
	if hello.Auth != protocol.AuthRequired {
		t.Errorf("with a secret it has to be auth required, got: %q", hello.Auth)
	}
}

// Someone who knows the peer-id but not the secret CANNOT CONNECT
// (BRIEF section 4).
func TestSecretlessVisitorRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "right-secret")
	v := newVisitor(t, a, "") // no secret

	v.offer()

	// The agent must not answer and must not open a session.
	if msg, ok := v.nextSignaling(5 * time.Second); ok {
		t.Errorf("the agent should not have answered, got: %s", msg.Type)
	}
	if got := a.acceptor.SessionCount(); got != 0 {
		t.Errorf("no session should have opened, open: %d", got)
	}
}

// An offer that arrives with the wrong secret is refused too.
func TestWrongSecretRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "right-secret")
	v := newVisitor(t, a, "wrong-secret")

	v.offer()

	if msg, ok := v.nextSignaling(5 * time.Second); ok {
		t.Errorf("the agent should not have answered, got: %s", msg.Type)
	}
	if got := a.acceptor.SessionCount(); got != 0 {
		t.Errorf("no session should have opened, open: %d", got)
	}
}

// Once the tunnel is up a request really does reach the proxy.
func TestRequestOverNostrTunnel(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	if _, ok := v.nextCtrl(settle); !ok {
		t.Fatal("no hello arrived")
	}

	req := protocol.Req{
		ID: 1, Method: "GET", Path: "/try-me",
		Headers: map[string]string{}, HasBody: false,
	}
	if err := v.ctrl.SendText(req.Canonical()); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		msg, ok := v.nextCtrl(5 * time.Second)
		if !ok {
			select {
			case <-deadline:
				t.Fatal("no res arrived")
			default:
				continue
			}
		}
		if res, isRes := msg.(protocol.Res); isRes {
			if res.Status != 200 {
				t.Errorf("status %d", res.Status)
			}
			return
		}
	}
}

// Once the session limit is passed the agent refuses with bye.
func TestSessionLimit(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "") // MaxSessions: 2

	v1 := newVisitor(t, a, "")
	v1.connect(settle)
	v2 := newVisitor(t, a, "")
	v2.connect(settle)

	if got := a.acceptor.SessionCount(); got != 2 {
		t.Fatalf("two sessions were expected, open: %d", got)
	}

	// The third visitor must be refused.
	v3 := newVisitor(t, a, "")
	v3.offer()

	deadline := time.After(settle)
	for {
		msg, ok := v3.nextSignaling(3 * time.Second)
		if !ok {
			select {
			case <-deadline:
				t.Fatal("no bye arrived for the third visitor")
			default:
				continue
			}
		}
		if msg.Type == signaling.TypeBye {
			if msg.Reason != "too-many-sessions" {
				t.Errorf("the bye reason: %q", msg.Reason)
			}
			return
		}
		if msg.Type == signaling.TypeAnswer {
			t.Fatal("no answer should have arrived with the limit passed")
		}
	}
}

// On bye the session closes and the resources are released.
func TestByeClosesSession(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	if got := a.acceptor.SessionCount(); got != 1 {
		t.Fatalf("one session was expected: %d", got)
	}

	v.send(signaling.Msg{Type: signaling.TypeBye, SID: v.sid, Reason: "visitor-left"})

	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if a.acceptor.SessionCount() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("the session did not close after bye, open: %d", a.acceptor.SessionCount())
}

// A second offer with the same sid does not open a new session.
func TestDuplicateOfferIgnored(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	before := a.acceptor.SessionCount()
	// The same sid, a new event (a different id, so not a replay).
	v.send(signaling.Msg{Type: signaling.TypeOffer, SID: v.sid, SDP: "v=0\r\ncorrupt\r\n"})
	time.Sleep(2 * time.Second)

	if got := a.acceptor.SessionCount(); got != before {
		t.Errorf("the session count changed: %d -> %d", before, got)
	}
}

func TestConfigValidation(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	pool, err := nostr.NewPool(context.Background(),
		nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test")

	ok := connect.Config{
		Identity:      id,
		Pool:          pool,
		NewProxy:      func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:       []string{"default"},
		MaxSessions:   1,
		MaxConcurrent: 1,
		MaxBody:       1,
		Logger:        quietLogger(),
	}
	if _, err := connect.New(ok); err != nil {
		t.Fatalf("a valid configuration was refused: %v", err)
	}

	bad := map[string]func(*connect.Config){
		"no credentials":    func(c *connect.Config) { c.Identity = identity.Identity{} },
		"pool nil":          func(c *connect.Config) { c.Pool = nil },
		"proxy nil":         func(c *connect.Config) { c.NewProxy = nil },
		"no target":         func(c *connect.Config) { c.Targets = nil },
		"maxsessions sifir": func(c *connect.Config) { c.MaxSessions = 0 },
		"concurrent sifir":  func(c *connect.Config) { c.MaxConcurrent = 0 },
		"maxbody sifir":     func(c *connect.Config) { c.MaxBody = 0 },
		"logger nil":        func(c *connect.Config) { c.Logger = nil },
	}
	for name, mutate := range bad {
		cfg := ok
		mutate(&cfg)
		if _, err := connect.New(cfg); err == nil {
			t.Errorf("%s: should have been refused", name)
		}
	}
}
