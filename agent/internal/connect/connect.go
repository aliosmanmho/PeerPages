// Package connect runs the agent's flow for accepting visitors over
// Nostr.
//
// Who does what:
//   - internal/nostr      : the relay transport (publish / subscribe)
//   - internal/signaling  : the message format and the secret layer
//   - internal/tunnel     : the WebRTC session
//   - THIS PACKAGE        : the flow that joins them and the visitor rules
//
// The flow (protocol/signaling.md): the visitor publishes an offer, the
// agent answers, the two sides exchange candidates, and signaling ends
// once WebRTC is up.
package connect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Timings.
const (
	// SessionSetupTimeout is the time the tunnel is given to open after an
	// offer arrives (BRIEF phase 2: 30 s).
	SessionSetupTimeout = 30 * time.Second
	// PublishTimeout bounds publishing a single signaling message.
	PublishTimeout = 15 * time.Second
	// SubscribeBacklog is how far back a subscription looks. Kept short:
	// ephemeral events are not stored, so looking far into the past is
	// pointless.
	SubscribeBacklog = time.Minute
)

// Subscription supervision (phase 4).
const (
	// ResubscribeMin is the shortest wait before resubscribing.
	ResubscribeMin = time.Second
	// ResubscribeMax is the upper bound of the backoff.
	ResubscribeMax = time.Minute
	// epochHealthy: a subscription that lives longer than this counts as
	// healthy and the backoff resets.
	epochHealthy = time.Minute
	// healthInterval is how often relay health is polled.
	healthInterval = 5 * time.Second
	// downChecksBeforeResubscribe: if no relay is connected for this many
	// polls, the subscription is rebuilt from scratch (which forces a
	// reconnect).
	downChecksBeforeResubscribe = 3
	// resumeFactor: if the gap between two polls is more than this
	// multiple of the expected one, the process is assumed to have been
	// suspended.
	resumeFactor = 3
)

// ProxyFactory builds a Proxy for the given target.
//
// The target varies per session: the visitor states it in the URL
// (`#npub1.../admin`) and it rides in the offer
// (protocol/signaling.md). targetName is always one of the names in
// Config.Targets — the validation happens on the caller's side.
type ProxyFactory func(targetName string) (tunnel.Proxy, error)

// WSDialerFactory builds the WebSocket bridge for the given target.
//
// Same pattern and same reasoning as ProxyFactory: the target varies per
// session. When nil the bridge is off and ws-open requests are refused
// (phase 6).
type WSDialerFactory func(targetName string) (tunnel.WSDialer, error)

// ICEFactory returns the ICE server list valid at the time a session is
// built.
//
// Cloudflare TURN credentials are short-lived and refreshed in the
// background, so the list is asked for per session rather than read once
// at startup.
type ICEFactory func() (servers []webrtc.ICEServer, policy webrtc.ICETransportPolicy)

// Config is the configuration of the accept flow.
type Config struct {
	// Identity is the agent's Nostr identity.
	Identity identity.Identity
	// Pool is the relay pool (an interface, so tests can pass a fake).
	Pool Pool
	// Sealer is the shared-secret layer; nil means there is no secret.
	Sealer *signaling.Sealer
	// NewProxy builds the proxy for each session.
	NewProxy ProxyFactory
	// NewWSDialer builds the WebSocket bridge for each session; nil means
	// the bridge is off (phase 6, the default).
	NewWSDialer WSDialerFactory
	// Targets are the names of the published targets: they are announced
	// in the hello message and the target name in an offer is validated
	// against them.
	Targets []string
	// ICE provides the ICE server list and policy for each session.
	//
	// A FUNCTION rather than a static list: Cloudflare TURN credentials
	// expire, so every new session must get the list that is valid right
	// then (same pattern as ProxyFactory). When nil, no ICE server is used
	// (loopback tests).
	ICE ICEFactory
	// MaxSessions is how many visitors are accepted at once.
	MaxSessions int
	// MaxConcurrent is the concurrent request limit per session.
	MaxConcurrent int
	// MaxBody is the request body limit.
	MaxBody int64
	// MaxStreams is the long-lived stream limit per session (ws + SSE);
	// tunnel.DefaultMaxStreams when 0.
	MaxStreams int
	// MaxWSMessage is the limit for a ws message;
	// tunnel.DefaultMaxWSMessage when 0.
	MaxWSMessage int64
	// Logger is mandatory.
	Logger *slog.Logger
	// Verbose turns off path masking in logs.
	Verbose bool

	// now injects the clock for tests; time.Now when nil.
	now func() time.Time
	// healthInterval is how often relay health is polled; the package
	// healthInterval when 0.
	healthInterval time.Duration
	// resubscribeMin is the base of the resubscribe backoff;
	// ResubscribeMin when 0. Tests shorten it to speed the supervision
	// up.
	resubscribeMin time.Duration
}

