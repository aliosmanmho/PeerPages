// Package tunnel ajan tarafindaki WebRTC oturumunu ve ctrl/data kanallarini
// yonetir.
//
// Ajan ANSWERER'dir: offer'i ziyaretci yayinlar, ajan answer doner.
// Kanallari (`ctrl` ve `data`) ziyaretci acar, ajan `OnDataChannel` ile alir.
//
// Neden boyle: Nostr'da ajan, ziyaretcinin gecici pubkey'ini onceden bilemez,
// dolayisiyla ilk mesaji o gonderemez (kime sifreleyecegini bilemez). Gelen
// taraf offer'i yayinlar. Karar ve gerekcesi README bolum 8'de. Elle signaling
// yolu (`--manual`) da ayni yonu kullanir: tek bir kod yolu var.
//
// Istegin HTTP tarafi burada DEGIL: bu paket yalnizca protokolu konusur ve
// isleri Proxy arayuzune devreder (internal/proxy). Boylece reverse proxy,
// WebRTC olmadan test edilebilir.
//
// # Kanallar arasinda siralama yoktur
//
// `ctrl` ve `data` iki ayri SCTP stream'idir; her biri kendi icinde ordered
// ama ARALARINDA siralama garantisi yok (Faz 1'de olculdu, protocol/README.md).
// Bunun iki sonucu var, ikisi de burada ele aliniyor:
//
//  1. Bir istegin govde cerceveleri `req` mesajindan ONCE gelebilir. Sahibi
//     henuz bilinmeyen cerceveler kisa sureli bir tamponda (s.early) tutulur
//     ve `req` gelince sirasiyla islenir.
//  2. `end` mesaji govde cercevelerinden ONCE gelebilir. Bu yuzden `end` tek
//     basina "govde bitti" demek degil: `frames`/`len` sayaclari tamamlanana
//     kadar beklenir.
//
// Her istegin govde olaylari (cerceve / end / abort) tek bir kuyruktan tek bir
// goroutine tarafindan islenir; boylece sira karismaz.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// ResponseSink bir istege verilecek cevabi tunele yazar.
//
// Cagri sirasi: SendRes -> Write* -> End. Hata halinde Abort.
type ResponseSink interface {
	// SendRes cevap basligini yollar (ctrl `res`).
	SendRes(status int, headers map[string]string) error
	// Write govdenin bir parcasini yollar; 16 KiB'lik cercevelere bolunur ve
	// backpressure uygulanir.
	Write(p []byte) error
	// End govdenin bittigini bildirir (ctrl `end`, frames/len sayaclariyla).
	End() error
	// Abort akisi iptal eder (ctrl `abort`).
	Abort(reason string) error
}

// Proxy bir ctrl `req` mesajini isler.
//
// body istek govdesidir; hasBody false ise nil olur. Uygulama cevabi sink'e
// yazar ve hata durumunda sink.Abort cagirir.
type Proxy interface {
	Serve(ctx context.Context, req protocol.Req, body io.Reader, sink ResponseSink)
}

// Backpressure esikleri ve tampon sinirlari.
const (
	// bufferedHigh data channel tamponu bu degerin uzerindeyse gonderim bekler.
	bufferedHigh = 1 << 20 // 1 MiB
	// bufferedLow tampon bu degere dusunce gonderim devam eder.
	bufferedLow = 256 << 10 // 256 KiB
	// bodyQueueSlack istek basina govde olay kuyrugunun temel kapasitesi.
	bodyQueueSlack = 64
	// maxEarlyIDs sahibi henuz bilinmeyen kac id icin cerceve tutulur.
	maxEarlyIDs = 64
	// earlyTTL sahipsiz erken cercevelerin tamponda kalma suresi.
	earlyTTL = 30 * time.Second
)

// Bosta kalma denetimi (README Faz 3: ziyaretci gidince ajan kaynak
// sizdirmasin).
const (
	// DefaultIdleTimeout hic trafik gelmeden gecen bu sureden sonra oturum
	// kapatilir.
	//
	// Neden gerekli: tek basina WebRTC durum degisikligine guvenemiyoruz.
	// Sayfanin pagehide'da gonderdigi haber EN IYI CABADIR — sekme cokerse,
	// oldurulurse ya da isletim sistemi sekmeyi atarsa hic gelmez. ICE'in
	// Disconnected'a gecmesi ise 30 saniyeden cok daha uzun surebilir ve
	// geri de donebilir.
	DefaultIdleTimeout = 30 * time.Second
	// DefaultPingInterval ajanin canlilik icin ctrl ping yollama araligi.
	//
	// Ziyaretci sayfasi gelen ping'e pong donuyor (web/src/tunnel.ts), yani
	// protokol degisikligi gerekmiyor: sessiz ama acik bir tunelde bile
	// lastSeen tazelenir.
	DefaultPingInterval = 10 * time.Second
	// DefaultDisconnectGrace ICE "Disconnected"a dustukten sonra geri
	// donmesi icin verilen sure.
	//
	// Neden sure veriyoruz: Disconnected GECICI bir durumdur, pion sik sik
	// kendi kendine Connected'a geri doner (yukaridaki yorum da bunu
	// soyluyor). Faz 3'e kadar bu durum oturumu ANINDA kapatiyordu, ustelik
	// temiz kapanis gibi raporlayarak; kisa bir ag kesintisi tuneli
	// gereksiz yere oldurmus oluyordu.
	DefaultDisconnectGrace = 10 * time.Second
	// resumeFactor uyanma tespiti: iki tik arasi, beklenen araligin bu
	// katindan fazlaysa surecin askiya alindigi varsayilir.
	resumeFactor = 3
)

