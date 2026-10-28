package protocol

import "fmt"

// The frame sub-format of the WebSocket bridge (phase 6).
//
// The outer frame ([4B id][4B seq][payload]) is kept EXACTLY as is: the ws
// payload uses neither a separate channel nor a separate framing. The
// PAYLOAD of a frame belonging to a ws id looks like this:
//
//	[1B flags][chunk]
//
// That way backpressure (internal/tunnel/flow.go), the seq check and
// 16 KiB chunking come for free; binary data takes no base64 inflation,
// and bulk ws traffic does not clog the ctrl channel (ping, abort).
//
// Normative definition: protocol/README.md.
const (
	// WSFlagBinary set to 1 means a binary message, 0 a text one.
	WSFlagBinary byte = 0x01
	// WSFlagFin set to 1 means this is the LAST chunk of the ws message.
	WSFlagFin byte = 0x02
	// WSFlagKnown is the set of defined bits; any bit outside it is a
	// protocol error.
	WSFlagKnown byte = WSFlagBinary | WSFlagFin

	// WSHeaderBytes is the ws sub-header: just the flag byte.
	WSHeaderBytes = 1
	// MaxWSPayload is the largest size of a single ws chunk.
	MaxWSPayload = MaxPayload - WSHeaderBytes
)

// WSFrameErrorCode is a ws frame error code, the same in both languages.
type WSFrameErrorCode string

const (
	// CodeWSEmptyFrame means the ws frame payload is empty (no flag byte).
	CodeWSEmptyFrame WSFrameErrorCode = "ws-empty-frame"
	// CodeWSBadFlags means the flag byte carries an undefined bit.
	CodeWSBadFlags WSFrameErrorCode = "ws-bad-flags"
	// CodeWSPayloadTooLarge means the chunk exceeds the MaxWSPayload limit.
	CodeWSPayloadTooLarge WSFrameErrorCode = "ws-payload-too-large"
)

// WSFrameError is a ws frame error. Code matches testvectors.json.
type WSFrameError struct {
	Code   WSFrameErrorCode
	Detail string
}

func (e *WSFrameError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

// WSChunk is a decoded ws chunk.
type WSChunk struct {
	// Binary says whether the message is binary. It must be the same on
	// ALL chunks of a message.
	Binary bool
	// Fin says whether this chunk ends the message.
	Fin bool
	// Payload is the chunk itself (may be empty: an empty ws message is
	// just flags).
	Payload []byte
}

// EncodeWSPayload turns a ws chunk into the outer frame's payload.
func EncodeWSPayload(c WSChunk) ([]byte, error) {
	if len(c.Payload) > MaxWSPayload {
		return nil, &WSFrameError{
			Code:   CodeWSPayloadTooLarge,
			Detail: fmt.Sprintf("%d bytes, limit %d", len(c.Payload), MaxWSPayload),
		}
	}
	out := make([]byte, WSHeaderBytes+len(c.Payload))
	out[0] = wsFlags(c)
	copy(out[WSHeaderBytes:], c.Payload)
	return out, nil
}

// AppendWSPayload appends the ws chunk to dst, to cut allocations on the
// hot path (same reasoning as AppendFrame).
func AppendWSPayload(dst []byte, c WSChunk) ([]byte, error) {
	if len(c.Payload) > MaxWSPayload {
		return dst, &WSFrameError{
			Code:   CodeWSPayloadTooLarge,
			Detail: fmt.Sprintf("%d bytes, limit %d", len(c.Payload), MaxWSPayload),
		}
	}
	dst = append(dst, wsFlags(c))
	return append(dst, c.Payload...), nil
}

func wsFlags(c WSChunk) byte {
	var f byte
	if c.Binary {
		f |= WSFlagBinary
	}
	if c.Fin {
		f |= WSFlagFin
	}
	return f
}

// DecodeWSPayload decodes the outer frame's payload into a ws chunk.
//
// The returned Payload is NOT a copy of the given slice: DecodeFrame
// already made a copy, a second one would be pointless. A caller that
// keeps the slice copies it itself.
func DecodeWSPayload(payload []byte) (WSChunk, error) {
	if len(payload) < WSHeaderBytes {
		return WSChunk{}, &WSFrameError{
			Code:   CodeWSEmptyFrame,
			Detail: "the payload of a ws frame must be at least 1 byte (the flags)",
		}
	}
	flags := payload[0]
	if flags&^WSFlagKnown != 0 {
		return WSChunk{}, &WSFrameError{
			Code:   CodeWSBadFlags,
			Detail: fmt.Sprintf("undefined bit: 0x%02x", flags),
		}
	}
	return WSChunk{
		Binary:  flags&WSFlagBinary != 0,
		Fin:     flags&WSFlagFin != 0,
		Payload: payload[WSHeaderBytes:],
	}, nil
}
