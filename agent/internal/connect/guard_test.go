package connect

import (
	"sync"
	"testing"
	"time"
)

// The visitor is untrusted (BRIEF section 6, principle 4). These tests
// check the guards themselves; their use inside the flow lives in
// connect_test.go.

func TestSeenSetDetectsReplay(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	s := newSeenSet(time.Minute, clock)

	if s.seen("a") {
		t.Error("the first sighting has to return false")
	}
	if !s.seen("a") {
		t.Error("the second sighting has to return true (a replay)")
	}
	if s.seen("b") {
		t.Error("a different id has to return false")
	}
}

// Once the TTL passes the id is forgotten, so memory does not grow
// without bound.
func TestSeenSetExpires(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	s := newSeenSet(time.Minute, clock)

	s.seen("a")
	if s.size() != 1 {
		t.Fatalf("size %d", s.size())
	}

	now = now.Add(2 * time.Minute)
	if s.seen("a") {
		t.Error("the id should have been forgotten once the TTL passed")
	}
}

// SeenTTL has to be larger than MaxClockSkew: otherwise an event we
// stopped remembering would still count as "in time" and be accepted
// again.
func TestSeenTTLCoversClockSkew(t *testing.T) {
	if SeenTTL <= MaxClockSkew {
		t.Errorf("SeenTTL (%v) has to be larger than MaxClockSkew (%v)", SeenTTL, MaxClockSkew)
	}
}

func TestSeenSetConcurrent(t *testing.T) {
	s := newSeenSet(time.Minute, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.seen("shared")
			s.seen(string(rune('a' + n%26)))
		}(i)
	}
	wg.Wait()
	// It is enough that there is no race and "shared" was recorded once.
	if !s.seen("shared") {
		t.Error("the shared id should have been recorded")
	}
}

func TestFreshEnough(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name  string
		at    time.Time
		fresh bool
	}{
		{"simdi", now, true},
		{"1 min ago", now.Add(-time.Minute), true},
		{"1 min ahead", now.Add(time.Minute), true},
		{"exactly the limit (in the past)", now.Add(-MaxClockSkew), true},
		{"exactly the limit (in the future)", now.Add(MaxClockSkew), true},
		{"beyond the limit (in the past)", now.Add(-MaxClockSkew - time.Second), false},
		{"beyond the limit (in the future)", now.Add(MaxClockSkew + time.Second), false},
		{"too old", now.Add(-24 * time.Hour), false},
	}
	for _, c := range cases {
		if got := freshEnough(c.at, now, MaxClockSkew); got != c.fresh {
			t.Errorf("%s: %v, expected %v", c.name, got, c.fresh)
		}
	}
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 3, clock)

	for i := 0; i < 3; i++ {
		if !r.allow("pk") {
			t.Fatalf("request %d should have been allowed", i+1)
		}
	}
	if r.allow("pk") {
		t.Error("it should have been refused once the limit was passed")
	}
}

// The rate limit is per pubkey: one visitor does not affect another.
func TestRateLimiterIsPerKey(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 2, clock)

	r.allow("a")
	r.allow("a")
	if r.allow("a") {
		t.Error("a should have passed its limit")
	}
	if !r.allow("b") {
		t.Error("b has to be able to use its own limit")
	}
}

func TestRateLimiterWindowResets(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 1, clock)

	if !r.allow("pk") {
		t.Fatal("the first request should have been allowed")
	}
	if r.allow("pk") {
		t.Fatal("a second request in the same window should have been refused")
	}

	now = now.Add(time.Minute + time.Second)
	if !r.allow("pk") {
		t.Error("it should have been allowed once the window reset")
	}
}

// With trickle ICE one session sends dozens of candidates; the default
// limit must not choke that.
func TestRateBurstAllowsTrickleTraffic(t *testing.T) {
	if RateBurst < 60 {
		t.Errorf("RateBurst (%d) is too tight for trickle ICE", RateBurst)
	}
}

func TestRateLimiterPrunesOldBuckets(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 5, clock)

	for i := 0; i < 10; i++ {
		r.allow(string(rune('a' + i)))
	}
	now = now.Add(3 * time.Minute)
	r.allow("yeni")

	r.mu.Lock()
	size := len(r.buckets)
	r.mu.Unlock()
	if size > 1 {
		t.Errorf("the old buckets should have been cleared, left: %d", size)
	}
}