// ErrIdle oturum bosta kaldigi icin kapatildi.
var ErrIdle = errors.New("oturum bosta kaldi: ziyaretci yanit vermiyor")

// ErrDisconnected ICE baglantisi geri donmedigi icin kapatildi.
var ErrDisconnected = errors.New("WebRTC baglantisi koptu ve geri donmedi")

// Config oturum yapilandirmasi.
type Config struct {
	// ICEServers ICE icin kullanilacak STUN/TURN sunuculari. Bos ise
	// yalnizca host adaylari toplanir (loopback testleri).
	//
	// Hazir liste aliyoruz (ice.Config.Servers()), cunku TURN kimlik
	// bilgisi sunucu girdisi basinadir ve kimlik bilgisini bu katmana
	// kadar tasimanin bir faydasi yok.
	ICEServers []webrtc.ICEServer
	// ICEPolicy aday politikasi; sifir deger "all" demektir.
	ICEPolicy webrtc.ICETransportPolicy
	// Targets hello mesajinda bildirilecek hedef adlari.
	Targets []string
	// Auth hello mesajindaki auth degeri.
	Auth protocol.AuthMode
	// MaxConcurrent ayni anda islenecek en fazla istek.
	MaxConcurrent int
	// MaxBody ziyaretciden kabul edilecek en buyuk istek govdesi.
	MaxBody int64
	// Proxy istekleri isleyen katman.
	Proxy Proxy
	// Logger zorunlu.
	Logger *slog.Logger
	// Verbose loglarda path maskelemesini kapatir.
	Verbose bool
	// Trickle true ise Answer ICE toplanmasini BEKLEMEZ; adaylar
	// OnLocalCandidate ile tek tek akar (Nostr yolu). false ise Answer tam
	// SDP dondurur, cunku elle signaling'de tek parca kopyalanacak.
	Trickle bool
	// IdleTimeout bosta kalma siniri; 0 ise DefaultIdleTimeout.
	IdleTimeout time.Duration
	// PingInterval canlilik ping'i araligi; 0 ise DefaultPingInterval.
	//
	// Testler bu ikisini kisaltarak denetimi hizlandirir.
	PingInterval time.Duration
	// DisconnectGrace ICE Disconnected'dan geri donmesi icin beklenen sure;
	// 0 ise DefaultDisconnectGrace.
	DisconnectGrace time.Duration
}

func (c *Config) validate() error {
	switch {
	case len(c.Targets) == 0:
		return errors.New("tunnel: hedef listesi bos")
	case c.MaxConcurrent < 1:
		return errors.New("tunnel: MaxConcurrent en az 1 olmali")
	case c.MaxBody < 1:
		return errors.New("tunnel: MaxBody en az 1 olmali")
	case c.Proxy == nil:
		return errors.New("tunnel: Proxy nil")
	case c.Logger == nil:
		return errors.New("tunnel: Logger nil")
	}
	return nil
}

// bodyEvent bir istegin govde kuyrugundaki olay. Tam olarak bir alani dolu.
type bodyEvent struct {
	frame *protocol.Frame
	end   *protocol.End
	abort string
}

// inflight islenmekte olan bir istek.
type inflight struct {
	id     uint32
	cancel context.CancelFunc

	// bodyW ve q yalnizca govdeli isteklerde dolu.
	bodyW *io.PipeWriter
	q     chan bodyEvent

	stop     chan struct{}
	stopOnce sync.Once
}

// push govde olayini kuyruga koyar. Kuyruk doluysa bekler — bu, ziyaretciye
// uygulanan alis yonu backpressure'idir. Istek bittiyse false doner.
func (fl *inflight) push(ev bodyEvent) bool {
	if fl.q == nil {
		return false
	}
	select {
	case fl.q <- ev:
		return true
	case <-fl.stop:
		return false
	}
}

func (fl *inflight) halt() {
	fl.stopOnce.Do(func() { close(fl.stop) })
}

// earlyBody sahibi henuz bilinmeyen bir id icin biriken govde olaylari.
type earlyBody struct {
	frames []protocol.Frame
	bytes  int64
	end    *protocol.End
	at     time.Time
	// abortReason bos degilse bu id icin govde kabul EDILMEYECEK ve `req`
	// gelince istek dogrudan iptal edilir.
	//
	// Neden isaret tutuyoruz: ihlal eden cerceveyi sessizce dusurmek istegi
	// askida birakiyordu. Govde hic gelmedigi icin proxy sonsuza kadar
	// bekliyor, ziyaretci de abort almiyordu — tavan korumasi bir kaynak
	// sizintisina donusuyordu.
	abortReason string
}

