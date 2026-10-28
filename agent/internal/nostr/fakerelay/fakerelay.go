// Package fakerelay is a small in-process Nostr relay for tests.
//
// It speaks ONLY the subset of NIP-01 that PeerPages uses:
//
//	client -> relay : ["EVENT", <event>]              publish
//	                  ["REQ", <subId>, <filter>]      subscribe
//	                  ["CLOSE", <subId>]              unsubscribe
//	relay -> client : ["OK", <id>, true|false, <msg>] publish result
//	                  ["EVENT", <subId>, <event>]     a matching event
//	                  ["EOSE", <subId>]               end of stored events
//	                  ["NOTICE", <msg>]               information
//
// Why it exists: a CI test that depends on real relays out on the
// internet is fragile (a relay can go down, rate-limit, or drop an
// ephemeral event). The flow itself is verified deterministically here;
// the real relays are measured separately with `peerpages doctor`.
//
// Ephemeral events (kind 20000-29999) are NOT STORED, as NIP-01 says:
// they only go to the subscribers listening right then. That way the
// question "does the relay drop an ephemeral event" is modelled correctly
// in the test.
package fakerelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	gonostr "github.com/nbd-wtf/go-nostr"
)

// Options changes the relay's behaviour to suit a test scenario.
type Options struct {
	// DropEphemeral set to true means ephemeral events are accepted but
	// NOT FORWARDED to subscribers. It models the "some relays may drop
	// ephemeral events" case from BRIEF section 8.
	DropEphemeral bool
	// RejectAll set to true means every publish is refused with "OK
	// false".
	RejectAll bool
	// RequireAuth set to true makes it behave like a relay that wants
	// NIP-42: a publish is refused with "auth-required".
	RequireAuth bool
	// PublishDelay delays the publish answer (to test the hedged
	// publish).
	PublishDelay time.Duration
}

// Relay is the in-process test relay.
type Relay struct {
	srv  *http.Server
	ln   net.Listener
	opts Options

	mu   sync.Mutex
	subs map[*subscription]struct{}

	// published holds every event seen (ephemeral ones included), so
	// tests can check "did it reach the relay".
	publishedMu sync.Mutex
	published   []gonostr.Event
}

type subscription struct {
	conn   *websocket.Conn
	subID  string
	filter gonostr.Filter
	writeM *sync.Mutex
}

// Start starts the relay on a free port and returns its ws:// address.
func Start(opts Options) (*Relay, error) {
	return StartOn(0, opts)
}

// StartOn starts the relay on the given port (0 = pick a free one).
//
// To run it in a separate process: `peerpages fakerelay --port N`. Needed
// so the Nostr e2e test can connect from the browser.
func StartOn(port int, opts Options) (*Relay, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("could not listen: %w", err)
	}

	r := &Relay{
		ln:   ln,
		opts: opts,
		subs: map[*subscription]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handle)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		_ = r.srv.Serve(ln)
	}()
	return r, nil
}

// URL is the relay's ws address.
func (r *Relay) URL() string {
	return "ws://" + r.ln.Addr().String()
}

// Close shuts the relay down.
func (r *Relay) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.srv.Shutdown(ctx)
}

// Published returns a copy of the events that reached the relay.
func (r *Relay) Published() []gonostr.Event {
	r.publishedMu.Lock()
	defer r.publishedMu.Unlock()
	out := make([]gonostr.Event, len(r.published))
	copy(out, r.published)
	return out
}

// SubscriberCount is the number of open subscriptions.
func (r *Relay) SubscriberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

func (r *Relay) handle(w http.ResponseWriter, req *http.Request) {
	// NIP-11: the client may be asking for the information document.
	if req.Header.Get("Accept") == "application/nostr+json" {
		w.Header().Set("content-type", "application/nostr+json")
		info := map[string]any{
			"name":           "fakerelay",
			"description":    "the PeerPages test relay",
			"supported_nips": []int{1, 11, 44},
		}
		if r.opts.RequireAuth {
			info["supported_nips"] = []int{1, 11, 42, 44}
			info["limitation"] = map[string]any{"auth_required": true}
		}
		_ = json.NewEncoder(w).Encode(info)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	// Message size: a 5 MB body does not go through here, but an SDP can
	// be large.
	conn.SetReadLimit(1 << 20)

	writeM := &sync.Mutex{}
	ctx := req.Context()

	defer r.dropConn(conn)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if err := r.onMessage(ctx, conn, writeM, data); err != nil {
			return
		}
	}
}

