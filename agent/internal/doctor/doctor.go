// Package doctor is the health check for the relays and the network
// path.
//
// Why it was the FIRST step of phase 2: BRIEF section 4 says "test the
// real relay list live in phase 2 and update it", and section 8 carries
// the warning that "some relays may drop ephemeral events". Before
// writing the signaling flow we had to measure that the relays really
// work; otherwise code gets written blind on top of a relay list that
// does not.
//
// For each relay, in order:
//  1. the NIP-11 information document — does it want auth, what are the
//     limits?
//  2. the WebSocket connection — is it reachable, and how fast?
//  3. the ephemeral echo test — can a kind 25050 event be published and
//     read back FROM A SUBSCRIPTION? This is the critical step: a relay
//     can accept the event with "OK" and drop it silently, and then
//     signaling does not work.
//
// Plus STUN reachability and the NAT type guess (which belongs to BRIEF
// phase 4, but is needed here to diagnose connection problems).
package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip11"
	"github.com/pion/stun/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
)

// The default durations.
const (
	// DefaultNip11Timeout bounds the information document.
	DefaultNip11Timeout = 8 * time.Second
	// DefaultConnectTimeout bounds the WebSocket connection.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultEchoTimeout bounds a published event coming back from the
	// subscription.
	DefaultEchoTimeout = 8 * time.Second
	// DefaultSTUNTimeout bounds a single STUN query.
	DefaultSTUNTimeout = 5 * time.Second
)

// Options makes the durations tunable.
//
// Fields left at zero fall back to the defaults. Tests pass short
// durations to cut the run time; production uses the defaults.
type Options struct {
	Nip11Timeout   time.Duration
	ConnectTimeout time.Duration
	EchoTimeout    time.Duration
	STUNTimeout    time.Duration
	TURNTimeout    time.Duration
}

func (o Options) withDefaults() Options {
	if o.Nip11Timeout <= 0 {
		o.Nip11Timeout = DefaultNip11Timeout
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = DefaultConnectTimeout
	}
	if o.EchoTimeout <= 0 {
		o.EchoTimeout = DefaultEchoTimeout
	}
	if o.STUNTimeout <= 0 {
		o.STUNTimeout = DefaultSTUNTimeout
	}
	if o.TURNTimeout <= 0 {
		o.TURNTimeout = DefaultTURNTimeout
	}
	return o
}

// Verdict is the usability verdict on a relay.
type Verdict string

// The possible verdicts.
const (
	// VerdictUsable means the relay can be used for signaling.
	VerdictUsable Verdict = "usable"
	// VerdictUnreachable means the connection could not be established.
	VerdictUnreachable Verdict = "unreachable"
	// VerdictAuthRequired means it wants NIP-42 auth; BRIEF says to skip
	// those.
	VerdictAuthRequired Verdict = "skip: wants auth"
	// VerdictRejected means the publish was refused.
	VerdictRejected Verdict = "skip: publish refused"
	// VerdictDropsEphemeral means the publish was accepted but the event
	// never reached the subscriber.
	VerdictDropsEphemeral Verdict = "skip: drops ephemeral"
)

// Nip11Summary holds the useful fields of the information document.
type Nip11Summary struct {
	Name             string `json:"name,omitempty"`
	Software         string `json:"software,omitempty"`
	SupportedNIPs    []int  `json:"supported_nips,omitempty"`
	AuthRequired     bool   `json:"auth_required"`
	PaymentRequired  bool   `json:"payment_required"`
	RestrictedWrites bool   `json:"restricted_writes"`
	MaxMessageLength int    `json:"max_message_length,omitempty"`
	MaxContentLength int    `json:"max_content_length,omitempty"`
	MinPowDifficulty int    `json:"min_pow_difficulty,omitempty"`
}

// RelayReport is a single relay's result.
type RelayReport struct {
	URL       string        `json:"url"`
	Verdict   Verdict       `json:"verdict"`
	Nip11     *Nip11Summary `json:"nip11,omitempty"`
	Nip11Err  string        `json:"nip11_err,omitempty"`
	Connected bool          `json:"connected"`
	ConnectMs float64       `json:"connect_ms,omitempty"`
	Published bool          `json:"published"`
	PublishMs float64       `json:"publish_ms,omitempty"`
	Echoed    bool          `json:"echoed"`
	EchoMs    float64       `json:"echo_ms,omitempty"`
	Err       string        `json:"err,omitempty"`
}

