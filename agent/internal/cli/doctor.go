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

// doctorTimeout tum kontrolun ust siniri.
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
		Short: "Relay'leri ve ag yolunu test et",
		Long: `Relay'lerin ve ag yolunun signaling icin uygun olup olmadigini olcer.

Her relay icin:
  1. NIP-11 bilgi dokumani (auth gerekiyor mu, sinirlar ne)
  2. WebSocket baglantisi (ulasilabiliyor mu, ne kadar surede)
  3. Ephemeral yanki testi: kind 25050 yayinlanip ABONEDEN geri okunuyor mu

Ucuncu adim en onemlisi: bir relay event'i "OK" ile kabul edip sessizce
dusurebilir. O zaman signaling calismaz ama hata da gorunmez.

Ayrica STUN erisimi ve NAT tipi tahmini yapar (iki STUN sunucusunun gordugu
dis portu karsilastirir).

--turn verilirse her TURN sunucusundan ALLOCATION ISTENIR: "sunucu ayakta mi"
degil "bana relay adresi veriyor mu" sorusu olculur. Rapor sonunda NAT karari
ile TURN sonucunu birlestiren tek satirlik bir tavsiye yazar.`,
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
			// Cloudflare verilmisse kimlik bilgisini SIMDI cekip gercek
			// adresle test ediyoruz: kullanicinin elle kimlik bilgisi
			// kopyalamasina gerek yok.
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
				return fmt.Errorf("kullanilabilir relay bulunamadi (%d relay denendi)", len(report.Relays))
			}
			// TURN istendi ama hicbiri allocation vermedi: sessizce 0 ile
			// cikmak, kullanicinin TURN'un calistigini sanmasina yol acar.
			if report.TURN != nil && !report.TURN.Usable() {
				return fmt.Errorf("hicbir TURN sunucusu allocation vermedi (%d sunucu denendi)",
					len(report.TURN.Servers))
			}
			return nil
		},
	}

	fl := cmd.Flags()
	fl.StringArrayVar(&relays, "relay", nil,
		"test edilecek relay (tekrarlanabilir; verilmezse varsayilan liste)")
	fl.StringArrayVar(&stuns, "stun", nil,
		"test edilecek STUN sunucusu (tekrarlanabilir; NAT tipi icin en az iki tane)")
	fl.StringArrayVar(&turns, "turn", nil,
		"test edilecek TURN sunucusu (turn:/turns:, tekrarlanabilir)")
	fl.StringVar(&turnUser, "turn-user", "", "TURN kullanici adi")
	fl.StringVar(&turnPass, "turn-pass", "",
		"TURN sifresi ("+envTURNPass+" ortam degiskeni de kullanilabilir)")
	fl.StringVar(&cfKeyID, "turn-cloudflare-key-id", "",
		"Cloudflare Realtime TURN anahtar kimligi (kimlik bilgisi API'den cekilir)")
	fl.StringVar(&cfToken, "turn-cloudflare-token", "",
		"Cloudflare API jetonu ("+envCloudflareToken+" ortam degiskeni onerilir)")
	fl.BoolVar(&asJSON, "json", false, "ciktiyi JSON olarak ver")
	fl.BoolVarP(&verbose, "verbose", "v", false, "ayrintili log")
	return cmd
}

// defaultSTUNServers NAT tipi tahmini icin IKI sunucu gerekiyor.
//
// Liste internal/ice'tan geliyor: daha once hem burada hem serve.go'da
// kopyalanmisti ve ikisi sessizce ayrilabilirdi.
func defaultSTUNServers() []string {
	return ice.DefaultSTUN
}

func printReport(w io.Writer, report doctor.Report) {
	fmt.Fprintln(w, "RELAY'LER")
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "  relay\tdurum\tbaglanti\tyayin\tyanki\tnot")
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

	fmt.Fprintf(w, "\n  kullanilabilir: %d / %d\n", len(report.UsableRelays), len(report.Relays))
	if len(report.UsableRelays) > 0 {
		fmt.Fprintf(w, "  liste: %s\n", strings.Join(report.UsableRelays, " "))
	}

	fmt.Fprintln(w, "\nSTUN / NAT")
	tw = tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "  sunucu\tdurum\tgorulen adres\tsure")
	for _, s := range report.STUN.Servers {
		status := "basarisiz"
		addr := "—"
		if s.OK {
			status = "tamam"
			addr = fmt.Sprintf("%s:%d", s.MappedIP, s.Port)
		}
		detail := ""
		if s.Err != "" {
			detail = "  " + s.Err
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s%s\n", s.URL, status, addr, msOrDash(s.OK, s.Ms), detail)
	}
	_ = tw.Flush()

	fmt.Fprintf(w, "\n  NAT tahmini: %s\n", report.STUN.Verdict)
	if report.STUN.Note != "" {
		fmt.Fprintf(w, "  %s\n", report.STUN.Note)
	}

	if report.TURN != nil {
		fmt.Fprintln(w, "\nTURN")
		tw = tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "  sunucu\tdurum\trelay adresi\tsure\tnot")
		for _, t := range report.TURN.Servers {
			status := "basarisiz"
			addr := "—"
			if t.OK {
				status = "tamam"
				addr = t.RelayedAddr
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n",
				t.URL, status, addr, msOrDash(t.OK, t.MS), t.Err)
		}
		_ = tw.Flush()
	}

	fmt.Fprintln(w, "\nSONUC")
	switch {
	case len(report.UsableRelays) == 0:
		fmt.Fprintln(w, "  Kullanilabilir relay YOK. --relay ile kendi relay'ini ver.")
	case report.TURN != nil && !report.TURN.Usable():
		fmt.Fprintln(w, "  Relay var ama TURN calismiyor.")
	default:
		fmt.Fprintln(w, "  Signaling icin yeterli relay var.")
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
		return "NIP-11 okunamadi: " + r.Nip11Err
	case r.Nip11 != nil && r.Nip11.MinPowDifficulty > 0:
		return fmt.Sprintf("PoW istiyor (zorluk %d)", r.Nip11.MinPowDifficulty)
	case r.Nip11 != nil && r.Nip11.PaymentRequired:
		return "ucretli relay"
	case r.Nip11 != nil && r.Nip11.RestrictedWrites:
		return "yazma kisitli"
	case r.Nip11 != nil && r.Nip11.Name != "":
		return r.Nip11.Name
	default:
		return ""
	}
}
