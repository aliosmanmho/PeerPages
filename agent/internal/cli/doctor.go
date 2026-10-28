package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osignal "os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/doctor"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
)

// doctorTimeout bounds the whole check.
const doctorTimeout = 60 * time.Second

func newDoctorCmd() *cobra.Command {
	var (
		relays   []string
		stuns    []string
		turns    []string
		turnUser string
		turnPass string
		cfKeyID  string
		cfToken  string
		asJSON   bool
		verbose  bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Test the relays and the network path",
		Long: `Measures whether the relays and the network path are fit for signaling.

For every relay:
  1. the NIP-11 information document (does it want auth, what are the limits)
  2. the WebSocket connection (is it reachable, and how fast)
  3. the ephemeral echo test: can a kind 25050 event be published and read
     back FROM A SUBSCRIPTION

The third step matters most: a relay can accept an event with "OK" and
drop it silently. Then signaling does not work and no error shows up.

It also checks STUN reachability and guesses the NAT type (by comparing
the external port two STUN servers see).

With --turn, every TURN server is ASKED FOR AN ALLOCATION: the question
measured is not "is the server up" but "does it give me a relay address".
At the end of the report it writes one line of advice that joins the NAT
verdict and the TURN result.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := logging.New(cmd.ErrOrStderr(), verbose)

			if len(relays) == 0 {
				relays = nostr.DefaultRelays
			}
			if len(stuns) == 0 {
				stuns = defaultSTUNServers()
			}

			ctx, stop := osignal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
			defer cancel()

			turnPassword := turnPass
			if turnPassword == "" {
				turnPassword = os.Getenv(envTURNPass)
			}
			cfApiToken := cfToken
			if cfApiToken == "" {
				cfApiToken = os.Getenv(envCloudflareToken)
			}

			iceCfg := ice.Config{STUN: stuns, TURN: turns, User: turnUser, Pass: turnPassword}
			// If Cloudflare was given we fetch the credentials NOW and
			// test against the real address: the user does not have to
			// copy credentials by hand.
			if cfKeyID != "" {
				creds, cerr := ice.FetchCredentials(ctx, ice.CloudflareOptions{
					KeyID: cfKeyID, Token: cfApiToken,
				})
				if cerr != nil {
					return cerr
				}
				iceCfg.TURN = append(iceCfg.TURN, creds.URLs...)
				iceCfg.User = creds.Username
				iceCfg.Pass = creds.Password
			}
			iceCfg, ierr := iceCfg.Validated()
			if ierr != nil {
				return ierr
			}

			report := doctor.Run(ctx, relays, iceCfg, log, doctor.Options{})

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printReport(out, report)
			}

			if len(report.UsableRelays) == 0 {
				return fmt.Errorf("no usable relay was found (%d relays tried)", len(report.Relays))
			}
			// TURN was asked for but none granted an allocation: exiting
			// quietly with 0 would let the user think TURN works.
			if report.TURN != nil && !report.TURN.Usable() {
				return fmt.Errorf("no TURN server granted an allocation (%d servers tried)",
					len(report.TURN.Servers))
			}
			return nil
		},
	}

	fl := cmd.Flags()
	fl.StringArrayVar(&relays, "relay", nil,
		"a relay to test (repeatable; the default list when omitted)")
	fl.StringArrayVar(&stuns, "stun", nil,
		"a STUN server to test (repeatable; at least two for the NAT type)")
	fl.StringArrayVar(&turns, "turn", nil,
		"a TURN server to test (turn:/turns:, repeatable)")
	fl.StringVar(&turnUser, "turn-user", "", "the TURN user name")
	fl.StringVar(&turnPass, "turn-pass", "",
		"the TURN password (the "+envTURNPass+" environment variable works too)")
	fl.StringVar(&cfKeyID, "turn-cloudflare-key-id", "",
		"the Cloudflare Realtime TURN key id (the credentials are fetched from the API)")
	fl.StringVar(&cfToken, "turn-cloudflare-token", "",
		"the Cloudflare API token (the "+envCloudflareToken+" environment variable is preferred)")
	fl.BoolVar(&asJSON, "json", false, "write the output as JSON")
	fl.BoolVarP(&verbose, "verbose", "v", false, "verbose logging")
	return cmd
}

// defaultSTUNServers: the NAT type guess needs TWO servers.
//
// The list comes from internal/ice: it used to be copied both here and in
// serve.go, and the two could drift apart silently.
func defaultSTUNServers() []string {
	return ice.DefaultSTUN
}

func printReport(w io.Writer, report doctor.Report) {
	fmt.Fprintln(w, "RELAYS")
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "  relay\tverdict\tconnect\tpublish\techo\tnote")
	for _, r := range report.Relays {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			r.URL,
			r.Verdict,
			msOrDash(r.Connected, r.ConnectMs),
			msOrDash(r.Published, r.PublishMs),
			msOrDash(r.Echoed, r.EchoMs),
			note(r),
		)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n  usable: %d / %d\n", len(report.UsableRelays), len(report.Relays))
	if len(report.UsableRelays) > 0 {
		fmt.Fprintf(w, "  list: %s\n", strings.Join(report.UsableRelays, " "))
	}

	fmt.Fprintln(w, "\nSTUN / NAT")
	tw = tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "  server\tstatus\tseen address\ttime")
	for _, s := range report.STUN.Servers {
		status := "failed"
		addr := "—"
		if s.OK {
			status = "ok"
			addr = fmt.Sprintf("%s:%d", s.MappedIP, s.Port)
		}
		detail := ""
		if s.Err != "" {
			detail = "  " + s.Err
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s%s\n", s.URL, status, addr, msOrDash(s.OK, s.Ms), detail)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n  NAT guess: %s\n", report.STUN.Verdict)
	if report.STUN.Note != "" {
		fmt.Fprintf(w, "  %s\n", report.STUN.Note)
	}

	if report.TURN != nil {
		fmt.Fprintln(w, "\nTURN")
		tw = tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "  server\tstatus\trelay address\ttime\tnote")
		for _, t := range report.TURN.Servers {
			status := "failed"
			addr := "—"
			if t.OK {
				status = "ok"
				addr = t.RelayedAddr
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
				t.URL, status, addr, msOrDash(t.OK, t.MS), t.Err)
		}
		_ = tw.Flush()
	}

	fmt.Fprintln(w, "\nVERDICT")
	switch {
	case len(report.UsableRelays) == 0:
		fmt.Fprintln(w, "  NO usable relay. Pass your own with --relay.")
	case report.TURN != nil && !report.TURN.Usable():
		fmt.Fprintln(w, "  There are relays but TURN does not work.")
	default:
		fmt.Fprintln(w, "  There are enough relays for signaling.")
	}
	if report.Advice != "" {
		fmt.Fprintf(w, "  %s\n", report.Advice)
	}
}

func msOrDash(ok bool, ms float64) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%.0f ms", ms)
}

func note(r doctor.RelayReport) string {
	switch {
	case r.Err != "":
		return r.Err
	case r.Nip11Err != "":
		return "NIP-11 could not be read: " + r.Nip11Err
	case r.Nip11 != nil && r.Nip11.MinPowDifficulty > 0:
		return fmt.Sprintf("wants PoW (difficulty %d)", r.Nip11.MinPowDifficulty)
	case r.Nip11 != nil && r.Nip11.PaymentRequired:
		return "paid relay"
	case r.Nip11 != nil && r.Nip11.RestrictedWrites:
		return "writes restricted"
	case r.Nip11 != nil && r.Nip11.Name != "":
		return r.Nip11.Name
	default:
		return ""
	}
}
