package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/proxy"
	"github.com/aliosmanmho/PeerPages/agent/internal/signal"
	"github.com/aliosmanmho/PeerPages/agent/internal/stats"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// The markers printed to stdout in the manual signaling flow.
//
// Both humans and machines read them: the Playwright e2e test takes the
// answer from between these markers, writes the offer to stdin and waits
// for TunnelOpenMarker (web/e2e/fixtures/agent.ts).
//
// DIRECTION: the roles swapped in phase 2. The PAGE produces the offer now
// and the agent answers (protocol/signaling.md), so the copy direction is
// the reverse of phase 1.
const (
	OfferPrompt      = "---PEERPAGES-OFFER-WAITING---"
	AnswerBeginMark  = "---PEERPAGES-ANSWER-BEGIN---"
	AnswerEndMark    = "---PEERPAGES-ANSWER-END---"
	TunnelOpenMarker = "---PEERPAGES-TUNNEL-OPEN---"
	TunnelClosedMark = "---PEERPAGES-TUNNEL-CLOSED---"

	channelOpenWait   = 60 * time.Second
	iceGatheringLimit = 45 * time.Second
)

// runManual runs a single tunnel session with manual signaling.
//
// The flow: the visitor page produces an offer -> the user pastes it into
// the agent -> the agent prints an answer -> the user pastes it into the
// page -> the tunnel opens. NO signaling server and no Nostr.
func runManual(ctx context.Context, cfg *ServeConfig, stdin io.Reader, stdout io.Writer) error {
	log := cfg.Logger

	// On the manual path there is no visitor URL, so a flag states the
	// target (--manual-target); it was validated in serve.go.
	targetName := cfg.ManualTarget

	iceServers, icePolicy := cfg.iceServers()

	counters := stats.New(cfg.Targets.Names())

	px, err := proxy.New(proxy.Config{
		Targets:      cfg.Targets,
		TargetName:   targetName,
		Stats:        counters.Target(targetName),
		MaxBody:      cfg.MaxBody,
		Logger:       log,
		Verbose:      cfg.Verbose,
		InjectWSShim: cfg.InjectWSShim,
	})
	if err != nil {
		return err
	}

	// When the bridge is off it stays nil and ws-open is refused.
	var wsDialer tunnel.WSDialer
	if cfg.InjectWSShim {
		if wsDialer, err = cfg.newWSDialer(targetName); err != nil {
			return err
		}
	}

	sess, err := tunnel.New(tunnel.Config{
		ICEServers:    iceServers,
		ICEPolicy:     icePolicy,
		Targets:       cfg.Targets.Names(),
		Auth:          cfg.AuthMode(),
		MaxConcurrent: cfg.MaxConcurrent,
		MaxBody:       cfg.MaxBody,
		MaxStreams:    cfg.MaxStreams,
		MaxWSMessage:  cfg.MaxWSMessage,
		Proxy:         px,
		WSDialer:      wsDialer,
		Logger:        log,
		Verbose:       cfg.Verbose,
		// Manual signaling: let the candidates travel inside the complete
		// SDP so a single blob can be copied. Trickle is turned on on the
		// Nostr path.
		Trickle: false,
	})
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil {
			log.Debug("while closing the session", slog.String("err", cerr.Error()))
		}
	}()

	fmt.Fprintf(stdout, `
Open the visitor page (the connect screen), press "Create offer", then
paste the offer it produced below and press Enter.

%s
`, OfferPrompt)

	offerLine, err := readPastedLine(ctx, stdin)
	if err != nil {
		return err
	}
	offer, err := signal.Decode(offerLine)
	if err != nil {
		return fmt.Errorf("the offer could not be read: %w", err)
	}

	// No credential reaches the log: Describe only writes the addresses
	// and the password length.
	log.Info("gathering ICE candidates", slog.String("ice", cfg.iceDescribe()))
	answerCtx, cancelAnswer := context.WithTimeout(ctx, iceGatheringLimit)
	defer cancelAnswer()

	answer, err := sess.Answer(answerCtx, offer)
	if err != nil {
		return fmt.Errorf("the answer could not be created: %w", err)
	}
	encoded, err := signal.Encode(answer)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\n%s\n%s\n%s\n", AnswerBeginMark, encoded, AnswerEndMark)
	fmt.Fprint(stdout, "\nPaste this answer into the answer field on the visitor page.\n")

	if err := sess.WaitReady(ctx, channelOpenWait); err != nil {
		return fmt.Errorf("the tunnel could not be opened: %w", err)
	}

	fmt.Fprintf(stdout, "\n%s\n", TunnelOpenMarker)
	log.Info("tunnel open",
		slog.String("target", targetName),
		slog.Any("targets", cfg.Targets.Names()))

	select {
	case <-sess.Done():
		fmt.Fprintf(stdout, "\n%s\n", TunnelClosedMark)
		if serr := sess.Err(); serr != nil {
			return serr
		}
		log.Info("tunnel closed")
		return nil
	case <-ctx.Done():
		fmt.Fprintf(stdout, "\n%s\n", TunnelClosedMark)
		log.Info("shutting down")
		return nil
	}
}

// readPastedLine reads a single pasted line from stdin.
//
// The read happens on its own goroutine; otherwise Ctrl+C does nothing
// until a line arrives (a stdin read cannot be cancelled).
func readPastedLine(ctx context.Context, stdin io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		reader := bufio.NewReaderSize(stdin, 1<<16)
		for {
			line, err := reader.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				ch <- result{line: trimmed}
				return
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					ch <- result{err: errors.New("the input could not be read: the input ended")}
					return
				}
				ch <- result{err: fmt.Errorf("the input could not be read: %w", err)}
				return
			}
		}
	}()

	select {
	case r := <-ch:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
