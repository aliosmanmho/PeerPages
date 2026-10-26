// Package fakerelay testler icin surec icinde calisan kucuk bir Nostr relay'i.
//
// NIP-01'in YALNIZCA PeerPages'in kullandigi alt kumesini konusur:
//
//	istemci -> relay : ["EVENT", <event>]              yayin
//	                   ["REQ", <subId>, <filter>]      abonelik
//	                   ["CLOSE", <subId>]              abonelikten cik
//	relay -> istemci : ["OK", <id>, true|false, <msg>] yayin sonucu
//	                   ["EVENT", <subId>, <event>]     eslesen event
//	                   ["EOSE", <subId>]               gecmis bitti
//	                   ["NOTICE", <msg>]               bilgi
//
// Neden var: gercek internet relay'lerine bagimli CI testi kirilgan olur
// (relay dusebilir, hiz limiti uygulayabilir, ephemeral event'i atabilir).
// Akisin kendisi burada deterministik dogrulanir; gercek relay'ler
// `peerpages doctor` ile ayrica olculur.
//
// Ephemeral event'ler (kind 20000-29999) NIP-01'e uygun olarak SAKLANMAZ:
// yalnizca o anda dinleyen abonelere iletilir. Boylece "relay ephemeral
// event'i dusurur mu" sorusu testte dogru modellenir.
package fakerelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	gonostr "github.com/nbd-wtf/go-nostr"
)

// Options relay davranisini test senaryosuna gore degistirir.
type Options struct {
	// DropEphemeral true ise ephemeral event'ler kabul edilir ama abonelere
	// ILETILMEZ. README bolum 8'deki "bazi relay'ler ephemeral event'leri
	// dusurebilir" durumunu modellemek icin.
	DropEphemeral bool
	// RejectAll true ise her yayin "OK false" ile reddedilir.
	RejectAll bool
	// RequireAuth true ise NIP-42 isteyen bir relay gibi davranir: yayin
	// "auth-required" ile reddedilir.
	RequireAuth bool
	// PublishDelay yayin cevabini geciktirir (hedged yayini test etmek icin).
	PublishDelay time.Duration
}

// Relay surec ici test relay'i.
type Relay struct {
	srv  *http.Server
	ln   net.Listener
	opts Options

	mu   sync.Mutex
	subs map[*subscription]struct{}

	// published gorulen tum event'ler (ephemeral dahil) — testler
	// "relay'e ulasti mi" diye bakabilsin.
	publishedMu sync.Mutex
	published   []gonostr.Event
}

type subscription struct {
	conn   *websocket.Conn
	subID  string
	filter gonostr.Filter
	writeM *sync.Mutex
}

// Start relay'i baslatir ve ws:// adresini dondurur.
func Start(opts Options) (*Relay, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("dinlenemedi: %w", err)
	}

	r := &Relay{
		ln:   ln,
		opts: opts,
		subs: map[*subscription]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handle)
	r.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		_ = r.srv.Serve(ln)
	}()
	return r, nil
}

// URL relay'in ws adresi.
func (r *Relay) URL() string {
	return "ws://" + r.ln.Addr().String()
}

// Close relay'i kapatir.
func (r *Relay) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.srv.Shutdown(ctx)
}

// Published relay'e ulasan event'lerin kopyasini dondurur.
func (r *Relay) Published() []gonostr.Event {
	r.publishedMu.Lock()
	defer r.publishedMu.Unlock()
	out := make([]gonostr.Event, len(r.published))
	copy(out, r.published)
	return out
}

// SubscriberCount acik abonelik sayisi.
func (r *Relay) SubscriberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

func (r *Relay) handle(w http.ResponseWriter, req *http.Request) {
	// NIP-11: istemci bilgi dokumani istiyor olabilir.
	if req.Header.Get("Accept") == "application/nostr+json" {
		w.Header().Set("content-type", "application/nostr+json")
		info := map[string]any{
			"name":           "fakerelay",
			"description":    "PeerPages test relay'i",
			"supported_nips": []int{1, 11, 44},
		}
		if r.opts.RequireAuth {
			info["supported_nips"] = []int{1, 11, 42, 44}
			info["limitation"] = map[string]any{"auth_required": true}
		}
		_ = json.NewEncoder(w).Encode(info)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	// Mesaj boyutu: 5 MB'lik gövde buradan gecmiyor ama SDP buyuk olabilir.
	conn.SetReadLimit(1 << 20)

	writeM := &sync.Mutex{}
	ctx := req.Context()

	defer r.dropConn(conn)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if err := r.onMessage(ctx, conn, writeM, data); err != nil {
			return
		}
	}
}

func (r *Relay) onMessage(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "gecersiz mesaj"})
	}

	var verb string
	if err := json.Unmarshal(raw[0], &verb); err != nil {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "gecersiz mesaj tipi"})
	}

	switch verb {
	case "EVENT":
		return r.onEvent(ctx, conn, writeM, raw)
	case "REQ":
		return r.onReq(ctx, conn, writeM, raw)
	case "CLOSE":
		return r.onClose(raw)
	default:
		return r.write(ctx, conn, writeM, []any{"NOTICE", "desteklenmeyen: " + verb})
	}
}

