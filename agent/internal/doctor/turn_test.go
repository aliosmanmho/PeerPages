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

// The TURN check measures "does it give me a relay address", not "is the
// server up": with the wrong credentials the server looks up yet grants
// no allocation, and then TURN is of no use at all.

func startFakeTURN(t *testing.T) *faketurn.Server {
	t.Helper()
	srv, err := faketurn.Start(faketurn.Options{User: "user", Pass: "password"})
	if err != nil {
		t.Fatalf("the fake TURN could not be started: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestCheckTURNAllocates(t *testing.T) {
	srv := startFakeTURN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := ice.Config{TURN: []string{srv.URL()}, User: "user", Pass: "password"}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.CheckTURN(ctx, cfg, doctor.Options{TURNTimeout: 10 * time.Second})
	if len(report.Servers) != 1 {
		t.Fatalf("1 report expected, got %d", len(report.Servers))
	}
	got := report.Servers[0]
	if !got.OK {
		t.Fatalf("no allocation was granted: %s", got.Err)
	}
	// The real proof: the server gave us a relay address.
	if got.RelayedAddr == "" {
		t.Error("the relay address is empty")
	}
	if !report.Usable() {
		t.Error("Usable() is false")
	}
	if srv.Allocations() == 0 {
		t.Error("the fake server counted no allocation")
	}
}

func TestCheckTURNWrongCredentials(t *testing.T) {
	srv := startFakeTURN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := ice.Config{
		TURN: []string{srv.URL()},
		User: "user",
		Pass: "wrong-password",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	report := doctor.CheckTURN(ctx, cfg, doctor.Options{TURNTimeout: 5 * time.Second})
	if report.Usable() {
		t.Error("no allocation should have been granted with the wrong password")
	}
	if report.Servers[0].Err == "" {
		t.Error("there should have been an error message")
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
		t.Error("an unreachable server should not count as a success")
	}
	if report.Servers[0].Err == "" {
		t.Error("there should have been an error message")
	}
}

// With no TURN given the report must carry NO TURN field at all (none in
// the JSON either).
func TestRunWithoutTURN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := doctor.Run(ctx, []string{"ws://127.0.0.1:1"}, ice.Config{}, quietLogger(), fastOpts)
	if report.TURN != nil {
		t.Error("with no TURN given the report field has to be nil")
	}
	if report.Advice == "" {
		t.Error("the advice is empty")
	}
}

// If TURN was asked for but does not work, OK() has to be false: exiting
// quietly with 0 would let the user think TURN works.
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
		t.Fatal("the relay should have been usable")
	}
	if report.OK() {
		t.Error("OK() has to be false while TURN does not work")
	}
	if !strings.Contains(report.Advice, "allocation") {
		t.Errorf("the advice has to mention the allocation failure: %q", report.Advice)
	}
}
