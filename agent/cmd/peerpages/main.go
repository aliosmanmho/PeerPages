// Command peerpages is the install entry point that gives the binary its
// own name.
//
// `go install .../PeerPages/agent@latest` names the binary after the last
// element of the module path, so it lands as `agent`, while every document
// and the release archives call it `peerpages`. Installing this package
// instead produces `peerpages`.
//
// The real entry point is still agent/main.go: that is where goreleaser
// builds from, and the module root has to stay a main package so the older
// `.../agent@latest` path keeps working. This file only forwards.
//
// No build-time version variables here: a binary installed with
// `go install` reads its version from runtime/debug.ReadBuildInfo
// (internal/version), which is exactly the path this file serves.
package main

import (
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/cli"
	"github.com/aliosmanmho/PeerPages/agent/internal/version"
)

func main() {
	if err := cli.Execute(version.Resolve("dev", "", "")); err != nil {
		os.Exit(1)
	}
}