// Session tek bir ziyaretci baglantisi.
type Session struct {
	cfg Config
	log *slog.Logger

	pc   *webrtc.PeerConnection
	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	ctrlMu sync.Mutex // ctrl gonderimlerini sirala

	// chanMu ctrl/data kanallarini ve flow'u korur: kanallar
	// OnDataChannel ile ASENKRON gelir.
	chanMu sync.Mutex
	flow   *flowControl

	// candMu uzak adaylarin tamponunu korur.
	//
	// Adaylar uzak SDP ayarlanmadan once gelebilir (Nostr'da mesaj sirasi
	// garanti degil). pion bu durumda AddICECandidate'i reddeder, bu yuzden
	// tamponlayip Answer'dan sonra bosaltiyoruz.
	candMu       sync.Mutex
	remoteSet    bool
	pendingCands []webrtc.ICECandidateInit

	onLocalCandidate func(webrtc.ICECandidateInit)

	reqMu sync.Mutex
	reqs  map[uint32]*inflight
	early map[uint32]*earlyBody

	sem     chan struct{}
	ready   chan struct{}
	readyOn sync.Once

	closed  chan struct{}
	closeOn sync.Once
	// closeErr kapanma nedeni. Atomik kutu icinde: closeWith yazarken
	// Err() ve WaitReady() baska goroutine'lerden okuyabiliyor.
	closeErr atomic.Value // errBox

	// born oturumun kuruldugu an. MONOTONIK okumayi tasir; lastSeen bu ana
	// gore sure olarak tutuluyor ki duvar saati degisimleri (NTP adimi,
	// elle saat degisikligi) bosta kalma denetimini bozmasin.
	born time.Time
	// lastSeen ziyaretciden en son bir sey duyuldugu an; born'dan bu yana
	// gecen sure (nanosaniye).
	// Atomik: ctrl ve data kanallari ayri goroutine'lerden gelir.
	lastSeen atomic.Int64

	// disconnMu ICE Disconnected zamanlayicisini korur.
	disconnMu    sync.Mutex
	disconnTimer *time.Timer
}

// errBox atomic.Value'nun tek tip saklamasi icin sarmalayici (nil error de
// saklanabilsin).
type errBox struct{ err error }

// New oturumu kurar; kanallar acilir ama henuz offer uretilmez.
func New(cfg Config) (*Session, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var rtcCfg webrtc.Configuration
	rtcCfg.ICETransportPolicy = cfg.ICEPolicy
	if len(cfg.ICEServers) > 0 {
		rtcCfg.ICEServers = cfg.ICEServers
	}
	pc, err := webrtc.NewPeerConnection(rtcCfg)
	if err != nil {
		return nil, fmt.Errorf("peer connection kurulamadi: %w", err)
	}

	s := &Session{
		cfg:    cfg,
		log:    cfg.Logger,
		pc:     pc,
		reqs:   make(map[uint32]*inflight),
		early:  make(map[uint32]*earlyBody),
		sem:    make(chan struct{}, cfg.MaxConcurrent),
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
		born:   time.Now(),
	}
	s.touch()

	// Kanallari ZIYARETCI aciyor; biz aliyoruz.
	pc.OnDataChannel(s.onDataChannel)

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			// Toplama bitti. Aday sonunu karsi tarafa BILDIRMIYORUZ
			// (protocol/signaling.md): sadece gondermeyi birakiyoruz.
			s.log.Debug("ICE aday toplama bitti")
			return
		}
		s.chanMu.Lock()
		cb := s.onLocalCandidate
		s.chanMu.Unlock()
		if cb != nil {
			cb(c.ToJSON())
		}
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.log.Info("baglanti durumu", slog.String("state", st.String()))
		switch st {
		case webrtc.PeerConnectionStateFailed:
			s.cancelDisconnectTimer()
			s.closeWith(errors.New("WebRTC baglantisi basarisiz"))
		case webrtc.PeerConnectionStateClosed:
			s.cancelDisconnectTimer()
			s.closeWith(nil)
		case webrtc.PeerConnectionStateDisconnected:
			// Hemen kapatmiyoruz: Disconnected gecicidir ve geri donebilir.
			s.armDisconnectTimer()
		case webrtc.PeerConnectionStateConnected:
			if s.cancelDisconnectTimer() {
				s.log.Info("baglanti geri geldi")
			}
		}
	})

	return s, nil
}

// onDataChannel ziyaretcinin actigi kanallari alir.
//
// Beklenen iki kanal: `ctrl` ve `data` (protocol/README.md). Baska bir ad
// gelirse yok sayilir; ziyaretci guvenilmez.
func (s *Session) onDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case "ctrl":
		s.chanMu.Lock()
		s.ctrl = dc
		s.chanMu.Unlock()
		dc.OnMessage(s.onCtrlMessage)
	case "data":
		dc.SetBufferedAmountLowThreshold(bufferedLow)
		s.chanMu.Lock()
		s.data = dc
		s.flow = newFlowControl(dc, bufferedHigh, bufferedLow)
		s.chanMu.Unlock()
		dc.OnMessage(s.onDataMessage)
	default:
		s.log.Warn("beklenmeyen data channel", slog.String("label", dc.Label()))
		return
	}

	dc.OnOpen(s.onChannelOpen)
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		s.onChannelOpen()
	}
}

