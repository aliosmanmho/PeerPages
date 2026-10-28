// Package wsbridge carries the visitor's WebSocket connections to the
// local target.
//
// Why a separate package: while internal/proxy sanitizes headers it
// strips `upgrade` and `connection` in both directions, so the WebSocket
// handshake CANNOT go through there. Here a real ws client is used
// (coder/websocket, already a dependency of the Nostr client).
//
// Security: the target URL comes ONLY from the internal/target allowlist;
// deriving a host from the ws path is impossible (BRIEF principle 1). The
// visitor is untrusted: the message size is bounded (BRIEF principle 4)
// and the allowlist validates the path.
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

// DefaultHandshakeTimeout bounds the handshake.
//
// The HANDSHAKE only: once the connection is up the session has no
// lifetime limit (a ws connection may stay open for hours, which is
// normal).
const DefaultHandshakeTimeout = 15 * time.Second

// Config is the bridge configuration.
type Config struct {
	// Targets are the allowed targets.
	Targets *target.Set
	// TargetName is this session's target.
	TargetName string
	// MaxMessage is the largest size of a message (applied on the read
	// side).
	MaxMessage int64
	// HandshakeTimeout bounds the handshake; DefaultHandshakeTimeout when
	// 0.
	HandshakeTimeout time.Duration
	// Logger is mandatory.
	Logger *slog.Logger
	// Verbose turns off path masking in logs.
	Verbose bool
}

// Dialer implements the tunnel.WSDialer interface.
type Dialer struct {
	cfg Config
}

// New builds the bridge.
func New(cfg Config) (*Dialer, error) {
	switch {
	case cfg.Targets == nil || cfg.Targets.Len() == 0:
		return nil, errors.New("wsbridge: the target list is empty")
	case cfg.TargetName == "":
		return nil, errors.New("wsbridge: the target name is empty")
	case !cfg.Targets.Has(cfg.TargetName):
		return nil, fmt.Errorf("wsbridge: the target %q is not in the allowlist", cfg.TargetName)
	case cfg.MaxMessage < 1:
		return nil, errors.New("wsbridge: MaxMessage must be at least 1")
	case cfg.Logger == nil:
		return nil, errors.New("wsbridge: Logger nil")
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = DefaultHandshakeTimeout
	}
	return &Dialer{cfg: cfg}, nil
}

// Dial turns a ws-open request into a local target.
func (d *Dialer) Dial(ctx context.Context, open protocol.WSOpen) (tunnel.WSConn, string, error) {
	// The target URL comes from the allowlist: the path is joined as a
	// path only.
	targetURL, err := d.cfg.Targets.Resolve(d.cfg.TargetName, open.Path)
	if err != nil {
		d.cfg.Logger.Warn("the ws target could not be resolved",
			slog.Uint64("id", uint64(open.ID)), slog.String("err", err.Error()))
		return nil, "", &tunnel.WSDialError{
			Code:   protocol.WSClosePolicy,
			Reason: "invalid ws path",
			Err:    err,
		}
	}
	wsURL := toWebSocketScheme(*targetURL)

	// A separate deadline for the handshake; the session itself is
	// unbounded.
	hsCtx, cancel := context.WithTimeout(ctx, d.cfg.HandshakeTimeout)
	defer cancel()

	conn, resp, err := websocket.Dial(hsCtx, wsURL.String(), &websocket.DialOptions{
		Subprotocols: open.Protos,
		// Origin: local servers (Vite, Socket.IO) check Origin and would
		// refuse the visitor's github.io origin. The tunnel behaves like a
		// local client; the decision and its reasoning: BRIEF section 8.
		HTTPHeader: http.Header{"Origin": {targetOrigin(targetURL)}},
	})
	if err != nil {
		code, reason := protocol.WSCloseInternal, "the local target was unreachable"
		if resp != nil && resp.StatusCode != 0 {
			// The local server refused the handshake (404, 403, 426...):
			// we report that as a policy close rather than 1011, which
			// makes diagnosis easier.
			code = protocol.WSClosePolicy
			reason = fmt.Sprintf("the local server refused the handshake (%d)", resp.StatusCode)
		}
		d.cfg.Logger.Warn("the ws handshake failed",
			slog.Uint64("id", uint64(open.ID)),
			logging.PathAttr(open.Path, d.cfg.Verbose),
			slog.String("err", err.Error()))
		return nil, "", &tunnel.WSDialError{Code: code, Reason: reason, Err: err}
	}

	// The read limit: the visitor is untrusted, and the local server
	// should not send unbounded messages either.
	conn.SetReadLimit(d.cfg.MaxMessage)

	d.cfg.Logger.Debug("ws connection established",
		slog.Uint64("id", uint64(open.ID)),
		logging.PathAttr(open.Path, d.cfg.Verbose),
		slog.String("proto", conn.Subprotocol()))

	return &wsConn{c: conn}, conn.Subprotocol(), nil
}

// toWebSocketScheme turns http(s) schemes into ws(s).
func toWebSocketScheme(u url.URL) url.URL {
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	return u
}

// targetOrigin builds the target's own origin (scheme + host).
func targetOrigin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

// wsConn implements the tunnel.WSConn interface on top of
// coder/websocket.
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
	// The close code range has to obey WebSocket's own rules: 1005/1006
	// are forbidden ON THE WIRE (they can travel in the tunnel, but
	// cannot be sent on a real connection).
	status := websocket.StatusCode(code)
	switch code {
	case 1005, 1006:
		status = websocket.StatusNormalClosure
		reason = ""
	}
	return w.c.Close(status, reason)
}

// mapReadError turns coder/websocket errors into bridge errors.
//
// The close code passing through EXACTLY matters: application codes
// (4000-4999) carry meaning in libraries such as Socket.IO.
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
