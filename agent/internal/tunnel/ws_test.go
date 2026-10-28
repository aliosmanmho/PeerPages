package tunnel

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// This file tests the ws bridge over a REAL WebRTC connection (the pion
// loopback in harness_test.go). The local server side is fake: the point
// is that the bridge speaks the protocol correctly, not that
// coder/websocket works.

type wsMsg struct {
	binary bool
	data   []byte
}

// fakeWSConn stands in for the local server.
type fakeWSConn struct {
	// toAgent: the messages coming from the local server to the agent.
	toAgent chan wsMsg
	// fromAgent: the messages the agent writes to the local server.
	fromAgent chan wsMsg

	closeOnce   sync.Once
	closed      chan struct{}
	closeCode   atomic.Int32
	closeReason atomic.Value

	// writeErr is the error Write will return (when not nil).
	writeErr error
}

func newFakeWSConn() *fakeWSConn {
	return &fakeWSConn{
		toAgent:   make(chan wsMsg, 64),
		fromAgent: make(chan wsMsg, 64),
		closed:    make(chan struct{}),
	}
}

func (f *fakeWSConn) Read(ctx context.Context) (bool, []byte, error) {
	select {
	case m := <-f.toAgent:
		return m.binary, m.data, nil
	case <-f.closed:
		return false, nil, ErrWSClosedNormally
	case <-ctx.Done():
		return false, nil, ctx.Err()
	}
}

func (f *fakeWSConn) Write(ctx context.Context, binary bool, data []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	select {
	case f.fromAgent <- wsMsg{binary: binary, data: cp}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeWSConn) Close(code int, reason string) error {
	f.closeOnce.Do(func() {
		f.closeCode.Store(int32(code))
		f.closeReason.Store(reason)
		close(f.closed)
	})
	return nil
}

func (f *fakeWSConn) waitClosed(t *testing.T, d time.Duration) int {
	t.Helper()
	select {
	case <-f.closed:
		return int(f.closeCode.Load())
	case <-time.After(d):
		t.Fatalf("the local connection did not close within %s", d)
		return 0
	}
}

// echo sends back every message the agent writes.
func (f *fakeWSConn) echo() {
	go func() {
		for {
			select {
			case m := <-f.fromAgent:
				select {
				case f.toAgent <- m:
				case <-f.closed:
					return
				}
			case <-f.closed:
				return
			}
		}
	}()
}

// fakeDialer implements the tunnel.WSDialer interface with a fake
// connection.
type fakeDialer struct {
	conn  *fakeWSConn
	proto string
	err   error

	mu    sync.Mutex
	opens []protocol.WSOpen
}

func (d *fakeDialer) Dial(_ context.Context, open protocol.WSOpen) (WSConn, string, error) {
	d.mu.Lock()
	d.opens = append(d.opens, open)
	d.mu.Unlock()
	if d.err != nil {
		return nil, "", d.err
	}
	return d.conn, d.proto, nil
}

func (d *fakeDialer) opened() []protocol.WSOpen {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]protocol.WSOpen, len(d.opens))
	copy(out, d.opens)
	return out
}

// wsReader is the visitor-side ws message assembler.
//
// The same job the browser side does (web/src/tunnel.ts): it joins the
// frames in seq order and completes the message on FIN.
type wsReader struct {
	id      uint32
	nextSeq uint32
	partial []byte
	binary  bool
}

func (r *wsReader) feed(t *testing.T, f protocol.Frame) (msg *wsMsg) {
	t.Helper()
	if f.Seq != r.nextSeq {
		t.Fatalf("the ws frame order is broken: %d, expected %d", f.Seq, r.nextSeq)
	}
	r.nextSeq++
	chunk, err := protocol.DecodeWSPayload(f.Payload)
	if err != nil {
		t.Fatalf("the ws frame could not be decoded: %v", err)
	}
	r.binary = chunk.Binary
	r.partial = append(r.partial, chunk.Payload...)
	if !chunk.Fin {
		return nil
	}
	out := wsMsg{binary: r.binary, data: r.partial}
	r.partial = nil
	return &out
}

// nextWSEvent waits for the visitor's next ws event: a message or a
// close.
func (v *visitor) nextWSEvent(t *testing.T, r *wsReader, d time.Duration) (*wsMsg, *protocol.WSClose) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.WSClose:
				if msg.ID == r.id {
					cp := msg
					return nil, &cp
				}
			case protocol.WSOk, protocol.Hello, protocol.Ping, protocol.Pong:
				// yok say
			default:
				t.Errorf("unexpected ctrl message: %s", m.Type())
			}
		case f := <-v.dataCh:
			if f.ID != r.id {
				continue
			}
			if msg := r.feed(t, f); msg != nil {
				return msg, nil
			}
		case <-deadline:
			t.Fatalf("no ws event arrived within %s", d)
			return nil, nil
		}
	}
}

