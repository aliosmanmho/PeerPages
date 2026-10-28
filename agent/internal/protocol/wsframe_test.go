package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func wsCode(t *testing.T, err error) WSFrameErrorCode {
	t.Helper()
	var we *WSFrameError
	if !errors.As(err, &we) {
		t.Fatalf("a WSFrameError was expected, got: %T (%v)", err, err)
	}
	return we.Code
}

func TestWSVectorsMatchConstants(t *testing.T) {
	v := loadVectors(t)
	if v.WSFrames.Limits.HeaderBytes != WSHeaderBytes {
		t.Errorf("wsFrames.headerBytes: vector %d, Go %d",
			v.WSFrames.Limits.HeaderBytes, WSHeaderBytes)
	}
	if v.WSFrames.Limits.MaxPayload != MaxWSPayload {
		t.Errorf("wsFrames.maxPayload: vector %d, Go %d",
			v.WSFrames.Limits.MaxPayload, MaxWSPayload)
	}
	if len(v.WSFrames.Valid) < 6 || len(v.WSFrames.DecodeInvalid) < 4 {
		t.Fatalf("fewer ws vectors than expected: %d/%d",
			len(v.WSFrames.Valid), len(v.WSFrames.DecodeInvalid))
	}
}

func TestEncodeWSPayloadVectors(t *testing.T) {
	for _, vec := range loadVectors(t).WSFrames.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			got, err := EncodeWSPayload(WSChunk{
				Binary:  vec.Binary,
				Fin:     vec.Fin,
				Payload: mustHex(t, vec.PayloadHex),
			})
			if err != nil {
				t.Fatalf("EncodeWSPayload error: %v", err)
			}
			if want := mustHex(t, vec.OutHex); !bytes.Equal(got, want) {
				t.Errorf("the output differs:\n  got:      %x\n  expected: %x", got, want)
			}
		})
	}
}

func TestDecodeWSPayloadVectors(t *testing.T) {
	for _, vec := range loadVectors(t).WSFrames.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			got, err := DecodeWSPayload(mustHex(t, vec.OutHex))
			if err != nil {
				t.Fatalf("DecodeWSPayload error: %v", err)
			}
			if got.Binary != vec.Binary || got.Fin != vec.Fin {
				t.Errorf("the flags differ: binary=%v fin=%v, expected binary=%v fin=%v",
					got.Binary, got.Fin, vec.Binary, vec.Fin)
			}
			if want := mustHex(t, vec.PayloadHex); !bytes.Equal(got.Payload, want) {
				t.Errorf("the payload differs:\n  got:      %x\n  expected: %x", got.Payload, want)
			}
		})
	}
}

func TestDecodeWSPayloadInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).WSFrames.DecodeInvalid {
		t.Run(vec.Name, func(t *testing.T) {
			chunk, err := DecodeWSPayload(mustHex(t, vec.Hex))
			if err == nil {
				t.Fatalf("an error was expected (%s), returned: %#v", vec.Error, chunk)
			}
			if code := wsCode(t, err); string(code) != vec.Error {
				t.Errorf("error code %q, expected %q (detail: %v)", code, vec.Error, err)
			}
		})
	}
}

// A ws chunk has to fit the outer frame: 1 flag byte + the chunk <=
// MaxPayload.
func TestWSPayloadFitsOuterFrame(t *testing.T) {
	big := make([]byte, MaxWSPayload)
	out, err := EncodeWSPayload(WSChunk{Fin: true, Payload: big})
	if err != nil {
		t.Fatalf("the chunk at the limit could not be encoded: %v", err)
	}
	if len(out) != MaxPayload {
		t.Errorf("the ws payload at the limit is %d bytes, expected %d", len(out), MaxPayload)
	}
	if _, err := EncodeFrame(1, 0, out); err != nil {
		t.Errorf("the ws payload at the limit did not fit the outer frame: %v", err)
	}

	if _, err := EncodeWSPayload(WSChunk{Fin: true, Payload: make([]byte, MaxWSPayload+1)}); err == nil {
		t.Error("an oversize payload was accepted")
	} else if code := wsCode(t, err); code != CodeWSPayloadTooLarge {
		t.Errorf("error code %q, expected %q", code, CodeWSPayloadTooLarge)
	}
}

func TestAppendWSPayloadMatchesEncode(t *testing.T) {
	for _, c := range []WSChunk{
		{Binary: false, Fin: false, Payload: []byte("ab")},
		{Binary: true, Fin: true, Payload: []byte{0, 255}},
		{Binary: false, Fin: true, Payload: nil},
	} {
		want, err := EncodeWSPayload(c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := AppendWSPayload(nil, c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("AppendWSPayload %x, EncodeWSPayload %x", got, want)
		}
	}
}

// DecodeWSPayload makes no copy: the returned slice is a view of the
// given buffer. That is deliberate (DecodeFrame copies already) and part
// of the contract.
func TestDecodeWSPayloadSharesBuffer(t *testing.T) {
	buf := []byte{WSFlagFin, 'a', 'b'}
	chunk, err := DecodeWSPayload(buf)
	if err != nil {
		t.Fatal(err)
	}
	buf[1] = 'z'
	if chunk.Payload[0] != 'z' {
		t.Error("the payload was copied: the contract may have changed, see the comment in wsframe.go")
	}
}
