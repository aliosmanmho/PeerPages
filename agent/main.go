// peerpages is the agent that shares your localhost with no server.
//
// By default signaling goes over Nostr relays; with `--manual` the offer
// and answer can be carried by copy-paste.
package main

import (
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/cli"
	"github.com/aliosmanmho/PeerPages/agent/internal/version"
)

// Filled in at build time with -ldflags -X (goreleaser).
//
// If they stay empty the version information is read from
// runtime/debug.ReadBuildInfo, so a binary installed with
// `go install ...@latest` prints the right version too.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func main() {
	if err := cli.Execute(version.Resolve(buildVersion, buildCommit, buildDate)); err != nil {
		os.Exit(1)
	}
}
