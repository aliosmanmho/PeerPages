package testsite

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

// The streaming endpoints (phase 6): SSE and WebSocket.
//
// Why they live in the test site: the acceptance criterion is "the Vite
// HMR and Socket.IO examples work over the tunnel". We really do run Vite
// HMR (web/e2e), but the bridge's own behaviour (text/binary echo, close
// codes, a fragmented message) needs a fast, non-fragile endpoint to
// measure against.

// The SSE defaults keep the phase 1 behaviour so the existing tests do
// not break: the stream closes after the third event, with no delay.
const (
	sseDefaultEvents = 3
	sseMaxEvents     = 10_000
	sseMaxDelay      = 10 * time.Second
)

// sseHandler streams `text/event-stream`.
//
// The query parameters:
//
//	?n=5          how many events to send (default 3, 0 = endless)
//	?delay=400    the wait between events (ms, default 0)
//	?keepalive=1  sends a comment line (`:ping`) before the wait
//
// Without `?delay=` three events can fit into a single frame, so the
// claim "it arrived as a stream" CANNOT BE MEASURED; that is why the test
// which proves incremental delivery passes a delay.
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
		http.Error(w, "flushing is not supported", http.StatusInternalServerError)
		return
	}

	delay := time.Duration(delayMS) * time.Millisecond
	for i := 0; events == 0 || i < events; i++ {
		if _, err := io.WriteString(w, fmt.Sprintf("data: event-%d\n\n", i)); err != nil {
			return
		}
		flusher.Flush()

		if delay == 0 {
			// The old behaviour: carry on with no wait, the cancellation
			// check is enough.
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

// wsHandler echoes the messages it receives.
//
// The query parameters:
//
//	?proto=json   a subprotocol to accept (may be repeated)
//	?close=4002   close with this code right after the handshake
//	?reason=...   the close reason
//	?limit=       the read limit (bytes); 1 MiB by default
func wsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: q["proto"],
		// A test site: no Origin check. A real server must not do this.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()

	limit, err := intParam(r, "limit", 1<<20, 125, 16<<20)
	if err != nil {
		_ = c.Close(websocket.StatusUnsupportedData, "invalid limit")
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
