// Package signaling Nostr uzerinden tasinan signaling mesajlarini uygular.
//
// Normatif tanim: protocol/signaling.md. TypeScript karsiligi:
// web/src/nostr/signaling.ts. Iki taraf AYNI test vektorlerini okur:
// protocol/signaling-testvectors.json (ucuncu bir bagimsiz uygulama ile
// uretildi).
//
// Bu paket YALNIZCA mesaj bicimini bilir: Nostr tasimasi internal/nostr'da,
// oturum kurallari (hiz limiti, replay, saat kaymasi) ajan akisinda.
//
// Not: data channel protokolunde (internal/protocol) her mesaj tipi ayri bir
// Go tipiydi; burada dort tipin alanlari buyuk olcude ortusuyor (v, t, sid)
// ve ayrim yalnizca bir-iki alanda, bu yuzden tek struct + Type ayirici
// daha az kod ve daha okunur.
package signaling

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aliosmanmho/PeerPages/agent/internal/protocol"
)

// Version su an desteklenen signaling surumu.
const Version = 1

// Sinirlar (protocol/signaling.md).
const (
	// SIDLength oturum kimliginin karakter sayisi (16 hex = 64 bit).
	SIDLength = 16
	// MaxSDP en buyuk SDP.
	MaxSDP = 65536
	// MaxCandidate en buyuk ICE aday satiri.
	MaxCandidate = 1024
	// MaxMid en buyuk sdpMid.
	MaxMid = 32
	// MaxReason en buyuk bye nedeni.
	MaxReason = 64
	// MaxTargetName en uzun hedef adi (offer.target).
	MaxTargetName = 32
)

// targetNameRe hedef adi bicimi (protocol/signaling.md).
//
// internal/target'taki nameRe ile AYNI olmak zorunda: biri URL'den gelen adi,
// oteki CLI'dan gelen adi dogruluyor ve ikisi ayni kumeyi kabul etmeli. Ayni
// tutuldugu signaling_test.go'da ayrica dogruluyor (ikisini birbirine
// import etmiyoruz: bu paket yalnizca mesaj bicimini bilir).
var targetNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

// ValidTargetName hedef adi bicimini dogrular.
func ValidTargetName(name string) bool {
	return targetNameRe.MatchString(name)
}

// Type mesaj tipi.
type Type string

// Mesaj tipleri (README bolum 4'teki liste).
const (
	TypeOffer     Type = "offer"
	TypeAnswer    Type = "answer"
	TypeCandidate Type = "candidate"
	TypeBye       Type = "bye"
)

// ErrorCode hata kodlari — data channel protokolununkiyle ayni kume, artı
// bad-secret.
type ErrorCode string

// Hata kodlari.
const (
	// CodeBadSecret sirli payload cozulemedi ya da sir beklenirken duz JSON geldi.
	CodeBadSecret ErrorCode = "bad-secret"
	// CodeBadJSON JSON okunamadi ya da en ust duzey nesne degil.
	CodeBadJSON ErrorCode = "bad-json"
	// CodeUnknownType t taninmayan bir deger.
	CodeUnknownType ErrorCode = "unknown-type"
	// CodeMissingField zorunlu alan yok.
	CodeMissingField ErrorCode = "missing-field"
	// CodeBadField alan var ama gecersiz.
	CodeBadField ErrorCode = "bad-field"
)

