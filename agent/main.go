// peerpages — localhost'u sunucusuz yayinlayan ajan.
//
// Signaling varsayilan olarak Nostr relay'leri uzerinden yapilir; `--manual`
// ile offer/answer kopyala-yapistir ile tasinabilir.
package main

import (
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/cli"
	"github.com/aliosmanmho/PeerPages/agent/internal/version"
)

// Derleme sirasinda -ldflags -X ile doldurulur (goreleaser).
//
// Bos kalirlarsa surum bilgisi runtime/debug.ReadBuildInfo'dan okunur:
// `go install ...@latest` ile kurulan binary de dogru surumu basar.
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
