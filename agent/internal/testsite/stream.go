package testsite

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// Akis uclari (Faz 6): SSE ve WebSocket.
//
// Neden test sitesinde: kabul kriteri "Vite HMR ve Socket.IO ornegi tunel
// uzerinden calisiyor". Vite HMR'i gercekten kosuyoruz (web/e2e), ama
// koprunun kendi davranisini (metin/ikili yanki, kapanis kodlari, parcali
// mesaj) hizli ve kirilgan olmayan bir ucla olcmek gerekiyor.

// sseVarsayilanlar mevcut testleri bozmamak icin Faz 1'deki davranisi
// korur: ucuncu olaydan sonra akis kapanir, bekleme yok.
const (
	sseDefaultEvents = 3
	sseMaxEvents     = 10_000
	sseMaxDelay      = 10 * time.Second
)

// sseHandler `text/event-stream` akitir.
//
// Sorgu parametreleri:
//
//	?n=5          kac olay yollanacak (varsayilan 3, 0 = sonsuz)
//	?delay=400    olaylar arasi bekleme (ms, varsayilan 0)
//	?keepalive=1  beklemeden once bir yorum satiri (`:ping`) yollar
//
// `?delay=` olmadan uc olay tek cerceveye sigabilir, yani "akis halinde
// geldi" iddiasi OLCULEMEZ; artimli teslimi kanitlayan test bu yuzden
// gecikme veriyor.
func sseHandler(w http.ResponseWriter, r *http.Request) {
	events, err := intParam(r, "n", sseDefaultEvents, 0, sseMaxEvents)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	delayMS, err := intParam(r, "delay", 0, 0, int(sseMaxDelay/time.Millisecond))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	keepalive := r.URL.Query().Get("keepalive") != ""

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-store")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "flush desteklenmiyor", http.StatusInternalServerError)
		return
	}

	delay := time.Duration(delayMS) * time.Millisecond
	for i := 0; events == 0 || i < events; i++ {
		if _, err := io.WriteString(w, fmt.Sprintf("data: olay-%d\n\n", i)); err != nil {
			return
		}
		flusher.Flush()

		if delay == 0 {
			// Eski davranis: beklemeden devam, iptal kontrolu yeter.
			select {
			case <-r.Context().Done():
				return
			default:
			}
			continue
		}

		if keepalive {
			if _, err := io.WriteString(w, ":ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(delay):
		}
	}
}

// wsHandler gelen mesajlari yankilar.
//
// Sorgu parametreleri:
//
//	?proto=json   kabul edilecek alt protokol (tekrarlanabilir)
//	?close=4002   el sikismadan hemen sonra bu kodla kapat
//	?reason=...   kapanis sebebi
//	?limit=       okuma siniri (bayt); varsayilan 1 MiB
func wsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: q["proto"],
		// Test sitesi: Origin denetimi yok. Gercek bir sunucuda bu
		// yapilmamali.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()

	limit, err := intParam(r, "limit", 1<<20, 125, 16<<20)
	if err != nil {
		_ = c.Close(websocket.StatusUnsupportedData, "limit gecersiz")
		return
	}
	c.SetReadLimit(int64(limit))

	if code := q.Get("close"); code != "" {
		n, perr := strconv.Atoi(code)
		if perr != nil || n < 1000 || n > 4999 {
			_ = c.Close(websocket.StatusUnsupportedData, "close kodu gecersiz")
			return
		}
		_ = c.Close(websocket.StatusCode(n), q.Get("reason"))
		return
	}

	for {
		typ, data, rerr := c.Read(r.Context())
		if rerr != nil {
			return
		}
		if werr := c.Write(r.Context(), typ, data); werr != nil {
			return
		}
	}
}

// intParam sorgu parametresini aralikla birlikte okur.
func intParam(r *http.Request, name string, def, minVal, maxVal int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s tamsayi olmali", name)
	}
	if n < minVal || n > maxVal {
		return 0, fmt.Errorf("%s %d…%d araliginda olmali", name, minVal, maxVal)
	}
	return n, nil
}
