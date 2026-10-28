// Package version gathers the agent's version information.
//
// There are two sources and both are needed:
//
//   - release builds (goreleaser) inject the version, commit and date
//     with `-ldflags -X`.
//   - a binary installed with
//     `go install github.com/aliosmanmho/PeerPages/agent@latest` has NO
//     ldflags; there the information is read from
//     runtime/debug.ReadBuildInfo. Without that fallback the binary in
//     the user's hands said "dev" and a bug report could not say which
//     version they were running.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Info is the version information.
type Info struct {
	// Version is the version tag ("v0.2.0") or "dev".
	Version string
	// Commit is the short commit hash; empty when unknown.
	Commit string
	// Date is the build date (RFC3339); empty when unknown.
	Date string
	// Dirty says whether the working tree was dirty (on the
	// ReadBuildInfo path only).
	Dirty bool
	// GoVersion is the Go version that built it.
	GoVersion string
	// Platform is GOOS/GOARCH.
	Platform string
}

// Resolve takes the ldflags values and fills the gaps from the build
// information.
func Resolve(version, commit, date string) Info {
	info := Info{
		Version:   strings.TrimSpace(version),
		Commit:    strings.TrimSpace(commit),
		Date:      strings.TrimSpace(date),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info.withDefaults()
	}

	// The module version: `go install ...@v0.2.0` fills this in, and
	// `@latest` writes the tag it resolved. The value "(devel)" carries no
	// information.
	if (info.Version == "" || info.Version == "dev") &&
		bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}

	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = shortCommit(s.Value)
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return info.withDefaults()
}

func (i Info) withDefaults() Info {
	if i.Version == "" {
		i.Version = "dev"
	}
	i.Commit = shortCommit(i.Commit)
	return i
}

func shortCommit(c string) string {
	c = strings.TrimSpace(c)
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// Short is the one-line version ("v0.2.0" or "dev (abc123, dirty)").
func (i Info) Short() string {
	var extra []string
	if i.Commit != "" {
		extra = append(extra, i.Commit)
	}
	if i.Dirty {
		extra = append(extra, "dirty")
	}
	if len(extra) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(extra, ", "))
}

// Details is the output block of `peerpages version`.
func (i Info) Details() string {
	var b strings.Builder
	fmt.Fprintf(&b, "peerpages %s\n", i.Short())
	if i.Commit != "" {
		fmt.Fprintf(&b, "  commit  : %s\n", i.Commit)
	}
	if i.Date != "" {
		fmt.Fprintf(&b, "  date    : %s\n", i.Date)
	}
	fmt.Fprintf(&b, "  platform: %s\n", i.Platform)
	fmt.Fprintf(&b, "  go      : %s\n", i.GoVersion)
	return b.String()
}
