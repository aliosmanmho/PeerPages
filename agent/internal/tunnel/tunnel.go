// Package tunnel manages the agent-side WebRTC session and the ctrl/data
// channels.
//
// The agent is the ANSWERER: the visitor publishes the offer and the agent
// answers. The visitor opens the channels (`ctrl` and `data`) and the
// agent receives them through `OnDataChannel`.
//
// Why: on Nostr the agent cannot know the visitor's ephemeral pubkey in
// advance, so it cannot send the first message (it would not know who to
// encrypt to). The incoming side publishes the offer. The decision and its
// reasoning are in BRIEF section 8. The manual signaling path (`--manual`)
// uses the same direction: there is a single code path.
//
// The HTTP side of a request is NOT here: this package only speaks the
// protocol and hands the work to the Proxy interface (internal/proxy).
// That way the reverse proxy can be tested without WebRTC.
//
// # There is no ordering between the channels
//
// `ctrl` and `data` are two separate SCTP streams; each is ordered within
// itself, but there is no ordering guarantee BETWEEN them (measured in
// phase 1, protocol/README.md). That has two consequences, both handled
// here:
//
//  1. The body frames of a request can arrive BEFORE its `req` message.
//     Frames whose owner is not known yet are held in a short-lived buffer
//     (s.early) and processed in order once `req` arrives.
//  2. The `end` message can arrive BEFORE the body frames. So `end` alone
//     does not mean "the body is done": we wait until the `frames`/`len`
//     counters are complete.
//
// The body events of each request (frame / end / abort) are processed by a
// single goroutine from a single queue, so the order cannot get mixed up.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// ResponseSink writes the response to a request into the tunnel.
//
// Call order: SendRes -> Write* -> End. On failure, Abort.
type ResponseSink interface {
	// SendRes sends the response head (ctrl `res`).
	SendRes(status int, headers map[string]string) error
	// Write sends a piece of the body; it is split into 16 KiB frames and
	// backpressure is applied.
	Write(p []byte) error
	// End reports that the body is finished (ctrl `end`, with the
	// frames/len counters).
	End() error
	// Abort cancels the stream (ctrl `abort`).
	Abort(reason string) error
}

// Proxy handles a ctrl `req` message.
//
// body is the request body; it is nil when hasBody is false. The
// implementation writes the response to sink and calls sink.Abort on
// failure.
type Proxy interface {
	Serve(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink)
}

// Backpressure thresholds and buffer limits.
const (
	// bufferedHigh: above this much buffered on the data channel, sending
	// waits.
	bufferedHigh = 1 << 20 // 1 MiB
	// bufferedLow: once the buffer drops to this, sending resumes.
	bufferedLow = 256 << 10 // 256 KiB
	// bodyQueueSlack is the base capacity of the per-request body event
	// queue.
	bodyQueueSlack = 64
	// maxEarlyIDs is how many not-yet-owned ids we hold frames for.
	maxEarlyIDs = 64
	// earlyTTL is how long ownerless early frames stay in the buffer.
	earlyTTL = 30 * time.Second
)

// The idle check (BRIEF phase 3: the agent must not leak resources once
// the visitor is gone).
const (
	// DefaultIdleTimeout: after this much time with no traffic at all, the
	// session is closed.
	//
	// Why it is needed: we cannot rely on the WebRTC state change alone.
	// The notice the page sends on pagehide is BEST EFFORT — if the tab
	// crashes, is killed, or the operating system drops it, it never
	// arrives. And ICE moving to Disconnected can take far longer than 30
	// seconds, and may come back.
	DefaultIdleTimeout = 30 * time.Second
	// DefaultPingInterval is how often the agent sends a ctrl ping for
	// liveness.
	//
	// The visitor page answers an incoming ping with a pong
	// (web/src/tunnel.ts), so no protocol change is needed: lastSeen gets
	// refreshed even on a quiet but open tunnel.
	DefaultPingInterval = 10 * time.Second
	// DefaultDisconnectGrace is the time ICE is given to come back after
	// dropping to "Disconnected".
	//
	// Why we give it time: Disconnected is TEMPORARY, and pion often
	// returns to Connected on its own. Until phase 3 this state closed the
	// session IMMEDIATELY, and reported it as a clean close on top of
	// that; a brief network blip was killing the tunnel for nothing.
	DefaultDisconnectGrace = 10 * time.Second
	// resumeFactor is used for wake detection: if the gap between two
	// ticks is more than this multiple of the expected interval, the
	// process is assumed to have been suspended.
	resumeFactor = 3
)

// ErrIdle means the session was closed for being idle.
var ErrIdle = errors.New("the session went idle: the visitor is not responding")

// ErrDisconnected means the session was closed because the ICE connection
// never came back.
var ErrDisconnected = errors.New("the WebRTC connection dropped and did not come back")

