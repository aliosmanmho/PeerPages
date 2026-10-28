// Package signaling implements the signaling messages carried over
// Nostr.
//
// Normative definition: protocol/signaling.md. TypeScript counterpart:
// web/src/nostr/signaling.ts. Both sides read the SAME test vectors:
// protocol/signaling-testvectors.json (produced with a third,
// independent implementation).
//
// This package knows ONLY the message format: the Nostr transport lives
// in internal/nostr, and the session rules (rate limiting, replay, clock
// skew) in the agent flow.
//
// Note: in the data channel protocol (internal/protocol) every message
// type was its own Go type; here the four types' fields overlap heavily
// (v, t, sid) and differ in only one or two, so a single struct plus a
// Type discriminator is less code and reads better.
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

// Version is the signaling version supported right now.
const Version = 1

// Limits (protocol/signaling.md).
const (
	// SIDLength is the session id's character count (16 hex = 64 bits).
	SIDLength = 16
	// MaxSDP is the largest SDP.
	MaxSDP = 65536
	// MaxCandidate is the largest ICE candidate line.
	MaxCandidate = 1024
	// MaxMid is the largest sdpMid.
	MaxMid = 32
	// MaxReason is the largest bye reason.
	MaxReason = 64
	// MaxTargetName is the longest target name (offer.target).
	MaxTargetName = 32
)

// targetNameRe is the target name format (protocol/signaling.md).
//
// It MUST be the same as nameRe in internal/target: one validates the
// name coming from the URL, the other the name coming from the CLI, and
// both must accept the same set. signaling_test.go checks separately that
// they stay in step (we do not import one from the other: this package
// knows only the message format).
var targetNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)

// ValidTargetName validates the target name format.
func ValidTargetName(name string) bool {
	return targetNameRe.MatchString(name)
}

// Type is the message type.
type Type string

// The message types (the list in BRIEF section 4).
const (
	TypeOffer     Type = "offer"
	TypeAnswer    Type = "answer"
	TypeCandidate Type = "candidate"
	TypeBye       Type = "bye"
)

// ErrorCode is a signaling error code — the same set as the data channel
// protocol's, plus bad-secret.
type ErrorCode string

// The error codes.
const (
	// CodeBadSecret means a sealed payload could not be opened, or plain
	// JSON arrived where a secret was expected.
	CodeBadSecret ErrorCode = "bad-secret"
	// CodeBadJSON means the JSON could not be read, or the top level is
	// not an object.
	CodeBadJSON ErrorCode = "bad-json"
	// CodeUnknownType means t holds an unrecognised value.
	CodeUnknownType ErrorCode = "unknown-type"
	// CodeMissingField means a required field is missing.
	CodeMissingField ErrorCode = "missing-field"
	// CodeBadField means the field is there but invalid.
	CodeBadField ErrorCode = "bad-field"
)

// Error is a signaling parse error.
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

// Msg is a signaling message.
//
// Which fields are set depends on Type:
//
//	offer         : SDP, (optional) Target
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
	// Target only means anything on an offer, and is optional: an empty
	// string means "no target given" (the agent falls back to its
	// default). When empty, the field is NOT written into the canonical
	// form at all.
	Target string
}

