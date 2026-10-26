// Package doctor relay'lerin ve ag yolunun saglik kontrolu.
//
// Neden Faz 2'nin ILK adimi: README bolum 4 "gercek relay listesini Faz 2'de
// canli test edip guncelle" diyor ve bolum 8 "bazi relay'ler ephemeral
// event'leri dusurebilir" uyarisini tasiyor. Signaling akisini yazmadan once
// relay'lerin gercekten ise yaradigini olcmek gerekiyor; aksi halde
// calismayan bir relay listesi uzerine korlemesine kod yazilir.
//
// Her relay icin sirayla:
//  1. NIP-11 bilgi dokumani  — auth gerekiyor mu, sinirlar ne?
//  2. WebSocket baglantisi   — ulasilabiliyor mu, ne kadar surede?
//  3. Ephemeral yankı testi  — kind 25050 yayinlanip ABONEDEN geri okunuyor
//     mu? Bu en kritik adim: relay event'i "OK" ile kabul edip sessizce
//     dusurebilir, o zaman signaling calismaz.
//
// Ayrica STUN erisimi ve NAT tipi tahmini (README Faz 4'te duruyor ama
// baglanti sorunlarinin teshisi icin burada lazim).
package doctor

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip11"
	"github.com/pion/stun/v4"

	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
)

// Varsayilan sureler.
const (
	// DefaultNip11Timeout bilgi dokumani icin.
	DefaultNip11Timeout = 8 * time.Second
	// DefaultConnectTimeout WebSocket baglantisi icin.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultEchoTimeout yayinlanan event'in aboneden geri gelmesi icin.
	DefaultEchoTimeout = 8 * time.Second
	// DefaultSTUNTimeout tek bir STUN sorgusu icin.
	DefaultSTUNTimeout = 5 * time.Second
)

// Options sureleri ayarlanabilir kilar.
//
// Sifir birakilan alanlar varsayilana duser. Testler kisa sureler vererek
// kosum suresini dusurur; uretimde varsayilanlar kullanilir.
type Options struct {
	Nip11Timeout   time.Duration
	ConnectTimeout time.Duration
	EchoTimeout    time.Duration
	STUNTimeout    time.Duration
}

func (o Options) withDefaults() Options {
	if o.Nip11Timeout <= 0 {
		o.Nip11Timeout = DefaultNip11Timeout
	}
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = DefaultConnectTimeout
	}
	if o.EchoTimeout <= 0 {
		o.EchoTimeout = DefaultEchoTimeout
	}
	if o.STUNTimeout <= 0 {
		o.STUNTimeout = DefaultSTUNTimeout
	}
	return o
}

// Verdict bir relay'in kullanilabilirlik karari.
type Verdict string

// Olasi kararlar.
const (
	// VerdictUsable relay signaling icin kullanilabilir.
	VerdictUsable Verdict = "uygun"
	// VerdictUnreachable baglanti kurulamadi.
	VerdictUnreachable Verdict = "ulasilamadi"
	// VerdictAuthRequired NIP-42 auth istiyor; README bunlari atlamamizi soyluyor.
	VerdictAuthRequired Verdict = "atla: auth istiyor"
	// VerdictRejected yayin reddedildi.
	VerdictRejected Verdict = "atla: yayin reddedildi"
	// VerdictDropsEphemeral yayin kabul edildi ama event aboneye ulasmadi.
	VerdictDropsEphemeral Verdict = "atla: ephemeral dusuruyor"
)

// Nip11Summary bilgi dokumanindan ise yarayan alanlar.
type Nip11Summary struct {
	Name             string `json:"name,omitempty"`
	Software         string `json:"software,omitempty"`
	SupportedNIPs    []int  `json:"supported_nips,omitempty"`
	AuthRequired     bool   `json:"auth_required"`
	PaymentRequired  bool   `json:"payment_required"`
	RestrictedWrites bool   `json:"restricted_writes"`
	MaxMessageLength int    `json:"max_message_length,omitempty"`
	MaxContentLength int    `json:"max_content_length,omitempty"`
	MinPowDifficulty int    `json:"min_pow_difficulty,omitempty"`
}

