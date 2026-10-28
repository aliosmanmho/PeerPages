package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signal"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// This file tests the AGENT HALF of the chain end to end:
//
//	visitor (pion) --WebRTC--> tunnel --> proxy --> a real HTTP test site
//
// The browser side (the Service Worker plus the page) is verified in the
// Playwright e2e tests (web/e2e/tunnel.spec.ts). With no browser here the
// tests are fast and stable in CI; the throughput measurement comes from here too.

const itTimeout = 30 * time.Second

// itVisitor ziyaretci tarafini taklit eden asgari istemci.
type itVisitor struct {
	pc   *webrtc.PeerConnection
	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	mu     sync.Mutex
	ctrlCh chan protocol.Msg
	dataCh chan protocol.Frame

	readyOnce sync.Once
	ready     chan struct{}
}

type itResponse struct {
	status  int
	headers map[string]string
	body    []byte
	abort   string
	// ttfb: from sending the request to the first body byte.
	ttfb time.Duration
	// total: from sending the request to the body being complete.
	total time.Duration
}

func itConnect(t *testing.T, p *Proxy, targets []string) *itVisitor {
	t.Helper()

	sess, err := tunnel.New(tunnel.Config{
		Targets:       targets,
		Auth:          protocol.AuthNone,
		MaxConcurrent: 16,
		MaxBody:       1 << 20,
		Proxy:         p,
		Logger:        quietLogger(),
	})
	if err != nil {
		t.Fatalf("the session could not be set up: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	v := newITVisitor(t)
	t.Cleanup(func() { _ = v.pc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), itTimeout)
	defer cancel()

	// ZIYARETCI offerer (Faz 2 rol degisimi): kanallari acar, offer uretir.
	offer, err := v.pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("offer: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(v.pc)
	if err := v.pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("the local SDP: %v", err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatalf("visitor ICE: %v", ctx.Err())
	}

	// Really use the manual signaling path, base64 encoding included.
	offerEnc, err := signal.Encode(*v.pc.LocalDescription())
	if err != nil {
		t.Fatalf("the offer could not be encoded: %v", err)
	}
	offerDec, err := signal.Decode("  " + offerEnc[:20] + "\n" + offerEnc[20:] + "\n")
	if err != nil {
		t.Fatalf("the offer could not be decoded: %v", err)
	}

	answer, err := sess.Answer(ctx, offerDec)
	if err != nil {
		t.Fatalf("the answer could not be created: %v", err)
	}
	answerEnc, err := signal.Encode(answer)
	if err != nil {
		t.Fatalf("the answer could not be encoded: %v", err)
	}
	answerDec, err := signal.Decode(answerEnc)
	if err != nil {
		t.Fatalf("the answer could not be decoded: %v", err)
	}
	if err := v.pc.SetRemoteDescription(answerDec); err != nil {
		t.Fatalf("the visitor could not apply the answer: %v", err)
	}

	if err := sess.WaitReady(ctx, itTimeout); err != nil {
		t.Fatalf("the agent channels did not open: %v", err)
	}
	select {
	case <-v.ready:
	case <-time.After(itTimeout):
		t.Fatal("the visitor channels did not open")
	}

	// hello'yu yut.
	select {
	case m := <-v.ctrlCh:
		if _, ok := m.(protocol.Hello); !ok {
			t.Fatalf("the first message has to be hello, got: %s", m.Type())
		}
	case <-time.After(itTimeout):
		t.Fatal("no hello arrived")
	}

	return v
}

func newITVisitor(t *testing.T) *itVisitor {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("the visitor peer connection: %v", err)
	}
	v := &itVisitor{
		pc:     pc,
		ctrlCh: make(chan protocol.Msg, 256),
		dataCh: make(chan protocol.Frame, 8192),
		ready:  make(chan struct{}),
	}

	// Kanallari ZIYARETCI aciyor.
	ordered := true
	ctrl, err := pc.CreateDataChannel("ctrl", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		t.Fatalf("ctrl channel: %v", err)
	}
	data, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		t.Fatalf("data channel: %v", err)
	}
	v.ctrl = ctrl
	v.data = data

	ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		msg, perr := protocol.ParseCtrl(string(m.Data))
		if perr != nil {
			t.Errorf("invalid ctrl: %v (%s)", perr, m.Data)
			return
		}
		v.ctrlCh <- msg
	})
	data.OnMessage(func(m webrtc.DataChannelMessage) {
		f, derr := protocol.DecodeFrame(m.Data)
		if derr != nil {
			t.Errorf("invalid frame: %v", derr)
			return
		}
		v.dataCh <- f
	})

	check := func() {
		v.mu.Lock()
		both := v.ctrl != nil && v.data != nil &&
			v.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
			v.data.ReadyState() == webrtc.DataChannelStateOpen
		v.mu.Unlock()
		if both {
			v.readyOnce.Do(func() { close(v.ready) })
		}
	}
	ctrl.OnOpen(check)
	data.OnOpen(check)

	return v
}

