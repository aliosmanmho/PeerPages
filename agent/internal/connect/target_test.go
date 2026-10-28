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

// Phase 3: the target comes from the visitor's URL and rides in the
// offer.
//
// The tests here check that a single agent shares TWO projects (the BRIEF
// phase 3 acceptance criterion) and that an unknown target does not fall
// back to the default silently.

// targetEchoProxy says which target it was built for in the response
// body.
type targetEchoProxy struct{ name string }

func (p targetEchoProxy) Serve(
	_ context.Context, req protocol.Req, _ io.Reader, sink tunnel.ResponseSink,
) {
	_ = sink.SendRes(200, map[string]string{"content-type": "text/plain"})
	_ = sink.Write([]byte(p.name + ":" + req.Path))
	_ = sink.End()
}

// startAgentWithTargets starts the accept flow with several targets.
//
// To see which target was picked we label the proxy with the target name:
// claiming the right target was chosen only means something once the
// request really lands on that proxy.
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

// bodyOf makes a single GET over the tunnel and returns the body.
func bodyOf(t *testing.T, v *visitor, path string) string {
	t.Helper()

	body := make(chan string, 1)
	v.data.OnMessage(func(m webrtc.DataChannelMessage) {
		frame, err := protocol.DecodeFrame(m.Data)
		if err != nil {
			t.Errorf("invalid frame: %v", err)
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
		t.Fatal("no body arrived")
		return ""
	}
}

// A single agent shares two projects: every visitor connects to the
// target written in their URL (the BRIEF phase 3 acceptance
// criterion).
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
		{"an offer with no target falls back to the default", "", "default:/x"},
		{"an offer with a target goes to that target", "admin", "admin:/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newVisitor(t, a, "")
			v.target = tc.target
			v.connect(settle)

			msg, ok := v.nextCtrl(settle)
			if !ok {
				t.Fatal("no hello arrived")
			}
			hello, isHello := msg.(protocol.Hello)
			if !isHello {
				t.Fatalf("the first message has to be hello, got: %s", msg.Type())
			}
			// hello announces the target list; that is how the visitor
			// learns which targets exist.
			if len(hello.Targets) != 2 {
				t.Errorf("hello.targets: %v, two targets expected", hello.Targets)
			}

			if got := bodyOf(t, v, "/x"); got != tc.want {
				t.Errorf("body %q, expected %q (it connected to the wrong target)", got, tc.want)
			}
		})
	}
}

// An unknown target does NOT fall back to the default SILENTLY: the
// agent refuses with bye and no session is built at all (so no WebRTC
// cost is wasted either).
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
				t.Fatal("no bye arrived")
			default:
				continue
			}
		}
		if msg.Type != signaling.TypeBye {
			t.Fatalf("a bye was expected, got: %s", msg.Type)
		}
		if msg.Reason != "unknown-target" {
			t.Errorf("the reason is %q, expected unknown-target", msg.Reason)
		}
		if got := a.acceptor.SessionCount(); got != 0 {
			t.Errorf("%d open sessions, expected 0 (no session should be built at all)", got)
		}
		return
	}
}
