package tunnel

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

const settle = 15 * time.Second

func TestConfigValidate(t *testing.T) {
	ok := Config{
		Targets:       []string{"default"},
		MaxConcurrent: 1,
		MaxBody:       1,
		Proxy:         proxyFunc(func(context.Context, protocol.Req, io.Reader, ResponseSink) {}),
		Logger:        quietLogger(),
	}
	if err := ok.validate(); err != nil {
		t.Fatalf("gecerli yapilandirma reddedildi: %v", err)
	}

	bad := map[string]func(*Config){
		"hedef yok":        func(c *Config) { c.Targets = nil },
		"concurrent sifir": func(c *Config) { c.MaxConcurrent = 0 },
		"maxbody sifir":    func(c *Config) { c.MaxBody = 0 },
		"proxy nil":        func(c *Config) { c.Proxy = nil },
		"logger nil":       func(c *Config) { c.Logger = nil },
	}
	for name, mutate := range bad {
		c := ok
		mutate(&c)
		if err := c.validate(); err == nil {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}

	// ICE sunucusu bos olabilir: yalnizca host adaylari toplanir.
	c := ok
	c.ICEServers = nil
	if err := c.validate(); err != nil {
		t.Errorf("bos ICE listesi kabul edilmeliydi: %v", err)
	}
}

// Baglanti kurulunca ajan hello yollar.
func TestHelloOnConnect(t *testing.T) {
	_, v := connect(t, Config{
		Targets: []string{"default", "admin"},
		Auth:    protocol.AuthRequired,
		Proxy:   proxyFunc(func(context.Context, protocol.Req, io.Reader, ResponseSink) {}),
	})

	msg := v.nextCtrl(t, settle)
	hello, ok := msg.(protocol.Hello)
	if !ok {
		t.Fatalf("ilk mesaj hello olmali, geldi: %s", msg.Type())
	}
	if hello.V != protocol.Version {
		t.Errorf("hello.v = %d", hello.V)
	}
	if strings.Join(hello.Targets, ",") != "default,admin" {
		t.Errorf("hello.targets = %v", hello.Targets)
	}
	if hello.Auth != protocol.AuthRequired {
		t.Errorf("hello.auth = %q", hello.Auth)
	}
}

func TestPingPong(t *testing.T) {
	_, v := connect(t, Config{
		Proxy: proxyFunc(func(context.Context, protocol.Req, io.Reader, ResponseSink) {}),
	})
	if _, ok := v.nextCtrl(t, settle).(protocol.Hello); !ok {
		t.Fatal("hello beklenirdi")
	}

	if err := v.sendCtrl(protocol.Ping{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.nextCtrl(t, settle).(protocol.Pong); !ok {
		t.Error("ping'e pong gelmeliydi")
	}
}

func TestRequestResponseRoundtrip(t *testing.T) {
	want := bytes.Repeat([]byte("PeerPages"), 10_000) // ~90 KB, coklu cerceve

	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, req protocol.Req, _ io.Reader, sink ResponseSink) {
			if req.Path != "/büyük" {
				t.Errorf("path = %q", req.Path)
			}
			if err := sink.SendRes(200, map[string]string{"content-type": "text/plain"}); err != nil {
				t.Errorf("SendRes: %v", err)
			}
			if err := sink.Write(want); err != nil {
				t.Errorf("Write: %v", err)
			}
			if err := sink.End(); err != nil {
				t.Errorf("End: %v", err)
			}
		}),
	})
	v.nextCtrl(t, settle) // hello

	req := protocol.Req{ID: 1, Method: "GET", Path: "/büyük", Headers: map[string]string{}, HasBody: false}
	if err := v.sendCtrl(req); err != nil {
		t.Fatal(err)
	}

	got := v.collect(t, 1, settle)
	if got.aborted != "" {
		t.Fatalf("abort geldi: %s", got.aborted)
	}
	if got.res == nil || got.res.Status != 200 {
		t.Fatalf("res yanlis: %#v", got.res)
	}
	if got.res.Headers["content-type"] != "text/plain" {
		t.Errorf("content-type = %q", got.res.Headers["content-type"])
	}
	if !got.ended {
		t.Error("end gelmedi")
	}
	if !bytes.Equal(got.body, want) {
		t.Errorf("govde farkli: %d bayt geldi, %d beklenen", len(got.body), len(want))
	}
	// Kanallar arasinda siralama yok: cerceveler end'den sonra da gelebilir.
	// Testin gecmesi, alicinin frames/len sayaclarini dogru kullandigini gosterir.
	t.Logf("end'den sonra gelen cerceve sayisi: %d", got.lateFrames)
}

