package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// WebSocket koprusu (Faz 6).
//
// HTTP'den ayri bir yol: internal/proxy basliklari temizlerken `upgrade` ve
// `connection`'i iki yonde de siliyor, yani el sikisma oradan GECEMEZ. Bu
// yuzden ws oturumu kendi dagitimina sahip; Proxy arayuzune metot eklenmedi
// (o arayuz testlerde birkac yerde gercekleniyor).
//
// Yuk `data` kanalindan, cerceve govdesinde bir bayrak baytiyla gider
// (protocol/wsframe.go). ws id'leri HTTP istekleriyle AYNI id uzayini
// paylasir; bir id'nin ws mi oldugunu `ws-open` belirler.

// WSConn kurulmus bir WebSocket baglantisi (yerel hedefe).
//
// Mesaj bazli: WebSocket parcalanmasi (fragmentation) bu arayuzun altinda
// kalir, tunel yalnizca tam mesajlari tasir.
type WSConn interface {
	// Read bir mesaj okur. binary false ise metin mesaji.
	Read(ctx context.Context) (binary bool, data []byte, err error)
	// Write bir mesaj yollar.
	Write(ctx context.Context, binary bool, data []byte) error
	// Close kapanis kodu ve sebebiyle kapatir.
	Close(code int, reason string) error
}

// WSDialer ws-open istegini yerel hedefe cevirir.
//
// Gerceklemesi internal/wsbridge; hedef URL'si YALNIZCA allowlist'ten gelir
// (BRIEF ilke 1).
type WSDialer interface {
	// Dial baglantiyi kurar ve sunucunun sectigi alt protokolu doner.
	Dial(ctx context.Context, open protocol.WSOpen) (WSConn, string, error)
}

// WSDialError kapanis kodunu tasiyan cevrim hatasi.
//
// Kodu burada tanimliyoruz ki tunnel paketi wsbridge'i import etmek zorunda
// kalmasin (wsbridge bu paketi import ediyor).
type WSDialError struct {
	Code   int
	Reason string
	Err    error
}

func (e *WSDialError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("ws baglantisi kurulamadi (%d): %s", e.Code, e.Reason)
	}
	return fmt.Sprintf("ws baglantisi kurulamadi (%d): %s: %v", e.Code, e.Reason, e.Err)
}

func (e *WSDialError) Unwrap() error { return e.Err }

// WSCloseError yerel sunucunun kendi kapanis kodunu tasir.
//
// Gerekli cunku kapanis kodu ziyaretcinin `onclose`'una AYNEN gecmeli:
// uygulamaya ait kodlar (4000-4999) Socket.IO gibi kutuphanelerde anlam
// tasiyor, bizim 1001'e cevirmemiz bilgiyi yok ederdi.
type WSCloseError struct {
	Code   int
	Reason string
}

func (e *WSCloseError) Error() string {
	return fmt.Sprintf("ws kapandi (%d): %s", e.Code, e.Reason)
}

// ws oturumu sinirlari.
const (
	// DefaultMaxWSMessage bir ws mesajinin en buyuk boyu (iki yonde de).
	//
	// Ziyaretci guvenilmez (BRIEF ilke 4): sinirsiz mesaj, ajanin bellegini
	// tek istekle sisirebilirdi.
	DefaultMaxWSMessage = 1 << 20 // 1 MiB
	// DefaultMaxStreams ayni anda acik uzun yasayan akis sayisi (ws oturumu
	// ya da SSE cevabi).
	//
	// Neden MaxConcurrent'tan ayri: uzun yasayan bir akis slotunu omru
	// boyunca tutar, yani 64 akis normal istekleri ac birakirdi.
	DefaultMaxStreams = 8
	// wsQueueSlack ws oturumu basina olay kuyrugunun temel kapasitesi.
	wsQueueSlack = 64
)

// wsEvent bir ws oturumunun kuyrugundaki olay. Tam olarak bir alani dolu.
type wsEvent struct {
	frame *protocol.Frame
	close *protocol.WSClose
}

// wsSession tunel uzerindeki tek bir WebSocket oturumu.
type wsSession struct {
	id     uint32
	cancel context.CancelFunc

	q        chan wsEvent
	stop     chan struct{}
	stopOnce sync.Once

	sink *wsSink

	// closeMu kapanis muhasebesini korur: iki pompa ayni anda bitebilir.
	closeMu sync.Mutex
	// closeSent ziyaretciye ws-close yollandi (ya da ondan geldi).
	closeSent bool
	// upCode/upReason yerel sunucuya yollanacak kapanis kodu.
	upCode   int
	upReason string
}

// push olayi kuyruga koyar; kuyruk doluysa bekler (alis yonu backpressure).
func (w *wsSession) push(ev wsEvent) bool {
	select {
	case w.q <- ev:
		return true
	case <-w.stop:
		return false
	}
}

