package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/proxy"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/size"
	"github.com/aliosmanmho/PeerPages/agent/internal/stats"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// ReadyMarker reports that the agent has started listening for
// signaling.
//
// A machine reads it too: the e2e test waits for this line before opening
// the visitor page (web/e2e/fixtures/agent.ts).
const ReadyMarker = "---PEERPAGES-READY---"

// relayConnectTimeout is the time allowed for connecting to the relays
// ahead of time.
const relayConnectTimeout = 20 * time.Second

// visitorTargetLines builds the "name, local address, visitor URL" block
// for every target.
//
// With several targets one URL is not enough: which project the visitor
// opens is written in the URL itself (`#npub1.../admin`).
func visitorTargetLines(id identity.Identity, cfg *ServeConfig) (string, error) {
	var b strings.Builder
	names := cfg.Targets.Names()
	width := 0
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}

	for _, name := range names {
		base, err := cfg.Targets.Resolve(name, "/")
		if err != nil {
			return "", err
		}
		url, err := id.VisitorURL(cfg.AppURL, cfg.Secret, name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", width, name, base.String())
		fmt.Fprintf(&b, "  %-*s  %s\n\n", width, "", url)
	}
	return b.String(), nil
}

// runNostr runs with Nostr signaling: the visitor only opens the URL.
func runNostr(ctx context.Context, cfg *ServeConfig, stdout io.Writer) error {
	log := cfg.Logger

	id, created, err := identity.LoadOrCreate(cfg.KeyPath)
	if err != nil {
		return fmt.Errorf("the agent identity could not be read: %w", err)
	}
	npub, err := id.Npub()
	if err != nil {
		return err
	}
	if created {
		log.Info("agent identity created", slog.String("file", cfg.KeyPath))
	}

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: cfg.Relays, Logger: log})
	if err != nil {
		return err
	}
	defer pool.Close("the agent is shutting down")

	var sealer *signaling.Sealer
	if cfg.Secret != "" {
		if sealer, err = signaling.NewSealer(cfg.Secret); err != nil {
			return err
		}
	}

	counters := stats.New(cfg.Targets.Names())

	// The target is per session now: the visitor states it in the URL and
	// it arrives in the offer. It is validated in the connect layer; only
	// valid names reach here.
	newProxy := func(targetName string) (tunnel.Proxy, error) {
		return proxy.New(proxy.Config{
			Targets:      cfg.Targets,
			TargetName:   targetName,
			Stats:        counters.Target(targetName),
			MaxBody:      cfg.MaxBody,
			Logger:       log,
			Verbose:      cfg.Verbose,
			InjectWSShim: cfg.InjectWSShim,
		})
	}

	acceptor, err := connect.New(connect.Config{
		Identity:      id,
		Pool:          pool,
		Sealer:        sealer,
		NewProxy:      newProxy,
		NewWSDialer:   cfg.wsDialerFactory(),
		Targets:       cfg.Targets.Names(),
		ICE:           cfg.iceServers,
		MaxSessions:   cfg.MaxSessions,
		MaxConcurrent: cfg.MaxConcurrent,
		MaxBody:       cfg.MaxBody,
		MaxStreams:    cfg.MaxStreams,
		MaxWSMessage:  cfg.MaxWSMessage,
		Logger:        log,
		Verbose:       cfg.Verbose,
	})
	if err != nil {
		return err
	}

	targetLines, err := visitorTargetLines(id, cfg)
	if err != nil {
		return err
	}

	// THE ORDER MATTERS: connect to the relays first, then subscribe, and
	// only THEN say "ready". Because ephemeral events are not stored, an
	// offer that arrives before we listen is lost with no way back. (In
	// the e2e run against real relays the first connection failed for
	// exactly this reason.)
	connectCtx, cancelConnect := context.WithTimeout(ctx, relayConnectTimeout)
	defer cancelConnect()

	connReport, err := pool.Connect(connectCtx)
	if err != nil {
		return fmt.Errorf("the relays could not be reached: %w", err)
	}
	for relayURL, reason := range connReport.Failed {
		log.Warn("a relay could not be reached",
			slog.String("relay", relayURL), slog.String("err", reason))
	}

	errCh := make(chan error, 1)
	go func() { errCh <- acceptor.Run(ctx) }()

	select {
	case <-acceptor.Ready():
	case runErr := <-errCh:
		return runErr
	case <-ctx.Done():
		return nil
	}

	fmt.Fprintf(stdout, `
The PeerPages agent is ready.

  peer-id : %s
  relays  : %d/%d connected (the status line shows this live)
  secret  : %s
  ice     : %s
  ws      : %s

The published targets and the addresses the visitor should open:

%s
Keep this tab or terminal open: the tunnel is up as long as the agent runs.
%s
`,
		identity.ShortenNpub(npub),
		len(connReport.OK),
		len(connReport.OK)+len(connReport.Failed),
		secretNote(cfg.Secret),
		cfg.ICE.Describe(),
		cfg.wsNote(),
		targetLines,
		ReadyMarker,
	)

	// Print the counter line on its own goroutine; it stops when the agent
	// shuts down.
	statusCtx, stopStatus := context.WithCancel(ctx)
	defer stopStatus()
	go printStatusLines(statusCtx, stdout, acceptor, counters)

	// Keep going even if the relays go down: Run renews the subscription
	// itself (phase 4) and only returns once ctx ends.
	runErr := <-errCh
	stopStatus()

	total := counters.Total()
	log.Info("agent shut down",
		slog.Int("open_sessions", acceptor.SessionCount()),
		slog.Uint64("requests", total.Requests),
		slog.Uint64("success", total.Success),
		slog.Uint64("errors", total.Errors),
		slog.Uint64("bytes_in", total.BytesIn),
		slog.Uint64("bytes_out", total.BytesOut))
	return runErr
}

// statusInterval is how often the live status line refreshes.
const statusInterval = 5 * time.Second

// printStatusLines prints the session and request counters to stdout.
//
// It prints only WHEN SOMETHING CHANGES: we do not want to fill the
// terminal of an idle agent. The logs go to stderr so they do not mix
// with these lines, and because they come after the ReadyMarker they do
// not affect the machine-read e2e fixture.
func printStatusLines(
	ctx context.Context, out io.Writer, acceptor *connect.Acceptor, counters *stats.Registry,
) {
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()

	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		t := counters.Total()
		// The relay field matters: when every relay goes down the agent
		// keeps running but no visitor can reach it. That information was
		// nowhere to be seen.
		healthy, total := acceptor.RelayStatus()
		line := fmt.Sprintf("[status] relays %d/%d | sessions %d | requests %d (success %d, errors %d) | in %s | out %s",
			healthy, total,
			acceptor.SessionCount(), t.Requests, t.Success, t.Errors,
			size.Format(int64(t.BytesIn)), size.Format(int64(t.BytesOut)))
		if line == last {
			continue
		}
		last = line
		fmt.Fprintln(out, line)
	}
}

func secretNote(secret string) string {
	if secret == "" {
		return "none (anyone who knows the peer-id can connect)"
	}
	return fmt.Sprintf("set (%d characters) — carried in the URL", len(secret))
}