// NewSID generates a new session id (16 lowercase hex chars = 64
// bits).
func NewSID() (string, error) {
	var b [SIDLength / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the sid could not be generated: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidSID validates the sid format: exactly 16 lowercase hex chars.
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

// Canonical is the message's canonical serialization
// (protocol/signaling.md).
//
// It produces byte-for-byte the same output as canonicalSignaling on the
// TS side.
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

// Validate checks the field rules.
func (m Msg) Validate() error {
	if !ValidSID(m.SID) {
		return errf(CodeBadField, "sid must be exactly %d lowercase hex chars, got: %q", SIDLength, m.SID)
	}
	switch m.Type {
	case TypeOffer:
		if err := checkLen("offer.sdp", m.SDP, MaxSDP); err != nil {
			return err
		}
		if m.Target != "" && !ValidTargetName(m.Target) {
			return errf(CodeBadField, "offer.target format is invalid, got: %q", m.Target)
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
		return errf(CodeBadField, "%s must not be empty", label)
	}
	if len(value) > max {
		return errf(CodeBadField, "%s may be at most %d bytes, got: %d", label, max, len(value))
	}
	return nil
}

// Parse parses and validates canonical JSON text.
//
// For sealed payloads Open must be called first (secret.go); this
// function expects plain JSON.
func Parse(text string) (Msg, error) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if trimmed == "" {
		return Msg{}, errf(CodeBadJSON, "empty text")
	}
	if trimmed[0] != '{' {
		return Msg{}, errf(CodeBadJSON, "the top level must be a JSON object")
	}

	var o map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &o); err != nil {
		return Msg{}, errf(CodeBadJSON, "%v", err)
	}

	// t: the type discriminator. The order is defined in
	// protocol/signaling.md.
	rawT, ok := o["t"]
	if !ok {
		return Msg{}, errf(CodeMissingField, "t is missing")
	}
	var t string
	if err := json.Unmarshal(rawT, &t); err != nil {
		return Msg{}, errf(CodeBadField, "t must be a string, got: %s", rawT)
	}

	// v: the version. We look at it after t not to catch an unknown type
	// first — the order is defined: a missing v is missing-field, a wrong
	// one is bad-field.
	rawV, ok := o["v"]
	if !ok {
		return Msg{}, errf(CodeMissingField, "v is missing")
	}
	v, verr := intField(rawV, "v")
	if verr != nil {
		return Msg{}, verr
	}
	if v != Version {
		return Msg{}, errf(CodeBadField, "v must be %d, got: %d", Version, v)
	}

	sid, serr := stringField(o, "sid")
	if serr != nil {
		return Msg{}, serr
	}
	if !ValidSID(sid) {
		return Msg{}, errf(CodeBadField, "sid must be exactly %d lowercase hex chars, got: %q", SIDLength, sid)
	}

	msg := Msg{Type: Type(t), SID: sid}

	switch Type(t) {
	case TypeOffer:
		sdp, err := stringField(o, "sdp")
		if err != nil {
			return Msg{}, err
		}
		msg.SDP = sdp
		// target is optional: the field being absent entirely is valid,
		// but if present it must be a valid name (not an empty string or
		// null).
		if raw, ok := o["target"]; ok {
			name, terr := nonNullString(raw, "target")
			if terr != nil {
				return Msg{}, terr
			}
			if name == "" {
				return Msg{}, errf(CodeBadField, "offer.target must not be empty (in a target-less offer the field must be absent)")
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
		return "", errf(CodeMissingField, "%s is missing", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errf(CodeBadField, "%s must be a string, got: %s", key, raw)
	}
	return s, nil
}

// nonNullString reads raw as a string.
//
// json.Unmarshal decodes "null" into a string without error and leaves
// the value empty, so we reject null explicitly (otherwise
// "target":null would look like a target-less offer).
func nonNullString(raw json.RawMessage, label string) (string, *Error) {
	if strings.TrimSpace(string(raw)) == "null" {
		return "", errf(CodeBadField, "%s must not be null", label)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errf(CodeBadField, "%s must be a string, got: %s", label, raw)
	}
	return s, nil
}

func intField(raw json.RawMessage, label string) (int, *Error) {
	// json.Number is a string type: it accepts a JSON string too. So we
	// first check the token really is a number (we hit the same bug in
	// the ctrl parser in phase 1).
	t := strings.TrimLeft(string(raw), " \t\r\n")
	if t == "" || !(t[0] == '-' || (t[0] >= '0' && t[0] <= '9')) {
		return 0, errf(CodeBadField, "%s must be an integer, got: %s", label, raw)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, errf(CodeBadField, "%s must be an integer, got: %s", label, raw)
	}
	i, perr := strconv.ParseInt(n.String(), 10, 64)
	if perr != nil {
		return 0, errf(CodeBadField, "%s must be an integer, got: %s", label, n.String())
	}
	return int(i), nil
}
