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

// vectorsPath — TS tarafi (web/src/nostr/*.test.ts) AYNI dosyayi okur.
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
	} `json:"limits"`
	Errors []string `json:"errors"`
	Secret struct {
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
		t.Fatalf("test vektorleri okunamadi (%s): %v", vectorsPath, err)
	}
	var v testVectors
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("test vektorleri ayristirilamadi: %v", err)
	}
	return &v
}

func codeOf(t *testing.T, err error) ErrorCode {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("signaling.Error beklenirdi, geldi: %T (%v)", err, err)
	}
	return e.Code
}

func TestVectorsMatchConstants(t *testing.T) {
	v := loadVectors(t)
	if v.Version != Version {
		t.Errorf("version: vektor %d, Go %d", v.Version, Version)
	}
	if v.Limits.SIDLength != SIDLength {
		t.Errorf("sidLength: vektor %d, Go %d", v.Limits.SIDLength, SIDLength)
	}
	if v.Limits.MaxSDP != MaxSDP {
		t.Errorf("maxSDP: vektor %d, Go %d", v.Limits.MaxSDP, MaxSDP)
	}
	if v.Limits.MaxCandidate != MaxCandidate {
		t.Errorf("maxCandidate: vektor %d, Go %d", v.Limits.MaxCandidate, MaxCandidate)
	}
	if v.Limits.MaxMid != MaxMid {
		t.Errorf("maxMid: vektor %d, Go %d", v.Limits.MaxMid, MaxMid)
	}
	if v.Limits.MaxReason != MaxReason {
		t.Errorf("maxReason: vektor %d, Go %d", v.Limits.MaxReason, MaxReason)
	}

	// Sir katmani parametreleri koddaki sabitlerle ayni olmali; biri degisirse
	// iki uygulama sessizce birbirinden ayrilir.
	if v.Secret.Prefix != SecretPrefix {
		t.Errorf("prefix: vektor %q, Go %q", v.Secret.Prefix, SecretPrefix)
	}
	if v.Secret.Salt != secretSalt {
		t.Errorf("salt: vektor %q, Go %q", v.Secret.Salt, secretSalt)
	}
	if v.Secret.Info != secretInfo {
		t.Errorf("info: vektor %q, Go %q", v.Secret.Info, secretInfo)
	}
	if v.Secret.AAD != secretAAD {
		t.Errorf("aad: vektor %q, Go %q", v.Secret.AAD, secretAAD)
	}
	if v.Secret.NonceBytes != nonceBytes {
		t.Errorf("nonceBytes: vektor %d, Go %d", v.Secret.NonceBytes, nonceBytes)
	}
}

func TestVectorsNotEmpty(t *testing.T) {
	v := loadVectors(t)
	if len(v.Messages.Valid) < 8 || len(v.Messages.Invalid) < 25 ||
		len(v.Messages.Generated) < 8 || len(v.Sealed.Valid) < 3 || len(v.Sealed.Invalid) < 4 {
		t.Fatalf("vektor dosyasi beklenenden az kayit iceriyor: %d/%d/%d/%d/%d",
			len(v.Messages.Valid), len(v.Messages.Invalid), len(v.Messages.Generated),
			len(v.Sealed.Valid), len(v.Sealed.Invalid))
	}
}

func TestValidMessageVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Messages.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := Parse(vec.Text)
			if err != nil {
				t.Fatalf("Parse hata: %v", err)
			}
			// Kanonik cikti TS tarafiyla bayt bayt ayni olmali.
			if got := msg.Canonical(); got != vec.Canonical {
				t.Errorf("kanonik cikti farkli:\n  geldi:    %s\n  beklenen: %s", got, vec.Canonical)
			}
			// Kanonik metin yeniden ayristirildiginda ayni mesaji vermeli.
			again, err := Parse(msg.Canonical())
			if err != nil {
				t.Fatalf("kanonik metin yeniden ayristirilamadi: %v", err)
			}
			if again != msg {
				t.Errorf("roundtrip bozuldu:\n  %#v\n  %#v", again, msg)
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
			t.Errorf("%q tipi vektorlerde kapsanmiyor", want)
		}
	}
}

func TestInvalidMessageVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Messages.Invalid {
		t.Run(vec.Name, func(t *testing.T) {
			msg, err := Parse(vec.Text)
			if err == nil {
				t.Fatalf("hata beklenirdi (%s), donen: %#v", vec.Error, msg)
			}
			if got := codeOf(t, err); string(got) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q (detay: %v)", got, vec.Error, err)
			}
		})
	}
}

// Sinir vakalari: tam sinirda kabul, bir fazlasinda red.
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
				t.Fatalf("bilinmeyen sinir vakasi: %q", vec.Kind)
			}

			err := msg.Validate()
			if vec.Accept && err != nil {
				t.Errorf("%d bayt kabul edilmeliydi: %v", vec.Len, err)
			}
			if !vec.Accept && err == nil {
				t.Errorf("%d bayt reddedilmeliydi", vec.Len)
			}

			// Ayristirma tarafi da ayni karari vermeli.
			parsed, perr := Parse(msg.Canonical())
			if vec.Accept && perr != nil {
				t.Errorf("Parse %d bayti kabul etmeliydi: %v", vec.Len, perr)
			}
			if !vec.Accept && perr == nil {
				t.Errorf("Parse %d bayti reddetmeliydi (%#v)", vec.Len, parsed)
			}
		})
	}
}

// --- sir katmani ---

func TestSealerKeyDerivationMatchesVector(t *testing.T) {
	v := loadVectors(t)
	s, err := NewSealer(v.Sealed.Key.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.KeyHex(); got != v.Sealed.Key.KeyHex {
		t.Errorf("turetilen anahtar farkli:\n  geldi:    %s\n  beklenen: %s", got, v.Sealed.Key.KeyHex)
	}
}

// Vektordeki payload'lari COZEBILMELIYIZ (python ile uretildiler).
func TestSealedValidVectorsOpen(t *testing.T) {
	for _, vec := range loadVectors(t).Sealed.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			s, err := NewSealer(vec.Secret)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Open(vec.Payload)
			if err != nil {
				t.Fatalf("Open hata: %v", err)
			}
			if got != vec.Plaintext {
				t.Errorf("duz metin farkli:\n  geldi:    %s\n  beklenen: %s", got, vec.Plaintext)
			}
			// Mesaj olarak da ayristirilabilmeli.
			if _, err := s.Decode(vec.Payload); err != nil {
				t.Errorf("Decode hata: %v", err)
			}
		})
	}
}

// Ayni nonce ile sifreledigimizde vektordekiyle BAYT BAYT ayni payload
// uretmeliyiz: bu, iki uygulamanin ayni cercevelemeyi kullandigini kanitlar.
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
				t.Fatalf("sealWithNonce hata: %v", err)
			}
			if got != vec.Payload {
				t.Errorf("payload farkli:\n  geldi:    %s\n  beklenen: %s", got, vec.Payload)
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
				t.Fatalf("hata beklenirdi (%s), donen: %q", vec.Error, got)
			} else if code := codeOf(t, err); string(code) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q", code, vec.Error)
			}
		})
	}
}

// Sir beklenirken duz JSON gelirse reddedilmeli: "sirsiz teklifleri reddet".
func TestPlainRejectedWhenSecretExpected(t *testing.T) {
	v := loadVectors(t).Sealed.PlainWhenSecretExpected
	s, err := NewSealer(v.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Open(v.Payload); err == nil {
		t.Fatalf("duz payload reddedilmeliydi, donen: %q", got)
	} else if code := codeOf(t, err); string(code) != v.Error {
		t.Errorf("hata kodu %q, beklenen %q", code, v.Error)
	}
}

// Sir yokken sirli payload gelirse reddedilmeli (cozemeyiz).
func TestSealedRejectedWhenNoSecret(t *testing.T) {
	v := loadVectors(t).Sealed.SealedWhenNoSecret
	var s *Sealer // sir yok
	if got, err := s.Open(v.Payload); err == nil {
		t.Fatalf("sirli payload reddedilmeliydi, donen: %q", got)
	} else if code := codeOf(t, err); string(code) != v.Error {
		t.Errorf("hata kodu %q, beklenen %q", code, v.Error)
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
				return nil, errors.New("gecersiz hex")
			}
			b = b<<4 | v
		}
		out[i] = b
	}
	return out, nil
}
