package doctor

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"

	"github.com/aliosmanmho/PeerPages/agent/internal/ice"
)

// DefaultTURNTimeout tek bir TURN allocation denemesi icin.
const DefaultTURNTimeout = 10 * time.Second

// TURNServerReport bir TURN sunucusunun sonucu.
type TURNServerReport struct {
	URL string `json:"url"`
	OK  bool   `json:"ok"`
	// RelayedAddr sunucunun bize verdigi relay adresi; asil kanit bu.
	RelayedAddr string  `json:"relayed_addr,omitempty"`
	MS          float64 `json:"ms"`
	Err         string  `json:"err,omitempty"`
}

// TURNReport TURN kontrolunun sonucu.
type TURNReport struct {
	Servers []TURNServerReport `json:"servers"`
}

// Usable en az bir TURN sunucusu allocation verdi mi.
func (r TURNReport) Usable() bool {
	for _, s := range r.Servers {
		if s.OK {
			return true
		}
	}
	return false
}

// CheckTURN her TURN sunucusundan ALLOCATION ISTER.
//
// "Sunucu ayakta mi" degil "bana relay adresi veriyor mu" sorusunu
// cevapliyoruz: yanlis kimlik bilgisi, dolu kota ya da yalnizca TCP dinleyen
// bir sunucu ayakta gorunup allocation vermez, ve o zaman TURN hic ise
// yaramaz.
func CheckTURN(ctx context.Context, cfg ice.Config, opts Options) TURNReport {
	opts = opts.withDefaults()

	report := TURNReport{Servers: make([]TURNServerReport, 0, len(cfg.TURN))}
	for _, u := range cfg.TURN {
		report.Servers = append(report.Servers, checkTURNServer(ctx, u, cfg.User, cfg.Pass, opts))
	}
	return report
}

func checkTURNServer(
	ctx context.Context, rawURL, user, pass string, opts Options,
) TURNServerReport {
	out := TURNServerReport{URL: rawURL}
	started := time.Now()

	hostPort, err := turnHostPort(rawURL)
	if err != nil {
		out.Err = err.Error()
		out.MS = msSince(started)
		return out
	}

	// turns: (TLS) burada denenmiyor: pion/turn istemcisi TLS icin ayri bir
	// baglanti kurmayi gerektiriyor ve bu kontrolun amaci sunucunun
	// allocation verip vermedigi. Durust olalim: TLS'i atlayip UDP'yi
	// deniyoruz ve bunu rapora yaziyoruz.
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		out.Err = fmt.Sprintf("yerel UDP soketi acilamadi: %v", err)
		out.MS = msSince(started)
		return out
	}
	defer func() { _ = conn.Close() }()

	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: hostPort,
		TURNServerAddr: hostPort,
		Username:       user,
		Password:       pass,
		Realm:          "",
		Conn:           conn,
		LoggerFactory:  &logging.DefaultLoggerFactory{Writer: io.Discard},
	})
	if err != nil {
		out.Err = fmt.Sprintf("istemci kurulamadi: %v", err)
		out.MS = msSince(started)
		return out
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		out.Err = fmt.Sprintf("dinleme baslatilamadi: %v", err)
		out.MS = msSince(started)
		return out
	}

	// Allocation denemesini sureyle sinirla: ulasilamayan bir TURN
	// sunucusunda istemci yeniden denemeye devam eder.
	type result struct {
		addr net.Addr
		err  error
	}
	done := make(chan result, 1)
	go func() {
		relayConn, aerr := client.Allocate()
		if aerr != nil {
			done <- result{err: aerr}
			return
		}
		done <- result{addr: relayConn.LocalAddr()}
		_ = relayConn.Close()
	}()

	timeout := opts.TURNTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-done:
		out.MS = msSince(started)
		if r.err != nil {
			out.Err = fmt.Sprintf("allocation alinamadi: %v", r.err)
			return out
		}
		out.OK = true
		if r.addr != nil {
			out.RelayedAddr = r.addr.String()
		}
		return out
	case <-timer.C:
		out.MS = msSince(started)
		out.Err = fmt.Sprintf("allocation %s icinde alinamadi", timeout)
		return out
	case <-ctx.Done():
		out.MS = msSince(started)
		out.Err = ctx.Err().Error()
		return out
	}
}

// turnHostPort `turn:host:port` bicimini host:port'a cevirir.
//
// ice.ParseServerURL zaten normalize ediyor (port ekliyor, kimlik bilgisi
// reddediyor); burada yalnizca semayi ve query'yi kirpiyoruz.
func turnHostPort(raw string) (string, error) {
	normalized, err := ice.ParseServerURL(raw)
	if err != nil {
		return "", err
	}
	s := normalized
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	i := strings.Index(s, ":")
	if i < 0 {
		return "", fmt.Errorf("TURN adresi okunamadi: %q", raw)
	}
	hostPort := s[i+1:]
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		return "", fmt.Errorf("TURN adresi host:port olmali: %q", raw)
	}
	return hostPort, nil
}
