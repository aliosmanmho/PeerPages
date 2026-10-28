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

// fastOpts: the tests run on the same machine as the fake relay, so the
// production durations are unnecessary. It cuts the run time noticeably.
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
		t.Fatalf("the fake relay could not be started: %v", err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	reports := doctor.CheckRelays(ctx, []string{relay.URL()}, quietLogger(), fastOpts)
	if len(reports) != 1 {
		t.Fatalf("1 report was expected, got %d", len(reports))
	}
	return reports[0]
}

// A healthy relay: the connection, the publish and the echo all work.
func TestHealthyRelayIsUsable(t *testing.T) {
	r := checkOne(t, fakerelay.Options{})

	if r.Verdict != doctor.VerdictUsable {
		t.Fatalf("the verdict is %q, expected %q (error: %s)", r.Verdict, doctor.VerdictUsable, r.Err)
	}
	if !r.Connected || !r.Published || !r.Echoed {
		t.Errorf("every step should have succeeded: %+v", r)
	}
	if !r.Usable() {
		t.Error("Usable() should have been true")
	}
	if r.Nip11 == nil {
		t.Error("the NIP-11 document should have been read")
	} else if r.Nip11.Name != "fakerelay" {
		t.Errorf("the NIP-11 name: %q", r.Nip11.Name)
	}
	// The fake relay is on the same machine; the timings may be tiny but
	// they must not be negative and the fields have to be filled in.
	if r.ConnectMs < 0 || r.PublishMs < 0 || r.EchoMs < 0 {
		t.Errorf("the timings cannot be negative: connect=%v publish=%v echo=%v",
			r.ConnectMs, r.PublishMs, r.EchoMs)
	}
}

// THE MOST CRITICAL CASE: the relay accepts the publish with "OK" but
// never forwards the event to the subscriber. doctor has to catch that
// silent failure, or signaling does not work and the cause stays
// invisible (BRIEF section 8).
func TestRelayThatDropsEphemeralIsDetected(t *testing.T) {
	r := checkOne(t, fakerelay.Options{DropEphemeral: true})

	if r.Verdict != doctor.VerdictDropsEphemeral {
		t.Fatalf("the verdict is %q, expected %q", r.Verdict, doctor.VerdictDropsEphemeral)
	}
	if !r.Published {
		t.Error("the publish should have been accepted (which is why a silent failure is dangerous)")
	}
	if r.Echoed {
		t.Error("no echo should have arrived")
	}
	if r.Usable() {
		t.Error("a relay that drops ephemeral events should not count as usable")
	}
}

// Relays that want NIP-42 auth are skipped (BRIEF section 4).
func TestAuthRequiredRelayIsSkipped(t *testing.T) {
	r := checkOne(t, fakerelay.Options{RequireAuth: true})

	if r.Verdict != doctor.VerdictAuthRequired {
		t.Fatalf("the verdict is %q, expected %q", r.Verdict, doctor.VerdictAuthRequired)
	}
	if r.Usable() {
		t.Error("a relay that wants auth should not count as usable")
	}
	if r.Nip11 == nil || !r.Nip11.AuthRequired {
		t.Error("the NIP-11 auth_required should have been read")
	}
}

// A relay that refuses the publish.
func TestRejectingRelay(t *testing.T) {
	r := checkOne(t, fakerelay.Options{RejectAll: true})

	if r.Verdict != doctor.VerdictRejected {
		t.Fatalf("the verdict is %q, expected %q (error: %s)", r.Verdict, doctor.VerdictRejected, r.Err)
	}
	if r.Usable() {
		t.Error("a relay that refuses should not count as usable")
	}
}

func TestUnreachableRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A closed port.
	reports := doctor.CheckRelays(ctx, []string{"ws://127.0.0.1:1"}, quietLogger(), fastOpts)
	r := reports[0]

	if r.Verdict != doctor.VerdictUnreachable {
		t.Errorf("the verdict is %q, expected %q", r.Verdict, doctor.VerdictUnreachable)
	}
	if r.Connected {
		t.Error("no connection should have been made")
	}
	if r.Err == "" {
		t.Error("there should have been an error message")
	}
}

func TestInvalidRelayURL(t *testing.T) {
	ctx := context.Background()
	reports := doctor.CheckRelays(ctx, []string{"http://relay.example", "", "relay.example"}, quietLogger(), fastOpts)

	for _, r := range reports {
		if r.Usable() {
			t.Errorf("%q should not count as usable", r.URL)
		}
		if r.Err == "" {
			t.Errorf("there should have been an error message for %q", r.URL)
		}
	}
}

// Several relays are tested in parallel and their order is kept.
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
		t.Fatalf("3 reports were expected, got %d", len(report.Relays))
	}
	// The order has to hold: the reports come in input order.
	if report.Relays[0].Verdict != doctor.VerdictUsable {
		t.Errorf("relay 1: %q", report.Relays[0].Verdict)
	}
	if report.Relays[1].Verdict != doctor.VerdictDropsEphemeral {
		t.Errorf("relay 2: %q", report.Relays[1].Verdict)
	}
	if report.Relays[2].Verdict != doctor.VerdictUnreachable {
		t.Errorf("relay 3: %q", report.Relays[2].Verdict)
	}

	if len(report.UsableRelays) != 1 || report.UsableRelays[0] != report.Relays[0].URL {
		t.Errorf("the usable list: %v", report.UsableRelays)
	}
	if !report.OK() {
		t.Error("OK() has to be true while one relay works")
	}
}

func TestReportNotOKWhenNoUsableRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := doctor.Run(ctx, []string{"ws://127.0.0.1:1"}, ice.Config{}, quietLogger(), fastOpts)
	if report.OK() {
		t.Error("OK() has to be false while there is no usable relay")
	}
}

// --- STUN ---

func TestCheckSTUNNoServers(t *testing.T) {
	got := doctor.CheckSTUN(context.Background(), nil, fastOpts)
	if got.Verdict != doctor.NATNoSTUN {
		t.Errorf("the verdict is %q, expected %q", got.Verdict, doctor.NATNoSTUN)
	}
}

func TestCheckSTUNUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// An unroutable address: no answer comes back.
	got := doctor.CheckSTUN(ctx, []string{"stun:127.0.0.1:1"}, fastOpts)
	if got.Verdict != doctor.NATNoSTUN {
		t.Errorf("the verdict is %q, expected %q (%+v)", got.Verdict, doctor.NATNoSTUN, got.Servers)
	}
	if len(got.Servers) != 1 || got.Servers[0].OK {
		t.Errorf("the server report: %+v", got.Servers)
	}
	if got.Note == "" {
		t.Error("there should have been a note")
	}
}

func TestCheckSTUNInvalidAddress(t *testing.T) {
	got := doctor.CheckSTUN(context.Background(), []string{"", "stun:"}, fastOpts)
	for _, s := range got.Servers {
		if s.OK {
			t.Errorf("%q should not count as a success", s.URL)
		}
		if s.Err == "" {
			t.Errorf("there should have been an error message for %q", s.URL)
		}
	}
}
