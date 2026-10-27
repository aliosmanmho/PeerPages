package connect

import (
	"sync"
	"time"
)

// Ziyaretci guvenilmez (BRIEF bolum 6 ilke 4). Bu dosya signaling'e
// uygulanan korumalari tutar: replay, saat kaymasi ve hiz limiti.

// Varsayilan koruma parametreleri (protocol/signaling.md).
const (
	// MaxClockSkew event'in `created_at` degeri icin kabul edilen sapma.
	// Daha eski/yeni event'ler reddedilir: eski event'lerin tekrar
	// oynatilmasini sinirlar.
	MaxClockSkew = 5 * time.Minute
	// SeenTTL gorulen event id'lerinin hatirlanma suresi. MaxClockSkew'den
	// buyuk olmali: aksi halde hatirlamayi biraktigimiz bir event hala
	// "zamaninda" sayilip tekrar kabul edilirdi.
	SeenTTL = 2 * MaxClockSkew
	// RateWindow hiz limiti penceresi.
	RateWindow = time.Minute
	// RateBurst bir ziyaretci pubkey'inin pencere basina event hakki.
	//
	// Trickle ICE'ta bir oturum onlarca candidate yollayabilir, bu yuzden
	// comert; amac kotu niyetli selleme karsi ust sinir koymak.
	RateBurst = 120
)

// seenSet gorulen event id'lerini sureli olarak hatirlar (replay korumasi).
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

// seen id daha once gorulduyse true doner; gorulmemisse kaydeder.
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

// rateLimiter ziyaretci pubkey'i basina sabit pencereli hiz limiti.
//
// Sabit pencere sectik: kayan pencereye gore daha az bellek ve bu is icin
// yeterli. Amac adalet degil, selleme karsi ust sinir.
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

// allow key icin bir event hakki tuketir; hak kalmadiysa false doner.
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

// freshEnough event zaman damgasi kabul edilen sapma icinde mi.
func freshEnough(createdAt, now time.Time, skew time.Duration) bool {
	diff := now.Sub(createdAt)
	if diff < 0 {
		diff = -diff
	}
	return diff <= skew
}
