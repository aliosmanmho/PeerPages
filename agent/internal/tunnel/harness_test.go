package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Bu dosya, ziyaretci tarayicisinin yerine gecen bir pion peer'i kurar.
// Boylece tunnel + sink + flowControl, tarayici olmadan gercek bir WebRTC
// baglantisi uzerinde test edilebilir. Faz 1.3'te gercek reverse proxy de
// ayni kosumu kullanir.

// visitor ziyaretci tarafini taklit eder.
type visitor struct {
	t  *testing.T
	pc *webrtc.PeerConnection

	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	mu     sync.Mutex
	ctrlCh chan protocol.Msg
	dataCh chan protocol.Frame

	openOnce sync.Once
	ready    chan struct{}
}

// proxyFunc Proxy arayuzunu fonksiyon olarak uygulamayi saglar.
type proxyFunc func(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink)

func (f proxyFunc) Serve(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink) {
	f(ctx, req, body, sink)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// connect ajan oturumu ile ziyaretciyi baglar (elle signaling akisinin
// otomatiklestirilmis hali).
func connect(t *testing.T, cfg Config) (*Session, *visitor) {
	t.Helper()

	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = 8
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 1 << 20
	}
	if len(cfg.Targets) == 0 {
		cfg.Targets = []string{"default"}
	}
	if cfg.Auth == "" {
		cfg.Auth = protocol.AuthNone
	}

	sess, err := New(cfg)
	if err != nil {
		t.Fatalf("oturum kurulamadi: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	v := newVisitor(t)
	t.Cleanup(func() { _ = v.pc.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	offer, err := sess.Offer(ctx)
	if err != nil {
		t.Fatalf("offer uretilemedi: %v", err)
	}
	answer := v.answer(ctx, offer)
	if err := sess.Accept(answer); err != nil {
		t.Fatalf("answer uygulanamadi: %v", err)
	}

	if err := sess.WaitReady(ctx, 20*time.Second); err != nil {
		t.Fatalf("ajan kanallari acilmadi: %v", err)
	}
	v.waitReady(t, 20*time.Second)

	return sess, v
}

func newVisitor(t *testing.T) *visitor {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("ziyaretci peer connection kurulamadi: %v", err)
	}

	v := &visitor{
		t:      t,
		pc:     pc,
		ctrlCh: make(chan protocol.Msg, 256),
		dataCh: make(chan protocol.Frame, 4096),
		ready:  make(chan struct{}),
	}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "ctrl":
			v.mu.Lock()
			v.ctrl = dc
			v.mu.Unlock()
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				msg, err := protocol.ParseCtrl(string(m.Data))
				if err != nil {
					t.Errorf("ziyaretci gecersiz ctrl aldi: %v (%s)", err, m.Data)
					return
				}
				v.ctrlCh <- msg
			})
		case "data":
			v.mu.Lock()
			v.data = dc
			v.mu.Unlock()
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				f, err := protocol.DecodeFrame(m.Data)
				if err != nil {
					t.Errorf("ziyaretci gecersiz cerceve aldi: %v", err)
					return
				}
				v.dataCh <- f
			})
		default:
			t.Errorf("beklenmeyen kanal: %q", dc.Label())
		}
		dc.OnOpen(v.maybeReady)
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			v.maybeReady()
		}
	})

	return v
}

func (v *visitor) maybeReady() {
	v.mu.Lock()
	both := v.ctrl != nil && v.data != nil &&
		v.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
		v.data.ReadyState() == webrtc.DataChannelStateOpen
	v.mu.Unlock()
	if both {
		v.openOnce.Do(func() { close(v.ready) })
	}
}

