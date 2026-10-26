package cli

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/testsite"
)

// ListenMarker testsite komutunun dinlemeye basladigini bildiren satir.
// Playwright e2e testi gercek portu bu satirdan okur.
const ListenMarker = "---PEERPAGES-TESTSITE-LISTENING---"

// newTestSiteCmd Faz 1 test sitesini servis eden yardimci komut.
//
// Gizli: kullaniciya yonelik bir ozellik degil, yalnizca testlerin tunelin
// karsisina koyacak gercek bir yerel sunucuya ihtiyaci oldugu icin var.
func newTestSiteCmd() *cobra.Command {
	var (
		dir  string
		port int
	)

	cmd := &cobra.Command{
		Use:    "testsite",
		Short:  "Faz 1 test sitesini servis eder (yalnizca testler icin)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// 5 MB'lik big.bin repoda tutulmuyor: gerekirse uret.
			if _, _, err := testsite.GenerateBig(dir); err != nil {
				return fmt.Errorf("test verisi uretilemedi: %w", err)
			}

			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				return fmt.Errorf("dinlenemedi: %w", err)
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

	cmd.Flags().StringVar(&dir, "dir", "testdata/site", "servis edilecek dizin")
	cmd.Flags().IntVar(&port, "port", 0, "dinlenecek port (0 = bos bir port sec)")
	return cmd
}
