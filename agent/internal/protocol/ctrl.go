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

// HeaderWSShim ajanin WebSocket shim'i icin ziyaretci sayfasina verdigi
// isaret (Faz 6).
//
// ctrl mesaji DEGIL, bir HTTP cevap basligi — ama tel uzerindeki sozlesmenin
// parcasi oldugu icin burada: `--inject-ws-shim` verildiginde ajan HTML
// cevaplarina bunu ekler, Service Worker gorunce `<script>` enjekte eder.
// Boylece protokol surumu kirilmiyor ve ajan govdeye dokunmuyor
// (protocol/README.md).
const HeaderWSShim = "x-peerpages-ws-shim"

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
//
// Frames ve Len zorunlu: ctrl ve data ayri SCTP stream'leri oldugundan End,
// govde cercevelerinden ONCE varabilir. Alici bu sayaclara ulasana kadar data
// kanalini dinlemeye devam eder (protocol/README.md).
type End struct {
	ID     uint32
	Frames uint32
	Len    int64
}

// Abort o id icin akisin iptal edildigini bildirir.
type Abort struct {
	ID     uint32
	Reason string
}

// Ping canlilik/gecikme olcumu.
type Ping struct{}

// Pong Ping cevabi.
type Pong struct{}

// WSOpen ziyaretciden gelen WebSocket acma istegi (Faz 6).
//
// Yuk `data` kanalindan gider (wsframe.go); bu mesaj yalnizca el sikismadir.
// ID, HTTP istekleriyle AYNI id uzayindan gelir, yani cakisma yapisal olarak
// imkansiz.
type WSOpen struct {
	ID uint32
	// Path ws yolu; hedef host ASLA buradan turetilmez (README ilke 1).
	Path string
	// Protos istenen alt protokoller; bos olabilir.
	Protos []string
}

// WSOk ajanin kabul ettigi WebSocket baglantisi.
type WSOk struct {
	ID uint32
	// Proto sunucunun sectigi alt protokol; yoksa bos string.
	Proto string
}

// WSClose WebSocket oturumunun kapandigini bildirir (iki yonde de).
//
// HTTP'nin end/abort ikilisi ws icin kullanilmaz: kapanisin kodu ve sebebi
// ziyaretcinin onclose'una dogrudan gecer.
type WSClose struct {
	ID     uint32
	Code   int
	Reason string
}

// Hello baglanti kurulunca ajandan ziyaretciye gider.
type Hello struct {
	V       int
	Targets []string
	Auth    AuthMode
}

func (Req) Type() string     { return "req" }
func (Res) Type() string     { return "res" }
func (End) Type() string     { return "end" }
func (Abort) Type() string   { return "abort" }
func (Ping) Type() string    { return "ping" }
func (Pong) Type() string    { return "pong" }
func (Hello) Type() string   { return "hello" }
func (WSOpen) Type() string  { return "ws-open" }
func (WSOk) Type() string    { return "ws-ok" }
func (WSClose) Type() string { return "ws-close" }

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
	return `{"t":"end","id":` + strconv.FormatUint(uint64(m.ID), 10) +
		`,"frames":` + strconv.FormatUint(uint64(m.Frames), 10) +
		`,"len":` + strconv.FormatInt(m.Len, 10) + `}`
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

// Canonical kanonik serilestirme.
func (m WSOpen) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"ws-open","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"path":`)
	writeJSONString(&b, m.Path)
	b.WriteString(`,"protos":[`)
	for i, p := range m.Protos {
		if i > 0 {
			b.WriteString(",")
		}
		writeJSONString(&b, p)
	}
	b.WriteString("]}")
	return b.String()
}

// Canonical kanonik serilestirme.
func (m WSOk) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"ws-ok","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"proto":`)
	writeJSONString(&b, m.Proto)
	b.WriteString("}")
	return b.String()
}

