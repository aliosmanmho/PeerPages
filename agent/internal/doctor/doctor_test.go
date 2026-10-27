package doctor_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/doctor"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fastOpts: testler sahte relay ile ayni makinede kosuyor, uretim sureleri
// gereksiz. Kosum suresini belirgin sekilde kisaltiyor.
var fastOpts = doctor.Options{
	Nip11Timeout:   2 * time.Second,
	ConnectTimeout: 2 * time.Second,
	EchoTimeout:    1500 * time.Millisecond,
	STUNTimeout:    500 * time.Millisecond,
}

func checkOne(t *testing.T, opts fakerelay.Options) doctor.RelayReport {
	t.Helper()

	relay, err := fakerelay.Start(opts)
	if err != nil {
		t.Fatalf("sahte relay baslatilamadi: %v", err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	reports := doctor.CheckRelays(ctx, []string{relay.URL()}, quietLogger(), fastOpts)
	if len(reports) != 1 {
		t.Fatalf("1 rapor beklenirdi, %d", len(reports))
	}
	return reports[0]
}

// Saglikli relay: baglanti, yayin ve yanki calisiyor.
func TestHealthyRelayIsUsable(t *testing.T) {
	r := checkOne(t, fakerelay.Options{})

	if r.Verdict != doctor.VerdictUsable {
		t.Fatalf("karar %q, beklenen %q (hata: %s)", r.Verdict, doctor.VerdictUsable, r.Err)
	}
	if !r.Connected || !r.Published || !r.Echoed {
		t.Errorf("tum adimlar basarili olmaliydi: %+v", r)
	}
	if !r.Usable() {
		t.Error("Usable() true olmaliydi")
	}
	if r.Nip11 == nil {
		t.Error("NIP-11 dokumani okunmaliydi")
	} else if r.Nip11.Name != "fakerelay" {
		t.Errorf("NIP-11 adi: %q", r.Nip11.Name)
	}
	// Sahte relay ayni makinede; sureler cok kucuk olabilir ama negatif
	// olmamali ve alanlar doldurulmus olmali.
	if r.ConnectMs < 0 || r.PublishMs < 0 || r.EchoMs < 0 {
		t.Errorf("sureler negatif olamaz: baglanti=%v yayin=%v yanki=%v",
			r.ConnectMs, r.PublishMs, r.EchoMs)
	}
}

// EN KRITIK DURUM: relay yayini "OK" ile kabul ediyor ama event'i aboneye
// iletmiyor. Bu sessiz arizayi doctor yakalamak zorunda, yoksa signaling
// calismaz ve nedeni gorunmez (BRIEF bolum 8).
func TestRelayThatDropsEphemeralIsDetected(t *testing.T) {
	r := checkOne(t, fakerelay.Options{DropEphemeral: true})

	if r.Verdict != doctor.VerdictDropsEphemeral {
		t.Fatalf("karar %q, beklenen %q", r.Verdict, doctor.VerdictDropsEphemeral)
	}
	if !r.Published {
		t.Error("yayin kabul edilmis olmaliydi (sessiz ariza bu yuzden tehlikeli)")
	}
	if r.Echoed {
		t.Error("yanki gelmemeliydi")
	}
	if r.Usable() {
		t.Error("ephemeral dusuren relay kullanilabilir sayilmamali")
	}
}

// NIP-42 auth isteyen relay'ler atlanir (BRIEF bolum 4).
func TestAuthRequiredRelayIsSkipped(t *testing.T) {
	r := checkOne(t, fakerelay.Options{RequireAuth: true})

	if r.Verdict != doctor.VerdictAuthRequired {
		t.Fatalf("karar %q, beklenen %q", r.Verdict, doctor.VerdictAuthRequired)
	}
	if r.Usable() {
		t.Error("auth isteyen relay kullanilabilir sayilmamali")
	}
	if r.Nip11 == nil || !r.Nip11.AuthRequired {
		t.Error("NIP-11 auth_required okunmaliydi")
	}
}

// Yayini reddeden relay.
func TestRejectingRelay(t *testing.T) {
	r := checkOne(t, fakerelay.Options{RejectAll: true})

	if r.Verdict != doctor.VerdictRejected {
		t.Fatalf("karar %q, beklenen %q (hata: %s)", r.Verdict, doctor.VerdictRejected, r.Err)
	}
	if r.Usable() {
		t.Error("reddeden relay kullanilabilir sayilmamali")
	}
}

func TestUnreachableRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Kapali port.
	reports := doctor.CheckRelays(ctx, []string{"ws://127.0.0.1:1"}, quietLogger(), fastOpts)
	r := reports[0]

	if r.Verdict != doctor.VerdictUnreachable {
		t.Errorf("karar %q, beklenen %q", r.Verdict, doctor.VerdictUnreachable)
	}
	if r.Connected {
		t.Error("baglanti kurulmamaliydi")
	}
	if r.Err == "" {
		t.Error("hata mesaji olmaliydi")
	}
}

func TestInvalidRelayURL(t *testing.T) {
	ctx := context.Background()
	reports := doctor.CheckRelays(ctx, []string{"http://relay.example", "", "relay.example"}, quietLogger(), fastOpts)

	for _, r := range reports {
		if r.Usable() {
			t.Errorf("%q kullanilabilir sayilmamali", r.URL)
		}
		if r.Err == "" {
			t.Errorf("%q icin hata mesaji olmaliydi", r.URL)
		}
	}
}

// Birden cok relay paralel test edilir ve sirasi korunur.
func TestCheckRelaysMixed(t *testing.T) {
	good, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	dropper, err := fakerelay.Start(fakerelay.Options{DropEphemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer dropper.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	urls := []string{good.URL(), dropper.URL(), "ws://127.0.0.1:1"}
	report := doctor.Run(ctx, urls, ice.Config{}, quietLogger(), fastOpts)

	if len(report.Relays) != 3 {
		t.Fatalf("3 rapor beklenirdi, %d", len(report.Relays))
	}
	// Sira korunmali: raporlar girdi sirasinda.
	if report.Relays[0].Verdict != doctor.VerdictUsable {
		t.Errorf("1. relay: %q", report.Relays[0].Verdict)
	}
	if report.Relays[1].Verdict != doctor.VerdictDropsEphemeral {
		t.Errorf("2. relay: %q", report.Relays[1].Verdict)
	}
	if report.Relays[2].Verdict != doctor.VerdictUnreachable {
		t.Errorf("3. relay: %q", report.Relays[2].Verdict)
	}

	if len(report.UsableRelays) != 1 || report.UsableRelays[0] != report.Relays[0].URL {
		t.Errorf("kullanilabilir liste: %v", report.UsableRelays)
	}
	if !report.OK() {
		t.Error("bir relay calisiyorken OK() true olmali")
	}
}

func TestReportNotOKWhenNoUsableRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := doctor.Run(ctx, []string{"ws://127.0.0.1:1"}, ice.Config{}, quietLogger(), fastOpts)
	if report.OK() {
		t.Error("kullanilabilir relay yokken OK() false olmali")
	}
}

// --- STUN ---

func TestCheckSTUNNoServers(t *testing.T) {
	got := doctor.CheckSTUN(context.Background(), nil, fastOpts)
	if got.Verdict != doctor.NATNoSTUN {
		t.Errorf("karar %q, beklenen %q", got.Verdict, doctor.NATNoSTUN)
	}
}

func TestCheckSTUNUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Yonlendirilemeyen adres: cevap gelmez.
	got := doctor.CheckSTUN(ctx, []string{"stun:127.0.0.1:1"}, fastOpts)
	if got.Verdict != doctor.NATNoSTUN {
		t.Errorf("karar %q, beklenen %q (%+v)", got.Verdict, doctor.NATNoSTUN, got.Servers)
	}
	if len(got.Servers) != 1 || got.Servers[0].OK {
		t.Errorf("sunucu raporu: %+v", got.Servers)
	}
	if got.Note == "" {
		t.Error("aciklama notu olmaliydi")
	}
}

func TestCheckSTUNInvalidAddress(t *testing.T) {
	got := doctor.CheckSTUN(context.Background(), []string{"", "stun:"}, fastOpts)
	for _, s := range got.Servers {
		if s.OK {
			t.Errorf("%q basarili sayilmamali", s.URL)
		}
		if s.Err == "" {
			t.Errorf("%q icin hata mesaji olmaliydi", s.URL)
		}
	}
}
