// Package proxy reverse proxies the requests coming from the visitor to
// the local target.
//
// Security: the target URL comes ONLY from the internal/target allowlist;
// deriving a host from the request path is impossible (BRIEF principle
// 1). The visitor is treated as untrusted: headers are sanitized and the
// body size is bounded (BRIEF principle 4).
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/stats"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// chunkSize is the size of the chunk read from the target and put into a
// frame.
//
// Exactly 16 KiB: the same as protocol.MaxPayload, so every read
// corresponds to a single frame and there is no extra copying.
const chunkSize = protocol.MaxPayload

// DefaultTimeout is how long we wait for the response HEAD to arrive.
//
// The header phase only: there is NO time limit while the body streams.
// Until phase 6 this timeout wrapped the whole response and every stream
// living longer than 60 seconds (SSE, long polling) was cut off silently
// — it did not even look like an error, the visitor just saw "the stream
// ended". What bounds the body now is the visitor's `abort`, the
// session's idle check, or the upstream itself.
const DefaultTimeout = 60 * time.Second

// Config is the proxy configuration.
type Config struct {
	// Targets are the allowed targets.
	Targets *target.Set
	// TargetName is the target name this session uses; it comes from the
	// visitor's URL (protocol/signaling.md).
	TargetName string
	// Stats are this target's counters; it may be nil (no counting).
	Stats *stats.Counters
	// MaxBody is the largest request body accepted from the visitor.
	MaxBody int64
	// Timeout bounds the request to the target; DefaultTimeout when 0.
	Timeout time.Duration
	// Client is the HTTP client to use; a default is built when nil.
	Client *http.Client
	// Logger is mandatory.
	Logger *slog.Logger
	// Verbose turns off path masking in logs.
	Verbose bool
	// InjectWSShim set to true adds protocol.HeaderWSShim to HTML
	// responses (phase 6); the body is NOT CHANGED, the Service Worker
	// does the injection.
	InjectWSShim bool
}

// Proxy implements the tunnel.Proxy interface.
type Proxy struct {
	cfg    Config
	client *http.Client
}

// New builds the proxy.
func New(cfg Config) (*Proxy, error) {
	switch {
	case cfg.Targets == nil || cfg.Targets.Len() == 0:
		return nil, errors.New("proxy: the target list is empty")
	case cfg.TargetName == "":
		return nil, errors.New("proxy: the target name is empty")
	case !cfg.Targets.Has(cfg.TargetName):
		return nil, fmt.Errorf("proxy: the target %q is not in the allowlist", cfg.TargetName)
	case cfg.MaxBody < 1:
		return nil, errors.New("proxy: MaxBody must be at least 1")
	case cfg.Logger == nil:
		return nil, errors.New("proxy: Logger nil")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			// The agent does NOT FOLLOW redirects: a 3xx response goes to
			// the visitor and Location is rewritten to a relative path.
			// That way the redirect target resolves in the visitor's
			// browser, on our origin.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				// We manage compression ourselves (accept-encoding:
				// identity).
				DisableCompression:  true,
				MaxIdleConnsPerHost: 16,
				// The timeout covers the header phase ONLY: that way SSE
				// and long-polling streams are not cut off, while a target
				// that never answers still cannot hold a request forever.
				ResponseHeaderTimeout: cfg.Timeout,
				DialContext: (&net.Dialer{
					Timeout: cfg.Timeout,
				}).DialContext,
				TLSHandshakeTimeout: cfg.Timeout,
			},
		}
	}

	return &Proxy{cfg: cfg, client: client}, nil
}

