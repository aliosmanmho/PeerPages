// Package tunnel ajan tarafindaki WebRTC oturumunu ve ctrl/data kanallarini
// yonetir.
//
// Ajan offerer'dir: kanallari o acar (`ctrl` ve `data`), offer'i uretir,
// answer'i disaridan alir. Faz 1'de offer/answer elle tasinir
// (internal/signal); Faz 2'de Nostr gelecek.
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

// Config oturum yapilandirmasi.
type Config struct {
	// STUN ICE icin kullanilacak STUN sunuculari. Bos ise yalnizca host
	// adaylari toplanir (loopback testleri).
	STUN []string
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
}

// Session tek bir ziyaretci baglantisi.
type Session struct {
	cfg Config
	log *slog.Logger

	pc   *webrtc.PeerConnection
	ctrl *webrtc.DataChannel
	data *webrtc.DataChannel

	ctrlMu sync.Mutex // ctrl gonderimlerini sirala

	flow *flowControl

	reqMu sync.Mutex
	reqs  map[uint32]*inflight
	early map[uint32]*earlyBody

	sem     chan struct{}
	ready   chan struct{}
	readyOn sync.Once

	closed   chan struct{}
	closeOn  sync.Once
	closeErr error
}

// New oturumu kurar; kanallar acilir ama henuz offer uretilmez.
func New(cfg Config) (*Session, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var rtcCfg webrtc.Configuration
	if len(cfg.STUN) > 0 {
		rtcCfg.ICEServers = []webrtc.ICEServer{{URLs: cfg.STUN}}
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
	}

	ordered := true
	if s.ctrl, err = pc.CreateDataChannel("ctrl", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("ctrl kanali acilamadi: %w", err)
	}
	if s.data, err = pc.CreateDataChannel("data", &webrtc.DataChannelInit{Ordered: &ordered}); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("data kanali acilamadi: %w", err)
	}

	s.flow = newFlowControl(s.data, bufferedHigh, bufferedLow)

	s.ctrl.OnOpen(s.onChannelOpen)
	s.data.OnOpen(s.onChannelOpen)
	s.ctrl.OnMessage(s.onCtrlMessage)
	s.data.OnMessage(s.onDataMessage)

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		s.log.Info("baglanti durumu", slog.String("state", st.String()))
		switch st {
		case webrtc.PeerConnectionStateFailed:
			s.closeWith(errors.New("WebRTC baglantisi basarisiz"))
		case webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			s.closeWith(nil)
		}
	})

	return s, nil
}

func (s *Session) onChannelOpen() {
	if s.ctrl.ReadyState() != webrtc.DataChannelStateOpen ||
		s.data.ReadyState() != webrtc.DataChannelStateOpen {
		return
	}
	s.readyOn.Do(func() {
		close(s.ready)
		s.log.Info("kanallar acildi")
		if err := s.sendCtrl(protocol.Hello{
			V:       protocol.Version,
			Targets: s.cfg.Targets,
			Auth:    s.cfg.Auth,
		}); err != nil {
			s.log.Error("hello yollanamadi", slog.String("err", err.Error()))
		}
	})
}

// Offer offer uretir ve ICE toplanmasinin bitmesini bekler (trickle yok —
// Faz 1'de offer tek parca olarak kopyalanacak).
func (s *Session) Offer(ctx context.Context) (webrtc.SessionDescription, error) {
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("offer uretilemedi: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(s.pc)
	if err := s.pc.SetLocalDescription(offer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("yerel SDP ayarlanamadi: %w", err)
	}

	select {
	case <-gathered:
	case <-ctx.Done():
		return webrtc.SessionDescription{}, fmt.Errorf("ICE toplanmasi beklenirken: %w", ctx.Err())
	}

	local := s.pc.LocalDescription()
	if local == nil {
		return webrtc.SessionDescription{}, errors.New("yerel SDP bos")
	}
	return *local, nil
}

// Accept ziyaretciden gelen answer'i uygular.
func (s *Session) Accept(answer webrtc.SessionDescription) error {
	if answer.Type != webrtc.SDPTypeAnswer {
		return fmt.Errorf("answer bekleniyordu, gelen: %q", answer.Type.String())
	}
	if err := s.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("uzak SDP ayarlanamadi: %w", err)
	}
	return nil
}

// Ready iki kanal da acildiginda kapanan kanali dondurur.
func (s *Session) Ready() <-chan struct{} { return s.ready }

// Done oturum kapandiginda kapanan kanali dondurur.
func (s *Session) Done() <-chan struct{} { return s.closed }

// Err oturumun kapanma nedenini dondurur (temiz kapanista nil).
func (s *Session) Err() error { return s.closeErr }

// Close oturumu kapatir.
func (s *Session) Close() error {
	s.closeWith(nil)
	return s.pc.Close()
}

func (s *Session) closeWith(err error) {
	s.closeOn.Do(func() {
		s.closeErr = err
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
	if s.ctrl.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("ctrl kanali acik degil (%s)", s.ctrl.ReadyState())
	}
	return s.ctrl.SendText(m.Canonical())
}

func (s *Session) onCtrlMessage(msg webrtc.DataChannelMessage) {
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
	if e.bytes+int64(len(frame.Payload)) > s.cfg.MaxBody {
		s.log.Warn("erken govde tavani asti, cerceve dusuruldu",
			slog.Uint64("id", uint64(frame.ID)))
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
		if s.closeErr != nil {
			return s.closeErr
		}
		return errors.New("oturum kapandi")
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return fmt.Errorf("kanallar %s icinde acilmadi", timeout)
	}
}
