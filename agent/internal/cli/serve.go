package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	osignal "os/signal"
	"syscall"

	"github.com/pion/webrtc/v4"
	"github.com/spf13/cobra"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
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
)

// envTURNPass TURN sifresi icin ortam degiskeni.
//
// Neden bayrak tek yol degil: komut satiri argumanlari `ps` ciktisinda ve
// Gorev Yoneticisi'nde baska kullanicilara gorunur.
const envTURNPass = "PEERPAGES_TURN_PASS"

// envCloudflareToken Cloudflare TURN API jetonu icin ortam degiskeni.
//
// Birincil yol BU: jeton uzun omurlu bir sirdir ve komut satirinda
// durmamali.
const envCloudflareToken = "PEERPAGES_CF_TURN_TOKEN"

// ServeConfig serve komutunun dogrulanmis yapilandirmasi.
type ServeConfig struct {
	Targets       *target.Set
	MaxBody       int64
	MaxConcurrent int
	MaxSessions   int
	// Manual true ise signaling elle (kopyala-yapistir) yapilir; false ise
	// Nostr relay'leri uzerinden (varsayilan).
	Manual bool
	// ManualTarget --manual yolunda kullanilacak hedef adi. Elle signaling'de
	// ziyaretcinin URL'si yok, dolayisiyla hedefi bayrak soyler.
	ManualTarget string
	// ICE dogrulanmis STUN/TURN yapilandirmasi.
	ICE ice.Config
	// Cloudflare doldurulmussa TURN kimlik bilgisi API'den cekilir.
	Cloudflare ice.CloudflareOptions

	// iceProvider calisma zamaninda gecerli ICE listesini verir; runServe
	// dolduruyor.
	iceProvider *ice.Provider
	Relays      []string
	Secret      string
	KeyPath     string
	AppURL      string
	Verbose     bool
	Logger      *slog.Logger
}