func (r *Relay) onEvent(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, raw []json.RawMessage) error {
	if len(raw) < 2 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "EVENT eksik"})
	}
	var ev gonostr.Event
	if err := json.Unmarshal(raw[1], &ev); err != nil {
		return r.write(ctx, conn, writeM, []any{"OK", "", false, "invalid: event okunamadi"})
	}

	if r.opts.PublishDelay > 0 {
		select {
		case <-time.After(r.opts.PublishDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Imza dogrulamasi: gercek relay'ler yapar, biz de yapiyoruz ki
	// imzalamayi atlayan bir hata testte yakalanabilsin.
	ok, err := ev.CheckSignature()
	if err != nil || !ok {
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "invalid: imza dogrulanamadi"})
	}

	r.publishedMu.Lock()
	r.published = append(r.published, ev)
	r.publishedMu.Unlock()

	switch {
	case r.opts.RequireAuth:
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "auth-required: oturum acilmali"})
	case r.opts.RejectAll:
		return r.write(ctx, conn, writeM, []any{"OK", ev.ID, false, "blocked: test relay'i reddediyor"})
	}

	if err := r.write(ctx, conn, writeM, []any{"OK", ev.ID, true, ""}); err != nil {
		return err
	}

	ephemeral := ev.Kind >= 20000 && ev.Kind < 30000
	if ephemeral && r.opts.DropEphemeral {
		// Kabul ettik ama iletmiyoruz: "ephemeral'i dusuren relay".
		return nil
	}
	r.broadcast(ctx, ev)
	return nil
}

func (r *Relay) onReq(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, raw []json.RawMessage) error {
	if len(raw) < 3 {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "REQ eksik"})
	}
	var subID string
	if err := json.Unmarshal(raw[1], &subID); err != nil {
		return r.write(ctx, conn, writeM, []any{"NOTICE", "REQ subId okunamadi"})
	}
	var filter gonostr.Filter
	if err := json.Unmarshal(raw[2], &filter); err != nil {
		return r.write(ctx, conn, writeM, []any{"CLOSED", subID, "invalid: filtre okunamadi"})
	}

	sub := &subscription{conn: conn, subID: subID, filter: filter, writeM: writeM}
	r.mu.Lock()
	r.subs[sub] = struct{}{}
	r.mu.Unlock()

	// Ephemeral event saklamadigimiz icin gonderilecek gecmis yok.
	return r.write(ctx, conn, writeM, []any{"EOSE", subID})
}

func (r *Relay) onClose(raw []json.RawMessage) error {
	if len(raw) < 2 {
		return nil
	}
	var subID string
	if err := json.Unmarshal(raw[1], &subID); err != nil {
		return nil
	}
	r.mu.Lock()
	for sub := range r.subs {
		if sub.subID == subID {
			delete(r.subs, sub)
		}
	}
	r.mu.Unlock()
	return nil
}

func (r *Relay) dropConn(conn *websocket.Conn) {
	r.mu.Lock()
	for sub := range r.subs {
		if sub.conn == conn {
			delete(r.subs, sub)
		}
	}
	r.mu.Unlock()
}

func (r *Relay) broadcast(ctx context.Context, ev gonostr.Event) {
	r.mu.Lock()
	targets := make([]*subscription, 0, len(r.subs))
	for sub := range r.subs {
		if matches(sub.filter, ev) {
			targets = append(targets, sub)
		}
	}
	r.mu.Unlock()

	for _, sub := range targets {
		_ = r.write(ctx, sub.conn, sub.writeM, []any{"EVENT", sub.subID, ev})
	}
}

func (r *Relay) write(ctx context.Context, conn *websocket.Conn, writeM *sync.Mutex, msg []any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	writeM.Lock()
	defer writeM.Unlock()

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, data); err != nil {
		return errors.New("yazilamadi")
	}
	return nil
}

// matches NIP-01 filtresinin kullandigimiz alanlarini uygular.
func matches(f gonostr.Filter, ev gonostr.Event) bool {
	if len(f.IDs) > 0 && !contains(f.IDs, ev.ID) {
		return false
	}
	if len(f.Kinds) > 0 && !containsInt(f.Kinds, ev.Kind) {
		return false
	}
	if len(f.Authors) > 0 && !contains(f.Authors, ev.PubKey) {
		return false
	}
	if f.Since != nil && ev.CreatedAt < *f.Since {
		return false
	}
	if f.Until != nil && ev.CreatedAt > *f.Until {
		return false
	}
	for name, wanted := range f.Tags {
		if !tagMatches(ev, name, wanted) {
			return false
		}
	}
	return true
}

func tagMatches(ev gonostr.Event, name string, wanted []string) bool {
	for _, tag := range ev.Tags {
		if len(tag) < 2 || tag[0] != name {
			continue
		}
		if contains(wanted, tag[1]) {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func containsInt(haystack []int, needle int) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
