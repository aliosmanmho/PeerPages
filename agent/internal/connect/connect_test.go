package connect_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Bu dosya ajanin NOSTR YOLUNU uctan uca test eder: gercek bir (sahte) relay,
// gercek NIP-44 sifreleme, gercek WebRTC. Tarayici yok — ziyaretci tarafini
// pion ile taklit ediyoruz.
//
// Gercek internet relay'leri yerine sahte relay kullaniliyor: akisin kendisi
// deterministik dogrulanmali. Gercek relay'ler `peerpages doctor` ile ayrica
// olculuyor.

const settle = 30 * time.Second

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// echoProxy her istege sabit bir cevap doner.
type echoProxy struct{}

func (echoProxy) Serve(_ context.Context, req protocol.Req, _ io.Reader, sink tunnel.ResponseSink) {
	_ = sink.SendRes(200, map[string]string{"content-type": "text/plain"})
	_ = sink.Write([]byte("merhaba " + req.Path))
	_ = sink.End()
}

// visitor ziyaretci tarafini taklit eder: Nostr uzerinden signaling + WebRTC.
type visitor struct {
	t     *testing.T
	id    identity.Identity
	pool  *nostr.Pool
	codec codec
	agent string // ajan pubkey (hex)
	sid   string
	// target offer'da bildirilecek hedef adi; bos ise alan hic gonderilmez.
	target string
	pc     *webrtc.PeerConnection
	ctrl   *webrtc.DataChannel
	data   *webrtc.DataChannel
	events <-chan gonostr.RelayEvent

	mu     sync.Mutex
	ctrlCh chan protocol.Msg
	ready  chan struct{}
	once   sync.Once
}

type codec interface {
	Encode(m signaling.Msg) (string, error)
	Decode(payload string) (signaling.Msg, error)
}

type agentFixture struct {
	acceptor *connect.Acceptor
	id       identity.Identity
	relayURL string
	cancel   context.CancelFunc
	done     chan struct{}
}

// startAgent kabul akisini sahte relay uzerinde baslatir.
func startAgent(t *testing.T, relayURL, secret string) *agentFixture {
	t.Helper()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relayURL}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close("test bitti") })

	var sealer *signaling.Sealer
	if secret != "" {
		if sealer, err = signaling.NewSealer(secret); err != nil {
			t.Fatal(err)
		}
	}

	acceptor, err := connect.New(connect.Config{
		Identity: id,
		Pool:     pool,
		Sealer:   sealer,
		NewProxy: func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:  []string{"default"},
		// STUN yok: loopback'te host adaylari yeterli ve test hizli kalir.
		MaxSessions:   2,
		MaxConcurrent: 8,
		MaxBody:       1 << 20,
		Logger:        quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = acceptor.Run(ctx)
	}()

	// Aboneligin kurulmasina zaman tani: ephemeral event saklanmaz, ajan
	// dinlemiyorken yollanan offer kaybolur.
	time.Sleep(500 * time.Millisecond)

	return &agentFixture{acceptor: acceptor, id: id, relayURL: relayURL, cancel: cancel, done: done}
}

