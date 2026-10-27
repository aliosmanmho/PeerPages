// Package ice ajanin ICE (STUN/TURN) yapilandirmasini tek yerde tutar.
//
// Neden ayri paket: Faz 4'e kadar ICE bilgisi `[]string` olarak CLI'dan
// tunnel'a kadar uc katmandan geciyordu ve KIMLIK BILGISI TASIYAMIYORDU —
// webrtc.ICEServer'in Username/Credential alanlari hic kullanilmiyordu.
// TURN kimlik bilgisi gerektirdigi icin tipi burada topluyoruz; ayrica
// kimlik bilgisinin log'a ve banner'a sizmasini onleyen tek nokta da burasi
// (Describe).
//
// TURN YALNIZCA AJAN TARAFINDA kullanilir; ziyaretciye hicbir zaman kimlik
// bilgisi gonderilmez. Gerekce yapisal: ziyaretci offer'i ajandan tek kelime
// duymadan once uretiyor, yani TURN bilgisini signaling ile gondermek cok
// gec kalir. Tek tarafli TURN yeterlidir: ajan relay adayi toplar, ziyaretci
// o genel adrese disa dogru paket yollar (tek hedef, yani simetrik NAT'ta
// bile tutarli esleme) ve ajan onu peer-reflexive olarak tanir.
// Ayrinti: ../../README.md §8.
package ice

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/pion/webrtc/v4"
)

// DefaultSTUN varsayilan STUN sunuculari.
//
// Iki tane: ikincisi hem yedek hem de `doctor`un NAT tipi tahmini icin
// gerekli (iki sunucunun gordugu dis portu karsilastiriyor). Web tarafindaki
// liste (web/src/ice.ts) bununla AYNI olmali.
var DefaultSTUN = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// Error yapilandirma hatasi.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// Config ICE yapilandirmasi.
type Config struct {
	// STUN sunucu adresleri (stun:/stuns:). Bos ise yalnizca host adaylari
	// toplanir — loopback testleri boyle calisiyor.
	STUN []string
	// TURN sunucu adresleri (turn:/turns:).
	TURN []string
	// User, Pass TURN kimlik bilgisi (long-term credential).
	User, Pass string
	// RelayOnly yalnizca relay adayi toplanir.
	//
	// Teshis/test bayragi: dogrudan yolu IMKANSIZ kilar, boylece "baglanti
	// gercekten TURN uzerinden mi kuruldu" sorusu deterministik olarak
	// cevaplanabilir. Uretimde gerekmez — ICE zaten en iyi yolu secer ve
	// TURN kotasi bosa gitmez.
	RelayOnly bool
}

// ParseServerURL bir ICE sunucu adresini dogrular ve normalize eder.
//
// Kabul edilen semalar: stun, stuns, turn, turns. Port verilmemisse 3478
// (turns/stuns icin 5349) eklenir. `?transport=tcp` korunur: UDP'yi kapatan
// aglarda TURN'e TCP ile baglanmanin tek yolu bu.
//
// URL icinde kullanici bilgisi (turn://user:pass@host) REDDEDILIR: kimlik
// bilgisi ayri bayrakla gelir, boylece adres log'a ya da banner'a yazilirken
// sirri tasimaz (internal/target'taki ayni kontrolun kardesi).
func ParseServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errf("ICE sunucu adresi bos")
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", errf("ICE sunucu adresi okunamadi %q: %v", raw, err)
	}

	switch u.Scheme {
	case "stun", "stuns", "turn", "turns":
	default:
		return "", errf(
			"ICE sunucu adresi stun:, stuns:, turn: ya da turns: ile baslamali, verilen: %q", raw)
	}
	if u.User != nil {
		return "", errf("ICE sunucu adresinde kullanici bilgisi olamaz (--turn-user/--turn-pass kullan): %q", raw)
	}

	// STUN/TURN URI'leri (RFC 7064/7065) opaque'tir: "stun:host:port" —
	// yani `//` yok ve host, u.Opaque icinde durur.
	host := u.Opaque
	if host == "" {
		host = u.Host
	}
	if host == "" {
		return "", errf("ICE sunucu adresinde host yok: %q", raw)
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
	// Port yok: semaya gore varsayilani ekle (RFC 5389/5766).
	port := "3478"
	if scheme == "stuns" || scheme == "turns" {
		port = "5349"
	}
	withPort := net.JoinHostPort(host, port)
	if _, _, err := net.SplitHostPort(withPort); err != nil {
		return "", errors.New("ICE sunucu adresi host[:port] olmali")
	}
	return withPort, nil
}

