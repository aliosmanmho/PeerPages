package tunnel

import (
	"context"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// flowControl data channel'in gonderim tamponu icin backpressure uygular.
//
// protocol/README.md: bufferedAmount 1 MiB'i gecerse gonderim bekler,
// 256 KiB'a dusunce devam eder. Bu olmadan 5 MB'lik bir indirme tum govdeyi
// bellege alir ve kanali tikar.
type flowControl struct {
	dc   *webrtc.DataChannel
	high uint64

	mu     sync.Mutex
	waiter chan struct{}
}

func newFlowControl(dc *webrtc.DataChannel, high, low uint64) *flowControl {
	f := &flowControl{dc: dc, high: high}
	dc.SetBufferedAmountLowThreshold(low)
	dc.OnBufferedAmountLow(f.signal)
	return f
}

func (f *flowControl) signal() {
	f.mu.Lock()
	w := f.waiter
	f.waiter = nil
	f.mu.Unlock()
	if w != nil {
		close(w)
	}
}

// pollInterval guvenlik agi: OnBufferedAmountLow yalnizca esigin ALTINA
// dusuldugunde tetiklenir; olay kacirilirsa burada yakalanir.
const pollInterval = 20 * time.Millisecond

// wait tampon bosalana kadar bekler.
func (f *flowControl) wait(ctx context.Context) error {
	for {
		if f.dc.BufferedAmount() <= f.high {
			return nil
		}

		f.mu.Lock()
		if f.waiter == nil {
			f.waiter = make(chan struct{})
		}
		w := f.waiter
		f.mu.Unlock()

		t := time.NewTimer(pollInterval)
		select {
		case <-w:
			t.Stop()
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}
