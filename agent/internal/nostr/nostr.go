// Package nostr is PeerPages's Nostr relay layer.
//
// Signaling uses a single event kind: ephemeral kind 25050, its content
// encrypted with NIP-44 and its `#p` tag holding the recipient pubkey
// (BRIEF section 4). Relays only see "a pubkey received something"
// (security principle 2).
//
// This package wraps go-nostr's SimplePool in a thin layer, so BRIEF's
// two rules are implemented in one place:
//   - the same message goes to ALL relays and the FIRST success is enough
//     (hedged),
//   - if one relay goes down the others keep working.
package nostr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
)

// KindSignaling is the kind of signaling events.
//
// Per NIP-01 the 20000-29999 range is **ephemeral**: relays are not
// expected to store them, they forward them to subscribers and drop them.
// The right choice for signaling, because we do not want an offer or
// answer to persist.
const KindSignaling = 25050

// TagRecipient is the name of the tag that carries the recipient
// pubkey.
const TagRecipient = "p"

// DefaultRelays is the default relay list.
//
// Verified LIVE with `peerpages doctor` on 2026-10-26: all four
// connected, accepted an ephemeral kind 25050 event and delivered it back
// to the subscriber (echo 72-103 ms). Measurements:
// docs/MEASUREMENTS.md.
//
// The third relay from BRIEF section 4 (wss://relay.nostr.band) was
// dropped from the list: the WebSocket handshake timed out from the
// development network. It may work from another network, so we do not
// call it "broken" — we just stopped defaulting to it. You can pass your
// own list with `--relay`; `peerpages doctor` says which ones work.
//
// BRIEF asks for at least 3; we keep four so that losing one still leaves
// 3.
var DefaultRelays = []string{
	"wss://nos.lol",
	"wss://relay.primal.net",
	"wss://nostr.mom",
	"wss://relay.damus.io",
}

// Config is the pool configuration.
type Config struct {
	// URLs are the relay addresses (wss:// or ws://).
	URLs []string
	// Logger is mandatory.
	Logger *slog.Logger
}

// Pool is a pool that uses several relays together.
type Pool struct {
	pool *gonostr.SimplePool
	urls []string
	log  *slog.Logger
}