func (s *Session) onChannelOpen() {
	s.chanMu.Lock()
	both := s.ctrl != nil && s.data != nil &&
		s.ctrl.ReadyState() == webrtc.DataChannelStateOpen &&
		s.data.ReadyState() == webrtc.DataChannelStateOpen
	s.chanMu.Unlock()
	if !both {
		return
	}

	s.readyOn.Do(func() {
		close(s.ready)
		s.log.Info("kanallar acildi")
		s.touch()
		go s.watchIdle()
		if err := s.sendCtrl(protocol.Hello{
			V:       protocol.Version,
			Targets: s.cfg.Targets,
			Auth:    s.cfg.Auth,
		}); err != nil {
			s.log.Error("hello yollanamadi", slog.String("err", err.Error()))
		}
	})
}

// OnLocalCandidate yerel ICE adaylari icin geri cagirma kurar.
//
// Answer'dan ONCE cagrilmali: adaylar SetLocalDescription ile birlikte
// akmaya baslar. Trickle kapaliysa (elle signaling) adaylar tam SDP'nin
// icinde gider ve bu geri cagirma kullanilmaz.
func (s *Session) OnLocalCandidate(fn func(webrtc.ICECandidateInit)) {
	s.chanMu.Lock()
	s.onLocalCandidate = fn
	s.chanMu.Unlock()
}

// Answer ziyaretcinin offer'ini uygular ve answer uretir.
//
// Trickle kapaliysa ICE toplanmasinin bitmesini bekler ve adaylari icinde
// tasiyan tam SDP doner (elle signaling tek parca kopyalanacak). Acikken
// hemen doner; adaylar OnLocalCandidate ile akar.
func (s *Session) Answer(ctx context.Context, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	var zero webrtc.SessionDescription

	if offer.Type != webrtc.SDPTypeOffer {
		return zero, fmt.Errorf("offer bekleniyordu, gelen: %q", offer.Type.String())
	}
	if err := s.pc.SetRemoteDescription(offer); err != nil {
		return zero, fmt.Errorf("uzak SDP ayarlanamadi: %w", err)
	}

	// Uzak SDP hazir: tamponlanmis adaylari simdi uygulayabiliriz.
	s.flushPendingCandidates()

	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return zero, fmt.Errorf("answer uretilemedi: %w", err)
	}

	gathered := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return zero, fmt.Errorf("yerel SDP ayarlanamadi: %w", err)
	}

	if !s.cfg.Trickle {
		select {
		case <-gathered:
		case <-ctx.Done():
			return zero, fmt.Errorf("ICE toplanmasi beklenirken: %w", ctx.Err())
		}
	}

	local := s.pc.LocalDescription()
	if local == nil {
		return zero, errors.New("yerel SDP bos")
	}
	return *local, nil
}

// AddRemoteCandidate ziyaretciden gelen ICE adayini ekler.
//
// Uzak SDP henuz ayarlanmadiysa aday tamponlanir ve Answer'dan sonra
// uygulanir. Nostr'da mesaj sirasi garanti olmadigi icin bu sart.
func (s *Session) AddRemoteCandidate(c webrtc.ICECandidateInit) error {
	s.candMu.Lock()
	if !s.remoteSet {
		s.pendingCands = append(s.pendingCands, c)
		n := len(s.pendingCands)
		s.candMu.Unlock()
		s.log.Debug("aday tamponlandi (uzak SDP henuz yok)", slog.Int("bekleyen", n))
		return nil
	}
	s.candMu.Unlock()

	if err := s.pc.AddICECandidate(c); err != nil {
		return fmt.Errorf("aday eklenemedi: %w", err)
	}
	return nil
}

func (s *Session) flushPendingCandidates() {
	s.candMu.Lock()
	s.remoteSet = true
	pending := s.pendingCands
	s.pendingCands = nil
	s.candMu.Unlock()

	for _, c := range pending {
		if err := s.pc.AddICECandidate(c); err != nil {
			s.log.Warn("tamponlanmis aday eklenemedi", slog.String("err", err.Error()))
		}
	}
}

// Ready iki kanal da acildiginda kapanan kanali dondurur.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Done oturum kapandiginda kapanan kanali dondurur.
func (s *Session) Done() <-chan struct{} { return s.closed }

// Err oturumun kapanma nedenini dondurur (temiz kapanista nil).
func (s *Session) Err() error {
	if v, ok := s.closeErr.Load().(errBox); ok {
		return v.err
	}
	return nil
}

// Close oturumu kapatir.
func (s *Session) Close() error {
	s.closeWith(nil)
	return s.pc.Close()
}

