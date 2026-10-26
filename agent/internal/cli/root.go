// Package cli peerpages ajaninin komut satiri arayuzu.
package cli

import (
	"context"

	"github.com/spf13/cobra"
)

const rootLong = `peerpages — localhost'undaki siteyi sunucusuz yayinla.

Ajan yerel bir hedefe (ornegin http://localhost:3000) reverse proxy yapar ve
trafigi WebRTC data channel uzerinden ziyaretcinin tarayicisina tasir. Arada
relay ya da proxy sunucusu yoktur; trafik uctan uca DTLS ile sifrelidir.

Faz 1: signaling elle yapilir (--manual). Ajan offer'i stdout'a basar, sen
ziyaretci sayfasina yapistirirsin, sayfanin urettigi answer'i ajana geri
yapistirirsin. Nostr signaling Faz 2'de gelecek.`

// NewRootCmd kok komutu kurar.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "peerpages",
		Short:         "localhost'u sunucusuz yayinla",
		Long:          rootLong,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	// Version bayragini kisayolsuz tanimla: `-v` alt komutlarda verbose icin
	// kullaniliyor, ikisi karismasin.
	root.Flags().Bool("version", false, "peerpages surumunu yazdir")
	root.AddCommand(newServeCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newTestSiteCmd())
	return root
}

// Execute kok komutu calistirir.
func Execute(version string) error {
	return NewRootCmd(version).ExecuteContext(context.Background())
}
