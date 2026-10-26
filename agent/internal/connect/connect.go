// Package connect ajanin Nostr uzerinden ziyaretci kabul eden akisini yurutur.
//
// Gorev dagilimi:
//   - internal/nostr      : relay tasimasi (yayinla / abone ol)
//   - internal/signaling  : mesaj bicimi ve sir katmani
//   - internal/tunnel     : WebRTC oturumu
//   - BU PAKET            : ikisini birlestiren akis ve ziyaretci kurallari
//
// Akis (protocol/signaling.md): ziyaretci offer yayinlar, ajan answer doner,
// iki taraf candidate alisverisi yapar, WebRTC kurulunca signaling biter.
package connect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/pion/webrtc/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/identity"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/signaling"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// Zamanlamalar.
const (
	// SessionSetupTimeout offer geldikten sonra tunelin acilmasi icin
	// taninan sure (README Faz 2: 30 sn).
	SessionSetupTimeout = 30 * time.Second
	// PublishTimeout tek bir signaling mesajinin yayini icin.
	PublishTimeout = 15 * time.Second
	// SubscribeBacklog abonelikte ne kadar geriye bakilacagi. Kisa tutuluyor:
	// ephemeral event'ler saklanmaz, uzun gecmise bakmanin anlami yok.
	SubscribeBacklog = time.Minute
)

// ProxyFactory verilen hedef icin bir Proxy uretir.
//
// Hedef oturum basina degisir: ziyaretci URL'de soyler
// (`#npub1…/admin`), offer'da tasinir (protocol/signaling.md). targetName
// her zaman Config.Targets icindeki adlardan biridir — dogrulama cagiran
// tarafta yapilir.
type ProxyFactory func(targetName string) (tunnel.Proxy, error)

// Config kabul akisinin yapilandirmasi.
type Config struct {
	// Identity ajanin Nostr kimligi.
	Identity identity.Identity
	// Pool relay havuzu (arayuz: testler sahte havuz verebilsin).
	Pool Pool
	// Sealer paylasilan sir katmani; nil ise sir yok.
	Sealer *signaling.Sealer
	// NewProxy her oturum icin proxy uretir.
	NewProxy ProxyFactory
	// Targets yayinlanan hedeflerin adlari: hem hello mesajinda bildirilir
	// hem de offer'daki hedef adi buna karsi dogrulanir.
	Targets []string
	// STUN ICE sunuculari.
	STUN []string
	// MaxSessions ayni anda kac ziyaretci kabul edilir.
	MaxSessions int
	// MaxConcurrent oturum basina eszamanli istek tavani.
	MaxConcurrent int
	// MaxBody istek govdesi tavani.
	MaxBody int64
	// Logger zorunlu.
	Logger *slog.Logger
	// Verbose loglarda path maskelemesini kapatir.
	Verbose bool

	// now test icin saat enjeksiyonu; nil ise time.Now.
	now func() time.Time
}

// Pool internal/nostr.Pool'un bu paketin ihtiyac duydugu yuzu.
//
// Arayuz olarak aliyoruz ki testler sahte bir havuz verebilsin.
type Pool interface {
	Publish(ctx context.Context, ev gonostr.Event) (nostr.PublishReport, error)
	Subscribe(ctx context.Context, filter gonostr.Filter) <-chan gonostr.RelayEvent
}

func (c *Config) validate() error {
	switch {
	case c.Identity.SecretKey == "" || c.Identity.PublicKey == "":
		return errors.New("connect: kimlik eksik")
	case c.Pool == nil:
		return errors.New("connect: Pool nil")
	case c.NewProxy == nil:
		return errors.New("connect: NewProxy nil")
	case len(c.Targets) == 0:
		return errors.New("connect: hedef listesi bos")
	case c.MaxSessions < 1:
		return errors.New("connect: MaxSessions en az 1 olmali")
	case c.MaxConcurrent < 1:
		return errors.New("connect: MaxConcurrent en az 1 olmali")
	case c.MaxBody < 1:
		return errors.New("connect: MaxBody en az 1 olmali")
	case c.Logger == nil:
		return errors.New("connect: Logger nil")
	}
	return nil
}

