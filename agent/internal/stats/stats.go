// Package stats ajanin istek ve bayt sayaclarini tutar.
//
// Amac ziyaretcinin degil AJANIN sahibinin gordugu bilgi: hangi hedefe kac
// istek geldi, kaci basarisiz oldu, ne kadar veri aktarildi. Bagianti
// ekranindaki olcumler ayri bir sey — onlari tarayici kendi sayiyor.
//
// Sayaclar atomik: istekler oturum basina farkli goroutine'lerde islenir
// (tunnel.Session her istegi ayri goroutine'de surer), dolayisiyla kilit
// yerine sync/atomic kullaniyoruz.
package stats

import (
	"sort"
	"sync/atomic"
)

// Snapshot bir anlik okuma.
type Snapshot struct {
	// Requests baslayan istek sayisi.
	Requests uint64
	// Success cevabi eksiksiz aktarilan istek sayisi.
	//
	// Hedefin 404 ya da 500 dondurmesi BASARILIDIR: proxy isini yapmistir.
	// Errors yalnizca ajanin cevabi tasiyamadigi durumlari sayar.
	Success uint64
	// Errors hedefe ulasilamadi, yol gecersiz ya da aktarim yarida kesildi.
	Errors uint64
	// BytesIn ziyaretciden gelen istek govdesi baytlari.
	BytesIn uint64
	// BytesOut ziyaretciye giden cevap govdesi baytlari.
	//
	// Yalnizca GOVDE: ctrl kanalindaki JSON mesajlari ve cerceve basliklari
	// sayilmaz, yani gercek tel trafigi bundan bir miktar fazladir.
	BytesOut uint64
}

// Counters tek bir hedefin (ya da toplamin) sayaclari.
type Counters struct {
	requests atomic.Uint64
	success  atomic.Uint64
	errors   atomic.Uint64
	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64

	// parent toplam sayac; nil olabilir (toplamin kendisinde).
	parent *Counters
}

// AddRequest yeni bir istegi sayar.
func (c *Counters) AddRequest() {
	if c == nil {
		return
	}
	c.requests.Add(1)
	c.parent.AddRequest()
}

// AddSuccess cevabi eksiksiz aktarilan bir istegi sayar.
func (c *Counters) AddSuccess() {
	if c == nil {
		return
	}
	c.success.Add(1)
	c.parent.AddSuccess()
}

// AddError tasinamayan bir istegi sayar.
func (c *Counters) AddError() {
	if c == nil {
		return
	}
	c.errors.Add(1)
	c.parent.AddError()
}

// AddBytesIn ziyaretciden gelen govde baytlarini ekler.
func (c *Counters) AddBytesIn(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.bytesIn.Add(uint64(n))
	c.parent.AddBytesIn(n)
}

// AddBytesOut ziyaretciye giden govde baytlarini ekler.
func (c *Counters) AddBytesOut(n int64) {
	if c == nil || n <= 0 {
		return
	}
	c.bytesOut.Add(uint64(n))
	c.parent.AddBytesOut(n)
}

// Snapshot sayaclarin anlik kopyasini dondurur.
//
// Alanlar tek tek okunur, yani cok yuklu bir anda birbirleriyle tam tutarli
// olmayabilirler (ornegin requests artmis ama success henuz artmamistir).
// Bu bilincli: kilit almak sicak yolu yavaslatir ve gosterilen sayilar
// zaten bir sonraki tikte duzelir.
func (c *Counters) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	return Snapshot{
		Requests: c.requests.Load(),
		Success:  c.success.Load(),
		Errors:   c.errors.Load(),
		BytesIn:  c.bytesIn.Load(),
		BytesOut: c.bytesOut.Load(),
	}
}

// Registry hedef basina sayaclari ve toplami tutar.
//
// Hedef listesi ajan basladiginda sabitlenir (CLI'dan gelir), bu yuzden
// harita kurulduktan sonra yalnizca okunur: kilit gerekmez.
type Registry struct {
	total  *Counters
	byName map[string]*Counters
	names  []string
}

// New verilen hedef adlari icin sayac kumesi kurar.
func New(names []string) *Registry {
	r := &Registry{
		total:  &Counters{},
		byName: make(map[string]*Counters, len(names)),
	}
	r.names = append(r.names, names...)
	sort.Strings(r.names)
	for _, n := range r.names {
		r.byName[n] = &Counters{parent: r.total}
	}
	return r
}

// Target bir hedefin sayaclarini dondurur.
//
// Tanimsiz ad icin nil doner; Counters metotlari nil'de guvenli oldugu icin
// cagiran tarafin kontrol etmesi gerekmez (bilinmeyen hedef zaten connect
// katmaninda reddediliyor).
func (r *Registry) Target(name string) *Counters {
	if r == nil {
		return nil
	}
	return r.byName[name]
}

// Total toplam sayaclar.
func (r *Registry) Total() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return r.total.Snapshot()
}

// Names hedef adlari (alfabetik).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	return r.names
}

// Snapshot hedef basina anlik okuma.
func (r *Registry) Snapshot() map[string]Snapshot {
	if r == nil {
		return nil
	}
	out := make(map[string]Snapshot, len(r.byName))
	for name, c := range r.byName {
		out[name] = c.Snapshot()
	}
	return out
}