// Pool is the face of internal/nostr.Pool this package needs.
//
// It is taken as an interface so tests can pass a fake pool.
type Pool interface {
	Publish(ctx context.Context, ev gonostr.Event) (nostr.PublishReport, error)
	Subscribe(ctx context.Context, filter gonostr.Filter) <-chan gonostr.RelayEvent
}

func (c *Config) validate() error {
	switch {
	case c.Identity.SecretKey == "" || c.Identity.PublicKey == "":
		return errors.New("connect: the identity is incomplete")
	case c.Pool == nil:
		return errors.New("connect: Pool nil")
	case c.NewProxy == nil:
		return errors.New("connect: NewProxy nil")
	case len(c.Targets) == 0:
		return errors.New("connect: the target list is empty")
	case c.MaxSessions < 1:
		return errors.New("connect: MaxSessions must be at least 1")
	case c.MaxConcurrent < 1:
		return errors.New("connect: MaxConcurrent must be at least 1")
	case c.MaxBody < 1:
		return errors.New("connect: MaxBody must be at least 1")
	case c.Logger == nil:
		return errors.New("connect: Logger nil")
	}
	return nil
}

// Acceptor turns incoming offers into tunnel sessions.
type Acceptor struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	seen  *seenSet
	rate  *rateLimiter
	codec codec

	mu       sync.Mutex
	sessions map[string]*visitorSession // sid -> oturum

	// ready closes once the subscription is up. The caller must wait for
	// it before saying "ready": because ephemeral events are not stored,
	// an offer that arrives before we listen is lost.
	ready   chan struct{}
	readyOn sync.Once
}

// codec reduces sealed and plain encoding to one interface.
type codec interface {
	Encode(m signaling.Msg) (string, error)
	Decode(payload string) (signaling.Msg, error)
}

// visitorSession is a single visitor's session.
type visitorSession struct {
	sid     string
	peerPub string
	sess    *tunnel.Session
	cancel  context.CancelFunc
	created time.Time
}

// New builds the accept flow.
func New(cfg Config) (*Acceptor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}

	return &Acceptor{
		cfg:  cfg,
		log:  cfg.Logger,
		now:  now,
		seen: newSeenSet(SeenTTL, now),
		rate: newRateLimiter(RateWindow, RateBurst, now),
		// A nil *Sealer is valid too: Seal/Open implement the plain JSON
		// path with a nil receiver (secret.go). The interface value itself
		// is not nil, so the calls go through safely.
		codec:    cfg.Sealer,
		sessions: map[string]*visitorSession{},
		ready:    make(chan struct{}),
	}, nil
}

// Run subscribes and handles incoming events until ctx ends.
//
// The subscription IS SUPERVISED: it is rebuilt if it closes, if every
// relay goes down and stays down, or if the process was suspended. This
// function used to return an error when the subscription channel closed
// and the agent process exited — whereas what the user wants is for the
// agent to recover on its own when the laptop wakes up.
func (a *Acceptor) Run(ctx context.Context) error {
	npub, err := a.cfg.Identity.Npub()
	if err != nil {
		return err
	}

	a.log.Info("listening for signaling",
		slog.String("peer_id", identity.ShortenNpub(npub)),
		slog.Bool("secret_required", a.cfg.Sealer != nil),
	)

	_, backoffMin := a.superviseLimits()
	backoff := backoffMin
	for {
		started := a.now()
		reason := a.runEpoch(ctx)

		if ctx.Err() != nil {
			a.closeAll()
			return nil
		}
		if reason == nil {
			reason = errSubClosed
		}

		// A long-lived subscription counts as healthy: the backoff
		// resets.
		if a.now().Sub(started) >= epochHealthy {
			backoff = backoffMin
		}

		wait := backoff
		if errors.Is(reason, errResume) {
			// A wake: waiting is pointless, the visitors are arriving
			// now.
			wait = 0
		}
		a.log.Warn("renewing the signaling subscription",
			slog.String("reason", reason.Error()),
			slog.Duration("wait", wait))

		if wait > 0 {
			select {
			case <-ctx.Done():
				a.closeAll()
				return nil
			case <-time.After(wait):
			}
			if backoff < ResubscribeMax {
				backoff = min(2*backoff, ResubscribeMax)
			}
		}
	}
}