func (v *visitor) answer(ctx context.Context, offer webrtc.SessionDescription) webrtc.SessionDescription {
	v.t.Helper()
	if err := v.pc.SetRemoteDescription(offer); err != nil {
		v.t.Fatalf("offer uygulanamadi: %v", err)
	}
	answer, err := v.pc.CreateAnswer(nil)
	if err != nil {
		v.t.Fatalf("answer uretilemedi: %v", err)
	}
	gathered := webrtc.GatheringCompletePromise(v.pc)
	if err := v.pc.SetLocalDescription(answer); err != nil {
		v.t.Fatalf("yerel SDP ayarlanamadi: %v", err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		v.t.Fatalf("ziyaretci ICE toplanmasi: %v", ctx.Err())
	}
	local := v.pc.LocalDescription()
	if local == nil {
		v.t.Fatal("ziyaretci yerel SDP bos")
	}
	return *local
}

func (v *visitor) waitReady(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-v.ready:
	case <-time.After(d):
		t.Fatalf("ziyaretci kanallari %s icinde acilmadi", d)
	}
}

func (v *visitor) sendCtrl(m protocol.Msg) error {
	v.mu.Lock()
	dc := v.ctrl
	v.mu.Unlock()
	if dc == nil {
		return errors.New("ctrl kanali yok")
	}
	return dc.SendText(m.Canonical())
}

func (v *visitor) sendFrame(id, seq uint32, payload []byte) error {
	v.mu.Lock()
	dc := v.data
	v.mu.Unlock()
	if dc == nil {
		return errors.New("data kanali yok")
	}
	f, err := protocol.EncodeFrame(id, seq, payload)
	if err != nil {
		return err
	}
	return dc.Send(f)
}

// nextCtrl bir sonraki ctrl mesajini bekler.
func (v *visitor) nextCtrl(t *testing.T, d time.Duration) protocol.Msg {
	t.Helper()
	select {
	case m := <-v.ctrlCh:
		return m
	case <-time.After(d):
		t.Fatalf("ctrl mesaji %s icinde gelmedi", d)
		return nil
	}
}

// collect id'si verilen istegin cevabini toplar: res, govde, end/abort.
//
// DIKKAT: `end` tek basina "govde bitti" demek degil. ctrl ve data ayri SCTP
// stream'leri oldugundan end, cercevelerden once varabilir; bu yuzden
// end'teki frames/len sayaclari tamamlanana kadar data dinlenmeye devam eder
// (protocol/README.md). Tarayici tarafi da ayni kurali uygular.
type collected struct {
	res     *protocol.Res
	body    []byte
	ended   bool
	aborted string
	// end mesajindan SONRA gelen cerceve sayisi; siralama olmadiginin kaniti.
	lateFrames int
}

func (v *visitor) collect(t *testing.T, id uint32, d time.Duration) collected {
	t.Helper()
	var out collected
	deadline := time.After(d)
	nextSeq := uint32(0)

	var expectFrames uint32
	var expectLen int64
	done := func() bool {
		return out.ended && nextSeq == expectFrames && int64(len(out.body)) == expectLen
	}

	for {
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.Res:
				if msg.ID == id {
					r := msg
					out.res = &r
				}
			case protocol.End:
				if msg.ID == id {
					out.ended = true
					expectFrames = msg.Frames
					expectLen = msg.Len
					if done() {
						return out
					}
				}
			case protocol.Abort:
				if msg.ID == id {
					out.aborted = msg.Reason
					return out
				}
			case protocol.Hello, protocol.Ping, protocol.Pong:
				// yok say
			default:
				t.Errorf("beklenmeyen ctrl mesaji: %s", m.Type())
			}
		case f := <-v.dataCh:
			if f.ID != id {
				continue
			}
			if f.Seq != nextSeq {
				t.Errorf("beklenmeyen seq: %d, beklenen %d", f.Seq, nextSeq)
			}
			nextSeq++
			out.body = append(out.body, f.Payload...)
			if out.ended {
				out.lateFrames++
			}
			if done() {
				return out
			}
		case <-deadline:
			t.Fatalf("istek %d icin cevap %s icinde tamamlanmadi (res=%v ended=%v %d bayt, beklenen %d cerceve/%d bayt)",
				id, d, out.res != nil, out.ended, len(out.body), expectFrames, expectLen)
			return out
		}
	}
}