// checkQuery yalnizca transport parametresine izin verir.
func checkQuery(q string) error {
	vals, err := url.ParseQuery(q)
	if err != nil {
		return errors.New("ICE sunucu adresindeki query okunamadi")
	}
	for k, v := range vals {
		if k != "transport" {
			return fmt.Errorf("ICE sunucu adresinde yalnizca transport parametresi olabilir, verilen: %q", k)
		}
		if len(v) != 1 || (v[0] != "udp" && v[0] != "tcp") {
			return errors.New("transport yalnizca udp ya da tcp olabilir")
		}
	}
	return nil
}

// Normalize adresleri dogrular, normalize eder ve tekrarlari ayiklar.
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

// Validated dogrulanmis ve normalize edilmis bir kopya dondurur.
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
			return Config{}, errf("--turn bir TURN adresi olmali (turn:/turns:), verilen: %q", u)
		}
	}
	for _, u := range stun {
		if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
			return Config{}, errf("--stun bir STUN adresi olmali (stun:/stuns:), verilen: %q", u)
		}
	}

	out := Config{STUN: stun, TURN: turn, User: c.User, Pass: c.Pass, RelayOnly: c.RelayOnly}

	switch {
	case len(turn) > 0 && out.User == "":
		return Config{}, errf("--turn verildi ama --turn-user yok")
	case len(turn) > 0 && out.Pass == "":
		return Config{}, errf("--turn verildi ama --turn-pass yok (PEERPAGES_TURN_PASS da kullanilabilir)")
	case len(turn) == 0 && (out.User != "" || out.Pass != ""):
		return Config{}, errf("--turn-user/--turn-pass verildi ama --turn yok")
	}

	// Relay-only + TURN yok = hic aday toplanmaz, yani ajan hicbir
	// ziyaretciyi kabul edemez. Sessizce bu duruma dusmek, sebebi
	// anlasilmayan bir "baglanti kurulamadi" demektir.
	if out.RelayOnly && len(turn) == 0 {
		return Config{}, errf("--ice-relay-only icin en az bir --turn gerekiyor")
	}
	return out, nil
}

// Servers webrtc icin ICE sunucu listesini uretir.
//
// Iki ayri girdi: STUN'lar kimliksiz, TURN'lar kimlik bilgisiyle. Tek bir
// girdide birlestirilemezler cunku Username/Credential girdi basinadir.
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

// Policy ICE aday politikasi.
func (c Config) Policy() webrtc.ICETransportPolicy {
	if c.RelayOnly {
		return webrtc.ICETransportPolicyRelay
	}
	return webrtc.ICETransportPolicyAll
}

// HasTURN TURN verilmis mi.
func (c Config) HasTURN() bool { return len(c.TURN) > 0 }

// Describe banner ve log icin ozet.
//
// Kimlik bilgisi ASLA yazilmaz, yalnizca uzunlugu — `--secret` icin
// kullanilan desenin aynisi (cli/nostr.go secretNote).
func (c Config) Describe() string {
	var b strings.Builder
	if len(c.STUN) == 0 {
		b.WriteString("stun yok")
	} else {
		fmt.Fprintf(&b, "stun %d (%s)", len(c.STUN), strings.Join(c.STUN, ", "))
	}
	if len(c.TURN) == 0 {
		b.WriteString("; turn yok")
	} else {
		fmt.Fprintf(&b, "; turn %d (%s), kimlik: %s",
			len(c.TURN), strings.Join(c.TURN, ", "), credNote(c.User, c.Pass))
	}
	if c.RelayOnly {
		b.WriteString("; YALNIZCA RELAY (teshis)")
	}
	return b.String()
}

func credNote(user, pass string) string {
	if user == "" && pass == "" {
		return "yok"
	}
	return fmt.Sprintf("kullanici %q, sifre %d karakter", user, len(pass))
}
