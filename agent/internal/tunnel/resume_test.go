package tunnel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Phase 4: the tunnel has to recover when the laptop sleeps and wakes.
//
// The phase 3 idle check killed the session after a wake WITHOUT FAIL:
// lastSeen was stored as unix nanos and read back with time.Unix, which
// dropped the monotonic reading and turned the comparison into a wall
// clock one. The suspended time counted as "silence" as it was, and the
// first tick after the wake closed every session — even with the visitor
// still there, without a single ping attempt.

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
		{"the silence is at the limit", interval, idle, idleClose},
		{"the silence passed the limit", interval, 2 * idle, idleClose},
		{"tik biraz gecikti", 2 * interval, 5 * time.Second, idlePing},
		// This is the real case: after a long suspend the silence APPEARS
		// to have passed the limit. Closing would be wrong; it is tested
		// with a ping first.
		{"a wake, where the silence looks like it passed the limit", 20 * time.Minute, 20 * time.Minute, idleResume},
		{"uyanma, sessizlik az", 5 * interval, time.Second, idleResume},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idleDecide(c.gap, c.silence, idle, interval); got != c.want {
				t.Errorf("idleDecide(%v, %v) = %v, expected %v", c.gap, c.silence, got, c.want)
			}
		})
	}
}

// lastSeen must hold the TIME ELAPSED since the session was built, not a
// TIMESTAMP. That distinction is the whole protection against wall clock
// changes (an NTP step, a manual clock change): a change back to unix
// nanos gets caught by this test through the magnitude.
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

	// The session was just built: the elapsed time cannot be more than a
	// few seconds.
	if stored < 0 || time.Duration(stored) > time.Minute {
		t.Errorf("lastSeen = %v; that does not look like an elapsed time (a unix timestamp?)", time.Duration(stored))
	}
	if s := sess.silence(); s < 0 || s > time.Minute {
		t.Errorf("silence() = %v, which is not plausible", s)
	}
}

// When ICE drops to "Disconnected" the session must not close
// IMMEDIATELY: the state is temporary and may come back (the comment in
// tunnel.go says so itself). Until phase 3 the code treated it as an
// instant, CLEAN close.
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

	// The connection came back: the pending close must be cancelled.
	time.Sleep(200 * time.Millisecond)
	if !sess.cancelDisconnectTimer() {
		t.Fatal("no pending close was found")
	}

	select {
	case <-sess.Done():
		t.Fatalf("the session should not have closed when the connection came back: %v", sess.Err())
	case <-time.After(1500 * time.Millisecond):
	}
}

// If it does not come back it closes once the grace expires and the
// reason is DISTINGUISHABLE: this path used to be indistinguishable from
// the visitor's `bye` because of closeWith(nil).
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
		t.Fatal("a connection that never came back was not closed")
	}
	if !errors.Is(sess.Err(), ErrDisconnected) {
		t.Errorf("the close reason: %v, expected ErrDisconnected", sess.Err())
	}
}

// A second Disconnected must not reset the clock of the first drop:
// otherwise, with the state flapping, the close would never happen.
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
			t.Errorf("the close took %v; the grace looks like it was reset", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the session did not close")
	}
}
