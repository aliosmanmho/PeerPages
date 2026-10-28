package version

import (
	"runtime"
	"strings"
	"testing"
)

// When ldflags were given they are used as they are.
func TestResolvePrefersLdflags(t *testing.T) {
	got := Resolve("v1.2.3", "abcdef1234567890", "2026-10-27T10:00:00Z")

	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, expected v1.2.3", got.Version)
	}
	// The commit is shortened: a full hash makes the line needlessly
	// long.
	if got.Commit != "abcdef123456" {
		t.Errorf("Commit = %q, expected abcdef123456", got.Commit)
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

// With no ldflags the version must not be left empty: on the
// `go install ...@latest` path the binary in the user's hands has to be
// able to say which version it is.
func TestResolveFallsBackToBuildInfo(t *testing.T) {
	got := Resolve("", "", "")

	if got.Version == "" {
		t.Error("Version was left empty")
	}
	// The test binary is built from a module; there may be no VCS
	// information, but at least "dev" has to be written.
	if got.Version != "dev" && !strings.HasPrefix(got.Version, "v") {
		t.Errorf("Version = %q; 'dev' or v... was expected", got.Version)
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
		{"dirty only", Info{Version: "dev", Dirty: true}, "dev (dirty)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Short(); got != c.want {
				t.Errorf("Short() = %q, expected %q", got, c.want)
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
			t.Errorf("the output does not contain %q:\n%s", want, out)
		}
	}
}
