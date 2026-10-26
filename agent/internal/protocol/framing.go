// Package protocol data channel uzerindeki mesaj bicimini uygular.
//
// Normatif tanim: protocol/README.md. TypeScript karsiligi
// web/src/protocol/. Iki taraf AYNI test vektorlerini okur:
// protocol/testvectors.json.
package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// HeaderBytes cerceve basligi: id (4) + seq (4).
	HeaderBytes = 8
	// MaxPayload en buyuk payload: 16 KiB (tarayici data channel uyumlulugu).
	MaxPayload = 16384
	// MaxFrame en buyuk cerceve.
	MaxFrame = HeaderBytes + MaxPayload
)

// FramingErrorCode iki dilde ayni olan framing hata kodlari.
type FramingErrorCode string

const (
	// CodeShortFrame cerceve basligi tamamlanmiyor.
	CodeShortFrame FramingErrorCode = "short-frame"
	// CodePayloadTooLarge payload 16 KiB sinirini asiyor.
	CodePayloadTooLarge FramingErrorCode = "payload-too-large"
)

// FramingError framing hatasi. Code degeri testvectors.json ile birebir eslesir.
type FramingError struct {
	Code   FramingErrorCode
	Detail string
}

func (e *FramingError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// Frame cozulmus bir data cercevesi.
type Frame struct {
	ID      uint32
	Seq     uint32
	Payload []byte
}

// EncodeFrame cerceveyi kodlar.
//
// payload MaxPayload'dan buyukse payload-too-large doner. id/seq uint32
// oldugundan aralik kontrolune gerek yok (TS tarafinda var, cunku orada
// number).
func EncodeFrame(id, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bayt, sinir %d", len(payload), MaxPayload),
		}
	}
	out := make([]byte, HeaderBytes+len(payload))
	binary.BigEndian.PutUint32(out[0:4], id)
	binary.BigEndian.PutUint32(out[4:8], seq)
	copy(out[HeaderBytes:], payload)
	return out, nil
}

// AppendFrame cerceveyi dst'ye ekler; sicak yolda tahsisi azaltmak icin.
func AppendFrame(dst []byte, id, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return dst, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bayt, sinir %d", len(payload), MaxPayload),
		}
	}
	var head [HeaderBytes]byte
	binary.BigEndian.PutUint32(head[0:4], id)
	binary.BigEndian.PutUint32(head[4:8], seq)
	dst = append(dst, head[:]...)
	return append(dst, payload...), nil
}

// DecodeFrame cerceveyi cozer.
//
// Donen Payload, frame'in KOPYASIDIR: cagiran taraf gelen tamponu yeniden
// kullanabilir (pion data channel tamponu paylasabilir).
func DecodeFrame(frame []byte) (Frame, error) {
	if len(frame) < HeaderBytes {
		return Frame{}, &FramingError{
			Code:   CodeShortFrame,
			Detail: fmt.Sprintf("%d bayt, en az %d gerekli", len(frame), HeaderBytes),
		}
	}
	payloadLen := len(frame) - HeaderBytes
	if payloadLen > MaxPayload {
		return Frame{}, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bayt, sinir %d", payloadLen, MaxPayload),
		}
	}

	payload := make([]byte, payloadLen)
	copy(payload, frame[HeaderBytes:])
	return Frame{
		ID:      binary.BigEndian.Uint32(frame[0:4]),
		Seq:     binary.BigEndian.Uint32(frame[4:8]),
		Payload: payload,
	}, nil
}

// FrameBody govdeyi MaxPayload'lik parcalara boler ve cerceveleri dondurur.
// seq startSeq'ten baslayarak artar. Bos govde icin cerceve uretilmez.
//
// Akis halinde gonderim icin proxy katmani dogrudan AppendFrame kullanir; bu
// yardimci kucuk govdeler ve testler icin.
func FrameBody(id uint32, body []byte, startSeq uint32) ([][]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	n := (len(body) + MaxPayload - 1) / MaxPayload
	out := make([][]byte, 0, n)
	seq := startSeq
	for off := 0; off < len(body); off += MaxPayload {
		end := off + MaxPayload
		if end > len(body) {
			end = len(body)
		}
		f, err := EncodeFrame(id, seq, body[off:end])
		if err != nil {
			return nil, err
		}
		out = append(out, f)
		seq++
	}
	return out, nil
}
