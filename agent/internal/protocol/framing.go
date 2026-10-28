// Package protocol implements the message format carried over the data
// channel.
//
// Normative definition: protocol/README.md. TypeScript counterpart:
// web/src/protocol/. Both sides read the SAME test vectors:
// protocol/testvectors.json.
package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	// HeaderBytes is the frame header: id (4) + seq (4).
	HeaderBytes = 8
	// MaxPayload is the largest payload: 16 KiB (browser data channel
	// compatibility).
	MaxPayload = 16384
	// MaxFrame is the largest frame.
	MaxFrame = HeaderBytes + MaxPayload
)

// FramingErrorCode is a framing error code, the same in both languages.
type FramingErrorCode string

const (
	// CodeShortFrame means the frame header is incomplete.
	CodeShortFrame FramingErrorCode = "short-frame"
	// CodePayloadTooLarge means the payload exceeds the 16 KiB limit.
	CodePayloadTooLarge FramingErrorCode = "payload-too-large"
)

// FramingError is a framing error. Code matches testvectors.json exactly.
type FramingError struct {
	Code   FramingErrorCode
	Detail string
}

func (e *FramingError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// Frame is a decoded data frame.
type Frame struct {
	ID      uint32
	Seq     uint32
	Payload []byte
}

// EncodeFrame encodes a frame.
//
// If payload is larger than MaxPayload it returns payload-too-large. id
// and seq are uint32, so no range check is needed (the TS side has one,
// because there they are numbers).
func EncodeFrame(id, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bytes, limit %d", len(payload), MaxPayload),
		}
	}
	out := make([]byte, HeaderBytes+len(payload))
	binary.BigEndian.PutUint32(out[0:4], id)
	binary.BigEndian.PutUint32(out[4:8], seq)
	copy(out[HeaderBytes:], payload)
	return out, nil
}

// AppendFrame appends the frame to dst, to cut allocations on the hot
// path.
func AppendFrame(dst []byte, id, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return dst, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bytes, limit %d", len(payload), MaxPayload),
		}
	}
	var head [HeaderBytes]byte
	binary.BigEndian.PutUint32(head[0:4], id)
	binary.BigEndian.PutUint32(head[4:8], seq)
	dst = append(dst, head[:]...)
	return append(dst, payload...), nil
}

// DecodeFrame decodes a frame.
//
// The returned Payload is a COPY of frame: the caller may reuse the
// incoming buffer (the pion data channel can share its buffer).
func DecodeFrame(frame []byte) (Frame, error) {
	if len(frame) < HeaderBytes {
		return Frame{}, &FramingError{
			Code:   CodeShortFrame,
			Detail: fmt.Sprintf("%d bytes, at least %d required", len(frame), HeaderBytes),
		}
	}
	payloadLen := len(frame) - HeaderBytes
	if payloadLen > MaxPayload {
		return Frame{}, &FramingError{
			Code:   CodePayloadTooLarge,
			Detail: fmt.Sprintf("%d bytes, limit %d", payloadLen, MaxPayload),
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

// FrameBody splits the body into MaxPayload chunks and returns the
// frames. seq starts at startSeq and increments. An empty body yields no
// frame.
//
// For streaming sends the proxy layer uses AppendFrame directly; this
// helper is for small bodies and tests.
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