// RelayReport tek bir relay'in sonucu.
type RelayReport struct {
	URL       string        `json:"url"`
	Verdict   Verdict       `json:"verdict"`
	Nip11     *Nip11Summary `json:"nip11,omitempty"`
	Nip11Err  string        `json:"nip11_err,omitempty"`
	Connected bool          `json:"connected"`
	ConnectMs float64       `json:"connect_ms,omitempty"`
	Published bool          `json:"published"`
	PublishMs float64       `json:"publish_ms,omitempty"`
	Echoed    bool          `json:"echoed"`
	EchoMs    float64       `json:"echo_ms,omitempty"`
	Err       string        `json:"err,omitempty"`
}

// Usable relay signaling icin kullanilabilir mi.
func (r RelayReport) Usable() bool { return r.Verdict == VerdictUsable }

// STUNServerReport tek bir STUN sunucusunun sonucu.
type STUNServerReport struct {
	URL      string  `json:"url"`
	OK       bool    `json:"ok"`
	MappedIP string  `json:"mapped_ip,omitempty"`
	Port     int     `json:"port,omitempty"`
	Ms       float64 `json:"ms,omitempty"`
	Err      string  `json:"err,omitempty"`
}

// NATVerdict NAT tipi tahmini.
type NATVerdict string

// Olasi NAT kararlari.
const (
	// NATUnknown yeterli veri yok (tek sunucuya ulasildi ya da hic ulasilamadi).
	NATUnknown NATVerdict = "bilinmiyor"
	// NATFriendly iki STUN sunucusu ayni dis portu gordu: endpoint-independent
	// mapping. WebRTC dogrudan baglanti kurabilir.
	NATFriendly NATVerdict = "uyumlu (TURN gerekmez)"
	// NATSymmetric portlar farkli: simetrik NAT. TURN gerekebilir (Faz 5).
	NATSymmetric NATVerdict = "simetrik (TURN gerekebilir)"
	// NATNoSTUN hicbir STUN sunucusuna ulasilamadi.
	NATNoSTUN NATVerdict = "STUN'a ulasilamadi"
)

// STUNReport STUN bolumunun sonucu.
type STUNReport struct {
	Servers []STUNServerReport `json:"servers"`
	Verdict NATVerdict         `json:"verdict"`
	Note    string             `json:"note,omitempty"`
}

// Report doctor'un tum sonucu.
type Report struct {
	Relays []RelayReport `json:"relays"`
	STUN   STUNReport    `json:"stun"`
	// UsableRelays kullanilabilir relay adresleri.
	UsableRelays []string `json:"usable_relays"`
}

// OK en az bir relay ve STUN calisiyor mu.
func (r Report) OK() bool {
	return len(r.UsableRelays) > 0
}

// CheckRelays verilen relay'leri paralel olarak test eder.
func CheckRelays(ctx context.Context, urls []string, log *slog.Logger, opts Options) []RelayReport {
	opts = opts.withDefaults()
	reports := make([]RelayReport, len(urls))
	var wg sync.WaitGroup

	for i, raw := range urls {
		wg.Add(1)
		go func(idx int, raw string) {
			defer wg.Done()
			reports[idx] = checkRelay(ctx, raw, log, opts)
		}(i, raw)
	}
	wg.Wait()
	return reports
}