// Reasons for supervising the subscription.
var (
	errSubClosed = errors.New("the relay subscriptions closed")
	errNoRelay   = errors.New("no connection to any relay is left")
	errResume    = errors.New("the process appears to have been suspended")
)

// runEpoch runs a single subscription epoch; it returns the reason when
// one needs renewing. It returns nil once ctx is done.
func (a *Acceptor) runEpoch(ctx context.Context) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The filter is rebuilt EVERY epoch: because ephemeral events are not
	// stored, subscribing with an old `since` buys nothing.
	filter := nostr.SignalingFilter(a.cfg.Identity.PublicKey, a.now().Add(-SubscribeBacklog))
	events := a.cfg.Pool.Subscribe(subCtx, filter)

	// The subscription is up: we can catch incoming offers now.
	a.readyOn.Do(func() { close(a.ready) })

	health, _ := a.superviseLimits()
	ticker := time.NewTicker(health)
	defer ticker.Stop()

	lastTick := a.now()
	downChecks := 0
	lastHealthy := -1

	for {
		select {
		case <-ctx.Done():
			a.closeAll()
			return nil

		case ev, ok := <-events:
			if !ok {
				return errSubClosed
			}
			a.onEvent(ctx, ev.Event)

		case <-ticker.C:
			now := a.now()
			gap := now.Sub(lastTick)
			lastTick = now

			// The same wake detection as in tunnel: if the check ran far
			// later than expected, the process was suspended. The relay
			// connections may have died quietly in the meantime, and
			// go-nostr's own backoff can climb to 5 minutes — making a
			// visitor wait that long is not acceptable.
			if gap > time.Duration(resumeFactor)*health {
				return errResume
			}

			healthy, total := a.relayHealth()
			if total == 0 {
				continue // havuz saglik bildirmiyor (test sahtesi)
			}
			if healthy != lastHealthy {
				lastHealthy = healthy
				a.logRelayHealth(healthy, total)
			}
			if healthy == 0 {
				downChecks++
				if downChecks >= downChecksBeforeResubscribe {
					return errNoRelay
				}
			} else {
				downChecks = 0
			}
		}
	}
}

// HealthReporter is a pool that can report its health.
//
// A separate interface: the fake pools in the tests implement the Pool
// interface and do not care about health.
type HealthReporter interface {
	Health() []nostr.RelayHealth
}

// relayHealth returns the connected and total relay counts, or (0, 0)
// when the pool does not report them.
func (a *Acceptor) relayHealth() (healthy, total int) {
	hr, ok := a.cfg.Pool.(HealthReporter)
	if !ok {
		return 0, 0
	}
	list := hr.Health()
	for _, h := range list {
		if h.Connected {
			healthy++
		}
	}
	return healthy, len(list)
}

func (a *Acceptor) logRelayHealth(healthy, total int) {
	switch healthy {
	case 0:
		a.log.Warn("no connection to any relay: visitors cannot reach you right now",
			slog.Int("total", total))
	case total:
		a.log.Info("every relay is connected", slog.Int("total", total))
	default:
		a.log.Info("the relay connections changed",
			slog.Int("connected", healthy), slog.Int("total", total))
	}
}

// RelayStatus returns the connected/total relay counts for the status
// line.
func (a *Acceptor) RelayStatus() (healthy, total int) { return a.relayHealth() }

// superviseLimits returns the supervision durations (the configured ones
// when set).
func (a *Acceptor) superviseLimits() (health, backoffMin time.Duration) {
	health, backoffMin = a.cfg.healthInterval, a.cfg.resubscribeMin
	if health <= 0 {
		health = healthInterval
	}
	if backoffMin <= 0 {
		backoffMin = ResubscribeMin
	}
	return health, backoffMin
}

// Ready returns a channel that closes once the subscription is up.
//
// Wait for it before printing the "ready" message: otherwise a visitor who
// opens the URL right away loses their offer (ephemeral events are not
// stored).
func (a *Acceptor) Ready() <-chan struct{} { return a.ready }

