package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
)

// FakeTurnMarker sahte TURN sunucusunun dinlemeye basladigini bildirir.
// Playwright e2e testi gercek adresi bu satirdan okur.
const FakeTurnMarker = "---PEERPAGES-FAKETURN-LISTENING---"

// newFakeTurnCmd testler icin surec ici TURN sunucusu calistirir.
//
// Gizli: kullaniciya yonelik bir ozellik degil. Faz 5'in kabul kriteri
// "simetrik NAT'ta TURN ile baglaniyor" — simetrik NAT'i tek makinede taklit
// edemiyoruz, ama ajani --ice-relay-only ile yalnizca relay adayi toplamaya
// zorlayip buraya baglayinca dogrudan yol IMKANSIZ oluyor. O zaman tunelin
// acilmasi, trafigin TURN'den gectiginin kanitidir.
func newFakeTurnCmd() *cobra.Command {
	var (
		port int
		user string
		pass string
	)

	cmd := &cobra.Command{
		Use:    "faketurn",
		Short:  "Testler icin kucuk bir TURN sunucusu calistirir",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv, err := faketurn.StartOn(port, faketurn.Options{User: user, Pass: pass})
			if err != nil {
				return err
			}
			defer func() { _ = srv.Close() }()

			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", FakeTurnMarker, srv.URL())

			<-cmd.Context().Done()
			return nil
		},
	}

	fl := cmd.Flags()
	fl.IntVar(&port, "port", 0, "dinlenecek port (0 = bos bir port sec)")
	fl.StringVar(&user, "user", "peerpages", "kabul edilen kullanici adi")
	fl.StringVar(&pass, "pass", "test-sifre", "kabul edilen sifre")
	return cmd
}