// Canonical kanonik serilestirme.
func (m WSClose) Canonical() string {
	var b strings.Builder
	b.WriteString(`{"t":"ws-close","id":`)
	b.WriteString(strconv.FormatUint(uint64(m.ID), 10))
	b.WriteString(`,"code":`)
	b.WriteString(strconv.Itoa(m.Code))
	b.WriteString(`,"reason":`)
	writeJSONString(&b, m.Reason)
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

// QuoteJSON bir stringi JSON literali olarak kacirir (cift tirnaklar dahil).
//
// JS'in JSON.stringify'i ile AYNI kurallari uygular; signaling katmani da
// (internal/signaling) ayni kanonik bicimi uretmek icin bunu kullanir.
func QuoteJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	writeJSONString(&b, s)
	return b.String()
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

// wsProtoRe alt protokol adi: RFC 6455 token'i (bos olamaz).
var wsProtoRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// WebSocket kapanis kodu sinirlari ve kullandigimiz kodlar.
//
// 1005/1006 araligin icinde: WebSocket protokolu bunlari TELDE yasaklar ama
// bir kopru olarak anormal kapanisi ziyaretciye bildirmemiz gerekiyor
// (tarayici da onclose'ta bu kodlari uretir).
const (
	// WSCloseMin en kucuk gecerli kapanis kodu.
	WSCloseMin = 1000
	// WSCloseMax en buyuk gecerli kapanis kodu (4000-4999 uygulamaya ait).
	WSCloseMax = 4999
	// WSCloseReasonMax kapanis sebebinin en buyuk boyu (WebSocket siniri).
	WSCloseReasonMax = 123

	// WSCloseNormal normal kapanis.
	WSCloseNormal = 1000
	// WSCloseGoingAway taraf gidiyor (oturum kapandi).
	WSCloseGoingAway = 1001
	// WSCloseProtocol protokol hatasi (bozuk bayrak, tutarsiz parca).
	WSCloseProtocol = 1002
	// WSClosePolicy politika: yol reddedildi ya da kopru kapali.
	WSClosePolicy = 1008
	// WSCloseTooBig mesaj --max-ws-message sinirini asti.
	WSCloseTooBig = 1009
	// WSCloseInternal yerel sunucuya baglanilamadi ya da beklenmeyen hata.
	WSCloseInternal = 1011
	// WSCloseTryAgainLater akis butcesi dolu (--max-streams).
	WSCloseTryAgainLater = 1013
)

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
		return parseEnd(o, t)
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
	case "ws-open":
		return parseWSOpen(o, t)
	case "ws-ok":
		return parseWSOk(o, t)
	case "ws-close":
		return parseWSClose(o, t)
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

func parseEnd(o map[string]json.RawMessage, t string) (Msg, error) {
	id, err := uint32Field(o, "id", t)
	if err != nil {
		return nil, err
	}
	frames, err := uint32Field(o, "frames", t)
	if err != nil {
		return nil, err
	}
	rawLen, ok := o["len"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".len yok"}
	}
	if !rawIsJSONNumber(rawLen) {
		return nil, &CtrlError{CodeBadField, t + ".len tamsayi olmali, verilen: " + string(rawLen)}
	}
	var n json.Number
	if uerr := json.Unmarshal(rawLen, &n); uerr != nil {
		return nil, &CtrlError{CodeBadField, t + ".len tamsayi olmali, verilen: " + string(rawLen)}
	}
	length, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return nil, &CtrlError{CodeBadField, t + ".len tamsayi olmali, verilen: " + n.String()}
	}
	if length < 0 {
		return nil, &CtrlError{CodeBadField, "end.len negatif olamaz: " + n.String()}
	}
	// frames ve len tutarli olmali: biri 0 ise digeri de 0.
	if (frames == 0) != (length == 0) {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("end.frames=%d ile end.len=%d tutarsiz", frames, length)}
	}
	return End{ID: id, Frames: frames, Len: length}, nil
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

func parseWSOpen(o map[string]json.RawMessage, t string) (Msg, error) {
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
	rawProtos, ok := o["protos"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".protos yok"}
	}
	var protos []string
	if uerr := json.Unmarshal(rawProtos, &protos); uerr != nil {
		return nil, &CtrlError{CodeBadField, "ws-open.protos string dizisi olmali"}
	}
	for _, p := range protos {
		if !wsProtoRe.MatchString(p) {
			return nil, &CtrlError{
				CodeBadField,
				"ws-open.protos elemanlari token olmali: " + strconv.Quote(p),
			}
		}
	}
	return WSOpen{ID: id, Path: path, Protos: protos}, nil
}

func parseWSOk(o map[string]json.RawMessage, t string) (Msg, error) {
	id, err := uint32Field(o, "id", t)
	if err != nil {
		return nil, err
	}
	// Bos string gecerli: sunucu alt protokol secmemis olabilir.
	proto, err := stringField(o, "proto", t)
	if err != nil {
		return nil, err
	}
	if proto != "" && !wsProtoRe.MatchString(proto) {
		return nil, &CtrlError{CodeBadField, "ws-ok.proto token olmali: " + strconv.Quote(proto)}
	}
	return WSOk{ID: id, Proto: proto}, nil
}

func parseWSClose(o map[string]json.RawMessage, t string) (Msg, error) {
	id, err := uint32Field(o, "id", t)
	if err != nil {
		return nil, err
	}
	raw, ok := o["code"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".code yok"}
	}
	code, cerr := intFromRaw(raw, t+".code")
	if cerr != nil {
		return nil, cerr
	}
	if code < WSCloseMin || code > WSCloseMax {
		return nil, &CtrlError{
			CodeBadField,
			fmt.Sprintf("ws-close.code %d…%d olmali, verilen: %d", WSCloseMin, WSCloseMax, code),
		}
	}
	// Bos string gecerli: WebSocket kapanis sebebi opsiyoneldir.
	reason, err := stringField(o, "reason", t)
	if err != nil {
		return nil, err
	}
	if len(reason) > WSCloseReasonMax {
		return nil, &CtrlError{
			CodeBadField,
			fmt.Sprintf("ws-close.reason en fazla %d bayt olmali, verilen: %d",
				WSCloseReasonMax, len(reason)),
		}
	}
	return WSClose{ID: id, Code: code, Reason: reason}, nil
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