// NormalizeURL validates and normalizes a relay address.
//
// Only ws/wss are accepted; a trailing slash is dropped so the same relay
// cannot be listed twice.
func NormalizeURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("the relay address is empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("the relay address could not be read %q: %w", raw, err)
	}
	switch u.Scheme {
	case "ws", "wss":
	default:
		return "", fmt.Errorf("a relay address must be ws:// or wss://, got: %q", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("the relay address has no host: %q", raw)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.Fragment = ""
	return u.String(), nil
}

// NormalizeURLs normalizes the list and removes duplicates.
func NormalizeURLs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("at least one relay is required")
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		u, err := NormalizeURL(r)
		if err != nil {
			return nil, err
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}

// NewPool builds the pool. ctx sets the pool's lifetime.
func NewPool(ctx context.Context, cfg Config) (*Pool, error) {
	if cfg.Logger == nil {
		return nil, errors.New("nostr: Logger nil")
	}
	urls, err := NormalizeURLs(cfg.URLs)
	if err != nil {
		return nil, err
	}
	return &Pool{
		pool: gonostr.NewSimplePool(ctx),
		urls: urls,
		log:  cfg.Logger,
	}, nil
}

// URLs returns the relay addresses in the pool.
func (p *Pool) URLs() []string {
	out := make([]string, len(p.urls))
	copy(out, p.urls)
	return out
}

// PublishReport is the per-relay outcome of a publish.
type PublishReport struct {
	// OK lists the relays that accepted the publish.
	OK []string
	// Failed lists the relays that failed and why.
	Failed map[string]string
	// FirstOK is the time until the first acceptance.
	FirstOK time.Duration
	// Elapsed is the time until every relay answered (or ctx ended).
	Elapsed time.Duration
}

// Succeeded says whether at least one relay accepted.
func (r PublishReport) Succeeded() bool { return len(r.OK) > 0 }

// Publish sends the event to every relay.
//
// Hedged: it counts as a success and returns no error once AT LEAST ONE
// relay accepts. If none does, it returns an error. The caller can put a
// deadline on ctx to bound the wait for the first success; the function
// does not keep collecting every answer, it leaves the remaining relays
// in the background after the first success.
func (p *Pool) Publish(ctx context.Context, ev gonostr.Event) (PublishReport, error) {
	started := time.Now()
	report := PublishReport{Failed: map[string]string{}}

	results := p.pool.PublishMany(ctx, p.urls, ev)

	for i := 0; i < len(p.urls); i++ {
		select {
		case res, ok := <-results:
			if !ok {
				i = len(p.urls)
				break
			}
			if res.Error != nil {
				report.Failed[res.RelayURL] = res.Error.Error()
				continue
			}
			if len(report.OK) == 0 {
				report.FirstOK = time.Since(started)
			}
			report.OK = append(report.OK, res.RelayURL)
		case <-ctx.Done():
			report.Elapsed = time.Since(started)
			if report.Succeeded() {
				// The first success is enough; we stop waiting for the
				// rest.
				return report, nil
			}
			return report, fmt.Errorf("the publish could not complete: %w", ctx.Err())
		}
	}

	report.Elapsed = time.Since(started)
	if !report.Succeeded() {
		return report, fmt.Errorf("no relay accepted the event (%d relays tried)", len(p.urls))
	}

	p.log.Debug("event published",
		slog.Int("ok", len(report.OK)),
		slog.Int("failed", len(report.Failed)),
		slog.Duration("first_ok", report.FirstOK))
	return report, nil
}

// ConnectReport is the outcome of connecting ahead of time.
type ConnectReport struct {
	// OK lists the relays we could connect to.
	OK []string
	// Failed lists the relays we could not connect to, and why.
	Failed map[string]string
	// Elapsed is how long every attempt took.
	Elapsed time.Duration
}

// Connect connects to the relays ahead of time.
//
// Why it is needed: saying "ready" before the subscription is up is
// wrong. Because ephemeral events are not stored, an offer that arrives
// before the agent listens is lost WITH NO WAY BACK. So the agent
// connects first, then subscribes, and only then says "ready".
//
// It counts as a success once AT LEAST ONE relay can be reached
// (hedged).
func (p *Pool) Connect(ctx context.Context) (ConnectReport, error) {
	started := time.Now()
	report := ConnectReport{Failed: map[string]string{}}

	type result struct {
		url string
		err error
	}
	results := make(chan result, len(p.urls))

	for _, u := range p.urls {
		go func(u string) {
			_, err := p.pool.EnsureRelay(u)
			results <- result{url: u, err: err}
		}(u)
	}

	for i := 0; i < len(p.urls); i++ {
		select {
		case r := <-results:
			if r.err != nil {
				report.Failed[r.url] = r.err.Error()
				continue
			}
			report.OK = append(report.OK, r.url)
		case <-ctx.Done():
			report.Elapsed = time.Since(started)
			if len(report.OK) > 0 {
				return report, nil
			}
			return report, fmt.Errorf("connecting to the relays could not complete: %w", ctx.Err())
		}
	}

	report.Elapsed = time.Since(started)
	if len(report.OK) == 0 {
		return report, fmt.Errorf("no relay could be reached (%d relays tried)", len(p.urls))
	}
	p.log.Info("connected to the relays",
		slog.Int("ok", len(report.OK)),
		slog.Int("failed", len(report.Failed)),
		slog.Duration("duration", report.Elapsed))
	return report, nil
}

// Subscribe returns the events from every relay on a single channel.
//
// go-nostr removes duplicates when it gets the same event from several
// relays; the signaling layer still tracks event ids itself (replay
// protection, internal/signaling).
//
// The channel closes once ctx ends.
func (p *Pool) Subscribe(ctx context.Context, filter gonostr.Filter) <-chan gonostr.RelayEvent {
	return p.pool.SubscribeMany(ctx, p.urls, filter)
}

// Close closes the pool and every relay connection.
func (p *Pool) Close(reason string) {
	p.pool.Close(reason)
}

// RelayHealth is a relay's current state.
type RelayHealth struct {
	// URL is the relay address (in the form we normalized it to).
	URL string
	// Connected says whether the WebSocket is open right now.
	Connected bool
	// Err is the last connection error; empty when there is none.
	Err string
}

// Health returns the current state per relay.
//
// Why it is needed: go-nostr's own loop does all the reconnecting and
// none of it was visible to PeerPages. Even if every relay went down
// after the agent said "ready", the count in the banner stayed frozen and
// not a single line reached the log — so an agent no visitor could reach
// went on thinking it was working.
func (p *Pool) Health() []RelayHealth {
	out := make([]RelayHealth, 0, len(p.urls))
	for _, u := range p.urls {
		h := RelayHealth{URL: u}
		// The go-nostr pool keys by its own normalized form.
		if r, ok := p.pool.Relays.Load(gonostr.NormalizeURL(u)); ok && r != nil {
			h.Connected = r.IsConnected()
			if err := r.ConnectionError; err != nil {
				h.Err = err.Error()
			}
		}
		out = append(out, h)
	}
	return out
}

// HealthyCount is the number of connected relays.
func (p *Pool) HealthyCount() int {
	n := 0
	for _, h := range p.Health() {
		if h.Connected {
			n++
		}
	}
	return n
}

// SignalingFilter builds the filter for signaling events addressed to a
// given recipient pubkey.
//
// since: events created before this moment are ignored. It bounds the
// replay of old events at the very first step.
func SignalingFilter(recipientPubKey string, since time.Time) gonostr.Filter {
	ts := gonostr.Timestamp(since.Unix())
	return gonostr.Filter{
		Kinds: []int{KindSignaling},
		Tags:  gonostr.TagMap{TagRecipient: []string{recipientPubKey}},
		Since: &ts,
	}
}

// NewSignalingEvent prepares an unsigned signaling event.
//
// content must already be encrypted (NIP-44); this function does no
// encryption.
func NewSignalingEvent(recipientPubKey, content string) gonostr.Event {
	return gonostr.Event{
		CreatedAt: gonostr.Now(),
		Kind:      KindSignaling,
		Tags:      gonostr.Tags{gonostr.Tag{TagRecipient, recipientPubKey}},
		Content:   content,
	}
}

// RecipientOf returns the recipient pubkey from the event's `#p` tag.
func RecipientOf(ev *gonostr.Event) (string, bool) {
	if ev == nil {
		return "", false
	}
	tag := ev.Tags.GetFirst([]string{TagRecipient})
	if tag == nil || len(*tag) < 2 {
		return "", false
	}
	return (*tag)[1], true
}

// ShortPubKey is a shortened pubkey for logs (in the spirit of security
// principle 6: logs should not carry a full identity for no reason).
func ShortPubKey(pub string) string {
	if len(pub) <= 12 {
		return pub
	}
	return pub[:8] + "…" + pub[len(pub)-4:]
}
