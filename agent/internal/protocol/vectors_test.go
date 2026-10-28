package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// vectorsPath — the TS side (web/src/protocol/*.test.ts) reads the SAME
// file.
const vectorsPath = "../../../protocol/testvectors.json"

type pattern struct {
	Kind string `json:"kind"`
	Byte *int   `json:"byte"`
	Len  int    `json:"len"`
}

type validFrameVector struct {
	Name       string `json:"name"`
	ID         uint32 `json:"id"`
	Seq        uint32 `json:"seq"`
	PayloadHex string `json:"payloadHex"`
	FrameHex   string `json:"frameHex"`
}

type generatedFrameVector struct {
	Name    string  `json:"name"`
	ID      uint32  `json:"id"`
	Seq     uint32  `json:"seq"`
	Pattern pattern `json:"pattern"`
}

type encodeInvalidVector struct {
	Name    string  `json:"name"`
	ID      uint32  `json:"id"`
	Seq     uint32  `json:"seq"`
	Pattern pattern `json:"pattern"`
	Error   string  `json:"error"`
}

type decodeInvalidVector struct {
	Name  string `json:"name"`
	Frame struct {
		Hex     *string  `json:"hex"`
		ID      uint32   `json:"id"`
		Seq     uint32   `json:"seq"`
		Pattern *pattern `json:"pattern"`
	} `json:"frame"`
	Error string `json:"error"`
}

type ctrlValidVector struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Canonical string `json:"canonical"`
}

type ctrlInvalidVector struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

type wsValidVector struct {
	Name       string `json:"name"`
	Binary     bool   `json:"binary"`
	Fin        bool   `json:"fin"`
	PayloadHex string `json:"payloadHex"`
	OutHex     string `json:"outHex"`
}

type wsDecodeInvalidVector struct {
	Name  string `json:"name"`
	Hex   string `json:"hex"`
	Error string `json:"error"`
}

type testVectors struct {
	Version int    `json:"version"`
	Note    string `json:"note"`
	Framing struct {
		HeaderBytes int `json:"headerBytes"`
		MaxPayload  int `json:"maxPayload"`
		MaxFrame    int `json:"maxFrame"`
	} `json:"framing"`
	Errors struct {
		Framing []string `json:"framing"`
		Ctrl    []string `json:"ctrl"`
		WS      []string `json:"ws"`
	} `json:"errors"`
	Frames struct {
		Valid         []validFrameVector     `json:"valid"`
		Generated     []generatedFrameVector `json:"generated"`
		EncodeInvalid []encodeInvalidVector  `json:"encodeInvalid"`
		DecodeInvalid []decodeInvalidVector  `json:"decodeInvalid"`
	} `json:"frames"`
	Ctrl struct {
		Valid   []ctrlValidVector   `json:"valid"`
		Invalid []ctrlInvalidVector `json:"invalid"`
	} `json:"ctrl"`
	WSFrames struct {
		Limits struct {
			HeaderBytes int `json:"headerBytes"`
			MaxPayload  int `json:"maxPayload"`
		} `json:"limits"`
		Valid         []wsValidVector         `json:"valid"`
		DecodeInvalid []wsDecodeInvalidVector `json:"decodeInvalid"`
	} `json:"wsFrames"`
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

// buildPattern turns a vector's generation recipe into bytes. The same
// rules as buildPattern on the TS side.
func buildPattern(t *testing.T, p pattern) []byte {
	t.Helper()
	out := make([]byte, p.Len)
	switch p.Kind {
	case "repeat":
		if p.Byte == nil {
			t.Fatalf("the repeat pattern requires byte")
		}
		for i := range out {
			out[i] = byte(*p.Byte)
		}
	case "counter":
		for i := range out {
			out[i] = byte(i % 256)
		}
	default:
		t.Fatalf("unknown pattern: %q", p.Kind)
	}
	return out
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("the hex could not be decoded (%q): %v", s, err)
	}
	return b
}