func (s *Session) closeWith(err error) {
	s.closeOn.Do(func() {
		s.closeErr.Store(errBox{err: err})
		close(s.closed)

		s.reqMu.Lock()
		for _, r := range s.reqs {
			r.cancel()
			r.halt()
		}
		s.reqs = map[uint32]*inflight{}
		s.early = map[uint32]*earlyBody{}
		s.reqMu.Unlock()
	})
}

// sendCtrl bir ctrl mesajini kanonik bicimde yollar.
func (s *Session) sendCtrl(m protocol.Msg) error {
	s.ctrlMu.Lock()
	defer s.ctrlMu.Unlock()

	// Kanallar OnDataChannel ile asenkron geldigi icin nil olabilir.
	s.chanMu.Lock()
	ctrl := s.ctrl
	s.chanMu.Unlock()

	if ctrl == nil {
		return errors.New("ctrl kanali henuz acilmadi")
	}
	if ctrl.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("ctrl kanali acik degil (%s)", ctrl.ReadyState())
	}
	return ctrl.SendText(m.Canonical())
}

func (s *Session) onCtrlMessage(msg webrtc.DataChannelMessage) {
	// Bozuk mesaj bile canlilik kanitidir: karsi taraf orada.
	s.touch()

	m, err := protocol.ParseCtrl(string(msg.Data))
	if err != nil {
		// Ziyaretci guvenilmez: bozuk mesaj baglantiyi dusurmez, loglanir.
		s.log.Warn("gecersiz ctrl mesaji", slog.String("err", err.Error()))
		return
	}

	switch v := m.(type) {
	case protocol.Req:
		s.onReq(v)
	case protocol.End:
		s.onEnd(v)
	case protocol.Abort:
		s.onAbort(v.ID, v.Reason)
	case protocol.Ping:
		if err := s.sendCtrl(protocol.Pong{}); err != nil {
			s.log.Warn("pong yollanamadi", slog.String("err", err.Error()))
		}
	case protocol.Pong:
		// Olcum tarafi henuz yok.
	default:
		// res ve hello ajana gelmez: protokol ihlali.
		s.log.Warn("beklenmeyen ctrl mesaji", slog.String("t", m.Type()))
	}
}

