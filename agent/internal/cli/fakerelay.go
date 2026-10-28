package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
)

// FakeRelayMarker reports that the fake relay has started listening.
// The Playwright e2e test reads the real address from this line.
const FakeRelayMarker = "---PEERPAGES-FAKERELAY-LISTENING---"

// newFakeRelayCmd runs the in-process Nostr relay for tests.
//
// Hidden: it is not a user-facing feature. It exists so the Nostr e2e test
// does not depend on real relays out on the internet; the real ones are
// measured separately with `peerpages doctor`.
func newFakeRelayCmd() *cobra.Command {
	var (
		port          int
		dropEphemeral bool
		rejectAll     bool
		requireAuth   bool
	)

	cmd := &cobra.Command{
		Use:    "fakerelay",
		Short:  "Runs a small Nostr relay for tests",
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
	fl.IntVar(&port, "port", 0, "the port to listen on (0 = pick a free one)")
	fl.BoolVar(&dropEphemeral, "drop-ephemeral", false,
		"accept ephemeral events but do not forward them to subscribers")
	fl.BoolVar(&rejectAll, "reject-all", false, "refuse every publish")
	fl.BoolVar(&requireAuth, "require-auth", false, "behave like a relay that wants NIP-42 auth")
	return cmd
}
