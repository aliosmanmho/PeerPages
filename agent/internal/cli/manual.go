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

// Elle signaling akisinda stdout'a basilan isaretciler.
//
// Hem insan hem makine okur: Playwright e2e testi answer'i bu isaretciler
// arasindan alir, offer'i stdin'e yazar ve TunnelOpenMarker'i bekler
// (web/e2e/fixtures/agent.ts).
//
// YON: Faz 2'de rol degisti. Artik SAYFA offer uretiyor, ajan answer doniyor
// (protocol/signaling.md). Yani kopyalama yonu Faz 1'in tersi.
const (
	OfferPrompt      = "---PEERPAGES-OFFER-BEKLENIYOR---"
	AnswerBeginMark  = "---PEERPAGES-ANSWER-BEGIN---"
	AnswerEndMark    = "---PEERPAGES-ANSWER-END---"
	TunnelOpenMarker = "---PEERPAGES-TUNNEL-OPEN---"
	TunnelClosedMark = "---PEERPAGES-TUNNEL-CLOSED---"

	channelOpenWait   = 60 * time.Second
	iceGatheringLimit = 45 * time.Second
)

// runManual elle signaling ile tek bir tunel oturumu calistirir.
//
// Akis: ziyaretci sayfasi offer uretir -> kullanici ajana yapistirir -> ajan
// answer basar -> kullanici sayfaya yapistirir -> tunel acilir. Signaling
// sunucusu ve Nostr YOK.
func runManual(ctx context.Context, cfg *ServeConfig, stdin io.Reader, stdout io.Writer) error {
	log := cfg.Logger

	// Elle yolda ziyaretcinin URL'si yok, hedefi bayrak soyler
	// (--manual-target); dogrulamasi serve.go'da yapildi.
	targetName := cfg.ManualTarget

	counters := stats.New(cfg.Targets.Names())

	px, err := proxy.New(proxy.Config{
		Targets:    cfg.Targets,
		TargetName: targetName,
		Stats:      counters.Target(targetName),
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
		// Elle signaling: adaylar tam SDP'nin icinde gitsin, tek parca
		// kopyalanabilsin. Trickle Nostr yolunda aciliyor.
		Trickle: false,
	})
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil {
			log.Debug("oturum kapatilirken", slog.String("err", cerr.Error()))
		}
	}()

	fmt.Fprintf(stdout, `
Ziyaretci sayfasini ac (baglanti ekrani), "Offer uret" butonuna bas ve
uretilen offer'i asagiya yapistirip Enter'a bas.

%s
`, OfferPrompt)

	offerLine, err := readPastedLine(ctx, stdin)
	if err != nil {
		return err
	}
	offer, err := signal.Decode(offerLine)
	if err != nil {
		return fmt.Errorf("offer okunamadi: %w", err)
	}

	log.Info("ICE adaylari toplaniyor", slog.Any("stun", cfg.STUN))
	answerCtx, cancelAnswer := context.WithTimeout(ctx, iceGatheringLimit)
	defer cancelAnswer()

	answer, err := sess.Answer(answerCtx, offer)
	if err != nil {
		return fmt.Errorf("answer uretilemedi: %w", err)
	}
	encoded, err := signal.Encode(answer)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\n%s\n%s\n%s\n", AnswerBeginMark, encoded, AnswerEndMark)
	fmt.Fprint(stdout, "\nBu answer'i ziyaretci sayfasindaki answer alanina yapistir.\n")

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

// readPastedLine stdin'den tek satirlik yapistirilmis metni okur.
//
// Okuma ayri bir goroutine'de yapilir; aksi halde Ctrl+C bir satir
// gelmedikce islemez (stdin okumasi iptal edilemez).
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
					ch <- result{err: errors.New("girdi okunamadi: girdi bitti")}
					return
				}
				ch <- result{err: fmt.Errorf("girdi okunamadi: %w", err)}
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
