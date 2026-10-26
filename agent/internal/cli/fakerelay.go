package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
)

// FakeRelayMarker sahte relay'in dinlemeye basladigini bildirir.
// Playwright e2e testi gercek adresi bu satirdan okur.
const FakeRelayMarker = "---PEERPAGES-FAKERELAY-LISTENING---"

// newFakeRelayCmd testler icin surec ici Nostr relay'i calistirir.
//
// Gizli: kullaniciya yonelik bir ozellik degil. Nostr e2e testinin gercek
// internet relay'lerine bagimli olmamasi icin var; gercek relay'ler
// `peerpages doctor` ile ayrica olculuyor.
func newFakeRelayCmd() *cobra.Command {
	var (
		port          int
		dropEphemeral bool
		rejectAll     bool
		requireAuth   bool
	)

	cmd := &cobra.Command{
		Use:    "fakerelay",
		Short:  "Testler icin kucuk bir Nostr relay'i calistirir",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			relay, err := fakerelay.StartOn(port, fakerelay.Options{
				DropEphemeral: dropEphemeral,
				RejectAll:     rejectAll,
				RequireAuth:   requireAuth,
			})
			if err != nil {
				return err
			}
			defer relay.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", FakeRelayMarker, relay.URL())

			<-cmd.Context().Done()
			return nil
		},
	}

	fl := cmd.Flags()
	fl.IntVar(&port, "port", 0, "dinlenecek port (0 = bos bir port sec)")
	fl.BoolVar(&dropEphemeral, "drop-ephemeral", false,
		"ephemeral event'leri kabul et ama abonelere iletme")
	fl.BoolVar(&rejectAll, "reject-all", false, "her yayini reddet")
	fl.BoolVar(&requireAuth, "require-auth", false, "NIP-42 auth isteyen relay gibi davran")
	return cmd
}