// Error signaling ayristirma hatasi.
type Error struct {
	Code   ErrorCode
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func errf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Msg bir signaling mesaji.
//
// Hangi alanlarin dolu oldugu Type'a bagli:
//
//	offer         : SDP, (opsiyonel) Target
//	answer        : SDP
//	candidate     : Candidate, Mid
//	bye           : Reason
type Msg struct {
	Type      Type
	SID       string
	SDP       string
	Candidate string
	Mid       string
	Reason    string
	// Target yalnizca offer'da anlamli ve opsiyonel: bos string "hedef
	// belirtilmedi" demektir (ajan default'a duser). Kanonik bicimde bos
	// oldugunda alan HIC yazilmaz.
	Target string
}

// NewSID yeni bir oturum kimligi uretir (16 kucuk harf hex = 64 bit).
func NewSID() (string, error) {
	var b [SIDLength / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("sid uretilemedi: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidSID sid bicimini dogrular: tam 16 kucuk harf hex.
func ValidSID(sid string) bool {
	if len(sid) != SIDLength {
		return false
	}
	for i := 0; i < len(sid); i++ {
		c := sid[i]
		isDigit := c >= '0' && c <= '9'
		isLowerHex := c >= 'a' && c <= 'f'
		if !isDigit && !isLowerHex {
			return false
		}
	}
	return true
}

// Canonical mesajin kanonik serilestirmesi (protocol/signaling.md).
//
// TS tarafindaki canonicalSignaling ile bayt bayt ayni cikti uretir.
func (m Msg) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"v":`)
	b.WriteString(strconv.Itoa(Version))
	b.WriteString(`,"t":`)
	b.WriteString(protocol.QuoteJSON(string(m.Type)))
	b.WriteString(`,"sid":`)
	b.WriteString(protocol.QuoteJSON(m.SID))

	switch m.Type {
	case TypeOffer:
		b.WriteString(`,"sdp":`)
		b.WriteString(protocol.QuoteJSON(m.SDP))
		if m.Target != "" {
			b.WriteString(`,"target":`)
			b.WriteString(protocol.QuoteJSON(m.Target))
		}
	case TypeAnswer:
		b.WriteString(`,"sdp":`)
		b.WriteString(protocol.QuoteJSON(m.SDP))
	case TypeCandidate:
		b.WriteString(`,"cand":`)
		b.WriteString(protocol.QuoteJSON(m.Candidate))
		b.WriteString(`,"mid":`)
		b.WriteString(protocol.QuoteJSON(m.Mid))
	case TypeBye:
		b.WriteString(`,"reason":`)
		b.WriteString(protocol.QuoteJSON(m.Reason))
	}
	b.WriteString("}")
	return b.String()
}

// Validate alan kurallarini dogrular.
func (m Msg) Validate() error {
	if !ValidSID(m.SID) {
		return errf(CodeBadField, "sid tam %d kucuk harf hex olmali, verilen: %q", SIDLength, m.SID)
	}
	switch m.Type {
	case TypeOffer:
		if err := checkLen("offer.sdp", m.SDP, MaxSDP); err != nil {
			return err
		}
		if m.Target != "" && !ValidTargetName(m.Target) {
			return errf(CodeBadField, "offer.target bicimi gecersiz, verilen: %q", m.Target)
		}
		return nil
	case TypeAnswer:
		return checkLen("answer.sdp", m.SDP, MaxSDP)
	case TypeCandidate:
		if err := checkLen("candidate.cand", m.Candidate, MaxCandidate); err != nil {
			return err
		}
		return checkLen("candidate.mid", m.Mid, MaxMid)
	case TypeBye:
		return checkLen("bye.reason", m.Reason, MaxReason)
	default:
		return errf(CodeUnknownType, "%q", string(m.Type))
	}
}

func checkLen(label, value string, max int) error {
	if value == "" {
		return errf(CodeBadField, "%s bos olamaz", label)
	}
	if len(value) > max {
		return errf(CodeBadField, "%s en fazla %d bayt olabilir, verilen: %d", label, max, len(value))
	}
	return nil
}

// Parse kanonik JSON metnini ayristirip dogrular.
//
// Sirli payload'lar icin once Open cagrilmali (secret.go); bu fonksiyon duz
// JSON bekler.
func Parse(text string) (Msg, error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return Msg{}, errf(CodeBadJSON, "bos metin")
	}
	if trimmed[0] != '{' {
		return Msg{}, errf(CodeBadJSON, "en ust duzey bir JSON nesnesi olmali")
	}

	var o map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &o); err != nil {
		return Msg{}, errf(CodeBadJSON, "%v", err)
	}

	// t: tip ayirici. Sira protocol/signaling.md'de tanimli.
	rawT, ok := o["t"]
	if !ok {
		return Msg{}, errf(CodeMissingField, "t yok")
	}
	var t string
	if err := json.Unmarshal(rawT, &t); err != nil {
		return Msg{}, errf(CodeBadField, "t string olmali, verilen: %s", rawT)
	}

	// v: surum. t'den sonra bakiyoruz ki bilinmeyen tip once yakalansin mi
	// diye degil — sira tanimli: v eksikse missing-field, yanlissa bad-field.
	rawV, ok := o["v"]
	if !ok {
		return Msg{}, errf(CodeMissingField, "v yok")
	}
	v, verr := intField(rawV, "v")
	if verr != nil {
		return Msg{}, verr
	}
	if v != Version {
		return Msg{}, errf(CodeBadField, "v %d olmali, verilen: %d", Version, v)
	}

	sid, serr := stringField(o, "sid")
	if serr != nil {
		return Msg{}, serr
	}
	if !ValidSID(sid) {
		return Msg{}, errf(CodeBadField, "sid tam %d kucuk harf hex olmali, verilen: %q", SIDLength, sid)
	}

	msg := Msg{Type: Type(t), SID: sid}

	switch Type(t) {
	case TypeOffer:
		sdp, err := stringField(o, "sdp")
		if err != nil {
			return Msg{}, err
		}
		msg.SDP = sdp
		// target opsiyonel: alanin HIC olmamasi gecerli, ama varsa gecerli
		// bir ad olmak zorunda (bos string ya da null degil).
		if raw, ok := o["target"]; ok {
			name, terr := nonNullString(raw, "target")
			if terr != nil {
				return Msg{}, terr
			}
			if name == "" {
				return Msg{}, errf(CodeBadField, "offer.target bos olamaz (hedefsiz offer'da alan hic olmamali)")
			}
			msg.Target = name
		}
	case TypeAnswer:
		sdp, err := stringField(o, "sdp")
		if err != nil {
			return Msg{}, err
		}
		msg.SDP = sdp
	case TypeCandidate:
		cand, err := stringField(o, "cand")
		if err != nil {
			return Msg{}, err
		}
		mid, err := stringField(o, "mid")
		if err != nil {
			return Msg{}, err
		}
		msg.Candidate = cand
		msg.Mid = mid
	case TypeBye:
		reason, err := stringField(o, "reason")
		if err != nil {
			return Msg{}, err
		}
		msg.Reason = reason
	default:
		return Msg{}, errf(CodeUnknownType, "%q", t)
	}

	if err := msg.Validate(); err != nil {
		return Msg{}, err
	}
	return msg, nil
}

func stringField(o map[string]json.RawMessage, key string) (string, error) {
	raw, ok := o[key]
	if !ok {
		return "", errf(CodeMissingField, "%s yok", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errf(CodeBadField, "%s string olmali, verilen: %s", key, raw)
	}
	return s, nil
}

// nonNullString raw'i string olarak okur.
//
// json.Unmarshal "null" degerini stringe hatasiz cozer ve degeri bos birakir;
// bu yuzden null'i acikca reddediyoruz (aksi halde "target":null hedefsiz
// offer gibi gorunurdu).
func nonNullString(raw json.RawMessage, label string) (string, *Error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return "", errf(CodeBadField, "%s null olamaz", label)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errf(CodeBadField, "%s string olmali, verilen: %s", label, raw)
	}
	return s, nil
}

func intField(raw json.RawMessage, label string) (int, *Error) {
	// json.Number bir string tipi: JSON string'i de kabul eder. Bu yuzden
	// token'in gercekten sayi oldugunu once kontrol ediyoruz (ayni hatayi
	// Faz 1'de ctrl ayristiricisinda yasadik).
	t := strings.TrimLeft(string(raw), " \t\r\n")
	if t == "" || !(t[0] == '-' || (t[0] >= '0' && t[0] <= '9')) {
		return 0, errf(CodeBadField, "%s tamsayi olmali, verilen: %s", label, raw)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, errf(CodeBadField, "%s tamsayi olmali, verilen: %s", label, raw)
	}
	i, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return 0, errf(CodeBadField, "%s tamsayi olmali, verilen: %s", label, n.String())
	}
	return int(i), nil
}
