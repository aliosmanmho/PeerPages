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

// Version is the protocol version supported right now (hello.v).
const Version = 1

// HeaderWSShim is the hint the agent gives the visitor page for the
// WebSocket shim (phase 6).
//
// NOT a ctrl message but an HTTP response header — it lives here because
// it is part of the contract on the wire: with `--inject-ws-shim` the
// agent adds it to HTML responses, and when the Service Worker sees it, it
// injects the `<script>`. That way the protocol version is not broken and
// the agent never touches the body (protocol/README.md).
const HeaderWSShim = "x-peerpages-ws-shim"

// CtrlErrorCode is a ctrl error code, the same in both languages.
type CtrlErrorCode string

const (
	// CodeBadJSON means the JSON could not be read, or the top level is
	// not an object.
	CodeBadJSON CtrlErrorCode = "bad-json"
	// CodeUnknownType means t holds an unrecognised value.
	CodeUnknownType CtrlErrorCode = "unknown-type"
	// CodeMissingField means a field required for the type is missing.
	CodeMissingField CtrlErrorCode = "missing-field"
	// CodeBadField means the field is there but its type, range or format
	// is invalid.
	CodeBadField CtrlErrorCode = "bad-field"
)

// CtrlError is a ctrl parse error.
type CtrlError struct {
	Code   CtrlErrorCode
	Detail string
}

func (e *CtrlError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// AuthMode is the hello.auth value.
type AuthMode string

// The supported auth values.
const (
	AuthNone     AuthMode = "none"
	AuthRequired AuthMode = "required"
)

// Msg is a ctrl message.
type Msg interface {
	// Type is the message type ("req", "res", ...).
	Type() string
	// Canonical is the message's canonical serialization
	// (protocol/README.md). It produces byte-for-byte the same output as
	// canonicalCtrl on the TS side.
	Canonical() string
}

// Req is an HTTP request from the visitor.
type Req struct {
	ID      uint32
	Method  string
	Path    string
	Headers map[string]string
	HasBody bool
}

// Res is the head of the HTTP response to a request.
type Res struct {
	ID      uint32
	Status  int
	Headers map[string]string
}

// End reports that the body for that id is finished.
//
// Frames and Len are mandatory: because ctrl and data are separate SCTP
// streams, End can arrive BEFORE the body frames. The receiver keeps
// listening on the data channel until it reaches these counters
// (protocol/README.md).
type End struct {
	ID     uint32
	Frames uint32
	Len    int64
}

// Abort reports that the stream for that id was cancelled.
type Abort struct {
	ID     uint32
	Reason string
}

// Ping measures liveness and latency.
type Ping struct{}

// Pong answers a Ping.
type Pong struct{}

// WSOpen is a WebSocket open request from the visitor (phase 6).
//
// The payload travels on the `data` channel (wsframe.go); this message is
// only the handshake. ID comes from the SAME id space as HTTP requests, so
// a collision is structurally impossible.
type WSOpen struct {
	ID uint32
	// Path is the ws path; the target host is NEVER derived from it
	// (BRIEF principle 1).
	Path string
	// Protos holds the requested subprotocols; may be empty.
	Protos []string
}

// WSOk is a WebSocket connection the agent accepted.
type WSOk struct {
	ID uint32
	// Proto is the subprotocol the server chose; empty when there is none.
	Proto string
}

// WSClose reports that a WebSocket session closed (in either direction).
//
// HTTP's end/abort pair is not used for ws: the close code and reason go
// straight into the visitor's onclose.
type WSClose struct {
	ID     uint32
	Code   int
	Reason string
}

// Hello goes from the agent to the visitor once the connection is up.
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

// Canonical is the canonical serialization: key order fixed per type, no
// whitespace, headers lowercase and sorted.
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

// QuoteJSON escapes a string as a JSON literal (double quotes included).
//
// It applies the SAME rules as JS JSON.stringify; the signaling layer
// (internal/signaling) also uses it to produce the same canonical form.
func QuoteJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	writeJSONString(&b, s)
	return b.String()
}

// writeJSONString applies the SAME escaping rules as JS JSON.stringify.
//
// Go's encoding/json is not used because:
//   - it escapes <, >, & by default (can be turned off with SetEscapeHTML),
//   - it escapes U+2028/U+2029, which JS does not,
//   - it writes \u0008/\u000c for 0x08 and 0x0C, where JS writes \b and \f.
//
// The canonical form must be byte-for-byte identical in both languages
// (protocol/README.md).
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
				// Invalid UTF-8: it shows up as U+FFFD on the JS side too.
				b.WriteRune(utf8.RuneError)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

var methodRe = regexp.MustCompile(`^[A-Z]+$`)