// get bir GET istegi yollar ve cevabi toplar.
//
// `end` on its own does not mean the body is finished (there is no ordering
// between ctrl and data): we wait until the frames/len counters are complete.
func (v *itVisitor) get(t *testing.T, id uint32, path string) itResponse {
	t.Helper()

	v.mu.Lock()
	ctrl := v.ctrl
	v.mu.Unlock()
	if ctrl == nil {
		t.Fatal("there is no ctrl channel")
	}

	start := time.Now()
	req := protocol.Req{ID: id, Method: "GET", Path: path, Headers: map[string]string{}, HasBody: false}
	if err := ctrl.SendText(req.Canonical()); err != nil {
		t.Fatalf("the request could not be sent: %v", err)
	}

	var (
		out       itResponse
		gotFrames uint32
		endSeen   bool
		expFrames uint32
		expLen    int64
		body      bytes.Buffer
	)
	deadline := time.After(itTimeout)

	for {
		done := endSeen && gotFrames == expFrames && int64(body.Len()) == expLen
		if done {
			out.body = body.Bytes()
			out.total = time.Since(start)
			return out
		}

		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.Res:
				if msg.ID == id {
					out.status = msg.Status
					out.headers = msg.Headers
				}
			case protocol.End:
				if msg.ID == id {
					endSeen = true
					expFrames = msg.Frames
					expLen = msg.Len
				}
			case protocol.Abort:
				if msg.ID == id {
					out.abort = msg.Reason
					out.body = body.Bytes()
					out.total = time.Since(start)
					return out
				}
			}
		case f := <-v.dataCh:
			if f.ID != id {
				continue
			}
			if f.Seq != gotFrames {
				t.Errorf("unexpected seq %d, expected %d", f.Seq, gotFrames)
			}
			if gotFrames == 0 {
				out.ttfb = time.Since(start)
			}
			gotFrames++
			body.Write(f.Payload)
		case <-deadline:
			t.Fatalf("the request %q did not complete within %s (status=%d, %d bytes, end=%v)",
				path, itTimeout, out.status, body.Len(), endSeen)
		}
	}
}

// ping ctrl kanalinda ping/pong gidis donus suresini olcer.
func (v *itVisitor) ping(t *testing.T) time.Duration {
	t.Helper()
	v.mu.Lock()
	ctrl := v.ctrl
	v.mu.Unlock()

	start := time.Now()
	if err := ctrl.SendText(protocol.Ping{}.Canonical()); err != nil {
		t.Fatalf("the ping could not be sent: %v", err)
	}
	deadline := time.After(itTimeout)
	for {
		select {
		case m := <-v.ctrlCh:
			if _, ok := m.(protocol.Pong); ok {
				return time.Since(start)
			}
		case <-deadline:
			t.Fatal("no pong arrived")
		}
	}
}