// Istek govdesi: ziyaretci data cerceveleri yollar, ajan tarafinda okunur.
func TestRequestBodyStreaming(t *testing.T) {
	body := bytes.Repeat([]byte("x"), protocol.MaxPayload*2+123)
	gotBody := make(chan []byte, 1)

	_, v := connect(t, Config{
		MaxBody: int64(len(body)) + 1,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			b, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("govde okunamadi: %v", err)
			}
			gotBody <- b
			_ = sink.SendRes(201, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{
		ID: 5, Method: "POST", Path: "/api", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}
	for seq, off := uint32(0), 0; off < len(body); seq, off = seq+1, off+protocol.MaxPayload {
		end := off + protocol.MaxPayload
		if end > len(body) {
			end = len(body)
		}
		if err := v.sendFrame(5, seq, body[off:end]); err != nil {
			t.Fatal(err)
		}
	}
	frames := uint32((len(body) + protocol.MaxPayload - 1) / protocol.MaxPayload)
	if err := v.sendCtrl(protocol.End{ID: 5, Frames: frames, Len: int64(len(body))}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-gotBody:
		if !bytes.Equal(got, body) {
			t.Errorf("govde farkli: %d bayt, beklenen %d", len(got), len(body))
		}
	case <-time.After(settle):
		t.Fatal("govde zamaninda gelmedi")
	}

	res := v.collect(t, 5, settle)
	if res.res == nil || res.res.Status != 201 {
		t.Errorf("res yanlis: %#v", res.res)
	}
}

// README ilke 4: govde boyutu tavani.
func TestRequestBodyTooLarge(t *testing.T) {
	_, v := connect(t, Config{
		MaxBody: 1024,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			if _, err := io.ReadAll(r); err == nil {
				t.Error("govde okumasi hata vermeliydi")
			}
			_ = sink.Abort("body-too-large")
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{
		ID: 9, Method: "POST", Path: "/api", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Tavanin uzerinde govde yolla.
	if err := v.sendFrame(9, 0, bytes.Repeat([]byte("y"), 2048)); err != nil {
		t.Fatal(err)
	}

	got := v.collect(t, 9, settle)
	if got.aborted == "" {
		t.Error("abort beklenirdi")
	}
}

// Kanal ordered: beklenmeyen seq protokol hatasi.
func TestBadSeqAborts(t *testing.T) {
	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			if r != nil {
				_, _ = io.Copy(io.Discard, r)
			}
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{
		ID: 11, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}
	// seq 0 atlanip 3 yollaniyor.
	if err := v.sendFrame(11, 3, []byte("veri")); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		select {
		case m := <-v.ctrlCh:
			if ab, ok := m.(protocol.Abort); ok && ab.ID == 11 {
				if ab.Reason != "bad-seq" {
					t.Errorf("abort nedeni %q, beklenen bad-seq", ab.Reason)
				}
				return
			}
		case <-deadline:
			t.Fatal("bad-seq icin abort gelmedi")
		}
	}
}

// Ayni id ile ikinci istek reddedilir.
func TestDuplicateIDAborts(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			<-release // istek askida kalsin
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	req := protocol.Req{ID: 7, Method: "GET", Path: "/", Headers: map[string]string{}, HasBody: false}
	if err := v.sendCtrl(req); err != nil {
		t.Fatal(err)
	}
	// Ilk istegin kayda girmesini bekle.
	waitForRes(t, v, 7)
	if err := v.sendCtrl(req); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		select {
		case m := <-v.ctrlCh:
			if ab, ok := m.(protocol.Abort); ok && ab.ID == 7 {
				if ab.Reason != "duplicate-id" {
					t.Errorf("abort nedeni %q", ab.Reason)
				}
				return
			}
		case <-deadline:
			t.Fatal("duplicate-id icin abort gelmedi")
		}
	}
}

// README ilke 4: eszamanli istek tavani.
func TestMaxConcurrent(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	_, v := connect(t, Config{
		MaxConcurrent: 1,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			<-release
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{ID: 1, Method: "GET", Path: "/", Headers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	waitForRes(t, v, 1)

	if err := v.sendCtrl(protocol.Req{ID: 2, Method: "GET", Path: "/", Headers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		select {
		case m := <-v.ctrlCh:
			if ab, ok := m.(protocol.Abort); ok && ab.ID == 2 {
				if ab.Reason != "too-many-requests" {
					t.Errorf("abort nedeni %q", ab.Reason)
				}
				return
			}
		case <-deadline:
			t.Fatal("too-many-requests icin abort gelmedi")
		}
	}
}

// Proxy akisi kapatmadan donerse ziyaretci askida kalmamali.
func TestProxyThatDoesNotCloseGetsAborted(t *testing.T) {
	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(200, map[string]string{})
			// Bilerek ne End ne Abort.
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{ID: 3, Method: "GET", Path: "/", Headers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	got := v.collect(t, 3, settle)
	if got.aborted != "agent-did-not-close" {
		t.Errorf("abort nedeni %q, beklenen agent-did-not-close", got.aborted)
	}
}

// Bozuk ctrl mesaji baglantiyi dusurmemeli.
func TestGarbageCtrlDoesNotKillSession(t *testing.T) {
	sess, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, _ io.Reader, sink ResponseSink) {
			_ = sink.SendRes(204, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	v.mu.Lock()
	dc := v.ctrl
	v.mu.Unlock()
	for _, junk := range []string{"", "{", "null", `{"t":"zipzip"}`, `{"t":"res","id":1,"status":200,"headers":{}}`} {
		if err := dc.SendText(junk); err != nil {
			t.Fatal(err)
		}
	}

	// Oturum hala calisiyor olmali.
	if err := v.sendCtrl(protocol.Req{ID: 4, Method: "GET", Path: "/", Headers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	got := v.collect(t, 4, settle)
	if got.res == nil || got.res.Status != 204 {
		t.Errorf("oturum bozuldu: %#v", got)
	}
	select {
	case <-sess.Done():
		t.Error("oturum kapanmamaliydi")
	default:
	}
}

func waitForRes(t *testing.T, v *visitor, id uint32) {
	t.Helper()
	deadline := time.After(settle)
	for {
		select {
		case m := <-v.ctrlCh:
			if res, ok := m.(protocol.Res); ok && res.ID == id {
				return
			}
		case <-deadline:
			t.Fatalf("istek %d icin res gelmedi", id)
		}
	}
}

// Kanallar arasinda siralama olmadigi icin govde cerceveleri `req`'ten ONCE
// gelebilir. Ajan bunlari tamponlayip `req` gelince sirasiyla islemeli.
func TestFramesBeforeReqAreBuffered(t *testing.T) {
	body := bytes.Repeat([]byte("z"), protocol.MaxPayload+500)
	gotBody := make(chan []byte, 1)

	_, v := connect(t, Config{
		MaxBody: int64(len(body)) + 1,
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			b, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("govde okunamadi: %v", err)
			}
			gotBody <- b
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	// ONCE cerceveler ve end, SONRA req.
	frames := uint32(0)
	for off := 0; off < len(body); off += protocol.MaxPayload {
		end := off + protocol.MaxPayload
		if end > len(body) {
			end = len(body)
		}
		if err := v.sendFrame(21, frames, body[off:end]); err != nil {
			t.Fatal(err)
		}
		frames++
	}
	if err := v.sendCtrl(protocol.End{ID: 21, Frames: frames, Len: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	// Cercevelerin varmasina zaman taniyalim ki req gercekten sonra gelsin.
	time.Sleep(150 * time.Millisecond)
	if err := v.sendCtrl(protocol.Req{
		ID: 21, Method: "POST", Path: "/sonra", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-gotBody:
		if !bytes.Equal(got, body) {
			t.Errorf("erken cerceveler kaybedildi: %d bayt geldi, %d beklenen", len(got), len(body))
		}
	case <-time.After(settle):
		t.Fatal("govde gelmedi: erken cerceveler tamponlanmiyor")
	}
}

// `end` govde cercevelerinden once gelirse akis erken kapanmamali.
func TestEndBeforeFramesWaitsForCounters(t *testing.T) {
	body := []byte("gecikmeli govde")
	gotBody := make(chan []byte, 1)

	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			b, _ := io.ReadAll(r)
			gotBody <- b
			_ = sink.SendRes(200, map[string]string{})
			_ = sink.End()
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{
		ID: 23, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}
	// end ONCE.
	if err := v.sendCtrl(protocol.End{ID: 23, Frames: 1, Len: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	// Cerceve SONRA.
	if err := v.sendFrame(23, 0, body); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-gotBody:
		if !bytes.Equal(got, body) {
			t.Errorf("end erken kapatti: %q geldi, %q beklenen", got, body)
		}
	case <-time.After(settle):
		t.Fatal("govde gelmedi")
	}
}

// Bildirilen uzunluktan fazla govde gelirse protokol hatasi.
func TestBodyLengthMismatchAborts(t *testing.T) {
	_, v := connect(t, Config{
		Proxy: proxyFunc(func(_ context.Context, _ protocol.Req, r io.Reader, sink ResponseSink) {
			if _, err := io.ReadAll(r); err == nil {
				t.Error("govde okumasi hata vermeliydi")
			}
			_ = sink.Abort("mismatch")
		}),
	})
	v.nextCtrl(t, settle)

	if err := v.sendCtrl(protocol.Req{
		ID: 25, Method: "POST", Path: "/", Headers: map[string]string{}, HasBody: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 5 bayt bildirip 10 bayt yolla.
	if err := v.sendCtrl(protocol.End{ID: 25, Frames: 1, Len: 5}); err != nil {
		t.Fatal(err)
	}
	if err := v.sendFrame(25, 0, []byte("0123456789")); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		select {
		case m := <-v.ctrlCh:
			if ab, ok := m.(protocol.Abort); ok && ab.ID == 25 {
				if ab.Reason != "body-length-mismatch" && ab.Reason != "mismatch" {
					t.Errorf("abort nedeni %q", ab.Reason)
				}
				return
			}
		case <-deadline:
			t.Fatal("uzunluk uyusmazligi icin abort gelmedi")
		}
	}
}
