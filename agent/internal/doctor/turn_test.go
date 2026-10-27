package doctor_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/doctor"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
)

// TURN kontrolu "sunucu ayakta mi" degil "bana relay adresi veriyor mu"
// sorusunu olcer: yanlis kimlik bilgisiyle sunucu ayakta gorunup allocation
// vermez, ve o zaman TURN hic ise yaramaz.

func startFakeTURN(t *testing.T) *faketurn.Server {
	t.Helper()
	srv, err := faketurn.Start(faketurn.Options{User: "kullanici", Pass: "sifre"})
	if err != nil {
		t.Fatalf("sahte TURN baslatilamadi: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestCheckTURNAllocates(t *testing.T) {
	srv := startFakeTURN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := ice.Config{TURN: []string{srv.URL()}, User: "kullanici", Pass: "sifre"}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.CheckTURN(ctx, cfg, doctor.Options{TURNTimeout: 10 * time.Second})
	if len(report.Servers) != 1 {
		t.Fatalf("1 rapor beklendi, %d", len(report.Servers))
	}
	got := report.Servers[0]
	if !got.OK {
		t.Fatalf("allocation alinamadi: %s", got.Err)
	}
	// Asil kanit: sunucu bize bir relay adresi verdi.
	if got.RelayedAddr == "" {
		t.Error("relay adresi bos")
	}
	if !report.Usable() {
		t.Error("Usable() false")
	}
	if srv.Allocations() == 0 {
		t.Error("sahte sunucu allocation saymadi")
	}
}

func TestCheckTURNWrongCredentials(t *testing.T) {
	srv := startFakeTURN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := ice.Config{
		TURN: []string{srv.URL()},
		User: "kullanici",
		Pass: "yanlis-sifre",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.CheckTURN(ctx, cfg, doctor.Options{TURNTimeout: 5 * time.Second})
	if report.Usable() {
		t.Error("yanlis sifreyle allocation alinmamaliydi")
	}
	if report.Servers[0].Err == "" {
		t.Error("hata mesaji olmaliydi")
	}
}

func TestCheckTURNUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := ice.Config{
		TURN: []string{"turn:127.0.0.1:1"},
		User: "u",
		Pass: "p",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.CheckTURN(ctx, cfg, doctor.Options{TURNTimeout: 2 * time.Second})
	if report.Usable() {
		t.Error("ulasilamayan sunucu basarili sayilmamali")
	}
	if report.Servers[0].Err == "" {
		t.Error("hata mesaji olmaliydi")
	}
}

// TURN verilmediyse rapora TURN alani HIC girmemeli (JSON'da da yok).
func TestRunWithoutTURN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := doctor.Run(ctx, []string{"ws://127.0.0.1:1"}, ice.Config{}, quietLogger(), fastOpts)
	if report.TURN != nil {
		t.Error("TURN verilmediginde rapor alani nil olmali")
	}
	if report.Advice == "" {
		t.Error("tavsiye bos")
	}
}

// TURN istendi ama calismiyorsa OK() false olmali: sessizce 0 ile cikmak
// kullanicinin TURN'un calistigini sanmasina yol acar.
func TestReportNotOKWhenTURNFails(t *testing.T) {
	good, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := ice.Config{
		TURN: []string{"turn:127.0.0.1:1"},
		User: "u",
		Pass: "p",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.Run(ctx, []string{good.URL()}, cfg, quietLogger(),
		doctor.Options{
			Nip11Timeout:   fastOpts.Nip11Timeout,
			ConnectTimeout: fastOpts.ConnectTimeout,
			EchoTimeout:    fastOpts.EchoTimeout,
			STUNTimeout:    fastOpts.STUNTimeout,
			TURNTimeout:    2 * time.Second,
		})

	if len(report.UsableRelays) == 0 {
		t.Fatal("relay kullanilabilir olmaliydi")
	}
	if report.OK() {
		t.Error("TURN calismiyorken OK() false olmali")
	}
	if !strings.Contains(report.Advice, "allocation") {
		t.Errorf("tavsiye allocation hatasini soylemeli: %q", report.Advice)
	}
}