type serveFlags struct {
	targets       []string
	maxBody       string
	maxConcurrent int
	maxSessions   int
	manual        bool
	manualTarget  string
	stun          []string
	turn          []string
	turnUser      string
	turnPass      string
	cfKeyID       string
	cfToken       string
	iceRelayOnly  bool
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
	fl.StringVar(&f.manualTarget, "manual-target", "",
		"--manual yolunda yayinlanacak hedefin adi (varsayilan: default)")
	fl.StringArrayVar(&f.relays, "relay", nil,
		"Nostr relay adresi (tekrarlanabilir; verilmezse varsayilan liste)")
	fl.StringVar(&f.secret, "secret", "",
		"paylasilan sir: URL '#npub:secret' olur, sirsiz teklifler reddedilir")
	fl.StringVar(&f.keyPath, "key", "",
		"ajan anahtar dosyasi (varsayilan: ~/.peerpages/key)")
	fl.StringVar(&f.appURL, "app-url", DefaultAppURL,
		"app shell'in yayinlandigi adres (ziyaretci URL'sini basmak icin)")
	fl.StringArrayVar(&f.stun, "stun", ice.DefaultSTUN,
		"ICE icin STUN sunucusu (tekrarlanabilir)")
	fl.StringArrayVar(&f.turn, "turn", nil,
		"TURN sunucusu (turn:/turns:, tekrarlanabilir); simetrik NAT'ta gerekir")
	fl.StringVar(&f.turnUser, "turn-user", "", "TURN kullanici adi")
	fl.StringVar(&f.turnPass, "turn-pass", "",
		"TURN sifresi ("+envTURNPass+" ortam degiskeni de kullanilabilir)")
	fl.StringVar(&f.cfKeyID, "turn-cloudflare-key-id", "",
		"Cloudflare Realtime TURN anahtar kimligi (kimlik bilgisi API'den cekilir)")
	fl.StringVar(&f.cfToken, "turn-cloudflare-token", "",
		"Cloudflare API jetonu ("+envCloudflareToken+" ortam degiskeni onerilir)")
	fl.BoolVar(&f.iceRelayOnly, "ice-relay-only", false,
		"yalnizca TURN relay adayi topla (teshis: dogrudan yolu imkansiz kilar)")
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

	if len(f.stun) == 0 && len(f.turn) == 0 {
		return nil, fmt.Errorf("--stun bos olamaz")
	}

	turnPass := f.turnPass
	if turnPass == "" {
		turnPass = os.Getenv(envTURNPass)
	}
	cfToken := f.cfToken
	if cfToken == "" {
		cfToken = os.Getenv(envCloudflareToken)
	}
	cf := ice.CloudflareOptions{KeyID: f.cfKeyID, Token: cfToken}
	switch {
	case cf.KeyID != "" && cf.Token == "":
		return nil, fmt.Errorf(
			"--turn-cloudflare-key-id verildi ama jeton yok (--turn-cloudflare-token ya da %s)",
			envCloudflareToken)
	case cf.KeyID == "" && cf.Token != "":
		return nil, fmt.Errorf("cloudflare jetonu verildi ama --turn-cloudflare-key-id yok")
	}

	iceCfg := ice.Config{
		STUN:      f.stun,
		TURN:      f.turn,
		User:      f.turnUser,
		Pass:      turnPass,
		RelayOnly: f.iceRelayOnly,
	}
	// Cloudflare kullaniliyorsa TURN adresi ve kimlik bilgisi calisma
	// zamaninda gelir; dogrulama bunu bilmeli, yoksa "relay-only icin turn
	// gerekiyor" ya da "turn-user yok" diye bosa duser.
	if cf.KeyID != "" {
		iceCfg.RuntimeTURN = true
	}
	iceCfg, err = iceCfg.Validated()
	if err != nil {
		return nil, err
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

	// Elle yolda hedefi bayrak secer; adin gercekten var oldugunu burada
	// dogruluyoruz ki hata tunel kurulduktan sonra degil, baslangicta cikisin.
	manualTarget := f.manualTarget
	if manualTarget == "" {
		manualTarget = target.DefaultName
		if !targets.Has(manualTarget) {
			manualTarget = targets.Names()[0]
		}
	} else if !targets.Has(manualTarget) {
		return nil, fmt.Errorf("--manual-target %q: boyle bir hedef yok (tanimli: %v)",
			manualTarget, targets.Names())
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
		ManualTarget:  manualTarget,
		ICE:           iceCfg,
		Cloudflare:    cf,
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

	// ICE saglayicisi: Cloudflare verilmisse kimlik bilgisini SIMDI ceker
	// (basarisizsa ajan baslamaz — kullanici TURN istediyse sessizce
	// TURN'suz devam etmek yanlis olur) ve arka planda yeniler.
	ice, err := newICEProvider(ctx, cfg)
	if err != nil {
		return err
	}
	go ice.Run(ctx)
	cfg.iceProvider = ice

	if cfg.Manual {
		return runManual(ctx, cfg, cmd.InOrStdin(), cmd.OutOrStdout())
	}
	return runNostr(ctx, cfg, cmd.OutOrStdout())
}

// newICEProvider yapilandirmaya gore statik ya da Cloudflare saglayicisi
// dondurur.
func newICEProvider(ctx context.Context, cfg *ServeConfig) (*ice.Provider, error) {
	if cfg.Cloudflare.KeyID == "" {
		return ice.NewProvider(cfg.ICE, cfg.Logger), nil
	}
	return ice.NewCloudflareProvider(ctx, cfg.ICE, cfg.Cloudflare, cfg.Logger)
}

// iceServers oturum basina gecerli ICE listesini dondurur
// (connect.ICEFactory imzasi).
//
// Saglayici yoksa (testler ServeConfig'i elle kuruyor) statik yapilandirma
// kullanilir.
func (c *ServeConfig) iceServers() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
	if c.iceProvider == nil {
		return c.ICE.Servers(), c.ICE.Policy()
	}
	return c.iceProvider.Servers()
}

// iceDescribe log ve banner icin ozet; kimlik bilgisi yazilmaz.
func (c *ServeConfig) iceDescribe() string {
	if c.iceProvider == nil {
		return c.ICE.Describe()
	}
	return c.iceProvider.Config().Describe()
}
