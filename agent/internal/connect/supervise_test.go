package connect

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Phase 4: supervising the subscription.
//
// Run used to return an error when the subscription channel closed and
// the agent process exited. That is not what should happen when the
// relays go down or the laptop wakes up: the agent has to recover on its
// own.

// silentLogger keeps the log noise out of the tests.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// scriptedPool is a fake pool that implements the Pool interface (and
// HealthReporter when asked) and whose subscriptions the test drives.
type scriptedPool struct {
	mu      sync.Mutex
	chans   []chan gonostr.RelayEvent
	filters []gonostr.Filter
	health  []nostr.RelayHealth
	// With reportHealth false the pool reports no health (so the path for
	// pools that do not implement HealthReporter gets exercised too).
	reportHealth bool
}

func (p *scriptedPool) Publish(_ context.Context, _ gonostr.Event) (nostr.PublishReport, error) {
	return nostr.PublishReport{OK: []string{"ws://test"}}, nil
}

func (p *scriptedPool) Subscribe(_ context.Context, filter gonostr.Filter) <-chan gonostr.RelayEvent {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch := make(chan gonostr.RelayEvent)
	p.chans = append(p.chans, ch)
	p.filters = append(p.filters, filter)
	return ch
}

func (p *scriptedPool) Health() []nostr.RelayHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reportHealth {
		return nil
	}
	out := make([]nostr.RelayHealth, len(p.health))
	copy(out, p.health)
	return out
}

func (p *scriptedPool) subscribeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.chans)
}

func (p *scriptedPool) filterAt(i int) (gonostr.Filter, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.filters) {
		return gonostr.Filter{}, false
	}
	return p.filters[i], true
}

// closeSub closes subscription channel i (a relay giving up).
func (p *scriptedPool) closeSub(i int) {
	p.mu.Lock()
	ch := p.chans[i]
	p.mu.Unlock()
	close(ch)
}

func (p *scriptedPool) setHealth(h []nostr.RelayHealth) {
	p.mu.Lock()
	p.health = h
	p.reportHealth = true
	p.mu.Unlock()
}

// superviseAcceptor builds an acceptor with shortened supervision
// durations.
func superviseAcceptor(t *testing.T, pool *scriptedPool) *Acceptor {
	t.Helper()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(Config{
		Identity:      id,
		Pool:          pool,
		NewProxy:      func(string) (tunnel.Proxy, error) { return nil, nil },
		Targets:       []string{"default"},
		MaxSessions:   1,
		MaxConcurrent: 1,
		MaxBody:       1024,
		Logger:        silentLogger(),

		healthInterval: 30 * time.Millisecond,
		resubscribeMin: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// waitFor blocks until the condition holds.
func waitFor(t *testing.T, d time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}

// If the subscription channel closes, Run does not exit, it
// resubscribes.
func TestResubscribesWhenSubscriptionCloses(t *testing.T) {
	pool := &scriptedPool{}
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	waitFor(t, 2*time.Second, "ilk abonelik", func() bool { return pool.subscribeCount() >= 1 })
	pool.closeSub(0)

	waitFor(t, 3*time.Second, "yeniden abonelik", func() bool { return pool.subscribeCount() >= 2 })

	select {
	case err := <-done:
		t.Fatalf("Run should not have exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

// The filter has to be rebuilt every epoch: because ephemeral events are
// not stored, subscribing with an old `since` brings back no offer from
// the gap, it would only look at the past.
func TestFilterRefreshedEachEpoch(t *testing.T) {
	pool := &scriptedPool{}
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	waitFor(t, 2*time.Second, "ilk abonelik", func() bool { return pool.subscribeCount() >= 1 })
	first, _ := pool.filterAt(0)

	time.Sleep(1100 * time.Millisecond) // Since saniye hassasiyetinde
	pool.closeSub(0)
	waitFor(t, 3*time.Second, "the second subscription", func() bool { return pool.subscribeCount() >= 2 })

	second, ok := pool.filterAt(1)
	if !ok {
		t.Fatal("there is no second filter")
	}
	if first.Since == nil || second.Since == nil {
		t.Fatal("the filter has no since")
	}
	if !(*second.Since > *first.Since) {
		t.Errorf("the second since (%d) has to be larger than the first (%d)", *second.Since, *first.Since)
	}
}

// With no relay connected the subscription is rebuilt from scratch,
// which forces a reconnect without waiting for go-nostr's own backoff
// (which can climb to 5 minutes).
func TestResubscribesWhenAllRelaysDown(t *testing.T) {
	pool := &scriptedPool{}
	pool.setHealth([]nostr.RelayHealth{
		{URL: "ws://a", Connected: false, Err: "the connection broke"},
		{URL: "ws://b", Connected: false},
	})
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	waitFor(t, 3*time.Second, "a resubscribe for the relays that went down",
		func() bool { return pool.subscribeCount() >= 2 })
}

// While the relays are healthy the subscription should be left alone.
func TestNoResubscribeWhileHealthy(t *testing.T) {
	pool := &scriptedPool{}
	pool.setHealth([]nostr.RelayHealth{
		{URL: "ws://a", Connected: true},
		{URL: "ws://b", Connected: true},
	})
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	waitFor(t, 2*time.Second, "ilk abonelik", func() bool { return pool.subscribeCount() >= 1 })
	time.Sleep(500 * time.Millisecond) // ~16 saglik yoklamasi

	if n := pool.subscribeCount(); n != 1 {
		t.Errorf("%d subscriptions, a healthy pool should have 1", n)
	}
	if healthy, total := a.RelayStatus(); healthy != 2 || total != 2 {
		t.Errorf("RelayStatus() = %d/%d, expected 2/2", healthy, total)
	}
}

// Once ctx ends, Run should exit cleanly (the agent is shutting
// down).
func TestRunReturnsOnCancel(t *testing.T) {
	pool := &scriptedPool{}
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	waitFor(t, 2*time.Second, "ilk abonelik", func() bool { return pool.subscribeCount() >= 1 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit when ctx was cancelled")
	}
}