// SessionCount is the number of open sessions.
func (a *Acceptor) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// onEvent handles an incoming signaling event.
//
// The visitor is untrusted: replay, clock skew, rate limiting and content
// validation, in that order (guard.go, protocol/signaling.md).
func (a *Acceptor) onEvent(ctx context.Context, ev *gonostr.Event) {
	if ev == nil {
		return
	}
	short := nostr.ShortPubKey(ev.PubKey)

	// Ignore our own publish coming back.
	if ev.PubKey == a.cfg.Identity.PublicKey {
		return
	}

	// Replay: the same event id is never handled twice.
	if a.seen.seen(ev.ID) {
		a.log.Debug("duplicate event skipped", slog.String("peer", short))
		return
	}

	// Clock skew: events too old or in the future are rejected.
	if !freshEnough(ev.CreatedAt.Time(), a.now(), MaxClockSkew) {
		a.log.Warn("the timestamp is outside the accepted window",
			slog.String("peer", short),
			slog.Time("created_at", ev.CreatedAt.Time()))
		return
	}

	// Rate limiting: a per-pubkey allowance within a window.
	if !a.rate.allow(ev.PubKey) {
		a.log.Warn("rate limit exceeded, event skipped", slog.String("peer", short))
		return
	}

	payload, err := nostr.Decrypt(a.cfg.Identity.SecretKey, ev.PubKey, ev.Content)
	if err != nil {
		a.log.Warn("NIP-44 could not be decrypted",
			slog.String("peer", short), slog.String("err", err.Error()))
		return
	}

	msg, err := a.codec.Decode(payload)
	if err != nil {
		// Messages that arrive with the wrong secret die here; someone who
		// knows the peer-id but not the secret cannot connect.
		a.log.Warn("the signaling message could not be decoded",
			slog.String("peer", short), slog.String("err", err.Error()))
		return
	}

	switch msg.Type {
	case signaling.TypeOffer:
		a.onOffer(ctx, ev.PubKey, msg)
	case signaling.TypeCandidate:
		a.onCandidate(ev.PubKey, msg)
	case signaling.TypeBye:
		a.onBye(ev.PubKey, msg)
	case signaling.TypeAnswer:
		// The agent does not RECEIVE answers, it produces them: a protocol
		// violation.
		a.log.Warn("an answer arrived at the agent, a protocol violation", slog.String("peer", short))
	}
}

func (a *Acceptor) onOffer(ctx context.Context, peerPub string, msg signaling.Msg) {
	short := nostr.ShortPubKey(peerPub)

	// The target first: it is the cheapest check and if it fails we never
	// enter WebRTC. We do not fall back to the default silently — the
	// visitor may not notice they are browsing the wrong project (BRIEF
	// section 8).
	targetName, ok := a.resolveTarget(msg.Target)
	if !ok {
		a.log.Warn("unknown target, offer refused",
			slog.String("peer", short), slog.String("target", msg.Target))
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "unknown-target",
		})
		return
	}

	a.mu.Lock()
	if _, dup := a.sessions[msg.SID]; dup {
		a.mu.Unlock()
		a.log.Debug("a second offer with the same sid was skipped",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	if len(a.sessions) >= a.cfg.MaxSessions {
		a.mu.Unlock()
		a.log.Warn("session limit exceeded, offer refused",
			slog.String("peer", short), slog.Int("limit", a.cfg.MaxSessions))
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "too-many-sessions",
		})
		return
	}
	// Reserve the slot: we put the record in before releasing the lock, so
	// a second offer arriving at the same moment cannot pass the limit.
	placeholder := &visitorSession{sid: msg.SID, peerPub: peerPub, created: a.now()}
	a.sessions[msg.SID] = placeholder
	a.mu.Unlock()

	a.log.Info("offer received",
		slog.String("peer", short), slog.String("sid", msg.SID),
		slog.String("target", targetName))

	if err := a.startSession(ctx, peerPub, msg, targetName, placeholder); err != nil {
		a.log.Warn("the session could not be set up",
			slog.String("peer", short), slog.String("err", err.Error()))
		a.dropSession(msg.SID)
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "setup-failed",
		})
	}
}

// resolveTarget maps the target name in an offer to one of the published
// targets.
//
// An empty name means "not specified": default if there is one, otherwise
// the alphabetically first target (Targets is the output of
// target.Set.Names() and is sorted).
func (a *Acceptor) resolveTarget(name string) (string, bool) {
	if name == "" {
		for _, n := range a.cfg.Targets {
			if n == target.DefaultName {
				return n, true
			}
		}
		return a.cfg.Targets[0], true
	}
	for _, n := range a.cfg.Targets {
		if n == name {
			return n, true
		}
	}
	return "", false
}

