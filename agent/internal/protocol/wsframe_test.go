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
		t.Fatalf("WSFrameError beklenirdi, geldi: %T (%v)", err, err)
	}
	return we.Code
}

func TestWSVectorsMatchConstants(t *testing.T) {
	v := loadVectors(t)
	if v.WSFrames.Limits.HeaderBytes != WSHeaderBytes {
		t.Errorf("wsFrames.headerBytes: vektor %d, Go %d",
			v.WSFrames.Limits.HeaderBytes, WSHeaderBytes)
	}
	if v.WSFrames.Limits.MaxPayload != MaxWSPayload {
		t.Errorf("wsFrames.maxPayload: vektor %d, Go %d",
			v.WSFrames.Limits.MaxPayload, MaxWSPayload)
	}
	if len(v.WSFrames.Valid) < 6 || len(v.WSFrames.DecodeInvalid) < 4 {
		t.Fatalf("ws vektorleri beklenenden az: %d/%d",
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
				t.Fatalf("EncodeWSPayload hata: %v", err)
			}
			if want := mustHex(t, vec.OutHex); !bytes.Equal(got, want) {
				t.Errorf("cikti farkli:\n  geldi:    %x\n  beklenen: %x", got, want)
			}
		})
	}
}

func TestDecodeWSPayloadVectors(t *testing.T) {
	for _, vec := range loadVectors(t).WSFrames.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			got, err := DecodeWSPayload(mustHex(t, vec.OutHex))
			if err != nil {
				t.Fatalf("DecodeWSPayload hata: %v", err)
			}
			if got.Binary != vec.Binary || got.Fin != vec.Fin {
				t.Errorf("bayraklar farkli: binary=%v fin=%v, beklenen binary=%v fin=%v",
					got.Binary, got.Fin, vec.Binary, vec.Fin)
			}
			if want := mustHex(t, vec.PayloadHex); !bytes.Equal(got.Payload, want) {
				t.Errorf("payload farkli:\n  geldi:    %x\n  beklenen: %x", got.Payload, want)
			}
		})
	}
}

func TestDecodeWSPayloadInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).WSFrames.DecodeInvalid {
		t.Run(vec.Name, func(t *testing.T) {
			chunk, err := DecodeWSPayload(mustHex(t, vec.Hex))
			if err == nil {
				t.Fatalf("hata beklenirdi (%s), donen: %#v", vec.Error, chunk)
			}
			if code := wsCode(t, err); string(code) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q (detay: %v)", code, vec.Error, err)
			}
		})
	}
}

// Ws parcasi dis cerceveye sigmak zorunda: 1 bayt bayrak + parca <= MaxPayload.
func TestWSPayloadFitsOuterFrame(t *testing.T) {
	big := make([]byte, MaxWSPayload)
	out, err := EncodeWSPayload(WSChunk{Fin: true, Payload: big})
	if err != nil {
		t.Fatalf("sinirdaki parca kodlanamadi: %v", err)
	}
	if len(out) != MaxPayload {
		t.Errorf("sinirdaki ws payload %d bayt, beklenen %d", len(out), MaxPayload)
	}
	if _, err := EncodeFrame(1, 0, out); err != nil {
		t.Errorf("sinirdaki ws payload dis cerceveye sigmadi: %v", err)
	}

	if _, err := EncodeWSPayload(WSChunk{Fin: true, Payload: make([]byte, MaxWSPayload+1)}); err == nil {
		t.Error("sinir asimi kabul edildi")
	} else if code := wsCode(t, err); code != CodeWSPayloadTooLarge {
		t.Errorf("hata kodu %q, beklenen %q", code, CodeWSPayloadTooLarge)
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

// DecodeWSPayload kopya uretmez: donen dilim verilen tamponun gorunumudur.
// Bu bilincli (DecodeFrame zaten kopyaliyor) ve sozlesmenin parcasi.
func TestDecodeWSPayloadSharesBuffer(t *testing.T) {
	buf := []byte{WSFlagFin, 'a', 'b'}
	chunk, err := DecodeWSPayload(buf)
	if err != nil {
		t.Fatal(err)
	}
	buf[1] = 'z'
	if chunk.Payload[0] != 'z' {
		t.Error("payload kopyalanmis: sozlesme degismis olabilir, wsframe.go yorumuna bak")
	}
}