// siteProxy sets up a proxy that serves the real test site.
func siteProxy(t *testing.T) (*Proxy, []string, *httptest.Server) {
	t.Helper()

	// big.bin is not in the repo: it has to be generated.
	if _, _, err := testsite.GenerateBig("../../testdata/site"); err != nil {
		t.Fatalf("the test data could not be generated: %v", err)
	}

	srv := httptest.NewServer(testsite.Handler("../../testdata/site"))
	t.Cleanup(srv.Close)

	targets, err := target.Parse([]string{srv.URL})
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	p, err := New(Config{
		Targets:    targets,
		TargetName: target.DefaultName,
		MaxBody:    50 << 20,
		Timeout:    itTimeout,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	return p, targets.Names(), srv
}

// The agent-side counterpart of the phase 1 acceptance criteria.
func TestIntegrationTestSiteOverTunnel(t *testing.T) {
	p, names, _ := siteProxy(t)
	v := itConnect(t, p, names)

	t.Run("HTML", func(t *testing.T) {
		// "/" istenir: http.FileServer /index.html'i "./"a 301 ile
		// kanoniklestirir (asagidaki alt testte dogrulaniyor).
		res := v.get(t, 1, "/")
		if res.abort != "" {
			t.Fatalf("abort: %s", res.abort)
		}
		if res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if !bytes.Contains(res.body, []byte("PeerPages test site")) {
			t.Errorf("the HTML content is wrong: %d bytes", len(res.body))
		}
		if ct := res.headers["content-type"]; !bytes.Contains([]byte(ct), []byte("text/html")) {
			t.Errorf("content-type: %q", ct)
		}
		// The body is split into frames, so no content-length should go out.
		if _, ok := res.headers["content-length"]; ok {
			t.Error("content-length must not go to the visitor")
		}
	})

	// http.FileServer /index.html -> "./" kanoniklestirmesi; Location zaten
	// it is relative, so it passes through as it is.
	t.Run("index.html canonicalization", func(t *testing.T) {
		res := v.get(t, 10, "/index.html")
		if res.status != 301 {
			t.Fatalf("status %d, 301 was expected", res.status)
		}
		if loc := res.headers["location"]; loc != "./" {
			t.Errorf("location: %q", loc)
		}
	})

	t.Run("CSS", func(t *testing.T) {
		res := v.get(t, 2, "/style.css")
		if res.status != 200 || !bytes.Contains(res.body, []byte("rgb(13, 17, 23)")) {
			t.Errorf("the CSS is wrong: status=%d, %d bytes", res.status, len(res.body))
		}
	})

	t.Run("JS", func(t *testing.T) {
		res := v.get(t, 3, "/app.js")
		if res.status != 200 || !bytes.Contains(res.body, []byte("api/items")) {
			t.Errorf("the JS is wrong: status=%d, %d bytes", res.status, len(res.body))
		}
	})

	t.Run("image", func(t *testing.T) {
		res := v.get(t, 4, "/logo.png")
		if res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if !bytes.HasPrefix(res.body, []byte("\x89PNG\r\n\x1a\n")) {
			t.Errorf("no PNG signature: %x", res.body[:min(8, len(res.body))])
		}
	})

	t.Run("JSON API", func(t *testing.T) {
		res := v.get(t, 5, "/api/items")
		if res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		var payload struct {
			Items []struct {
				Name  string `json:"name"`
				Value int    `json:"value"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(res.body, &payload); err != nil {
			t.Fatalf("the JSON could not be decoded: %v (%q)", err, res.body)
		}
		if payload.Total != 3 || len(payload.Items) != 3 || payload.Items[0].Name != "alpha" {
			t.Errorf("the JSON content is wrong: %+v", payload)
		}
	})

	t.Run("a redirect Location is relative", func(t *testing.T) {
		res := v.get(t, 6, "/redirect")
		if res.status != 302 {
			t.Fatalf("status %d", res.status)
		}
		if res.headers["location"] != "/index.html" {
			t.Errorf("location: %q", res.headers["location"])
		}
	})

	t.Run("404", func(t *testing.T) {
		res := v.get(t, 7, "/no-such-thing")
		if res.status != 404 {
			t.Errorf("status %d", res.status)
		}
	})

	t.Run("SSE streaming", func(t *testing.T) {
		res := v.get(t, 8, "/sse")
		if res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if !bytes.Contains(res.body, []byte("data: event-0")) ||
			!bytes.Contains(res.body, []byte("data: event-2")) {
			t.Errorf("the SSE body: %q", res.body)
		}
	})

	// BRIEF phase 1 acceptance: downloading a 5 MB file.
	t.Run("5 MB file", func(t *testing.T) {
		res := v.get(t, 9, "/big.bin")
		if res.abort != "" {
			t.Fatalf("abort: %s", res.abort)
		}
		if res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if len(res.body) != testsite.BigFileSize {
			t.Fatalf("size %d, expected %d", len(res.body), testsite.BigFileSize)
		}
		sum := sha256.Sum256(res.body)
		if got := hex.EncodeToString(sum[:]); got != testsite.BigFileSHA256() {
			t.Errorf("the sha256 does not match:\n  got:      %s\n  expected: %s", got, testsite.BigFileSHA256())
		}

		mbps := float64(len(res.body)) / (1 << 20) / res.total.Seconds()
		t.Logf("MEASURED 5MB download: %s total, first byte %s, %.1f MB/s",
			res.total.Round(time.Millisecond), res.ttfb.Round(time.Millisecond), mbps)
	})

	t.Run("ping RTT", func(t *testing.T) {
		var total time.Duration
		const n = 20
		worst := time.Duration(0)
		for i := 0; i < n; i++ {
			d := v.ping(t)
			total += d
			if d > worst {
				worst = d
			}
		}
		t.Logf("MEASURED ctrl ping RTT: mean %s, worst %s (n=%d)",
			(total / n).Round(10*time.Microsecond), worst.Round(10*time.Microsecond), n)
	})

	t.Run("small request latency", func(t *testing.T) {
		const n = 20
		var total time.Duration
		for i := 0; i < n; i++ {
			res := v.get(t, uint32(100+i), "/api/items")
			if res.status != 200 {
				t.Fatalf("status %d", res.status)
			}
			total += res.total
		}
		t.Logf("MEASURED small GET (api/items, %d bytes): mean %s (n=%d)",
			290, (total / n).Round(10*time.Microsecond), n)
	})
}

// SSRF attempts have to be refused over the tunnel too (end to end).
func TestIntegrationSSRFOverTunnel(t *testing.T) {
	p, names, _ := siteProxy(t)
	v := itConnect(t, p, names)

	for i, path := range []string{"/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd"} {
		res := v.get(t, uint32(200+i), path)
		if res.status != 400 {
			t.Errorf("path %q: status %d, 400 was expected", path, res.status)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