func (a *Acceptor) startSession(
	ctx context.Context, peerPub string, offerMsg signaling.Msg,
	targetName string, slot *visitorSession,
) error {
	px, err := a.cfg.NewProxy(targetName)
	if err != nil {
		return fmt.Errorf("the proxy could not be built: %w", err)
	}

	// When the bridge is off (the default) it stays nil and ws-open is
	// refused.
	var wsDialer tunnel.WSDialer
	if a.cfg.NewWSDialer != nil {
		wsDialer, err = a.cfg.NewWSDialer(targetName)
		if err != nil {
			return fmt.Errorf("the ws bridge could not be built: %w", err)
		}
	}

	iceServers, icePolicy := a.iceConfig()

	sess, err := tunnel.New(tunnel.Config{
		ICEServers:    iceServers,
		ICEPolicy:     icePolicy,
		Targets:       a.cfg.Targets,
		Auth:          a.authMode(),
		MaxConcurrent: a.cfg.MaxConcurrent,
		MaxBody:       a.cfg.MaxBody,
		MaxStreams:    a.cfg.MaxStreams,
		MaxWSMessage:  a.cfg.MaxWSMessage,
		Proxy:         px,
		WSDialer:      wsDialer,
		Logger:        a.log.With(slog.String("sid", offerMsg.SID)),
		Verbose:       a.cfg.Verbose,
		// On the Nostr path trickle is on: candidates travel as separate
		// events (BRIEF phase 2).
		Trickle: true,
	})
	if err != nil {
		return err
	}

	sessCtx, cancel := context.WithCancel(ctx)

	a.mu.Lock()
	slot.sess = sess
	slot.cancel = cancel
	a.mu.Unlock()

	// Send the local candidates to the visitor.
	sess.OnLocalCandidate(func(c webrtc.ICECandidateInit) {
		mid := ""
		if c.SDPMid != nil {
			mid = *c.SDPMid
		}
		if mid == "" {
			// There is only one m-line (the data channel); rather than
			// leaving mid empty we use the defined value.
			mid = "0"
		}
		a.publish(sessCtx, peerPub, signaling.Msg{
			Type:      signaling.TypeCandidate,
			SID:       offerMsg.SID,
			Candidate: c.Candidate,
			Mid:       mid,
		})
	})

	answerCtx, answerCancel := context.WithTimeout(sessCtx, SessionSetupTimeout)
	defer answerCancel()

	answer, err := sess.Answer(answerCtx, webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerMsg.SDP,
	})
	if err != nil {
		cancel()
		_ = sess.Close()
		return fmt.Errorf("the answer could not be created: %w", err)
	}

	if err := a.publish(sessCtx, peerPub, signaling.Msg{
		Type: signaling.TypeAnswer, SID: offerMsg.SID, SDP: answer.SDP,
	}); err != nil {
		cancel()
		_ = sess.Close()
		return fmt.Errorf("the answer could not be published: %w", err)
	}

	// Wait for the session to open, and clean up when it closes.
	go a.watchSession(sessCtx, peerPub, offerMsg.SID, sess)
	return nil
}

func (a *Acceptor) watchSession(ctx context.Context, peerPub, sid string, sess *tunnel.Session) {
	short := nostr.ShortPubKey(peerPub)

	if err := sess.WaitReady(ctx, SessionSetupTimeout); err != nil {
		a.log.Warn("the tunnel did not open, closing the session",
			slog.String("peer", short), slog.String("sid", sid),
			slog.String("err", err.Error()))
		a.dropSession(sid)
		return
	}

	a.log.Info("tunnel open", slog.String("peer", short), slog.String("sid", sid))

	select {
	case <-sess.Done():
		// We write down why it closed: going idle, ICE dropping and the
		// visitor's `bye` all printed the same line until now, so the log
		// did not say why the tunnel went away.
		a.log.Info("tunnel closed",
			slog.String("peer", short), slog.String("sid", sid),
			slog.String("reason", closeReason(sess.Err())))
	case <-ctx.Done():
	}
	a.dropSession(sid)
}

