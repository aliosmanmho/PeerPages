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
		// UDP'yi kapatan aglarda TURN'e TCP ile baglanmanin tek yolu bu,
		// bu yuzden query korunuyor.
		{"transport tcp", "turn:example.com:3478?transport=tcp", "turn:example.com:3478?transport=tcp"},
		{"bosluk kirpilir", "  stun:example.com:3478  ", "stun:example.com:3478"},
		{"ipv4", "turn:203.0.113.5:3478", "turn:203.0.113.5:3478"},
		{"ipv6", "turn:[2001:db8::1]:3478", "turn:[2001:db8::1]:3478"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseServerURL(c.in)
			if err != nil {
				t.Fatalf("beklenmeyen hata: %v", err)
			}
			if got != c.want {
				t.Errorf("ParseServerURL(%q) = %q, beklenen %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseServerURLRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"bos", ""},
		{"yalnizca bosluk", "   "},
		{"sema yok", "example.com:3478"},
		{"http", "http://example.com"},
		{"ws", "wss://example.com"},
		{"host yok", "turn:"},
		// Kimlik bilgisi URL'de olamaz: adres log'a ve banner'a yaziliyor,
		// sir tasimamali.
		{"kullanici bilgisi", "turn://user:pass@example.com:3478"},
		{"tanimsiz query", "turn:example.com:3478?foo=bar"},
		{"gecersiz transport", "turn:example.com:3478?transport=sctp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := ParseServerURL(c.in); err == nil {
				t.Errorf("ParseServerURL(%q) = %q, hata beklendi", c.in, got)
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
		t.Errorf("Normalize = %v, 2 girdi beklendi (portsuz hali 3478'e esit)", got)
	}
}

func TestValidatedAccepts(t *testing.T) {
	cfg, err := Config{
		STUN: []string{"stun:example.com"},
		TURN: []string{"turn:turn.example.com"},
		User: "kullanici",
		Pass: "sifre",
	}.Validated()
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	if !cfg.HasTURN() {
		t.Error("HasTURN false")
	}
	if cfg.Policy() != webrtc.ICETransportPolicyAll {
		t.Error("politika all olmaliydi")
	}
}

func TestValidatedRejects(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"turn ama kullanici yok", Config{TURN: []string{"turn:a.example.com"}, Pass: "x"}},
		{"turn ama sifre yok", Config{TURN: []string{"turn:a.example.com"}, User: "x"}},
		{"kimlik ama turn yok", Config{STUN: []string{"stun:a.example.com"}, User: "x", Pass: "y"}},
		// Relay-only + TURN yok = hic aday toplanmaz; sessizce bu duruma
		// dusmek "sebebi anlasilmayan baglanti hatasi" demek.
		{"relay-only ama turn yok", Config{STUN: []string{"stun:a.example.com"}, RelayOnly: true}},
		{"turn alanina stun", Config{TURN: []string{"stun:a.example.com"}, User: "x", Pass: "y"}},
		{"stun alanina turn", Config{STUN: []string{"turn:a.example.com"}}},
		{"gecersiz adres", Config{STUN: []string{"nope"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.cfg.Validated(); err == nil {
				t.Error("hata beklendi")
			}
		})
	}
}

// STUN ve TURN AYRI girdiler olmali: Username/Credential girdi basinadir,
// tek listede birlestirilirse kimlik bilgisi STUN'a da gonderilir.
func TestServersSeparatesSTUNAndTURN(t *testing.T) {
	cfg, err := Config{
		STUN: []string{"stun:a.example.com:3478", "stun:b.example.com:3478"},
		TURN: []string{"turn:t.example.com:3478"},
		User: "kullanici",
		Pass: "sifre",
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	servers := cfg.Servers()
	if len(servers) != 2 {
		t.Fatalf("girdi sayisi %d, 2 beklendi", len(servers))
	}
	if len(servers[0].URLs) != 2 || servers[0].Username != "" || servers[0].Credential != nil {
		t.Errorf("STUN girdisi kimlik tasimamali: %+v", servers[0])
	}
	if servers[1].Username != "kullanici" || servers[1].Credential != "sifre" {
		t.Errorf("TURN girdisi kimlik tasimali: %+v", servers[1])
	}
	if servers[1].CredentialType != webrtc.ICECredentialTypePassword {
		t.Errorf("kimlik tipi %v, password beklendi", servers[1].CredentialType)
	}
}

func TestServersEmpty(t *testing.T) {
	// Loopback testleri hic ICE sunucusu vermiyor: host adaylari yeterli.
	if got := (Config{}).Servers(); len(got) != 0 {
		t.Errorf("bos yapilandirma %v dondurdu", got)
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
		t.Errorf("politika %v, relay beklendi", cfg.Policy())
	}
}

// Describe banner ve log'a gidiyor: SIFRE ASLA icinde olmamali.
func TestDescribeHidesPassword(t *testing.T) {
	const pass = "cok-gizli-sifre"
	cfg, err := Config{
		STUN: []string{"stun:a.example.com:3478"},
		TURN: []string{"turn:t.example.com:3478"},
		User: "kullanici",
		Pass: pass,
	}.Validated()
	if err != nil {
		t.Fatal(err)
	}

	got := cfg.Describe()
	if strings.Contains(got, pass) {
		t.Errorf("Describe sifreyi yaziyor: %q", got)
	}
	for _, want := range []string{"turn:t.example.com:3478", "kullanici", "15 characters"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe %q icermiyor: %q", want, got)
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
		t.Errorf("Describe = %q, 'no turn' beklendi", got)
	}
}

// Varsayilan liste iki sunucu olmali: `doctor` NAT tipini iki sunucunun
// gordugu dis portu karsilastirarak tahmin ediyor.
func TestDefaultSTUNHasTwoServers(t *testing.T) {
	if len(DefaultSTUN) < 2 {
		t.Errorf("DefaultSTUN %d sunucu; NAT tahmini icin en az 2 gerekiyor", len(DefaultSTUN))
	}
	if _, err := Normalize(DefaultSTUN); err != nil {
		t.Errorf("varsayilan liste kendi dogrulamasini gecmiyor: %v", err)
	}
}