// Config is the session configuration.
type Config struct {
	// ICEServers are the STUN/TURN servers used for ICE. When empty, only
	// host candidates are gathered (loopback tests).
	//
	// We take a ready-made list (ice.Config.Servers()), because TURN
	// credentials are per server entry and carrying credentials down to
	// this layer buys nothing.
	ICEServers []webrtc.ICEServer
	// ICEPolicy is the candidate policy; the zero value means "all".
	ICEPolicy webrtc.ICETransportPolicy
	// Targets are the target names announced in the hello message.
	Targets []string
	// Auth is the auth value in the hello message.
	Auth protocol.AuthMode
	// MaxConcurrent is the most requests handled at once.
	MaxConcurrent int
	// MaxBody is the largest request body accepted from the visitor.
	MaxBody int64
	// MaxStreams is how many long-lived streams (a ws session or an SSE
	// response) may be open at once; DefaultMaxStreams when 0.
	//
	// Separate from MaxConcurrent: a long-lived stream holds its slot for
	// its whole life, so with a single budget it would starve ordinary
	// requests.
	MaxStreams int
	// MaxWSMessage is the largest size of a ws message;
	// DefaultMaxWSMessage when 0.
	MaxWSMessage int64
	// Proxy is the layer that handles requests.
	Proxy Proxy
	// WSDialer is the WebSocket bridge; when nil the bridge is off and
	// ws-open requests are refused with ws-close 1008 (phase 6, off by
	// default).
	WSDialer WSDialer
	// Logger is mandatory.
	Logger *slog.Logger
	// Verbose turns off path masking in logs.
	Verbose bool
	// Trickle set to true means Answer does NOT WAIT for ICE gathering;
	// candidates flow one by one through OnLocalCandidate (the Nostr
	// path). When false, Answer returns the complete SDP, because with
	// manual signaling a single blob gets copied.
	Trickle bool
	// IdleTimeout is the idle limit; DefaultIdleTimeout when 0.
	IdleTimeout time.Duration
	// PingInterval is the liveness ping interval; DefaultPingInterval when
	// 0.
	//
	// Tests shorten these two to speed the check up.
	PingInterval time.Duration
	// DisconnectGrace is how long ICE is given to return from
	// Disconnected; DefaultDisconnectGrace when 0.
	DisconnectGrace time.Duration
}

func (c *Config) validate() error {
	switch {
	case len(c.Targets) == 0:
		return errors.New("tunnel: the target list is empty")
	case c.MaxConcurrent < 1:
		return errors.New("tunnel: MaxConcurrent must be at least 1")
	case c.MaxBody < 1:
		return errors.New("tunnel: MaxBody must be at least 1")
	case c.Proxy == nil:
		return errors.New("tunnel: Proxy nil")
	case c.Logger == nil:
		return errors.New("tunnel: Logger nil")
	}
	return nil
}

// bodyEvent is an event in a request's body queue. Exactly one field is
// set.
type bodyEvent struct {
	frame *protocol.Frame
	end   *protocol.End
	abort string
}

// inflight is a request being processed.
type inflight struct {
	id     uint32
	cancel context.CancelFunc

	// bodyW and q are only set for requests with a body.
	bodyW *io.PipeWriter
	q     chan bodyEvent

	stop     chan struct{}
	stopOnce sync.Once

	// onStream set to true means this request counts against the stream
	// budget instead of the ordinary request budget (an SSE response; see
	// reclassifyAsStream).
	onStream atomic.Bool
}

// push puts a body event on the queue. If the queue is full it waits —
// that is the receive-direction backpressure applied to the visitor. It
// returns false once the request is finished.
func (fl *inflight) push(ev bodyEvent) bool {
	if fl.q == nil {
		return false
	}
	select {
	case fl.q <- ev:
		return true
	case <-fl.stop:
		return false
	}
}

func (fl *inflight) halt() {
	fl.stopOnce.Do(func() { close(fl.stop) })
}

// earlyBody holds the body events accumulated for an id whose owner is
// not known yet.
type earlyBody struct {
	frames []protocol.Frame
	bytes  int64
	end    *protocol.End
	at     time.Time
	// abortReason: when non-empty, no body will be ACCEPTED for this id
	// and the request is cancelled outright once `req` arrives.
	//
	// Why we keep a flag: silently dropping the offending frame left the
	// request hanging. Because the body never arrived the proxy waited
	// forever and the visitor got no abort — a limit guard was turning
	// into a resource leak.
	abortReason string
}

