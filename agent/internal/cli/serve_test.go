package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
)

func TestServeFlagsValidation(t *testing.T) {
	base := func() *serveFlags {
		return &serveFlags{
			targets:       []string{"http://localhost:3000"},
			maxBody:       defaultMaxBody,
			maxConcurrent: defaultMaxConcurrent,
			maxSessions:   defaultMaxSessions,
			maxStreams:    defaultMaxStreams,
			maxWSMessage:  defaultMaxWSMessage,
			manual:        true,
			stun:          ice.DefaultSTUN,
		}
	}

	t.Run("a valid configuration", func(t *testing.T) {
		cfg, err := base().toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cfg.MaxBody != 50<<20 {
			t.Errorf("max-body = %d, expected %d", cfg.MaxBody, 50<<20)
		}
		if !cfg.Targets.Has("default") {
			t.Errorf("there is no default target: %v", cfg.Targets.Names())
		}
	})

	t.Run("a target is required", func(t *testing.T) {
		f := base()
		f.targets = nil
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("a configuration with no target should have been refused")
		}
	})

	// Phase 3: on the --manual path a flag picks the target, since there
	// is no visitor URL.
	t.Run("manual-target defaults to default", func(t *testing.T) {
		cfg, err := base().toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "default" {
			t.Errorf("ManualTarget = %q, expected default", cfg.ManualTarget)
		}
	})

	t.Run("manual-target picks the given target", func(t *testing.T) {
		f := base()
		f.targets = []string{"http://localhost:3000", "admin=http://localhost:4000"}
		f.manualTarget = "admin"
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "admin" {
			t.Errorf("ManualTarget = %q, expected admin", cfg.ManualTarget)
		}
	})

	// The error has to come out at startup, not after the tunnel is
	// up.
	t.Run("manual-target refuses an undefined target", func(t *testing.T) {
		f := base()
		f.manualTarget = "missing"
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--manual-target") {
			t.Errorf("a --manual-target error was expected, got: %v", err)
		}
	})

	// With no default name it falls back to the alphabetically first
	// target.
	t.Run("the first target when there is no default", func(t *testing.T) {
		f := base()
		f.targets = []string{"zeta=http://localhost:3000", "admin=http://localhost:4000"}
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "admin" {
			t.Errorf("ManualTarget = %q, expected admin (the alphabetically first)", cfg.ManualTarget)
		}
	})

	t.Run("an invalid max-body", func(t *testing.T) {
		f := base()
		f.maxBody = "coook"
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--max-body") {
			t.Errorf("a --max-body error was expected, got: %v", err)
		}
	})

	t.Run("max-concurrent zero", func(t *testing.T) {
		f := base()
		f.maxConcurrent = 0
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("max-concurrent=0 should have been refused")
		}
	})

	t.Run("nostr mode uses the default relay list", func(t *testing.T) {
		f := base()
		f.manual = false
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(cfg.Relays) < 3 {
			t.Errorf("the default relay list was expected, got: %v", cfg.Relays)
		}
		if cfg.KeyPath == "" {
			t.Error("the key path should have been filled in")
		}
	})

	t.Run("manual mode needs no relay and no key", func(t *testing.T) {
		f := base()
		f.manual = true
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(cfg.Relays) != 0 {
			t.Errorf("no relay is expected in manual mode: %v", cfg.Relays)
		}
	})

	t.Run("an invalid relay is refused", func(t *testing.T) {
		f := base()
		f.manual = false
		f.relays = []string{"http://relay.example"}
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("a relay that is not ws/wss should have been refused")
		}
	})

	t.Run("max-sessions zero", func(t *testing.T) {
		f := base()
		f.maxSessions = 0
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("max-sessions=0 should have been refused")
		}
	})

	t.Run("auth required when there is a secret", func(t *testing.T) {
		f := base()
		f.secret = "kedi-balik"
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AuthMode() != "required" {
			t.Errorf("auth mode: %q", cfg.AuthMode())
		}
	})

	t.Run("stun cannot be empty", func(t *testing.T) {
		f := base()
		f.stun = nil
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("an empty --stun should have been refused")
		}
	})

	// Phase 6: the ws bridge has to be OFF BY DEFAULT — a feature that
	// touches the HTML must not turn itself on silently.
	t.Run("the ws bridge is off by default", func(t *testing.T) {
		cfg, err := base().toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.InjectWSShim {
			t.Error("the bridge is on without --inject-ws-shim")
		}
		if cfg.wsDialerFactory() != nil {
			t.Error("the dialer factory has to be nil while the bridge is off")
		}
		if !strings.Contains(cfg.wsNote(), "off") {
			t.Errorf("the banner note: %q", cfg.wsNote())
		}
	})

	t.Run("a dialer is built when the ws bridge is on", func(t *testing.T) {
		f := base()
		f.injectWSShim = true
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		factory := cfg.wsDialerFactory()
		if factory == nil {
			t.Fatal("the dialer factory is nil")
		}
		if _, err := factory(cfg.ManualTarget); err != nil {
			t.Errorf("the dialer could not be built: %v", err)
		}
		if !strings.Contains(cfg.wsNote(), "on,") {
			t.Errorf("the banner note: %q", cfg.wsNote())
		}
	})

	t.Run("max-streams at least 1", func(t *testing.T) {
		f := base()
		f.maxStreams = 0
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("a zero --max-streams should have been refused")
		}
	})

	// A limit smaller than one frame would refuse every message: that
	// would be a silent breakage, so saying it at startup is right.
	t.Run("max-ws-message cannot be smaller than one frame", func(t *testing.T) {
		f := base()
		f.maxWSMessage = "1KB"
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil {
			t.Fatal("a small --max-ws-message should have been refused")
		}
		if !strings.Contains(err.Error(), "--max-ws-message") {
			t.Errorf("the error has to mention the flag: %v", err)
		}
	})

	t.Run("max-ws-message invalid unit", func(t *testing.T) {
		f := base()
		f.maxWSMessage = "elma"
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("an invalid --max-ws-message should have been refused")
		}
	})
}

func TestRootCmdHelpRuns(t *testing.T) {
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help returned an error: %v", err)
	}
	if !strings.Contains(out.String(), "serve") {
		t.Errorf("the help text has to show the serve command:\n%s", out.String())
	}
}

func TestServeRequiresTarget(t *testing.T) {
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"serve"})
	if err := root.Execute(); err == nil {
		t.Error("serve should have errored without --target")
	}
}
