package connect_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Faz 5 kabul kriteri: "simetrik NAT simulasyonunda TURN ile baglaniyor".
//
// Simetrik NAT'i tek makinede taklit edemiyoruz, ama AYNI SONUCU ureten bir
// durum kurabiliyoruz: ajani --ice-relay-only ile yalnizca relay adayi
// toplamaya zorluyoruz. O zaman dogrudan yol IMKANSIZ olur ve tunelin
// acilmasi, trafigin TURN'den gectiginin kanitidir. Negatif kontrol (TURN
// yoksa hic baglanilamadigi) hemen altinda: o olmadan bu testin dogrudan
// yoldan gecip yanlislikla yesil kalmasi mumkun olurdu.

// startRelayOnlyAgent ajani yalnizca relay adayi toplayacak sekilde baslatir.
func startRelayOnlyAgent(t *testing.T, relayURL string, iceCfg ice.Config) *agentFixture {
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

	acceptor, err := connect.New(connect.Config{
		Identity: id,
		Pool:     pool,
		NewProxy: func(string) (tunnel.Proxy, error) { return echoProxy{}, nil },
		Targets:  []string{"default"},
		ICE: func() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
			return iceCfg.Servers(), iceCfg.Policy()
		},
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
	time.Sleep(500 * time.Millisecond)

	return &agentFixture{acceptor: acceptor, id: id, relayURL: relayURL, cancel: cancel, done: done}
}

func TestTunnelOverTURNOnly(t *testing.T) {
	turnSrv, err := faketurn.Start(faketurn.Options{User: "kullanici", Pass: "sifre"})
	if err != nil {
		t.Fatalf("sahte TURN baslatilamadi: %v", err)
	}
	defer func() { _ = turnSrv.Close() }()

	iceCfg, err := ice.Config{
		TURN:      []string{turnSrv.URL()},
		User:      "kullanici",
		Pass:      "sifre",
		RelayOnly: true,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startRelayOnlyAgent(t, relay.URL(), iceCfg)
	v := newVisitor(t, a, "")

	// Ziyaretci NORMAL yapilandirmayla geliyor (host adaylari): uretimde de
	// boyle, TURN kimlik bilgisi sayfaya hic gitmiyor.
	// Teshis: ajanin hangi adaylari yolladigini gormek istiyoruz.
	v.connect(settle)

	if got := bodyOf(t, v, "/turn-testi"); got == "" {
		t.Error("cevap govdesi bos")
	}

	// Asil kanit: ajanin tek adayi TURN relay adayiydi, yani bu trafik
	// TURN sunucusundan gecti.
	if n := turnSrv.Allocations(); n == 0 {
		t.Error("TURN sunucusu hic allocation vermedi: baglanti relay'den gecmemis")
	}
}

// Negatif kontrol: relay-only ama TURN ulasilamaz durumda. Ajan hic
// kullanilabilir aday toplamaz ve tunel ACILMAMALI.
//
// Bu test olmadan yukaridaki testin dogrudan yoldan gecip yanlislikla yesil
// kalmasi mumkun olurdu.
func TestRelayOnlyWithoutTURNFails(t *testing.T) {
	iceCfg, err := ice.Config{
		// Dinlemeyen bir adres: allocation alinamaz.
		TURN:      []string{"turn:127.0.0.1:1"},
		User:      "kullanici",
		Pass:      "sifre",
		RelayOnly: true,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	a := startRelayOnlyAgent(t, relay.URL(), iceCfg)
	v := newVisitor(t, a, "")

	if err := tryConnect(v, 12*time.Second); err == nil {
		t.Error("TURN yokken relay-only ajana baglanilmamaliydi")
	}
}

// tryConnect connect() ile ayni akisi yurutur ama basarisizlikta testi
// DUSURMEZ, hata doner: negatif kontrolde beklenen sonuc zaten
// baglanamamak.
func tryConnect(v *visitor, d time.Duration) error {
	v.offer()

	deadline := time.After(d)
	for {
		select {
		case <-v.ready:
			return nil
		case <-deadline:
			return errors.New("tunel acilmadi")
		default:
		}

		msg, ok := v.nextSignaling(time.Second)
		if !ok {
			continue
		}
		if msg.SID != v.sid {
			continue
		}
		switch msg.Type {
		case signaling.TypeAnswer:
			if err := v.pc.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				return err
			}
		case signaling.TypeCandidate:
			mid := msg.Mid
			_ = v.pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate: msg.Candidate, SDPMid: &mid,
			})
		case signaling.TypeBye:
			return errors.New("ajan bye yolladi: " + msg.Reason)
		case signaling.TypeOffer:
			return errors.New("ajandan offer geldi")
		}
	}
}
