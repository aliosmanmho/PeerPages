package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

func framingCode(t *testing.T, err error) FramingErrorCode {
	t.Helper()
	var fe *FramingError
	if !errors.As(err, &fe) {
		t.Fatalf("FramingError beklenirdi, geldi: %T (%v)", err, err)
	}
	return fe.Code
}

func TestVectorsMatchConstants(t *testing.T) {
	v := loadVectors(t)
	if v.Framing.HeaderBytes != HeaderBytes {
		t.Errorf("headerBytes: vektor %d, Go %d", v.Framing.HeaderBytes, HeaderBytes)
	}
	if v.Framing.MaxPayload != MaxPayload {
		t.Errorf("maxPayload: vektor %d, Go %d", v.Framing.MaxPayload, MaxPayload)
	}
	if v.Framing.MaxFrame != MaxFrame {
		t.Errorf("maxFrame: vektor %d, Go %d", v.Framing.MaxFrame, MaxFrame)
	}
	if v.Version != Version {
		t.Errorf("version: vektor %d, Go %d", v.Version, Version)
	}
}

func TestVectorsNotEmpty(t *testing.T) {
	v := loadVectors(t)
	if len(v.Frames.Valid) < 6 || len(v.Frames.Generated) < 3 ||
		len(v.Frames.EncodeInvalid) < 2 || len(v.Frames.DecodeInvalid) < 4 {
		t.Fatalf("vektor dosyasi beklenenden az kayit iceriyor: %d/%d/%d/%d",
			len(v.Frames.Valid), len(v.Frames.Generated),
			len(v.Frames.EncodeInvalid), len(v.Frames.DecodeInvalid))
	}
}

func TestEncodeFrameVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Frames.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			got, err := EncodeFrame(vec.ID, vec.Seq, mustHex(t, vec.PayloadHex))
			if err != nil {
				t.Fatalf("EncodeFrame hata: %v", err)
			}
			if h := hex.EncodeToString(got); h != vec.FrameHex {
				t.Errorf("cerceve farkli:\n  geldi:    %s\n  beklenen: %s", h, vec.FrameHex)
			}
		})
	}
}

func TestDecodeFrameVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Frames.Valid {
		t.Run(vec.Name, func(t *testing.T) {
			got, err := DecodeFrame(mustHex(t, vec.FrameHex))
			if err != nil {
				t.Fatalf("DecodeFrame hata: %v", err)
			}
			if got.ID != vec.ID || got.Seq != vec.Seq {
				t.Errorf("id/seq farkli: (%d,%d), beklenen (%d,%d)", got.ID, got.Seq, vec.ID, vec.Seq)
			}
			if h := hex.EncodeToString(got.Payload); h != vec.PayloadHex {
				t.Errorf("payload farkli:\n  geldi:    %s\n  beklenen: %s", h, vec.PayloadHex)
			}
		})
	}
}

func TestGeneratedFrameVectorsRoundtrip(t *testing.T) {
	for _, vec := range loadVectors(t).Frames.Generated {
		t.Run(vec.Name, func(t *testing.T) {
			payload := buildPattern(t, vec.Pattern)
			if len(payload) != vec.Pattern.Len {
				t.Fatalf("desen uzunlugu: %d, beklenen %d", len(payload), vec.Pattern.Len)
			}

			frame, err := EncodeFrame(vec.ID, vec.Seq, payload)
			if err != nil {
				t.Fatalf("EncodeFrame hata: %v", err)
			}
			if len(frame) != HeaderBytes+vec.Pattern.Len {
				t.Fatalf("cerceve uzunlugu: %d, beklenen %d", len(frame), HeaderBytes+vec.Pattern.Len)
			}

			back, err := DecodeFrame(frame)
			if err != nil {
				t.Fatalf("DecodeFrame hata: %v", err)
			}
			if back.ID != vec.ID || back.Seq != vec.Seq {
				t.Errorf("id/seq farkli: (%d,%d)", back.ID, back.Seq)
			}
			if !bytes.Equal(back.Payload, payload) {
				t.Errorf("payload roundtrip bozuldu")
			}
		})
	}
}

func TestEncodeInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Frames.EncodeInvalid {
		t.Run(vec.Name, func(t *testing.T) {
			_, err := EncodeFrame(vec.ID, vec.Seq, buildPattern(t, vec.Pattern))
			if err == nil {
				t.Fatalf("hata beklenirdi (%s)", vec.Error)
			}
			if code := framingCode(t, err); string(code) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q", code, vec.Error)
			}
		})
	}
}