// Session is a single visitor connection.
type Session struct {
	cfg Config
	log *slog.Logger

	pc   *webrtc.PeerConnection
	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	ctrlMu sync.Mutex // ctrl gonderimlerini sirala

	// chanMu guards the ctrl/data channels and flow: the channels arrive
	// ASYNCHRONOUSLY through OnDataChannel.
	chanMu sync.Mutex
	flow   *flowControl

	// candMu guards the buffer of remote candidates.
	//
	// Candidates can arrive before the remote SDP is set (on Nostr the
	// message order is not guaranteed). pion refuses AddICECandidate in
	// that case, so we buffer them and drain after Answer.
	candMu       sync.Mutex
	remoteSet    bool
	pendingCands []webrtc.ICECandidateInit

	onLocalCandidate func(webrtc.ICECandidateInit)

	// reqMu guards reqs, socks and the early buffer. One lock, because an
	// id is owned either by a request or by a ws session: the two have to
	// be told apart at a single decision point.
	reqMu sync.Mutex
	reqs  map[uint32]*inflight
	socks map[uint32]*wsSession
	early map[uint32]*earlyBody

	// sem is the ordinary request budget, streams the long-lived stream
	// budget.
	streams chan struct{}
	sem     chan struct{}
	ready   chan struct{}
	readyOn sync.Once

	closed  chan struct{}
	closeOn sync.Once
	// closeErr is why we closed. In an atomic box: while closeWith writes,
	// Err() and WaitReady() may read from other goroutines.
	closeErr atomic.Value // errBox

	// born is when the session was built. It carries the MONOTONIC
	// reading; lastSeen is kept as a duration relative to it, so wall
	// clock changes (an NTP step, a manual clock change) cannot break the
	// idle check.
	born time.Time
	// lastSeen is when we last heard anything from the visitor, as the
	// time elapsed since born (nanoseconds).
	// Atomic: the ctrl and data channels arrive on separate goroutines.
	lastSeen atomic.Int64

	// disconnMu guards the ICE Disconnected timer.
	disconnMu    sync.Mutex
	disconnTimer *time.Timer
}

// errBox wraps the value so atomic.Value stores a single type (so a nil
// error can be stored too).
type errBox struct{ err error }

// New builds the session; the channels open but no offer is produced
// yet.
func New(cfg Config) (*Session, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var rtcCfg webrtc.Configuration
	rtcCfg.ICETransportPolicy = cfg.ICEPolicy
	if len(cfg.ICEServers) > 0 {
		rtcCfg.ICEServers = cfg.ICEServers
	}
	pc, err := webrtc.NewPeerConnection(rtcCfg)
	if err != nil {
		return nil, fmt.Errorf("the peer connection could not be built: %w", err)
	}

	maxStreams := cfg.MaxStreams
	if maxStreams < 1 {
		maxStreams = DefaultMaxStreams
	}

	s := &Session{
		cfg:     cfg,
		log:     cfg.Logger,
		pc:      pc,
		reqs:    make(map[uint32]*inflight),
		socks:   make(map[uint32]*wsSession),
		early:   make(map[uint32]*earlyBody),
		sem:     make(chan struct{}, cfg.MaxConcurrent),
		streams: make(chan struct{}, maxStreams),
		ready:   make(chan struct{}),
		closed:  make(chan struct{}),
		born:    time.Now(),
	}
	s.touch()

	// The VISITOR opens the channels; we receive them.
	pc.OnDataChannel(s.onDataChannel)

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			// Gathering is done. We do NOT signal the end of candidates
			// to the other side (protocol/signaling.md): we simply stop
			// sending.
			s.log.Debug("ICE candidate gathering finished")
			return
		}
		s.chanMu.Lock()
		cb := s.onLocalCandidate
		s.chanMu.Unlock()
		if cb != nil {
			cb(c.ToJSON())
		}
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.log.Info("connection state", slog.String("state", st.String()))
		switch st {
		case webrtc.PeerConnectionStateFailed:
			s.cancelDisconnectTimer()
			s.closeWith(errors.New("the WebRTC connection failed"))
		case webrtc.PeerConnectionStateClosed:
			s.cancelDisconnectTimer()
			s.closeWith(nil)
		case webrtc.PeerConnectionStateDisconnected:
			// We do not close right away: Disconnected is temporary and
			// may come back.
			s.armDisconnectTimer()
		case webrtc.PeerConnectionStateConnected:
			if s.cancelDisconnectTimer() {
				s.log.Info("connection came back")
			}
		}
	})

	return s, nil
}

// onDataChannel receives the channels the visitor opened.
//
// Two channels are expected: `ctrl` and `data` (protocol/README.md). Any
// other label is ignored; the visitor is untrusted.
func (s *Session) onDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case "ctrl":
		s.chanMu.Lock()
		s.ctrl = dc
		s.chanMu.Unlock()
		dc.OnMessage(s.onCtrlMessage)
	case "data":
		dc.SetBufferedAmountLowThreshold(bufferedLow)
		s.chanMu.Lock()
		s.data = dc
		s.flow = newFlowControl(dc, bufferedHigh, bufferedLow)
		s.chanMu.Unlock()
		dc.OnMessage(s.onDataMessage)
	default:
		s.log.Warn("unexpected data channel", slog.String("label", dc.Label()))
		return
	}

	dc.OnOpen(s.onChannelOpen)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		s.onChannelOpen()
	}
}