// waitWSOk waits for the ws-ok message.
func (v *visitor) waitWSOk(t *testing.T, id uint32, d time.Duration) protocol.WSOk {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.WSOk:
				if msg.ID == id {
					return msg
				}
			case protocol.WSClose:
				if msg.ID == id {
					t.Fatalf("a ws-ok was expected, a ws-close arrived: %d %q", msg.Code, msg.Reason)
				}
			}
		case <-deadline:
			t.Fatalf("no ws-ok arrived within %s", d)
			return protocol.WSOk{}
		}
	}
}

// waitWSClose waits for the ws-close message (which may come without a
// ws-ok).
func (v *visitor) waitWSClose(t *testing.T, id uint32, d time.Duration) protocol.WSClose {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-v.ctrlCh:
			if msg, ok := m.(protocol.WSClose); ok && msg.ID == id {
				return msg
			}
		case <-deadline:
			t.Fatalf("no ws-close arrived within %s", d)
			return protocol.WSClose{}
		}
	}
}

// sendWSMessage sends a ws message split into chunks.
func (v *visitor) sendWSMessage(t *testing.T, id uint32, seq *uint32, binary bool, data []byte) {
	t.Helper()
	for off := 0; ; off += protocol.MaxWSPayload {
		end := off + protocol.MaxWSPayload
		if end > len(data) {
			end = len(data)
		}
		fin := end >= len(data)
		payload, err := protocol.EncodeWSPayload(protocol.WSChunk{
			Binary: binary, Fin: fin, Payload: data[off:end],
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.sendFrame(id, *seq, payload); err != nil {
			t.Fatal(err)
		}
		*seq++
		if fin {
			return
		}
	}
}

// noopProxy: the HTTP side is unused in the ws tests, but Config.Proxy
// is mandatory.
func noopProxy() proxyFunc {
	return func(_ context.Context, _ protocol.Req, _ io.Reader, _ ResponseSink) {}
}

func TestWSEchoBothDirections(t *testing.T) {
	conn := newFakeWSConn()
	conn.echo()
	dialer := &fakeDialer{conn: conn, proto: "vite-hmr"}

	sess, v := connect(t, Config{Proxy: noopProxy(), WSDialer: dialer})
	defer func() { _ = sess.Close() }()

	const id = uint32(7)
	if err := v.sendCtrl(protocol.WSOpen{
		ID: id, Path: "/socket?x=1", Protos: []string{"vite-hmr"},
	}); err != nil {
		t.Fatal(err)
	}

	ok := v.waitWSOk(t, id, 5*time.Second)
	if ok.Proto != "vite-hmr" {
		t.Errorf("the subprotocol is %q, expected %q", ok.Proto, "vite-hmr")
	}
	// Check the target path reaches the dialer as it was (the host is
	// NEVER derived from the path; the allowlist resolves it).
	if opens := dialer.opened(); len(opens) != 1 || opens[0].Path != "/socket?x=1" {
		t.Errorf("the ws-open that reached the dialer: %+v", opens)
	}

	r := &wsReader{id: id}
	var seq uint32

	v.sendWSMessage(t, id, &seq, false, []byte("merhaba"))
	msg, cl := v.nextWSEvent(t, r, 5*time.Second)
	if cl != nil {
		t.Fatalf("unexpected close: %d %q", cl.Code, cl.Reason)
	}
	if msg.binary || string(msg.data) != "merhaba" {
		t.Errorf("the text echo: binary=%v %q", msg.binary, msg.data)
	}

	v.sendWSMessage(t, id, &seq, true, []byte{0, 255, 16})
	msg, cl = v.nextWSEvent(t, r, 5*time.Second)
	if cl != nil {
		t.Fatalf("unexpected close: %d %q", cl.Code, cl.Reason)
	}
	if !msg.binary || len(msg.data) != 3 || msg.data[1] != 255 {
		t.Errorf("the binary echo: binary=%v %v", msg.binary, msg.data)
	}

	// An empty message has to pass too: the FIN flag marks its end.
	v.sendWSMessage(t, id, &seq, false, nil)
	msg, cl = v.nextWSEvent(t, r, 5*time.Second)
	if cl != nil {
		t.Fatalf("unexpected close: %d %q", cl.Code, cl.Reason)
	}
	if len(msg.data) != 0 {
		t.Errorf("an empty message was expected, %d bytes arrived", len(msg.data))
	}
}
