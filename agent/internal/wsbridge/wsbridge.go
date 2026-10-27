// Package wsbridge ziyaretcinin WebSocket baglantilarini yerel hedefe tasir.
//
// Neden ayri paket: internal/proxy basliklari temizlerken `upgrade` ve
// `connection`'i iki yonde de siliyor, yani WebSocket el sikismasi oradan
// GECEMEZ. Burada gercek bir ws istemcisi (coder/websocket, zaten Nostr
// istemcisinin bagimliligi) kullaniliyor.
//
// Guvenlik: hedef URL'si YALNIZCA internal/target allowlist'inden gelir;
// ws yolundan host turetmek imkansiz (BRIEF ilke 1). Ziyaretci guvenilmez:
// mesaj boyutu sinirli (BRIEF ilke 4) ve yolu allowlist dogruluyor.
package wsbridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// DefaultHandshakeTimeout el sikisma icin ust sinir.
//
// Yalnizca EL SIKISMA icin: baglanti kurulduktan sonra oturumun bir omur
// siniri yok (bir ws baglantisi saatlerce acik kalabilir, bu normaldir).
const DefaultHandshakeTimeout = 15 * time.Second

// Config kopru yapilandirmasi.
type Config struct {
	// Targets izin verilen hedefler.
	Targets *target.Set
	// TargetName bu oturumun hedefi.
	TargetName string
	// MaxMessage bir mesajin en buyuk boyu (okuma yonunde uygulanir).
	MaxMessage int64
	// HandshakeTimeout el sikisma siniri; 0 ise DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration
	// Logger zorunlu.
	Logger *slog.Logger
	// Verbose loglarda path maskelemesini kapatir.
	Verbose bool
}

// Dialer tunnel.WSDialer arayuzunu uygular.
type Dialer struct {
	cfg Config
}

// New kopruyu kurar.
func New(cfg Config) (*Dialer, error) {
	switch {
	case cfg.Targets == nil || cfg.Targets.Len() == 0:
		return nil, errors.New("wsbridge: hedef listesi bos")
	case cfg.TargetName == "":
		return nil, errors.New("wsbridge: hedef adi bos")
	case !cfg.Targets.Has(cfg.TargetName):
		return nil, fmt.Errorf("wsbridge: %q hedefi allowlist'te yok", cfg.TargetName)
	case cfg.MaxMessage < 1:
		return nil, errors.New("wsbridge: MaxMessage en az 1 olmali")
	case cfg.Logger == nil:
		return nil, errors.New("wsbridge: Logger nil")
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = DefaultHandshakeTimeout
	}
	return &Dialer{cfg: cfg}, nil
}

// Dial ws-open istegini yerel hedefe cevirir.
func (d *Dialer) Dial(ctx context.Context, open protocol.WSOpen) (tunnel.WSConn, string, error) {
	// Hedef URL'si allowlist'ten: path yalnizca yol olarak birlestirilir.
	targetURL, err := d.cfg.Targets.Resolve(d.cfg.TargetName, open.Path)
	if err != nil {
		d.cfg.Logger.Warn("ws hedefi cozulemedi",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		return nil, "", &tunnel.WSDialError{
			Code:   protocol.WSClosePolicy,
			Reason: "gecersiz ws yolu",
			Err:    err,
		}
	}
	wsURL := toWebSocketScheme(*targetURL)

	// El sikisma icin ayri sure; oturumun kendisi sinirsiz.
	hsCtx, cancel := context.WithTimeout(ctx, d.cfg.HandshakeTimeout)
	defer cancel()

	conn, resp, err := websocket.Dial(hsCtx, wsURL.String(), &websocket.DialOptions{
		Subprotocols: open.Protos,
		// Origin: yerel sunucular (Vite, Socket.IO) Origin denetimi yapar ve
		// ziyaretcinin github.io origin'ini reddederdi. Tunel yerel bir
		// istemci gibi davraniyor; karar ve gerekcesi BRIEF §8.
		HTTPHeader: http.Header{"Origin": {targetOrigin(targetURL)}},
	})
	if err != nil {
		code, reason := protocol.WSCloseInternal, "yerel hedefe baglanilamadi"
		if resp != nil && resp.StatusCode != 0 {
			// Yerel sunucu el sikismayi reddetti (404, 403, 426...): bunu
			// 1011 degil politika olarak bildiriyoruz, teshisi kolaylastirir.
			code = protocol.WSClosePolicy
			reason = fmt.Sprintf("yerel sunucu el sikismayi reddetti (%d)", resp.StatusCode)
		}
		d.cfg.Logger.Warn("ws el sikismasi basarisiz",
			slog.Uint64("id", uint64(open.ID)),
			logging.PathAttr(open.Path, d.cfg.Verbose),
			slog.String("err", err.Error()))
		return nil, "", &tunnel.WSDialError{Code: code, Reason: reason, Err: err}
	}

	// Okuma siniri: ziyaretci guvenilmez, yerel sunucu da sinirsiz mesaj
	// yollamamali.
	conn.SetReadLimit(d.cfg.MaxMessage)

	d.cfg.Logger.Debug("ws baglantisi kuruldu",
		slog.Uint64("id", uint64(open.ID)),
		logging.PathAttr(open.Path, d.cfg.Verbose),
		slog.String("proto", conn.Subprotocol()))

	return &wsConn{c: conn}, conn.Subprotocol(), nil
}

// toWebSocketScheme http(s) semalarini ws(s)'ye cevirir.
func toWebSocketScheme(u url.URL) url.URL {
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	return u
}

// targetOrigin hedefin kendi origin'ini uretir (sema + host).
func targetOrigin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// wsConn tunnel.WSConn arayuzunu coder/websocket uzerine kurar.
type wsConn struct {
	c *websocket.Conn
}

func (w *wsConn) Read(ctx context.Context) (bool, []byte, error) {
	typ, data, err := w.c.Read(ctx)
	if err != nil {
		return false, nil, mapReadError(err)
	}
	return typ == websocket.MessageBinary, data, nil
}

func (w *wsConn) Write(ctx context.Context, binary bool, data []byte) error {
	typ := websocket.MessageText
	if binary {
		typ = websocket.MessageBinary
	}
	return w.c.Write(ctx, typ, data)
}

func (w *wsConn) Close(code int, reason string) error {
	// Kapanis kodu araligi WebSocket'in kendi kurallarina uymak zorunda:
	// 1005/1006 TELDE yasaklidir (tunelde tasinabilir, ama gercek bir
	// baglantida yollanamaz).
	status := websocket.StatusCode(code)
	switch code {
	case 1005, 1006:
		status = websocket.StatusNormalClosure
		reason = ""
	}
	return w.c.Close(status, reason)
}

// mapReadError coder/websocket hatalarini kopru hatalarina cevirir.
//
// Kapanis kodunun AYNEN gecmesi onemli: uygulamaya ait kodlar (4000-4999)
// Socket.IO gibi kutuphanelerde anlam tasiyor.
func mapReadError(err error) error {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		if ce.Code == websocket.StatusNormalClosure {
			return tunnel.ErrWSClosedNormally
		}
		return &tunnel.WSCloseError{Code: int(ce.Code), Reason: ce.Reason}
	}
	return err
}
