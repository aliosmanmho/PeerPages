package connect

import (
	"sync"
	"testing"
	"time"
)

// Ziyaretci guvenilmez (README bolum 6 ilke 4). Bu testler korumalarin
// kendisini dogrular; akis icindeki kullanimlari connect_test.go'da.

func TestSeenSetDetectsReplay(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	s := newSeenSet(time.Minute, clock)

	if s.seen("a") {
		t.Error("ilk gorulme false donmeli")
	}
	if !s.seen("a") {
		t.Error("ikinci gorulme true donmeli (replay)")
	}
	if s.seen("b") {
		t.Error("farkli id false donmeli")
	}
}

// TTL gecince id unutulur: bellek sinirsiz buyumesin.
func TestSeenSetExpires(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	s := newSeenSet(time.Minute, clock)

	s.seen("a")
	if s.size() != 1 {
		t.Fatalf("boyut %d", s.size())
	}

	now = now.Add(2 * time.Minute)
	if s.seen("a") {
		t.Error("TTL gecince id unutulmaliydi")
	}
}

// SeenTTL, MaxClockSkew'den buyuk olmali: aksi halde hatirlamayi biraktigimiz
// bir event hala "zamaninda" sayilip tekrar kabul edilirdi.
func TestSeenTTLCoversClockSkew(t *testing.T) {
	if SeenTTL <= MaxClockSkew {
		t.Errorf("SeenTTL (%v) MaxClockSkew'den (%v) buyuk olmali", SeenTTL, MaxClockSkew)
	}
}

func TestSeenSetConcurrent(t *testing.T) {
	s := newSeenSet(time.Minute, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s.seen("ortak")
			s.seen(string(rune('a' + n%26)))
		}(i)
	}
	wg.Wait()
	// Yaris hatasi yoksa ve "ortak" bir kez kaydedildiyse yeterli.
	if !s.seen("ortak") {
		t.Error("ortak id kaydedilmis olmaliydi")
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
		{"1 dk once", now.Add(-time.Minute), true},
		{"1 dk sonra", now.Add(time.Minute), true},
		{"tam sinir (gecmis)", now.Add(-MaxClockSkew), true},
		{"tam sinir (gelecek)", now.Add(MaxClockSkew), true},
		{"sinirin otesi (gecmis)", now.Add(-MaxClockSkew - time.Second), false},
		{"sinirin otesi (gelecek)", now.Add(MaxClockSkew + time.Second), false},
		{"cok eski", now.Add(-24 * time.Hour), false},
	}
	for _, c := range cases {
		if got := freshEnough(c.at, now, MaxClockSkew); got != c.fresh {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.fresh)
		}
	}
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 3, clock)

	for i := 0; i < 3; i++ {
		if !r.allow("pk") {
			t.Fatalf("%d. istek izinli olmaliydi", i+1)
		}
	}
	if r.allow("pk") {
		t.Error("tavan asildiktan sonra reddedilmeliydi")
	}
}

// Hiz limiti pubkey basina: bir ziyaretci digerini etkilemez.
func TestRateLimiterIsPerKey(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 2, clock)

	r.allow("a")
	r.allow("a")
	if r.allow("a") {
		t.Error("a tavani asmis olmali")
	}
	if !r.allow("b") {
		t.Error("b kendi tavanini kullanabilmeli")
	}
}

func TestRateLimiterWindowResets(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	r := newRateLimiter(time.Minute, 1, clock)

	if !r.allow("pk") {
		t.Fatal("ilk istek izinli olmaliydi")
	}
	if r.allow("pk") {
		t.Fatal("ayni pencerede ikinci istek reddedilmeliydi")
	}

	now = now.Add(time.Minute + time.Second)
	if !r.allow("pk") {
		t.Error("pencere yenilenince izin verilmeliydi")
	}
}

// Trickle ICE'ta bir oturum onlarca candidate yollar; varsayilan tavan
// bunu bogmamali.
func TestRateBurstAllowsTrickleTraffic(t *testing.T) {
	if RateBurst < 60 {
		t.Errorf("RateBurst (%d) trickle ICE icin fazla dar", RateBurst)
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
		t.Errorf("eski kovalar temizlenmeliydi, kalan: %d", size)
	}
}
