// Package cli is the command line interface of the peerpages agent.
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/version"
)

const rootLong = `peerpages — share the site on your localhost with no server.

The agent reverse proxies to a local target (for example
http://localhost:3000) and carries the traffic to the visitor's browser
over a WebRTC data channel. There is no relay or proxy server in between;
the traffic is end-to-end encrypted with DTLS.

By default signaling goes over public Nostr relays: the visitor only opens
the URL. With --manual the offer and answer are carried by copy-paste (no
relay needed).`

// NewRootCmd builds the root command.
//
// Used when only the version string is available (tests); the path with
// full build information is NewRootCmdWith.
func NewRootCmd(ver string) *cobra.Command {
	return NewRootCmdWith(version.Resolve(ver, "", ""))
}

// NewRootCmdWith builds the root command with full version
// information.
func NewRootCmdWith(info version.Info) *cobra.Command {
	root := &cobra.Command{
		Use:           "peerpages",
		Short:         "share your localhost with no server",
		Long:          rootLong,
		Version:       info.Short(),
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	// Define the version flag with no shorthand: `-v` is used for verbose
	// in the subcommands and the two must not be confused.
	root.Flags().Bool("version", false, "print the peerpages version")
	root.AddCommand(newServeCmd())
	root.AddCommand(newKeyCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newVersionCmd(info))
	root.AddCommand(newTestSiteCmd())
	root.AddCommand(newFakeRelayCmd())
	root.AddCommand(newFakeTurnCmd())
	return root
}

// newVersionCmd prints the version, commit, date and platform.
//
// Why a separate command: in a bug report the question "which binary" has
// no one-line answer. --version goes on printing the version alone.
func newVersionCmd(info version.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), info.Details())
			return err
		},
	}
}

// Execute runs the root command.
func Execute(info version.Info) error {
	return NewRootCmdWith(info).ExecuteContext(context.Background())
}