// newVisitor ziyaretci tarafini kurar: kanallari acar, Nostr'a abone olur.
func newVisitor(t *testing.T, a *agentFixture, secret string) *visitor {
	t.Helper()

	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{a.relayURL}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close("test bitti") })

	var c codec = (*signaling.Sealer)(nil)
	if secret != "" {
		s, serr := signaling.NewSealer(secret)
		if serr != nil {
			t.Fatal(serr)
		}
		c = s
	}

	sid, err := signaling.NewSID()
	if err != nil {
		t.Fatal(err)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	v := &visitor{
		t: t, id: id, pool: pool, codec: c,
		agent: a.id.PublicKey, sid: sid, pc: pc,
		ctrlCh: make(chan protocol.Msg, 64),
		ready:  make(chan struct{}),
	}

	// Kanallari ZIYARETCI aciyor.
	ordered := true
	if v.ctrl, err = pc.CreateDataChannel("ctrl", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		t.Fatal(err)
	}
	if v.data, err = pc.CreateDataChannel("data", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		t.Fatal(err)
	}

	v.ctrl.OnMessage(func(m webrtc.DataChannelMessage) {
		msg, perr := protocol.ParseCtrl(string(m.Data))
		if perr != nil {
			t.Errorf("gecersiz ctrl: %v", perr)
			return
		}
		v.ctrlCh <- msg
	})

	check := func() {
		if v.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
			v.data.ReadyState() == webrtc.DataChannelStateOpen {
			v.once.Do(func() { close(v.ready) })
		}
	}
	v.ctrl.OnOpen(check)
	v.data.OnOpen(check)

	// Kendi adresimize gelen signaling'i dinle.
	v.events = pool.Subscribe(ctx, nostr.SignalingFilter(id.PublicKey, time.Now().Add(-time.Minute)))
	time.Sleep(300 * time.Millisecond)

	// Yerel adaylari ajana yolla (trickle).
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		mid := "0"
		if j := cand.ToJSON(); j.SDPMid != nil && *j.SDPMid != "" {
			mid = *j.SDPMid
		}
		v.send(signaling.Msg{
			Type: signaling.TypeCandidate, SID: sid,
			Candidate: cand.ToJSON().Candidate, Mid: mid,
		})
	})

	return v
}

// send bir signaling mesajini ajana yollar (NIP-44 + sir katmani).
func (v *visitor) send(msg signaling.Msg) {
	payload, err := v.codec.Encode(msg)
	if err != nil {
		v.t.Errorf("mesaj kodlanamadi: %v", err)
		return
	}
	content, err := nostr.Encrypt(v.id.SecretKey, v.agent, payload)
	if err != nil {
		v.t.Errorf("NIP-44: %v", err)
		return
	}
	ev := nostr.NewSignalingEvent(v.agent, content)
	if err := ev.Sign(v.id.SecretKey); err != nil {
		v.t.Errorf("imza: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := v.pool.Publish(ctx, ev); err != nil {
		v.t.Errorf("yayin: %v", err)
	}
}

// offer offer uretir ve yayinlar (trickle: gathering beklenmez).
func (v *visitor) offer() {
	v.t.Helper()
	offer, err := v.pc.CreateOffer(nil)
	if err != nil {
		v.t.Fatal(err)
	}
	if err := v.pc.SetLocalDescription(offer); err != nil {
		v.t.Fatal(err)
	}
	v.send(signaling.Msg{
		Type: signaling.TypeOffer, SID: v.sid, SDP: offer.SDP, Target: v.target,
	})
}

// nextSignaling ajandan gelen bir sonraki signaling mesajini bekler.
func (v *visitor) nextSignaling(d time.Duration) (signaling.Msg, bool) {
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-v.events:
			if !ok {
				return signaling.Msg{}, false
			}
			if ev.Event == nil || ev.Event.PubKey != v.agent {
				continue
			}
			payload, err := nostr.Decrypt(v.id.SecretKey, v.agent, ev.Event.Content)
			if err != nil {
				continue
			}
			msg, err := v.codec.Decode(payload)
			if err != nil {
				continue
			}
			return msg, true
		case <-deadline:
			return signaling.Msg{}, false
		}
	}
}

// connect tam el sikismasini yurutur: offer -> answer -> candidate'lar.
func (v *visitor) connect(d time.Duration) {
	v.t.Helper()
	v.offer()

	deadline := time.After(d)
	answered := false

	for {
		select {
		case <-v.ready:
			return
		case <-deadline:
			v.t.Fatalf("tunel %s icinde acilmadi (answer alindi: %v)", d, answered)
			return
		default:
		}

		msg, ok := v.nextSignaling(2 * time.Second)
		if !ok {
			continue
		}
		switch msg.Type {
		case signaling.TypeAnswer:
			if msg.SID != v.sid {
				continue
			}
			if err := v.pc.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				v.t.Fatalf("answer uygulanamadi: %v", err)
			}
			answered = true
		case signaling.TypeCandidate:
			if msg.SID != v.sid {
				continue
			}
			mid := msg.Mid
			if err := v.pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate: msg.Candidate, SDPMid: &mid,
			}); err != nil {
				v.t.Logf("aday eklenemedi (zararsiz olabilir): %v", err)
			}
		case signaling.TypeBye:
			v.t.Fatalf("ajan bye yolladi: %s", msg.Reason)
		case signaling.TypeOffer:
			v.t.Error("ajandan offer gelmemeli")
		}
	}
}

