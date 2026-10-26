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

	// Toplam hedeflerin uzerine biner.
	total := r.Total()
	if total.Requests != 2 || total.Success != 1 || total.Errors != 1 {
		t.Errorf("toplam: %+v", total)
	}
	if total.BytesOut != 1007 || total.BytesIn != 42 {
		t.Errorf("toplam bayt: %+v", total)
	}
}

// Tanimsiz hedef nil doner ve nil sayac cagrilari patlamaz: cagiran tarafin
// her yerde kontrol etmesi gerekmesin.
func TestNilSafety(t *testing.T) {
	r := stats.New([]string{"default"})

	var c = r.Target("boyle-bir-hedef-yok")
	if c != nil {
		t.Fatalf("tanimsiz hedef icin nil beklenirdi, geldi: %v", c)
	}
	c.AddRequest()
	c.AddSuccess()
	c.AddError()
	c.AddBytesIn(5)
	c.AddBytesOut(5)
	if got := c.Snapshot(); got != (stats.Snapshot{}) {
		t.Errorf("nil sayacin snapshot'i bos olmali: %+v", got)
	}

	// Nil Registry de guvenli.
	var nilReg *stats.Registry
	if got := nilReg.Total(); got != (stats.Snapshot{}) {
		t.Errorf("nil registry: %+v", got)
	}
	if nilReg.Target("default") != nil || nilReg.Names() != nil || nilReg.Snapshot() != nil {
		t.Error("nil registry bos deger dondurmeli")
	}

	// Toplam, sayilmayan hedefler yuzunden bozulmamali.
	if got := r.Total(); got != (stats.Snapshot{}) {
		t.Errorf("tanimsiz hedefe yazilanlar toplama girmemeli: %+v", got)
	}
}

// Istekler ayri goroutine'lerde islendigi icin sayaclar yarissiz olmali.
// -race ile kosuldugunda asil deger bu testte.
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
		t.Errorf("sayaclar: %+v, beklenen istek/basari %d", got, want)
	}
}

func TestNamesSorted(t *testing.T) {
	r := stats.New([]string{"zeta", "admin", "default"})
	want := []string{"admin", "default", "zeta"}
	got := r.Names()
	if len(got) != len(want) {
		t.Fatalf("adlar: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("adlar siralanmali: %v", got)
		}
	}
}
