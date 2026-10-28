package ice

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestParseServerURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"stun port ile", "stun:stun.l.google.com:19302", "stun:stun.l.google.com:19302"},
		{"stun portsuz", "stun:example.com", "stun:example.com:3478"},
		{"stuns portsuz", "stuns:example.com", "stuns:example.com:5349"},
		{"turn portsuz", "turn:example.com", "turn:example.com:3478"},
		{"turns portsuz", "turns:example.com", "turns:example.com:5349"},
		// On networks that block UDP this is the only way to reach TURN
		// over TCP, which is why the query is kept.
		{"transport tcp", "turn:example.com:3478?transport=tcp", "turn:example.com:3478?transport=tcp"},
		{"whitespace is trimmed", "  stun:example.com:3478  ", "stun:example.com:3478"},
		{"ipv4", "turn:203.0.113.5:3478", "turn:203.0.113.5:3478"},
		{"ipv6", "turn:[2001:db8::1]:3478", "turn:[2001:db8::1]:3478"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseServerURL(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("ParseServerURL(%q) = %q, expected %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseServerURLRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"no scheme", "example.com:3478"},
		{"http", "http://example.com"},
		{"ws", "wss://example.com"},
		{"no host", "turn:"},
		// Credentials cannot live in the URL: the address is written to
		// the log and the banner, and must carry no secret.
		{"user information", "turn://user:pass@example.com:3478"},
		{"tanimsiz query", "turn:example.com:3478?foo=bar"},
		{"an invalid transport", "turn:example.com:3478?transport=sctp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := ParseServerURL(c.in); err == nil {
				t.Errorf("ParseServerURL(%q) = %q, an error was expected", c.in, got)
			}
		})
	}
}

func TestNormalizeDedupes(t *testing.T) {
	got, err := Normalize([]string{"stun:example.com", "stun:example.com:3478", "stun:other.com:19302"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("Normalize = %v, 2 entries expected (the form without a port equals 3478)", got)
	}
}

func TestValidatedAccepts(t *testing.T) {
	cfg, err := Config{
		STUN: []string{"stun:example.com"},
		TURN: []string{"turn:turn.example.com"},
		User: "user",
		Pass: "password",
	}.Validated()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.HasTURN() {
		t.Error("HasTURN false")
	}
	if cfg.Policy() != webrtc.ICETransportPolicyAll {
		t.Error("the policy should have been all")
	}
}

func TestValidatedRejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"turn but no user", Config{TURN: []string{"turn:a.example.com"}, Pass: "x"}},
		{"turn but no password", Config{TURN: []string{"turn:a.example.com"}, User: "x"}},
		{"credentials but no turn", Config{STUN: []string{"stun:a.example.com"}, User: "x", Pass: "y"}},
		// Relay-only with no TURN means no candidate is gathered at all;
		// falling into that silently means "a connection error nobody can
		// explain".
		{"relay-only but no turn", Config{STUN: []string{"stun:a.example.com"}, RelayOnly: true}},
		{"turn alanina stun", Config{TURN: []string{"stun:a.example.com"}, User: "x", Pass: "y"}},
		{"stun alanina turn", Config{STUN: []string{"turn:a.example.com"}}},
		{"an invalid address", Config{STUN: []string{"nope"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.cfg.Validated(); err == nil {
				t.Error("an error was expected")
			}
		})
	}
}

// STUN and TURN have to be SEPARATE entries: Username/Credential are per
// entry, and merging them into one list would send the credentials to
// STUN as well.
func TestServersSeparatesSTUNAndTURN(t *testing.T) {
	cfg, err := Config{
		STUN: []string{"stun:a.example.com:3478", "stun:b.example.com:3478"},
		TURN: []string{"turn:t.example.com:3478"},
		User: "user",
		Pass: "password",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	servers := cfg.Servers()
	if len(servers) != 2 {
		t.Fatalf("%d entries, 2 expected", len(servers))
	}
	if len(servers[0].URLs) != 2 || servers[0].Username != "" || servers[0].Credential != nil {
		t.Errorf("a STUN entry must carry no credentials: %+v", servers[0])
	}
	if servers[1].Username != "user" || servers[1].Credential != "password" {
		t.Errorf("a TURN entry has to carry credentials: %+v", servers[1])
	}
	if servers[1].CredentialType != webrtc.ICECredentialTypePassword {
		t.Errorf("the credential type is %v, password expected", servers[1].CredentialType)
	}
}

func TestServersEmpty(t *testing.T) {
	// The loopback tests pass no ICE server at all: host candidates are
	// enough.
	if got := (Config{}).Servers(); len(got) != 0 {
		t.Errorf("the empty configuration returned %v", got)
	}
}

func TestPolicyRelayOnly(t *testing.T) {
	cfg, err := Config{
		TURN:      []string{"turn:t.example.com"},
		User:      "u",
		Pass:      "p",
		RelayOnly: true,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy() != webrtc.ICETransportPolicyRelay {
		t.Errorf("policy %v, relay expected", cfg.Policy())
	}
}

// Describe goes to the banner and the log: the PASSWORD must NEVER be in
// it.
func TestDescribeHidesPassword(t *testing.T) {
	const pass = "very-secret-password"
	cfg, err := Config{
		STUN: []string{"stun:a.example.com:3478"},
		TURN: []string{"turn:t.example.com:3478"},
		User: "user",
		Pass: pass,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Describe()
	if strings.Contains(got, pass) {
		t.Errorf("Describe writes the password out: %q", got)
	}
	for _, want := range []string{"turn:t.example.com:3478", "user", "20 characters"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe does not contain %q: %q", want, got)
		}
	}
}

func TestDescribeWithoutTURN(t *testing.T) {
	cfg, err := Config{STUN: DefaultSTUN}.Validated()
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Describe()
	if !strings.Contains(got, "no turn") {
		t.Errorf("Describe = %q, 'no turn' expected", got)
	}
}

// The default list has to hold two servers: `doctor` guesses the NAT
// type by comparing the external port the two servers see.
func TestDefaultSTUNHasTwoServers(t *testing.T) {
	if len(DefaultSTUN) < 2 {
		t.Errorf("DefaultSTUN has %d servers; the NAT guess needs at least 2", len(DefaultSTUN))
	}
	if _, err := Normalize(DefaultSTUN); err != nil {
		t.Errorf("the default list does not pass its own validation: %v", err)
	}
}
