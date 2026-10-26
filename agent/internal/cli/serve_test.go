package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestServeFlagsValidation(t *testing.T) {
	base := func() *serveFlags {
		return &serveFlags{
			targets:       []string{"http://localhost:3000"},
			maxBody:       defaultMaxBody,
			maxConcurrent: defaultMaxConcurrent,
			manual:        true,
			stun:          []string{defaultSTUN},
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

	t.Run("manual kapali henuz desteklenmiyor", func(t *testing.T) {
		f := base()
		f.manual = false
		_, err := f.toConfig(&bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "Faz 2") {
			t.Errorf("Faz 2 uyarisi beklenirdi, geldi: %v", err)
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