// wsProtoRe is the subprotocol name: an RFC 6455 token (may not be empty).
var wsProtoRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// WebSocket close code bounds and the codes we use.
//
// 1005/1006 are inside the range: the WebSocket protocol forbids them ON
// THE WIRE, but as a bridge we have to report an abnormal close to the
// visitor (a browser produces these codes in onclose as well).
const (
	// WSCloseMin is the smallest valid close code.
	WSCloseMin = 1000
	// WSCloseMax is the largest valid close code (4000-4999 belong to the
	// application).
	WSCloseMax = 4999
	// WSCloseReasonMax is the largest close reason (a WebSocket limit).
	WSCloseReasonMax = 123

	// WSCloseNormal is a normal close.
	WSCloseNormal = 1000
	// WSCloseGoingAway means the peer is going away (the session closed).
	WSCloseGoingAway = 1001
	// WSCloseProtocol is a protocol error (bad flags, inconsistent chunk).
	WSCloseProtocol = 1002
	// WSClosePolicy is a policy close: the path was refused or the bridge
	// is off.
	WSClosePolicy = 1008
	// WSCloseTooBig means the message exceeded the --max-ws-message limit.
	WSCloseTooBig = 1009
	// WSCloseInternal means the local server was unreachable, or an
	// unexpected error.
	WSCloseInternal = 1011
	// WSCloseTryAgainLater means the stream budget is full (--max-streams).
	WSCloseTryAgainLater = 1013
)

