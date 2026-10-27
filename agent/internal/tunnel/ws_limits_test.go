package tunnel

import (
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// ws koprusunun sinirlari, hata yollari ve omru.
// Temel akis (echo, cok parcali mesaj) ws_test.go'da.

// Tek mesaj birden cok cerceveye bolunuyor ve sinirini FIN bayragi belirliyor.
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

	// Ziyaretci -> yerel sunucu: iki parcali tek mesaj.
	big := make([]byte, protocol.MaxWSPayload+1000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	var seq uint32
	v.sendWSMessage(t, id, &seq, true, big)
	if seq != 2 {
		t.Fatalf("beklenen 2 cerceve, gonderilen %d", seq)
	}

	select {
	case got := <-conn.fromAgent:
		if !got.binary || len(got.data) != len(big) {
			t.Fatalf("birlestirme bozuk: binary=%v %d bayt", got.binary, len(got.data))
		}
		for i := range big {
			if got.data[i] != big[i] {
				t.Fatalf("bayt %d farkli", i)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("yerel sunucuya mesaj ulasmadi")
	}

	// Yerel sunucu -> ziyaretci: ayni mesaj iki cerceve olarak donmeli.
	conn.toAgent <- wsMsg{binary: false, data: big}
	r := &wsReader{id: id}
	msg, cl := v.nextWSEvent(t, r, 5*time.Second)
	if cl != nil {
		t.Fatalf("beklenmeyen kapanis: %d %q", cl.Code, cl.Reason)
	}
	if msg.binary || len(msg.data) != len(big) {
		t.Fatalf("ters yon bozuk: binary=%v %d bayt", msg.binary, len(msg.data))
	}
	if r.nextSeq != 2 {
		t.Errorf("beklenen 2 cerceve, gelen %d", r.nextSeq)
	}
}

// Kopru kapaliyken (varsayilan) ws-open reddedilmeli: HTML'e mudahale ve ws
// koprusu acik bir secim olmali (BRIEF Faz 6).
func TestWSBridgeDisabled(t *testing.T) {
	sess, v := connect(t, Config{Proxy: noopProxy()})
	defer func() { _ = sess.Close() }()

	const id = uint32(1)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSClosePolicy {
		t.Errorf("kapanis kodu %d, beklenen %d", cl.Code, protocol.WSClosePolicy)
	}
}

// Cevrim basarisizsa kapanis kodu dialer'dan gelmeli: teshis bilgisi
// ziyaretciye ulasmali.
func TestWSDialFailureCarriesCode(t *testing.T) {
	dialer := &fakeDialer{err: &WSDialError{
		Code:   protocol.WSClosePolicy,
		Reason: "yerel sunucu el sikismayi reddetti (404)",
	}}
	sess, v := connect(t, Config{Proxy: noopProxy(), WSDialer: dialer})
	defer func() { _ = sess.Close() }()

	const id = uint32(11)
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/yok"}); err != nil {
		t.Fatal(err)
	}
	cl := v.waitWSClose(t, id, 5*time.Second)
	if cl.Code != protocol.WSClosePolicy || cl.Reason == "" {
		t.Errorf("kapanis: %d %q", cl.Code, cl.Reason)
	}
}

// Mesaj tavani (BRIEF ilke 4: ziyaretci guvenilmez).
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
		t.Errorf("kapanis kodu %d, beklenen %d", cl.Code, protocol.WSCloseTooBig)
	}
	if code := conn.waitClosed(t, 5*time.Second); code != protocol.WSCloseTooBig {
		t.Errorf("yerel baglanti kodu %d, beklenen %d", code, protocol.WSCloseTooBig)
	}
}

// Bozuk cerceveler protokol hatasi: sessizce dusurulmemeli.
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
				// seq 0 bekleniyor, 1 yolluyoruz.
				if err := v.sendFrame(id, 1, payload); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tanimsiz-bayrak",
			send: func(t *testing.T, v *visitor, id uint32) {
				if err := v.sendFrame(id, 0, []byte{0xff, 'a'}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "bos-payload",
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
				t.Errorf("kapanis kodu %d, beklenen %d (%q)",
					cl.Code, protocol.WSCloseProtocol, cl.Reason)
			}
		})
	}
}

// Akis butcesi: uzun yasayan akislar normal istekleri ac birakmasin.
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
		t.Errorf("kapanis kodu %d, beklenen %d", cl.Code, protocol.WSCloseTryAgainLater)
	}

	// Ilk oturum kapaninca slot geri gelmeli.
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
					t.Fatalf("beklenmeyen kapanis: %d %q", msg.Code, msg.Reason)
				}
			}
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("akis slotu geri gelmedi")
		}
	}
}

// Ziyaretci ws-close yollarsa kodu yerel sunucuya AYNEN gecmeli (4000-4999
// uygulamaya ait kodlar Socket.IO gibi kutuphanelerde anlam tasiyor).
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
		t.Errorf("yerel kapanis kodu %d, beklenen 4001", code)
	}
}

// Yerel sunucu kapatirsa kapanis ziyaretciye ulasmali.
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
		t.Errorf("kapanis kodu %d, beklenen %d", cl.Code, protocol.WSCloseNormal)
	}
}

// ws-open'dan ONCE gelen cerceveler kaybolmamali: ctrl ve data arasinda
// siralama garantisi yok (Faz 1'de olculdu, protocol/README.md).
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
	// ws-open SONRA.
	if err := v.sendCtrl(protocol.WSOpen{ID: id, Path: "/ws"}); err != nil {
		t.Fatal(err)
	}
	v.waitWSOk(t, id, 5*time.Second)

	select {
	case got := <-conn.fromAgent:
		if string(got.data) != "erken" {
			t.Errorf("erken mesaj %q", got.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ws-open'dan once gelen cerceve kayboldu")
	}
}

// Tunel kapaninca acik ws oturumlari da dusmeli: aksi halde yerel sunucuya
// baglanti ve akis slotu sizar.
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
