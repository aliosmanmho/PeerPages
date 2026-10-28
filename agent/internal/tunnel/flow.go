package tunnel

import (
	"context"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// flowControl applies backpressure for the data channel's send buffer.
//
// protocol/README.md: once bufferedAmount passes 1 MiB sending waits, and
// it resumes when it drops to 256 KiB. Without this a 5 MB download pulls
// the whole body into memory and clogs the channel.
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

// pollInterval is a safety net: OnBufferedAmountLow only fires when the
// buffer drops BELOW the threshold; a missed event is caught here.
const pollInterval = 20 * time.Millisecond

// wait blocks until the buffer drains.
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
