package version

import (
	"runtime"
	"strings"
	"testing"
)

// ldflags verilmisse oldugu gibi kullanilir.
func TestResolvePrefersLdflags(t *testing.T) {
	got := Resolve("v1.2.3", "abcdef1234567890", "2026-10-27T10:00:00Z")

	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, beklenen v1.2.3", got.Version)
	}
	// Commit kisaltilir: tam hash satiri gereksiz uzatiyor.
	if got.Commit != "abcdef123456" {
		t.Errorf("Commit = %q, beklenen abcdef123456", got.Commit)
	}
	if got.Date != "2026-10-27T10:00:00Z" {
		t.Errorf("Date = %q", got.Date)
	}
	if got.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("Platform = %q", got.Platform)
	}
	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q", got.GoVersion)
	}
}

// ldflags yoksa surum bos kalmamali: `go install ...@latest` yolunda
// kullanicinin elindeki binary hangi surum oldugunu soyleyebilmeli.
func TestResolveFallsBackToBuildInfo(t *testing.T) {
	got := Resolve("", "", "")

	if got.Version == "" {
		t.Error("Version bos kaldi")
	}
	// Test binary'si bir modulden derleniyor; VCS bilgisi olmayabilir ama
	// "dev" en azindan yazilmali.
	if got.Version != "dev" && !strings.HasPrefix(got.Version, "v") {
		t.Errorf("Version = %q; 'dev' ya da v… bekleniyordu", got.Version)
	}
}

func TestShort(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{"yalin", Info{Version: "v1.0.0"}, "v1.0.0"},
		{"commit'li", Info{Version: "v1.0.0", Commit: "abc123"}, "v1.0.0 (abc123)"},
		{"kirli", Info{Version: "dev", Commit: "abc123", Dirty: true}, "dev (abc123, dirty)"},
		{"yalnizca kirli", Info{Version: "dev", Dirty: true}, "dev (dirty)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Short(); got != c.want {
				t.Errorf("Short() = %q, beklenen %q", got, c.want)
			}
		})
	}
}

func TestDetails(t *testing.T) {
	out := Info{
		Version:   "v1.0.0",
		Commit:    "abc123",
		Date:      "2026-10-27T10:00:00Z",
		Platform:  "linux/amd64",
		GoVersion: "go1.27.0",
	}.Details()

	for _, want := range []string{"peerpages v1.0.0", "abc123", "2026-10-27", "linux/amd64", "go1.27.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("cikti %q icermiyor:\n%s", want, out)
		}
	}
}