// Usable says whether the relay can be used for signaling.
func (r RelayReport) Usable() bool { return r.Verdict == VerdictUsable }

// STUNServerReport is a single STUN server's result.
type STUNServerReport struct {
	URL      string  `json:"url"`
	OK       bool    `json:"ok"`
	MappedIP string  `json:"mapped_ip,omitempty"`
	Port     int     `json:"port,omitempty"`
	Ms       float64 `json:"ms,omitempty"`
	Err      string  `json:"err,omitempty"`
}

// NATVerdict is the NAT type guess.
type NATVerdict string

// The possible NAT verdicts.
const (
	// NATUnknown means there is not enough data (only one server was
	// reached, or none at all).
	NATUnknown NATVerdict = "unknown"
	// NATFriendly means two STUN servers saw the same external port:
	// endpoint-independent mapping. WebRTC can connect directly.
	NATFriendly NATVerdict = "friendly (no TURN needed)"
	// NATSymmetric means the ports differ: symmetric NAT. TURN may be
	// needed (phase 5).
	NATSymmetric NATVerdict = "symmetric (TURN may be needed)"
	// NATNoSTUN means no STUN server could be reached.
	NATNoSTUN NATVerdict = "STUN unreachable"
)

// STUNReport is the result of the STUN section.
type STUNReport struct {
	Servers []STUNServerReport `json:"servers"`
	Verdict NATVerdict         `json:"verdict"`
	Note    string             `json:"note,omitempty"`
}

// Report is doctor's whole result.
type Report struct {
	Relays []RelayReport `json:"relays"`
	STUN   STUNReport    `json:"stun"`
	// TURN is only filled in when --turn was given.
	TURN *TURNReport `json:"turn,omitempty"`
	// UsableRelays are the addresses of the usable relays.
	UsableRelays []string `json:"usable_relays"`
	// Advice is the one sentence that joins the NAT verdict and the TURN
	// result.
	Advice string `json:"advice"`
}

// OK says whether the agent is in a state where it can work.
//
// Two conditions: at least one usable relay AND (either no TURN was given
// or at least one of the given TURNs grants an allocation). Symmetric NAT
// alone is NOT a failure — it is a fact about the network, so it does not
// affect the verdict.
func (r Report) OK() bool {
	if len(r.UsableRelays) == 0 {
		return false
	}
	if r.TURN != nil && !r.TURN.Usable() {
		return false
	}
	return true
}

// advice builds the advice from the NAT verdict and the TURN result.
func advice(stun STUNReport, turn *TURNReport) string {
	turnWorks := turn != nil && turn.Usable()

	switch {
	case turn != nil && !turnWorks:
		return "TURN was given but no allocation was granted: check the address and the credentials."
	case stun.Verdict == NATNoSTUN:
		return "STUN is unreachable: outbound UDP may be blocked, in which case TURN may not work either."
	case stun.Verdict == NATSymmetric && turnWorks:
		return "The NAT is symmetric but TURN is ready: visitors that cannot connect go over the relay."
	case stun.Verdict == NATSymmetric:
		return "The NAT is symmetric and no TURN was given: if the other side is behind symmetric NAT too, " +
			"no connection can be made. Pass a TURN server with --turn (docs/TURN.md)."
	case stun.Verdict == NATFriendly && turnWorks:
		return "The NAT is friendly; TURN is ready too, but ICE prefers the direct path."
	case stun.Verdict == NATFriendly:
		return "The NAT is friendly: no TURN needed."
	default:
		return "The NAT type could not be determined: at least two STUN servers have to be reachable."
	}
}

// CheckRelays tests the given relays in parallel.
func CheckRelays(ctx context.Context, urls []string, log *slog.Logger, opts Options) []RelayReport {
	opts = opts.withDefaults()
	reports := make([]RelayReport, len(urls))
	var wg sync.WaitGroup

	for i, raw := range urls {
		wg.Add(1)
		go func(idx int, raw string) {
			defer wg.Done()
			reports[idx] = checkRelay(ctx, raw, log, opts)
		}(i, raw)
	}
	wg.Wait()
	return reports
}

