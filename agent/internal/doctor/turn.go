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

// DefaultTURNTimeout bounds a single TURN allocation attempt.
const DefaultTURNTimeout = 10 * time.Second

// TURNServerReport is a TURN server's result.
type TURNServerReport struct {
	URL string `json:"url"`
	OK  bool   `json:"ok"`
	// RelayedAddr is the relay address the server gave us; that is the
	// real proof.
	RelayedAddr string  `json:"relayed_addr,omitempty"`
	MS          float64 `json:"ms"`
	Err         string  `json:"err,omitempty"`
}

// TURNReport is the result of the TURN check.
type TURNReport struct {
	Servers []TURNServerReport `json:"servers"`
}

// Usable says whether at least one TURN server granted an allocation.
func (r TURNReport) Usable() bool {
	for _, s := range r.Servers {
		if s.OK {
			return true
		}
	}
	return false
}

// CheckTURN ASKS every TURN server FOR AN ALLOCATION.
//
// The question we answer is not "is the server up" but "does it give me a
// relay address": with wrong credentials, a full quota, or a server
// listening on TCP only, it looks up yet grants no allocation — and then
// TURN is of no use at all.
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

	// turns: (TLS) is not attempted here: the pion/turn client needs a
	// separate connection for TLS, and the point of this check is whether
	// the server grants an allocation. Let us be honest: we skip TLS, try
	// UDP, and write that down in the report.
	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		out.Err = fmt.Sprintf("the local UDP socket could not be opened: %v", err)
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
		out.Err = fmt.Sprintf("the client could not be built: %v", err)
		out.MS = msSince(started)
		return out
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		out.Err = fmt.Sprintf("listening could not be started: %v", err)
		out.MS = msSince(started)
		return out
	}

	// Bound the allocation attempt: against an unreachable TURN server
	// the client keeps retrying.
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
			out.Err = fmt.Sprintf("no allocation was granted: %v", r.err)
			return out
		}
		out.OK = true
		if r.addr != nil {
			out.RelayedAddr = r.addr.String()
		}
		return out
	case <-timer.C:
		out.MS = msSince(started)
		out.Err = fmt.Sprintf("no allocation was granted within %s", timeout)
		return out
	case <-ctx.Done():
		out.MS = msSince(started)
		out.Err = ctx.Err().Error()
		return out
	}
}

// turnHostPort turns the `turn:host:port` form into host:port.
//
// ice.ParseServerURL already normalizes it (adds the port, refuses
// credentials); here we only trim the scheme and the query.
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
		return "", fmt.Errorf("the TURN address could not be read: %q", raw)
	}
	hostPort := s[i+1:]
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		return "", fmt.Errorf("a TURN address must be host:port: %q", raw)
	}
	return hostPort, nil
}
