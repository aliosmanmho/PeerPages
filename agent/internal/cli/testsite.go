package cli

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
)

// ListenMarker is the line that reports the testsite command has started
// listening. The Playwright e2e test reads the real port from this line.
const ListenMarker = "---PEERPAGES-TESTSITE-LISTENING---"

// newTestSiteCmd is the helper command that serves the phase 1 test
// site.
//
// Hidden: it is not a user-facing feature, it exists only because the
// tests need a real local server to put on the other side of the
// tunnel.
func newTestSiteCmd() *cobra.Command {
	var (
		dir  string
		port int
	)

	cmd := &cobra.Command{
		Use:    "testsite",
		Short:  "Serves the phase 1 test site (for tests only)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The 5 MB big.bin is not kept in the repo: generate it if
			// needed.
			if _, _, err := testsite.GenerateBig(dir); err != nil {
				return fmt.Errorf("the test data could not be generated: %w", err)
			}

			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return fmt.Errorf("could not listen: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s http://%s\n", ListenMarker, ln.Addr().String())
			fmt.Fprintf(out, "big.bin sha256: %s\n", testsite.BigFileSHA256())

			srv := &http.Server{
				Handler:           testsite.Handler(dir),
				ReadHeaderTimeout: 10 * time.Second,
			}
			go func() {
				<-cmd.Context().Done()
				_ = srv.Close()
			}()
			if serveErr := srv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
				return serveErr
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "testdata/site", "the directory to serve")
	cmd.Flags().IntVar(&port, "port", 0, "the port to listen on (0 = pick a free one)")
	return cmd
}