func (s *Session) onReq(req protocol.Req) {
	// Eszamanlilik tavani (README ilke 4).
	select {
	case s.sem <- struct{}{}:
	default:
		s.log.Warn("eszamanli istek tavani asildi", slog.Uint64("id", uint64(req.ID)))
		_ = s.sendCtrl(protocol.Abort{ID: req.ID, Reason: "too-many-requests"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	fl := &inflight{id: req.ID, cancel: cancel, stop: make(chan struct{})}

	var body io.Reader

	s.reqMu.Lock()
	if _, dup := s.reqs[req.ID]; dup {
		s.reqMu.Unlock()
		cancel()
		<-s.sem
		s.log.Warn("ayni id ile ikinci istek", slog.Uint64("id", uint64(req.ID)))
		_ = s.sendCtrl(protocol.Abort{ID: req.ID, Reason: "duplicate-id"})
		return
	}

	// `req`'ten once gelmis govde olaylari.
	early := s.early[req.ID]
	delete(s.early, req.ID)

	if early != nil && early.abortReason != "" {
		s.reqMu.Unlock()
		cancel()
		<-s.sem
		s.log.Warn("erken govde ihlali nedeniyle istek reddedildi",
			slog.Uint64("id", uint64(req.ID)), slog.String("reason", early.abortReason))
		// abort cerceve geldiginde zaten yollandi; burada tekrar etmiyoruz.
		return
	}

	switch {
	case req.HasBody:
		pr, pw := io.Pipe()
		fl.bodyW = pw
		body = pr

		capacity := bodyQueueSlack
		if early != nil {
			capacity += len(early.frames) + 1
		}
		fl.q = make(chan bodyEvent, capacity)

		// Erken olaylari kuyruga ONCE koy: bundan sonra gelen gercek
		// cerceveler onlarin arkasina dizilir ve seq sirasi korunur.
		// Kapasite yeterli oldugu icin bu yazimlar bloklanmaz.
		if early != nil {
			for i := range early.frames {
				f := early.frames[i]
				fl.q <- bodyEvent{frame: &f}
			}
			if early.end != nil {
				fl.q <- bodyEvent{end: early.end}
			}
		}
	case early != nil:
		s.log.Warn("govdesiz istek icin erken cerceve geldi, atildi",
			slog.Uint64("id", uint64(req.ID)), slog.Int("cerceve", len(early.frames)))
	}

	s.reqs[req.ID] = fl
	s.reqMu.Unlock()

	s.log.Debug("istek alindi",
		slog.Uint64("id", uint64(req.ID)),
		slog.String("method", req.Method),
		logging.PathAttr(req.Path, s.cfg.Verbose),
		slog.Bool("has_body", req.HasBody),
	)

	if fl.q != nil {
		go s.runBodyWriter(fl)
	}

	go func() {
		defer func() {
			cancel()
			s.finish(req.ID)
			<-s.sem
		}()
		sink := newSink(s, req.ID)
		s.cfg.Proxy.Serve(ctx, req, body, sink)
		// Proxy akisi kapatmadiysa biz kapatalim: ziyaretci askida kalmasin.
		sink.ensureClosed()
	}()
}

// runBodyWriter bir istegin TUM govde olaylarini tek sirada isler.
//
// Tek goroutine olmasi onemli: cerceveler, `end` ve `abort` ayni kuyruktan
// gectigi icin kanallar arasi siralama eksikligi burada normalize edilir.
func (s *Session) runBodyWriter(fl *inflight) {
	var (
		nextSeq   uint32
		seen      int64
		endSeen   bool
		expFrames uint32
		expLen    int64
		finished  bool
	)

	closeBody := func(err error) {
		if finished {
			return
		}
		finished = true
		if err != nil {
			_ = fl.bodyW.CloseWithError(err)
			return
		}
		_ = fl.bodyW.Close()
	}
	defer func() { closeBody(errors.New("istek sonlandi")) }()

	for {
		select {
		case <-fl.stop:
			return
		case ev := <-fl.q:
			if finished {
				// Kuyrugu bosaltmaya devam et: gonderenler bloklanmasin.
				continue
			}

			switch {
			case ev.abort != "":
				s.log.Debug("istek iptal edildi",
					slog.Uint64("id", uint64(fl.id)), slog.String("reason", ev.abort))
				closeBody(fmt.Errorf("ziyaretci iptal etti: %s", ev.abort))
				continue

			case ev.end != nil:
				endSeen = true
				expFrames = ev.end.Frames
				expLen = ev.end.Len

			case ev.frame != nil:
				// Kanal ordered: beklenmeyen seq protokol ihlalidir, sessizce
				// yeniden siralamayiz (protocol/README.md).
				if ev.frame.Seq != nextSeq {
					s.log.Warn("beklenmeyen seq",
						slog.Uint64("id", uint64(fl.id)),
						slog.Uint64("geldi", uint64(ev.frame.Seq)),
						slog.Uint64("beklenen", uint64(nextSeq)))
					closeBody(errors.New("protokol hatasi: beklenmeyen seq"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "bad-seq"})
					continue
				}
				nextSeq++
				seen += int64(len(ev.frame.Payload))

				// Govde boyutu tavani (README ilke 4).
				if seen > s.cfg.MaxBody {
					s.log.Warn("istek govdesi tavani asildi",
						slog.Uint64("id", uint64(fl.id)), slog.Int64("bayt", seen))
					closeBody(errors.New("istek govdesi cok buyuk"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "body-too-large"})
					continue
				}
				if _, err := fl.bodyW.Write(ev.frame.Payload); err != nil {
					s.log.Debug("govde yazilamadi",
						slog.Uint64("id", uint64(fl.id)), slog.String("err", err.Error()))
					closeBody(err)
					continue
				}
			}

			// `end` gelmis olsa bile sayaclar tamamlanana kadar bekle.
			if endSeen {
				switch {
				case nextSeq > expFrames || seen > expLen:
					s.log.Warn("govde uzunlugu bildirilenle uyusmuyor",
						slog.Uint64("id", uint64(fl.id)),
						slog.Uint64("cerceve", uint64(nextSeq)),
						slog.Uint64("bildirilen_cerceve", uint64(expFrames)),
						slog.Int64("bayt", seen),
						slog.Int64("bildirilen_bayt", expLen))
					closeBody(errors.New("protokol hatasi: govde uzunlugu uyusmuyor"))
					_ = s.sendCtrl(protocol.Abort{ID: fl.id, Reason: "body-length-mismatch"})
				case nextSeq == expFrames && seen == expLen:
					closeBody(nil)
				}
			}
		}
	}
}

// onEnd `end` mesajini ilgili istege yonlendirir; istek henuz gelmediyse
// tamponlar (ctrl/data siralamasi yok).
func (s *Session) onEnd(e protocol.End) {
	s.reqMu.Lock()
	fl, ok := s.reqs[e.ID]
	if !ok {
		s.bufferEarlyEndLocked(e)
		s.reqMu.Unlock()
		return
	}
	s.reqMu.Unlock()

	if !fl.push(bodyEvent{end: &e}) {
		s.log.Debug("end yonlendirilemedi", slog.Uint64("id", uint64(e.ID)))
	}
}

func (s *Session) onAbort(id uint32, reason string) {
	if reason == "" {
		reason = "client-abort"
	}
	s.reqMu.Lock()
	fl, ok := s.reqs[id]
	s.reqMu.Unlock()
	if !ok {
		return
	}
	// Govdesiz isteklerde kuyruk yok: yalnizca context iptal edilir.
	if !fl.push(bodyEvent{abort: reason}) {
		s.log.Debug("istek iptal edildi (govdesiz)",
			slog.Uint64("id", uint64(id)), slog.String("reason", reason))
	}
	fl.cancel()
}

func (s *Session) finish(id uint32) {
	s.reqMu.Lock()
	fl, ok := s.reqs[id]
	if ok {
		delete(s.reqs, id)
	}
	s.reqMu.Unlock()
	if ok {
		fl.halt()
	}
}

// onDataMessage gelen govde cercevelerini ilgili istege yonlendirir.
func (s *Session) onDataMessage(msg webrtc.DataChannelMessage) {
	s.touch()

	frame, err := protocol.DecodeFrame(msg.Data)
	if err != nil {
		s.log.Warn("gecersiz data cercevesi", slog.String("err", err.Error()))
		return
	}

	s.reqMu.Lock()
	fl, ok := s.reqs[frame.ID]
	if !ok {
		// `req` henuz gelmemis olabilir: kanallar arasinda siralama yok.
		s.bufferEarlyFrameLocked(frame)
		s.reqMu.Unlock()
		return
	}
	s.reqMu.Unlock()

	if fl.q == nil {
		s.log.Warn("govdesiz istek icin cerceve geldi", slog.Uint64("id", uint64(frame.ID)))
		return
	}
	// Kuyruk doluysa burada beklenir: alis yonu backpressure.
	if !fl.push(bodyEvent{frame: &frame}) {
		s.log.Debug("cerceve yonlendirilemedi", slog.Uint64("id", uint64(frame.ID)))
	}
}

// bufferEarlyFrameLocked sahibi bilinmeyen cerceveyi gecici tamponda tutar.
// reqMu tutulurken cagrilmali.
func (s *Session) bufferEarlyFrameLocked(frame protocol.Frame) {
	s.pruneEarlyLocked()

	e, ok := s.early[frame.ID]
	if !ok {
		if len(s.early) >= maxEarlyIDs {
			s.log.Warn("erken cerceve tamponu dolu, cerceve dusuruldu",
				slog.Uint64("id", uint64(frame.ID)))
			return
		}
		e = &earlyBody{at: time.Now()}
		s.early[frame.ID] = e
	}
	if e.abortReason != "" {
		// Bu id zaten iptal edildi; gerisini saymaya gerek yok.
		return
	}
	if e.bytes+int64(len(frame.Payload)) > s.cfg.MaxBody {
		s.log.Warn("erken govde tavani asti, istek iptal ediliyor",
			slog.Uint64("id", uint64(frame.ID)), slog.Int64("tavan", s.cfg.MaxBody))
		e.abortReason = "body-too-large"
		e.frames = nil
		e.bytes = 0
		// Ziyaretciyi BEKLETMEDEN haberdar et: istek kaydini `req`'ten once
		// olusturuyor, yani abort'u yerine ulasir (protocol/README.md).
		_ = s.sendCtrl(protocol.Abort{ID: frame.ID, Reason: "body-too-large"})
		return
	}
	e.frames = append(e.frames, frame)
	e.bytes += int64(len(frame.Payload))
}

func (s *Session) bufferEarlyEndLocked(end protocol.End) {
	s.pruneEarlyLocked()

	e, ok := s.early[end.ID]
	if !ok {
		if len(s.early) >= maxEarlyIDs {
			s.log.Debug("bilinmeyen id icin end, tampon dolu", slog.Uint64("id", uint64(end.ID)))
			return
		}
		e = &earlyBody{at: time.Now()}
		s.early[end.ID] = e
	}
	cp := end
	e.end = &cp
}

// pruneEarlyLocked sahiplenilmemis erken olaylari temizler; aksi halde `req`
// hic gelmezse tampon sizar.
func (s *Session) pruneEarlyLocked() {
	if len(s.early) == 0 {
		return
	}
	cutoff := time.Now().Add(-earlyTTL)
	for id, e := range s.early {
		if e.at.Before(cutoff) {
			delete(s.early, id)
		}
	}
}

// dataChannel korumali erisim: kanal OnDataChannel ile asenkron geliyor.
func (s *Session) dataChannel() (*webrtc.DataChannel, *flowControl) {
	s.chanMu.Lock()
	defer s.chanMu.Unlock()
	return s.data, s.flow
}

// Ping ziyaretciye ping yollar (gecikme olcumu).
func (s *Session) Ping() error { return s.sendCtrl(protocol.Ping{}) }

// WaitReady kanallarin acilmasini bekler.
func (s *Session) WaitReady(ctx context.Context, timeout time.Duration) error {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-s.ready:
		return nil
	case <-s.closed:
		if err := s.Err(); err != nil {
			return err
		}
		return errors.New("oturum kapandi")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return fmt.Errorf("kanallar %s icinde acilmadi", timeout)
	}
}

// touch ziyaretciden bir sey duyuldugunu isaretler.
//
// Zaman MONOTONIK olcuyoruz: born'dan bu yana gecen sure. time.Now()'u unix
// nano olarak saklayip geri okumak duvar saatine donusuyordu — askiya alma,
// NTP adimi ya da elle saat degisikligi bosta kalma denetimini bozuyordu.
func (s *Session) touch() { s.lastSeen.Store(int64(time.Since(s.born))) }

// silence ziyaretciden en son haber alinmasindan bu yana gecen sure.
func (s *Session) silence() time.Duration {
	return time.Since(s.born) - time.Duration(s.lastSeen.Load())
}

// idleLimits yapilandirilmis sureleri (ya da varsayilanlari) dondurur.
func (s *Session) idleLimits() (idle, ping time.Duration) {
	idle, ping = s.cfg.IdleTimeout, s.cfg.PingInterval
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	if ping <= 0 {
		ping = DefaultPingInterval
	}
	return idle, ping
}

// disconnectGrace Disconnected'dan donus icin verilen sure.
func (s *Session) disconnectGrace() time.Duration {
	if s.cfg.DisconnectGrace > 0 {
		return s.cfg.DisconnectGrace
	}
	return DefaultDisconnectGrace
}

// armDisconnectTimer ICE geri donmezse oturumu kapatacak zamanlayiciyi
// kurar. Zaten kurulmussa dokunmaz: ilk kopmanin saati gecerlidir.
func (s *Session) armDisconnectTimer() {
	grace := s.disconnectGrace()

	s.disconnMu.Lock()
	defer s.disconnMu.Unlock()
	if s.disconnTimer != nil {
		return
	}
	s.log.Info("baglanti koptu, geri donmesi bekleniyor",
		slog.Duration("sure", grace))
	s.disconnTimer = time.AfterFunc(grace, func() {
		select {
		case <-s.closed:
			return
		default:
		}
		s.log.Info("baglanti geri donmedi, oturum kapatiliyor")
		s.closeWith(ErrDisconnected)
		_ = s.pc.Close()
	})
}

// cancelDisconnectTimer bekleyen kapatmayi iptal eder (baglanti geri geldi
// ya da oturum baska bir nedenle kapaniyor). Bekleyen bir kapatma varsa
// true doner.
func (s *Session) cancelDisconnectTimer() bool {
	s.disconnMu.Lock()
	t := s.disconnTimer
	s.disconnTimer = nil
	s.disconnMu.Unlock()

	return t != nil && t.Stop()
}

// watchIdle ziyaretci sessizlige gomulurse oturumu kapatir.
//
// Ping'i AJAN yolluyor: sessiz ama acik bir tunelde (ziyaretci sayfayi acik
// birakip hicbir istek yapmiyorsa) tek trafik kaynagi bu olur, ve sekme
// gercekten gittiyse pong gelmez. Boylece kapanis WebRTC'nin durum
// makinesine degil kendi saatimize bagli olur.
// Uyku/uyanma: monotonik saat askiya alinan sureyi isletim sistemine gore
// sayar ya da saymaz (Linux CLOCK_MONOTONIC saymaz, Windows sayar). Bu
// yuzden saate ek olarak denetimin KENDI gecikmesine bakiyoruz: iki tik
// arasi beklenenden cok uzun surduyse surec askiya alinmis demektir ve
// ziyaretci "sessiz" sayilmaz — oturum bir tur daha ping'le sinanir.
// Boylece kapanis karari her zaman cevapsiz kalan bir ping'e dayanir.
func (s *Session) watchIdle() {
	idle, interval := s.idleLimits()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastTick := time.Since(s.born)

	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}

		now := time.Since(s.born)
		gap := now - lastTick
		lastTick = now

		since := s.silence()

		switch idleDecide(gap, since, idle, interval) {
		case idleClose:
			s.log.Info("oturum bosta, kapatiliyor",
				slog.Duration("sessizlik", since.Round(time.Second)))
			s.closeWith(ErrIdle)
			_ = s.pc.Close()
			return

		case idleResume:
			s.log.Info("denetim gecikti, surec askiya alinmis olabilir",
				slog.Duration("tik_araligi", gap.Round(time.Second)),
				slog.Duration("sessizlik", since.Round(time.Second)))
			// Sessizlik sayaci sifirlanir: oturum bir tur daha ping'le
			// sinanir, uyku suresi "ziyaretci gitti" sayilmaz.
			s.touch()
			fallthrough

		case idlePing:
			if err := s.Ping(); err != nil {
				// Kanal kapanmis olabilir; kapanis yolu zaten calisiyordur.
				s.log.Debug("canlilik ping'i yollanamadi", slog.String("err", err.Error()))
			}
		}
	}
}

// idleAction watchIdle'in bir tikte verdigi karar.
type idleAction int

const (
	// idlePing her sey normal: canlilik ping'i yolla.
	idlePing idleAction = iota
	// idleResume denetim beklenenden cok gec kosuldu (askiya alma): sessizlik
	// sayacini sifirla ve bir tur daha sina.
	idleResume
	// idleClose ziyaretci gercekten sessiz: oturumu kapat.
	idleClose
)

// idleDecide tik basina karari verir.
//
// Ayri bir fonksiyon, cunku onemli olan kosul tam da ikisinin CAKISTIGI
// durum: uzun bir askiya almadan sonra sessizlik suresi tavani asmis
// GORUNUR, ama bu ziyaretcinin gittigini gostermez — kapatma karari her
// zaman cevapsiz kalan bir ping'e dayanmali.
func idleDecide(gap, silence, idle, interval time.Duration) idleAction {
	if gap > time.Duration(resumeFactor)*interval {
		return idleResume
	}
	if silence >= idle {
		return idleClose
	}
	return idlePing
}