// Acceptor gelen offer'lari tunel oturumlarina cevirir.
type Acceptor struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	seen  *seenSet
	rate  *rateLimiter
	codec codec

	mu       sync.Mutex
	sessions map[string]*visitorSession // sid -> oturum

	// ready abonelik kurulduktan sonra kapanir. Cagiran taraf "hazirim"
	// demeden once bunu beklemeli: ephemeral event'ler saklanmadigi icin
	// dinlemeye baslamadan gelen offer kaybolur.
	ready   chan struct{}
	readyOn sync.Once
}

// codec sirli/sirsiz kodlamayi tek arayuze indirir.
type codec interface {
	Encode(m signaling.Msg) (string, error)
	Decode(payload string) (signaling.Msg, error)
}

// visitorSession tek bir ziyaretcinin oturumu.
type visitorSession struct {
	sid     string
	peerPub string
	sess    *tunnel.Session
	cancel  context.CancelFunc
	created time.Time
}

// New kabul akisini kurar.
func New(cfg Config) (*Acceptor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}

	return &Acceptor{
		cfg:  cfg,
		log:  cfg.Logger,
		now:  now,
		seen: newSeenSet(SeenTTL, now),
		rate: newRateLimiter(RateWindow, RateBurst, now),
		// nil *Sealer de gecerli: Seal/Open nil alici ile duz JSON yolunu
		// uyguluyor (secret.go). Arayuzun kendisi nil olmadigi icin
		// cagrilar guvenle gidiyor.
		codec:    cfg.Sealer,
		sessions: map[string]*visitorSession{},
		ready:    make(chan struct{}),
	}, nil
}

// Run abone olur ve ctx bitene kadar gelen event'leri isler.
func (a *Acceptor) Run(ctx context.Context) error {
	npub, err := a.cfg.Identity.Npub()
	if err != nil {
		return err
	}

	filter := nostr.SignalingFilter(a.cfg.Identity.PublicKey, a.now().Add(-SubscribeBacklog))
	events := a.cfg.Pool.Subscribe(ctx, filter)

	// Abonelik kuruldu: artik gelen offer'lari yakalayabiliriz.
	a.readyOn.Do(func() { close(a.ready) })

	a.log.Info("signaling dinleniyor",
		slog.String("peer_id", identity.ShortenNpub(npub)),
		slog.Bool("sir_gerekli", a.cfg.Sealer != nil),
	)

	for {
		select {
		case <-ctx.Done():
			a.closeAll()
			return nil
		case ev, ok := <-events:
			if !ok {
				a.closeAll()
				return errors.New("relay abonelikleri kapandi")
			}
			a.onEvent(ctx, ev.Event)
		}
	}
}

// Ready abonelik kuruldugunda kapanan kanali dondurur.
//
// "Hazirim" mesajini basmadan once bunu bekle: aksi halde ziyaretci URL'yi
// hemen acarsa offer'i kaybolur (ephemeral event'ler saklanmaz).
func (a *Acceptor) Ready() <-chan struct{} { return a.ready }

// SessionCount acik oturum sayisi.
func (a *Acceptor) SessionCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

