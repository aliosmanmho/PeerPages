// Package ice keeps the agent's ICE (STUN/TURN) configuration in one
// place.
//
// Why a separate package: until phase 4 the ICE information travelled
// through three layers from the CLI to the tunnel as a `[]string` and
// COULD NOT CARRY CREDENTIALS — webrtc.ICEServer's Username/Credential
// fields were never used. Because TURN needs credentials we gather the
// type here; this is also the single point that keeps credentials out of
// the logs and the banner (Describe).
//
// TURN is used ON THE AGENT SIDE ONLY; no credential is ever sent to the
// visitor. The reason is structural: the visitor creates the offer before
// hearing a single word from the agent, so sending TURN details over
// signaling would come too late. One-sided TURN is enough: the agent
// gathers a relay candidate, the visitor sends packets outward to that
// public address (a single destination, so the mapping is consistent even
// with symmetric NAT) and the agent recognises it as peer-reflexive.
// Details: ../../docs/BRIEF.md section 8.
package ice

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/pion/webrtc/v4"
)

// DefaultSTUN are the default STUN servers.
//
// Two of them: the second is both a fallback and what `doctor` needs for
// its NAT type guess (it compares the external port the two servers see).
// The list on the web side (web/src/ice.ts) must be the SAME as this
// one.
var DefaultSTUN = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// Error is a configuration error.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// Config is the ICE configuration.
type Config struct {
	// STUN holds the STUN server addresses (stun:/stuns:). When empty,
	// only host candidates are gathered — that is how the loopback tests
	// work.
	STUN []string
	// TURN holds the TURN server addresses (turn:/turns:).
	TURN []string
	// User and Pass are the TURN credentials (long-term credential).
	User, Pass string
	// RelayOnly gathers relay candidates only.
	//
	// A diagnostic/test flag: it makes the direct path IMPOSSIBLE, so the
	// question "did the connection really go over TURN" can be answered
	// deterministically. It is not needed in production — ICE already
	// picks the best path and no TURN quota is wasted.
	RelayOnly bool
	// RuntimeTURN set to true means the TURN address and credentials
	// arrive AT RUNTIME (the Cloudflare API).
	//
	// The validation has to know that: otherwise the "relay-only needs
	// --turn" or "no --turn-user" error would fire for nothing, simply
	// because the credentials have not been fetched yet.
	RuntimeTURN bool
}

// ParseServerURL validates and normalizes an ICE server address.
//
// The accepted schemes: stun, stuns, turn, turns. With no port given,
// 3478 is added (5349 for turns/stuns). `?transport=tcp` is kept: on
// networks that block UDP it is the only way to reach TURN over TCP.
//
// User information inside the URL (turn://user:pass@host) is REFUSED: the
// credentials come in separate flags, so the address carries no secret
// when it is written to a log or the banner (the sibling of the same
// check in internal/target).
func ParseServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errf("the ICE server address is empty")
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", errf("the ICE server address could not be read %q: %v", raw, err)
	}

	switch u.Scheme {
	case "stun", "stuns", "turn", "turns":
	default:
		return "", errf(
			"an ICE server address must start with stun:, stuns:, turn: or turns:, got: %q", raw)
	}
	if u.User != nil {
		return "", errf("an ICE server address must not carry user information (use --turn-user/--turn-pass): %q", raw)
	}

	// STUN/TURN URIs (RFC 7064/7065) are opaque: "stun:host:port" — there
	// is no `//` and the host sits inside u.Opaque.
	host := u.Opaque
	if host == "" {
		host = u.Host
	}
	if host == "" {
		return "", errf("the ICE server address has no host: %q", raw)
	}

	host, err = withDefaultPort(host, u.Scheme)
	if err != nil {
		return "", errf("%v: %q", err, raw)
	}

	out := u.Scheme + ":" + host
	if q := strings.TrimSpace(u.RawQuery); q != "" {
		if err := checkQuery(q); err != nil {
			return "", errf("%v: %q", err, raw)
		}
		out += "?" + q
	}
	return out, nil
}

func withDefaultPort(host, scheme string) (string, error) {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host, nil
	}
	// No port: add the default for the scheme (RFC 5389/5766).
	port := "3478"
	if scheme == "stuns" || scheme == "turns" {
		port = "5349"
	}
	withPort := net.JoinHostPort(host, port)
	if _, _, err := net.SplitHostPort(withPort); err != nil {
		return "", errors.New("an ICE server address must be host[:port]")
	}
	return withPort, nil
}

