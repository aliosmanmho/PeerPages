package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// The WebSocket bridge (phase 6).
//
// A path of its own, separate from HTTP: while internal/proxy strips
// headers it removes `upgrade` and `connection` in both directions, so the
// handshake CANNOT go through there. That is why a ws session has its own
// dispatch; no method was added to the Proxy interface (that interface is
// implemented in several places in the tests).
//
// The payload travels on the `data` channel, with a flag byte in the frame
// body (protocol/wsframe.go). ws ids share the SAME id space as HTTP
// requests; `ws-open` is what makes an id a ws one.

// WSConn is an established WebSocket connection (to the local target).
//
// Message based: WebSocket fragmentation stays below this interface, and
// the tunnel only carries whole messages.
type WSConn interface {
	// Read reads one message. binary false means a text message.
	Read(ctx context.Context) (binary bool, data []byte, err error)
	// Write sends one message.
	Write(ctx context.Context, binary bool, data []byte) error
	// Close closes with a close code and reason.
	Close(code int, reason string) error
}

// WSDialer turns a ws-open request into a local target.
//
// It is implemented by internal/wsbridge; the target URL comes ONLY from
// the allowlist (BRIEF principle 1).
type WSDialer interface {
	// Dial establishes the connection and returns the subprotocol the
	// server chose.
	Dial(ctx context.Context, open protocol.WSOpen) (WSConn, string, error)
}

// WSDialError is a dial error that carries a close code.
//
// It is defined here so the tunnel package does not have to import
// wsbridge (wsbridge imports this package).
type WSDialError struct {
	Code   int
	Reason string
	Err    error
}

func (e *WSDialError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("the ws connection could not be established (%d): %s", e.Code, e.Reason)
	}
	return fmt.Sprintf("the ws connection could not be established (%d): %s: %v", e.Code, e.Reason, e.Err)
}

func (e *WSDialError) Unwrap() error { return e.Err }

// WSCloseError carries the local server's own close code.
//
// Needed because the close code must reach the visitor's `onclose`
// EXACTLY as it was: application codes (4000-4999) carry meaning in
// libraries such as Socket.IO, and turning them into 1001 would destroy
// that information.
type WSCloseError struct {
	Code   int
	Reason string
}

func (e *WSCloseError) Error() string {
	return fmt.Sprintf("ws closed (%d): %s", e.Code, e.Reason)
}

// ws session limits.
const (
	// DefaultMaxWSMessage is the largest size of a ws message (in both
	// directions).
	//
	// The visitor is untrusted (BRIEF principle 4): an unbounded message
	// could blow up the agent's memory in a single request.
	DefaultMaxWSMessage = 1 << 20 // 1 MiB
	// DefaultMaxStreams is how many long-lived streams (a ws session or an
	// SSE response) may be open at once.
	//
	// Why it is separate from MaxConcurrent: a long-lived stream holds its
	// slot for its whole life, so 64 streams would starve ordinary
	// requests.
	DefaultMaxStreams = 8
	// wsQueueSlack is the base capacity of the per-ws-session event queue.
	wsQueueSlack = 64
)

// wsEvent is an event in a ws session's queue. Exactly one field is set.
type wsEvent struct {
	frame *protocol.Frame
	close *protocol.WSClose
}

// wsSession is a single WebSocket session over the tunnel.
type wsSession struct {
	id     uint32
	cancel context.CancelFunc

	q        chan wsEvent
	stop     chan struct{}
	stopOnce sync.Once

	sink *wsSink

	// closeMu guards the close bookkeeping: both pumps can finish at
	// once.
	closeMu sync.Mutex
	// closeSent means a ws-close went to the visitor (or came from it).
	closeSent bool
	// upCode/upReason is the close code to send to the local server.
	upCode   int
	upReason string
}

// push puts an event on the queue; if the queue is full it waits
// (receive-direction backpressure).
func (w *wsSession) push(ev wsEvent) bool {
	select {
	case w.q <- ev:
		return true
	case <-w.stop:
		return false
	}
}

