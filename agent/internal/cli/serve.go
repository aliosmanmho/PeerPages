package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	osignal "os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/size"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// Varsayilan limitler — README guvenlik ilkesi 4 (ziyaretci guvenilmez).
const (
	defaultMaxBody       = "50MB"
	defaultMaxConcurrent = 64
	defaultMaxSessions   = 4
	defaultSTUN          = "stun:stun.l.google.com:19302"
	// defaultSTUN2 NAT tipi tahmini ve yedek icin ikinci sunucu.
	defaultSTUN2 = "stun:stun1.l.google.com:19302"
)

// ServeConfig serve komutunun dogrulanmis yapilandirmasi.
type ServeConfig struct {
	Targets       *target.Set
	MaxBody       int64
	MaxConcurrent int
	MaxSessions   int
	// Manual true ise signaling elle (kopyala-yapistir) yapilir; false ise
	// Nostr relay'leri uzerinden (varsayilan).
	Manual  bool
	STUN    []string
	Relays  []string
	Secret  string
	KeyPath string
	AppURL  string
	Verbose bool
	Logger  *slog.Logger
}

type serveFlags struct {
	targets       []string
	maxBody       string
	maxConcurrent int
	maxSessions   int
	manual        bool
	stun          []string
	relays        []string
	secret        string
	keyPath       string
	appURL        string
	verbose       bool
}

func newServeCmd() *cobra.Command {
	f := &serveFlags{}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Yerel hedefi tunel uzerinden yayinla",
		Long: `Yerel hedefi WebRTC data channel uzerinden yayinlar.

Varsayilan olarak signaling herkese acik Nostr relay'leri uzerinden yapilir:
ziyaretci yalnizca URL'yi acar. --manual ile offer/answer elle tasinir
(signaling sunucusu da Nostr da yok).

Hedefler yalnizca bu komutta verilir; ajan baska hicbir hosta istek atmaz
(istek yolundan host turetmek mumkun degil).

Ornekler:
  peerpages serve --target http://localhost:3000
  peerpages serve -t http://localhost:3000 --secret kedi-balik-7
  peerpages serve -t http://localhost:3000 --manual
  peerpages serve -t http://localhost:3000 --relay wss://nos.lol`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := f.toConfig(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return runServe(cmd, cfg)
		},
	}

	fl := cmd.Flags()
	fl.StringArrayVarP(&f.targets, "target", "t", nil,
		"yayinlanacak yerel hedef: `url` ya da `ad=url` (tekrarlanabilir, zorunlu)")
	fl.StringVar(&f.maxBody, "max-body", defaultMaxBody,
		"ziyaretciden kabul edilecek en buyuk istek govdesi")
	fl.IntVar(&f.maxConcurrent, "max-concurrent", defaultMaxConcurrent,
		"ayni anda islenecek en fazla istek sayisi")
	fl.IntVar(&f.maxSessions, "max-sessions", defaultMaxSessions,
		"ayni anda kabul edilecek en fazla ziyaretci")
	fl.BoolVar(&f.manual, "manual", false,
		"elle signaling: sayfanin urettigi offer yapistirilir, ajan answer basar")
	fl.StringArrayVar(&f.relays, "relay", nil,
		"Nostr relay adresi (tekrarlanabilir; verilmezse varsayilan liste)")
	fl.StringVar(&f.secret, "secret", "",
		"paylasilan sir: URL '#npub:secret' olur, sirsiz teklifler reddedilir")
	fl.StringVar(&f.keyPath, "key", "",
		"ajan anahtar dosyasi (varsayilan: ~/.peerpages/key)")
	fl.StringVar(&f.appURL, "app-url", DefaultAppURL,
		"app shell'in yayinlandigi adres (ziyaretci URL'sini basmak icin)")
	fl.StringArrayVar(&f.stun, "stun", []string{defaultSTUN, defaultSTUN2},
		"ICE icin STUN sunucusu (tekrarlanabilir)")
	fl.BoolVarP(&f.verbose, "verbose", "v", false,
		"ayrintili log; URL path'leri MASKELENMEZ")

	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func (f *serveFlags) toConfig(logTo io.Writer) (*ServeConfig, error) {
	targets, err := target.Parse(f.targets)
	if err != nil {
		return nil, err
	}

	maxBody, err := size.Parse(f.maxBody)
	if err != nil {
		return nil, fmt.Errorf("--max-body: %w", err)
	}

	if f.maxConcurrent < 1 {
		return nil, fmt.Errorf("--max-concurrent en az 1 olmali, verilen: %d", f.maxConcurrent)
	}

	if f.maxSessions < 1 {
		return nil, fmt.Errorf("--max-sessions en az 1 olmali, verilen: %d", f.maxSessions)
	}

	if len(f.stun) == 0 {
		return nil, fmt.Errorf("--stun bos olamaz")
	}

	relays := f.relays
	if !f.manual {
		if len(relays) == 0 {
			relays = nostr.DefaultRelays
		}
		normalized, rerr := nostr.NormalizeURLs(relays)
		if rerr != nil {
			return nil, fmt.Errorf("--relay: %w", rerr)
		}
		relays = normalized
	}

	keyPath := f.keyPath
	if keyPath == "" && !f.manual {
		p, kerr := identity.DefaultKeyPath()
		if kerr != nil {
			return nil, kerr
		}
		keyPath = p
	}

	return &ServeConfig{
		Targets:       targets,
		MaxBody:       maxBody,
		MaxConcurrent: f.maxConcurrent,
		MaxSessions:   f.maxSessions,
		Manual:        f.manual,
		STUN:          f.stun,
		Relays:        relays,
		Secret:        f.secret,
		KeyPath:       keyPath,
		AppURL:        f.appURL,
		Verbose:       f.verbose,
		Logger:        logging.New(logTo, f.verbose),
	}, nil
}

// AuthMode hello mesajinda bildirilecek auth degeri.
func (c *ServeConfig) AuthMode() protocol.AuthMode {
	if c.Secret != "" {
		return protocol.AuthRequired
	}
	return protocol.AuthNone
}

func runServe(cmd *cobra.Command, cfg *ServeConfig) error {
	log := cfg.Logger
	log.Info("ajan baslatiliyor",
		slog.Any("targets", cfg.Targets.Names()),
		slog.String("max_body", size.Format(cfg.MaxBody)),
		slog.Int("max_concurrent", cfg.MaxConcurrent),
		slog.Int("max_sessions", cfg.MaxSessions),
		slog.Bool("manual_signaling", cfg.Manual),
	)
	if cfg.Verbose {
		log.Warn("verbose acik: URL path'leri loglarda maskelenmiyor")
	}

	// Ctrl+C / SIGTERM ile temiz kapanis.
	ctx, stop := osignal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Manual {
		return runManual(ctx, cfg, cmd.InOrStdin(), cmd.OutOrStdout())
	}
	return runNostr(ctx, cfg, cmd.OutOrStdout())
}