func checkRelay(ctx context.Context, raw string, log *slog.Logger, opts Options) RelayReport {
	report := RelayReport{URL: raw, Verdict: VerdictUnreachable}

	normalized, err := nostr.NormalizeURL(raw)
	if err != nil {
		report.Err = err.Error()
		return report
	}
	report.URL = normalized

	// 1) NIP-11: we can spot the relays that want auth before trying to
	// publish.
	if summary, err := fetchNip11(ctx, normalized, opts.Nip11Timeout); err != nil {
		report.Nip11Err = err.Error()
	} else {
		report.Nip11 = summary
		if summary.AuthRequired {
			report.Verdict = VerdictAuthRequired
			// Try the connection anyway, so the timing gets measured.
		}
	}

	// 2) The connection.
	connectCtx, cancelConnect := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancelConnect()

	started := time.Now()
	relay, err := gonostr.RelayConnect(connectCtx, normalized)
	if err != nil {
		report.Err = fmt.Sprintf("connection: %v", err)
		return report
	}
	defer func() { _ = relay.Close() }()

	report.Connected = true
	report.ConnectMs = msSince(started)

	if report.Verdict == VerdictAuthRequired {
		return report
	}

	// 3) The ephemeral echo test: subscribe FIRST, publish AFTER. An
	// ephemeral event is not stored; if you are not listening you miss
	// it.
	sk := gonostr.GeneratePrivateKey()
	pub, err := gonostr.GetPublicKey(sk)
	if err != nil {
		report.Err = fmt.Sprintf("key: %v", err)
		return report
	}

	echoCtx, cancelEcho := context.WithTimeout(ctx, opts.EchoTimeout)
	defer cancelEcho()

	filter := nostr.SignalingFilter(pub, time.Now().Add(-time.Minute))
	sub, err := relay.Subscribe(echoCtx, gonostr.Filters{filter})
	if err != nil {
		report.Err = fmt.Sprintf("subscription: %v", err)
		return report
	}
	defer sub.Unsub()

	// Wait for the EOSE: it means the subscription really is up.
	select {
	case <-sub.EndOfStoredEvents:
	case <-echoCtx.Done():
		report.Err = "the subscription could not be established (no EOSE)"
		return report
	}

	// We send it to ourselves: it does not get mixed up with anyone
	// else's traffic.
	ev := nostr.NewSignalingEvent(pub, "peerpages-doctor-"+randomHex())
	if err := ev.Sign(sk); err != nil {
		report.Err = fmt.Sprintf("signature: %v", err)
		return report
	}

	publishStarted := time.Now()
	if err := relay.Publish(echoCtx, ev); err != nil {
		report.Verdict = VerdictRejected
		report.Err = fmt.Sprintf("publish: %v", err)
		return report
	}
	report.Published = true
	report.PublishMs = msSince(publishStarted)

	echoStarted := time.Now()
	for {
		select {
		case got, ok := <-sub.Events:
			if !ok {
				report.Verdict = VerdictDropsEphemeral
				report.Err = "the subscription closed and the event never came back"
				return report
			}
			if got == nil || got.ID != ev.ID {
				continue // baska bir event; bizimkini beklemeye devam
			}
			report.Echoed = true
			report.EchoMs = msSince(echoStarted)
			report.Verdict = VerdictUsable
			log.Debug("relay usable",
				slog.String("relay", normalized),
				slog.Float64("echo_ms", report.EchoMs))
			return report

		case <-echoCtx.Done():
			// The publish was accepted but the event never reached the
			// subscriber: the relay drops ephemeral events (BRIEF section
			// 8).
			report.Verdict = VerdictDropsEphemeral
			report.Err = fmt.Sprintf("the event did not come back within %s", opts.EchoTimeout)
			return report
		}
	}
}

func fetchNip11(ctx context.Context, relayURL string, timeout time.Duration) (*Nip11Summary, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	info, err := nip11.Fetch(c, relayURL)
	if err != nil {
		return nil, err
	}

	s := &Nip11Summary{
		Name:     info.Name,
		Software: info.Software,
	}
	for _, n := range info.SupportedNIPs {
		switch v := n.(type) {
		case float64:
			s.SupportedNIPs = append(s.SupportedNIPs, int(v))
		case int:
			s.SupportedNIPs = append(s.SupportedNIPs, v)
		}
	}
	sort.Ints(s.SupportedNIPs)

	if lim := info.Limitation; lim != nil {
		s.AuthRequired = lim.AuthRequired
		s.PaymentRequired = lim.PaymentRequired
		s.RestrictedWrites = lim.RestrictedWrites
		s.MaxMessageLength = lim.MaxMessageLength
		s.MaxContentLength = lim.MaxContentLength
		s.MinPowDifficulty = lim.MinPowDifficulty
	}
	return s, nil
}