func checkRelay(ctx context.Context, raw string, log *slog.Logger, opts Options) RelayReport {
	report := RelayReport{URL: raw, Verdict: VerdictUnreachable}

	normalized, err := nostr.NormalizeURL(raw)
	if err != nil {
		report.Err = err.Error()
		return report
	}
	report.URL = normalized

	// 1) NIP-11: auth isteyen relay'leri yayin denemeden once anlayabiliriz.
	if summary, err := fetchNip11(ctx, normalized, opts.Nip11Timeout); err != nil {
		report.Nip11Err = err.Error()
	} else {
		report.Nip11 = summary
		if summary.AuthRequired {
			report.Verdict = VerdictAuthRequired
			// Yine de baglanti denemesi yapalim ki sure olculsun.
		}
	}

	// 2) Baglanti.
	connectCtx, cancelConnect := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancelConnect()

	started := time.Now()
	relay, err := gonostr.RelayConnect(connectCtx, normalized)
	if err != nil {
		report.Err = fmt.Sprintf("baglanti: %v", err)
		return report
	}
	defer func() { _ = relay.Close() }()

	report.Connected = true
	report.ConnectMs = msSince(started)

	if report.Verdict == VerdictAuthRequired {
		return report
	}

	// 3) Ephemeral yanki testi: ONCE abone ol, SONRA yayinla.
	// Ephemeral event saklanmaz; dinlemiyorsan kacirirsin.
	sk := gonostr.GeneratePrivateKey()
	pub, err := gonostr.GetPublicKey(sk)
	if err != nil {
		report.Err = fmt.Sprintf("anahtar: %v", err)
		return report
	}

	echoCtx, cancelEcho := context.WithTimeout(ctx, opts.EchoTimeout)
	defer cancelEcho()

	filter := nostr.SignalingFilter(pub, time.Now().Add(-time.Minute))
	sub, err := relay.Subscribe(echoCtx, gonostr.Filters{filter})
	if err != nil {
		report.Err = fmt.Sprintf("abonelik: %v", err)
		return report
	}
	defer sub.Unsub()

	// EOSE'i bekle: abonelik gercekten kuruldu demek.
	select {
	case <-sub.EndOfStoredEvents:
	case <-echoCtx.Done():
		report.Err = "abonelik kurulamadi (EOSE gelmedi)"
		return report
	}

	// Kendimize yolluyoruz: baskasinin trafigine karismaz.
	ev := nostr.NewSignalingEvent(pub, "peerpages-doctor-"+randomHex())
	if err := ev.Sign(sk); err != nil {
		report.Err = fmt.Sprintf("imza: %v", err)
		return report
	}

	publishStarted := time.Now()
	if err := relay.Publish(echoCtx, ev); err != nil {
		report.Verdict = VerdictRejected
		report.Err = fmt.Sprintf("yayin: %v", err)
		return report
	}
	report.Published = true
	report.PublishMs = msSince(publishStarted)

	echoStarted := time.Now()
	for {
		select {
		case got, ok := <-sub.Events:
			if !ok {
				report.Verdict = VerdictDropsEphemeral
				report.Err = "abonelik kapandi, event geri gelmedi"
				return report
			}
			if got == nil || got.ID != ev.ID {
				continue // baska bir event; bizimkini beklemeye devam
			}
			report.Echoed = true
			report.EchoMs = msSince(echoStarted)
			report.Verdict = VerdictUsable
			log.Debug("relay uygun",
				slog.String("relay", normalized),
				slog.Float64("echo_ms", report.EchoMs))
			return report

		case <-echoCtx.Done():
			// Yayin kabul edildi ama event aboneye hic ulasmadi: relay
			// ephemeral event'leri dusuruyor (README bolum 8).
			report.Verdict = VerdictDropsEphemeral
			report.Err = fmt.Sprintf("event %s icinde geri gelmedi", opts.EchoTimeout)
			return report
		}
	}
}

func fetchNip11(ctx context.Context, relayURL string, timeout time.Duration) (*Nip11Summary, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	info, err := nip11.Fetch(c, relayURL)
	if err != nil {
		return nil, err
	}

	s := &Nip11Summary{
		Name:     info.Name,
		Software: info.Software,
	}
	for _, n := range info.SupportedNIPs {
		switch v := n.(type) {
		case float64:
			s.SupportedNIPs = append(s.SupportedNIPs, int(v))
		case int:
			s.SupportedNIPs = append(s.SupportedNIPs, v)
		}
	}
	sort.Ints(s.SupportedNIPs)

	if lim := info.Limitation; lim != nil {
		s.AuthRequired = lim.AuthRequired
		s.PaymentRequired = lim.PaymentRequired
		s.RestrictedWrites = lim.RestrictedWrites
		s.MaxMessageLength = lim.MaxMessageLength
		s.MaxContentLength = lim.MaxContentLength
		s.MinPowDifficulty = lim.MinPowDifficulty
	}
	return s, nil
}

