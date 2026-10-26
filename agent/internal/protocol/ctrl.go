package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Version su an desteklenen protokol surumu (hello.v).
const Version = 1

// CtrlErrorCode iki dilde ayni olan ctrl hata kodlari.
type CtrlErrorCode string

const (
	// CodeBadJSON JSON okunamadi ya da en ust duzey bir nesne degil.
	CodeBadJSON CtrlErrorCode = "bad-json"
	// CodeUnknownType t taninmayan bir deger.
	CodeUnknownType CtrlErrorCode = "unknown-type"
	// CodeMissingField tip icin zorunlu bir alan yok.
	CodeMissingField CtrlErrorCode = "missing-field"
	// CodeBadField alan var ama tipi/araligi/bicimi gecersiz.
	CodeBadField CtrlErrorCode = "bad-field"
)

// CtrlError ctrl ayristirma hatasi.
type CtrlError struct {
	Code   CtrlErrorCode
	Detail string
}

func (e *CtrlError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// AuthMode hello.auth degeri.
type AuthMode string

// Desteklenen auth degerleri.
const (
	AuthNone     AuthMode = "none"
	AuthRequired AuthMode = "required"
)

// Msg bir ctrl mesaji.
type Msg interface {
	// Type mesaj tipi ("req", "res", ...).
	Type() string
	// Canonical mesajin kanonik serilestirmesi (protocol/README.md).
	// TS tarafindaki canonicalCtrl ile bayt bayt ayni cikti uretir.
	Canonical() string
}

// Req ziyaretciden gelen HTTP istegi.
type Req struct {
	ID      uint32
	Method  string
	Path    string
	Headers map[string]string
	HasBody bool
}

// Res istege verilen HTTP cevabinin basligi.
type Res struct {
	ID      uint32
	Status  int
	Headers map[string]string
}

// End o id icin govdenin bittigini bildirir.
type End struct{ ID uint32 }

// Abort o id icin akisin iptal edildigini bildirir.
type Abort struct {
	ID     uint32
	Reason string
}

// Ping canlilik/gecikme olcumu.
type Ping struct{}

// Pong Ping cevabi.
type Pong struct{}

// Hello baglanti kurulunca ajandan ziyaretciye gider.
type Hello struct {
	V       int
	Targets []string
	Auth    AuthMode
}

func (Req) Type() string   { return "req" }
func (Res) Type() string   { return "res" }
func (End) Type() string   { return "end" }
func (Abort) Type() string { return "abort" }
func (Ping) Type() string  { return "ping" }
func (Pong) Type() string  { return "pong" }
func (Hello) Type() string { return "hello" }

// Canonical kanonik serilestirme — anahtar sirasi tipe gore sabit, bosluk yok,
// headers kucuk harf ve sozluk sirali.
func (m Req) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"req","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"method":`)
	writeJSONString(&b, m.Method)
	b.WriteString(`,"path":`)
	writeJSONString(&b, m.Path)
	b.WriteString(`,"headers":`)
	writeCanonicalHeaders(&b, m.Headers)
	b.WriteString(`,"hasBody":`)
	b.WriteString(strconv.FormatBool(m.HasBody))
	b.WriteString("}")
	return b.String()
}

// Canonical kanonik serilestirme.
func (m Res) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"res","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"status":`)
	b.WriteString(strconv.Itoa(m.Status))
	b.WriteString(`,"headers":`)
	writeCanonicalHeaders(&b, m.Headers)
	b.WriteString("}")
	return b.String()
}

// Canonical kanonik serilestirme.
func (m End) Canonical() string {
	return `{"t":"end","id":` + strconv.FormatUint(uint64(m.ID), 10) + `}`
}

// Canonical kanonik serilestirme.
func (m Abort) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"abort","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"reason":`)
	writeJSONString(&b, m.Reason)
	b.WriteString("}")
	return b.String()
}

// Canonical kanonik serilestirme.
func (Ping) Canonical() string { return `{"t":"ping"}` }

// Canonical kanonik serilestirme.
func (Pong) Canonical() string { return `{"t":"pong"}` }