func TestDecodeInvalidVectors(t *testing.T) {
	for _, vec := range loadVectors(t).Frames.DecodeInvalid {
		t.Run(vec.Name, func(t *testing.T) {
			var frame []byte
			switch {
			case vec.Frame.Hex != nil:
				frame = mustHex(t, *vec.Frame.Hex)
			case vec.Frame.Pattern != nil:
				payload := buildPattern(t, *vec.Frame.Pattern)
				frame = make([]byte, HeaderBytes+len(payload))
				binary.BigEndian.PutUint32(frame[0:4], vec.Frame.ID)
				binary.BigEndian.PutUint32(frame[4:8], vec.Frame.Seq)
				copy(frame[HeaderBytes:], payload)
			default:
				t.Fatalf("vektorde ne hex ne pattern var")
			}

			_, err := DecodeFrame(frame)
			if err == nil {
				t.Fatalf("hata beklenirdi (%s)", vec.Error)
			}
			if code := framingCode(t, err); string(code) != vec.Error {
				t.Errorf("hata kodu %q, beklenen %q", code, vec.Error)
			}
		})
	}
}

func TestDecodeFrameCopiesPayload(t *testing.T) {
	frame, err := EncodeFrame(1, 0, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	frame[HeaderBytes] = 99 // kaynak tamponu bozalim
	if !bytes.Equal(got.Payload, []byte{1, 2, 3}) {
		t.Errorf("DecodeFrame payload'i kopyalamadi: %v", got.Payload)
	}
}

func TestEncodeFrameBoundaries(t *testing.T) {
	if _, err := EncodeFrame(0, 0, make([]byte, MaxPayload)); err != nil {
		t.Errorf("tam sinir (%d bayt) kabul edilmeliydi: %v", MaxPayload, err)
	}
	_, err := EncodeFrame(0, 0, make([]byte, MaxPayload+1))
	if err == nil {
		t.Fatalf("sinirin bir bayt ustu reddedilmeliydi")
	}
	if code := framingCode(t, err); code != CodePayloadTooLarge {
		t.Errorf("hata kodu %q", code)
	}
}

func TestAppendFrameMatchesEncodeFrame(t *testing.T) {
	payload := []byte("hello")
	want, err := EncodeFrame(17, 3, payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AppendFrame(nil, 17, 3, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("AppendFrame EncodeFrame'den farkli:\n  %x\n  %x", got, want)
	}

	// Var olan tamponun uzerine ekleme bozulmamali.
	prefix := []byte{0xAA, 0xBB}
	got, err = AppendFrame(prefix, 17, 3, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:2], prefix) || !bytes.Equal(got[2:], want) {
		t.Errorf("AppendFrame onek tamponu bozdu: %x", got)
	}

	if _, err := AppendFrame(nil, 1, 0, make([]byte, MaxPayload+1)); err == nil {
		t.Errorf("AppendFrame sinir asimini reddetmeliydi")
	}
}

func TestFrameBody(t *testing.T) {
	t.Run("bos govde", func(t *testing.T) {
		got, err := FrameBody(1, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("bos govde icin cerceve uretilmemeli, %d uretildi", len(got))
		}
	})

	t.Run("tam sinir tek cerceve", func(t *testing.T) {
		got, err := FrameBody(1, make([]byte, MaxPayload), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0]) != MaxFrame {
			t.Errorf("tek %d baytlik cerceve beklenirdi, %d cerceve", MaxFrame, len(got))
		}
	})

	t.Run("sinir+1 iki cerceve", func(t *testing.T) {
		got, err := FrameBody(1, make([]byte, MaxPayload+1), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("2 cerceve beklenirdi, %d", len(got))
		}
		f0, _ := DecodeFrame(got[0])
		f1, _ := DecodeFrame(got[1])
		if len(f0.Payload) != MaxPayload || len(f1.Payload) != 1 {
			t.Errorf("parcalanma yanlis: %d + %d", len(f0.Payload), len(f1.Payload))
		}
	})

	t.Run("seq artar ve icerik korunur", func(t *testing.T) {
		body := buildPattern(t, pattern{Kind: "counter", Len: MaxPayload*2 + 7})
		frames, err := FrameBody(42, body, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 3 {
			t.Fatalf("3 cerceve beklenirdi, %d", len(frames))
		}
		var rebuilt []byte
		for i, f := range frames {
			d, err := DecodeFrame(f)
			if err != nil {
				t.Fatal(err)
			}
			if d.ID != 42 {
				t.Errorf("id %d", d.ID)
			}
			if want := uint32(3 + i); d.Seq != want {
				t.Errorf("seq %d, beklenen %d", d.Seq, want)
			}
			rebuilt = append(rebuilt, d.Payload...)
		}
		if !bytes.Equal(rebuilt, body) {
			t.Errorf("govde yeniden birlestirilemedi")
		}
	})
}
