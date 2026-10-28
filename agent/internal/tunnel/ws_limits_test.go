package tunnel

import (
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// ws koprusunun sinirlari, hata yollari ve omru.
// The basic flow (echo, a multi-chunk message) is in ws_test.go.

// One message is split across frames and the FIN flag marks its end.
func TestWSMultiFrameMessage(t *testing.T) {
	conn := newFakeWSConn()
	dialer := &fakeDialer{conn: conn}

	sess, v := connect(t, Config{
		Proxy:        noopProxy(),
		WSDialer:     dialer,
		MaxWSMessage: 1 << 20,
	})
	defer func() { _ = sess.Close() }()

	const id = uint32(3)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	// Visitor -> the local server: one message in two chunks.
	big := make([]byte, protocol.MaxWSPayload+1000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	var seq uint32
	v.sendWSMessage(t, id, &seq, true, big)
	if seq != 2 {
		t.Fatalf("2 frames expected, sent %d", seq)
	}

	select {
	case got := <-conn.fromAgent:
		if !got.binary || len(got.data) != len(big) {
			t.Fatalf("the assembly is broken: binary=%v %d bytes", got.binary, len(got.data))
		}
		for i := range big {
			if got.data[i] != big[i] {
				t.Fatalf("byte %d differs", i)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the local server")
	}

	// The local server -> the visitor: the same message has to come back as two frames.
	conn.toAgent <- wsMsg{binary: false, data: big}
	r := &wsReader{id: id}
	msg, cl := v.nextWSEvent(t, r, 5*time.Second)
	if cl != nil {
		t.Fatalf("unexpected close: %d %q", cl.Code, cl.Reason)
	}
	if msg.binary || len(msg.data) != len(big) {
		t.Fatalf("the reverse direction is broken: binary=%v %d bytes", msg.binary, len(msg.data))
	}
	if r.nextSeq != 2 {
		t.Errorf("2 frames expected, got %d", r.nextSeq)
	}
}

// With the bridge off (the default) ws-open has to be refused: touching the
// HTML and the ws bridge have to be an explicit choice (BRIEF phase 6).
func TestWSBridgeDisabled(t *testing.T) {
	sess, v := connect(t, Config{Proxy: noopProxy()})
	defer func() { _ = sess.Close() }()

	const id = uint32(1)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSClosePolicy {
		t.Errorf("close code %d, expected %d", cl.Code, protocol.WSClosePolicy)
	}
}

// Cevrim basarisizsa kapanis kodu dialer'dan gelmeli: teshis bilgisi
// ziyaretciye ulasmali.
func TestWSDialFailureCarriesCode(t *testing.T) {
	dialer := &fakeDialer{err: &WSDialError{
		Code:   protocol.WSClosePolicy,
		Reason: "the local server refused the handshake (404)",
	}}
	sess, v := connect(t, Config{Proxy: noopProxy(), WSDialer: dialer})
	defer func() { _ = sess.Close() }()

	const id = uint32(11)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/missing"}); err != nil {
		t.Fatal(err)
	}
	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSClosePolicy || cl.Reason == "" {
		t.Errorf("close: %d %q", cl.Code, cl.Reason)
	}
}

// The message ceiling (BRIEF principle 4: the visitor is untrusted).
func TestWSMessageTooLarge(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:        noopProxy(),
		WSDialer:     &fakeDialer{conn: conn},
		MaxWSMessage: 100,
	})
	defer func() { _ = sess.Close() }()

	const id = uint32(5)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	var seq uint32
	v.sendWSMessage(t, id, &seq, false, make([]byte, 101))

	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSCloseTooBig {
		t.Errorf("close code %d, expected %d", cl.Code, protocol.WSCloseTooBig)
	}
	if code := conn.waitClosed(t, 5*time.Second); code != protocol.WSCloseTooBig {
		t.Errorf("the local connection code is %d, expected %d", code, protocol.WSCloseTooBig)
	}
}

// Malformed frames are a protocol error: they must not be dropped silently.
func TestWSProtocolErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		send func(t *testing.T, v *visitor, id uint32)
	}{
		{
			name: "bad-seq",
			send: func(t *testing.T, v *visitor, id uint32) {
				payload, err := protocol.EncodeWSPayload(protocol.WSChunk{
					Fin: true, Payload: []byte("a"),
				})
				if err != nil {
					t.Fatal(err)
				}
				// seq 0 is expected, we send 1.
				if err := v.sendFrame(id, 1, payload); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "undefined-flag",
			send: func(t *testing.T, v *visitor, id uint32) {
				if err := v.sendFrame(id, 0, []byte{0xff, 'a'}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty-payload",
			send: func(t *testing.T, v *visitor, id uint32) {
				if err := v.sendFrame(id, 0, nil); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "karisik-tip",
			send: func(t *testing.T, v *visitor, id uint32) {
				first, err := protocol.EncodeWSPayload(protocol.WSChunk{Payload: []byte("a")})
				if err != nil {
					t.Fatal(err)
				}
				second, err := protocol.EncodeWSPayload(protocol.WSChunk{
					Binary: true, Fin: true, Payload: []byte("b"),
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := v.sendFrame(id, 0, first); err != nil {
					t.Fatal(err)
				}
				if err := v.sendFrame(id, 1, second); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := newFakeWSConn()
			sess, v := connect(t, Config{
				Proxy:    noopProxy(),
				WSDialer: &fakeDialer{conn: conn},
			})
			defer func() { _ = sess.Close() }()

			const id = uint32(9)
			if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
				t.Fatal(err)
			}
			v.waitWSOk(t, id, 5*time.Second)

			tc.send(t, v, id)

			cl := v.waitWSClose(t, id, 5*time.Second)
			if cl.Code != protocol.WSCloseProtocol {
				t.Errorf("close code %d, expected %d (%q)",
					cl.Code, protocol.WSCloseProtocol, cl.Reason)
			}
		})
	}
}

// The stream budget: long-lived streams must not starve normal requests.
func TestWSStreamBudget(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:      noopProxy(),
		WSDialer:   &fakeDialer{conn: conn},
		MaxStreams: 1,
	})
	defer func() { _ = sess.Close() }()

	if err := v.sendCtrl(protocol.WSOpen{ID: 1, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, 1, 5*time.Second)

	if err := v.sendCtrl(protocol.WSOpen{ID: 2, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	cl := v.waitWSClose(t, 2, 5*time.Second)
	if cl.Code != protocol.WSCloseTryAgainLater {
		t.Errorf("close code %d, expected %d", cl.Code, protocol.WSCloseTryAgainLater)
	}

	// Closing the first session has to give the slot back.
	if err := v.sendCtrl(protocol.WSClose{ID: 1, Code: protocol.WSCloseNormal}); err != nil {
		t.Fatal(err)
	}
	conn.waitClosed(t, 5*time.Second)

	// Slotun birakilmasi oturumun temizligini bekler: kisa aralikla tekrar
	// dene, surekli reddediliyorsa test duser.
	deadline := time.Now().Add(10 * time.Second)
	for attempt := uint32(3); ; attempt++ {
		if err := v.sendCtrl(protocol.WSOpen{ID: attempt, Path: "/ws"}); err != nil {
			t.Fatal(err)
		}
		select {
		case m := <-v.ctrlCh:
			switch msg := m.(type) {
			case protocol.WSOk:
				if msg.ID == attempt {
					return // slot geri geldi
				}
			case protocol.WSClose:
				if msg.ID == attempt && msg.Code != protocol.WSCloseTryAgainLater {
					t.Fatalf("unexpected close: %d %q", msg.Code, msg.Reason)
				}
			}
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream slot did not come back")
		}
	}
}

// Ziyaretci ws-close yollarsa kodu yerel sunucuya AYNEN gecmeli (4000-4999
// application-specific codes carry meaning in libraries like Socket.IO).
func TestWSVisitorCloseReachesUpstream(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:    noopProxy(),
		WSDialer: &fakeDialer{conn: conn},
	})
	defer func() { _ = sess.Close() }()

	const id = uint32(4)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	if err := v.sendCtrl(protocol.WSClose{
		ID: id, Code: 4001, Reason: "uygulama kapatti",
	}); err != nil {
		t.Fatal(err)
	}
	if code := conn.waitClosed(t, 5*time.Second); code != 4001 {
		t.Errorf("the local close code is %d, expected 4001", code)
	}
}

// If the local server closes, the close has to reach the visitor.
func TestWSUpstreamCloseReachesVisitor(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:    noopProxy(),
		WSDialer: &fakeDialer{conn: conn},
	})
	defer func() { _ = sess.Close() }()

	const id = uint32(6)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	_ = conn.Close(protocol.WSCloseNormal, "")
	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSCloseNormal {
		t.Errorf("close code %d, expected %d", cl.Code, protocol.WSCloseNormal)
	}
}

// Frames arriving BEFORE ws-open must not be lost: there is no ordering
// guarantee between ctrl and data (measured in phase 1, protocol/README.md).
func TestWSEarlyFramesBeforeOpen(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:    noopProxy(),
		WSDialer: &fakeDialer{conn: conn},
	})
	defer func() { _ = sess.Close() }()

	const id = uint32(8)
	var seq uint32
	// Cerceveler ONCE.
	v.sendWSMessage(t, id, &seq, false, []byte("erken"))
	// ws-open comes AFTERWARDS.
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	select {
	case got := <-conn.fromAgent:
		if string(got.data) != "erken" {
			t.Errorf("early message %q", got.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a frame that arrived before ws-open was lost")
	}
}

// Closing the tunnel has to drop the open ws sessions too: otherwise a
// connection to the local server and a stream slot leak.
func TestWSClosedWithSession(t *testing.T) {
	conn := newFakeWSConn()
	sess, v := connect(t, Config{
		Proxy:    noopProxy(),
		WSDialer: &fakeDialer{conn: conn},
	})

	const id = uint32(2)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	_ = sess.Close()
	conn.waitClosed(t, 5*time.Second)
}