func (w *wsSession) halt() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// sendClose ziyaretciye ws-close yollar; oturum basina en fazla bir kez.
func (w *wsSession) sendClose(s *Session, code int, reason string) {
	w.closeMu.Lock()
	already := w.closeSent
	w.closeSent = true
	if w.upCode == 0 {
		w.upCode, w.upReason = code, reason
	}
	w.closeMu.Unlock()

	if already {
		return
	}
	if err := s.sendCtrl(protocol.WSClose{
		ID: w.id, Code: code, Reason: truncateReason(reason),
	}); err != nil {
		s.log.Debug("ws-close yollanamadi",
			slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
	}
}

// markClosedByVisitor ziyaretci ws-close yolladi: geri yollamiyoruz.
func (w *wsSession) markClosedByVisitor(code int, reason string) {
	w.closeMu.Lock()
	w.closeSent = true
	w.upCode, w.upReason = code, reason
	w.closeMu.Unlock()
}

func (w *wsSession) upstreamClose() (int, string) {
	w.closeMu.Lock()
	defer w.closeMu.Unlock()
	if w.upCode == 0 {
		return protocol.WSCloseNormal, ""
	}
	return w.upCode, w.upReason
}

// truncateReason kapanis sebebini WebSocket sinirina kirpar; aksi halde
// karsi taraf mesaji bad-field olarak reddeder.
func truncateReason(reason string) string {
	if len(reason) <= protocol.WSCloseReasonMax {
		return reason
	}
	return reason[:protocol.WSCloseReasonMax]
}

// maxWSMessage yapilandirilmis mesaj siniri.
func (s *Session) maxWSMessage() int64 {
	if s.cfg.MaxWSMessage > 0 {
		return s.cfg.MaxWSMessage
	}
	return DefaultMaxWSMessage
}

// onWSOpen ziyaretcinin ws acma istegini isler.
func (s *Session) onWSOpen(open protocol.WSOpen) {
	if s.cfg.WSDialer == nil {
		s.log.Debug("ws-open geldi ama kopru kapali", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSClosePolicy, "ws koprusu kapali")
		return
	}

	// Akis butcesi: uzun yasayan akislar normal istekleri ac birakmasin.
	select {
	case s.streams <- struct{}{}:
	default:
		s.log.Warn("akis butcesi dolu, ws reddedildi", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSCloseTryAgainLater, "akis butcesi dolu")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &wsSession{id: open.ID, cancel: cancel, stop: make(chan struct{})}
	w.sink = &wsSink{s: s, id: open.ID, buf: make([]byte, 0, protocol.MaxFrame)}

	s.reqMu.Lock()
	_, dupReq := s.reqs[open.ID]
	_, dupWS := s.socks[open.ID]
	if dupReq || dupWS {
		s.reqMu.Unlock()
		cancel()
		<-s.streams
		s.log.Warn("ayni id ile ikinci oturum", slog.Uint64("id", uint64(open.ID)))
		s.closeWSEarly(open.ID, protocol.WSCloseProtocol, "duplicate-id")
		return
	}

	// `ws-open`'dan once gelmis cerceveler (kanallar arasinda siralama yok).
	early := s.early[open.ID]
	delete(s.early, open.ID)
	if early != nil && early.abortReason != "" {
		s.reqMu.Unlock()
		cancel()
		<-s.streams
		s.log.Warn("erken cerceve ihlali nedeniyle ws reddedildi",
			slog.Uint64("id", uint64(open.ID)), slog.String("reason", early.abortReason))
		s.closeWSEarly(open.ID, protocol.WSCloseTooBig, early.abortReason)
		return
	}

	capacity := wsQueueSlack
	if early != nil {
		capacity += len(early.frames)
	}
	w.q = make(chan wsEvent, capacity)
	// Erken cerceveleri kuyruga ONCE koy: sonraki cerceveler arkasina dizilir
	// ve seq sirasi korunur. Kapasite yeterli oldugu icin bloklanmaz.
	if early != nil {
		for i := range early.frames {
			f := early.frames[i]
			w.q <- wsEvent{frame: &f}
		}
	}

	s.socks[open.ID] = w
	s.reqMu.Unlock()

	s.log.Debug("ws acma istegi",
		slog.Uint64("id", uint64(open.ID)),
		logging.PathAttr(open.Path, s.cfg.Verbose),
		slog.Int("proto", len(open.Protos)),
	)

	go s.runWS(ctx, w, open)
}

// closeWSEarly oturum hic kurulmadan ws-close yollar.
func (s *Session) closeWSEarly(id uint32, code int, reason string) {
	if err := s.sendCtrl(protocol.WSClose{
		ID: id, Code: code, Reason: truncateReason(reason),
	}); err != nil {
		s.log.Debug("ws-close yollanamadi",
			slog.Uint64("id", uint64(id)), slog.String("err", err.Error()))
	}
}

// runWS oturumun tum omrunu yurutur: cevrim, iki pompa, temizlik.
func (s *Session) runWS(ctx context.Context, w *wsSession, open protocol.WSOpen) {
	defer func() {
		w.cancel()
		s.finishWS(w)
	}()

	conn, proto, err := s.cfg.WSDialer.Dial(ctx, open)
	if err != nil {
		code, reason := protocol.WSCloseInternal, "yerel hedefe baglanilamadi"
		var de *WSDialError
		if errors.As(err, &de) {
			code, reason = de.Code, de.Reason
		}
		s.log.Warn("ws baglantisi kurulamadi",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		w.sendClose(s, code, reason)
		return
	}

	if err := s.sendCtrl(protocol.WSOk{ID: open.ID, Proto: proto}); err != nil {
		s.log.Warn("ws-ok yollanamadi",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		_ = conn.Close(protocol.WSCloseInternal, "")
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.pumpFromUpstream(ctx, w, conn)
	}()

	// Ziyaretci -> yerel sunucu yonu bu goroutine'de kosar.
	s.pumpToUpstream(ctx, w, conn)

	// Okuma pompasini durdur ve yerel baglantiyi kapat.
	w.cancel()
	code, reason := w.upstreamClose()
	_ = conn.Close(code, truncateReason(reason))
	wg.Wait()
}

// pumpToUpstream ziyaretciden gelen cerceveleri yerel sunucuya yazar.
//
// Tek goroutine olmasi onemli: cerceveler ve ws-close ayni kuyruktan gectigi
// icin kanallar arasi siralama eksikligi burada normalize edilir (istek
// govdelerindeki runBodyWriter ile ayni gerekce).
func (s *Session) pumpToUpstream(ctx context.Context, w *wsSession, conn WSConn) {
	var (
		nextSeq  uint32
		partial  []byte
		binary   bool
		havePart bool
	)
	maxMsg := s.maxWSMessage()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case ev := <-w.q:
			if ev.close != nil {
				w.markClosedByVisitor(ev.close.Code, ev.close.Reason)
				return
			}
			f := ev.frame

			// Kanal ordered: beklenmeyen seq protokol hatasi.
			if f.Seq != nextSeq {
				s.log.Warn("ws cerceve sirasi bozuk",
					slog.Uint64("id", uint64(w.id)),
					slog.Uint64("seq", uint64(f.Seq)),
					slog.Uint64("beklenen", uint64(nextSeq)))
				w.sendClose(s, protocol.WSCloseProtocol, "bad-seq")
				return
			}
			nextSeq++

			chunk, err := protocol.DecodeWSPayload(f.Payload)
			if err != nil {
				s.log.Warn("gecersiz ws cercevesi",
					slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
				w.sendClose(s, protocol.WSCloseProtocol, "ws-bad-frame")
				return
			}
			if havePart && chunk.Binary != binary {
				s.log.Warn("ws mesajinin parcalari farkli tipte",
					slog.Uint64("id", uint64(w.id)))
				w.sendClose(s, protocol.WSCloseProtocol, "ws-mixed-type")
				return
			}
			binary = chunk.Binary
			havePart = true

			if int64(len(partial))+int64(len(chunk.Payload)) > maxMsg {
				s.log.Warn("ws mesaji tavani asti",
					slog.Uint64("id", uint64(w.id)), slog.Int64("tavan", maxMsg))
				w.sendClose(s, protocol.WSCloseTooBig, "mesaj cok buyuk")
				return
			}
			partial = append(partial, chunk.Payload...)

			if !chunk.Fin {
				continue
			}
			// Yerel sunucu yavassa burada beklenir: alis yonu backpressure.
			if err := conn.Write(ctx, binary, partial); err != nil {
				if ctx.Err() == nil {
					s.log.Warn("ws mesaji yerel sunucuya yazilamadi",
						slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
					w.sendClose(s, protocol.WSCloseInternal, "yerel sunucuya yazilamadi")
				}
				return
			}
			partial = partial[:0]
			havePart = false
		}
	}
}

// pumpFromUpstream yerel sunucudan gelen mesajlari tunele yazar.
func (s *Session) pumpFromUpstream(ctx context.Context, w *wsSession, conn WSConn) {
	maxMsg := s.maxWSMessage()

	for {
		binary, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Oturum zaten kapaniyor: kapanis kodunu diger pompa belirledi.
				return
			}
			code, reason := protocol.WSCloseGoingAway, "yerel baglanti kapandi"
			var ce *WSCloseError
			switch {
			case errors.As(err, &ce):
				// Yerel sunucunun kendi kapanis kodu: oldugu gibi gecsin.
				code, reason = ce.Code, ce.Reason
			case errors.Is(err, ErrWSClosedNormally):
				code, reason = protocol.WSCloseNormal, ""
			}
			s.log.Debug("ws yerel baglanti bitti",
				slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
			w.sendClose(s, code, reason)
			w.halt()
			return
		}
		if int64(len(data)) > maxMsg {
			s.log.Warn("yerel sunucudan gelen ws mesaji tavani asti",
				slog.Uint64("id", uint64(w.id)), slog.Int64("tavan", maxMsg))
			w.sendClose(s, protocol.WSCloseTooBig, "mesaj cok buyuk")
			w.halt()
			return
		}
		if err := w.sink.writeMessage(binary, data); err != nil {
			s.log.Warn("ws mesaji tunele yazilamadi",
				slog.Uint64("id", uint64(w.id)), slog.String("err", err.Error()))
			w.halt()
			return
		}
	}
}

// ErrWSClosedNormally yerel sunucu baglantiyi temiz kapatti (kod 1000).
// internal/wsbridge Read'den bunu doner.
var ErrWSClosedNormally = errors.New("ws baglantisi temiz kapandi")

// onWSClose ziyaretciden gelen ws-close mesajini isler.
func (s *Session) onWSClose(m protocol.WSClose) {
	s.reqMu.Lock()
	w, ok := s.socks[m.ID]
	s.reqMu.Unlock()
	if !ok {
		s.log.Debug("bilinmeyen id icin ws-close", slog.Uint64("id", uint64(m.ID)))
		return
	}
	cp := m
	if !w.push(wsEvent{close: &cp}) {
		// Pompa zaten bitmis: oturum kapaniyor.
		w.markClosedByVisitor(m.Code, m.Reason)
		w.cancel()
	}
}

// routeWSFrame bir cerceveyi ws oturumuna yonlendirir; sahibi ws degilse
// false doner.
func (s *Session) routeWSFrame(frame protocol.Frame) bool {
	s.reqMu.Lock()
	w, ok := s.socks[frame.ID]
	s.reqMu.Unlock()
	if !ok {
		return false
	}
	// Kuyruk doluysa burada beklenir: alis yonu backpressure.
	if !w.push(wsEvent{frame: &frame}) {
		s.log.Debug("ws cercevesi yonlendirilemedi", slog.Uint64("id", uint64(frame.ID)))
	}
	return true
}

// finishWS oturumu kayitlardan siler ve akis slotunu birakir.
func (s *Session) finishWS(w *wsSession) {
	s.reqMu.Lock()
	cur, ok := s.socks[w.id]
	if ok && cur == w {
		delete(s.socks, w.id)
	}
	s.reqMu.Unlock()

	w.halt()
	if ok && cur == w {
		<-s.streams
	}
}

// wsSink bir ws oturumunun mesajlarini data kanalina yazar.
//
// HTTP sink'inden ayri: res/end durum makinesi yok, her mesaj kendi
// bayragini tasir. seq oturum boyunca artar, mesaj basina sifirlanmaz
// (protocol/README.md).
type wsSink struct {
	s  *Session
	id uint32

	mu      sync.Mutex
	nextSeq uint32
	buf     []byte
}

// writeMessage bir ws mesajini parcalara bolerek yollar.
//
// Bos mesaj da bir cerceve uretir: alici FIN bayragini gormeden mesajin
// bittigini bilemez.
func (k *wsSink) writeMessage(binary bool, data []byte) error {
	dc, flow := k.s.dataChannel()
	if dc == nil || flow == nil {
		return errors.New("data kanali henuz acilmadi")
	}

	for off := 0; ; off += protocol.MaxWSPayload {
		end := off + protocol.MaxWSPayload
		if end > len(data) {
			end = len(data)
		}
		fin := end >= len(data)

		if err := flow.wait(context.Background()); err != nil {
			return err
		}
		if dc.ReadyState() != webrtc.DataChannelStateOpen {
			return fmt.Errorf("data kanali acik degil (%s)", dc.ReadyState())
		}

		k.mu.Lock()
		seq := k.nextSeq
		k.nextSeq++
		// Once dis cercevenin basligi (payload'siz), sonra ws yuku: ortaya
		// [id][seq][bayrak][parca] cikar. AppendWSPayload parcayi
		// MaxWSPayload ile sinirladigi icin toplam MaxPayload'i gecmez.
		frame, err := protocol.AppendFrame(k.buf[:0], k.id, seq, nil)
		if err == nil {
			frame, err = protocol.AppendWSPayload(frame, protocol.WSChunk{
				Binary:  binary,
				Fin:     fin,
				Payload: data[off:end],
			})
		}
		k.mu.Unlock()
		if err != nil {
			return err
		}

		if err := dc.Send(frame); err != nil {
			return fmt.Errorf("ws cercevesi yollanamadi: %w", err)
		}
		if fin {
			return nil
		}
	}
}