// CheckSTUN queries the STUN servers and produces the NAT type guess.
//
// How the guess works: ask two different STUN servers and compare the
// EXTERNAL PORTS they see. Same port -> endpoint-independent mapping
// (WebRTC can connect directly). Different ports -> symmetric NAT, TURN
// may be needed (phase 5). With only one server reachable, no verdict can
// be made.
func CheckSTUN(ctx context.Context, urls []string, opts Options) STUNReport {
	opts = opts.withDefaults()
	report := STUNReport{Verdict: NATUnknown}

	for _, raw := range urls {
		report.Servers = append(report.Servers, checkSTUNServer(ctx, raw, opts.STUNTimeout))
	}

	ports := make([]int, 0, len(report.Servers))
	for _, s := range report.Servers {
		if s.OK {
			ports = append(ports, s.Port)
		}
	}

	switch {
	case len(ports) == 0:
		report.Verdict = NATNoSTUN
		report.Note = "Outbound UDP may be blocked; in that case WebRTC does not work at all."
	case len(ports) == 1:
		report.Verdict = NATUnknown
		report.Note = "The NAT type needs at least two STUN servers to be reachable."
	default:
		same := true
		for _, p := range ports[1:] {
			if p != ports[0] {
				same = false
				break
			}
		}
		if same {
			report.Verdict = NATFriendly
		} else {
			report.Verdict = NATSymmetric
			report.Note = "Different STUN servers saw different external ports. If the other side is behind symmetric NAT too, TURN is needed (phase 5)."
		}
	}
	return report
}

func checkSTUNServer(ctx context.Context, raw string, timeout time.Duration) STUNServerReport {
	report := STUNServerReport{URL: raw}

	addr, err := stunHostPort(raw)
	if err != nil {
		report.Err = err.Error()
		return report
	}

	started := time.Now()
	conn, err := net.DialTimeout("udp4", addr, timeout)
	if err != nil {
		report.Err = fmt.Sprintf("connection: %v", err)
		return report
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	client, err := stun.NewClient(conn)
	if err != nil {
		report.Err = fmt.Sprintf("client: %v", err)
		return report
	}
	defer func() { _ = client.Close() }()

	var mapped stun.XORMappedAddress
	var doErr error
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if err := client.Do(msg, func(res stun.Event) {
		if res.Error != nil {
			doErr = res.Error
			return
		}
		doErr = mapped.GetFrom(res.Message)
	}); err != nil {
		report.Err = fmt.Sprintf("query: %v", err)
		return report
	}
	if doErr != nil {
		report.Err = fmt.Sprintf("response: %v", doErr)
		return report
	}

	report.OK = true
	report.MappedIP = mapped.IP.String()
	report.Port = mapped.Port
	report.Ms = msSince(started)
	return report
}

// stunHostPort turns the `stun:host:port` or `host:port` form into
// host:port.
func stunHostPort(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("the STUN address is empty")
	}
	if strings.HasPrefix(s, "stun:") || strings.HasPrefix(s, "stuns:") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("the STUN address could not be read %q: %w", raw, err)
		}
		s = u.Opaque
		if s == "" {
			s = u.Host
		}
	}
	if !strings.Contains(s, ":") {
		s += ":3478"
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		return "", fmt.Errorf("a STUN address must be host:port %q", raw)
	}
	return s, nil
}

// Run runs the full check.
//
// With an empty iceCfg.TURN the TURN check is not run at all and the
// report carries no TURN field.
func Run(
	ctx context.Context, relays []string, iceCfg ice.Config, log *slog.Logger, opts Options,
) Report {
	report := Report{
		Relays: CheckRelays(ctx, relays, log, opts),
		STUN:   CheckSTUN(ctx, iceCfg.STUN, opts),
	}
	if len(iceCfg.TURN) > 0 {
		t := CheckTURN(ctx, iceCfg, opts)
		report.TURN = &t
	}
	for _, r := range report.Relays {
		if r.Usable() {
			report.UsableRelays = append(report.UsableRelays, r.URL)
		}
	}
	report.Advice = advice(report.STUN, report.TURN)
	return report
}

// msSince returns the elapsed time in milliseconds.
//
// Nanosaniye uzerinden hesaplaniyor: sahte relay'e baglanmak 1 mikrosaniyenin
// altinda surebiliyor ve mikrosaniye tabanli hesap bunu 0'a yuvarliyordu.
func msSince(t time.Time) float64 {
	return float64(time.Since(t).Nanoseconds()) / 1e6
}

func randomHex() string {
	// Only to make the event content unique; it need not be cryptographic.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
