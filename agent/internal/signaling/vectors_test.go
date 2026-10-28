package signaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vectorsPath — the TS side (web/src/nostr/*.test.ts) reads the SAME
// file.
const vectorsPath = "../../../protocol/signaling-testvectors.json"

type validMsgVector struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Canonical string `json:"canonical"`
}

type invalidMsgVector struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

type boundaryVector struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Len    int    `json:"len"`
	Accept bool   `json:"accept"`
}

type sealedValidVector struct {
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	NonceHex  string `json:"nonceHex"`
	Plaintext string `json:"plaintext"`
	Payload   string `json:"payload"`
}

type sealedInvalidVector struct {
	Name    string `json:"name"`
	Secret  string `json:"secret"`
	Payload string `json:"payload"`
	Error   string `json:"error"`
}

type testVectors struct {
	Version int    `json:"version"`
	Note    string `json:"note"`
	Limits  struct {
		SIDLength    int `json:"sidLength"`
		MaxSDP       int `json:"maxSDP"`
		MaxCandidate int `json:"maxCandidate"`
		MaxMid       int `json:"maxMid"`
		MaxReason    int `json:"maxReason"`
		MaxTarget    int `json:"maxTargetName"`
	} `json:"limits"`
	TargetNamePattern string   `json:"targetNamePattern"`
	Errors            []string `json:"errors"`
	Secret            struct {
		Prefix     string `json:"prefix"`
		Salt       string `json:"salt"`
		Info       string `json:"info"`
		AAD        string `json:"aad"`
		KDF        string `json:"kdf"`
		AEAD       string `json:"aead"`
		NonceBytes int    `json:"nonceBytes"`
		Base64     string `json:"base64"`
	} `json:"secret"`
	Messages struct {
		Valid     []validMsgVector   `json:"valid"`
		Invalid   []invalidMsgVector `json:"invalid"`
		Generated []boundaryVector   `json:"generated"`
	} `json:"messages"`
	Sealed struct {
		Key struct {
			Secret string `json:"secret"`
			KeyHex string `json:"keyHex"`
		} `json:"key"`
		Valid                   []sealedValidVector   `json:"valid"`
		Invalid                 []sealedInvalidVector `json:"invalid"`
		PlainWhenSecretExpected struct {
			Secret  string `json:"secret"`
			Payload string `json:"payload"`
			Error   string `json:"error"`
		} `json:"plainWhenSecretExpected"`
		SealedWhenNoSecret struct {
			Payload string `json:"payload"`
			Error   string `json:"error"`
		} `json:"sealedWhenNoSecret"`
	} `json:"sealed"`
}

func loadVectors(t *testing.T) *testVectors {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(vectorsPath))
	if err != nil {
		t.Fatalf("the test vectors could not be read (%s): %v", vectorsPath, err)
	}
	var v testVectors
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("the test vectors could not be parsed: %v", err)
	}
	return &v
}

func codeOf(t *testing.T, err error) ErrorCode {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("a signaling.Error was expected, got: %T (%v)", err, err)
	}
	return e.Code
}

func TestVectorsMatchConstants(t *testing.T) {
	v := loadVectors(t)
	if v.Version != Version {
		t.Errorf("version: vector %d, Go %d", v.Version, Version)
	}
	if v.Limits.SIDLength != SIDLength {
		t.Errorf("sidLength: vector %d, Go %d", v.Limits.SIDLength, SIDLength)
	}
	if v.Limits.MaxSDP != MaxSDP {
		t.Errorf("maxSDP: vector %d, Go %d", v.Limits.MaxSDP, MaxSDP)
	}
	if v.Limits.MaxCandidate != MaxCandidate {
		t.Errorf("maxCandidate: vector %d, Go %d", v.Limits.MaxCandidate, MaxCandidate)
	}
	if v.Limits.MaxMid != MaxMid {
		t.Errorf("maxMid: vector %d, Go %d", v.Limits.MaxMid, MaxMid)
	}
	if v.Limits.MaxReason != MaxReason {
		t.Errorf("maxReason: vector %d, Go %d", v.Limits.MaxReason, MaxReason)
	}
	if v.Limits.MaxTarget != MaxTargetName {
		t.Errorf("maxTargetName: vector %d, Go %d", v.Limits.MaxTarget, MaxTargetName)
	}
	if v.TargetNamePattern != targetNameRe.String() {
		t.Errorf("targetNamePattern: vector %q, Go %q", v.TargetNamePattern, targetNameRe.String())
	}

	// The secret layer parameters have to match the constants in the
	// code; if one changes, the two implementations drift apart
	// silently.
	if v.Secret.Prefix != SecretPrefix {
		t.Errorf("prefix: vector %q, Go %q", v.Secret.Prefix, SecretPrefix)
	}
	if v.Secret.Salt != secretSalt {
		t.Errorf("salt: vector %q, Go %q", v.Secret.Salt, secretSalt)
	}
	if v.Secret.Info != secretInfo {
		t.Errorf("info: vector %q, Go %q", v.Secret.Info, secretInfo)
	}
	if v.Secret.AAD != secretAAD {
		t.Errorf("aad: vector %q, Go %q", v.Secret.AAD, secretAAD)
	}
	if v.Secret.NonceBytes != nonceBytes {
		t.Errorf("nonceBytes: vector %d, Go %d", v.Secret.NonceBytes, nonceBytes)
	}
}

func TestVectorsNotEmpty(t *testing.T) {
	v := loadVectors(t)
	if len(v.Messages.Valid) < 8 || len(v.Messages.Invalid) < 25 ||
		len(v.Messages.Generated) < 8 || len(v.Sealed.Valid) < 3 || len(v.Sealed.Invalid) < 4 {
		t.Fatalf("the vector file holds fewer records than expected: %d/%d/%d/%d/%d",
			len(v.Messages.Valid), len(v.Messages.Invalid), len(v.Messages.Generated),
			len(v.Sealed.Valid), len(v.Sealed.Invalid))
	}
}

func TestValidMessageVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Messages.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := Parse(vec.Text)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			// The canonical output has to be byte-for-byte the same as
			// the TS side's.
			if got := msg.Canonical(); got != vec.Canonical {
				t.Errorf("the canonical output differs:\n  got:      %s\n  expected: %s", got, vec.Canonical)
			}
			// Reparsing the canonical text has to give the same
			// message.
			again, err := Parse(msg.Canonical())
			if err != nil {
				t.Fatalf("the canonical text could not be reparsed: %v", err)
			}
			if again != msg {
				t.Errorf("the round trip broke:\n  %#v\n  %#v", again, msg)
			}
		})
	}
}

func TestValidVectorsCoverAllTypes(t *testing.T) {
	seen := map[Type]bool{}
	for _, vec := range loadVectors(t).Messages.Valid {
		msg, err := Parse(vec.Text)
		if err != nil {
			t.Fatalf("%s: %v", vec.Name, err)
		}
		seen[msg.Type] = true
	}
	for _, want := range []Type{TypeOffer, TypeAnswer, TypeCandidate, TypeBye} {
		if !seen[want] {
			t.Errorf("the type %q is not covered by the vectors", want)
		}
	}
}

func TestInvalidMessageVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Messages.Invalid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := Parse(vec.Text)
			if err == nil {
				t.Fatalf("an error was expected (%s), returned: %#v", vec.Error, msg)
			}
			if got := codeOf(t, err); string(got) != vec.Error {
				t.Errorf("error code %q, expected %q (detail: %v)", got, vec.Error, err)
			}
		})
	}
}

// The boundary cases: accepted exactly at the limit, refused one
// over.
func TestBoundaryVectors(t *testing.T) {
	const sid = "0123456789abcdef"

	for _, vec := range loadVectors(t).Messages.Generated {
		t.Run(vec.Name, func(t *testing.T) {
			filler := strings.Repeat("x", vec.Len)

			var msg Msg
			switch vec.Kind {
			case "offer-sdp":
				msg = Msg{Type: TypeOffer, SID: sid, SDP: filler}
			case "candidate-cand":
				msg = Msg{Type: TypeCandidate, SID: sid, Candidate: filler, Mid: "0"}
			case "candidate-mid":
				msg = Msg{Type: TypeCandidate, SID: sid, Candidate: "candidate:1", Mid: filler}
			case "bye-reason":
				msg = Msg{Type: TypeBye, SID: sid, Reason: filler}
			default:
				t.Fatalf("unknown boundary case: %q", vec.Kind)
			}

			err := msg.Validate()
			if vec.Accept && err != nil {
				t.Errorf("%d bytes should have been accepted: %v", vec.Len, err)
			}
			if !vec.Accept && err == nil {
				t.Errorf("%d bytes should have been refused", vec.Len)
			}

			// The parsing side has to make the same decision.
			parsed, perr := Parse(msg.Canonical())
			if vec.Accept && perr != nil {
				t.Errorf("Parse should have accepted %d bytes: %v", vec.Len, perr)
			}
			if !vec.Accept && perr == nil {
				t.Errorf("Parse should have refused %d bytes (%#v)", vec.Len, parsed)
			}
		})
	}
}

// --- the secret layer ---

func TestSealerKeyDerivationMatchesVector(t *testing.T) {
	v := loadVectors(t)
	s, err := NewSealer(v.Sealed.Key.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.KeyHex(); got != v.Sealed.Key.KeyHex {
		t.Errorf("the derived key differs:\n  got:      %s\n  expected: %s", got, v.Sealed.Key.KeyHex)
	}
}

// We HAVE TO BE ABLE TO OPEN the payloads in the vectors (they were
// produced with python).
func TestSealedValidVectorsOpen(t *testing.T) {
	for _, vec := range loadVectors(t).Sealed.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			s, err := NewSealer(vec.Secret)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Open(vec.Payload)
			if err != nil {
				t.Fatalf("Open error: %v", err)
			}
			if got != vec.Plaintext {
				t.Errorf("the plain text differs:\n  got:      %s\n  expected: %s", got, vec.Plaintext)
			}
			// It also has to parse as a message.
			if _, err := s.Decode(vec.Payload); err != nil {
				t.Errorf("Decode hata: %v", err)
			}
		})
	}
}

// Encrypting with the same nonce has to produce a payload that is
// BYTE-FOR-BYTE the one in the vector: that proves the two
// implementations use the same framing.
func TestSealedValidVectorsSealBitExact(t *testing.T) {
	for _, vec := range loadVectors(t).Sealed.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			s, err := NewSealer(vec.Secret)
			if err != nil {
				t.Fatal(err)
			}
			nonce, err := hexBytes(vec.NonceHex)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.sealWithNonce(nonce, vec.Plaintext)
			if err != nil {
				t.Fatalf("sealWithNonce error: %v", err)
			}
			if got != vec.Payload {
				t.Errorf("the payload differs:\n  got:      %s\n  expected: %s", got, vec.Payload)
			}
		})
	}
}

func TestSealedInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Sealed.Invalid {
		t.Run(vec.Name, func(t *testing.T) {
			s, err := NewSealer(vec.Secret)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := s.Open(vec.Payload); err == nil {
				t.Fatalf("an error was expected (%s), returned: %q", vec.Error, got)
			} else if code := codeOf(t, err); string(code) != vec.Error {
				t.Errorf("error code %q, expected %q", code, vec.Error)
			}
		})
	}
}

// Plain JSON arriving where a secret is expected has to be refused:
// "refuse unsealed offers".
func TestPlainRejectedWhenSecretExpected(t *testing.T) {
	v := loadVectors(t).Sealed.PlainWhenSecretExpected
	s, err := NewSealer(v.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Open(v.Payload); err == nil {
		t.Fatalf("a plain payload should have been refused, returned: %q", got)
	} else if code := codeOf(t, err); string(code) != v.Error {
		t.Errorf("error code %q, expected %q", code, v.Error)
	}
}

// A sealed payload arriving with no secret has to be refused (we cannot
// open it).
func TestSealedRejectedWhenNoSecret(t *testing.T) {
	v := loadVectors(t).Sealed.SealedWhenNoSecret
	var s *Sealer // no secret
	if got, err := s.Open(v.Payload); err == nil {
		t.Fatalf("a sealed payload should have been refused, returned: %q", got)
	} else if code := codeOf(t, err); string(code) != v.Error {
		t.Errorf("error code %q, expected %q", code, v.Error)
	}
}

func hexBytes(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		var b byte
		for j := 0; j < 2; j++ {
			c := s[i*2+j]
			var v byte
			switch {
			case c >= '0' && c <= '9':
				v = c - '0'
			case c >= 'a' && c <= 'f':
				v = c - 'a' + 10
			case c >= 'A' && c <= 'F':
				v = c - 'A' + 10
			default:
				return nil, errors.New("invalid hex")
			}
			b = b<<4 | v
		}
		out[i] = b
	}
	return out, nil
}