func (v *visitor) nextCtrl(d time.Duration) (protocol.Msg, bool) {
	select {
	case m := <-v.ctrlCh:
		return m, true
	case <-time.After(d):
		return nil, false
	}
}

// --- testler ---

func TestNostrHandshakeOverFakeRelay(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")

	v.connect(settle)

	// Baglanti kurulunca ajan hello yollar.
	msg, ok := v.nextCtrl(settle)
	if !ok {
		t.Fatal("hello gelmedi")
	}
	hello, isHello := msg.(protocol.Hello)
	if !isHello {
		t.Fatalf("ilk mesaj hello olmali, geldi: %s", msg.Type())
	}
	if hello.Auth != protocol.AuthNone {
		t.Errorf("sir yokken auth none olmali, geldi: %q", hello.Auth)
	}

	if got := a.acceptor.SessionCount(); got != 1 {
		t.Errorf("acik oturum sayisi %d, beklenen 1", got)
	}
}

// Paylasilan sir: iki taraf da sirri biliyorsa baglanti kurulur ve hello
// auth=required bildirir.
func TestNostrHandshakeWithSecret(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	const secret = "kedi-balik-7"
	a := startAgent(t, relay.URL(), secret)
	v := newVisitor(t, a, secret)

	v.connect(settle)

	msg, ok := v.nextCtrl(settle)
	if !ok {
		t.Fatal("hello gelmedi")
	}
	hello, isHello := msg.(protocol.Hello)
	if !isHello {
		t.Fatalf("hello beklenirdi: %s", msg.Type())
	}
	if hello.Auth != protocol.AuthRequired {
		t.Errorf("sir varken auth required olmali, geldi: %q", hello.Auth)
	}
}

// peer-id'yi bilip sirri bilmeyen biri BAGLANAMAZ (README bolum 4).
func TestSecretlessVisitorRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "dogru-sir")
	v := newVisitor(t, a, "") // sir yok

	v.offer()

	// Ajan cevap vermemeli ve oturum acmamali.
	if msg, ok := v.nextSignaling(5 * time.Second); ok {
		t.Errorf("ajan cevap vermemeliydi, geldi: %s", msg.Type)
	}
	if got := a.acceptor.SessionCount(); got != 0 {
		t.Errorf("oturum acilmamaliydi, acik: %d", got)
	}
}

// Yanlis sirla gelen de reddedilir.
func TestWrongSecretRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "dogru-sir")
	v := newVisitor(t, a, "yanlis-sir")

	v.offer()

	if msg, ok := v.nextSignaling(5 * time.Second); ok {
		t.Errorf("ajan cevap vermemeliydi, geldi: %s", msg.Type)
	}
	if got := a.acceptor.SessionCount(); got != 0 {
		t.Errorf("oturum acilmamaliydi, acik: %d", got)
	}
}

// Tunel kurulduktan sonra istek gercekten proxy'ye ulasiyor.
func TestRequestOverNostrTunnel(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	if _, ok := v.nextCtrl(settle); !ok {
		t.Fatal("hello gelmedi")
	}

	req := protocol.Req{
		ID: 1, Method: "GET", Path: "/deneme",
		Headers: map[string]string{}, HasBody: false,
	}
	if err := v.ctrl.SendText(req.Canonical()); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(settle)
	for {
		msg, ok := v.nextCtrl(5 * time.Second)
		if !ok {
			select {
			case <-deadline:
				t.Fatal("res gelmedi")
			default:
				continue
			}
		}
		if res, isRes := msg.(protocol.Res); isRes {
			if res.Status != 200 {
				t.Errorf("status %d", res.Status)
			}
			return
		}
	}
}