// onEvent gelen bir signaling event'ini isler.
//
// Ziyaretci guvenilmez: sirayla replay, saat kaymasi, hiz limiti ve icerik
// dogrulamasi (guard.go, protocol/signaling.md).
func (a *Acceptor) onEvent(ctx context.Context, ev *gonostr.Event) {
	if ev == nil {
		return
	}
	short := nostr.ShortPubKey(ev.PubKey)

	// Kendi yayinimiz geri gelirse yok say.
	if ev.PubKey == a.cfg.Identity.PublicKey {
		return
	}

	// Replay: ayni event id'si iki kez islenmez.
	if a.seen.seen(ev.ID) {
		a.log.Debug("tekrarlanan event atlandi", slog.String("peer", short))
		return
	}

	// Saat kaymasi: cok eski ya da gelecekteki event'ler reddedilir.
	if !freshEnough(ev.CreatedAt.Time(), a.now(), MaxClockSkew) {
		a.log.Warn("zaman damgasi kabul araligi disinda",
			slog.String("peer", short),
			slog.Time("created_at", ev.CreatedAt.Time()))
		return
	}

	// Hiz limiti: pubkey basina pencere hakki.
	if !a.rate.allow(ev.PubKey) {
		a.log.Warn("hiz limiti asildi, event atlandi", slog.String("peer", short))
		return
	}

	payload, err := nostr.Decrypt(a.cfg.Identity.SecretKey, ev.PubKey, ev.Content)
	if err != nil {
		a.log.Warn("NIP-44 cozulemedi",
			slog.String("peer", short), slog.String("err", err.Error()))
		return
	}

	msg, err := a.codec.Decode(payload)
	if err != nil {
		// Yanlis sirla gelen mesajlar burada duser; peer-id'yi bilip sirri
		// bilmeyen biri baglanamaz.
		a.log.Warn("signaling mesaji cozulemedi",
			slog.String("peer", short), slog.String("err", err.Error()))
		return
	}

	switch msg.Type {
	case signaling.TypeOffer:
		a.onOffer(ctx, ev.PubKey, msg)
	case signaling.TypeCandidate:
		a.onCandidate(ev.PubKey, msg)
	case signaling.TypeBye:
		a.onBye(ev.PubKey, msg)
	case signaling.TypeAnswer:
		// Ajan answer ALMAZ, uretir: protokol ihlali.
		a.log.Warn("ajana answer geldi, protokol ihlali", slog.String("peer", short))
	}
}

func (a *Acceptor) onOffer(ctx context.Context, peerPub string, msg signaling.Msg) {
	short := nostr.ShortPubKey(peerPub)

	// Hedef once: en ucuz kontrol ve basarisizsa WebRTC'ye hic girmiyoruz.
	// Sessizce default'a dusmuyoruz — ziyaretci yanlis projeyi gezdigini
	// fark etmeyebilir (README bolum 8).
	targetName, ok := a.resolveTarget(msg.Target)
	if !ok {
		a.log.Warn("bilinmeyen hedef, offer reddedildi",
			slog.String("peer", short), slog.String("hedef", msg.Target))
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "unknown-target",
		})
		return
	}

	a.mu.Lock()
	if _, dup := a.sessions[msg.SID]; dup {
		a.mu.Unlock()
		a.log.Debug("ayni sid ile ikinci offer atlandi",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	if len(a.sessions) >= a.cfg.MaxSessions {
		a.mu.Unlock()
		a.log.Warn("oturum tavani asildi, offer reddedildi",
			slog.String("peer", short), slog.Int("tavan", a.cfg.MaxSessions))
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "too-many-sessions",
		})
		return
	}
	// Yer ayir: kilidi birakmadan once kaydi koyuyoruz ki ayni anda gelen
	// ikinci offer tavani asamasin.
	placeholder := &visitorSession{sid: msg.SID, peerPub: peerPub, created: a.now()}
	a.sessions[msg.SID] = placeholder
	a.mu.Unlock()

	a.log.Info("offer alindi",
		slog.String("peer", short), slog.String("sid", msg.SID),
		slog.String("hedef", targetName))

	if err := a.startSession(ctx, peerPub, msg, targetName, placeholder); err != nil {
		a.log.Warn("oturum kurulamadi",
			slog.String("peer", short), slog.String("err", err.Error()))
		a.dropSession(msg.SID)
		a.publish(ctx, peerPub, signaling.Msg{
			Type: signaling.TypeBye, SID: msg.SID, Reason: "setup-failed",
		})
	}
}