func (s *Session) onChannelOpen() {
	s.chanMu.Lock()
	both := s.ctrl != nil && s.data != nil &&
		s.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
		s.data.ReadyState() == webrtc.DataChannelStateOpen
	s.chanMu.Unlock()
	if !both {
		return
	}

	s.readyOn.Do(func() {
		close(s.ready)
		s.log.Info("channels opened")
		s.touch()
		go s.watchIdle()
		if err := s.sendCtrl(protocol.Hello{
			V:       protocol.Version,
			Targets: s.cfg.Targets,
			Auth:    s.cfg.Auth,
		}); err != nil {
			s.log.Error("hello could not be sent", slog.String("err", err.Error()))
		}
	})
}

// OnLocalCandidate installs the callback for local ICE candidates.
//
// It must be called BEFORE Answer: candidates start flowing together with
// SetLocalDescription. With Trickle off (manual signaling) the candidates
// travel inside the complete SDP and this callback is unused.
func (s *Session) OnLocalCandidate(fn func(webrtc.ICECandidateInit)) {
	s.chanMu.Lock()
	s.onLocalCandidate = fn
	s.chanMu.Unlock()
}

// Answer applies the visitor's offer and produces an answer.
//
// With Trickle off it waits for ICE gathering to finish and returns the
// complete SDP with the candidates inside (manual signaling copies a
// single blob). With it on, it returns right away and the candidates flow
// through OnLocalCandidate.
func (s *Session) Answer(ctx context.Context, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	var zero webrtc.SessionDescription

	if offer.Type != webrtc.SDPTypeOffer {
		return zero, fmt.Errorf("expected an offer, got: %q", offer.Type.String())
	}
	if err := s.pc.SetRemoteDescription(offer); err != nil {
		return zero, fmt.Errorf("the remote SDP could not be set: %w", err)
	}

	// The remote SDP is ready: we can apply the buffered candidates now.
	s.flushPendingCandidates()

	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return zero, fmt.Errorf("the answer could not be created: %w", err)
	}

	gathered := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return zero, fmt.Errorf("the local SDP could not be set: %w", err)
	}

	if !s.cfg.Trickle {
		select {
		case <-gathered:
		case <-ctx.Done():
			return zero, fmt.Errorf("while waiting for ICE gathering: %w", ctx.Err())
		}
	}

	local := s.pc.LocalDescription()
	if local == nil {
		return zero, errors.New("the local SDP is empty")
	}
	return *local, nil
}

// AddRemoteCandidate adds an ICE candidate from the visitor.
//
// If the remote SDP is not set yet the candidate is buffered and applied
// after Answer. That is required because on Nostr the message order is not
// guaranteed.
func (s *Session) AddRemoteCandidate(c webrtc.ICECandidateInit) error {
	s.candMu.Lock()
	if !s.remoteSet {
		s.pendingCands = append(s.pendingCands, c)
		n := len(s.pendingCands)
		s.candMu.Unlock()
		s.log.Debug("candidate buffered (no remote SDP yet)", slog.Int("pending", n))
		return nil
	}
	s.candMu.Unlock()

	if err := s.pc.AddICECandidate(c); err != nil {
		return fmt.Errorf("the candidate could not be added: %w", err)
	}
	return nil
}

func (s *Session) flushPendingCandidates() {
	s.candMu.Lock()
	s.remoteSet = true
	pending := s.pendingCands
	s.pendingCands = nil
	s.candMu.Unlock()

	for _, c := range pending {
		if err := s.pc.AddICECandidate(c); err != nil {
			s.log.Warn("a buffered candidate could not be added", slog.String("err", err.Error()))
		}
	}
}

// Ready returns a channel that closes once both channels are open.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Done returns a channel that closes once the session closes.
func (s *Session) Done() <-chan struct{} { return s.closed }

// Err returns why the session closed (nil on a clean close).
func (s *Session) Err() error {
	if v, ok := s.closeErr.Load().(errBox); ok {
		return v.err
	}
	return nil
}

// Close closes the session.
func (s *Session) Close() error {
	s.closeWith(nil)
	return s.pc.Close()
}

func (s *Session) closeWith(err error) {
	s.closeOn.Do(func() {
		s.closeErr.Store(errBox{err: err})
		close(s.closed)

		s.reqMu.Lock()
		for _, r := range s.reqs {
			r.cancel()
			r.halt()
		}
		// Open ws sessions must go down too: otherwise a connection to the
		// local server and a stream slot leak.
		for _, w := range s.socks {
			w.cancel()
			w.halt()
		}
		s.reqs = map[uint32]*inflight{}
		s.socks = map[uint32]*wsSession{}
		s.early = map[uint32]*earlyBody{}
		s.reqMu.Unlock()
	})
}

