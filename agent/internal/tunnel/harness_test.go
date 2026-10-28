package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// This file builds a pion peer that stands in for the visitor's browser,
// so tunnel + sink + flowControl can be tested over a real WebRTC
// connection without one.
//
// The roles after phase 2: the VISITOR opens the channels and publishes
// the offer, and the agent answers (protocol/signaling.md). This harness
// behaves the same way.

// visitor imitates the visitor side.
type visitor struct {
	t  *testing.T
	pc *webrtc.PeerConnection

	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	mu     sync.Mutex
	ctrlCh chan protocol.Msg
	dataCh chan protocol.Frame

	openOnce sync.Once
	ready    chan struct{}
}

// proxyFunc lets a function implement the Proxy interface.
type proxyFunc func(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink)

func (f proxyFunc) Serve(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink) {
	f(ctx, req, body, sink)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// connect joins the agent session and the visitor (the manual signaling
// flow, automated).
func connect(t *testing.T, cfg Config) (*Session, *visitor) {
	t.Helper()

	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 1 << 20
	}
	if len(cfg.Targets) == 0 {
		cfg.Targets = []string{"default"}
	}
	if cfg.Auth == "" {
		cfg.Auth = protocol.AuthNone
	}

	sess, err := New(cfg)
	if err != nil {
		t.Fatalf("the session could not be set up: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	v := newVisitor(t)
	t.Cleanup(func() { _ = v.pc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The visitor is the offerer: it opens the channels and creates the
	// offer.
	offer := v.offer(ctx)
	answer, err := sess.Answer(ctx, offer)
	if err != nil {
		t.Fatalf("the answer could not be created: %v", err)
	}
	if err := v.acceptAnswer(answer); err != nil {
		t.Fatalf("the answer could not be applied: %v", err)
	}

	if err := sess.WaitReady(ctx, 20*time.Second); err != nil {
		t.Fatalf("the agent channels did not open: %v", err)
	}
	v.waitReady(t, 20*time.Second)

	return sess, v
}

func newVisitor(t *testing.T) *visitor {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("the visitor peer connection could not be built: %v", err)
	}

	v := &visitor{
		t:      t,
		pc:     pc,
		ctrlCh: make(chan protocol.Msg, 256),
		dataCh: make(chan protocol.Frame, 4096),
		ready:  make(chan struct{}),
	}

	// The VISITOR opens the channels (the phase 2 role swap).
	ordered := true
	ctrl, err := pc.CreateDataChannel("ctrl", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		t.Fatalf("the ctrl channel could not be opened: %v", err)
	}
	data, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		t.Fatalf("the data channel could not be opened: %v", err)
	}
	v.ctrl = ctrl
	v.data = data

	ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		msg, perr := protocol.ParseCtrl(string(m.Data))
		if perr != nil {
			t.Errorf("the visitor received an invalid ctrl: %v (%s)", perr, m.Data)
			return
		}
		v.ctrlCh <- msg
	})
	data.OnMessage(func(m webrtc.DataChannelMessage) {
		f, derr := protocol.DecodeFrame(m.Data)
		if derr != nil {
			t.Errorf("the visitor received an invalid frame: %v", derr)
			return
		}
		v.dataCh <- f
	})

	ctrl.OnOpen(v.maybeReady)
	data.OnOpen(v.maybeReady)

	return v
}

func (v *visitor) maybeReady() {
	v.mu.Lock()
	both := v.ctrl != nil && v.data != nil &&
		v.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
		v.data.ReadyState() == webrtc.DataChannelStateOpen
	v.mu.Unlock()
	if both {
		v.openOnce.Do(func() { close(v.ready) })
	}
}

// offer creates the visitor's offer and waits for ICE gathering.
func (v *visitor) offer(ctx context.Context) webrtc.SessionDescription {
	v.t.Helper()

	offer, err := v.pc.CreateOffer(nil)
	if err != nil {
		v.t.Fatalf("the offer could not be created: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(v.pc)
	if err := v.pc.SetLocalDescription(offer); err != nil {
		v.t.Fatalf("the local SDP could not be set: %v", err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		v.t.Fatalf("visitor ICE gathering: %v", ctx.Err())
	}
	local := v.pc.LocalDescription()
	if local == nil {
		v.t.Fatal("the visitor's local SDP is empty")
	}
	return *local
}

// acceptAnswer applies the agent's answer.
func (v *visitor) acceptAnswer(answer webrtc.SessionDescription) error {
	return v.pc.SetRemoteDescription(answer)
}

func (v *visitor) waitReady(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-v.ready:
	case <-time.After(d):
		t.Fatalf("the visitor channels did not open within %s", d)
	}
}

func (v *visitor) sendCtrl(m protocol.Msg) error {
	v.mu.Lock()
	dc := v.ctrl
	v.mu.Unlock()
	if dc == nil {
		return errors.New("there is no ctrl channel")
	}
	return dc.SendText(m.Canonical())
}

func (v *visitor) sendFrame(id, seq uint32, payload []byte) error {
	v.mu.Lock()
	dc := v.data
	v.mu.Unlock()
	if dc == nil {
		return errors.New("there is no data channel")
	}
	f, err := protocol.EncodeFrame(id, seq, payload)
	if err != nil {
		return err
	}
	return dc.Send(f)
}

// nextCtrl waits for the next ctrl message.
func (v *visitor) nextCtrl(t *testing.T, d time.Duration) protocol.Msg {
	t.Helper()
	select {
	case m := <-v.ctrlCh:
		return m
	case <-time.After(d):
		t.Fatalf("no ctrl message arrived within %s", d)
		return nil
	}
}

// collect gathers the response of the request with the given id: res,
// the body, end/abort.
//
// CAREFUL: `end` alone does not mean "the body is over". Because ctrl and
// data are separate SCTP streams, end can arrive before the frames; so
// data keeps being read until the frames/len counters in end are complete
// (protocol/README.md). The browser side applies the same rule.
type collected struct {
	res     *protocol.Res
	body    []byte
	ended   bool
	aborted string
	// How many frames arrived AFTER the end message; the proof that there
	// is no ordering.
	lateFrames int
}

func (v *visitor) collect(t *testing.T, id uint32, d time.Duration) collected {
	t.Helper()
	var out collected
	deadline := time.After(d)
	nextSeq := uint32(0)

	var expectFrames uint32
	var expectLen int64
	done := func() bool {
		return out.ended && nextSeq == expectFrames && int64(len(out.body)) == expectLen
	}

	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.Res:
				if msg.ID == id {
					r := msg
					out.res = &r
				}
			case protocol.End:
				if msg.ID == id {
					out.ended = true
					expectFrames = msg.Frames
					expectLen = msg.Len
					if done() {
						return out
					}
				}
			case protocol.Abort:
				if msg.ID == id {
					out.aborted = msg.Reason
					return out
				}
			case protocol.Hello, protocol.Ping, protocol.Pong:
				// yok say
			default:
				t.Errorf("unexpected ctrl message: %s", m.Type())
			}
		case f := <-v.dataCh:
			if f.ID != id {
				continue
			}
			if f.Seq != nextSeq {
				t.Errorf("unexpected seq: %d, expected %d", f.Seq, nextSeq)
			}
			nextSeq++
			out.body = append(out.body, f.Payload...)
			if out.ended {
				out.lateFrames++
			}
			if done() {
				return out
			}
		case <-deadline:
			t.Fatalf("the response for request %d did not complete within %s (res=%v ended=%v %d bytes, expected %d frames/%d bytes)",
				id, d, out.res != nil, out.ended, len(out.body), expectFrames, expectLen)
			return out
		}
	}
}
