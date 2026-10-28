package stats_test

import (
	"sync"
	"testing"

	"github.com/aliosmanmho/PeerPages/agent/internal/stats"
)

func TestPerTargetAndTotal(t *testing.T) {
	r := stats.New([]string{"default", "admin"})

	d := r.Target("default")
	a := r.Target("admin")

	d.AddRequest()
	d.AddSuccess()
	d.AddBytesOut(1000)

	a.AddRequest()
	a.AddError()
	a.AddBytesIn(42)
	a.AddBytesOut(7)

	per := r.Snapshot()
	if got := per["default"]; got.Requests != 1 || got.Success != 1 || got.BytesOut != 1000 {
		t.Errorf("default: %+v", got)
	}
	if got := per["admin"]; got.Requests != 1 || got.Errors != 1 || got.BytesIn != 42 {
		t.Errorf("admin: %+v", got)
	}

	// The total accumulates over the targets.
	total := r.Total()
	if total.Requests != 2 || total.Success != 1 || total.Errors != 1 {
		t.Errorf("total: %+v", total)
	}
	if total.BytesOut != 1007 || total.BytesIn != 42 {
		t.Errorf("total bytes: %+v", total)
	}
}

// An undefined target returns nil and calls on a nil counter do not
// panic, so the caller does not have to check everywhere.
func TestNilSafety(t *testing.T) {
	r := stats.New([]string{"default"})

	var c = r.Target("no-such-target")
	if c != nil {
		t.Fatalf("nil was expected for an undefined target, got: %v", c)
	}
	c.AddRequest()
	c.AddSuccess()
	c.AddError()
	c.AddBytesIn(5)
	c.AddBytesOut(5)
	if got := c.Snapshot(); got != (stats.Snapshot{}) {
		t.Errorf("a nil counter's snapshot has to be empty: %+v", got)
	}

	// A nil Registry is safe too.
	var nilReg *stats.Registry
	if got := nilReg.Total(); got != (stats.Snapshot{}) {
		t.Errorf("nil registry: %+v", got)
	}
	if nilReg.Target("default") != nil || nilReg.Names() != nil || nilReg.Snapshot() != nil {
		t.Error("a nil registry has to return the zero value")
	}

	// The total must not be corrupted by targets that are not counted.
	if got := r.Total(); got != (stats.Snapshot{}) {
		t.Errorf("writes to an undefined target must not reach the total: %+v", got)
	}
}

// Because requests are handled on separate goroutines the counters have
// to be race free. The real value of this test shows under -race.
func TestConcurrentCounting(t *testing.T) {
	r := stats.New([]string{"default"})
	c := r.Target("default")

	const workers, each = 8, 500
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				c.AddRequest()
				c.AddSuccess()
				c.AddBytesOut(2)
			}
		}()
	}
	wg.Wait()

	want := uint64(workers * each)
	got := r.Total()
	if got.Requests != want || got.Success != want || got.BytesOut != want*2 {
		t.Errorf("counters: %+v, expected requests/success %d", got, want)
	}
}

func TestNamesSorted(t *testing.T) {
	r := stats.New([]string{"zeta", "admin", "default"})
	want := []string{"admin", "default", "zeta"}
	got := r.Names()
	if len(got) != len(want) {
		t.Fatalf("names: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the names have to be sorted: %v", got)
		}
	}
}
