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

// Faz 4: abonelik denetimi.
//
// Daha once Run, abonelik kanali kapandiginda hata donuyordu ve ajan
// surecinden cikiyordu. Relay'ler dustugunde ya da laptop uyandiginda
// olmasi gereken sey bu degil: ajan kendi kendine toparlanmali.

// silentLogger testlerde log gurultusunu keser.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// scriptedPool Pool (ve istenirse HealthReporter) arayuzunu uygulayan,
// aboneliklerini test kontrol eden sahte havuz.
type scriptedPool struct {
	mu      sync.Mutex
	chans   []chan gonostr.RelayEvent
	filters []gonostr.Filter
	health  []nostr.RelayHealth
	// reportHealth false ise havuz saglik bildirmez (HealthReporter
	// uygulanmamis havuzlarin yolunu da sinamak icin).
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

// closeSub i. abonelik kanalini kapatir (relay'in vazgecmesi).
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

// superviseAcceptor denetim sureleri kisaltilmis bir acceptor kurar.
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

// waitFor kosul saglanana kadar bekler.
func waitFor(t *testing.T, d time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", msg)
}

// Abonelik kanali kapanirsa Run cikmaz, yeniden abone olur.
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
		t.Fatalf("Run cikmamaliydi: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

// Her donemde filtre yeniden kurulmali: ephemeral event'ler saklanmadigi
// icin eski bir `since` ile abone olmak bosluktaki offer'lari getirmez,
// yalnizca gecmise bakmis olurduk.
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
	waitFor(t, 3*time.Second, "ikinci abonelik", func() bool { return pool.subscribeCount() >= 2 })

	second, ok := pool.filterAt(1)
	if !ok {
		t.Fatal("ikinci filtre yok")
	}
	if first.Since == nil || second.Since == nil {
		t.Fatal("filtrede since yok")
	}
	if !(*second.Since > *first.Since) {
		t.Errorf("ikinci since (%d) ilkinden (%d) buyuk olmali", *second.Since, *first.Since)
	}
}

// Hicbir relay bagli degilse abonelik bastan kurulur: bu, go-nostr'un
// kendi geri sayimini (5 dakikaya kadar cikabiliyor) beklemeden yeniden
// baglanmayi zorlar.
func TestResubscribesWhenAllRelaysDown(t *testing.T) {
	pool := &scriptedPool{}
	pool.setHealth([]nostr.RelayHealth{
		{URL: "ws://a", Connected: false, Err: "baglanti koptu"},
		{URL: "ws://b", Connected: false},
	})
	a := superviseAcceptor(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = a.Run(ctx) }()

	waitFor(t, 3*time.Second, "dusen relay'ler icin yeniden abonelik",
		func() bool { return pool.subscribeCount() >= 2 })
}

// Relay'ler sagliklıyken abonelik rahat birakilmali.
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
		t.Errorf("abonelik sayisi %d, saglikli havuzda 1 olmali", n)
	}
	if healthy, total := a.RelayStatus(); healthy != 2 || total != 2 {
		t.Errorf("RelayStatus() = %d/%d, beklenen 2/2", healthy, total)
	}
}

// ctx bitince Run temiz ciksin (ajan kapaniyor).
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
			t.Errorf("Run hatasi: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ctx iptalinde cikmadi")
	}
}
