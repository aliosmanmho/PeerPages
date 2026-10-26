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

// ReadyMarker ajanin signaling'i dinlemeye basladigini bildirir.
//
// Makine de okur: e2e testi bu satiri bekleyip ziyaretci sayfasini aciyor
// (web/e2e/fixtures/agent.ts).
const ReadyMarker = "---PEERPAGES-READY---"

// relayConnectTimeout relay'lere onceden baglanmak icin taninan sure.
const relayConnectTimeout = 20 * time.Second

// visitorTargetLines her hedef icin "ad, yerel adres, ziyaretci URL'si"
// bloklarini uretir.
//
// Coklu hedefte tek bir URL yetmez: ziyaretcinin hangi projeyi acacagi
// URL'nin kendisinde yaziyor (`#npub1…/admin`).
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

// runNostr Nostr signaling ile calisir: ziyaretci yalnizca URL'yi acar.
func runNostr(ctx context.Context, cfg *ServeConfig, stdout io.Writer) error {
	log := cfg.Logger

	id, created, err := identity.LoadOrCreate(cfg.KeyPath)
	if err != nil {
		return fmt.Errorf("ajan kimligi okunamadi: %w", err)
	}
	npub, err := id.Npub()
	if err != nil {
		return err
	}
	if created {
		log.Info("ajan kimligi olusturuldu", slog.String("dosya", cfg.KeyPath))
	}

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: cfg.Relays, Logger: log})
	if err != nil {
		return err
	}
	defer pool.Close("ajan kapaniyor")

	var sealer *signaling.Sealer
	if cfg.Secret != "" {
		if sealer, err = signaling.NewSealer(cfg.Secret); err != nil {
			return err
		}
	}

	counters := stats.New(cfg.Targets.Names())

	// Hedef artik oturum basina: ziyaretci URL'de soyler, offer'da gelir.
	// Dogrulamasi connect katmaninda; buraya yalnizca gecerli adlar duser.
	newProxy := func(targetName string) (tunnel.Proxy, error) {
		return proxy.New(proxy.Config{
			Targets:    cfg.Targets,
			TargetName: targetName,
			Stats:      counters.Target(targetName),
			MaxBody:    cfg.MaxBody,
			Logger:     log,
			Verbose:    cfg.Verbose,
		})
	}

	acceptor, err := connect.New(connect.Config{
		Identity:      id,
		Pool:          pool,
		Sealer:        sealer,
		NewProxy:      newProxy,
		Targets:       cfg.Targets.Names(),
		STUN:          cfg.STUN,
		MaxSessions:   cfg.MaxSessions,
		MaxConcurrent: cfg.MaxConcurrent,
		MaxBody:       cfg.MaxBody,
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

	// SIRA ONEMLI: once relay'lere baglan, sonra abone ol, EN SON "hazir" de.
	// Ephemeral event'ler saklanmadigi icin, dinlemeye baslamadan gelen bir
	// offer geri donusu olmadan kaybolur. (Gercek relay'lerle yapilan e2e
	// kosumunda ilk baglanti tam bu yuzden basarisiz oluyordu.)
	connectCtx, cancelConnect := context.WithTimeout(ctx, relayConnectTimeout)
	defer cancelConnect()

	connReport, err := pool.Connect(connectCtx)
	if err != nil {
		return fmt.Errorf("relay'lere baglanilamadi: %w", err)
	}
	for relayURL, reason := range connReport.Failed {
		log.Warn("relay'e baglanilamadi",
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
PeerPages ajani hazir.

  peer-id : %s
  relay   : %d adet (bagli)
  sir     : %s

Yayinlanan hedefler ve ziyaretcinin acmasi gereken adresler:

%s
Bu sekmeyi/terminali acik tut: tunel ajan calistigi surece ayakta.
%s
`,
		identity.ShortenNpub(npub),
		len(connReport.OK),
		secretNote(cfg.Secret),
		targetLines,
		ReadyMarker,
	)

	// Sayac satirini ayri goroutine'de bas; ajan kapanirken dursun.
	statusCtx, stopStatus := context.WithCancel(ctx)
	defer stopStatus()
	go printStatusLines(statusCtx, stdout, acceptor, counters)

	// Relay'ler dususe de akis devam etsin; Run yalnizca tum abonelikler
	// kapandiginda hata doner.
	runErr := <-errCh
	stopStatus()

	total := counters.Total()
	log.Info("ajan kapandi",
		slog.Int("acik_oturum", acceptor.SessionCount()),
		slog.Uint64("istek", total.Requests),
		slog.Uint64("basari", total.Success),
		slog.Uint64("hata", total.Errors),
		slog.Uint64("gelen_bayt", total.BytesIn),
		slog.Uint64("giden_bayt", total.BytesOut))
	return runErr
}

// statusInterval canli durum satirinin tazelenme araligi.
const statusInterval = 5 * time.Second

// printStatusLines oturum ve istek sayaclarini stdout'a basar.
//
// Yalnizca DEGISTIGINDE basar: bos bir ajanin terminalini doldurmak
// istemiyoruz. Loglar stderr'e gittigi icin bu satirlarla karismaz, ve
// ReadyMarker'dan sonra geldigi icin makine okuyan e2e fixture'i etkilemez.
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
		line := fmt.Sprintf("[durum] oturum %d | istek %d (basari %d, hata %d) | gelen %s | giden %s",
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
		return "yok (peer-id'yi bilen herkes baglanabilir)"
	}
	return fmt.Sprintf("var (%d karakter) — URL'de tasiniyor", len(secret))
}