// sendCtrl sends a ctrl message in canonical form.
func (s *Session) sendCtrl(m protocol.Msg) error {
	s.ctrlMu.Lock()
	defer s.ctrlMu.Unlock()

	// The channels arrive asynchronously through OnDataChannel, so they
	// may be nil.
	s.chanMu.Lock()
	ctrl := s.ctrl
	s.chanMu.Unlock()

	if ctrl == nil {
		return errors.New("the ctrl channel is not open yet")
	}
	if ctrl.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("the ctrl channel is not open (%s)", ctrl.ReadyState())
	}
	return ctrl.SendText(m.Canonical())
}

func (s *Session) onCtrlMessage(msg webrtc.DataChannelMessage) {
	// Even a malformed message proves liveness: the other side is there.
	s.touch()

	m, err := protocol.ParseCtrl(string(msg.Data))
	if err != nil {
		// The visitor is untrusted: a malformed message does not drop the
		// connection, it is logged.
		s.log.Warn("invalid ctrl message", slog.String("err", err.Error()))
		return
	}

	switch v := m.(type) {
	case protocol.Req:
		s.onReq(v)
	case protocol.End:
		s.onEnd(v)
	case protocol.Abort:
		s.onAbort(v.ID, v.Reason)
	case protocol.Ping:
		if err := s.sendCtrl(protocol.Pong{}); err != nil {
			s.log.Warn("pong could not be sent", slog.String("err", err.Error()))
		}
	case protocol.Pong:
		// There is no measurement side yet.
	case protocol.WSOpen:
		s.onWSOpen(v)
	case protocol.WSClose:
		s.onWSClose(v)
	default:
		// res and hello never come to the agent: a protocol violation.
		s.log.Warn("unexpected ctrl message", slog.String("t", m.Type()))
	}
}