// Serve carries a request to the target and streams the response into
// the tunnel.
func (p *Proxy) Serve(ctx context.Context, req protocol.Req, body io.Reader, sink tunnel.ResponseSink) {
	log := p.cfg.Logger.With(
		slog.Uint64("id", uint64(req.ID)),
		slog.String("method", req.Method),
		logging.PathAttr(req.Path, p.cfg.Verbose),
	)

	p.cfg.Stats.AddRequest()

	// The target URL comes from the allowlist: the path is joined as a
	// path only.
	targetURL, err := p.cfg.Targets.Resolve(p.cfg.TargetName, req.Path)
	if err != nil {
		log.Warn("the target could not be resolved", slog.String("err", err.Error()))
		p.cfg.Stats.AddError()
		p.respondError(sink, http.StatusBadRequest, "invalid request path")
		return
	}

	// CAREFUL: we do NOT put a deadline on the context here. The request
	// context is only cancelled when the session closes or the visitor
	// sends `abort`; the header phase is bounded in the Transport
	// (ResponseHeaderTimeout).
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var reqBody io.Reader
	if req.HasBody && body != nil {
		// The body limit: the tunnel layer counts too, this is the second
		// shield.
		reqBody = &countingReader{r: io.LimitReader(body, p.cfg.MaxBody), stats: p.cfg.Stats}
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL.String(), reqBody)
	if err != nil {
		log.Warn("the request could not be built", slog.String("err", err.Error()))
		p.cfg.Stats.AddError()
		p.respondError(sink, http.StatusBadRequest, "the request could not be built")
		return
	}
	httpReq.Header = SanitizeRequestHeaders(req.Headers)
	// Host is written from the target; the visitor's host does not leak to
	// the local server.
	httpReq.Host = targetURL.Host

	started := time.Now()
	resp, err := p.client.Do(httpReq)
	if err != nil {
		p.cfg.Stats.AddError()
		if ctx.Err() != nil {
			log.Warn("the request was cancelled or timed out", slog.String("err", err.Error()))
			_ = sink.Abort("upstream-canceled")
			return
		}
		log.Warn("the target was unreachable", slog.String("err", err.Error()))
		p.respondError(sink, http.StatusBadGateway, "the local target was unreachable")
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	headers := SanitizeResponseHeaders(resp.Header)
	// http://localhost:3000/... -> /...
	RewriteLocationHeaders(headers, targetURL)
	// The WebSocket shim hint: we do NOT touch the body, we only add a
	// header. The Service Worker does the injection (the decision and its
	// reasoning: BRIEF section 8).
	if p.cfg.InjectWSShim && isHTML(headers["content-type"]) {
		headers[protocol.HeaderWSShim] = "1"
	}

	if err := sink.SendRes(resp.StatusCode, headers); err != nil {
		log.Warn("res could not be sent", slog.String("err", err.Error()))
		p.cfg.Stats.AddError()
		return
	}

	written, copyErr := p.streamBody(ctx, resp.Body, sink)
	p.cfg.Stats.AddBytesOut(written)
	if copyErr != nil {
		log.Warn("the body could not be forwarded",
			slog.String("err", copyErr.Error()), slog.Int64("bytes", written))
		p.cfg.Stats.AddError()
		_ = sink.Abort("upstream-read-error")
		return
	}

	if err := sink.End(); err != nil {
		log.Warn("end could not be sent", slog.String("err", err.Error()))
		p.cfg.Stats.AddError()
		return
	}

	// The target answering 404/500 is NOT a failure: the proxy did its
	// job.
	p.cfg.Stats.AddSuccess()

	log.Debug("request completed",
		slog.Int("status", resp.StatusCode),
		slog.Int64("bytes", written),
		slog.Duration("duration", time.Since(started)))
}

// streamBody streams the response into the tunnel in 16 KiB chunks.
//
// The full body is never awaited (BRIEF phase 1): the first chunk goes out
// as soon as it arrives, so the ReadableStream on the Service Worker side
// starts flowing.
func (p *Proxy) streamBody(ctx context.Context, r io.Reader, sink tunnel.ResponseSink) (int64, error) {
	buf := make([]byte, chunkSize)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		n, readErr := r.Read(buf)
		if n > 0 {
			// sink.Write applies backpressure: if the data channel buffer
			// is full we wait here and no body piles up in memory.
			if err := sink.Write(buf[:n]); err != nil {
				return total, fmt.Errorf("could not write to the tunnel: %w", err)
			}
			total += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

// respondError answers the visitor with a short error.
//
// It sends a fixed text rather than the local target's error message, so
// the local server's internals do not leak to the visitor.
func (p *Proxy) respondError(sink tunnel.ResponseSink, status int, text string) {
	headers := map[string]string{
		"content-type":  "text/plain; charset=utf-8",
		"cache-control": "no-store",
	}
	if err := sink.SendRes(status, headers); err != nil {
		_ = sink.Abort("agent-error")
		return
	}
	if err := sink.Write([]byte("peerpages: " + text + "\n")); err != nil {
		_ = sink.Abort("agent-error")
		return
	}
	_ = sink.End()
}

// countingReader counts the body bytes coming from the visitor.
//
// The counting happens here rather than in the tunnel layer: the proxy is
// the only place that knows which target it belongs to, and it reads the
// body end to end anyway.
type countingReader struct {
	r     io.Reader
	stats *stats.Counters
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.stats.AddBytesIn(int64(n))
	return n, err
}