// ParseCtrl parses and validates text arriving on the ctrl channel.
//
// The error order is defined in protocol/README.md: bad-json ->
// missing-field (no t) -> bad-field (t is not a string) -> unknown-type ->
// type-specific checks.
func ParseCtrl(text string) (Msg, error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return nil, &CtrlError{CodeBadJSON, "empty text"}
	}
	if trimmed[0] != '{' {
		return nil, &CtrlError{CodeBadJSON, "the top level must be a JSON object"}
	}

	var o map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &o); err != nil {
		return nil, &CtrlError{CodeBadJSON, err.Error()}
	}

	rawT, ok := o["t"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, "t is missing"}
	}
	var t string
	if err := json.Unmarshal(rawT, &t); err != nil {
		return nil, &CtrlError{CodeBadField, "t must be a string, got: " + string(rawT)}
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
		return nil, &CtrlError{CodeBadField, "req.method must be an uppercase HTTP token: " + method}
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
		return nil, &CtrlError{CodeMissingField, t + ".len is missing"}
	}
	if !rawIsJSONNumber(rawLen) {
		return nil, &CtrlError{CodeBadField, t + ".len must be an integer, got: " + string(rawLen)}
	}
	var n json.Number
	if uerr := json.Unmarshal(rawLen, &n); uerr != nil {
		return nil, &CtrlError{CodeBadField, t + ".len must be an integer, got: " + string(rawLen)}
	}
	length, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return nil, &CtrlError{CodeBadField, t + ".len must be an integer, got: " + n.String()}
	}
	if length < 0 {
		return nil, &CtrlError{CodeBadField, "end.len must not be negative: " + n.String()}
	}
	// frames and len must agree: if one is 0 so is the other.
	if (frames == 0) != (length == 0) {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("end.frames=%d is inconsistent with end.len=%d", frames, length)}
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
		return nil, &CtrlError{CodeMissingField, t + ".status is missing"}
	}
	status, cerr := intFromRaw(raw, t+".status")
	if cerr != nil {
		return nil, cerr
	}
	if status < 100 || status > 599 {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("res.status must be 100-599, got: %d", status)}
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
		return nil, &CtrlError{CodeMissingField, t + ".v is missing"}
	}
	v, cerr := intFromRaw(rawV, t+".v")
	if cerr != nil {
		return nil, cerr
	}
	if v != Version {
		return nil, &CtrlError{CodeBadField, fmt.Sprintf("hello.v must be %d, got: %d", Version, v)}
	}

	rawTargets, ok := o["targets"]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + ".targets is missing"}
	}
	var targets []string
	if err := json.Unmarshal(rawTargets, &targets); err != nil {
		return nil, &CtrlError{CodeBadField, "hello.targets must be an array of strings"}
	}
	if len(targets) == 0 {
		return nil, &CtrlError{CodeBadField, "hello.targets must be an array with at least one item"}
	}
	for _, x := range targets {
		if x == "" {
			return nil, &CtrlError{CodeBadField, "hello.targets items must be non-empty strings"}
		}
	}

	auth, err := stringField(o, "auth", t)
	if err != nil {
		return nil, err
	}
	if AuthMode(auth) != AuthNone && AuthMode(auth) != AuthRequired {
		return nil, &CtrlError{CodeBadField, `hello.auth must be "none" or "required": ` + auth}
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
		return nil, &CtrlError{CodeMissingField, t + ".protos is missing"}
	}
	var protos []string
	if uerr := json.Unmarshal(rawProtos, &protos); uerr != nil {
		return nil, &CtrlError{CodeBadField, "ws-open.protos must be an array of strings"}
	}
	for _, p := range protos {
		if !wsProtoRe.MatchString(p) {
			return nil, &CtrlError{
				CodeBadField,
				"ws-open.protos items must be tokens: " + strconv.Quote(p),
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
	// An empty string is valid: the server may have chosen no subprotocol.
	proto, err := stringField(o, "proto", t)
	if err != nil {
		return nil, err
	}
	if proto != "" && !wsProtoRe.MatchString(proto) {
		return nil, &CtrlError{CodeBadField, "ws-ok.proto must be a token: " + strconv.Quote(proto)}
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
		return nil, &CtrlError{CodeMissingField, t + ".code is missing"}
	}
	code, cerr := intFromRaw(raw, t+".code")
	if cerr != nil {
		return nil, cerr
	}
	if code < WSCloseMin || code > WSCloseMax {
		return nil, &CtrlError{
			CodeBadField,
			fmt.Sprintf("ws-close.code must be %d-%d, got: %d", WSCloseMin, WSCloseMax, code),
		}
	}
	// An empty string is valid: a WebSocket close reason is optional.
	reason, err := stringField(o, "reason", t)
	if err != nil {
		return nil, err
	}
	if len(reason) > WSCloseReasonMax {
		return nil, &CtrlError{
			CodeBadField,
			fmt.Sprintf("ws-close.reason may be at most %d bytes, got: %d",
				WSCloseReasonMax, len(reason)),
		}
	}
	return WSClose{ID: id, Code: code, Reason: reason}, nil
}

// validatePath is the first-layer validation of the request path. The
// deciding layer is internal/target.Resolve (BRIEF principle 1).
func validatePath(p, t string) *CtrlError {
	if p == "" {
		return &CtrlError{CodeBadField, t + ".path must not be empty"}
	}
	if !strings.HasPrefix(p, "/") {
		return &CtrlError{CodeBadField, t + `.path must start with "/", got: ` + strconv.Quote(p)}
	}
	if strings.HasPrefix(p, "//") {
		return &CtrlError{CodeBadField, t + ".path must not be protocol-relative"}
	}
	return nil
}

// rawIsJSONNumber says whether the raw token is a JSON number.
//
// Needed because json.Number is a string type: encoding/json happily
// decodes a JSON string ("200") into a json.Number too. Without this check
// {"status":"200"} would count as valid.
func rawIsJSONNumber(raw json.RawMessage) bool {
	t := bytes.TrimLeft(raw, " \t\r\n")
	if len(t) == 0 {
		return false
	}
	return t[0] == '-' || (t[0] >= '0' && t[0] <= '9')
}

func intFromRaw(raw json.RawMessage, label string) (int, *CtrlError) {
	if !rawIsJSONNumber(raw) {
		return 0, &CtrlError{CodeBadField, label + " must be an integer, got: " + string(raw)}
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, &CtrlError{CodeBadField, label + " must be an integer, got: " + string(raw)}
	}
	v, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return 0, &CtrlError{CodeBadField, label + " must be an integer, got: " + n.String()}
	}
	return int(v), nil
}

func uint32Field(o map[string]json.RawMessage, k, t string) (uint32, error) {
	raw, ok := o[k]
	if !ok {
		return 0, &CtrlError{CodeMissingField, t + "." + k + " is missing"}
	}
	if !rawIsJSONNumber(raw) {
		return 0, &CtrlError{CodeBadField, t + "." + k + " must be a uint32, got: " + string(raw)}
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, &CtrlError{CodeBadField, t + "." + k + " must be a uint32, got: " + string(raw)}
	}
	v, perr := strconv.ParseUint(n.String(), 10, 32)
	if perr != nil {
		return 0, &CtrlError{CodeBadField, t + "." + k + " must be a uint32, got: " + n.String()}
	}
	return uint32(v), nil
}

func stringField(o map[string]json.RawMessage, k, t string) (string, error) {
	raw, ok := o[k]
	if !ok {
		return "", &CtrlError{CodeMissingField, t + "." + k + " is missing"}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", &CtrlError{CodeBadField, t + "." + k + " must be a string, got: " + string(raw)}
	}
	return s, nil
}

func nonEmptyStringField(o map[string]json.RawMessage, k, t string) (string, error) {
	s, err := stringField(o, k, t)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", &CtrlError{CodeBadField, t + "." + k + " must not be empty"}
	}
	return s, nil
}

func boolField(o map[string]json.RawMessage, k, t string) (bool, error) {
	raw, ok := o[k]
	if !ok {
		return false, &CtrlError{CodeMissingField, t + "." + k + " is missing"}
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, &CtrlError{CodeBadField, t + "." + k + " must be a boolean, got: " + string(raw)}
	}
	return v, nil
}

func headersField(o map[string]json.RawMessage, k, t string) (map[string]string, error) {
	raw, ok := o[k]
	if !ok {
		return nil, &CtrlError{CodeMissingField, t + "." + k + " is missing"}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, &CtrlError{CodeBadField, t + "." + k + " must be an object"}
	}
	out := make(map[string]string, len(m))
	for name, rawVal := range m {
		var s string
		if err := json.Unmarshal(rawVal, &s); err != nil {
			return nil, &CtrlError{CodeBadField, t + "." + k + `["` + name + `"] must be a string`}
		}
		// Header names are lowercase in the canonical form.
		out[strings.ToLower(name)] = s
	}
	return out, nil
}