// Canonical kanonik serilestirme.
func (m Hello) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"hello","v":`)
	b.WriteString(strconv.Itoa(m.V))
	b.WriteString(`,"targets":[`)
	for i, t := range m.Targets {
		if i > 0 {
			b.WriteString(",")
		}
		writeJSONString(&b, t)
	}
	b.WriteString(`],"auth":`)
	writeJSONString(&b, string(m.Auth))
	b.WriteString("}")
	return b.String()
}

func writeCanonicalHeaders(b *strings.Builder, h map[string]string) {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		writeJSONString(b, k)
		b.WriteString(":")
		writeJSONString(b, h[k])
	}
	b.WriteString("}")
}

// writeJSONString JS'in JSON.stringify'i ile AYNI kacis kurallarini uygular.
//
// Go'nun encoding/json'u kullanilmiyor cunku:
//   - varsayilan olarak <, >, & kacirir (SetEscapeHTML ile kapatilabilir),
//   - U+2028/U+2029'u kacirir, JS kacirmaz,
//   - 0x08 ve 0x0C icin \u0008/\u000c yazar, JS \b ve \f yazar.
//
// Kanonik bicim iki dilde bayt bayt ayni olmak zorunda (protocol/README.md).
func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			switch {
			case r < 0x20:
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			case r == utf8.RuneError:
				// Gecersiz UTF-8: JS tarafinda da U+FFFD olarak gorunur.
				b.WriteRune(utf8.RuneError)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

var methodRe = regexp.MustCompile(`^[A-Z]+$`)

// ParseCtrl ctrl kanalindan gelen metni ayristirip dogrular.
//
// Hata sirasi protocol/README.md'de tanimli: bad-json -> missing-field (t yok)
// -> bad-field (t string degil) -> unknown-type -> tipe ozgu kontroller.
func ParseCtrl(text string) (Msg, error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return nil, &CtrlError{CodeBadJSON, "bos metin"}
	}
	if trimmed[0] != '{' {
		return nil, &CtrlError{CodeBadJSON, "en ust duzey bir JSON nesnesi olmali"}
	}

	var o map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &o); err != nil {
		return nil, &CtrlError{CodeBadJSON, err.Error()}
	}

	rawT, ok := o["t"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, "t yok"}
	}
	var t string
	if err := json.Unmarshal(rawT, &t); err != nil {
		return nil, &CtrlError{CodeBadField, "t string olmali, verilen: " + string(rawT)}
	}

	switch t {
	case "req":
		return parseReq(o, t)
	case "res":
		return parseRes(o, t)
	case "end":
		id, err := uint32Field(o, "id", t)
		if err != nil {
			return nil, err
		}
		return End{ID: id}, nil
	case "abort":
		id, err := uint32Field(o, "id", t)
		if err != nil {
			return nil, err
		}
		reason, err := nonEmptyStringField(o, "reason", t)
		if err != nil {
			return nil, err
		}
		return Abort{ID: id, Reason: reason}, nil
	case "ping":
		return Ping{}, nil
	case "pong":
		return Pong{}, nil
	case "hello":
		return parseHello(o, t)
	default:
		return nil, &CtrlError{CodeUnknownType, strconv.Quote(t)}
	}
}

func parseReq(o map[string]json.RawMessage, t string) (Msg, error) {
	method, err := nonEmptyStringField(o, "method", t)
	if err != nil {
		return nil, err
	}
	if !methodRe.MatchString(method) {
		return nil, &CtrlError{CodeBadField, "req.method buyuk harf HTTP token'i olmali: " + method}
	}
	id, err := uint32Field(o, "id", t)
	if err != nil {
		return nil, err
	}
	path, err := stringField(o, "path", t)
	if err != nil {
		return nil, err
	}
	if cerr := validatePath(path, t); cerr != nil {
		return nil, cerr
	}
	headers, err := headersField(o, "headers", t)
	if err != nil {
		return nil, err
	}
	hasBody, err := boolField(o, "hasBody", t)
	if err != nil {
		return nil, err
	}
	return Req{ID: id, Method: method, Path: path, Headers: headers, HasBody: hasBody}, nil
}

func parseRes(o map[string]json.RawMessage, t string) (Msg, error) {
	id, err := uint32Field(o, "id", t)
	if err != nil {
		return nil, err
	}
	raw, ok := o["status"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".status yok"}
	}
	status, cerr := intFromRaw(raw, t+".status")
	if cerr != nil {
		return nil, cerr
	}
	if status < 100 || status > 599 {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("res.status 100…599 olmali, verilen: %d", status)}
	}
	headers, err := headersField(o, "headers", t)
	if err != nil {
		return nil, err
	}
	return Res{ID: id, Status: status, Headers: headers}, nil
}

func parseHello(o map[string]json.RawMessage, t string) (Msg, error) {
	rawV, ok := o["v"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".v yok"}
	}
	v, cerr := intFromRaw(rawV, t+".v")
	if cerr != nil {
		return nil, cerr
	}
	if v != Version {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("hello.v %d olmali, verilen: %d", Version, v)}
	}

	rawTargets, ok := o["targets"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".targets yok"}
	}
	var targets []string
	if err := json.Unmarshal(rawTargets, &targets); err != nil {
		return nil, &CtrlError{CodeBadField, "hello.targets string dizisi olmali"}
	}
	if len(targets) == 0 {
		return nil, &CtrlError{CodeBadField, "hello.targets en az bir elemanli dizi olmali"}
	}
	for _, x := range targets {
		if x == "" {
			return nil, &CtrlError{CodeBadField, "hello.targets elemanlari bos olmayan string olmali"}
		}
	}

	auth, err := stringField(o, "auth", t)
	if err != nil {
		return nil, err
	}
	if AuthMode(auth) != AuthNone && AuthMode(auth) != AuthRequired {
		return nil, &CtrlError{CodeBadField, `hello.auth "none" ya da "required" olmali: ` + auth}
	}
	return Hello{V: v, Targets: targets, Auth: AuthMode(auth)}, nil
}

// validatePath istek yolunun ilk katman dogrulamasi. Belirleyici katman
// internal/target.Resolve (README ilke 1).
func validatePath(p, t string) *CtrlError {
	if p == "" {
		return &CtrlError{CodeBadField, t + ".path bos olamaz"}
	}
	if !strings.HasPrefix(p, "/") {
		return &CtrlError{CodeBadField, t + `.path "/" ile baslamali, verilen: ` + strconv.Quote(p)}
	}
	if strings.HasPrefix(p, "//") {
		return &CtrlError{CodeBadField, t + ".path protokol-goreli olamaz"}
	}
	return nil
}

// rawIsJSONNumber raw token'in JSON sayisi olup olmadigini soyler.
//
// Gerekli cunku json.Number bir string tipi: encoding/json, JSON string'i
// ("200") de json.Number'a sorunsuz cozer. Bu kontrol olmadan
// {"status":"200"} gecerli sayilirdi.
func rawIsJSONNumber(raw json.RawMessage) bool {
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 {
		return false
	}
	return t[0] == '-' || (t[0] >= '0' && t[0] <= '9')
}

func intFromRaw(raw json.RawMessage, label string) (int, *CtrlError) {
	if !rawIsJSONNumber(raw) {
		return 0, &CtrlError{CodeBadField, label + " tamsayi olmali, verilen: " + string(raw)}
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, &CtrlError{CodeBadField, label + " tamsayi olmali, verilen: " + string(raw)}
	}
	v, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return 0, &CtrlError{CodeBadField, label + " tamsayi olmali, verilen: " + n.String()}
	}
	return int(v), nil
}

func uint32Field(o map[string]json.RawMessage, k, t string) (uint32, error) {
	raw, ok := o[k]
	if !ok {
		return 0, &CtrlError{CodeMissingField, t + "." + k + " yok"}
	}
	if !rawIsJSONNumber(raw) {
		return 0, &CtrlError{CodeBadField, t + "." + k + " uint32 olmali, verilen: " + string(raw)}
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, &CtrlError{CodeBadField, t + "." + k + " uint32 olmali, verilen: " + string(raw)}
	}
	v, perr := strconv.ParseUint(n.String(), 10, 32)
	if perr != nil {
		return 0, &CtrlError{CodeBadField, t + "." + k + " uint32 olmali, verilen: " + n.String()}
	}
	return uint32(v), nil
}

func stringField(o map[string]json.RawMessage, k, t string) (string, error) {
	raw, ok := o[k]
	if !ok {
		return "", &CtrlError{CodeMissingField, t + "." + k + " yok"}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", &CtrlError{CodeBadField, t + "." + k + " string olmali, verilen: " + string(raw)}
	}
	return s, nil
}

func nonEmptyStringField(o map[string]json.RawMessage, k, t string) (string, error) {
	s, err := stringField(o, k, t)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", &CtrlError{CodeBadField, t + "." + k + " bos olamaz"}
	}
	return s, nil
}

func boolField(o map[string]json.RawMessage, k, t string) (bool, error) {
	raw, ok := o[k]
	if !ok {
		return false, &CtrlError{CodeMissingField, t + "." + k + " yok"}
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, &CtrlError{CodeBadField, t + "." + k + " boolean olmali, verilen: " + string(raw)}
	}
	return v, nil
}

func headersField(o map[string]json.RawMessage, k, t string) (map[string]string, error) {
	raw, ok := o[k]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + "." + k + " yok"}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &CtrlError{CodeBadField, t + "." + k + " nesne olmali"}
	}
	out := make(map[string]string, len(m))
	for name, rawVal := range m {
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			return nil, &CtrlError{CodeBadField, t + "." + k + `["` + name + `"] string olmali`}
		}
		// Baslik adlari kanonik bicimde kucuk harf.
		out[strings.ToLower(name)] = s
	}
	return out, nil
}