// Oturum tavani asilinca ajan bye ile reddediyor.
func TestSessionLimit(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "") // MaxSessions: 2

	v1 := newVisitor(t, a, "")
	v1.connect(settle)
	v2 := newVisitor(t, a, "")
	v2.connect(settle)

	if got := a.acceptor.SessionCount(); got != 2 {
		t.Fatalf("iki oturum beklenirdi, acik: %d", got)
	}

	// Ucuncu ziyaretci reddedilmeli.
	v3 := newVisitor(t, a, "")
	v3.offer()

	deadline := time.After(settle)
	for {
		msg, ok := v3.nextSignaling(3 * time.Second)
		if !ok {
			select {
			case <-deadline:
				t.Fatal("ucuncu ziyaretciye bye gelmedi")
			default:
				continue
			}
		}
		if msg.Type == signaling.TypeBye {
			if msg.Reason != "too-many-sessions" {
				t.Errorf("bye nedeni: %q", msg.Reason)
			}
			return
		}
		if msg.Type == signaling.TypeAnswer {
			t.Fatal("tavan asilmisken answer gelmemeliydi")
		}
	}
}

// bye gelince oturum kapaniyor ve kaynak birakiliyor.
func TestByeClosesSession(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	if got := a.acceptor.SessionCount(); got != 1 {
		t.Fatalf("bir oturum beklenirdi: %d", got)
	}

	v.send(signaling.Msg{Type: signaling.TypeBye, SID: v.sid, Reason: "visitor-left"})

	deadline := time.Now().Add(settle)
	for time.Now().Before(deadline) {
		if a.acceptor.SessionCount() == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("bye sonrasi oturum kapanmadi, acik: %d", a.acceptor.SessionCount())
}

// Ayni sid ile ikinci offer yeni oturum acmaz.
func TestDuplicateOfferIgnored(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startAgent(t, relay.URL(), "")
	v := newVisitor(t, a, "")
	v.connect(settle)

	before := a.acceptor.SessionCount()
	// Ayni sid, yeni event (farkli id, yani replay degil).
	v.send(signaling.Msg{Type: signaling.TypeOffer, SID: v.sid, SDP: "v=0\r\nbozuk\r\n"})
	time.Sleep(2 * time.Second)

	if got := a.acceptor.SessionCount(); got != before {
		t.Errorf("oturum sayisi degisti: %d -> %d", before, got)
	}
}

func TestConfigValidation(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	pool, err := nostr.NewPool(context.Background(),
		nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test")

	ok := connect.Config{
		Identity:      id,
		Pool:          pool,
		NewProxy:      func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:       []string{"default"},
		MaxSessions:   1,
		MaxConcurrent: 1,
		MaxBody:       1,
		Logger:        quietLogger(),
	}
	if _, err := connect.New(ok); err != nil {
		t.Fatalf("gecerli yapilandirma reddedildi: %v", err)
	}

	bad := map[string]func(*connect.Config){
		"kimlik yok":        func(c *connect.Config) { c.Identity = identity.Identity{} },
		"pool nil":          func(c *connect.Config) { c.Pool = nil },
		"proxy nil":         func(c *connect.Config) { c.NewProxy = nil },
		"hedef yok":         func(c *connect.Config) { c.Targets = nil },
		"maxsessions sifir": func(c *connect.Config) { c.MaxSessions = 0 },
		"concurrent sifir":  func(c *connect.Config) { c.MaxConcurrent = 0 },
		"maxbody sifir":     func(c *connect.Config) { c.MaxBody = 0 },
		"logger nil":        func(c *connect.Config) { c.Logger = nil },
	}
	for name, mutate := range bad {
		cfg := ok
		mutate(&cfg)
		if _, err := connect.New(cfg); err == nil {
			t.Errorf("%s: reddedilmeliydi", name)
		}
	}
}