func (w *wsSession) halt() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// sendClose sends a ws-close to the visitor, at most once per session.
func (w *wsSession) sendClose(s *Session, code int, reason string) {
	w.closeMu.Lock()
	already := w.closeSent
	w.closeSent = true
	if w.upCode == 0 {
		w.upCode, w.upReason = code, reason
	}
	w.closeMu.Unlock()

	if already {
		return
	}
	if err := s.sendCtrl(protocol.WSClose{
		ID: w.id, Code: code, Reason: truncateReason(reason),
	}); err != nil {
		s.log.Debug("ws-close could not be sent",
			slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
	}
}

// markClosedByVisitor: the visitor sent a ws-close, so we do not send one
// back.
func (w *wsSession) markClosedByVisitor(code int, reason string) {
	w.closeMu.Lock()
	w.closeSent = true
	w.upCode, w.upReason = code, reason
	w.closeMu.Unlock()
}

func (w *wsSession) upstreamClose() (int, string) {
	w.closeMu.Lock()
	defer w.closeMu.Unlock()
	if w.upCode == 0 {
		return protocol.WSCloseNormal, ""
	}
	return w.upCode, w.upReason
}

// truncateReason trims the close reason to the WebSocket limit;
// otherwise the other side rejects the message as bad-field.
func truncateReason(reason string) string {
	if len(reason) <= protocol.WSCloseReasonMax {
		return reason
	}
	return reason[:protocol.WSCloseReasonMax]
}

// maxWSMessage is the configured message limit.
func (s *Session) maxWSMessage() int64 {
	if s.cfg.MaxWSMessage > 0 {
		return s.cfg.MaxWSMessage
	}
	return DefaultMaxWSMessage
}

// onWSOpen handles the visitor's ws open request.
func (s *Session) onWSOpen(open protocol.WSOpen) {
	if s.cfg.WSDialer == nil {
		s.log.Debug("a ws-open arrived but the bridge is off", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSClosePolicy, "the ws bridge is off")
		return
	}

	// The stream budget: long-lived streams must not starve ordinary
	// requests.
	select {
	case s.streams <- struct{}{}:
	default:
		s.log.Warn("the stream budget is full, ws refused", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSCloseTryAgainLater, "the stream budget is full")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &wsSession{id: open.ID, cancel: cancel, stop: make(chan struct{})}
	w.sink = &wsSink{s: s, id: open.ID, buf: make([]byte, 0, protocol.MaxFrame)}

	s.reqMu.Lock()
	_, dupReq := s.reqs[open.ID]
	_, dupWS := s.socks[open.ID]
	if dupReq || dupWS {
		s.reqMu.Unlock()
		cancel()
		<-s.streams
		s.log.Warn("second session with the same id", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSCloseProtocol, "duplicate-id")
		return
	}

	// Frames that arrived before `ws-open` (there is no ordering between
	// the channels).
	early := s.early[open.ID]
	delete(s.early, open.ID)
	if early != nil && early.abortReason != "" {
		s.reqMu.Unlock()
		cancel()
		<-s.streams
		s.log.Warn("ws refused because of an early frame violation",
			slog.Uint64("id", uint64(open.ID)), slog.String("reason", early.abortReason))
		s.closeWSEarly(open.ID, protocol.WSCloseTooBig, early.abortReason)
		return
	}

	capacity := wsQueueSlack
	if early != nil {
		capacity += len(early.frames)
	}
	w.q = make(chan wsEvent, capacity)
	// Put the early frames on the queue FIRST: later frames line up behind
	// them and the seq order holds. Capacity is sufficient, so it does not
	// block.
	if early != nil {
		for i := range early.frames {
			f := early.frames[i]
			w.q <- wsEvent{frame: &f}
		}
	}

	s.socks[open.ID] = w
	s.reqMu.Unlock()

	s.log.Debug("ws open request",
		slog.Uint64("id", uint64(open.ID)),
		logging.PathAttr(open.Path, s.cfg.Verbose),
		slog.Int("proto", len(open.Protos)),
	)

	go s.runWS(ctx, w, open)
}

// closeWSEarly sends a ws-close without the session ever being
// established.
func (s *Session) closeWSEarly(id uint32, code int, reason string) {
	if err := s.sendCtrl(protocol.WSClose{
		ID: id, Code: code, Reason: truncateReason(reason),
	}); err != nil {
		s.log.Debug("ws-close could not be sent",
			slog.Uint64("id", uint64(id)), slog.String("err", err.Error()))
	}
}

// runWS runs the session's whole life: the dial, both pumps, cleanup.
func (s *Session) runWS(ctx context.Context, w *wsSession, open protocol.WSOpen) {
	defer func() {
		w.cancel()
		s.finishWS(w)
	}()

	conn, proto, err := s.cfg.WSDialer.Dial(ctx, open)
	if err != nil {
		code, reason := protocol.WSCloseInternal, "the local target was unreachable"
		var de *WSDialError
		if errors.As(err, &de) {
			code, reason = de.Code, de.Reason
		}
		s.log.Warn("the ws connection could not be established",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		w.sendClose(s, code, reason)
		return
	}

	if err := s.sendCtrl(protocol.WSOk{ID: open.ID, Proto: proto}); err != nil {
		s.log.Warn("ws-ok could not be sent",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		_ = conn.Close(protocol.WSCloseInternal, "")
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.pumpFromUpstream(ctx, w, conn)
	}()

	// The visitor -> local server direction runs on this goroutine.
	s.pumpToUpstream(ctx, w, conn)

	// Stop the read pump and close the local connection.
	w.cancel()
	code, reason := w.upstreamClose()
	_ = conn.Close(code, truncateReason(reason))
	wg.Wait()
}

// pumpToUpstream writes the frames coming from the visitor to the local
// server.
//
// Being a single goroutine matters: because frames and ws-close go through
// the same queue, the missing ordering between the channels is normalized
// here (same reasoning as runBodyWriter for request bodies).
func (s *Session) pumpToUpstream(ctx context.Context, w *wsSession, conn WSConn) {
	var (
		nextSeq  uint32
		partial  []byte
		binary   bool
		havePart bool
	)
	maxMsg := s.maxWSMessage()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case ev := <-w.q:
			if ev.close != nil {
				w.markClosedByVisitor(ev.close.Code, ev.close.Reason)
				return
			}
			f := ev.frame

			// The channel is ordered: an unexpected seq is a protocol
			// error.
			if f.Seq != nextSeq {
				s.log.Warn("the ws frame order is broken",
					slog.Uint64("id", uint64(w.id)),
					slog.Uint64("seq", uint64(f.Seq)),
					slog.Uint64("expected", uint64(nextSeq)))
				w.sendClose(s, protocol.WSCloseProtocol, "bad-seq")
				return
			}
			nextSeq++

			chunk, err := protocol.DecodeWSPayload(f.Payload)
			if err != nil {
				s.log.Warn("invalid ws frame",
					slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
				w.sendClose(s, protocol.WSCloseProtocol, "ws-bad-frame")
				return
			}
			if havePart && chunk.Binary != binary {
				s.log.Warn("the chunks of the ws message have different types",
					slog.Uint64("id", uint64(w.id)))
				w.sendClose(s, protocol.WSCloseProtocol, "ws-mixed-type")
				return
			}
			binary = chunk.Binary
			havePart = true

			if int64(len(partial))+int64(len(chunk.Payload)) > maxMsg {
				s.log.Warn("the ws message exceeded the limit",
					slog.Uint64("id", uint64(w.id)), slog.Int64("limit", maxMsg))
				w.sendClose(s, protocol.WSCloseTooBig, "the message is too large")
				return
			}
			partial = append(partial, chunk.Payload...)

			if !chunk.Fin {
				continue
			}
			// If the local server is slow we wait here: receive-direction
			// backpressure.
			if err := conn.Write(ctx, binary, partial); err != nil {
				if ctx.Err() == nil {
					s.log.Warn("the ws message could not be written to the local server",
						slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
					w.sendClose(s, protocol.WSCloseInternal, "could not write to the local server")
				}
				return
			}
			partial = partial[:0]
			havePart = false
		}
	}
}

// pumpFromUpstream writes the messages coming from the local server into
// the tunnel.
func (s *Session) pumpFromUpstream(ctx context.Context, w *wsSession, conn WSConn) {
	maxMsg := s.maxWSMessage()

	for {
		binary, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// The session is already closing: the other pump decided
				// the close code.
				return
			}
			code, reason := protocol.WSCloseGoingAway, "the local connection closed"
			var ce *WSCloseError
			switch {
			case errors.As(err, &ce):
				// The local server's own close code: let it through as is.
				code, reason = ce.Code, ce.Reason
			case errors.Is(err, ErrWSClosedNormally):
				code, reason = protocol.WSCloseNormal, ""
			}
			s.log.Debug("the ws local connection ended",
				slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
			w.sendClose(s, code, reason)
			w.halt()
			return
		}
		if int64(len(data)) > maxMsg {
			s.log.Warn("a ws message from the local server exceeded the limit",
				slog.Uint64("id", uint64(w.id)), slog.Int64("limit", maxMsg))
			w.sendClose(s, protocol.WSCloseTooBig, "the message is too large")
			w.halt()
			return
		}
		if err := w.sink.writeMessage(binary, data); err != nil {
			s.log.Warn("the ws message could not be written to the tunnel",
				slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
			w.halt()
			return
		}
	}
}

// ErrWSClosedNormally means the local server closed the connection
// cleanly (code 1000). internal/wsbridge returns it from Read.
var ErrWSClosedNormally = errors.New("the ws connection closed cleanly")

// onWSClose handles a ws-close message from the visitor.
func (s *Session) onWSClose(m protocol.WSClose) {
	s.reqMu.Lock()
	w, ok := s.socks[m.ID]
	s.reqMu.Unlock()
	if !ok {
		s.log.Debug("ws-close for an unknown id", slog.Uint64("id", uint64(m.ID)))
		return
	}
	cp := m
	if !w.push(wsEvent{close: &cp}) {
		// The pump already finished: the session is closing.
		w.markClosedByVisitor(m.Code, m.Reason)
		w.cancel()
	}
}

// routeWSFrame routes a frame to a ws session; it returns false when the
// owner is not a ws one.
func (s *Session) routeWSFrame(frame protocol.Frame) bool {
	s.reqMu.Lock()
	w, ok := s.socks[frame.ID]
	s.reqMu.Unlock()
	if !ok {
		return false
	}
	// If the queue is full we wait here: receive-direction backpressure.
	if !w.push(wsEvent{frame: &frame}) {
		s.log.Debug("the ws frame could not be routed", slog.Uint64("id", uint64(frame.ID)))
	}
	return true
}

// finishWS removes the session from the records and releases the stream
// slot.
func (s *Session) finishWS(w *wsSession) {
	s.reqMu.Lock()
	cur, ok := s.socks[w.id]
	if ok && cur == w {
		delete(s.socks, w.id)
	}
	s.reqMu.Unlock()

	w.halt()
	if ok && cur == w {
		<-s.streams
	}
}

// wsSink writes a ws session's messages to the data channel.
//
// Separate from the HTTP sink: there is no res/end state machine, and
// every message carries its own flags. seq increments across the session
// rather than resetting per message (protocol/README.md).
type wsSink struct {
	s  *Session
	id uint32

	mu      sync.Mutex
	nextSeq uint32
	buf     []byte
}

// writeMessage sends a ws message split into chunks.
//
// An empty message produces a frame too: without seeing the FIN flag the
// receiver cannot know the message is over.
func (k *wsSink) writeMessage(binary bool, data []byte) error {
	dc, flow := k.s.dataChannel()
	if dc == nil || flow == nil {
		return errors.New("the data channel is not open yet")
	}

	for off := 0; ; off += protocol.MaxWSPayload {
		end := off + protocol.MaxWSPayload
		if end > len(data) {
			end = len(data)
		}
		fin := end >= len(data)

		if err := flow.wait(context.Background()); err != nil {
			return err
		}
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return fmt.Errorf("the data channel is not open (%s)", dc.ReadyState())
		}

		k.mu.Lock()
		seq := k.nextSeq
		k.nextSeq++
		// First the outer frame header (without payload), then the ws
		// payload: that gives [id][seq][flags][chunk]. Because
		// AppendWSPayload bounds the chunk by MaxWSPayload, the total
		// never passes MaxPayload.
		frame, err := protocol.AppendFrame(k.buf[:0], k.id, seq, nil)
		if err == nil {
			frame, err = protocol.AppendWSPayload(frame, protocol.WSChunk{
				Binary:  binary,
				Fin:     fin,
				Payload: data[off:end],
			})
		}
		k.mu.Unlock()
		if err != nil {
			return err
		}

		if err := dc.Send(frame); err != nil {
			return fmt.Errorf("the ws frame could not be sent: %w", err)
		}
		if fin {
			return nil
		}
	}
}