// CheckSTUN STUN sunucularini sorgular ve NAT tipi tahmini uretir.
//
// Tahmin yontemi: iki farkli STUN sunucusuna sorup gorulen DIS PORT'lari
// karsilastirmak. Ayni port → endpoint-independent mapping (WebRTC dogrudan
// baglanabilir). Farkli port → simetrik NAT, TURN gerekebilir (Faz 5).
// Tek sunucuya ulasilabiliyorsa karar verilemez.
func CheckSTUN(ctx context.Context, urls []string, opts Options) STUNReport {
	opts = opts.withDefaults()
	report := STUNReport{Verdict: NATUnknown}

	for _, raw := range urls {
		report.Servers = append(report.Servers, checkSTUNServer(ctx, raw, opts.STUNTimeout))
	}

	ports := make([]int, 0, len(report.Servers))
	for _, s := range report.Servers {
		if s.OK {
			ports = append(ports, s.Port)
		}
	}

	switch {
	case len(ports) == 0:
		report.Verdict = NATNoSTUN
		report.Note = "UDP disariya kapali olabilir; bu durumda WebRTC hic calismaz."
	case len(ports) == 1:
		report.Verdict = NATUnknown
		report.Note = "NAT tipi icin en az iki STUN sunucusuna ulasmak gerekiyor."
	default:
		same := true
		for _, p := range ports[1:] {
			if p != ports[0] {
				same = false
				break
			}
		}
		if same {
			report.Verdict = NATFriendly
		} else {
			report.Verdict = NATSymmetric
			report.Note = "Farkli STUN sunuculari farkli dis port gordu. Karsi taraf da simetrik NAT arkasindaysa TURN gerekir (Faz 5)."
		}
	}
	return report
}

func checkSTUNServer(ctx context.Context, raw string, timeout time.Duration) STUNServerReport {
	report := STUNServerReport{URL: raw}

	addr, err := stunHostPort(raw)
	if err != nil {
		report.Err = err.Error()
		return report
	}

	started := time.Now()
	conn, err := net.DialTimeout("udp4", addr, timeout)
	if err != nil {
		report.Err = fmt.Sprintf("baglanti: %v", err)
		return report
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	client, err := stun.NewClient(conn)
	if err != nil {
		report.Err = fmt.Sprintf("istemci: %v", err)
		return report
	}
	defer func() { _ = client.Close() }()

	var mapped stun.XORMappedAddress
	var doErr error
	msg := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if err := client.Do(msg, func(res stun.Event) {
		if res.Error != nil {
			doErr = res.Error
			return
		}
		doErr = mapped.GetFrom(res.Message)
	}); err != nil {
		report.Err = fmt.Sprintf("sorgu: %v", err)
		return report
	}
	if doErr != nil {
		report.Err = fmt.Sprintf("cevap: %v", doErr)
		return report
	}

	report.OK = true
	report.MappedIP = mapped.IP.String()
	report.Port = mapped.Port
	report.Ms = msSince(started)
	return report
}

// stunHostPort `stun:host:port` ya da `host:port` bicimini host:port'a cevirir.
func stunHostPort(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("STUN adresi bos")
	}
	if strings.HasPrefix(s, "stun:") || strings.HasPrefix(s, "stuns:") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("STUN adresi okunamadi %q: %w", raw, err)
		}
		s = u.Opaque
		if s == "" {
			s = u.Host
		}
	}
	if !strings.Contains(s, ":") {
		s += ":3478"
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		return "", fmt.Errorf("STUN adresi host:port olmali %q", raw)
	}
	return s, nil
}

// Run tam kontrolu calistirir.
func Run(ctx context.Context, relays, stunServers []string, log *slog.Logger, opts Options) Report {
	report := Report{
		Relays: CheckRelays(ctx, relays, log, opts),
		STUN:   CheckSTUN(ctx, stunServers, opts),
	}
	for _, r := range report.Relays {
		if r.Usable() {
			report.UsableRelays = append(report.UsableRelays, r.URL)
		}
	}
	return report
}

// msSince gecen sureyi milisaniye olarak dondurur.
//
// Nanosaniye uzerinden hesaplaniyor: sahte relay'e baglanmak 1 mikrosaniyenin
// altinda surebiliyor ve mikrosaniye tabanli hesap bunu 0'a yuvarliyordu.
func msSince(t time.Time) float64 {
	return float64(time.Since(t).Nanoseconds()) / 1e6
}

func randomHex() string {
	// Event icerigini benzersiz kilmak icin; kriptografik olmasi gerekmiyor.
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