func (r *Relay) onMessage(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "invalid message"})
	}

	var verb string
	if err := json.Unmarshal(raw[0], &verb); err != nil {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "invalid message type"})
	}

	switch verb {
	case "EVENT":
		return r.onEvent(ctx, conn, writeM, raw)
	case "REQ":
		return r.onReq(ctx, conn, writeM, raw)
	case "CLOSE":
		return r.onClose(raw)
	default:
		return r.write(ctx, conn, writeM, []any{"NOTICE", "unsupported: " + verb})
	}
}

func (r *Relay) onEvent(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, raw []json.RawMessage) error {
	if len(raw) < 2 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "EVENT is missing"})
	}
	var ev gonostr.Event
	if err := json.Unmarshal(raw[1], &ev); err != nil {
		return r.write(ctx, conn, writeM, []any{"OK", "", false, "invalid: the event could not be read"})
	}

	if r.opts.PublishDelay > 0 {
		select {
		case <-time.After(r.opts.PublishDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Signature verification: real relays do it, and so do we, so a bug
	// that skips signing gets caught in the tests.
	ok, err := ev.CheckSignature()
	if err != nil || !ok {
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "invalid: the signature could not be verified"})
	}

	r.publishedMu.Lock()
	r.published = append(r.published, ev)
	r.publishedMu.Unlock()

	switch {
	case r.opts.RequireAuth:
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "auth-required: you must authenticate"})
	case r.opts.RejectAll:
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "blocked: the test relay is refusing"})
	}

	if err := r.write(ctx, conn, writeM, []any{"OK", ev.ID, true, ""}); err != nil {
		return err
	}

	ephemeral := ev.Kind >= 20000 && ev.Kind < 30000
	if ephemeral && r.opts.DropEphemeral {
		// Accepted but not forwarded: "a relay that drops ephemeral
		// events".
		return nil
	}
	r.broadcast(ctx, ev)
	return nil
}

func (r *Relay) onReq(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, raw []json.RawMessage) error {
	if len(raw) < 3 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "REQ is missing"})
	}
	var subID string
	if err := json.Unmarshal(raw[1], &subID); err != nil {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "the REQ subId could not be read"})
	}
	var filter gonostr.Filter
	if err := json.Unmarshal(raw[2], &filter); err != nil {
		return r.write(ctx, conn, writeM, []any{"CLOSED", subID, "invalid: the filter could not be read"})
	}

	sub := &subscription{conn: conn, subID: subID, filter: filter, writeM: writeM}
	r.mu.Lock()
	r.subs[sub] = struct{}{}
	r.mu.Unlock()

	// Because we store no ephemeral events there is no history to send.
	return r.write(ctx, conn, writeM, []any{"EOSE", subID})
}

func (r *Relay) onClose(raw []json.RawMessage) error {
	if len(raw) < 2 {
		return nil
	}
	var subID string
	if err := json.Unmarshal(raw[1], &subID); err != nil {
		return nil
	}
	r.mu.Lock()
	for sub := range r.subs {
		if sub.subID == subID {
			delete(r.subs, sub)
		}
	}
	r.mu.Unlock()
	return nil
}

func (r *Relay) dropConn(conn *websocket.Conn) {
	r.mu.Lock()
	for sub := range r.subs {
		if sub.conn == conn {
			delete(r.subs, sub)
		}
	}
	r.mu.Unlock()
}

func (r *Relay) broadcast(ctx context.Context, ev gonostr.Event) {
	r.mu.Lock()
	targets := make([]*subscription, 0, len(r.subs))
	for sub := range r.subs {
		if matches(sub.filter, ev) {
			targets = append(targets, sub)
		}
	}
	r.mu.Unlock()

	for _, sub := range targets {
		_ = r.write(ctx, sub.conn, sub.writeM, []any{"EVENT", sub.subID, ev})
	}
}

func (r *Relay) write(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, msg []any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	writeM.Lock()
	defer writeM.Unlock()

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, data); err != nil {
		return errors.New("yazilamadi")
	}
	return nil
}

// matches NIP-01 filtresinin kullandigimiz alanlarini uygular.
func matches(f gonostr.Filter, ev gonostr.Event) bool {
	if len(f.IDs) > 0 && !contains(f.IDs, ev.ID) {
		return false
	}
	if len(f.Kinds) > 0 && !containsInt(f.Kinds, ev.Kind) {
		return false
	}
	if len(f.Authors) > 0 && !contains(f.Authors, ev.PubKey) {
		return false
	}
	if f.Since != nil && ev.CreatedAt < *f.Since {
		return false
	}
	if f.Until != nil && ev.CreatedAt > *f.Until {
		return false
	}
	for name, wanted := range f.Tags {
		if !tagMatches(ev, name, wanted) {
			return false
		}
	}
	return true
}

func tagMatches(ev gonostr.Event, name string, wanted []string) bool {
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != name {
			continue
		}
		if contains(wanted, tag[1]) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func containsInt(haystack []int, needle int) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