// resolveTarget offer'daki hedef adini yayinlanan hedeflerden birine cevirir.
//
// Bos ad "belirtilmedi" demektir: default varsa o, yoksa alfabetik ilk hedef
// (Targets, target.Set.Names() ciktisidir ve siralidir).
func (a *Acceptor) resolveTarget(name string) (string, bool) {
	if name == "" {
		for _, n := range a.cfg.Targets {
			if n == target.DefaultName {
				return n, true
			}
		}
		return a.cfg.Targets[0], true
	}
	for _, n := range a.cfg.Targets {
		if n == name {
			return n, true
		}
	}
	return "", false
}

func (a *Acceptor) startSession(
	ctx context.Context, peerPub string, offerMsg signaling.Msg,
	targetName string, slot *visitorSession,
) error {
	px, err := a.cfg.NewProxy(targetName)
	if err != nil {
		return fmt.Errorf("proxy kurulamadi: %w", err)
	}

	sess, err := tunnel.New(tunnel.Config{
		STUN:          a.cfg.STUN,
		Targets:       a.cfg.Targets,
		Auth:          a.authMode(),
		MaxConcurrent: a.cfg.MaxConcurrent,
		MaxBody:       a.cfg.MaxBody,
		Proxy:         px,
		Logger:        a.log.With(slog.String("sid", offerMsg.SID)),
		Verbose:       a.cfg.Verbose,
		// Nostr yolunda trickle acik: adaylar ayri event olarak gider
		// (README Faz 2).
		Trickle: true,
	})
	if err != nil {
		return err
	}

	sessCtx, cancel := context.WithCancel(ctx)

	a.mu.Lock()
	slot.sess = sess
	slot.cancel = cancel
	a.mu.Unlock()

	// Yerel adaylari ziyaretciye yolla.
	sess.OnLocalCandidate(func(c webrtc.ICECandidateInit) {
		mid := ""
		if c.SDPMid != nil {
			mid = *c.SDPMid
		}
		if mid == "" {
			// Yalnizca bir m-line var (data channel); mid'i bos bırakmak
			// yerine tanimli degeri kullaniyoruz.
			mid = "0"
		}
		a.publish(sessCtx, peerPub, signaling.Msg{
			Type:      signaling.TypeCandidate,
			SID:       offerMsg.SID,
			Candidate: c.Candidate,
			Mid:       mid,
		})
	})

	answerCtx, answerCancel := context.WithTimeout(sessCtx, SessionSetupTimeout)
	defer answerCancel()

	answer, err := sess.Answer(answerCtx, webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerMsg.SDP,
	})
	if err != nil {
		cancel()
		_ = sess.Close()
		return fmt.Errorf("answer uretilemedi: %w", err)
	}

	if err := a.publish(sessCtx, peerPub, signaling.Msg{
		Type: signaling.TypeAnswer, SID: offerMsg.SID, SDP: answer.SDP,
	}); err != nil {
		cancel()
		_ = sess.Close()
		return fmt.Errorf("answer yayinlanamadi: %w", err)
	}

	// Oturumun acilmasini bekle ve kapaninca temizle.
	go a.watchSession(sessCtx, peerPub, offerMsg.SID, sess)
	return nil
}

func (a *Acceptor) watchSession(ctx context.Context, peerPub, sid string, sess *tunnel.Session) {
	short := nostr.ShortPubKey(peerPub)

	if err := sess.WaitReady(ctx, SessionSetupTimeout); err != nil {
		a.log.Warn("tunel acilmadi, oturum kapatiliyor",
			slog.String("peer", short), slog.String("sid", sid),
			slog.String("err", err.Error()))
		a.dropSession(sid)
		return
	}

	a.log.Info("tunel acik", slog.String("peer", short), slog.String("sid", sid))

	select {
	case <-sess.Done():
		a.log.Info("tunel kapandi", slog.String("peer", short), slog.String("sid", sid))
	case <-ctx.Done():
	}
	a.dropSession(sid)
}

