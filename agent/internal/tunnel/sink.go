package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// sink bir istegin cevabini ctrl + data kanallarina yazar.
//
// Durum makinesi: yeni -> res yollandi -> (govde parcalari) -> kapandi.
// Yanlis sirali cagrilar hata doner; boylece proxy katmanindaki bir hata
// protokolu bozmaz.
type sink struct {
	s  *Session
	id uint32

	mu      sync.Mutex
	resSent bool
	closed  bool
	// nextSeq bir sonraki cerceve sirasi; ayni zamanda yollanan cerceve sayisi.
	nextSeq uint32
	// sentBytes yollanan toplam govde bayti; end mesajinda bildirilir.
	sentBytes int64
	buf       []byte
	writeErr  error
}

func newSink(s *Session, id uint32) *sink {
	return &sink{
		s:   s,
		id:  id,
		buf: make([]byte, 0, protocol.MaxFrame),
	}
}

// SendRes cevap basligini yollar.
func (k *sink) SendRes(status int, headers map[string]string) error {
	k.mu.Lock()
	switch {
	case k.closed:
		k.mu.Unlock()
		return errors.New("sink kapali")
	case k.resSent:
		k.mu.Unlock()
		return errors.New("res zaten yollandi")
	}
	k.resSent = true
	k.mu.Unlock()

	return k.s.sendCtrl(protocol.Res{ID: k.id, Status: status, Headers: headers})
}

// Write govdenin bir parcasini 16 KiB'lik cercevelere bolerek yollar.
//
// Her cerceveden once backpressure kontrolu yapilir; tampon dolu ise bekler.
func (k *sink) Write(p []byte) error {
	k.mu.Lock()
	switch {
	case k.closed:
		k.mu.Unlock()
		return errors.New("sink kapali")
	case !k.resSent:
		k.mu.Unlock()
		return errors.New("govdeden once res yollanmali")
	case k.writeErr != nil:
		err := k.writeErr
		k.mu.Unlock()
		return err
	}
	k.mu.Unlock()

	// Kanal ve akis denetimi ziyaretci tarafindan acildigi icin asenkron
	// geliyor; bir kez alip dongu boyunca kullaniyoruz.
	data, flow := k.s.dataChannel()
	if data == nil || flow == nil {
		return k.fail(errors.New("data kanali henuz acilmadi"))
	}

	for off := 0; off < len(p); off += protocol.MaxPayload {
		end := off + protocol.MaxPayload
		if end > len(p) {
			end = len(p)
		}

		if err := flow.wait(context.Background()); err != nil {
			return k.fail(err)
		}
		if data.ReadyState() != webrtc.DataChannelStateOpen {
			return k.fail(fmt.Errorf("data kanali acik degil (%s)", data.ReadyState()))
		}

		k.mu.Lock()
		seq := k.nextSeq
		k.nextSeq++
		frame, err := protocol.AppendFrame(k.buf[:0], k.id, seq, p[off:end])
		k.mu.Unlock()
		if err != nil {
			return k.fail(err)
		}

		if err := data.Send(frame); err != nil {
			return k.fail(fmt.Errorf("cerceve yollanamadi: %w", err))
		}

		k.mu.Lock()
		k.sentBytes += int64(end - off)
		k.mu.Unlock()
	}
	return nil
}

// End govdenin bittigini bildirir.
func (k *sink) End() error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return errors.New("sink kapali")
	}
	k.closed = true
	sent := k.resSent
	frames := k.nextSeq
	length := k.sentBytes
	k.mu.Unlock()

	if !sent {
		// res yollanmadan end anlamsiz: ziyaretci askida kalmasin.
		return k.s.sendCtrl(protocol.Abort{ID: k.id, Reason: "no-response"})
	}
	// frames/len ZORUNLU: alici ctrl ile data arasinda siralama olmadigi icin
	// bu sayaclarla govdenin bittigini anlar (protocol/README.md).
	return k.s.sendCtrl(protocol.End{ID: k.id, Frames: frames, Len: length})
}

// Abort akisi iptal eder.
func (k *sink) Abort(reason string) error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil
	}
	k.closed = true
	k.mu.Unlock()

	if reason == "" {
		reason = "agent-error"
	}
	return k.s.sendCtrl(protocol.Abort{ID: k.id, Reason: reason})
}

func (k *sink) fail(err error) error {
	k.mu.Lock()
	if k.writeErr == nil {
		k.writeErr = err
	}
	k.mu.Unlock()
	return err
}

// ensureClosed Proxy akisi kapatmadan dondugunde cagrilir.
func (k *sink) ensureClosed() {
	k.mu.Lock()
	closed := k.closed
	k.mu.Unlock()
	if closed {
		return
	}
	_ = k.Abort("agent-did-not-close")
}
