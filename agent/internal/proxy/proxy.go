// Package proxy ziyaretciden gelen istekleri yerel hedefe reverse proxy yapar.
//
// Guvenlik: hedef URL'si YALNIZCA internal/target allowlist'inden gelir;
// istek yolundan host turetmek imkansiz (README ilke 1). Ziyaretci
// guvenilmez kabul edilir: basliklar temizlenir, govde boyutu sinirlidir
// (README ilke 4).
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aliosmanmho/PeerPages/agent/internal/logging"
	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
	"github.com/aliosmanmho/PeerPages/agent/internal/target"
	"github.com/aliosmanmho/PeerPages/agent/internal/tunnel"
)

// chunkSize hedeften okunup cerceveye konan parca boyutu.
//
// Tam 16 KiB: protocol.MaxPayload ile ayni, boylece her okuma tek bir
// cerceveye karsilik gelir ve ek kopyalama olmaz.
const chunkSize = protocol.MaxPayload

// DefaultTimeout bir isteğin hedefte en fazla bekleyecegi sure.
const DefaultTimeout = 60 * time.Second

// Config proxy yapilandirmasi.
type Config struct {
	// Targets izin verilen hedefler.
	Targets *target.Set
	// TargetName bu oturumun kullandigi hedef adi (Faz 3'te URL'den gelecek).
	TargetName string
	// MaxBody ziyaretciden kabul edilecek en buyuk istek govdesi.
	MaxBody int64
	// Timeout hedef istegi icin ust sinir; 0 ise DefaultTimeout.
	Timeout time.Duration
	// Client kullanilacak HTTP istemcisi; nil ise varsayilan kurulur.
	Client *http.Client
	// Logger zorunlu.
	Logger *slog.Logger
	// Verbose loglarda path maskelemesini kapatir.
	Verbose bool
}

// Proxy tunnel.Proxy arayuzunu uygular.
type Proxy struct {
	cfg    Config
	client *http.Client
}

// New proxy kurar.
func New(cfg Config) (*Proxy, error) {
	switch {
	case cfg.Targets == nil || cfg.Targets.Len() == 0:
		return nil, errors.New("proxy: hedef listesi bos")
	case cfg.TargetName == "":
		return nil, errors.New("proxy: hedef adi bos")
	case !cfg.Targets.Has(cfg.TargetName):
		return nil, fmt.Errorf("proxy: %q hedefi allowlist'te yok", cfg.TargetName)
	case cfg.MaxBody < 1:
		return nil, errors.New("proxy: MaxBody en az 1 olmali")
	case cfg.Logger == nil:
		return nil, errors.New("proxy: Logger nil")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			// Yonlendirmeleri ajan IZLEMEZ: 3xx cevabi ziyaretciye gider ve
			// Location goreli yola cevrilir. Boylece yonlendirme hedefi
			// ziyaretcinin tarayicisinda, bizim origin'imizde cozulur.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				// Sikistirmayi kendimiz yonetiyoruz (accept-encoding: identity).
				DisableCompression:  true,
				MaxIdleConnsPerHost: 16,
			},
		}
	}

	return &Proxy{cfg: cfg, client: client}, nil
}

// Serve bir istegi hedefe tasir ve cevabi akis halinde tunele yazar.
func (p *Proxy) Serve(ctx context.Context, req protocol.Req, body io.Reader, sink tunnel.ResponseSink) {
	log := p.cfg.Logger.With(
		slog.Uint64("id", uint64(req.ID)),
		slog.String("method", req.Method),
		logging.PathAttr(req.Path, p.cfg.Verbose),
	)

	// Hedef URL'si allowlist'ten: path yalnizca yol olarak birlestirilir.
	targetURL, err := p.cfg.Targets.Resolve(p.cfg.TargetName, req.Path)
	if err != nil {
		log.Warn("hedef cozulemedi", slog.String("err", err.Error()))
		p.respondError(sink, http.StatusBadRequest, "gecersiz istek yolu")
		return
	}

	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	var reqBody io.Reader
	if req.HasBody && body != nil {
		// Govde tavani: tunnel katmani da sayiyor, burada ikinci kalkan.
		reqBody = io.LimitReader(body, p.cfg.MaxBody)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL.String(), reqBody)
	if err != nil {
		log.Warn("istek kurulamadi", slog.String("err", err.Error()))
		p.respondError(sink, http.StatusBadRequest, "istek kurulamadi")
		return
	}
	httpReq.Header = SanitizeRequestHeaders(req.Headers)
	// Host hedeften yazilir; ziyaretcinin host'u yerel sunucuya sizmaz.
	httpReq.Host = targetURL.Host

	started := time.Now()
	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			log.Warn("istek iptal edildi ya da zaman asti", slog.String("err", err.Error()))
			_ = sink.Abort("upstream-canceled")
			return
		}
		log.Warn("hedefe ulasilamadi", slog.String("err", err.Error()))
		p.respondError(sink, http.StatusBadGateway, "yerel hedefe ulasilamadi")
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}()

	headers := SanitizeResponseHeaders(resp.Header)
	// http://localhost:3000/... -> /...
	RewriteLocationHeaders(headers, targetURL)

	if err := sink.SendRes(resp.StatusCode, headers); err != nil {
		log.Warn("res yollanamadi", slog.String("err", err.Error()))
		return
	}

	written, copyErr := p.streamBody(ctx, resp.Body, sink)
	if copyErr != nil {
		log.Warn("govde aktarilamadi",
			slog.String("err", copyErr.Error()), slog.Int64("bayt", written))
		_ = sink.Abort("upstream-read-error")
		return
	}

	if err := sink.End(); err != nil {
		log.Warn("end yollanamadi", slog.String("err", err.Error()))
		return
	}

	log.Debug("istek tamamlandi",
		slog.Int("status", resp.StatusCode),
		slog.Int64("bayt", written),
		slog.Duration("sure", time.Since(started)))
}

// streamBody cevabi 16 KiB'lik parcalar halinde tunele akitir.
//
// Tam govde beklenmez (README Faz 1): ilk parca gelir gelmez yollanir, boylece
// Service Worker tarafinda ReadableStream akmaya baslar.
func (p *Proxy) streamBody(ctx context.Context, r io.Reader, sink tunnel.ResponseSink) (int64, error) {
	buf := make([]byte, chunkSize)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		n, readErr := r.Read(buf)
		if n > 0 {
			// sink.Write backpressure uygular: data channel tamponu dolu ise
			// burada bekleriz, bellekte govde birikmez.
			if err := sink.Write(buf[:n]); err != nil {
				return total, fmt.Errorf("tunele yazilamadi: %w", err)
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

// respondError ziyaretciye kisa bir hata cevabi doner.
//
// Yerel hedefin hata metnini DEGIL, sabit bir metni yollar: yerel sunucunun
// ic bilgisi ziyaretciye sizmasin.
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
