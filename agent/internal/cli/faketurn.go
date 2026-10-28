package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice/faketurn"
)

// FakeTurnMarker reports that the fake TURN server has started
// listening. The Playwright e2e test reads the real address from this
// line.
const FakeTurnMarker = "---PEERPAGES-FAKETURN-LISTENING---"

// newFakeTurnCmd runs the in-process TURN server for tests.
//
// Hidden: it is not a user-facing feature. The phase 5 acceptance
// criterion is "it connects over TURN on symmetric NAT" — we cannot
// imitate symmetric NAT on a single machine, but forcing the agent to
// gather relay candidates only with --ice-relay-only and pointing it here
// makes the direct path IMPOSSIBLE. Then the tunnel opening is proof that
// the traffic went over TURN.
func newFakeTurnCmd() *cobra.Command {
	var (
		port int
		user string
		pass string
	)

	cmd := &cobra.Command{
		Use:    "faketurn",
		Short:  "Runs a small TURN server for tests",
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
	fl.IntVar(&port, "port", 0, "the port to listen on (0 = pick a free one)")
	fl.StringVar(&user, "user", "peerpages", "the accepted user name")
	fl.StringVar(&pass, "pass", "test-sifre", "the accepted password")
	return cmd
}