func (a *Acceptor) onCandidate(peerPub string, msg signaling.Msg) {
	short := nostr.ShortPubKey(peerPub)

	a.mu.Lock()
	vs, ok := a.sessions[msg.SID]
	a.mu.Unlock()

	// Bilinmeyen sid: durum kirletmeye izin vermiyoruz.
	if !ok {
		a.log.Debug("bilinmeyen sid icin candidate atlandi",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	// Oturumu baska bir ziyaretci kapamasin/kirletmesin.
	if vs.peerPub != peerPub {
		a.log.Warn("baska bir pubkey bu oturuma candidate yolladi",
			slog.String("peer", short), slog.String("sid", msg.SID))
		return
	}
	if vs.sess == nil {
		a.log.Debug("oturum henuz hazir degil, candidate atlandi",
			slog.String("sid", msg.SID))
		return
	}

	mid := msg.Mid
	if err := vs.sess.AddRemoteCandidate(webrtc.ICECandidateInit{
		Candidate: msg.Candidate,
		SDPMid:    &mid,
	}); err != nil {
		a.log.Warn("aday eklenemedi",
			slog.String("sid", msg.SID), slog.String("err", err.Error()))
	}
}

func (a *Acceptor) onBye(peerPub string, msg signaling.Msg) {
	a.mu.Lock()
	vs, ok := a.sessions[msg.SID]
	a.mu.Unlock()
	if !ok || vs.peerPub != peerPub {
		return
	}

	a.log.Info("ziyaretci ayrildi",
		slog.String("peer", nostr.ShortPubKey(peerPub)),
		slog.String("sid", msg.SID),
		slog.String("reason", msg.Reason))
	a.dropSession(msg.SID)
}

func (a *Acceptor) dropSession(sid string) {
	a.mu.Lock()
	vs, ok := a.sessions[sid]
	if ok {
		delete(a.sessions, sid)
	}
	a.mu.Unlock()

	if !ok {
		return
	}
	if vs.cancel != nil {
		vs.cancel()
	}
	if vs.sess != nil {
		_ = vs.sess.Close()
	}
}

func (a *Acceptor) closeAll() {
	a.mu.Lock()
	all := make([]*visitorSession, 0, len(a.sessions))
	for _, vs := range a.sessions {
		all = append(all, vs)
	}
	a.sessions = map[string]*visitorSession{}
	a.mu.Unlock()

	for _, vs := range all {
		if vs.cancel != nil {
			vs.cancel()
		}
		if vs.sess != nil {
			_ = vs.sess.Close()
		}
	}
}

// publish bir signaling mesajini ziyaretciye yollar.
func (a *Acceptor) publish(ctx context.Context, peerPub string, msg signaling.Msg) error {
	payload, err := a.codec.Encode(msg)
	if err != nil {
		return fmt.Errorf("mesaj kodlanamadi: %w", err)
	}

	// NIP-44: icerik yalnizca aliciya acik; relay'ler goremez.
	content, err := nostr.Encrypt(a.cfg.Identity.SecretKey, peerPub, payload)
	if err != nil {
		return fmt.Errorf("NIP-44 sifreleme: %w", err)
	}

	ev := nostr.NewSignalingEvent(peerPub, content)
	if err := ev.Sign(a.cfg.Identity.SecretKey); err != nil {
		return fmt.Errorf("event imzalanamadi: %w", err)
	}

	pubCtx, cancel := context.WithTimeout(ctx, PublishTimeout)
	defer cancel()

	report, err := a.cfg.Pool.Publish(pubCtx, ev)
	if err != nil {
		return fmt.Errorf("yayin: %w", err)
	}
	a.log.Debug("signaling mesaji yayinlandi",
		slog.String("t", string(msg.Type)),
		slog.String("sid", msg.SID),
		slog.Int("relay_ok", len(report.OK)))
	return nil
}

// authMode hello mesajinda bildirilecek auth degeri.
//
// Sir varsa "required": ziyaretci tarafi bunu gorup kullaniciya anlamli bir
// mesaj gosterebilir.
func (a *Acceptor) authMode() protocol.AuthMode {
	if a.cfg.Sealer != nil {
		return protocol.AuthRequired
	}
	return protocol.AuthNone
}
