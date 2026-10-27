// Package version ajanin surum bilgisini toplar.
//
// Iki kaynak var ve ikisi de gerekli:
//
//   - Release derlemeleri (goreleaser) `-ldflags -X` ile surum, commit ve
//     tarih enjekte eder.
//   - `go install github.com/aliosmanmho/PeerPages/agent@latest` ile kurulan
//     binary'de ldflags YOKTUR; orada bilgi runtime/debug.ReadBuildInfo'dan
//     okunur. Bu yedek olmadan kullanicinin elindeki binary "dev" diyordu ve
//     hangi surumu kullandigi bir hata raporunda anlasilmiyordu.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Info surum bilgisi.
type Info struct {
	// Version surum etiketi ("v0.2.0") ya da "dev".
	Version string
	// Commit kisa commit hash'i; bilinmiyorsa bos.
	Commit string
	// Date derleme tarihi (RFC3339); bilinmiyorsa bos.
	Date string
	// Dirty calisma agaci kirli miydi (yalnizca ReadBuildInfo yolunda).
	Dirty bool
	// GoVersion derleyen Go surumu.
	GoVersion string
	// Platform GOOS/GOARCH.
	Platform string
}

// Resolve ldflags degerlerini alir, eksikleri derleme bilgisinden tamamlar.
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

	// Modul surumu: `go install ...@v0.2.0` bunu doldurur, `@latest` ise
	// cozdugu etiketi yazar. "(devel)" degeri bilgi tasimaz.
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

// Short tek satirlik surum ("v0.2.0" ya da "dev (abc123, kirli)").
func (i Info) Short() string {
	var extra []string
	if i.Commit != "" {
		extra = append(extra, i.Commit)
	}
	if i.Dirty {
		extra = append(extra, "kirli")
	}
	if len(extra) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(extra, ", "))
}

// Details `peerpages version` cikti bloku.
func (i Info) Details() string {
	var b strings.Builder
	fmt.Fprintf(&b, "peerpages %s\n", i.Short())
	if i.Commit != "" {
		fmt.Fprintf(&b, "  commit  : %s\n", i.Commit)
	}
	if i.Date != "" {
		fmt.Fprintf(&b, "  tarih   : %s\n", i.Date)
	}
	fmt.Fprintf(&b, "  platform: %s\n", i.Platform)
	fmt.Fprintf(&b, "  go      : %s\n", i.GoVersion)
	return b.String()
}
