// peerpages — localhost'u sunucusuz yayinlayan ajan.
//
// Faz 1'de signaling elle yapilir: offer stdout'a basilir, answer stdin'den
// okunur. Nostr signaling Faz 2'de gelir.
package main

import (
	"os"

	"github.com/aliosmanmho/PeerPages/agent/internal/cli"
)

// Derleme sirasinda -ldflags ile doldurulur (goreleaser, Faz 4).
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(1)
	}
}