// checkQuery allows the transport parameter only.
func checkQuery(q string) error {
	vals, err := url.ParseQuery(q)
	if err != nil {
		return errors.New("the query in the ICE server address could not be read")
	}
	for k, v := range vals {
		if k != "transport" {
			return fmt.Errorf("an ICE server address may only carry the transport parameter, got: %q", k)
		}
		if len(v) != 1 || (v[0] != "udp" && v[0] != "tcp") {
			return errors.New("transport may only be udp or tcp")
		}
	}
	return nil
}

// Normalize validates and normalizes the addresses and removes
// duplicates.
func Normalize(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		u, err := ParseServerURL(r)
		if err != nil {
			return nil, err
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out, nil
}

// Validated returns a validated and normalized copy.
func (c Config) Validated() (Config, error) {
	stun, err := Normalize(c.STUN)
	if err != nil {
		return Config{}, err
	}
	turn, err := Normalize(c.TURN)
	if err != nil {
		return Config{}, err
	}
	for _, u := range turn {
		if strings.HasPrefix(u, "stun:") || strings.HasPrefix(u, "stuns:") {
			return Config{}, errf("--turn must be a TURN address (turn:/turns:), got: %q", u)
		}
	}
	for _, u := range stun {
		if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
			return Config{}, errf("--stun must be a STUN address (stun:/stuns:), got: %q", u)
		}
	}

	out := Config{
		STUN:        stun,
		TURN:        turn,
		User:        c.User,
		Pass:        c.Pass,
		RelayOnly:   c.RelayOnly,
		RuntimeTURN: c.RuntimeTURN,
	}

	switch {
	case len(turn) > 0 && out.User == "":
		return Config{}, errf("--turn was given but --turn-user is missing")
	case len(turn) > 0 && out.Pass == "":
		return Config{}, errf("--turn was given but --turn-pass is missing (PEERPAGES_TURN_PASS works too)")
	case len(turn) == 0 && !out.RuntimeTURN && (out.User != "" || out.Pass != ""):
		return Config{}, errf("--turn-user/--turn-pass were given but --turn is missing")
	}

	// Relay-only with no TURN means no candidate is gathered at all, so
	// the agent can accept no visitor. Falling into that silently means a
	// "could not connect" whose cause nobody can see.
	if out.RelayOnly && len(turn) == 0 && !out.RuntimeTURN {
		return Config{}, errf("--ice-relay-only needs at least one --turn")
	}
	return out, nil
}

// Servers builds the ICE server list for webrtc.
//
// Two separate entries: the STUNs without credentials, the TURNs with
// them. They cannot be merged into one entry because Username/Credential
// are per entry.
func (c Config) Servers() []webrtc.ICEServer {
	var out []webrtc.ICEServer
	if len(c.STUN) > 0 {
		out = append(out, webrtc.ICEServer{URLs: append([]string(nil), c.STUN...)})
	}
	if len(c.TURN) > 0 {
		out = append(out, webrtc.ICEServer{
			URLs:           append([]string(nil), c.TURN...),
			Username:       c.User,
			Credential:     c.Pass,
			CredentialType: webrtc.ICECredentialTypePassword,
		})
	}
	return out
}

// Policy is the ICE candidate policy.
func (c Config) Policy() webrtc.ICETransportPolicy {
	if c.RelayOnly {
		return webrtc.ICETransportPolicyRelay
	}
	return webrtc.ICETransportPolicyAll
}

// HasTURN says whether TURN was given.
func (c Config) HasTURN() bool { return len(c.TURN) > 0 }

// Describe is the summary for the banner and the logs.
//
// A credential is NEVER written out, only its length — the same pattern
// used for `--secret` (cli/nostr.go secretNote).
func (c Config) Describe() string {
	var b strings.Builder
	if len(c.STUN) == 0 {
		b.WriteString("no stun")
	} else {
		fmt.Fprintf(&b, "stun %d (%s)", len(c.STUN), strings.Join(c.STUN, ", "))
	}
	if len(c.TURN) == 0 && c.RuntimeTURN {
		b.WriteString("; turn from cloudflare (credentials at runtime)")
	} else if len(c.TURN) == 0 {
		b.WriteString("; no turn")
	} else {
		fmt.Fprintf(&b, "; turn %d (%s), credentials: %s",
			len(c.TURN), strings.Join(c.TURN, ", "), credNote(c.User, c.Pass))
	}
	if c.RelayOnly {
		b.WriteString("; RELAY ONLY (diagnostics)")
	}
	return b.String()
}

func credNote(user, pass string) string {
	if user == "" && pass == "" {
		return "none"
	}
	return fmt.Sprintf("user %q, password %d characters", user, len(pass))
}
