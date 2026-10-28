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

// Phase 3: once the visitor is gone the agent has to release its
// resources.
//
// Why we do not rely on WebRTC alone: the notice the page sends on
// pagehide is best effort (if the tab crashes it never arrives), and ICE
// moving to Disconnected can take far longer and may come back.

// When the visitor falls silent the session closes within
// IdleTimeout.
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

	// The visitor sends nothing and answers no ping with a pong: taken as
	// gone.
	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the idle session did not close")
	}
	if !errors.Is(sess.Err(), ErrIdle) {
		t.Errorf("the close reason: %v, expected ErrIdle", sess.Err())
	}
}

// As long as the visitor answers, the session stays open.
//
// The agent sends pings and the page answers with pongs
// (web/src/tunnel.ts): that is what keeps a quiet but open tunnel
// alive.
func TestActiveSessionStaysOpen(t *testing.T) {
	sess, v := connect(t, Config{
		IdleTimeout:  600 * time.Millisecond,
		PingInterval: 100 * time.Millisecond,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})

	// Imitate the visitor side: answer an incoming ping with a pong.
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
		t.Fatalf("a session that answers with pongs should not have closed: %v", sess.Err())
	case <-time.After(2 * time.Second):
	}
}

// A body that passes the limit cancels the request even when it arrives
// BEFORE `req`.
//
// This path really does happen: because ctrl and data are separate SCTP
// streams there is NO ordering guarantee between them (measured in phase
// 1, BRIEF section 8). The frame used to be dropped silently in this
// case: because the body never arrived the proxy waited forever, the
// visitor got no abort and the request hung — so a limit guard turned
// into a resource leak.
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

	// The frame FIRST, req AFTER: the early buffer path.
	if err := v.sendFrame(11, 0, bytes.Repeat([]byte("y"), 2048)); err != nil {
		t.Fatal(err)
	}
	// Give the frame time to reach the agent; otherwise the order can
	// flip and we skip the path the test means to measure.
	time.Sleep(300 * time.Millisecond)

	if err := v.sendCtrl(protocol.Req{
		ID: 11, Method: "POST", Path: "/api", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}

	got := v.collect(t, 11, settle)
	if got.aborted != "body-too-large" {
		t.Errorf("the abort reason is %q, expected body-too-large", got.aborted)
	}
	// The request must NEVER reach the proxy: carrying a body that passes
	// the limit to the target is pointless.
	select {
	case <-served:
		t.Error("a request that passes the limit should not have reached the proxy")
	default:
	}
}
