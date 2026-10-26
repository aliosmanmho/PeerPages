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
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Elle signaling akisinda stdout'a basilan isaretciler.
//
// Hem insan hem makine okur: Playwright e2e testi offer'i bu isaretciler
// arasindan alir, answer'i stdin'e yazar ve TunnelOpenMarker'i bekler
// (web/e2e/tunnel.spec.ts).
const (
	OfferBeginMarker  = "---PEERPAGES-OFFER-BEGIN---"
	OfferEndMarker    = "---PEERPAGES-OFFER-END---"
	AnswerPrompt      = "---PEERPAGES-ANSWER-BEKLENIYOR---"
	TunnelOpenMarker  = "---PEERPAGES-TUNNEL-OPEN---"
	TunnelClosedMark  = "---PEERPAGES-TUNNEL-CLOSED---"
	channelOpenWait   = 60 * time.Second
	iceGatheringLimit = 45 * time.Second
)

// runManual elle signaling ile tek bir tunel oturumu calistirir.
//
// Akis (README Faz 1): ajan offer'i stdout'a basar -> kullanici ziyaretci
// sayfasina yapistirir -> sayfanin urettigi answer stdin'e yapistirilir ->
// tunel acilir. Signaling sunucusu ve Nostr YOK.
func runManual(ctx context.Context, cfg *ServeConfig, stdin io.Reader, stdout io.Writer) error {
	log := cfg.Logger

	// Faz 1'de tek hedef kullanilir; URL ile hedef secimi Faz 3'te gelecek.
	targetName := target.DefaultName
	if !cfg.Targets.Has(targetName) {
		names := cfg.Targets.Names()
		targetName = names[0]
	}

	px, err := proxy.New(proxy.Config{
		Targets:    cfg.Targets,
		TargetName: targetName,
		MaxBody:    cfg.MaxBody,
		Logger:     log,
		Verbose:    cfg.Verbose,
	})
	if err != nil {
		return err
	}

	sess, err := tunnel.New(tunnel.Config{
		STUN:          cfg.STUN,
		Targets:       cfg.Targets.Names(),
		Auth:          cfg.AuthMode(),
		MaxConcurrent: cfg.MaxConcurrent,
		MaxBody:       cfg.MaxBody,
		Proxy:         px,
		Logger:        log,
		Verbose:       cfg.Verbose,
	})
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil {
			log.Debug("oturum kapatilirken", slog.String("err", cerr.Error()))
		}
	}()

	offerCtx, cancelOffer := context.WithTimeout(ctx, iceGatheringLimit)
	defer cancelOffer()

	log.Info("ICE adaylari toplaniyor", slog.Any("stun", cfg.STUN))
	offer, err := sess.Offer(offerCtx)
	if err != nil {
		return fmt.Errorf("offer uretilemedi: %w", err)
	}
	encoded, err := signal.Encode(offer)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\n%s\n%s\n%s\n", OfferBeginMarker, encoded, OfferEndMarker)
	fmt.Fprintf(stdout, `
Bu offer'i ziyaretci sayfasindaki "Ajanin offer'ini yapistir" alanina
yapistir, "Offer'i kabul et" butonuna bas ve sayfanin urettigi answer'i
asagiya yapistirip Enter'a bas.

%s
`, AnswerPrompt)

	answerLine, err := readAnswer(ctx, stdin)
	if err != nil {
		return err
	}
	answer, err := signal.Decode(answerLine)
	if err != nil {
		return fmt.Errorf("answer okunamadi: %w", err)
	}
	if err := sess.Accept(answer); err != nil {
		return err
	}

	if err := sess.WaitReady(ctx, channelOpenWait); err != nil {
		return fmt.Errorf("tunel acilamadi: %w", err)
	}

	fmt.Fprintf(stdout, "\n%s\n", TunnelOpenMarker)
	log.Info("tunel acik",
		slog.String("target", targetName),
		slog.Any("targets", cfg.Targets.Names()))

	select {
	case <-sess.Done():
		fmt.Fprintf(stdout, "\n%s\n", TunnelClosedMark)
		if serr := sess.Err(); serr != nil {
			return serr
		}
		log.Info("tunel kapandi")
		return nil
	case <-ctx.Done():
		fmt.Fprintf(stdout, "\n%s\n", TunnelClosedMark)
		log.Info("kapatiliyor")
		return nil
	}
}

// readAnswer stdin'den tek satirlik answer'i okur.
//
// Okuma ayri bir goroutine'de yapilir; aksi halde Ctrl+C bir satir
// gelmedikce islemez (stdin okumasi iptal edilemez).
func readAnswer(ctx context.Context, stdin io.Reader) (string, error) {
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
					ch <- result{err: errors.New("answer okunamadi: girdi bitti")}
					return
				}
				ch <- result{err: fmt.Errorf("answer okunamadi: %w", err)}
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
