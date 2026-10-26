package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	osignal "os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/size"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
)

// Varsayilan limitler — README guvenlik ilkesi 4 (ziyaretci guvenilmez).
const (
	defaultMaxBody       = "50MB"
	defaultMaxConcurrent = 64
	defaultSTUN          = "stun:stun.l.google.com:19302"
)

// ServeConfig serve komutunun dogrulanmis yapilandirmasi.
type ServeConfig struct {
	Targets       *target.Set
	MaxBody       int64
	MaxConcurrent int
	Manual        bool
	STUN          []string
	Verbose       bool
	Logger        *slog.Logger
}

type serveFlags struct {
	targets       []string
	maxBody       string
	maxConcurrent int
	manual        bool
	stun          []string
	verbose       bool
}

func newServeCmd() *cobra.Command {
	f := &serveFlags{}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Yerel hedefi tunel uzerinden yayinla",
		Long: `Yerel hedefi WebRTC data channel uzerinden yayinlar.

Hedefler yalnizca bu komutta verilir; ajan baska hicbir hosta istek atmaz
(istek yolundan host turetmek mumkun degil).

Ornekler:
  peerpages serve --target http://localhost:3000
  peerpages serve -t http://localhost:3000 -t admin=http://localhost:4000`,
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
	fl.BoolVar(&f.manual, "manual", true,
		"elle signaling: offer stdout'a basilir, answer stdin'den okunur (Faz 1'de tek mod)")
	fl.StringArrayVar(&f.stun, "stun", []string{defaultSTUN},
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

	if !f.manual {
		// Faz 2'ye kadar baska signaling yolu yok; sessizce calismaya devam
		// etmek yerine acikca soyle.
		return nil, fmt.Errorf("--manual=false henuz desteklenmiyor: Nostr signaling Faz 2'de gelecek")
	}

	if len(f.stun) == 0 {
		return nil, fmt.Errorf("--stun bos olamaz")
	}

	return &ServeConfig{
		Targets:       targets,
		MaxBody:       maxBody,
		MaxConcurrent: f.maxConcurrent,
		Manual:        f.manual,
		STUN:          f.stun,
		Verbose:       f.verbose,
		Logger:        logging.New(logTo, f.verbose),
	}, nil
}

// AuthMode hello mesajinda bildirilecek auth degeri.
//
// Faz 1'de paylasilan sir yok; --secret Faz 2'de gelecek.
func (c *ServeConfig) AuthMode() protocol.AuthMode {
	return protocol.AuthNone
}

func runServe(cmd *cobra.Command, cfg *ServeConfig) error {
	log := cfg.Logger
	log.Info("ajan baslatiliyor",
		slog.Any("targets", cfg.Targets.Names()),
		slog.String("max_body", size.Format(cfg.MaxBody)),
		slog.Int("max_concurrent", cfg.MaxConcurrent),
		slog.Bool("manual_signaling", cfg.Manual),
	)
	if cfg.Verbose {
		log.Warn("verbose acik: URL path'leri loglarda maskelenmiyor")
	}

	// Ctrl+C / SIGTERM ile temiz kapanis.
	ctx, stop := osignal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runManual(ctx, cfg, cmd.InOrStdin(), cmd.OutOrStdout())
}
