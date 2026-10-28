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

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/size"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
	"github.com/aliosmanmho/PeerPages/agent/internal/wsbridge"
)

// The default limits — BRIEF security principle 4 (the visitor is
// untrusted).
const (
	defaultMaxBody       = "50MB"
	defaultMaxConcurrent = 64
	defaultMaxSessions   = 4
	defaultMaxStreams    = 8
	defaultMaxWSMessage  = "1MB"
)

// envTURNPass is the environment variable for the TURN password.
//
// Why a flag is not the only way: command line arguments are visible to
// other users in `ps` output and in Task Manager.
const envTURNPass = "PEERPAGES_TURN_PASS"

// envCloudflareToken is the environment variable for the Cloudflare TURN
// API token.
//
// THIS is the primary way: the token is a long-lived secret and should not
// sit on a command line.
const envCloudflareToken = "PEERPAGES_CF_TURN_TOKEN"

// ServeConfig is the validated configuration of the serve command.
type ServeConfig struct {
	Targets       *target.Set
	MaxBody       int64
	MaxConcurrent int
	MaxSessions   int
	// MaxStreams is the long-lived stream (ws + SSE) limit per session.
	MaxStreams int
	// MaxWSMessage is the limit for a ws message.
	MaxWSMessage int64
	// InjectWSShim turns the WebSocket bridge on and adds the shim hint to
	// HTML responses (phase 6, off by default: it interferes with the
	// HTML).
	InjectWSShim bool
	// Manual set to true does signaling by hand (copy-paste); false goes
	// over Nostr relays (the default).
	Manual bool
	// ManualTarget is the target name used on the --manual path. With
	// manual signaling there is no visitor URL, so a flag states the
	// target.
	ManualTarget string
	// ICE is the validated STUN/TURN configuration.
	ICE ice.Config
	// When Cloudflare is filled in, the TURN credentials are fetched from
	// the API.
	Cloudflare ice.CloudflareOptions

	// iceProvider provides the ICE list valid at runtime; runServe fills
	// it in.
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
	maxStreams    int
	maxWSMessage  string
	injectWSShim  bool
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
		Short: "Share a local target over the tunnel",
		Long: `Shares a local target over a WebRTC data channel.

By default signaling goes over public Nostr relays: the visitor only opens
the URL. With --manual the offer and answer are carried by hand (neither a
signaling server nor Nostr).

The targets are given in this command and nowhere else; the agent makes no
request to any other host (deriving a host from the request path is not
possible).

Examples:
  peerpages serve --target http://localhost:3000
  peerpages serve -t http://localhost:3000 --secret cat-fish-7
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
		"the local target to share: `url` or `name=url` (repeatable, required)")
	fl.StringVar(&f.maxBody, "max-body", defaultMaxBody,
		"the largest request body accepted from the visitor")
	fl.IntVar(&f.maxConcurrent, "max-concurrent", defaultMaxConcurrent,
		"the most requests handled at once")
	fl.IntVar(&f.maxSessions, "max-sessions", defaultMaxSessions,
		"the most visitors accepted at once")
	fl.IntVar(&f.maxStreams, "max-streams", defaultMaxStreams,
		"how many long-lived streams may be open at once (a WebSocket session or an SSE response)")
	fl.StringVar(&f.maxWSMessage, "max-ws-message", defaultMaxWSMessage,
		"the largest size of a WebSocket message (in both directions)")
	fl.BoolVar(&f.injectWSShim, "inject-ws-shim", false,
		"turn the WebSocket bridge on: a shim is injected into HTML responses (it touches the HTML)")
	fl.BoolVar(&f.manual, "manual", false,
		"manual signaling: you paste the offer the page produced and the agent prints the answer")
	fl.StringVar(&f.manualTarget, "manual-target", "",
		"the name of the target to share on the --manual path (default: default)")
	fl.StringArrayVar(&f.relays, "relay", nil,
		"a Nostr relay address (repeatable; the default list when omitted)")
	fl.StringVar(&f.secret, "secret", "",
		"a shared secret: the URL becomes '#npub:secret' and unsealed offers are refused")
	fl.StringVar(&f.keyPath, "key", "",
		"the agent key file (default: ~/.peerpages/key)")
	fl.StringVar(&f.appURL, "app-url", DefaultAppURL,
		"the address the app shell is published at (used to print the visitor URL)")
	fl.StringArrayVar(&f.stun, "stun", ice.DefaultSTUN,
		"a STUN server for ICE (repeatable)")
	fl.StringArrayVar(&f.turn, "turn", nil,
		"a TURN server (turn:/turns:, repeatable); needed on symmetric NAT")
	fl.StringVar(&f.turnUser, "turn-user", "", "the TURN user name")
	fl.StringVar(&f.turnPass, "turn-pass", "",
		"the TURN password (the "+envTURNPass+" environment variable works too)")
	fl.StringVar(&f.cfKeyID, "turn-cloudflare-key-id", "",
		"the Cloudflare Realtime TURN key id (the credentials are fetched from the API)")
	fl.StringVar(&f.cfToken, "turn-cloudflare-token", "",
		"the Cloudflare API token (the "+envCloudflareToken+" environment variable is preferred)")
	fl.BoolVar(&f.iceRelayOnly, "ice-relay-only", false,
		"gather TURN relay candidates only (diagnostics: it makes the direct path impossible)")
	fl.BoolVarP(&f.verbose, "verbose", "v", false,
		"verbose logging; URL paths are NOT MASKED")

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
		return nil, fmt.Errorf("--max-concurrent must be at least 1, got: %d", f.maxConcurrent)
	}

	if f.maxSessions < 1 {
		return nil, fmt.Errorf("--max-sessions must be at least 1, got: %d", f.maxSessions)
	}

	if f.maxStreams < 1 {
		return nil, fmt.Errorf("--max-streams must be at least 1, got: %d", f.maxStreams)
	}

	maxWSMessage, err := size.Parse(f.maxWSMessage)
	if err != nil {
		return nil, fmt.Errorf("--max-ws-message: %w", err)
	}
	// A ws message has to fit in at least one frame; a smaller limit would
	// refuse every message.
	if maxWSMessage < protocol.MaxWSPayload {
		return nil, fmt.Errorf("--max-ws-message must be at least %d bytes, got: %d",
			protocol.MaxWSPayload, maxWSMessage)
	}

	if len(f.stun) == 0 && len(f.turn) == 0 {
		return nil, fmt.Errorf("--stun must not be empty")
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
			"--turn-cloudflare-key-id was given but there is no token (--turn-cloudflare-token or %s)",
			envCloudflareToken)
	case cf.KeyID == "" && cf.Token != "":
		return nil, fmt.Errorf("a cloudflare token was given but --turn-cloudflare-key-id is missing")
	}

	iceCfg := ice.Config{
		STUN:      f.stun,
		TURN:      f.turn,
		User:      f.turnUser,
		Pass:      turnPass,
		RelayOnly: f.iceRelayOnly,
	}
	// With Cloudflare in use the TURN address and credentials arrive at
	// runtime; the validation has to know that, or it fires for nothing
	// with "relay-only needs turn" or "no turn-user".
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

	// On the manual path a flag picks the target; we check here that the
	// name really exists, so the error comes out at startup rather than
	// after the tunnel is up.
	manualTarget := f.manualTarget
	if manualTarget == "" {
		manualTarget = target.DefaultName
		if !targets.Has(manualTarget) {
			manualTarget = targets.Names()[0]
		}
	} else if !targets.Has(manualTarget) {
		return nil, fmt.Errorf("--manual-target %q: there is no such target (defined: %v)",
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
		MaxStreams:    f.maxStreams,
		MaxWSMessage:  maxWSMessage,
		InjectWSShim:  f.injectWSShim,
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

// newWSDialer builds the WebSocket bridge per target.
//
// The bridge and the shim are turned on by ONE flag
// (--inject-ws-shim): without the shim no browser page sends a ws-open,
// and without the bridge the shim is useless. With the flag omitted it
// returns nil and ws-open requests are refused with ws-close 1008.
func (c *ServeConfig) newWSDialer(targetName string) (tunnel.WSDialer, error) {
	return wsbridge.New(wsbridge.Config{
		Targets:    c.Targets,
		TargetName: targetName,
		MaxMessage: c.MaxWSMessage,
		Logger:     c.Logger,
		Verbose:    c.Verbose,
	})
}

// wsDialerFactory returns nil when the bridge is off (which is what
// connect.Config expects).
func (c *ServeConfig) wsDialerFactory() connect.WSDialerFactory {
	if !c.InjectWSShim {
		return nil
	}
	return c.newWSDialer
}

// wsNote is the bridge state for the banner.
func (c *ServeConfig) wsNote() string {
	if !c.InjectWSShim {
		return "off (--inject-ws-shim turns it on)"
	}
	return fmt.Sprintf("on, injecting the shim, message limit %s",
		size.Format(c.MaxWSMessage))
}

// AuthMode is the auth value announced in the hello message.
func (c *ServeConfig) AuthMode() protocol.AuthMode {
	if c.Secret != "" {
		return protocol.AuthRequired
	}
	return protocol.AuthNone
}

func runServe(cmd *cobra.Command, cfg *ServeConfig) error {
	log := cfg.Logger
	log.Info("starting the agent",
		slog.Any("targets", cfg.Targets.Names()),
		slog.String("max_body", size.Format(cfg.MaxBody)),
		slog.Int("max_concurrent", cfg.MaxConcurrent),
		slog.Int("max_sessions", cfg.MaxSessions),
		slog.Int("max_streams", cfg.MaxStreams),
		slog.Bool("inject_ws_shim", cfg.InjectWSShim),
		slog.Bool("manual_signaling", cfg.Manual),
	)
	if cfg.Verbose {
		log.Warn("verbose is on: URL paths are not masked in the logs")
	}

	// A clean shutdown on Ctrl+C / SIGTERM.
	ctx, stop := osignal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The ICE provider: if Cloudflare was given it fetches the credentials
	// NOW (and the agent does not start if that fails — carrying on
	// without TURN silently would be wrong when the user asked for it) and
	// refreshes them in the background.
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

// newICEProvider returns the static or the Cloudflare provider,
// depending on the configuration.
func newICEProvider(ctx context.Context, cfg *ServeConfig) (*ice.Provider, error) {
	if cfg.Cloudflare.KeyID == "" {
		return ice.NewProvider(cfg.ICE, cfg.Logger), nil
	}
	return ice.NewCloudflareProvider(ctx, cfg.ICE, cfg.Cloudflare, cfg.Logger)
}

// iceServers returns the ICE list valid for a session (the
// connect.ICEFactory signature).
//
// With no provider (the tests build ServeConfig by hand) the static
// configuration is used.
func (c *ServeConfig) iceServers() ([]webrtc.ICEServer, webrtc.ICETransportPolicy) {
	if c.iceProvider == nil {
		return c.ICE.Servers(), c.ICE.Policy()
	}
	return c.iceProvider.Servers()
}

// iceDescribe is the summary for the log and the banner; no credential
// is written out.
func (c *ServeConfig) iceDescribe() string {
	if c.iceProvider == nil {
		return c.ICE.Describe()
	}
	return c.iceProvider.Config().Describe()
}
