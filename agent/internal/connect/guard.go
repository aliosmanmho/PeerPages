package connect

import (
	"sync"
	"time"
)

// The visitor is untrusted (BRIEF section 6, principle 4). This file
// holds the guards applied to signaling: replay, clock skew and rate
// limiting.

// Default guard parameters (protocol/signaling.md).
const (
	// MaxClockSkew is the skew accepted for an event's `created_at`.
	// Older or newer events are rejected, which bounds the replay of old
	// events.
	MaxClockSkew = 5 * time.Minute
	// SeenTTL is how long seen event ids are remembered. It must be
	// larger than MaxClockSkew: otherwise an event we stopped remembering
	// would still count as "in time" and be accepted again.
	SeenTTL = 2 * MaxClockSkew
	// RateWindow is the rate limit window.
	RateWindow = time.Minute
	// RateBurst is a visitor pubkey's event allowance per window.
	//
	// With trickle ICE one session can send dozens of candidates, so it
	// is generous; the point is to put a ceiling on malicious flooding.
	RateBurst = 120
)

// seenSet remembers seen event ids for a while (replay protection).
type seenSet struct {
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	items map[string]time.Time
}

func newSeenSet(ttl time.Duration, now func() time.Time) *seenSet {
	if now == nil {
		now = time.Now
	}
	return &seenSet{ttl: ttl, now: now, items: map[string]time.Time{}}
}

// seen returns true when the id was seen before; otherwise it records
// it.
func (s *seenSet) seen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.pruneLocked(now)

	if _, ok := s.items[id]; ok {
		return true
	}
	s.items[id] = now
	return false
}

func (s *seenSet) pruneLocked(now time.Time) {
	cutoff := now.Add(-s.ttl)
	for id, at := range s.items {
		if at.Before(cutoff) {
			delete(s.items, id)
		}
	}
}

func (s *seenSet) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// rateLimiter is a fixed-window rate limit per visitor pubkey.
//
// We chose a fixed window: it uses less memory than a sliding one and is
// enough for this job. The goal is not fairness but a ceiling on
// flooding.
type rateLimiter struct {
	window time.Duration
	burst  int
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	start time.Time
	count int
}

func newRateLimiter(window time.Duration, burst int, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		window:  window,
		burst:   burst,
		now:     now,
		buckets: map[string]*bucket{},
	}
}

// allow consumes one event allowance for key; it returns false once none
// is left.
func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.pruneLocked(now)

	b, ok := r.buckets[key]
	if !ok || now.Sub(b.start) >= r.window {
		r.buckets[key] = &bucket{start: now, count: 1}
		return true
	}
	if b.count >= r.burst {
		return false
	}
	b.count++
	return true
}

func (r *rateLimiter) pruneLocked(now time.Time) {
	for key, b := range r.buckets {
		if now.Sub(b.start) >= 2*r.window {
			delete(r.buckets, key)
		}
	}
}

// freshEnough says whether the event timestamp is within the accepted
// skew.
func freshEnough(createdAt, now time.Time, skew time.Duration) bool {
	diff := now.Sub(createdAt)
	if diff < 0 {
		diff = -diff
	}
	return diff <= skew
}