func (s *Session) onReq(req protocol.Req) {
	// The concurrency limit (BRIEF principle 4).
	select {
	case s.sem <- struct{}{}:
	default:
		s.log.Warn("concurrent request limit exceeded", slog.Uint64("id", uint64(req.ID)))
		_ = s.sendCtrl(protocol.Abort{ID: req.ID, Reason: "too-many-requests"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	fl := &inflight{id: req.ID, cancel: cancel, stop: make(chan struct{})}

	var body io.Reader

	s.reqMu.Lock()
	if _, dup := s.reqs[req.ID]; dup {
		s.reqMu.Unlock()
		cancel()
		<-s.sem
		s.log.Warn("second request with the same id", slog.Uint64("id", uint64(req.ID)))
		_ = s.sendCtrl(protocol.Abort{ID: req.ID, Reason: "duplicate-id"})
		return
	}

	// Body events that arrived before `req`.
	early := s.early[req.ID]
	delete(s.early, req.ID)

	if early != nil && early.abortReason != "" {
		s.reqMu.Unlock()
		cancel()
		<-s.sem
		s.log.Warn("request refused because of an early body violation",
			slog.Uint64("id", uint64(req.ID)), slog.String("reason", early.abortReason))
		// The abort already went out when the frame arrived; we do not
		// repeat it here.
		return
	}

	switch {
	case req.HasBody:
		pr, pw := io.Pipe()
		fl.bodyW = pw
		body = pr

		capacity := bodyQueueSlack
		if early != nil {
			capacity += len(early.frames) + 1
		}
		fl.q = make(chan bodyEvent, capacity)

		// Put the early events on the queue FIRST: the real frames that
		// arrive after this line up behind them and the seq order holds.
		// Capacity is sufficient, so these writes do not block.
		if early != nil {
			for i := range early.frames {
				f := early.frames[i]
				fl.q <- bodyEvent{frame: &f}
			}
			if early.end != nil {
				fl.q <- bodyEvent{end: early.end}
			}
		}
	case early != nil:
		s.log.Warn("early frames arrived for a body-less request, dropped",
			slog.Uint64("id", uint64(req.ID)), slog.Int("frames", len(early.frames)))
	}

	s.reqs[req.ID] = fl
	s.reqMu.Unlock()

	s.log.Debug("request received",
		slog.Uint64("id", uint64(req.ID)),
		slog.String("method", req.Method),
		logging.PathAttr(req.Path, s.cfg.Verbose),
		slog.Bool("has_body", req.HasBody),
	)

	if fl.q != nil {
		go s.runBodyWriter(fl)
	}

	go func() {
		defer func() {
			cancel()
			s.finish(req.ID)
			s.releaseSlot(fl)
		}()
		sink := newSink(s, req.ID)
		s.cfg.Proxy.Serve(ctx, req, body, sink)
		// If the proxy did not close the stream, we do: the visitor must
		// not be left hanging.
		sink.ensureClosed()
	}()
}

// runBodyWriter processes ALL body events of a request in one order.
//
// Being a single goroutine matters: because frames, `end` and `abort` go
// through the same queue, the missing ordering between the channels is
// normalized here.
func (s *Session) runBodyWriter(fl *inflight) {
	var (
		nextSeq   uint32
		seen      int64
		endSeen   bool
		expFrames uint32
		expLen    int64
		finished  bool
	)

	closeBody := func(err error) {
		if finished {
			return
		}
		finished = true
		if err != nil {
			_ = fl.bodyW.CloseWithError(err)
			return
		}
		_ = fl.bodyW.Close()
	}
	defer func() { closeBody(errors.New("the request ended")) }()

	for {
		select {
		case <-fl.stop:
			return
		case ev := <-fl.q:
			if finished {
				// Keep draining the queue: senders must not block.
				continue
			}

			switch {
			case ev.abort != "":
				s.log.Debug("request cancelled",
					slog.Uint64("id", uint64(fl.id)), slog.String("reason", ev.abort))
				closeBody(fmt.Errorf("the visitor cancelled: %s", ev.abort))
				continue

			case ev.end != nil:
				endSeen = true
				expFrames = ev.end.Frames
				expLen = ev.end.Len

			case ev.frame != nil:
				// The channel is ordered: an unexpected seq is a protocol
				// violation, we do not silently reorder
				// (protocol/README.md).
				if ev.frame.Seq != nextSeq {
					s.log.Warn("unexpected seq",
						slog.Uint64("id", uint64(fl.id)),
						slog.Uint64("got", uint64(ev.frame.Seq)),
						slog.Uint64("expected", uint64(nextSeq)))
					closeBody(errors.New("protocol error: unexpected seq"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "bad-seq"})
					continue
				}
				nextSeq++
				seen += int64(len(ev.frame.Payload))

				// The body size limit (BRIEF principle 4).
				if seen > s.cfg.MaxBody {
					s.log.Warn("request body limit exceeded",
						slog.Uint64("id", uint64(fl.id)), slog.Int64("bytes", seen))
					closeBody(errors.New("the request body is too large"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "body-too-large"})
					continue
				}
				if _, err := fl.bodyW.Write(ev.frame.Payload); err != nil {
					s.log.Debug("the body could not be written",
						slog.Uint64("id", uint64(fl.id)), slog.String("err", err.Error()))
					closeBody(err)
					continue
				}
			}

			// Even once `end` has arrived, wait until the counters are
			// complete.
			if endSeen {
				switch {
				case nextSeq > expFrames || seen > expLen:
					s.log.Warn("the body length does not match what was reported",
						slog.Uint64("id", uint64(fl.id)),
						slog.Uint64("frames", uint64(nextSeq)),
						slog.Uint64("reported_frames", uint64(expFrames)),
						slog.Int64("bytes", seen),
						slog.Int64("reported_bytes", expLen))
					closeBody(errors.New("protocol error: the body length does not match"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "body-length-mismatch"})
				case nextSeq == expFrames && seen == expLen:
					closeBody(nil)
				}
			}
		}
	}
}

// onEnd routes the `end` message to its request; if the request has not
// arrived yet it buffers it (there is no ctrl/data ordering).
func (s *Session) onEnd(e protocol.End) {
	s.reqMu.Lock()
	fl, ok := s.reqs[e.ID]
	if !ok {
		s.bufferEarlyEndLocked(e)
		s.reqMu.Unlock()
		return
	}
	s.reqMu.Unlock()

	if !fl.push(bodyEvent{end: &e}) {
		s.log.Debug("end could not be routed", slog.Uint64("id", uint64(e.ID)))
	}
}

func (s *Session) onAbort(id uint32, reason string) {
	if reason == "" {
		reason = "client-abort"
	}
	s.reqMu.Lock()
	fl, ok := s.reqs[id]
	s.reqMu.Unlock()
	if !ok {
		return
	}
	// Body-less requests have no queue: only the context is cancelled.
	if !fl.push(bodyEvent{abort: reason}) {
		s.log.Debug("request cancelled (no body)",
			slog.Uint64("id", uint64(id)), slog.String("reason", reason))
	}
	fl.cancel()
}

// releaseSlot releases the budget slot the request holds.
func (s *Session) releaseSlot(fl *inflight) {
	if fl.onStream.Load() {
		<-s.streams
		return
	}
	<-s.sem
}

// reclassifyAsStream reclassifies a request as a long-lived stream: it
// releases the ordinary request slot and takes a stream slot.
//
// Why it is needed: an SSE response stays open for minutes or even hours
// and holds its MaxConcurrent slot for its whole life. 64 SSE requests
// would make the tunnel unusable for ordinary requests. We cannot know IN
// ADVANCE which response is long-lived; `content-type:
// text/event-stream` is the only honest hint that says so (called from
// sink.SendRes).
func (s *Session) reclassifyAsStream(id uint32) error {
	s.reqMu.Lock()
	fl, ok := s.reqs[id]
	s.reqMu.Unlock()
	if !ok {
		// The request is over; the limit has nothing left to mean.
		return nil
	}
	if fl.onStream.Load() {
		return nil
	}

	select {
	case s.streams <- struct{}{}:
	default:
		return errors.New("the stream budget is full")
	}
	fl.onStream.Store(true)
	<-s.sem
	return nil
}

func (s *Session) finish(id uint32) {
	s.reqMu.Lock()
	fl, ok := s.reqs[id]
	if ok {
		delete(s.reqs, id)
	}
	s.reqMu.Unlock()
	if ok {
		fl.halt()
	}
}

// onDataMessage routes incoming body frames to their request.
func (s *Session) onDataMessage(msg webrtc.DataChannelMessage) {
	s.touch()

	frame, err := protocol.DecodeFrame(msg.Data)
	if err != nil {
		s.log.Warn("invalid data frame", slog.String("err", err.Error()))
		return
	}

	s.reqMu.Lock()
	fl, ok := s.reqs[frame.ID]
	if !ok {
		_, isWS := s.socks[frame.ID]
		if !isWS {
			// `req`/`ws-open` may not have arrived yet: there is no
			// ordering between the channels.
			s.bufferEarlyFrameLocked(frame)
			s.reqMu.Unlock()
			return
		}
		s.reqMu.Unlock()
		// The ws path: the frame body is [1B flags][chunk]
		// (protocol/wsframe.go).
		s.routeWSFrame(frame)
		return
	}
	s.reqMu.Unlock()

	if fl.q == nil {
		s.log.Warn("a frame arrived for a body-less request", slog.Uint64("id", uint64(frame.ID)))
		return
	}
	// If the queue is full we wait here: receive-direction backpressure.
	if !fl.push(bodyEvent{frame: &frame}) {
		s.log.Debug("the frame could not be routed", slog.Uint64("id", uint64(frame.ID)))
	}
}

// bufferEarlyFrameLocked holds a frame whose owner is unknown in the
// short-lived buffer. It must be called while reqMu is held.
func (s *Session) bufferEarlyFrameLocked(frame protocol.Frame) {
	s.pruneEarlyLocked()

	e, ok := s.early[frame.ID]
	if !ok {
		if len(s.early) >= maxEarlyIDs {
			s.log.Warn("the early frame buffer is full, frame dropped",
				slog.Uint64("id", uint64(frame.ID)))
			return
		}
		e = &earlyBody{at: time.Now()}
		s.early[frame.ID] = e
	}
	if e.abortReason != "" {
		// This id was already cancelled; no need to keep counting.
		return
	}
	if e.bytes+int64(len(frame.Payload)) > s.cfg.MaxBody {
		s.log.Warn("the early body exceeded the limit, cancelling the request",
			slog.Uint64("id", uint64(frame.ID)), slog.Int64("limit", s.cfg.MaxBody))
		e.abortReason = "body-too-large"
		e.frames = nil
		e.bytes = 0
		// Tell the visitor WITHOUT WAITING: it creates the request record
		// before `req`, so the abort reaches its destination
		// (protocol/README.md).
		_ = s.sendCtrl(protocol.Abort{ID: frame.ID, Reason: "body-too-large"})
		return
	}
	e.frames = append(e.frames, frame)
	e.bytes += int64(len(frame.Payload))
}

func (s *Session) bufferEarlyEndLocked(end protocol.End) {
	s.pruneEarlyLocked()

	e, ok := s.early[end.ID]
	if !ok {
		if len(s.early) >= maxEarlyIDs {
			s.log.Debug("end for an unknown id, buffer full", slog.Uint64("id", uint64(end.ID)))
			return
		}
		e = &earlyBody{at: time.Now()}
		s.early[end.ID] = e
	}
	cp := end
	e.end = &cp
}

// pruneEarlyLocked clears unclaimed early events; otherwise the buffer
// leaks whenever `req` never arrives.
func (s *Session) pruneEarlyLocked() {
	if len(s.early) == 0 {
		return
	}
	cutoff := time.Now().Add(-earlyTTL)
	for id, e := range s.early {
		if e.at.Before(cutoff) {
			delete(s.early, id)
		}
	}
}

// dataChannel is the guarded accessor: the channel arrives
// asynchronously through OnDataChannel.
func (s *Session) dataChannel() (*webrtc.DataChannel, *flowControl) {
	s.chanMu.Lock()
	defer s.chanMu.Unlock()
	return s.data, s.flow
}

// Ping sends a ping to the visitor (latency measurement).
func (s *Session) Ping() error { return s.sendCtrl(protocol.Ping{}) }

// WaitReady waits for the channels to open.
func (s *Session) WaitReady(ctx context.Context, timeout time.Duration) error {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-s.ready:
		return nil
	case <-s.closed:
		if err := s.Err(); err != nil {
			return err
		}
		return errors.New("the session closed")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return fmt.Errorf("the channels did not open within %s", timeout)
	}
}

// touch marks that something was heard from the visitor.
//
// We measure time MONOTONICALLY: as the duration since born. Storing
// time.Now() as unix nanos and reading it back turned into a wall clock —
// a suspend, an NTP step or a manual clock change broke the idle check.
func (s *Session) touch() { s.lastSeen.Store(int64(time.Since(s.born))) }

// silence is how long it has been since we last heard from the visitor.
func (s *Session) silence() time.Duration {
	return time.Since(s.born) - time.Duration(s.lastSeen.Load())
}

// idleLimits returns the configured durations (or the defaults).
func (s *Session) idleLimits() (idle, ping time.Duration) {
	idle, ping = s.cfg.IdleTimeout, s.cfg.PingInterval
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	if ping <= 0 {
		ping = DefaultPingInterval
	}
	return idle, ping
}

// disconnectGrace is the time given to return from Disconnected.
func (s *Session) disconnectGrace() time.Duration {
	if s.cfg.DisconnectGrace > 0 {
		return s.cfg.DisconnectGrace
	}
	return DefaultDisconnectGrace
}

// armDisconnectTimer arms the timer that closes the session if ICE does
// not come back. If one is already armed it is left alone: the clock of
// the first drop is the one that counts.
func (s *Session) armDisconnectTimer() {
	grace := s.disconnectGrace()

	s.disconnMu.Lock()
	defer s.disconnMu.Unlock()
	if s.disconnTimer != nil {
		return
	}
	s.log.Info("connection dropped, waiting for it to come back",
		slog.Duration("duration", grace))
	s.disconnTimer = time.AfterFunc(grace, func() {
		select {
		case <-s.closed:
			return
		default:
		}
		s.log.Info("the connection did not come back, closing the session")
		s.closeWith(ErrDisconnected)
		_ = s.pc.Close()
	})
}

// cancelDisconnectTimer cancels a pending close (the connection came back
// or the session is closing for another reason). It returns true when
// there was a pending close.
func (s *Session) cancelDisconnectTimer() bool {
	s.disconnMu.Lock()
	t := s.disconnTimer
	s.disconnTimer = nil
	s.disconnMu.Unlock()

	return t != nil && t.Stop()
}

// watchIdle closes the session if the visitor falls silent.
//
// The AGENT sends the ping: on a quiet but open tunnel (the visitor leaves
// the page open and makes no request) that is the only source of traffic,
// and if the tab really is gone no pong comes back. That way the close
// depends on our own clock rather than on the WebRTC state machine.
//
// Sleep/wake: whether the monotonic clock counts suspended time depends on
// the operating system (Linux CLOCK_MONOTONIC does not, Windows does). So
// on top of the clock we look at the check's OWN lateness: if the gap
// between two ticks is far longer than expected, the process was
// suspended and the visitor does not count as "silent" — the session gets
// one more round of pings. That way the decision to close always rests on
// a ping that went unanswered.
func (s *Session) watchIdle() {
	idle, interval := s.idleLimits()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastTick := time.Since(s.born)

	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}

		now := time.Since(s.born)
		gap := now - lastTick
		lastTick = now

		since := s.silence()

		switch idleDecide(gap, since, idle, interval) {
		case idleClose:
			s.log.Info("session idle, closing",
				slog.Duration("silence", since.Round(time.Second)))
			s.closeWith(ErrIdle)
			_ = s.pc.Close()
			return

		case idleResume:
			s.log.Info("the check ran late, the process may have been suspended",
				slog.Duration("tick_interval", gap.Round(time.Second)),
				slog.Duration("silence", since.Round(time.Second)))
			// The silence counter is reset: the session gets one more
			// round of pings, and the sleep time does not count as "the
			// visitor is gone".
			s.touch()
			fallthrough

		case idlePing:
			if err := s.Ping(); err != nil {
				// The channel may be closed; the close path is already
				// running.
				s.log.Debug("the liveness ping could not be sent", slog.String("err", err.Error()))
			}
		}
	}
}

// idleAction is the decision watchIdle makes on one tick.
type idleAction int

const (
	// idlePing: everything is normal, send a liveness ping.
	idlePing idleAction = iota
	// idleResume: the check ran far later than expected (a suspend), so
	// reset the silence counter and try one more round.
	idleResume
	// idleClose: the visitor really is silent, close the session.
	idleClose
)

// idleDecide makes the per-tick decision.
//
// It is a separate function because the condition that matters is exactly
// where the two OVERLAP: after a long suspend the silence APPEARS to have
// passed the limit, but that does not show the visitor is gone — the
// decision to close must always rest on a ping that went unanswered.
func idleDecide(gap, silence, idle, interval time.Duration) idleAction {
	if gap > time.Duration(resumeFactor)*interval {
		return idleResume
	}
	if silence >= idle {
		return idleClose
	}
	return idlePing
}