// iceConfig gets the ICE list for a session; it returns empty when there
// is no factory.
func (a *Acceptor) iceConfig() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
	if a.cfg.ICE == nil {
		return nil, webrtc.ICETransportPolicyAll
	}
	return a.cfg.ICE()
}

// closeReason turns the close cause into a short label.
func closeReason(err error) string {
	switch {
	case err == nil:
		// A clean close: the visitor sent `bye` or the channel dropped.
		return "clean"
	case errors.Is(err, tunnel.ErrIdle):
		return "idle"
	case errors.Is(err, tunnel.ErrDisconnected):
		return "connection-lost"
	default:
		return err.Error()
	}
}

func (a *Acceptor) onCandidate(peerPub string, msg signaling.Msg) {
	short := nostr.ShortPubKey(peerPub)

	a.mu.Lock()
	vs, ok := a.sessions[msg.SID]
	a.mu.Unlock()

	// An unknown sid: we do not allow state to be polluted.
	if !ok {
		a.log.Debug("candidate for an unknown sid was skipped",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	// Another visitor must not be able to close or pollute the session.
	if vs.peerPub != peerPub {
		a.log.Warn("another pubkey sent a candidate to this session",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	if vs.sess == nil {
		a.log.Debug("the session is not ready yet, candidate skipped",
			slog.String("sid", msg.SID))
		return
	}

	mid := msg.Mid
	if err := vs.sess.AddRemoteCandidate(webrtc.ICECandidateInit{
		Candidate: msg.Candidate,
		SDPMid:    &mid,
	}); err != nil {
		a.log.Warn("the candidate could not be added",
			slog.String("sid", msg.SID), slog.String("err", err.Error()))
	}
}

func (a *Acceptor) onBye(peerPub string, msg signaling.Msg) {
	a.mu.Lock()
	vs, ok := a.sessions[msg.SID]
	a.mu.Unlock()
	if !ok || vs.peerPub != peerPub {
		return
	}

	a.log.Info("visitor left",
		slog.String("peer", nostr.ShortPubKey(peerPub)),
		slog.String("sid", msg.SID),
		slog.String("reason", msg.Reason))
	a.dropSession(msg.SID)
}

func (a *Acceptor) dropSession(sid string) {
	a.mu.Lock()
	vs, ok := a.sessions[sid]
	if ok {
		delete(a.sessions, sid)
	}
	a.mu.Unlock()

	if !ok {
		return
	}
	if vs.cancel != nil {
		vs.cancel()
	}
	if vs.sess != nil {
		_ = vs.sess.Close()
	}
}

func (a *Acceptor) closeAll() {
	a.mu.Lock()
	all := make([]*visitorSession, 0, len(a.sessions))
	for _, vs := range a.sessions {
		all = append(all, vs)
	}
	a.sessions = map[string]*visitorSession{}
	a.mu.Unlock()

	for _, vs := range all {
		if vs.cancel != nil {
			vs.cancel()
		}
		if vs.sess != nil {
			_ = vs.sess.Close()
		}
	}
}

// publish sends a signaling message to the visitor.
func (a *Acceptor) publish(ctx context.Context, peerPub string, msg signaling.Msg) error {
	payload, err := a.codec.Encode(msg)
	if err != nil {
		return fmt.Errorf("the message could not be encoded: %w", err)
	}

	// NIP-44: the content is readable only by the recipient; relays
	// cannot see it.
	content, err := nostr.Encrypt(a.cfg.Identity.SecretKey, peerPub, payload)
	if err != nil {
		return fmt.Errorf("NIP-44 encryption: %w", err)
	}

	ev := nostr.NewSignalingEvent(peerPub, content)
	if err := ev.Sign(a.cfg.Identity.SecretKey); err != nil {
		return fmt.Errorf("the event could not be signed: %w", err)
	}

	pubCtx, cancel := context.WithTimeout(ctx, PublishTimeout)
	defer cancel()

	report, err := a.cfg.Pool.Publish(pubCtx, ev)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	a.log.Debug("signaling message published",
		slog.String("t", string(msg.Type)),
		slog.String("sid", msg.SID),
		slog.Int("relay_ok", len(report.OK)))
	return nil
}

// authMode is the auth value announced in the hello message.
//
// With a secret it is "required": the visitor side can see that and show
// the user a meaningful message.
func (a *Acceptor) authMode() protocol.AuthMode {
	if a.cfg.Sealer != nil {
		return protocol.AuthRequired
	}
	return protocol.AuthNone
}
