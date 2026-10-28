package tunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// sink writes a request's response to the ctrl and data channels.
//
// The state machine: new -> res sent -> (body chunks) -> closed. Calls in
// the wrong order return an error, so a bug in the proxy layer cannot
// break the protocol.
type sink struct {
	s  *Session
	id uint32

	mu      sync.Mutex
	resSent bool
	closed  bool
	// nextSeq is the next frame sequence, and also the number of frames
	// sent.
	nextSeq uint32
	// sentBytes is the total body bytes sent; reported in the end
	// message.
	sentBytes int64
	buf       []byte
	writeErr  error
}

func newSink(s *Session, id uint32) *sink {
	return &sink{
		s:   s,
		id:  id,
		buf: make([]byte, 0, protocol.MaxFrame),
	}
}

// SendRes sends the response head.
func (k *sink) SendRes(status int, headers map[string]string) error {
	k.mu.Lock()
	switch {
	case k.closed:
		k.mu.Unlock()
		return errors.New("the sink is closed")
	case k.resSent:
		k.mu.Unlock()
		return errors.New("res zaten yollandi")
	}
	k.resSent = true
	k.mu.Unlock()

	// A long-lived stream: switch budgets (see
	// Session.reclassifyAsStream).
	if isEventStream(headers["content-type"]) {
		if err := k.s.reclassifyAsStream(k.id); err != nil {
			// The visitor must see the reason: answering 502 silently, or
			// leaving it hanging, leads to a wrong diagnosis.
			_ = k.Abort("too-many-streams")
			return fmt.Errorf("the stream could not be accepted: %w", err)
		}
	}

	return k.s.sendCtrl(protocol.Res{ID: k.id, Status: status, Headers: headers})
}

// isEventStream says whether the content-type is SSE.
//
// Parameters (`; charset=utf-8`) and surrounding whitespace are ignored;
// the header names and values come from the local server rather than the
// visitor, but we still do not trust their shape.
func isEventStream(contentType string) bool {
	mime := contentType
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mime), "text/event-stream")
}

// Write sends a piece of the body split into 16 KiB frames.
//
// Backpressure is checked before every frame; if the buffer is full it
// waits.
func (k *sink) Write(p []byte) error {
	k.mu.Lock()
	switch {
	case k.closed:
		k.mu.Unlock()
		return errors.New("the sink is closed")
	case !k.resSent:
		k.mu.Unlock()
		return errors.New("govdeden once res yollanmali")
	case k.writeErr != nil:
		err := k.writeErr
		k.mu.Unlock()
		return err
	}
	k.mu.Unlock()

	// The channel and its flow control arrive asynchronously because the
	// visitor opens them; we take them once and use them for the whole
	// loop.
	data, flow := k.s.dataChannel()
	if data == nil || flow == nil {
		return k.fail(errors.New("the data channel is not open yet"))
	}

	for off := 0; off < len(p); off += protocol.MaxPayload {
		end := off + protocol.MaxPayload
		if end > len(p) {
			end = len(p)
		}

		if err := flow.wait(context.Background()); err != nil {
			return k.fail(err)
		}
		if data.ReadyState() != webrtc.DataChannelStateOpen {
			return k.fail(fmt.Errorf("the data channel is not open (%s)", data.ReadyState()))
		}

		k.mu.Lock()
		seq := k.nextSeq
		k.nextSeq++
		frame, err := protocol.AppendFrame(k.buf[:0], k.id, seq, p[off:end])
		k.mu.Unlock()
		if err != nil {
			return k.fail(err)
		}

		if err := data.Send(frame); err != nil {
			return k.fail(fmt.Errorf("the frame could not be sent: %w", err))
		}

		k.mu.Lock()
		k.sentBytes += int64(end - off)
		k.mu.Unlock()
	}
	return nil
}

// End reports that the body is finished.
func (k *sink) End() error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return errors.New("the sink is closed")
	}
	k.closed = true
	sent := k.resSent
	frames := k.nextSeq
	length := k.sentBytes
	k.mu.Unlock()

	if !sent {
		// end without res is meaningless: the visitor must not be left
		// hanging.
		return k.s.sendCtrl(protocol.Abort{ID: k.id, Reason: "no-response"})
	}
	// frames/len are MANDATORY: because the receiver has no ordering
	// between ctrl and data, these counters are how it learns the body is
	// over (protocol/README.md).
	return k.s.sendCtrl(protocol.End{ID: k.id, Frames: frames, Len: length})
}

// Abort cancels the stream.
func (k *sink) Abort(reason string) error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil
	}
	k.closed = true
	k.mu.Unlock()

	if reason == "" {
		reason = "agent-error"
	}
	return k.s.sendCtrl(protocol.Abort{ID: k.id, Reason: reason})
}

func (k *sink) fail(err error) error {
	k.mu.Lock()
	if k.writeErr == nil {
		k.writeErr = err
	}
	k.mu.Unlock()
	return err
}

// ensureClosed is called when Proxy returns without closing the stream.
func (k *sink) ensureClosed() {
	k.mu.Lock()
	closed := k.closed
	k.mu.Unlock()
	if closed {
		return
	}
	_ = k.Abort("agent-did-not-close")
}
