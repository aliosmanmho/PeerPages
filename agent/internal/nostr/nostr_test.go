package nostr_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	gonostr "github.com/nbd-wtf/go-nostr"

	"github.com/aliosmanmho/PeerPages/agent/internal/nostr"
	"github.com/aliosmanmho/PeerPages/agent/internal/nostr/fakerelay"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// signed test icin imzali bir signaling event'i uretir.
func signed(t *testing.T, sk, recipient, content string) gonostr.Event {
	t.Helper()
	ev := nostr.NewSignalingEvent(recipient, content)
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("imzalanamadi: %v", err)
	}
	return ev
}

func keypair(t *testing.T) (sk, pub string) {
	t.Helper()
	sk = gonostr.GeneratePrivateKey()
	pub, err := gonostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("pubkey uretilemedi: %v", err)
	}
	return sk, pub
}

func TestNormalizeURL(t *testing.T) {
	ok := map[string]string{
		"wss://relay.damus.io":   "wss://relay.damus.io",
		"wss://relay.damus.io/":  "wss://relay.damus.io",
		" wss://nos.lol ":        "wss://nos.lol",
		"ws://127.0.0.1:8080":    "ws://127.0.0.1:8080",
		"wss://a.example/nostr/": "wss://a.example/nostr",
		"wss://a.example#x":      "wss://a.example",
	}
	for in, want := range ok {
		got, err := nostr.NormalizeURL(in)
		if err != nil {
			t.Errorf("NormalizeURL(%q) hata: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeURL(%q) = %q, beklenen %q", in, got, want)
		}
	}

	for _, in := range []string{
		"", "   ", "relay.damus.io", "http://relay.damus.io",
		"https://relay.damus.io", "wss://", "ftp://x",
	} {
		if got, err := nostr.NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q) reddedilmeliydi, donen: %q", in, got)
		}
	}
}

