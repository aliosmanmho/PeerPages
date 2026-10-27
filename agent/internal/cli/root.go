// Package cli peerpages ajaninin komut satiri arayuzu.
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/version"
)

const rootLong = `peerpages — localhost'undaki siteyi sunucusuz yayinla.

Ajan yerel bir hedefe (ornegin http://localhost:3000) reverse proxy yapar ve
trafigi WebRTC data channel uzerinden ziyaretcinin tarayicisina tasir. Arada
relay ya da proxy sunucusu yoktur; trafik uctan uca DTLS ile sifrelidir.

Signaling varsayilan olarak herkese acik Nostr relay'leri uzerinden yapilir:
ziyaretci yalnizca URL'yi acar. --manual ile offer/answer kopyala-yapistir
ile tasinir (relay gerekmez).`

// NewRootCmd kok komutu kurar.
//
// Yalnizca surum metni verildiginde kullanilir (testler); derleme bilgisi
// olan yol NewRootCmdWith.
func NewRootCmd(ver string) *cobra.Command {
	return NewRootCmdWith(version.Resolve(ver, "", ""))
}

// NewRootCmdWith kok komutu tam surum bilgisiyle kurar.
func NewRootCmdWith(info version.Info) *cobra.Command {
	root := &cobra.Command{
		Use:           "peerpages",
		Short:         "localhost'u sunucusuz yayinla",
		Long:          rootLong,
		Version:       info.Short(),
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	// Version bayragini kisayolsuz tanimla: `-v` alt komutlarda verbose icin
	// kullaniliyor, ikisi karismasin.
	root.Flags().Bool("version", false, "peerpages surumunu yazdir")
	root.AddCommand(newServeCmd())
	root.AddCommand(newKeyCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newTestSiteCmd())
	root.AddCommand(newFakeRelayCmd())
	root.AddCommand(newFakeTurnCmd())
	return root
}

// newVersionCmd surum, commit, tarih ve platformu basar.
//
// Neden ayri komut: hata raporunda "hangi binary" sorusunun cevabi tek
// satirla verilemiyor. --version yalnizca surumu basmaya devam ediyor.
func newVersionCmd(info version.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "surum ve derleme bilgisini yazdir",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), info.Details())
			return err
		},
	}
}

// Execute kok komutu calistirir.
func Execute(info version.Info) error {
	return NewRootCmdWith(info).ExecuteContext(context.Background())
}
