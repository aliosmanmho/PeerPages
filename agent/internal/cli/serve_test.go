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
			manual:        true,
			stun:          ice.DefaultSTUN,
		}
	}

	t.Run("gecerli yapilandirma", func(t *testing.T) {
		cfg, err := base().toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("hata: %v", err)
		}
		if cfg.MaxBody != 50<<20 {
			t.Errorf("max-body = %d, beklenen %d", cfg.MaxBody, 50<<20)
		}
		if !cfg.Targets.Has("default") {
			t.Errorf("default hedef yok: %v", cfg.Targets.Names())
		}
	})

	t.Run("hedef zorunlu", func(t *testing.T) {
		f := base()
		f.targets = nil
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("hedefsiz yapilandirma reddedilmeliydi")
		}
	})

	// Faz 3: --manual yolunda hedefi bayrak secer, ziyaretcinin URL'si yok.
	t.Run("manual-target varsayilani default", func(t *testing.T) {
		cfg, err := base().toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "default" {
			t.Errorf("ManualTarget = %q, beklenen default", cfg.ManualTarget)
		}
	})

	t.Run("manual-target verilen hedefi secer", func(t *testing.T) {
		f := base()
		f.targets = []string{"http://localhost:3000", "admin=http://localhost:4000"}
		f.manualTarget = "admin"
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "admin" {
			t.Errorf("ManualTarget = %q, beklenen admin", cfg.ManualTarget)
		}
	})

	// Hata tunel kurulduktan sonra degil, baslangicta cikmali.
	t.Run("manual-target tanimsiz hedefi reddeder", func(t *testing.T) {
		f := base()
		f.manualTarget = "yok"
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--manual-target") {
			t.Errorf("--manual-target hatasi beklenirdi, geldi: %v", err)
		}
	})

	// Varsayilan ad yoksa alfabetik ilk hedefe duser.
	t.Run("default yoksa ilk hedef", func(t *testing.T) {
		f := base()
		f.targets = []string{"zeta=http://localhost:3000", "admin=http://localhost:4000"}
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ManualTarget != "admin" {
			t.Errorf("ManualTarget = %q, beklenen admin (alfabetik ilk)", cfg.ManualTarget)
		}
	})

	t.Run("gecersiz max-body", func(t *testing.T) {
		f := base()
		f.maxBody = "coook"
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--max-body") {
			t.Errorf("--max-body hatasi beklenirdi, geldi: %v", err)
		}
	})

	t.Run("max-concurrent sifir", func(t *testing.T) {
		f := base()
		f.maxConcurrent = 0
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("max-concurrent=0 reddedilmeliydi")
		}
	})

	t.Run("nostr modu varsayilan relay listesini kullanir", func(t *testing.T) {
		f := base()
		f.manual = false
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("hata: %v", err)
		}
		if len(cfg.Relays) < 3 {
			t.Errorf("varsayilan relay listesi beklenirdi, geldi: %v", cfg.Relays)
		}
		if cfg.KeyPath == "" {
			t.Error("anahtar yolu doldurulmaliydi")
		}
	})

	t.Run("elle modda relay ve anahtar gerekmez", func(t *testing.T) {
		f := base()
		f.manual = true
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatalf("hata: %v", err)
		}
		if len(cfg.Relays) != 0 {
			t.Errorf("elle modda relay beklenmiyor: %v", cfg.Relays)
		}
	})

	t.Run("gecersiz relay reddedilir", func(t *testing.T) {
		f := base()
		f.manual = false
		f.relays = []string{"http://relay.example"}
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("ws/wss olmayan relay reddedilmeliydi")
		}
	})

	t.Run("max-sessions sifir", func(t *testing.T) {
		f := base()
		f.maxSessions = 0
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("max-sessions=0 reddedilmeliydi")
		}
	})

	t.Run("sir varsa auth required", func(t *testing.T) {
		f := base()
		f.secret = "kedi-balik"
		cfg, err := f.toConfig(&bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AuthMode() != "required" {
			t.Errorf("auth modu: %q", cfg.AuthMode())
		}
	})

	t.Run("stun bos olamaz", func(t *testing.T) {
		f := base()
		f.stun = nil
		if _, err := f.toConfig(&bytes.Buffer{}); err == nil {
			t.Error("bos --stun reddedilmeliydi")
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
		t.Fatalf("--help hata verdi: %v", err)
	}
	if !strings.Contains(out.String(), "serve") {
		t.Errorf("yardim metninde serve komutu gorunmeli:\n%s", out.String())
	}
}

func TestServeRequiresTarget(t *testing.T) {
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"serve"})
	if err := root.Execute(); err == nil {
		t.Error("serve --target olmadan hata vermeliydi")
	}
}
