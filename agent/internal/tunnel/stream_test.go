package tunnel

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Long-lived HTTP streams (SSE) count against a separate budget: if an
// SSE response held its MaxConcurrent slot for its whole life, 64 SSE
// requests would make the tunnel unusable for ordinary requests.

// sseProxy answers /sse with an endless event-stream response and
// everything else with a short text.
func sseProxy() proxyFunc {
	return func(ctx context.Context, req protocol.Req, _ io.Reader, sink ResponseSink) {
		if req.Path != "/sse" {
			if err := sink.SendRes(200, map[string]string{"content-type": "text/plain"}); err != nil {
				return
			}
			_ = sink.Write([]byte("ok"))
			_ = sink.End()
			return
		}

		if err := sink.SendRes(200, map[string]string{
			"content-type":  "text/event-stream",
			"cache-control": "no-store",
		}); err != nil {
			// The budget is full: the sink already sent an abort.
			return
		}
		_ = sink.Write([]byte("data: ilk\n\n"))
		// The stream runs until the visitor or the session closes.
		<-ctx.Done()
		_ = sink.Abort("upstream-canceled")
	}
}

// waitRes waits for a res or an abort for an id.
func (v *visitor) waitRes(t *testing.T, id uint32, d time.Duration) (*protocol.Res, string) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.Res:
				if msg.ID == id {
					cp := msg
					return &cp, ""
				}
			case protocol.Abort:
				if msg.ID == id {
					return nil, msg.Reason
				}
			}
		case <-v.dataCh:
			// The body frames do not matter in this test.
		case <-deadline:
			t.Fatalf("no response for id %d arrived within %s", id, d)
			return nil, ""
		}
	}
}

// An open SSE stream must NOT LOCK UP the ordinary request budget.
func TestSSEReleasesRequestBudget(t *testing.T) {
	sess, v := connect(t, Config{
		Proxy:         sseProxy(),
		MaxConcurrent: 1,
		MaxStreams:    2,
	})
	defer func() { _ = sess.Close() }()

	if err := v.sendCtrl(protocol.Req{
		ID: 1, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason := v.waitRes(t, 1, 5*time.Second)
	if res == nil {
		t.Fatalf("no SSE response arrived: %s", reason)
	}

	// MaxConcurrent is 1 and the SSE stream is still open; an ordinary
	// request still has to get through.
	if err := v.sendCtrl(protocol.Req{
		ID: 2, Method: "GET", Path: "/x", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason = v.waitRes(t, 2, 5*time.Second)
	if res == nil {
		t.Fatalf("an ordinary request was refused: %s (the SSE may not have released its slot)", reason)
	}
}

// With the stream budget full the SSE response has to be refused WITH A
// REASON: hanging silently, or answering 502, leads to a wrong
// diagnosis.
func TestSSEBudgetFullAborts(t *testing.T) {
	sess, v := connect(t, Config{
		Proxy:         sseProxy(),
		MaxConcurrent: 4,
		MaxStreams:    1,
	})
	defer func() { _ = sess.Close() }()

	if err := v.sendCtrl(protocol.Req{
		ID: 1, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	if res, reason := v.waitRes(t, 1, 5*time.Second); res == nil {
		t.Fatalf("the first SSE response did not arrive: %s", reason)
	}

	if err := v.sendCtrl(protocol.Req{
		ID: 2, Method: "GET", Path: "/sse", Headers: map[string]string{}, HasBody: false,
	}); err != nil {
		t.Fatal(err)
	}
	res, reason := v.waitRes(t, 2, 5*time.Second)
	if res != nil {
		t.Fatalf("a second SSE was accepted although the budget is full (status %d)", res.Status)
	}
	if reason != "too-many-streams" {
		t.Errorf("the abort reason is %q, expected %q", reason, "too-many-streams")
	}
}

// content-type parsing: parameters and whitespace have to be ignored.
func TestIsEventStream(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"  TEXT/Event-Stream ;charset=utf-8", true},
		{"text/plain", false},
		{"", false},
		{"text/event-streaming", false},
		{"application/json; x=text/event-stream", false},
	} {
		if got := isEventStream(tc.in); got != tc.want {
			t.Errorf("isEventStream(%q) = %v, expected %v", tc.in, got, tc.want)
		}
	}
}