func TestNormalizeURLsDeduplicates(t *testing.T) {
	got, err := nostr.NormalizeURLs([]string{
		"wss://a.example", "wss://a.example/", "wss://b.example", "wss://a.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "wss://a.example,wss://b.example" {
		t.Errorf("tekrarlar ayiklanmali, geldi: %v", got)
	}

	if _, err := nostr.NormalizeURLs(nil); err == nil {
		t.Error("bos liste reddedilmeliydi")
	}
}

func TestDefaultRelaysAreValid(t *testing.T) {
	got, err := nostr.NormalizeURLs(nostr.DefaultRelays)
	if err != nil {
		t.Fatalf("varsayilan relay listesi gecersiz: %v", err)
	}
	if len(got) < 3 {
		t.Errorf("BRIEF en az 3 relay istiyor, %d var", len(got))
	}
}

// Kind 25050 ephemeral araliginda olmali: relay'ler saklamasin.
func TestKindIsEphemeral(t *testing.T) {
	if nostr.KindSignaling < 20000 || nostr.KindSignaling >= 30000 {
		t.Errorf("kind %d ephemeral araliginda (20000-29999) degil", nostr.KindSignaling)
	}
}

func TestSignalingEventShape(t *testing.T) {
	ev := nostr.NewSignalingEvent("abc123", "sifreli")
	if ev.Kind != nostr.KindSignaling {
		t.Errorf("kind %d", ev.Kind)
	}
	if ev.Content != "sifreli" {
		t.Errorf("content %q", ev.Content)
	}
	got, ok := nostr.RecipientOf(&ev)
	if !ok || got != "abc123" {
		t.Errorf("alici tag'i yanlis: %q %v", got, ok)
	}

	if _, ok := nostr.RecipientOf(nil); ok {
		t.Error("nil event icin alici donmemeli")
	}
	empty := gonostr.Event{}
	if _, ok := nostr.RecipientOf(&empty); ok {
		t.Error("tag'siz event icin alici donmemeli")
	}
}

func TestShortPubKey(t *testing.T) {
	long := strings.Repeat("a", 64)
	got := nostr.ShortPubKey(long)
	if len(got) >= len(long) || !strings.Contains(got, "…") {
		t.Errorf("kisaltilmali, geldi: %q", got)
	}
	if nostr.ShortPubKey("kisa") != "kisa" {
		t.Error("kisa deger oldugu gibi kalmali")
	}
}

// --- sahte relay ile ---

func TestPublishAndSubscribe(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	sk, _ := keypair(t)
	_, recipientPub := keypair(t)

	// ONCE abone ol: ephemeral event saklanmaz, dinlemiyorsan kacirirsin.
	events := pool.Subscribe(ctx, nostr.SignalingFilter(recipientPub, time.Now().Add(-time.Minute)))
	time.Sleep(300 * time.Millisecond)

	ev := signed(t, sk, recipientPub, "merhaba-sifreli")
	report, err := pool.Publish(ctx, ev)
	if err != nil {
		t.Fatalf("Publish hata: %v (%+v)", err, report)
	}
	if !report.Succeeded() || len(report.OK) != 1 {
		t.Fatalf("yayin raporu: %+v", report)
	}

	select {
	case got := <-events:
		if got.Content != "merhaba-sifreli" {
			t.Errorf("icerik: %q", got.Content)
		}
		if got.Kind != nostr.KindSignaling {
			t.Errorf("kind: %d", got.Kind)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("event aboneye ulasmadi")
	}
}

// Filtre baska bir aliciya giden event'i getirmemeli.
func TestSubscribeFiltersByRecipient(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	sk, _ := keypair(t)
	_, mine := keypair(t)
	_, other := keypair(t)

	events := pool.Subscribe(ctx, nostr.SignalingFilter(mine, time.Now().Add(-time.Minute)))
	time.Sleep(300 * time.Millisecond)

	// Baskasina giden event.
	if _, err := pool.Publish(ctx, signed(t, sk, other, "baskasina")); err != nil {
		t.Fatal(err)
	}
	// Bize gelen event.
	if _, err := pool.Publish(ctx, signed(t, sk, mine, "bana")); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-events:
		if got.Content != "bana" {
			t.Errorf("filtre sizdirdi: %q", got.Content)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("event gelmedi")
	}
}

// Hedged yayin: bir relay reddetse bile digeri kabul ederse basarili.
func TestPublishHedgedAcrossRelays(t *testing.T) {
	bad, err := fakerelay.Start(fakerelay.Options{RejectAll: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	good, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{
		URLs:   []string{bad.URL(), good.URL()},
		Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	sk, _ := keypair(t)
	_, pub := keypair(t)

	report, err := pool.Publish(ctx, signed(t, sk, pub, "x"))
	if err != nil {
		t.Fatalf("bir relay kabul ettiginde basarili olmaliydi: %v (%+v)", err, report)
	}
	if len(report.OK) != 1 || len(report.Failed) != 1 {
		t.Errorf("rapor: %+v", report)
	}
}

// Hicbir relay kabul etmezse hata donmeli.
func TestPublishFailsWhenAllReject(t *testing.T) {
	bad1, err := fakerelay.Start(fakerelay.Options{RejectAll: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bad1.Close()
	bad2, err := fakerelay.Start(fakerelay.Options{RequireAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	defer bad2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{
		URLs:   []string{bad1.URL(), bad2.URL()},
		Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	sk, _ := keypair(t)
	_, pub := keypair(t)

	report, err := pool.Publish(ctx, signed(t, sk, pub, "x"))
	if err == nil {
		t.Fatalf("hata beklenirdi, rapor: %+v", report)
	}
	if len(report.Failed) != 2 {
		t.Errorf("iki relay de basarisiz olmaliydi: %+v", report)
	}
}

// BRIEF bolum 8: bazi relay'ler ephemeral event'leri dusurebilir.
// Dusuren relay yayini KABUL eder ama abonelere iletmez; doctor bunu
// yakalamali, pool ise yayini basarili saymali.
func TestEphemeralDroppingRelay(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{DropEphemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	sk, _ := keypair(t)
	_, pub := keypair(t)

	events := pool.Subscribe(ctx, nostr.SignalingFilter(pub, time.Now().Add(-time.Minute)))
	time.Sleep(300 * time.Millisecond)

	if _, err := pool.Publish(ctx, signed(t, sk, pub, "dusurulecek")); err != nil {
		t.Fatalf("yayin kabul edilmeliydi: %v", err)
	}
	if len(relay.Published()) != 1 {
		t.Errorf("event relay'e ulasmaliydi: %d", len(relay.Published()))
	}

	select {
	case got := <-events:
		t.Errorf("ephemeral dusuren relay event iletmemeliydi: %q", got.Content)
	case <-time.After(2 * time.Second):
		// Beklenen: event gelmedi.
	}
}

// Imzasiz event relay tarafindan reddedilmeli (imzalamayi atlayan hata
// testte yakalanabilsin).
func TestUnsignedEventRejected(t *testing.T) {
	relay, err := fakerelay.Start(fakerelay.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{relay.URL()}, Logger: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close("test bitti")

	_, pub := keypair(t)
	unsigned := nostr.NewSignalingEvent(pub, "imzasiz")

	if _, err := pool.Publish(ctx, unsigned); err == nil {
		t.Error("imzasiz event reddedilmeliydi")
	}
}

func TestNewPoolValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := nostr.NewPool(ctx, nostr.Config{URLs: []string{"wss://a.example"}}); err == nil {
		t.Error("Logger nil reddedilmeliydi")
	}
	if _, err := nostr.NewPool(ctx, nostr.Config{Logger: quietLogger()}); err == nil {
		t.Error("bos relay listesi reddedilmeliydi")
	}
	pool, err := nostr.NewPool(ctx, nostr.Config{
		URLs:   []string{"wss://a.example/", "wss://a.example"},
		Logger: quietLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.URLs(); len(got) != 1 {
		t.Errorf("tekrarlar ayiklanmali: %v", got)
	}
}
