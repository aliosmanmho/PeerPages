package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/aliosmanmho/PeerPages/agent/internal/connect"
	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/proxy"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// ReadyMarker ajanin signaling'i dinlemeye basladigini bildirir.
//
// Makine de okur: e2e testi bu satiri bekleyip ziyaretci sayfasini aciyor
// (web/e2e/fixtures/agent.ts).
const ReadyMarker = "---PEERPAGES-READY---"

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

	// Faz 1'de tek hedef; URL ile hedef secimi Faz 3'te.
	targetName := target.DefaultName
	if !cfg.Targets.Has(targetName) {
		targetName = cfg.Targets.Names()[0]
	}

	newProxy := func() (tunnel.Proxy, error) {
		return proxy.New(proxy.Config{
			Targets:    cfg.Targets,
			TargetName: targetName,
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

	visitorURL, err := id.VisitorURL(cfg.AppURL, cfg.Secret)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, `
PeerPages ajani hazir.

  peer-id : %s
  hedef   : %s
  relay   : %d adet
  sir     : %s

Ziyaretcinin acmasi gereken adres:

  %s

Bu sekmeyi/terminali acik tut: tunel ajan calistigi surece ayakta.
%s
`,
		identity.ShortenNpub(npub),
		targetName,
		len(pool.URLs()),
		secretNote(cfg.Secret),
		visitorURL,
		ReadyMarker,
	)

	// Relay'ler dususe de akis devam etsin; Run yalnizca tum abonelikler
	// kapandiginda hata doner.
	runErr := acceptor.Run(ctx)
	if runErr != nil {
		return runErr
	}
	log.Info("ajan kapandi", slog.Int("acik_oturum", acceptor.SessionCount()))
	return nil
}

func secretNote(secret string) string {
	if secret == "" {
		return "yok (peer-id'yi bilen herkes baglanabilir)"
	}
	return fmt.Sprintf("var (%d karakter) — URL'de tasiniyor", len(secret))
}
